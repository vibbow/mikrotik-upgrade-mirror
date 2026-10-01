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
| `mikrotik-mirror` | the mirror server | serves packages to routers over Winbox 8291; optionally self-syncs from MikroTik |
| `mirror-push` | your workstation | downloads packages locally (fast link) and pushes them to the server over SFTP |

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
  --longterm-dir /opt/mikrotik-mirror/packages/long-term \
  [--no-sync | --sync-interval 6h] \
  [--proxy http://host:port] \
  [--arches arm,arm64,mipsbe,mmips,smips,tile,ppc,x86]
```

- With self-sync (default), it downloads the latest stable + long-term packages
  for every architecture from MikroTik on startup and every `--sync-interval`.
- With `--no-sync`, it only serves whatever is already in the package dirs — use
  this when you push packages with `mirror-push` instead.
- Keeps only the **latest** version per channel.

## Push from a fast network

If the server's link to MikroTik is slow, download on a machine that has a fast
link (optionally through a proxy) and push over SSH:

```
mirror-push \
  --host your-server --user root \
  --remote-dir /opt/mikrotik-mirror/packages \
  --channels stable,long-term \
  --proxy http://127.0.0.1:7890
```

Downloads resume automatically if the CDN drops the connection. Uploads are swapped
into place atomically, so the server never serves a half-written directory. Run the
server with `--no-sync` when you use push.

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
