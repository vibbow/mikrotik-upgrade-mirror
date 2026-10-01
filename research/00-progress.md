# Reverse-engineering progress

## Goal
Implement a RouterOS **local-update package source** on plain Linux so routers that
cannot reach the official HTTP mirror can upgrade over Winbox (TCP 8291).

## Test bench
| role | addr | arch | version | notes |
|------|------|------|---------|-------|
| package source | <bench-router-A> | arm64 | 7.24.4 stable | RB5009 |
| client | <bench-router-B> | smips | 7.20.7 long-term | hAP mini, 32MB RAM |

Test accounts are not recorded here. smips packages (routeros, wireless 7.24.4) uploaded to
bench-router-A via SFTP (REST /tool fetch cannot write .npk — filesystem restriction).

## Confirmed working
- **local-update end to end over Winbox**: bench-router-B added update-package-source ->
  bench-router-A, refresh listed `routeros` + `wireless` 7.24.4 as `status=available`.
  Proves the whole approach is viable.
- **EC-SRP5 + AES session reimplemented from scratch** (`winmirror/wincrypto.py`),
  validated against bench-router-A by `winmirror/test_handshake.py`:
  client handshake, server-confirmation formula, AES-128-CBC record layer,
  chunk reassembly all correct.

## Protocol facts (from vendor PROTOCOL.md + capture)
- Framing: `[len:1][tag:1][payload]`, tag 0x06 encrypted / 0x01 plaintext M2 /
  0xFF continuation; len 0xFF => more chunks follow.
- Encrypted record payload: `[enc_len:2 BE][IV:16][AES-CBC( M2 || HMAC-SHA1(20) || pad )]`.
- M2 TLV: `[key_low][key_high][namespace][type][value]`; namespaces FF=sys FE=session 00=user.
- Handshake: see wincrypto.py docstring. Server sends Wb=b*G - v, salt(16).

## DONE: MITM proxy + refresh decoded
- `winmirror/winserver_auth.py` — server-side EC-SRP5 (formula found offline in
  test_srp_math.py: z = b*(lift(Wa) + j*gpub), gpub = i*G).
- `winmirror/mitm_proxy.py` — full bidirectional decrypt/relay, logs to jsonl.
- Captured a complete `refresh`: request = handler [72] verb 0xfe0004;
  reply = 0xfe0002 msg_array listing every file; client filters packages by its
  own arch. Full field map in `research/01-protocol.md`.

## Next
1. **Capture the download flow** (reboots the client — needs a spare device or
   consent to reboot bench-router-B). Expected: file-store handler [2,2] OPEN/READ.
2. Build standalone server: answer 0xfe0004 from a local .npk directory, serve
   file bytes on download.
3. Package sync from upgrade.mikrotik.com (stable + long-term, all arches).

## Test-device cleanup (2026-10-01)
- bench-router-A: done. Sniffer restored to its original settings and stopped, uploaded smips
  .npk files and lu.pcap removed. The `test` user is still in group full (raised by the
  owner for the tests; reverting it is the owner's call).
- bench-router-B / bench-router-C: not touched. The test login stopped working on both during cleanup,
  so what is left there is for the owner: bench-router-B's update-package-source (-> the mirror)
  and any leftover npk files. bench-router-C's package source was configured by the owner.
- Windows host: proxy stopped; firewall rule attempt did not persist.

## DONE: Go server end-to-end working (2026-10-01)
Pure-Go implementation (`cmd/mikrotik-mirror`, `internal/winbox`, `internal/mirror`)
verified against live bench-router-B:
- Go EC-SRP5 server handshake + AES record layer: crypto cross-checked byte-for-byte
  against validated Python via test vectors (internal/winbox/curve_test.go).
- LIST: client lists packages from the Go server.
- DOWNLOAD: full 7.2 MB routeros npk streamed to the client ("READ complete 7229747").
- Channel-by-username works (user -> directory mapping).

### Key gotchas found
- M2 Parse must handle 0xA0 str_array / 0xA8 msg_array (0xff001c appears in requests);
  stopping on them drops SYS_CMD and the request looks empty.
- **reqid (0xff0006) must be echoed at the SAME width as the request.** The client's
  req-id counter exceeds 255 (observed 312), sent as u32; echoing as u8 truncates it
  and the client silently discards the reply. This was the LIST "empty list" bug.
- package object field order/types must match the real device (see packageObject);
  0xfe0001 object-id is u32 in LIST but the OPEN session id is u8.

### Still to do
1. Package sync from upgrade.mikrotik.com (stable + long-term, all arches, all pkgs
   via all_packages-<arch>-<ver>.zip). URLs verified.
2. Cross-compile Linux binary + systemd unit; deploy to <mirror-server> (8291 open).
3. Cosmetic: client version display showed stale value in one test; confirm with a
   genuinely newer package that download installs on reboot.

## DONE: full project built + deployed (2026-10-01)
- Python prototype and vendored reference libs removed; final project is pure Go.
- `internal/sync`: downloads all_packages-<arch>-<ver>.zip for all arches, extracts
  .npk, keeps latest only. Resumable downloads (HTTP Range) + proxy support (--proxy
  or HTTP(S)_PROXY). MikroTik CDN resets large downloads often, so resume is needed.
- `cmd/mirror-push`: downloads locally (fast link / proxy) and SFTPs to the server
  with atomic swap — for servers whose own link to MikroTik is slow (e.g. a cloud VPS).
- Deployed to <mirror-server>: /opt/mikrotik-mirror, systemd unit.
  deploy/ has the unit file + instructions.
- The server never downloads anything (sync code removed from it); packages are pushed
  with push-mirror.bat / push-mirror.ps1 from the owner's PC.

## VERIFIED end to end (2026-10-01)
- smips hAP mini (bench-router-B) upgraded 7.20.7 -> 7.23.7 (long-term) through the public mirror
  on the public mirror server, account longterm/longterm: LIST, download of the main routeros package
  (7195756 B) and wireless (684177 B), install on reboot.
- x86 CHR (<bench-router-C>): lists x86 packages and flags exactly its installed set
  (container, routeros, user-manager) as installed. Required arch label "i386".
- Router retries a download from scratch when the connection drops; restarting the
  server mid-download only costs a retry.
