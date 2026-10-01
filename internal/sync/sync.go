// Package sync keeps the local package directories mirrored from MikroTik's
// official download servers. For each channel it reads the NEWESTa7.<channel>
// version file, and if the local copy is behind, downloads the all_packages zip
// for every configured architecture, extracts the .npk files into the channel
// directory, and removes any packages from older versions ("keep latest only").
package sync

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	versionURLFmt = "https://upgrade.mikrotik.com/routeros/NEWESTa7.%s"
	zipURLFmt     = "https://download.mikrotik.com/routeros/%s/all_packages-%s-%s.zip"
	mainURLFmt    = "https://download.mikrotik.com/routeros/%s/%s"
)

// DefaultArches are all RouterOS v7 CPU architectures.
var DefaultArches = []string{"arm", "arm64", "mipsbe", "mmips", "smips", "tile", "ppc", "x86"}

type Channel struct {
	Name string // "stable" or "long-term"
	Dir  string // local directory for this channel's packages
}

type Syncer struct {
	Channels []Channel
	Arches   []string
	Interval time.Duration
	Client   *http.Client
	// VersionFile records the version currently materialized in each dir.
	marker func(dir string) string
}

func New(channels []Channel, arches []string, interval time.Duration) *Syncer {
	return NewWithProxy(channels, arches, interval, "")
}

// NewWithProxy builds a Syncer whose downloads go through the given proxy.
// proxy may be "http://host:port", "https://host:port", "socks5://host:port",
// or a bare "host:port" (treated as http). Empty string falls back to the
// standard HTTP_PROXY/HTTPS_PROXY environment variables.
func NewWithProxy(channels []Channel, arches []string, interval time.Duration, proxy string) *Syncer {
	if len(arches) == 0 {
		arches = DefaultArches
	}
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment}
	if proxy != "" {
		if !strings.Contains(proxy, "://") {
			proxy = "http://" + proxy
		}
		if u, err := url.Parse(proxy); err == nil {
			tr.Proxy = http.ProxyURL(u)
			log.Printf("downloads via proxy %s", u.Redacted())
		} else {
			log.Printf("warning: bad proxy %q: %v (ignoring)", proxy, err)
		}
	}
	return &Syncer{
		Channels: channels,
		Arches:   arches,
		Interval: interval,
		// No overall client timeout: a slow 50MB download is normal. Each attempt
		// gets its own deadline via context in getWithRetry instead.
		Client: &http.Client{Transport: tr},
		marker: func(dir string) string { return filepath.Join(dir, ".version") },
	}
}

// Run does an initial sync then repeats on the interval until ctx-like stop.
func (s *Syncer) Run(stop <-chan struct{}) {
	s.SyncOnce()
	if s.Interval <= 0 {
		return
	}
	t := time.NewTicker(s.Interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			s.SyncOnce()
		}
	}
}

// SyncOnce checks and updates every channel once.
func (s *Syncer) SyncOnce() {
	for _, ch := range s.Channels {
		if err := s.syncChannel(ch); err != nil {
			log.Printf("sync %s: %v", ch.Name, err)
		}
	}
}

// LatestVersion returns the newest version string for a channel ("stable" / "long-term").
func (s *Syncer) LatestVersion(channel string) (string, error) {
	return s.latestVersion(channel)
}

// FetchInto downloads+extracts all configured arches for one channel version into
// dstDir (which it creates), writing a .version marker. It does not swap/clean any
// existing directory — the caller decides what to do with the result (serve it,
// or push it elsewhere). Returns the number of .npk files written.
func (s *Syncer) FetchInto(channel, version, dstDir string) (int, error) {
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return 0, err
	}
	total := 0
	for _, arch := range s.Arches {
		n, err := s.fetchArchN(version, arch, dstDir)
		if err != nil {
			return total, fmt.Errorf("arch %s: %w", arch, err)
		}
		total += n
	}
	if err := os.WriteFile(filepath.Join(dstDir, ".version"), []byte(version), 0o644); err != nil {
		return total, err
	}
	return total, nil
}

