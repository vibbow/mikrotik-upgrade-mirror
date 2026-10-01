# mikrotik-mirror

**中文** | [English](README_en.md)

一个可以跑在普通 Linux 上的自建 **RouterOS `local-update` 升级包源**。

RouterOS 7.17+ 可以通过 Winbox 协议从另一台设备升级（`/system/package/local-update`），
但正常情况下作为升级包源的那台设备本身也必须是 RouterOS。本项目用 Go 重新实现了协议里
“包源”这一侧，所以一台普通的 Linux 服务器就能充当升级包源。适用于官方 HTTP 源
（`upgrade.mikrotik.com`）很慢或者访问不了的网络环境。

包含四个程序：

| 程序 | 运行位置 | 作用 |
|------|----------|------|
| `mikrotik-mirror` | 镜像服务器 | 通过 Winbox 8291 端口向路由器提供升级包，自己从不下载任何东西 |
| `mirror-push` | 你的电脑 | 在本地下载升级包（走你的快速网络或代理），再通过 SFTP 推送到服务器 |
| `mirror-upgrade` | 你的电脑 | 读取 Winbox 地址簿，逐台连接路由器，绑定镜像并下载更新包 |
| `winbox-capture` | 你的电脑 | Winbox 中间人抓包代理，仅用于研究协议，日常不需要 |

**更新镜像：** 双击 `push-mirror.bat`（或运行 `push-mirror.ps1`），它会在你的电脑上下载最新的
stable 和 long-term 升级包并上传到服务器。

**升级路由器：** 双击 `upgrade-routers.bat`，见下文《批量升级路由器》。

## 路由器怎么选择更新通道

Winbox 的 `local-update` 协议里**没有通道字段**：包源只负责列出文件，路由器会把所有版本都显示出来。
为了避免 `stable` 和 `long-term` 的路由器互相看到对方的版本，服务器**按 Winbox 用户名选择通道**：

- 用户名配置为 `stable` 的路由器，看到的是 `packages/stable/`
- 用户名配置为 `longterm` 的路由器，看到的是 `packages/long-term/`

两个账号的密码默认和用户名相同：`stable` / `stable`、`longterm` / `longterm`。
（想改的话用 `--stable-password` / `--longterm-password`。）升级包本身是公开且带签名的，
所以密码只是用来限制谁能使用你的镜像。

服务器不能接受任意密码：Winbox 的 EC-SRP5 握手会用密码推导会话密钥，所以路由器必须输入
和账号完全一致的密码。密码错误时，服务器日志里会出现 “client confirmation mismatch”。

## 服务器用法

```
mikrotik-mirror \
  --listen 0.0.0.0:8291 \
  --stable-dir  /opt/mikrotik-mirror/packages/stable \
  --longterm-dir /opt/mikrotik-mirror/packages/long-term
```

它提供这两个目录里的内容（每次请求都会重新读取，所以推送新包后立即生效，不用重启）。
每个通道只保留**最新**版本。部署方法见 [`deploy/README.md`](deploy/README.md)。

## 推送升级包

`push-mirror.bat` / `push-mirror.ps1` 是对 `mirror-push` 的封装。先修改 `push-mirror.ps1` 顶部
的设置（服务器、用户、远程目录、代理），然后：

```
push-mirror.bat              # 两个通道都更新；已经是最新的通道会跳过
push-mirror.bat -Force       # 强制重新下载并重新上传
push-mirror.bat -Channels stable
```

也可以直接调用程序：

```
mirror-push --host your-server --user root --remote-dir /opt/mikrotik-mirror/packages \
  --channels stable,long-term --proxy http://127.0.0.1:7890
```

对每种 CPU 架构，它会下载 `all_packages` 压缩包（额外的功能包）**和**主包 `routeros`。
下载和上传过程会显示进度和速度；官方 CDN 断开连接时下载会断点续传；上传先写入临时目录，
再原子地替换线上目录，所以服务器永远不会提供写到一半的包。

注意：
- `--arches` 只指定一部分架构时，会替换整个通道目录，其他架构会被删掉（程序会警告）。
  正式推送请保持默认（全部架构）。
- 在 Git Bash 里运行时要设置 `MSYS_NO_PATHCONV=1`，否则 `/opt/...` 会被改写成
  `C:/Program Files/Git/opt/...`（程序会拒绝这样的路径）。

## 手动配置单台路由器

在每台 RouterOS 7.17+ 设备上：

```
/system/package/local-update/update-package-source
add address=<服务器IP> user=stable       ;# 或者 user=longterm
# 会提示输入密码：输入和用户名相同的词（stable / longterm）
/system/package/local-update/refresh
/system/package/local-update/print          ;# 列出可用的包
/system/package/local-update/download numbers=0,1
/system/reboot                               ;# 重启后安装
```

