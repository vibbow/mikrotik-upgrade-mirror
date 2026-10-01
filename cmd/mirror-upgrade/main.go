// mirror-upgrade reads the Winbox address book, connects to every router over the
// RouterOS REST API, points its local-update package source at the mirror (account
// chosen by the router's update channel), and downloads any newer packages.
//
// By default it is interactive: it asks before connecting to each router, before binding
// the package source (if not yet bound) and before downloading. --yes skips the questions;
// --dry-run only looks.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"net"
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
		book         = flag.String("addressbook", "", "Winbox 地址簿 Addresses.cdb 的路径 (默认在常见位置查找)")
		mirror       = flag.String("mirror", "203.0.113.10", "镜像服务器地址 (路由器访问它用的地址)")
		stableP      = flag.String("stable-password", "stable", "镜像 `stable` 账号的密码")
		longP        = flag.String("longterm-password", "longterm", "镜像 `longterm` 账号的密码")
		yes          = flag.Bool("yes", false, "不询问: 对所有路由器直接连接、绑定、下载 (并行执行)")
		dryRun       = flag.Bool("dry-run", false, "只查看: 不绑定也不下载")
		confirmSteps = flag.Bool("confirm-steps", false, "交互模式下，绑定镜像、下载之前也逐步询问 (默认只在连接前询问)")
		only         = flag.String("only", "", "只处理地址、备注或分组包含此文字的路由器")
		conc         = flag.Int("concurrency", 4, "同时处理的路由器数 (仅 --yes 模式)")
		timeout      = flag.Duration("timeout", 15*time.Second, "连接 / 单个请求超时")
		transport    = flag.String("transport", "winbox", "连接方式: winbox (地址簿里的 Winbox 端口，经终端执行命令) 或 rest (RouterOS REST API)")
		dlTime       = flag.Duration("download-timeout", 15*time.Minute, "路由器下载的最长等待时间")
		list         = flag.Bool("list", false, "只列出地址簿 (不显示密码) 然后退出")
	)
	flag.Parse()

	path, err := findBook(*book)
	if err != nil {
		fatal(err)
	}
	entries, err := addrbook.ReadFile(path)
	if err != nil {
		fatal(fmt.Errorf("%s: %w", path, err))
	}
	fmt.Printf("地址簿: %s (共 %d 条)\n", path, len(entries))

	var targets []addrbook.Entry
	seen := map[string]bool{}
	for _, e := range entries {
		if *only != "" && !strings.Contains(strings.ToLower(e.Address+"\x00"+e.Note+"\x00"+e.Group), strings.ToLower(*only)) {
			continue
		}
		reason := ""
		switch {
		case e.IsMAC():
			reason = "MAC 地址条目，跳过"
		case e.Login == "":
			reason = "没有保存登录名，跳过"
		case seen[e.Host()]:
			reason = "地址重复，跳过"
		}
		if *list || reason != "" {
			fmt.Printf("  %-34s 登录=%-10s 分组=%-10s %s\n", e.Name(), e.Login, e.Group, reason)
		}
		if reason == "" {
			seen[e.Host()] = true
			targets = append(targets, e)
		}
	}
	if *list {
		return
	}

	mode := "交互 (连接每台路由器前询问，确认后自动绑定镜像、检查更新、下载)"
	switch {
	case *dryRun:
		mode = "仅查看 (不做任何修改)"
	case *yes:
		mode = "免确认 (不询问)"
	}
	fmt.Printf("模式: %s; 镜像: %s; 共 %d 台路由器\n\n", mode, *mirror, len(targets))

	in := &asker{r: bufio.NewReader(os.Stdin)}
	interactive := !*yes

	opts := upgrade.Options{
		Mirror: *mirror,
		Accounts: map[string]upgrade.Account{
			"stable":    {User: "stable", Password: *stableP},
			"long-term": {User: "longterm", Password: *longP},
		},
		Apply:           !*dryRun,
		Claims:          &upgrade.Claims{},
		DownloadTimeout: *dlTime,
	}

	type outcome struct {
		e addrbook.Entry
		r upgrade.Result
	}
	results := make([]outcome, len(targets))
	var mu sync.Mutex
	handle := func(i int, e addrbook.Entry) {
		ctx := context.Background()
		var r upgrade.Result
		b, closeFn, err := connect(ctx, *transport, e, *timeout)
		if err != nil {
			r = upgrade.Result{Status: upgrade.Failed, Detail: "连接失败: " + err.Error()}
		} else {
			o := opts
			o.Name = e.Name()
			if interactive {
				o.Log = func(s string) { fmt.Println("  " + s) }
				if *confirmSteps { // by default only the connect question is asked
					o.Confirm = func(q string) bool { return in.ask("  " + q) }
				}
			}
			r = upgrade.Router(ctx, b, o)
			closeFn()
		}
		results[i] = outcome{e, r}
		mu.Lock()
		if interactive {
			fmt.Printf("  => %s: %s\n\n", r.Status, r.Detail)
		} else {
			fmt.Printf("%-34s %-11s %s\n", e.Name(), r.Status, describe(r))
		}
		mu.Unlock()
	}
	if interactive {
		// one router at a time, each confirmed by the user
		for i, e := range targets {
			fmt.Printf("[%d/%d] %s  (登录名 %s)\n", i+1, len(targets), e.Name(), e.Login)
			if !in.ask("  连接这台路由器?") {
				if in.quit {
					fmt.Println("已按你的要求停止")
					break
				}
				results[i] = outcome{e, upgrade.Result{Status: upgrade.Skipped, Detail: "未连接 (已拒绝)"}}
				fmt.Print("  => 跳过\n\n")
				continue
			}
			handle(i, e)
			if in.quit {
				fmt.Println("已按你的要求停止")
				break
			}
		}
	} else {
		var wg sync.WaitGroup
		sem := make(chan struct{}, max(1, *conc))
		for i, e := range targets {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				handle(i, e)
			}()
		}
		wg.Wait()
	}

	counts := map[upgrade.Status]int{}
	for _, o := range results {
		counts[o.r.Status]++
	}
	fmt.Printf("\n汇总: %d 台已下载, %d 台已是最新, %d 台计划, %d 台跳过, %d 台失败\n",
		counts[upgrade.Staged], counts[upgrade.UpToDate], counts[upgrade.Planned], counts[upgrade.Skipped], counts[upgrade.Failed])
	var reboot []outcome
	for _, o := range results {
		if o.r.Status == upgrade.Staged {
			reboot = append(reboot, o)
		}
	}
	if len(reboot) > 0 {
		fmt.Printf("\n以下 %d 台路由器已下载更新包，需要你手动重启才会安装 (本工具不会重启路由器):\n", len(reboot))
		for _, o := range reboot {
			fmt.Printf("  %-34s 当前 %s %s -> %s\n", o.e.Name(), o.r.Arch, o.r.Version, strings.Join(o.r.Packages, ", "))
		}
	} else if counts[upgrade.Staged] == 0 {
		fmt.Println("\n没有路由器需要重启。")
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
	return "", fmt.Errorf("找不到地址簿; 请用 --addressbook 指定 (Winbox 把它放在设置里指定的位置，例如 E:\\Tools\\winbox\\Addresses.cdb)")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "错误:", err)
	os.Exit(1)
}

