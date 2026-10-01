# local-update protocol (decoded)

Captured end-to-end via the MITM proxy on a real `refresh` (bench-router-B smips client ->
proxy -> bench-router-A source). Both messages decrypted; see `lu-session.jsonl`.

## The exchange is Winbox object-model file browsing

local-update does **not** have a bespoke package protocol. The client asks the
source's file subsystem to enumerate its files, then **picks the packages itself**
by matching `type == package` and `arch == own arch`.

### Request  (client -> source), handler [72]
```
SYS_TO    0xff0001 = u32_array [72]
SYS_FROM  0xff0002 = u32_array [72, 2]
0xff001c            = str_array (empty)      # "msg-proxy-<ver>" tag seen in a prior frame
SYS_REQUEST 0xff0005 = bool true
0xff0003           = u8 1                    # request marker
SYS_REQID 0xff0006 = u8 <id>
SYS_CMD   0xff0007 = u32 0x00FE0004          # verb 0xfe0004 = DETAIL / enumerate
```
(An earlier capture missing SYS_CMD got no reply — SYS_CMD is required.)

### Reply  (source -> client)
```
SYS_FROM  0xff0002 = [72]
SYS_TO    0xff0001 = [72, 2]
0xff0003           = u8 2                    # status = ok
0xfe0002           = msg_array[ N objects ]  # one M2 submessage per file/dir
```

### Per-object fields (M2 submessage inside 0xfe0002)
| key | type | meaning | example |
|-----|------|---------|---------|
| 0x01 | string | full path | `routeros-7.24.4-smips.npk`, `disk1/backup/x.rsc` |
| 0x06 | string | base name | `routeros-7.24.4-smips.npk` |
| 0x07 | string | type label | `package`, `directory`, `file`, `script`, `disk` |
| 0x03 | u8 | type code | 1=package, 3=script, 5=dir, 0=file, 9=container store |
| 0x02 | u32 | size | 7196348 |
| 0x0e | u64 | size (64-bit) | 7196348 |
| 0x04 | u32 | timestamp | unix-ish |
| 0x64 | string | **package name** | `system`, `wireless`, `container`, `rose-storage` |
| 0x65 | string | **version** | `7.24.4` |
| 0x6b | string | **architecture** | `smips`, `arm64` |
| 0x66 | u32 | package build time (unix) | 1789558341 |
| 0x67 | u32 | **numeric version — what the router displays** | 119039492 = 0x07186604 = 7.24.4 |

`0x67 = major<<24 | minor<<16 | 0x66<<8 | patch`. The router shows this, NOT the 0x65
string: a server that hardcoded the 7.24.4 value displayed every package as 7.24.4
(and the router then named the local file for 7.24.4 while downloading the file the
server named). Our first notes called it a constant only because every capture was 7.24.4.

`0x6b` architecture must equal the label inside the .npk header, which is what the
router matches against its own architecture: smips, arm64, ... and **`i386` for x86**
(x86 files have no arch suffix in their file name; CHR reports architecture-name x86_64
but matches `i386`). With any other label the router lists nothing for that arch.
| 0xfe0001 | u32 | object id | |
| 0xfe0010 | string | std name (= path) | |

Note `routeros` package reports `0x64 = "system"` (not "routeros").

### Client-side selection (observed)
From the 17 objects returned, the smips client marked exactly the two whose
`0x07==package` and `0x6b==smips` as `status=available`
(`routeros-7.24.4-smips.npk`, `wireless-7.24.4-smips.npk`). It ignored the arm64
packages and all non-package files. So the **server may simply advertise every
.npk it holds**; the client filters by its own arch.

## Implication for the server
To be a package source we must, over Winbox on 8291:
1. Accept EC-SRP5 auth as the server (done: `winserver_auth.py`).
2. Answer handler-[72] verb 0xfe0004 with an `0xfe0002` msg_array describing each
   `.npk` we hold (name/path/size/type=package/version/arch + ids/timestamps).
3. Serve the actual file bytes on download — expected to be the file-store
   handler `[2,2]` OPEN/READ flow (same as winbox-file-client.download). **Not yet
   captured** — needs a real download (which reboots the client), so capture it
   against a spare device or accept the reboot on bench-router-B.

## Download flow (decoded) — ALL on handler [72]

local-update serves files itself on handler [72]; it does NOT use file-store [2,2].
Captured downloading `wireless-7.24.4-smips.npk` (675985 bytes) in `lu-download.jsonl`.

SYS_CMD (0xff0007) carries the verb. Three verbs total:

| verb | name | request fields | reply fields |
|------|------|----------------|--------------|
| 0xfe0004 | LIST | (none) | 0xfe0002 = msg_array of file objects |
| 3 | OPEN | 0x1 = filename (string) | 0xfe0001 = session id, 0x2 = total size (u32) |
| 4 | READ | 0xfe0001 = session, 0x2 = chunk size (u32, 32768) | 0x5 = data (raw), 0x6 on last |

Common envelope on every message:
- request:  0xff0001 SYS_TO=[72] (or [72,sess]) ; 0xff0002 SYS_FROM=[72,2] ;
            0xff0005 REQUEST=true ; 0xff0003=1 ; 0xff0006 REQID=u8 (increments) ;
            0xff0007 SYS_CMD=verb
- reply:    0xff0001/0xff0002 swapped ; 0xff0003=2 (ok) ; 0xff0006 echoes REQID

Observed OPEN: client SYS_TO=[72] then subsequent messages SYS_TO=[72,1] where 1
is... actually the session index the server assigned (0xfe0001=1). READ loop:
client asks 0x2=32768 each time; server returns 32768 bytes of data in 0x5 until
the final short chunk (#46: len 20699, carries 0x6). Total 20 reads for 676 KB.

### Server must implement (handler [72])
1. LIST (0xfe0004) -> advertise each .npk as a package object (fields in table above).
2. OPEN (3) -> allocate a session id, return file size.
3. READ (4) -> return up to the requested chunk size from the file offset, mark
   last chunk with 0x6.
Nothing else is needed for refresh + download.

## Still cosmetic/unknown (safe to stub)
- 0xff001c "msg-proxy-<ver>" str_array — appears empty on wire; likely optional.
- 0x04 timestamps / 0xfe0001 object ids — plausible values are enough.
  (0x66 is filled from the file mtime; installed/available detection still matched
  the router's real package set on both smips and x86 devices.)
- After download the client needs a reboot to install; .npk signature is checked
  by the client, so the mirror can only serve genuine MikroTik packages.

## Channel handling (tested)
The protocol has **no channel field**. The client sends only LIST; the server
returns every file; the client shows all versions and lets the user pick.

Test: put both 7.24.4 (stable) and 7.20.7 (long-term) smips packages on one source,
refreshed from a 7.20.7 client. Result — client listed BOTH and tagged by state:
```
wireless 7.20.7 installed      routeros 7.20.7 installed
wireless 7.24.4 available      routeros 7.24.4 available
```
So versions do not "merge", but the client is channel-unaware: the user must pick
the right version by hand, and nothing stops a long-term box from grabbing stable.

**Decision: the server selects the channel by Winbox username.** It reads the
authenticated username in the handshake and serves only that channel's directory:
e.g. user `stable` -> packages/stable/, user `longterm` -> packages/long-term/.
Each client's update-package-source is configured with the matching username.
This keeps each client's listing to a single clean channel.