func (s *Syncer) syncChannel(ch Channel) error {
	latest, err := s.latestVersion(ch.Name)
	if err != nil {
		return err
	}
	cur := s.currentVersion(ch.Dir)
	if cur == latest {
		log.Printf("sync %s: up to date (%s)", ch.Name, latest)
		return nil
	}
	log.Printf("sync %s: %s -> %s, downloading %d arches", ch.Name, orNone(cur), latest, len(s.Arches))

	if err := os.MkdirAll(ch.Dir, 0o755); err != nil {
		return err
	}
	tmp := ch.Dir + ".new"
	os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}

	for _, arch := range s.Arches {
		if _, err := s.fetchArchN(latest, arch, tmp); err != nil {
			os.RemoveAll(tmp)
			return fmt.Errorf("arch %s: %w", arch, err)
		}
	}
	// write version marker, then atomically swap directories
	if err := os.WriteFile(filepath.Join(tmp, ".version"), []byte(latest), 0o644); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	old := ch.Dir + ".old"
	os.RemoveAll(old)
	if err := os.Rename(ch.Dir, old); err != nil && !os.IsNotExist(err) {
		os.RemoveAll(tmp)
		return err
	}
	if err := os.Rename(tmp, ch.Dir); err != nil {
		os.Rename(old, ch.Dir) // try to restore
		return err
	}
	os.RemoveAll(old)
	log.Printf("sync %s: now at %s", ch.Name, latest)
	return nil
}

func (s *Syncer) latestVersion(channel string) (string, error) {
	url := fmt.Sprintf(versionURLFmt, channel)
	resp, err := s.Client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("version file %s: HTTP %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return "", err
	}
	// format: "<version> <timestamp>"
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return "", fmt.Errorf("empty version file %s", url)
	}
	return fields[0], nil
}

// getWithRetry downloads a URL with resumable retries. MikroTik's CDN frequently
// resets connections mid-download, so on failure we reconnect with a Range header
// and continue from the bytes already received instead of starting over.
func (s *Syncer) getWithRetry(url string, maxStalls int) ([]byte, error) {
	var buf []byte
	var total int64 = -1
	var lastErr error
	stalls := 0 // consecutive attempts that made no forward progress

	for stalls < maxStalls {
		if stalls > 0 {
			time.Sleep(time.Duration(stalls*stalls) * time.Second) // 0,1,4,9s
		}
		before := len(buf)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
		if len(buf) > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", len(buf)))
		}
		resp, err := s.Client.Do(req)
		if err != nil {
			cancel()
			lastErr = err
			stalls++
			continue
		}
		switch resp.StatusCode {
		case 200:
			buf = buf[:0] // server ignored Range; restart from scratch
			total = resp.ContentLength
		case 206:
			if total < 0 {
				total = int64(len(buf)) + resp.ContentLength
			}
		default:
			resp.Body.Close()
			cancel()
			lastErr = fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
			if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != 416 {
				return nil, lastErr // client error: don't retry
			}
			stalls++
			continue
		}
		chunk, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		cancel()
		buf = append(buf, chunk...)

		if err == nil && (total < 0 || int64(len(buf)) >= total) {
			return buf, nil
		}
		if err != nil {
			lastErr = fmt.Errorf("read (have %d/%d): %w", len(buf), total, err)
		} else {
			lastErr = fmt.Errorf("short read %d/%d", len(buf), total)
		}
		// An attempt that downloaded more bytes is progress, not a stall.
		if len(buf) > before {
			stalls = 0
		} else {
			stalls++
		}
	}
	return nil, fmt.Errorf("after %d stalled attempts: %w", maxStalls, lastErr)
}

func (s *Syncer) currentVersion(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, ".version"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func (s *Syncer) fetchArchN(version, arch, dstDir string) (int, error) {
	url := fmt.Sprintf(zipURLFmt, version, arch, version)
	data, err := s.getWithRetry(url, 8)
	if err != nil {
		return 0, err
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return 0, err
	}
	n := 0
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !strings.HasSuffix(f.Name, ".npk") {
			continue
		}
		if err := extractFile(f, dstDir); err != nil {
			return n, err
		}
		n++
	}

	// all_packages-*.zip holds only the *extra* packages. The main system package
	// is published separately, so fetch it too — without it a router cannot
	// upgrade RouterOS itself.
	main := MainPackageName(version, arch)
	mainData, err := s.getWithRetry(fmt.Sprintf(mainURLFmt, version, main), 8)
	if err != nil {
		return n, fmt.Errorf("main package %s: %w", main, err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, main), mainData, 0o644); err != nil {
		return n, err
	}
	n++

	log.Printf("  %s %s: %d packages (incl. main)", arch, version, n)
	return n, nil
}

// MainPackageName is the file name of the main system package for an
// architecture. MikroTik publishes x86 without an architecture suffix.
func MainPackageName(version, arch string) string {
	if arch == "x86" {
		return fmt.Sprintf("routeros-%s.npk", version)
	}
	return fmt.Sprintf("routeros-%s-%s.npk", version, arch)
}

func extractFile(f *zip.File, dstDir string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	// flatten: use base name only
	out := filepath.Join(dstDir, filepath.Base(f.Name))
	w, err := os.Create(out)
	if err != nil {
		return err
	}
	defer w.Close()
	_, err = io.Copy(w, rc)
	return err
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
