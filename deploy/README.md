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

## 3. Keep packages fresh
Either let the server self-sync (remove --no-sync from the unit), or push from a
fast network:
```
go build -o mirror-push ./cmd/mirror-push
./mirror-push --host SERVER --user root \
  --remote-dir /opt/mikrotik-mirror/packages \
  --proxy http://127.0.0.1:7890
```
Automate the push with a cron job / scheduled task on the workstation.
