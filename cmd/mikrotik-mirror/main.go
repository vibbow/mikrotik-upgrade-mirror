package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"log"
	"os"
	"strings"
	"time"

	"github.com/local/mikrotik-mirror/internal/mirror"
	syncpkg "github.com/local/mikrotik-mirror/internal/sync"
)

func main() {
	listen := flag.String("listen", "0.0.0.0:8291", "winbox listen address")
	stableDir := flag.String("stable-dir", "packages/stable", "stable channel package dir")
	ltDir := flag.String("longterm-dir", "packages/long-term", "long-term channel package dir")
	stableUser := flag.String("stable-user", "stable", "username that selects the stable channel")
	ltUser := flag.String("longterm-user", "longterm", "username that selects the long-term channel")
	stablePass := flag.String("stable-password", "", "password for the stable account (default: same as its username)")
	ltPass := flag.String("longterm-password", "", "password for the long-term account (default: same as its username)")
	arches := flag.String("arches", strings.Join(syncpkg.DefaultArches, ","), "comma-separated architectures to mirror")
	interval := flag.Duration("sync-interval", 6*time.Hour, "how often to check for new packages (0 = once at startup)")
	proxy := flag.String("proxy", "", "download proxy, e.g. http://host:port (default: env HTTP(S)_PROXY)")
	noSync := flag.Bool("no-sync", false, "disable the package syncer (serve existing files only)")
	replayFile := flag.String("replay", "", "debug: JSON array of hex object bodies to return from LIST verbatim")
	flag.Parse()

	// The Winbox EC-SRP5 handshake needs the server to know each account's password
	// (it derives the session keys from it), so a router can only connect with exactly
	// that password. The packages are public and signed, so by default each password is
	// simply the username: stable/stable and longterm/longterm.
	sPass, lPass := *stablePass, *ltPass
	if sPass == "" {
		sPass = *stableUser
	}
	if lPass == "" {
		lPass = *ltUser
	}
	if *stableUser == "" || *ltUser == "" || *stableUser == *ltUser {
		log.Fatal("--stable-user and --longterm-user must be non-empty and different from each other")
	}

	// Package syncer: keeps stable-dir / longterm-dir mirrored from MikroTik.
	if !*noSync {
		s := syncpkg.NewWithProxy(
			[]syncpkg.Channel{
				{Name: "stable", Dir: *stableDir},
				{Name: "long-term", Dir: *ltDir},
			},
			splitArches(*arches),
			*interval,
			*proxy,
		)
		go s.Run(nil)
	}

	srv := &mirror.Server{
		Listen: *listen,
		Accounts: map[string]mirror.Account{
			*stableUser: {Username: *stableUser, Password: sPass, Dir: *stableDir},
			*ltUser:     {Username: *ltUser, Password: lPass, Dir: *ltDir},
		},
	}
	if *replayFile != "" {
		raw, err := os.ReadFile(*replayFile)
		if err != nil {
			log.Fatal(err)
		}
		var hexes []string
		if err := json.Unmarshal(raw, &hexes); err != nil {
			log.Fatal(err)
		}
		for _, h := range hexes {
			b, _ := hex.DecodeString(h)
			srv.ReplayObjects = append(srv.ReplayObjects, b)
		}
		log.Printf("replay mode: %d objects", len(srv.ReplayObjects))
	}
	log.Printf("channels:\n%s", srv.Describe())
	log.Fatal(srv.Run())
}

func splitArches(s string) []string {
	var out []string
	for _, a := range strings.Split(s, ",") {
		a = strings.TrimSpace(a)
		if a != "" {
			out = append(out, a)
		}
	}
	return out
}
