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
	// Confirm is asked before each change (bind, download). nil means yes to everything.
	Confirm func(question string) bool
	// Log receives progress lines (what was found); may be nil.
	Log func(line string)
}

// Status is the outcome for one router.
type Status string

const (
	Staged   Status = "已下载" // packages staged, reboot installs them
	UpToDate Status = "已是最新"
	Planned  Status = "计划" // dry run: would bind/download
	Skipped  Status = "跳过"
	Failed   Status = "失败"
)

// Result describes what happened on one router.
type Result struct {
	Status  Status
	Arch    string
	Version string
	Channel string
	Detail  string
	// Packages are those staged on the router (downloaded now or earlier); the router
	// installs them on its next reboot. Set when Status is Staged.
	Packages []string
}

func fail(r *Result, format string, a ...any) Result {
	r.Status, r.Detail = Failed, fmt.Sprintf(format, a...)
	return *r
}

// Router runs the whole flow against one connected router.
func Router(ctx context.Context, c routeros.Backend, o Options) Result {
	var r Result

	fullVersion, arch, err := c.Resource(ctx)
	if err != nil {
		return fail(&r, "读取系统信息失败: %v", err)
	}
	r.Version, r.Arch = routeros.MainVersion(fullVersion), arch

	channel, err := c.Channel(ctx)
	if err != nil {
		return fail(&r, "读取更新通道失败: %v", err)
	}

	if o.Claims != nil {
		if id := c.Identity(ctx); id != "" {
			if owner, first := o.Claims.Claim(id, o.Name); !first {
				r.Status, r.Detail = Skipped, "与 "+owner+" 是同一台路由器，已处理过"
				return r
			}
		}
	}

	r.Channel = channel
	if r.Channel == "" {
		r.Channel = versionChannel(fullVersion) // "7.23.7 (long-term)"
	}
	acct, ok := o.Accounts[r.Channel]
	if !ok {
		r.Status, r.Detail = Skipped, fmt.Sprintf("通道 %q 没有镜像，跳过", r.Channel)
		if r.Channel == "" {
			r.Detail = fmt.Sprintf("无法从路由器判断更新通道 (version=%q)", fullVersion)
		}
		return r
	}

	o.logf("已连接: %s %s [%s]", r.Arch, r.Version, r.Channel)

	// 1. bind the package source.
	srcs, err := c.Sources(ctx)
	if err != nil {
		return fail(&r, "读取升级包来源失败: %v", err)
	}
	bound := len(srcs) > 0 && srcs[0]["address"] == o.Mirror && srcs[0]["user"] == acct.User
	switch {
	case bound:
		o.logf("升级包来源: 已绑定到镜像 (%s，用户 %q)", o.Mirror, acct.User)
	case len(srcs) > 0:
		o.logf("升级包来源: 未绑定镜像 (当前是 %s，用户 %q)", srcs[0]["address"], srcs[0]["user"])
	default:
		o.logf("升级包来源: 未设置")
	}
	var steps []string
	if !bound {
		steps = append(steps, fmt.Sprintf("将绑定 %s (用户 %q)", o.Mirror, acct.User))
		if o.Apply {
			q := fmt.Sprintf("绑定到镜像 %s (用户 %s)?", o.Mirror, acct.User)
			if !o.confirm(q) {
				r.Status, r.Detail = Skipped, "未绑定镜像 (已拒绝绑定)"
				return r
			}
			if err := c.SetSource(ctx, srcs, o.Mirror, acct.User, acct.Password); err != nil {
				return fail(&r, "绑定升级包来源失败: %v", err)
			}
			// read it back: a command that silently did nothing must not look like success
			now, err := c.Sources(ctx)
			if err != nil || len(now) == 0 || now[0]["address"] != o.Mirror || now[0]["user"] != acct.User {
				return fail(&r, "绑定命令执行了，但读回来的升级包来源不是镜像: %v (err=%v)", now, err)
			}
			o.logf("已绑定到镜像 %s (用户 %s)", o.Mirror, acct.User)
		}
	}
	if !o.Apply {
		r.Status = Planned
		if bound {
			steps = append(steps, "已绑定; 将刷新并下载更新的包")
		} else {
			steps = append(steps, "然后刷新并下载更新的包")
		}
		r.Detail = strings.Join(steps, "，")
		return r
	}

	// 2. refresh and list what the mirror offers.
	if err := c.Refresh(ctx); err != nil {
		return fail(&r, "刷新失败: %v", err)
	}
	offered, err := listOffered(ctx, c)
	if err != nil {
		return fail(&r, "读取镜像包列表失败: %v", err)
	}
	o.logf("已刷新, 镜像提供 %d 个包", len(offered))
	installed, err := c.Installed(ctx)
	if err != nil {
		return fail(&r, "读取已安装的包失败: %v", err)
	}
	var ids, names []string
	for _, p := range offered {
		if strings.EqualFold(p["status"], "available") && installed[p["name"]] {
			ids = append(ids, p.ID())
			names = append(names, p["name"]+" "+p["version"])
		}
	}
	if len(ids) == 0 {
		if pk := packagesWithStatus(offered, "downloaded"); len(pk) > 0 {
			r.Status, r.Packages, r.Detail = Staged, pk, fmt.Sprintf("已有 %d 个包下载过了，重启即可安装", len(pk))
			return r
		}
		r.Status = UpToDate
		r.Detail = fmt.Sprintf("已启用的 %d 个包在镜像上都没有比 %s 更新的版本", len(installed), r.Version)
		return r
	}

	o.logf("镜像上有更新: %s", strings.Join(names, ", "))
	q := fmt.Sprintf("下载这 %d 个包?", len(names))
	if !o.confirm(q) {
		r.Status, r.Detail = Skipped, "已拒绝下载; 可下载: "+strings.Join(names, ", ")
		return r
	}
	// 3. download.
	if err := c.Download(ctx, ids, o.DownloadTimeout); err != nil {
		return fail(&r, "下载 %s 失败: %v", strings.Join(names, ", "), err)
	}
	after, err := c.Offered(ctx)
	if err != nil {
		return fail(&r, "确认下载结果失败: %v", err)
	}
	pending := 0
	for _, p := range after {
		if strings.EqualFold(p["status"], "available") && installed[p["name"]] {
			pending++
		}
	}
	if pending > 0 {
		return fail(&r, "下载命令执行后仍有 %d 个包未下载", pending)
	}
	r.Status, r.Packages = Staged, names
	r.Detail = fmt.Sprintf("%s 已下载，重启后安装 (已启用的包: %s)", strings.Join(names, ", "), sortedKeys(installed))
	return r
}

// listOffered reads the local-update list, waiting briefly for refresh to populate it.
func listOffered(ctx context.Context, c routeros.Backend) ([]routeros.Row, error) {
	var rows []routeros.Row
	var err error
	for i := 0; i < 10; i++ {
		if rows, err = c.Offered(ctx); err != nil || len(rows) > 0 {
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

// versionChannel extracts "long-term" from "7.23.7 (long-term)".
func versionChannel(v string) string {
	i, j := strings.IndexByte(v, '('), strings.IndexByte(v, ')')
	if i < 0 || j < i {
		return ""
	}
	return v[i+1 : j]
}

func sortedKeys(m map[string]bool) string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return strings.Join(ks, " ")
}

func (o Options) confirm(q string) bool { return o.Confirm == nil || o.Confirm(q) }

func (o Options) logf(format string, a ...any) {
	if o.Log != nil {
		o.Log(fmt.Sprintf(format, a...))
	}
}

// packagesWithStatus returns "name version" for each offered package in the given state.
func packagesWithStatus(rows []routeros.Row, status string) []string {
	var out []string
	for _, p := range rows {
		if strings.EqualFold(p["status"], status) {
			out = append(out, p["name"]+" "+p["version"])
		}
	}
	return out
}
