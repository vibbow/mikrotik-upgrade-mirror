package upgrade

import (
	"sync"
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
