# mikrotik-mirror

[中文](README.md) | **English**

A self-hosted **RouterOS `local-update` package source** that runs on plain Linux.

RouterOS 7.17+ can upgrade from another device over the Winbox protocol
(`/system/package/local-update`). Normally that other device must itself be a
RouterOS box. This project reimplements the source side of that protocol in Go, so
an ordinary Linux server can act as the package source — useful where the official
HTTP mirror (`upgrade.mikrotik.com`) is slow or unreachable.

Four programs:

| program | runs where | does what |
|---------|-----------|-----------|
| `mikrotik-mirror` | the mirror server | serves packages to routers over Winbox 8291. It never downloads anything itself |
| `mirror-push` | your workstation | downloads packages locally (fast link / proxy) and pushes them to the server over SFTP |
| `mirror-upgrade` | your workstation | reads the Winbox address book, connects to each router, binds the mirror and downloads updates |
| `winbox-capture` | your workstation | Winbox man-in-the-middle capture proxy, only for protocol research |

**Updating the mirror:** double-click `scripts/push-mirror.bat` (or run `scripts/push-mirror.ps1`). It
downloads the latest stable + long-term packages on your machine and uploads them.

**Upgrading routers:** double-click `scripts/upgrade-routers.bat`, see "Bulk-upgrade routers" below.

## How clients pick a channel

The Winbox `local-update` protocol has **no channel field** — a source just offers
files and the router lists them all. To keep `stable` and `long-term` clients from
seeing each other's versions, the server **selects the channel by Winbox username**:

- a client configured with user `stable` is served `packages/stable/`
- a client configured with user `longterm` is served `packages/long-term/`

Each account's password defaults to its **username**: `stable` / `stable` and
`longterm` / `longterm`. (Override with `--stable-password` / `--longterm-password`
if you want something else.) The packages are public and signed, so the password only
gates who can use your mirror.

A server cannot accept an arbitrary password: Winbox's EC-SRP5 handshake derives its
session keys from the password, so a router must enter exactly the account's password.
A wrong password shows up in the server log as "client confirmation mismatch".

## Server usage

```
mikrotik-mirror \
  --listen 0.0.0.0:8291 \
  --stable-dir  /opt/mikrotik-mirror/packages/stable \
  --longterm-dir /opt/mikrotik-mirror/packages/long-term
```

It serves whatever is in those two directories (re-read on every request, so a push
takes effect immediately, no restart). Only the **latest** version per channel is kept.
Deployment: see [`deploy/README.md`](deploy/README.md).

## Pushing packages

`scripts/push-mirror.bat` / `scripts/push-mirror.ps1` wraps `mirror-push`. First put your
server, user, remote dir and proxy in `scripts/config.ps1` (copy `scripts/config.example.ps1`), then:

```
scripts/push-mirror.bat              # both channels; skips a channel that is already current
scripts/push-mirror.bat -Force       # re-download and re-upload anyway
scripts/push-mirror.bat -Channels stable
```

Or call the tool directly:

```
mirror-push --host your-server --user root --remote-dir /opt/mikrotik-mirror/packages \
  --channels stable,long-term --proxy http://127.0.0.1:7890
```

For each architecture it fetches the `all_packages` zip (extra packages) **and** the
main `routeros` package. Downloads and uploads show progress and speed; downloads resume
if the CDN drops the connection; uploads go to a temporary directory and are swapped into
place atomically, so the server never serves a half-written directory.

Notes:
- `--arches` with a subset replaces the whole channel directory, dropping the other
  architectures (the tool warns). Leave it at the default for real pushes.
- Running from Git Bash: set `MSYS_NO_PATHCONV=1`, otherwise `/opt/...` is rewritten to
  `C:/Program Files/Git/opt/...` (the tool refuses such a path).

## Configure a single router by hand

On each RouterOS 7.17+ device:

```
/system/package/local-update/update-package-source
add address=<server-ip> user=stable       ;# or user=longterm
# it prompts for the password: type the same word as the user (stable / longterm)
/system/package/local-update/refresh
/system/package/local-update/print          ;# lists available packages
/system/package/local-update/download numbers=0,1
/system/reboot                               ;# installs on reboot
```

Packages are genuine signed MikroTik `.npk` files; the router verifies the
signature on install, so the mirror can only ever serve authentic packages.

## Bulk-upgrade routers

`mirror-upgrade` reads Winbox's address book `Addresses.cdb` (logins and passwords are
stored in clear text there), connects to each router and, per router:

