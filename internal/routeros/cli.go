package routeros

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/local/mikrotik-mirror/internal/winbox"
)

// CLI drives a router through a Winbox terminal session (TCP 8291), so it works for any
// router Winbox can reach, with no REST/SSH service needed.
type CLI struct {
	T       *winbox.Terminal
	Timeout time.Duration // per ordinary command
}

var _ Backend = (*CLI)(nil)

// DialWinbox opens a terminal on the router at addr (host:port).
func DialWinbox(addr, user, password string, timeout time.Duration) (*CLI, error) {
	t, err := winbox.OpenTerminal(addr, user, password, timeout)
	if err != nil {
		return nil, err
	}
	return &CLI{T: t, Timeout: timeout}, nil
}

func (c *CLI) Close() { c.T.Close() }

func (c *CLI) run(cmd string) (string, error) { return c.T.Run(cmd, c.Timeout) }

// parseTerse reads `print terse` output: one item per line, "<index> [flags] k=v k=v".
// Values containing spaces are cut at the space, which is fine for the short fields
// (name, version, status, address, user) read here. The row index becomes ".id".
func parseTerse(out string) []Row {
	var rows []Row
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || f[0][0] < '0' || f[0][0] > '9' {
			continue
		}
		r := Row{".id": f[0]}
		for _, tok := range f[1:] {
			if k, v, ok := strings.Cut(tok, "="); ok {
				r[k] = v
			}
		}
		rows = append(rows, r)
	}
	return rows
}

func (c *CLI) Resource(ctx context.Context) (string, string, error) {
	out, err := c.run(`:put ([/system/resource/get version] . "|" . [/system/resource/get architecture-name])`)
	if err != nil {
		return "", "", err
	}
	v, a, _ := strings.Cut(out, "|")
	return strings.TrimSpace(v), strings.TrimSpace(a), nil
}

func (c *CLI) Channel(ctx context.Context) (string, error) {
	out, err := c.run(`:put [/system/package/update/get channel]`)
	return strings.TrimSpace(out), err
}

func (c *CLI) Identity(ctx context.Context) string {
	if out, err := c.run(`:put [/system/license/get system-id]`); err == nil && out != "" {
		return "sysid:" + strings.TrimSpace(out)
	}
	return ""
}

func (c *CLI) Sources(ctx context.Context) ([]Row, error) {
	out, err := c.run(`/system/package/local-update/update-package-source/print terse without-paging`)
	return parseTerse(out), err
}

func (c *CLI) SetSource(ctx context.Context, existing []Row, address, user, password string) error {
	args := fmt.Sprintf("address=%s user=%s password=%s", address, user, password)
	cmd := "/system/package/local-update/update-package-source/add " + args
	if len(existing) > 0 {
		cmd = "/system/package/local-update/update-package-source/set " + existing[0].ID() + " " + args
	}
	_, err := c.run(cmd)
	return err
}

func (c *CLI) Refresh(ctx context.Context) error {
	_, err := c.T.Run(`/system/package/local-update/refresh`, 2*c.Timeout)
	return err
}

func (c *CLI) Offered(ctx context.Context) ([]Row, error) {
	out, err := c.run(`/system/package/local-update/print terse without-paging`)
	return parseTerse(out), err
}

func (c *CLI) Installed(ctx context.Context) (map[string]bool, error) {
	out, err := c.run(`/system/package/print terse without-paging where !disabled`)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, p := range parseTerse(out) {
		set[p["name"]] = true
	}
	return set, nil
}

func (c *CLI) Download(ctx context.Context, ids []string, timeout time.Duration) error {
	_, err := c.T.Run("/system/package/local-update/download numbers="+strings.Join(ids, ","), timeout)
	return err
}
