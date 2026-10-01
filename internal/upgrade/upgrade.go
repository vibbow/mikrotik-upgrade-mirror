// Package upgrade drives one router through: point local-update at the mirror (the
// account is chosen by the router's own update channel), refresh, and download whatever
// the mirror has that is newer than what is installed.
package upgrade

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/local/mikrotik-mirror/internal/routeros"
)

const (
	sourcePath = "/system/package/local-update/update-package-source"
	luPath     = "/system/package/local-update"
)

// Account is the mirror login that serves one channel.
type Account struct{ User, Password string }

// Options configure Router.
type Options struct {
	Mirror          string             // address of the mirror server
	Accounts        map[string]Account // keyed by router channel: "stable", "long-term"
	Apply           bool               // false = look only, change nothing
	DownloadTimeout time.Duration
	Claims          *Claims // shared across routers; detects one router listed under several addresses
	Name            string  // label of this address book entry
}

// Status is the outcome for one router.
type Status string

const (
	Staged   Status = "downloaded" // packages staged, reboot installs them
	UpToDate Status = "up-to-date"
	Planned  Status = "planned" // dry run: would bind/download
	Skipped  Status = "skipped"
	Failed   Status = "failed"
)

// Result describes what happened on one router.
type Result struct {
	Status  Status
	Arch    string
	Version string
	Channel string
	Detail  string
}

func fail(r *Result, format string, a ...any) Result {
	r.Status, r.Detail = Failed, fmt.Sprintf(format, a...)
	return *r
}

// Router runs the whole flow against one connected router.
func Router(ctx context.Context, c *routeros.Client, o Options) Result {
	var r Result

	var res routeros.Row
	if err := c.Get(ctx, "/system/resource", &res); err != nil {
		return fail(&r, "read resource: %v", err)
	}
	r.Version, r.Arch = routeros.MainVersion(res["version"]), res["architecture-name"]

	var upd routeros.Row
	if err := c.Get(ctx, "/system/package/update", &upd); err != nil {
		return fail(&r, "read update channel: %v", err)
	}

	if o.Claims != nil {
		if id := identity(ctx, c); id != "" {
			if owner, first := o.Claims.Claim(id, o.Name); !first {
				r.Status, r.Detail = Skipped, "same router as "+owner+", already handled"
				return r
			}
		}
	}

	r.Channel = upd["channel"]
	if r.Channel == "" {
		r.Channel = versionChannel(res["version"]) // "7.23.7 (long-term)"
	}
	acct, ok := o.Accounts[r.Channel]
	if !ok {
		r.Status, r.Detail = Skipped, fmt.Sprintf("channel %q is not mirrored", r.Channel)
		if r.Channel == "" {
			r.Detail = "cannot tell the update channel from the router"
		}
		return r
	}

	// 1. bind the package source.
	srcs, err := c.List(ctx, sourcePath)
	if err != nil {
		return fail(&r, "read package source: %v", err)
	}
	bound := len(srcs) > 0 && srcs[0]["address"] == o.Mirror && srcs[0]["user"] == acct.User
	var steps []string
	if !bound {
		steps = append(steps, fmt.Sprintf("bind %s as %q", o.Mirror, acct.User))
		if o.Apply {
			body := map[string]string{"address": o.Mirror, "user": acct.User, "password": acct.Password}
			if len(srcs) > 0 {
				err = c.Patch(ctx, sourcePath+"/"+srcs[0].ID(), body, nil)
			} else {
				err = c.Put(ctx, sourcePath, body, nil)
			}
			if err != nil {
				return fail(&r, "bind package source: %v", err)
			}
		}
	}
	if !o.Apply {
		r.Status = Planned
		if bound {
			steps = append(steps, "already bound; would refresh and download anything newer")
		} else {
			steps = append(steps, "then refresh and download anything newer")
		}
		r.Detail = strings.Join(steps, ", ")
		return r
	}

	// 2. refresh and list what the mirror offers.
	if err := c.Post(ctx, luPath+"/refresh", map[string]string{}, nil); err != nil {
		return fail(&r, "refresh: %v", err)
	}
	offered, err := listOffered(ctx, c)
	if err != nil {
		return fail(&r, "list mirror packages: %v", err)
	}
	installed, err := installedPackages(ctx, c)
	if err != nil {
		return fail(&r, "read installed packages: %v", err)
	}
	var ids, names []string
	for _, p := range offered {
		if strings.EqualFold(p["status"], "available") && installed[p["name"]] {
			ids = append(ids, p.ID())
			names = append(names, p["name"]+" "+p["version"])
		}
	}
	if len(ids) == 0 {
		if n := countStatus(offered, "downloaded"); n > 0 {
			r.Status, r.Detail = Staged, fmt.Sprintf("%d package(s) already downloaded earlier; reboot to install", n)
			return r
		}
		r.Status = UpToDate
		r.Detail = fmt.Sprintf("none of the %d installed package(s) has a newer version on the mirror than %s", len(installed), r.Version)
		return r
	}

	// 3. download.
	if err := c.WithTimeout(o.DownloadTimeout).Post(ctx, luPath+"/download", map[string]string{"numbers": strings.Join(ids, ",")}, nil); err != nil {
		return fail(&r, "download %s: %v", strings.Join(names, ", "), err)
	}
	after, err := c.List(ctx, luPath)
	if err != nil {
		return fail(&r, "verify download: %v", err)
	}
	pending := 0
	for _, p := range after {
		if strings.EqualFold(p["status"], "available") && installed[p["name"]] {
			pending++
		}
	}
	if pending > 0 {
		return fail(&r, "%d package(s) still not downloaded after the download command", pending)
	}
	r.Status = Staged
	r.Detail = fmt.Sprintf("%s staged; reboot to install (enabled packages: %s)", strings.Join(names, ", "), sortedKeys(installed))
	return r
}

// listOffered reads the local-update list, waiting briefly for refresh to populate it.
func listOffered(ctx context.Context, c *routeros.Client) ([]routeros.Row, error) {
	var rows []routeros.Row
	var err error
	for i := 0; i < 10; i++ {
		if rows, err = c.List(ctx, luPath); err != nil || len(rows) > 0 {
			return rows, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return rows, nil
}

func countStatus(rows []routeros.Row, status string) int {
	n := 0
	for _, p := range rows {
		if strings.EqualFold(p["status"], status) {
			n++
		}
	}
	return n
}

// versionChannel extracts "long-term" from "7.23.7 (long-term)".
func versionChannel(v string) string {
	i, j := strings.IndexByte(v, '('), strings.IndexByte(v, ')')
	if i < 0 || j < i {
		return ""
	}
	return v[i+1 : j]
}

// installedPackages returns the names of packages installed on the router. Only these
// are downloaded: the mirror offers every extra package, and the router installs every
// .npk it finds on reboot, so fetching the rest would install unwanted packages.
func installedPackages(ctx context.Context, c *routeros.Client) (map[string]bool, error) {
	rows, err := c.List(ctx, "/system/package")
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, p := range rows {
		if strings.EqualFold(p["disabled"], "true") { // bundled in routeros.npk but switched off
			continue
		}
		set[p["name"]] = true
	}
	return set, nil
}

func sortedKeys(m map[string]bool) string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return strings.Join(ks, " ")
}
