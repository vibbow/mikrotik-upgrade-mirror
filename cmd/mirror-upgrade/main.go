// mirror-upgrade reads the Winbox address book, connects to every router over the
// RouterOS REST API, points its local-update package source at the mirror (account
// chosen by the router's update channel), and downloads any newer packages.
//
// By default it only looks and prints a plan; pass --apply to change routers.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/local/mikrotik-mirror/internal/addrbook"
	"github.com/local/mikrotik-mirror/internal/routeros"
	"github.com/local/mikrotik-mirror/internal/upgrade"
)

func main() {
	var (
		book     = flag.String("addressbook", "", "path to Winbox Addresses.cdb (default: search the usual places)")
		mirror   = flag.String("mirror", "203.0.113.10", "address of the mirror server, as routers reach it")
		stableP  = flag.String("stable-password", "stable", "password of the mirror's `stable` account")
		longP    = flag.String("longterm-password", "longterm", "password of the mirror's `longterm` account")
		apply    = flag.Bool("apply", false, "actually bind the source and download (default: look only)")
		only     = flag.String("only", "", "only routers whose address, note or group contains this text")
		conc     = flag.Int("concurrency", 4, "routers handled at the same time")
		timeout  = flag.Duration("timeout", 15*time.Second, "connect / per-request timeout")
		dlTime   = flag.Duration("download-timeout", 15*time.Minute, "how long a router may take to download")
		list     = flag.Bool("list", false, "just list the address book (no passwords) and exit")
		insecure = flag.Bool("allow-plain-http", true, "fall back to http when https is unavailable")
	)
	flag.Parse()
	_ = insecure

	path, err := findBook(*book)
	if err != nil {
		fatal(err)
	}
	entries, err := addrbook.ReadFile(path)
	if err != nil {
		fatal(fmt.Errorf("%s: %w", path, err))
	}
	fmt.Printf("address book: %s (%d entries)\n", path, len(entries))

	var targets []addrbook.Entry
	seen := map[string]bool{}
	for _, e := range entries {
		if *only != "" && !strings.Contains(strings.ToLower(e.Address+"\x00"+e.Note+"\x00"+e.Group), strings.ToLower(*only)) {
			continue
		}
		reason := ""
		switch {
		case e.IsMAC():
			reason = "MAC address entry"
		case e.Login == "":
			reason = "no login saved"
		case seen[e.Host()]:
			reason = "duplicate host"
		}
		if *list || reason != "" {
			fmt.Printf("  %-34s login=%-10s group=%-10s %s\n", e.Name(), e.Login, e.Group, reason)
		}
		if reason == "" {
			seen[e.Host()] = true
			targets = append(targets, e)
		}
	}
	if *list {
		return
	}

	mode := "DRY RUN (nothing is changed; add --apply)"
	if *apply {
		mode = "APPLY"
	}
	fmt.Printf("mode: %s; mirror: %s; %d router(s)\n\n", mode, *mirror, len(targets))

	opts := upgrade.Options{
		Mirror: *mirror,
		Accounts: map[string]upgrade.Account{
			"stable":    {User: "stable", Password: *stableP},
			"long-term": {User: "longterm", Password: *longP},
		},
		Apply:           *apply,
		Claims:          &upgrade.Claims{},
		DownloadTimeout: *dlTime,
	}

	type outcome struct {
		e addrbook.Entry
		r upgrade.Result
	}
	results := make([]outcome, len(targets))
	var wg sync.WaitGroup
	var mu sync.Mutex
	sem := make(chan struct{}, max(1, *conc))
	for i, e := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ctx := context.Background()
			var r upgrade.Result
			c, err := routeros.Dial(ctx, e.Host(), e.Login, e.Password, *timeout)
			if err != nil {
				r = upgrade.Result{Status: upgrade.Failed, Detail: "connect: " + err.Error()}
			} else {
				o := opts
				o.Name = e.Name()
				r = upgrade.Router(ctx, c.WithTimeout(*timeout), o)
			}
			results[i] = outcome{e, r}
			mu.Lock()
			fmt.Printf("%-34s %-11s %s\n", e.Name(), r.Status, describe(r))
			mu.Unlock()
		}()
	}
	wg.Wait()

	counts := map[upgrade.Status]int{}
	for _, o := range results {
		counts[o.r.Status]++
	}
	fmt.Printf("\nsummary: %d downloaded, %d up-to-date, %d planned, %d skipped, %d failed\n",
		counts[upgrade.Staged], counts[upgrade.UpToDate], counts[upgrade.Planned], counts[upgrade.Skipped], counts[upgrade.Failed])
	if counts[upgrade.Staged] > 0 {
		fmt.Println("Downloaded packages are installed when the router reboots; this tool never reboots.")
	}
	if counts[upgrade.Failed] > 0 {
		os.Exit(1)
	}
}

func describe(r upgrade.Result) string {
	var p []string
	if r.Version != "" {
		p = append(p, fmt.Sprintf("%s %s [%s]", r.Arch, r.Version, r.Channel))
	}
	if r.Detail != "" {
		p = append(p, r.Detail)
	}
	return strings.Join(p, " | ")
}

func findBook(flagPath string) (string, error) {
	if flagPath != "" {
		return flagPath, nil
	}
	var cands []string
	if ad := os.Getenv("APPDATA"); ad != "" {
		cands = append(cands, filepath.Join(ad, "Mikrotik", "Winbox", "Addresses.cdb"))
	}
	cands = append(cands, "Addresses.cdb")
	for _, c := range cands {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("address book not found; pass --addressbook (Winbox keeps it where its settings point, e.g. E:\\Tools\\winbox\\Addresses.cdb)")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