升级包都是 MikroTik 官方签名的 `.npk` 文件，路由器安装时会校验签名，所以镜像只可能提供真实的官方包。

## 批量升级路由器

`mirror-upgrade` 读取 Winbox 的地址簿 `Addresses.cdb`（里面的登录名和密码是明文保存的），
逐台连接路由器，对每一台：

1. 读取它的更新**通道**，并选择对应的镜像账号（`stable` 或 `long-term`→`longterm`）；
   其他通道（testing、development）镜像里没有，会跳过；
2. 把 `local-update` 的 `update-package-source` 绑定到镜像（已经绑定的跳过），绑定后会读回确认；
3. 刷新，并只下载路由器上**已启用**且状态为 `available` 的包（已经是 `downloaded` 的不会重复下载；
   主包里内置但被禁用的功能包不会下载，否则重启时会被装上）。

最后汇总输出：哪些路由器已经下载了更新包，需要你**手动重启**才会安装。本工具从不重启路由器。

**连接方式。** 默认通过 **Winbox** 协议（就是地址簿里的 8291 端口）：打开一个终端会话（Winbox
处理器 `[76]`），在里面执行 RouterOS 命令，所以不需要额外开启 REST 或 SSH，也适用于 80 端口被
转发到别处的路由器。`--transport rest` 改用 RouterOS REST API（需要开启 `www` / `www-ssl`）。

**交互方式。** 默认每台路由器连接前问一次 `[y/N/q]`（`q` 退出）；回答 `y` 之后自动完成绑定、刷新和下载。
- `--confirm-steps`：绑定和下载之前也逐步询问
- `--yes`：完全不询问，并行处理所有路由器（慎用）
- `--dry-run`：只查看，不绑定也不下载
- `--only 文字`：只处理地址、备注或分组包含这段文字的条目
- `--list`：只列出地址簿（不显示密码）

同一台路由器如果在地址簿里有多个条目（内网 + 公网地址），会通过它的许可证 system-id 识别，只处理一次。
MAC 地址条目和没有保存登录名的条目会跳过。登录被拒绝后不会重试，避免触发锁定。

```
upgrade-routers.bat                              # 双击运行：自动编译，逐台询问
upgrade-routers.bat -DryRun -Only 192.168.1.1   # 只查看，只处理匹配的条目
upgrade-routers.bat -Transport rest              # 改用 REST API
mirror-upgrade.exe --addressbook C:\path\to\Addresses.cdb --mirror 203.0.113.10 --yes   # 不询问
```

使用前修改 `upgrade-routers.ps1` 顶部的设置（地址簿路径、镜像地址）。镜像地址是**路由器**访问镜像
服务器用的地址。

`winbox-capture` 是中间人代理：让 Winbox 连它，它转发给路由器并把解密后的报文记录下来，用来
解码新的 Winbox 操作（`--dump` 可以把记录文件重新输出成文本）。抓包文件里有路由器的配置和登录
密码，请用完删除，不要提交到 git。

## 编译

```
go build -o mikrotik-mirror ./cmd/mikrotik-mirror
go build -o mirror-push     ./cmd/mirror-push
go build -o mirror-upgrade  ./cmd/mirror-upgrade

# 交叉编译静态的 Linux 服务端程序：
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" \
  -o dist/mikrotik-mirror-linux-amd64 ./cmd/mikrotik-mirror
```

`push-mirror.ps1` 和 `upgrade-routers.ps1` 每次运行都会自动重新编译对应的程序（需要安装 Go）。

## 协议说明

本项目逆向了 Winbox 的 `local-update` 协议；线路格式（EC-SRP5 认证、AES 记录层、M2 消息、
处理器 `[72]` 的 LIST/OPEN/READ 命令、包对象字段）记录在
[`research/01-protocol.md`](research/01-protocol.md)。

Winbox 终端（处理器 `[76]`）的解码结果：

| 操作 | 方向 | 内容 |
|------|------|------|
| 打开 | 客户端→路由器 | `to [76]`、`from [0,H]`、命令 `0xa0065`，字段 5=列数 6=行数 7=`vt102` 1=密码 |
| 回复 | 路由器→客户端 | `fe0001`=会话号，状态 2 |
| 数据 | 双向 | 命令 `0xa0067`，`000002`=数据；客户端发出的还带 `000003`=已收到的数据字节累计数（确认） |
| 关闭 | 客户端→路由器 | 命令 `0xa0066`，`fe0001`=会话号 |

`mirror-upgrade` 在终端里执行的每条命令都被包成
`:put ("MKB" . "n"); :onerror e in={ 命令 } do={ :put ("MKERR:" . $e) }; :put ("MKE" . "n")`，
用起止标记从终端输出里截取结果，用 `print terse without-paging` 解析列表。