1. reads its update **channel** and picks the mirror account (`stable` or `long-term`→`longterm`);
   other channels (testing, development) are not mirrored and are skipped;
2. binds `local-update` `update-package-source` to the mirror with that account (skipped if
   already bound), then reads it back to confirm;
3. refreshes, and downloads only packages that are **enabled** on the router and reported as
   `available` (ones already `downloaded` are not fetched again; extra packages that are
   bundled in the main package but disabled are not downloaded, or they would be installed on reboot).

At the end it prints which routers downloaded packages and need a **manual reboot** to install
them. The tool never reboots a router.

**Transport.** By default it talks **Winbox** (the address book's own port, 8291): it opens a
terminal session (Winbox handler `[76]`) and runs RouterOS commands in it, so no REST or SSH
service is needed and routers whose port 80 is forwarded elsewhere work. `--transport rest`
uses the RouterOS REST API instead (needs `www` / `www-ssl`).

**Interaction.** By default it asks `[y/N/q]` once per router before connecting (`q` quits);
after a `y` it binds, refreshes and downloads by itself.
- `--confirm-steps`: also ask before binding and before downloading
- `--yes`: no questions, all routers in parallel (use with care)
- `--dry-run`: look only, never bind or download
- `--only text`: only entries whose address, note or group contains the text
- `--list`: list the address book (no passwords) and exit
- `--ignore-file file` / `--ignore addr1,addr2`: ignore list; these addresses are skipped without connecting

A router listed under several entries (LAN + public address) is recognised by its licence
system-id and handled once. Entries with a MAC address or no saved login are skipped. A
rejected login is never retried, to avoid triggering a lockout.

```
scripts/upgrade-routers.bat                              # double-click: builds, asks per router
scripts/upgrade-routers.bat -DryRun -Only 192.168.1.1   # look only, matching entries
scripts/upgrade-routers.bat -Transport rest              # use the REST API instead
mirror-upgrade.exe --addressbook C:\path\to\Addresses.cdb --mirror 203.0.113.10 --yes   # no questions
```

**Local config:** private settings (server, proxy, address book path, mirror address) live in
`scripts/config.ps1`, which is git-ignored. First use: copy `scripts/config.example.ps1` to
`scripts/config.ps1` and fill in your own values; both `push-mirror.ps1` and `upgrade-routers.ps1` read it.
The mirror address is the one the **routers** use to reach the mirror server.

For an ignore list, copy `scripts/ignore.example.txt` to `scripts/ignore.txt` (git-ignored); the
launcher uses it automatically when it exists.

`winbox-capture` is a man-in-the-middle proxy: point Winbox at it, it relays to the router and
records the decrypted messages, which is how new Winbox operations get decoded (`--dump`
prints a capture file as text). Capture files contain router configuration and the login
password: delete them when done and never commit them.

## Build

```
go build -o mikrotik-mirror ./cmd/mikrotik-mirror
go build -o mirror-push     ./cmd/mirror-push
go build -o mirror-upgrade  ./cmd/mirror-upgrade

# cross-compile a static Linux server binary:
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" \
  -o dist/mikrotik-mirror-linux-amd64 ./cmd/mikrotik-mirror
```

`scripts/push-mirror.ps1` and `scripts/upgrade-routers.ps1` rebuild their program on every run (Go required).

## Protocol notes

The Winbox `local-update` protocol was reverse-engineered for this project; the
wire format (EC-SRP5 auth, AES record layer, M2 messages, the handler-[72]
LIST/OPEN/READ verbs, and the package-object fields) is documented in
[`research/01-protocol.md`](research/01-protocol.md).

The Winbox terminal (handler `[76]`), as decoded:

| step | direction | content |
|------|-----------|---------|
| open | client→router | `to [76]`, `from [0,H]`, cmd `0xa0065`, fields 5=cols 6=rows 7=`vt102` 1=password |
| reply | router→client | `fe0001`=session id, status 2 |
| data | both | cmd `0xa0067`, `000002`=bytes; the client's messages also carry `000003`=cumulative count of data bytes received (the ack) |
| close | client→router | cmd `0xa0066`, `fe0001`=session id |

Every command `mirror-upgrade` runs in the terminal is wrapped as
`:put ("MKB" . "n"); :onerror e in={ CMD } do={ :put ("MKERR:" . $e) }; :put ("MKE" . "n")`
so the result can be cut out of the terminal output by the markers; lists are read with
`print terse without-paging`.
