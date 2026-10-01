# mikrotik-mirror

A self-hosted **RouterOS `local-update` package source** that runs on plain Linux.

RouterOS 7.17+ can upgrade from another device over the Winbox protocol
(`/system/package/local-update`). Normally that other device must itself be a
RouterOS box. This project reimplements the source side of that protocol in Go, so
an ordinary Linux server can act as the package source — useful where the official
HTTP mirror (`upgrade.mikrotik.com`) is slow or unreachable.

Two binaries:

| binary | runs where | does what |
|--------|-----------|-----------|
| `mikrotik-mirror` | the mirror server | serves packages to routers over Winbox 8291. It never downloads anything itself |
| `mirror-push` | your workstation | downloads packages locally (fast link / proxy) and pushes them to the server over SFTP |

**Updating the mirror:** double-click `push-mirror.bat` (or run `push-mirror.ps1`). It
downloads the latest stable + long-term packages on your machine and uploads them.

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

## Pushing packages

`push-mirror.bat` / `push-mirror.ps1` wraps `mirror-push`. Edit the settings block at the
top of `push-mirror.ps1` (server, user, remote dir, proxy), then:

```
push-mirror.bat              # both channels; skips a channel that is already current
push-mirror.bat -Force       # re-download and re-upload anyway
push-mirror.bat -Channels stable
```

Or call the tool directly:

```
mirror-push --host your-server --user root --remote-dir /opt/mikrotik-mirror/packages \
  --channels stable,long-term --proxy http://127.0.0.1:7890
```

For each architecture it fetches the `all_packages` zip (extra packages) **and** the
main `routeros` package. Downloads resume if the CDN drops the connection; uploads are
swapped into place atomically, so the server never serves a half-written directory.

Notes:
- `--arches` with a subset replaces the whole channel directory, dropping the other
  architectures (the tool warns). Leave it at the default for real pushes.
- Running from Git Bash: set `MSYS_NO_PATHCONV=1`, otherwise `/opt/...` is rewritten to
  `C:/Program Files/Git/opt/...` (the tool refuses such a path).

## Configure a router (client)

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

## Bulk-configure routers from the Winbox address book

`mirror-upgrade` reads Winbox's `Addresses.cdb`, logs in to each router (Winbox terminal, or the RouterOS
REST API with `--transport rest`), and per router:

1. reads its update **channel** and picks the mirror account (`stable` or `long-term`->`longterm`);
   other channels (testing, development) are skipped;
2. binds `local-update` `update-package-source` to the mirror with that account;
3. refreshes, and downloads only packages the router reports as `available` (already
   `downloaded` ones are not fetched again).

**Transport.** By default `mirror-upgrade` talks **Winbox** (the address book's own port, 8291): it opens a
terminal session (Winbox handler `[76]`) and runs RouterOS CLI commands, so no REST/SSH service is needed and
routers whose web port is forwarded elsewhere work. `--transport rest` uses the REST API instead
(needs `www`/`www-ssl`). `winbox-capture` is the man-in-the-middle used to decode the terminal
messages (see `research/`); it is only needed to extend the protocol support.

It is interactive: it asks once per router whether to connect; after a yes it binds the mirror,
refreshes and downloads by itself (`--confirm-steps` also asks before bind/download). `--yes` skips all questions, `--dry-run` only looks. At the end it lists the routers that need a manual reboot; it never reboots. A router listed under
several entries (LAN + public address) is recognised by its licence system-id and handled once.
Entries with a MAC address or no saved login are skipped. A rejected login is never retried.

```
upgrade-routers.bat                       # double-click: builds mirror-upgrade.exe, asks before each step
upgrade-routers.bat -DryRun -Only 192.168.1.1   # look only, matching entries
mirror-upgrade.exe --addressbook C:\path\to\Addresses.cdb --mirror 203.0.113.10 --yes   # no questions
```

Edit the settings block at the top of `upgrade-routers.ps1` (address book path, mirror address).

## Build

```
go build -o mikrotik-mirror ./cmd/mikrotik-mirror
go build -o mirror-push     ./cmd/mirror-push

# cross-compile a static Linux server binary:
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" \
  -o dist/mikrotik-mirror-linux-amd64 ./cmd/mikrotik-mirror
```

## Protocol notes

The Winbox `local-update` protocol was reverse-engineered for this project; the
wire format (EC-SRP5 auth, AES record layer, M2 messages, the handler-[72]
LIST/OPEN/READ verbs, and the package-object fields) is documented in
[`research/01-protocol.md`](research/01-protocol.md).
