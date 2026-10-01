package routeros

import (
	"context"
	"strings"
	"time"
)

// Backend is what the upgrade flow needs from a router, independent of how it is
// reached (REST API, or CLI over the Winbox terminal).
type Backend interface {
	// Resource returns the full version string ("7.23.7 (long-term)") and architecture.
	Resource(ctx context.Context) (version, arch string, err error)
	// Channel returns the update channel; may be empty if the router does not say.
	Channel(ctx context.Context) (string, error)
	// Identity is a stable per-router key ("sysid:..." / "mac:..."), or "" if unknown.
	Identity(ctx context.Context) string
	// Sources lists local-update package sources (keys: address, user).
	Sources(ctx context.Context) ([]Row, error)
	// SetSource replaces the first existing source, or adds one.
	SetSource(ctx context.Context, existing []Row, address, user, password string) error
	Refresh(ctx context.Context) error
	// Offered lists what the source offers (keys: name, version, status).
	Offered(ctx context.Context) ([]Row, error)
	// Installed returns the names of installed packages that are not disabled.
	Installed(ctx context.Context) (map[string]bool, error)
	// Download fetches the offered packages with the given .id values.
	Download(ctx context.Context, ids []string, timeout time.Duration) error
}

const (
	sourcePath = "/system/package/local-update/update-package-source"
	luPath     = "/system/package/local-update"
)

var _ Backend = (*Client)(nil)

func (c *Client) Resource(ctx context.Context) (string, string, error) {
	var res Row
	if err := c.Get(ctx, "/system/resource", &res); err != nil {
		return "", "", err
	}
	return res["version"], res["architecture-name"], nil
}

func (c *Client) Channel(ctx context.Context) (string, error) {
	var upd Row
	if err := c.Get(ctx, "/system/package/update", &upd); err != nil {
		return "", err
	}
	return upd["channel"], nil
}

func (c *Client) Identity(ctx context.Context) string {
	var lic Row
	if c.Get(ctx, "/system/license", &lic) == nil && lic["system-id"] != "" {
		return "sysid:" + lic["system-id"]
	}
	if ifs, err := c.List(ctx, "/interface/ethernet"); err == nil && len(ifs) > 0 && ifs[0]["mac-address"] != "" {
		return "mac:" + ifs[0]["mac-address"]
	}
	return ""
}

func (c *Client) Sources(ctx context.Context) ([]Row, error) { return c.List(ctx, sourcePath) }

func (c *Client) SetSource(ctx context.Context, existing []Row, address, user, password string) error {
	body := map[string]string{"address": address, "user": user, "password": password}
	if len(existing) > 0 {
		return c.Patch(ctx, sourcePath+"/"+existing[0].ID(), body, nil)
	}
	return c.Put(ctx, sourcePath, body, nil)
}

func (c *Client) Refresh(ctx context.Context) error {
	return c.Post(ctx, luPath+"/refresh", map[string]string{}, nil)
}

func (c *Client) Offered(ctx context.Context) ([]Row, error) { return c.List(ctx, luPath) }

func (c *Client) Installed(ctx context.Context) (map[string]bool, error) {
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

func (c *Client) Download(ctx context.Context, ids []string, timeout time.Duration) error {
	return c.WithTimeout(timeout).Post(ctx, luPath+"/download", map[string]string{"numbers": strings.Join(ids, ",")}, nil)
}
