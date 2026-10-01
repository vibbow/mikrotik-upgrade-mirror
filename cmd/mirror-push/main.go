// mirror-push downloads RouterOS packages locally (fast network) and pushes them
// to the mirror server over SFTP, so a server with slow access to MikroTik's CDN
// doesn't have to download them itself.
//
// For each channel it:
//  1. reads NEWESTa7.<channel> to find the latest version,
//  2. compares against the remote's packages/<channel>/.version,
//  3. if behind, downloads+extracts all arches into a local temp dir,
//  4. uploads them to packages/<channel>.new/ on the server,
//  5. atomically swaps .new -> live and removes the old dir.
//
// Usage:
//
//	mirror-push --host mirror.example.com --user root \
//	  --remote-dir /opt/mikrotik-mirror/packages \
//	  [--key ~/.ssh/id_ed25519] [--arches arm64,smips] [--channels stable,long-term]
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"

	syncpkg "github.com/local/mikrotik-mirror/internal/sync"
)

func main() {
	host := flag.String("host", "", "server host (required)")
	port := flag.Int("port", 22, "ssh port")
	user := flag.String("user", "root", "ssh user")
	key := flag.String("key", "", "ssh private key file (default: try agent then ~/.ssh/id_*)")
	remoteDir := flag.String("remote-dir", "/opt/mikrotik-mirror/packages", "remote packages root")
	channels := flag.String("channels", "stable,long-term", "channels to push")
	arches := flag.String("arches", strings.Join(syncpkg.DefaultArches, ","), "architectures to mirror")
	proxy := flag.String("proxy", "", "download proxy, e.g. http://127.0.0.1:7890 or host:port (default: env HTTP(S)_PROXY)")
	force := flag.Bool("force", false, "push even if remote already at latest version")
	flag.Parse()
	if *host == "" {
		log.Fatal("--host is required")
	}
	// Git Bash on Windows silently rewrites arguments like /opt/x into
	// "C:/Program Files/Git/opt/x"; uploading there would land under the remote
	// user's home directory instead. Refuse anything that is not a plain POSIX path.
	if !strings.HasPrefix(*remoteDir, "/") || strings.Contains(*remoteDir, ":") {
		log.Fatalf("--remote-dir %q is not an absolute POSIX path. If you ran this from Git Bash, "+
			"set MSYS_NO_PATHCONV=1 (or double the leading slash: //opt/...) so the shell does not rewrite it", *remoteDir)
	}

	client, err := dialSSH(*host, *port, *user, *key)
	if err != nil {
		log.Fatalf("ssh: %v", err)
	}
	defer client.Close()
	sc, err := sftp.NewClient(client)
	if err != nil {
		log.Fatalf("sftp: %v", err)
	}
	defer sc.Close()

	syncer := syncpkg.NewWithProxy(nil, splitCSV(*arches), 0, *proxy)

	for _, chName := range splitCSV(*channels) {
		if err := pushChannel(syncer, sc, chName, *remoteDir, *force); err != nil {
			log.Printf("push %s: %v", chName, err)
		}
	}
}

