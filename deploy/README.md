# Deployment

## 1. Build the Linux server binary (on your workstation)
```
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" \
  -o dist/mikrotik-mirror-linux-amd64 ./cmd/mikrotik-mirror
```

## 2. Install on the server
```
ssh root@SERVER 'mkdir -p /opt/mikrotik-mirror/packages'
scp dist/mikrotik-mirror-linux-amd64 root@SERVER:/opt/mikrotik-mirror/mikrotik-mirror
ssh root@SERVER 'chmod +x /opt/mikrotik-mirror/mikrotik-mirror'
scp deploy/mikrotik-mirror.service root@SERVER:/etc/systemd/system/
# accounts are stable/stable and longterm/longterm by default; then:
ssh root@SERVER 'systemctl daemon-reload && systemctl enable --now mikrotik-mirror'
```
Open TCP 8291 to the routers that will use it.

## 3. Put packages on the server
The server does not download anything. From your workstation, edit the settings at the
top of `push-mirror.ps1` (server, proxy) and double-click `push-mirror.bat`. The first
run downloads ~440 MB (both channels, all architectures) and takes a while through a
proxy; later runs skip a channel that is already at the latest version.

## Updating the server binary
```
scp dist/mikrotik-mirror-linux-amd64 root@SERVER:/opt/mikrotik-mirror/mikrotik-mirror.new
ssh root@SERVER 'chmod +x /opt/mikrotik-mirror/mikrotik-mirror.new && \
  mv /opt/mikrotik-mirror/mikrotik-mirror.new /opt/mikrotik-mirror/mikrotik-mirror && \
  systemctl restart mikrotik-mirror'
```
Do not forget the `chmod +x`: a copied binary without it makes the unit fail with
"Permission denied". Restarting interrupts any download in progress (the router retries).
