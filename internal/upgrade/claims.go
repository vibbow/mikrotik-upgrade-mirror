package upgrade

import (
	"context"
	"sync"

	"github.com/local/mikrotik-mirror/internal/routeros"
)

// Claims records which routers have been handled, so a router listed under several
// address book entries (e.g. LAN and public address) is only processed once, even when
// the entries run in parallel.
type Claims struct {
	mu    sync.Mutex
	owner map[string]string // router identity -> entry name that claimed it
}

// Claim returns ("", true) if the caller now owns id, or (owner, false) if another
// entry already did.
func (c *Claims) Claim(id, name string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.owner == nil {
		c.owner = map[string]string{}
	}
	if o, ok := c.owner[id]; ok {
		return o, false
	}
	c.owner[id] = name
	return "", true
}

// identity returns a stable per-router key: the licence system-id (present on
// routerboards and CHR), else the first ethernet MAC.
func identity(ctx context.Context, c *routeros.Client) string {
	var lic routeros.Row
	if c.Get(ctx, "/system/license", &lic) == nil && lic["system-id"] != "" {
		return "sysid:" + lic["system-id"]
	}
	if ifs, err := c.List(ctx, "/interface/ethernet"); err == nil && len(ifs) > 0 && ifs[0]["mac-address"] != "" {
		return "mac:" + ifs[0]["mac-address"]
	}
	return ""
}