// asker reads yes/no answers from the terminal. "q" (or end of input) stops the run.
type asker struct {
	r    *bufio.Reader
	quit bool
}

func (a *asker) ask(q string) bool {
	if a.quit {
		return false
	}
	fmt.Printf("%s [y/N/q] ", q)
	line, err := a.r.ReadString('\n')
	ans := strings.ToLower(strings.TrimSpace(line))
	if ans == "q" || ans == "quit" || (err != nil && ans == "") {
		a.quit = true
		return false
	}
	return ans == "y" || ans == "yes"
}

// connect reaches one router and returns the backend plus a function that closes it.
func connect(ctx context.Context, transport string, e addrbook.Entry, timeout time.Duration) (routeros.Backend, func(), error) {
	switch transport {
	case "winbox":
		addr := e.Address
		if _, _, err := net.SplitHostPort(addr); err != nil {
			addr = net.JoinHostPort(addr, "8291")
		}
		c, err := routeros.DialWinbox(addr, e.Login, e.Password, timeout)
		if err != nil {
			return nil, nil, err
		}
		return c, c.Close, nil
	case "rest":
		c, err := routeros.Dial(ctx, e.Host(), e.Login, e.Password, timeout)
		if err != nil {
			return nil, nil, err
		}
		return c.WithTimeout(timeout), func() {}, nil
	}
	return nil, nil, fmt.Errorf("未知的连接方式 %q (可选 winbox / rest)", transport)
}