func pushChannel(syncer *syncpkg.Syncer, sc *sftp.Client, chName, remoteRoot string, force bool) error {
	latest, err := syncer.LatestVersion(chName)
	if err != nil {
		return err
	}
	liveDir := remoteRoot + "/" + chName
	remoteVer := readRemoteVersion(sc, liveDir+"/.version")
	if remoteVer == latest && !force {
		log.Printf("push %s: remote already at %s", chName, latest)
		return nil
	}
	log.Printf("push %s: remote %s -> %s", chName, orNone(remoteVer), latest)

	localTmp, err := os.MkdirTemp("", "mirror-"+chName+"-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(localTmp)

	n, err := syncer.FetchInto(chName, latest, localTmp)
	if err != nil {
		return err
	}
	log.Printf("push %s: downloaded %d packages locally, uploading...", chName, n)

	newDir := remoteRoot + "/" + chName + ".new"
	oldDir := remoteRoot + "/" + chName + ".old"
	sc.RemoveDirectory(newDir) // best effort
	removeRemoteAll(sc, newDir)
	if err := sc.MkdirAll(newDir); err != nil {
		return fmt.Errorf("mkdir %s: %w", newDir, err)
	}

	entries, _ := os.ReadDir(localTmp)
	for _, e := range entries {
		local := filepath.Join(localTmp, e.Name())
		remote := newDir + "/" + e.Name()
		if err := uploadFile(sc, local, remote); err != nil {
			return fmt.Errorf("upload %s: %w", e.Name(), err)
		}
	}
	log.Printf("push %s: uploaded, swapping", chName)

	// atomic-ish swap: live -> old, new -> live, rm old
	removeRemoteAll(sc, oldDir)
	if _, err := sc.Stat(liveDir); err == nil {
		if err := sc.Rename(liveDir, oldDir); err != nil {
			return fmt.Errorf("rename live->old: %w", err)
		}
	}
	if err := sc.Rename(newDir, liveDir); err != nil {
		sc.Rename(oldDir, liveDir) // try restore
		return fmt.Errorf("rename new->live: %w", err)
	}
	removeRemoteAll(sc, oldDir)
	log.Printf("push %s: done, now at %s", chName, latest)
	return nil
}

func uploadFile(sc *sftp.Client, local, remote string) error {
	in, err := os.Open(local)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := sc.Create(remote)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = out.ReadFrom(in)
	return err
}

func readRemoteVersion(sc *sftp.Client, path string) string {
	f, err := sc.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, 64)
	n, _ := f.Read(buf)
	return strings.TrimSpace(string(buf[:n]))
}

func removeRemoteAll(sc *sftp.Client, dir string) {
	w := sc.Walk(dir)
	var files, dirs []string
	for w.Step() {
		if w.Err() != nil {
			continue
		}
		if w.Stat().IsDir() {
			dirs = append(dirs, w.Path())
		} else {
			files = append(files, w.Path())
		}
	}
	for _, f := range files {
		sc.Remove(f)
	}
	// remove dirs deepest-first
	for i := len(dirs) - 1; i >= 0; i-- {
		sc.RemoveDirectory(dirs[i])
	}
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// --- ssh dialing ---

func dialSSH(host string, port int, user, keyFile string) (*ssh.Client, error) {
	var auths []ssh.AuthMethod
	if keyFile != "" {
		if a, err := keyAuth(keyFile); err == nil {
			auths = append(auths, a)
		} else {
			return nil, err
		}
	} else {
		if a := agentAuth(); a != nil {
			auths = append(auths, a)
		}
		for _, name := range []string{"id_ed25519", "id_rsa"} {
			p := filepath.Join(homeDir(), ".ssh", name)
			if a, err := keyAuth(p); err == nil {
				auths = append(auths, a)
			}
		}
	}
	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            auths,
		HostKeyCallback: hostKeyCallback(),
		Timeout:         20 * time.Second,
	}
	return ssh.Dial("tcp", fmt.Sprintf("%s:%d", host, port), cfg)
}

func keyAuth(path string) (ssh.AuthMethod, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(b)
	if err != nil {
		return nil, err
	}
	return ssh.PublicKeys(signer), nil
}

func agentAuth() ssh.AuthMethod {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil
	}
	return ssh.PublicKeysCallback(agent.NewClient(conn).Signers)
}

func hostKeyCallback() ssh.HostKeyCallback {
	kh := filepath.Join(homeDir(), ".ssh", "known_hosts")
	if cb, err := knownhosts.New(kh); err == nil {
		return cb
	}
	log.Printf("warning: no known_hosts, accepting any host key")
	return ssh.InsecureIgnoreHostKey()
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "."
}
