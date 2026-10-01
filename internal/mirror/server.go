package mirror

import (
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/local/mikrotik-mirror/internal/winbox"
)

// Handler-[72] verbs (SYS_CMD values).
const (
	verbList = 0xFE0004
	verbOpen = 3
	verbRead = 4
)

const chunkMax = 32768

// Account maps a Winbox username to a channel directory and its password.
type Account struct {
	Username string
	Password string
	Dir      string // directory holding this channel's .npk files
}

type Server struct {
	Listen   string
	Accounts map[string]Account // keyed by username
}

func (s *Server) Run() error {
	ln, err := net.Listen("tcp", s.Listen)
	if err != nil {
		return err
	}
	log.Printf("winbox mirror listening on %s", s.Listen)
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("accept: %v", err)
			continue
		}
		go s.handle(conn)
	}
}

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()
	remote := conn.RemoteAddr()

	// The handshake proves knowledge of one account's password, but the username only
	// arrives inside the client's first message. Peek at it (the bytes are buffered and
	// replayed), pick the account, then run the handshake with that account's password.
	peek := &teeConn{Conn: conn}
	username, err := peekUsername(peek)
	if err != nil {
		log.Printf("[%s] hello read: %v", remote, err)
		return
	}
	acct, ok := s.Accounts[username]
	if !ok {
		log.Printf("[%s] unknown user %q", remote, username)
		return
	}
	peek.replay = true // next reads come from the buffered hello first

	chan_, who, err := winbox.ServerHandshake(peek, acct.Username, acct.Password)
	if err != nil {
		log.Printf("[%s] handshake: %v", remote, err)
		return
	}
	log.Printf("[%s] authenticated %q -> channel dir %s", remote, who, acct.Dir)

	c := &clientConn{conn: conn, chan_: chan_, acct: acct}
	c.serve(remote)
}

type clientConn struct {
	conn     net.Conn
	chan_    *winbox.SecureChannel
	acct     Account
	sessions sync.Map // sid -> *fileSession
	nextSID  uint32
}

type fileSession struct {
	f      *os.File
	size   int64
	offset int64
}

func (c *clientConn) serve(remote net.Addr) {
	var reasm winbox.Reassembler
	buf := make([]byte, 65536)
	for {
		n, err := c.conn.Read(buf)
		if err != nil {
			return
		}
		reasm.Feed(buf[:n])
		for {
			_, assembled, ok := reasm.Next()
			if !ok {
				break
			}
			pt, err := c.chan_.DecryptAssembled(assembled)
			if err != nil {
				log.Printf("decrypt err: %v", err)
				continue
			}
			c.dispatch(pt)
		}
	}
}

func (c *clientConn) dispatch(pt []byte) {
	msg := winbox.Parse(pt)
	cmd, ok := msg[winbox.KeySysCmd]
	if !ok {
		return
	}
	switch cmd.U {
	case verbList:
		c.doList(msg)
	case verbOpen:
		c.doOpen(msg)
	case verbRead:
		c.doRead(msg)
	}
}

func (c *clientConn) send(b []byte) {
	c.conn.Write(c.chan_.Encrypt(b))
}

// replyHead builds a reply envelope (swapped TO/FROM, status ok, echoed reqid).
func replyHead(msg map[uint32]winbox.Value, status byte) *winbox.M2Builder {
	to := arr(msg[winbox.KeySysFrom], []uint32{72, 2})
	from := arr(msg[winbox.KeySysTo], []uint32{72})
	b := winbox.NewM2().
		U32Array(0x01, 0, winbox.NsSys, to).
		U32Array(0x02, 0, winbox.NsSys, from).
		U8(0x03, 0, winbox.NsSys, status)
	echoReqID(b, msg)
	return b
}

func arr(v winbox.Value, def []uint32) []uint32 {
	if v.Arr != nil {
		return v.Arr
	}
	return def
}

func (c *clientConn) doList(msg map[uint32]winbox.Value) {
	pkgs, err := LoadDir(c.acct.Dir)
	if err != nil {
		log.Printf("list %s: %v", c.acct.Dir, err)
		pkgs = nil
	}
	subs := make([][]byte, 0, len(pkgs))
	for i, p := range pkgs {
		subs = append(subs, packageObject(p, uint32(520159587-i)))
	}
	// Match the real source's response layout: TO, FROM, empty 0xff001c str_array,
	// the msg_array of objects, then status + echoed reqid.
	to := arr(msg[winbox.KeySysFrom], []uint32{72, 2})
	from := arr(msg[winbox.KeySysTo], []uint32{72})
	b := winbox.NewM2().
		U32Array(0x01, 0, winbox.NsSys, to).
		U32Array(0x02, 0, winbox.NsSys, from).
		StrArray(0x1C, 0, winbox.NsSys, nil).
		MsgArray(0x02, 0, winbox.NsSes, subs).
		U8(0x03, 0, winbox.NsSys, 2)
	echoReqID(b, msg)
	bb := b.Build()
	c.send(bb)
	log.Printf("LIST %s -> %d packages", c.acct.Username, len(subs))
	return
}

// echoReqID appends 0xff0006 using the same width (u8/u32) the request used, so a
// client whose request-id counter exceeds 255 still matches the reply.
func echoReqID(b *winbox.M2Builder, msg map[uint32]winbox.Value) {
	v := msg[winbox.KeySysReqID]
	if v.Type == 0x08 {
		b.U32(0x06, 0, winbox.NsSys, uint32(v.U))
	} else {
		b.U8(0x06, 0, winbox.NsSys, byte(v.U))
	}
}

// packageObject matches the exact field order/types a real RouterOS source emits
// (decoded from a live capture — see research/01-protocol.md):
//
//	0x04 u32 mtime | 0x05 u32 parent(-1) | 0x66 u32 buildid | 0x0f u8 1 |
//	0x67 u32 const | 0xfe0001 u32 objid | 0x02 u32 size | 0x03 u8 1(package) |
//	0x6b arch | 0x65 version | 0x64 name | 0x0e u64 size | 0x07 "package" |
//	0x18 "" | 0x06 fname | 0x01 fname | 0xfe0010 fname
//
// 0x67 is the numeric version (see VersionCode): the router displays THIS, not the
// 0x65 string. Hardcoding the value seen in a 7.24.4 capture made every package show
// as 7.24.4. 0x66 is the build time; the file's mtime stands in for it.
func packageObject(p Package, objID uint32) []byte {
	versionCode := VersionCode(p.Version)
	buildID := uint32(p.MTime)
	return winbox.NewM2().
		U32(0x04, 0, winbox.NsUsr, uint32(p.MTime)).
		U32(0x05, 0, winbox.NsUsr, 0xFFFFFFFF).
		U32(0x66, 0, winbox.NsUsr, buildID).
		U8(0x0F, 0, winbox.NsUsr, 1).
		U32(0x67, 0, winbox.NsUsr, versionCode).
		U32(0x01, 0, winbox.NsSes, objID). // 0xfe0001 object id (u32)
		U32(0x02, 0, winbox.NsUsr, uint32(p.Size)).
		U8(0x03, 0, winbox.NsUsr, 1). // type code 1 = package
		String(0x6B, 0, winbox.NsUsr, p.Arch).
		String(0x65, 0, winbox.NsUsr, p.Version).
		String(0x64, 0, winbox.NsUsr, p.Name).
		U64(0x0E, 0, winbox.NsUsr, uint64(p.Size)).
		String(0x07, 0, winbox.NsUsr, "package").
		String(0x18, 0, winbox.NsUsr, "").
		String(0x06, 0, winbox.NsUsr, p.Fname).
		String(0x01, 0, winbox.NsUsr, p.Fname).
		String(0x10, 0, winbox.NsSes, p.Fname). // 0xfe0010 std name
		Build()
}

func (c *clientConn) doOpen(msg map[uint32]winbox.Value) {
	name := msg[winbox.KeyName].S
	path := filepath.Join(c.acct.Dir, filepath.Base(name))
	f, err := os.Open(path)
	if err != nil {
		log.Printf("OPEN miss %s: %v", name, err)
		c.send(replyHead(msg, 0).Build()) // status 0 = not found
		return
	}
	info, _ := f.Stat()
	sid := atomicNextSID(c)
	c.sessions.Store(sid, &fileSession{f: f, size: info.Size()})
	b := replyHead(msg, 2).
		U8(0x01, 0, winbox.NsSes, byte(sid)). // 0xfe0001 session id
		U32(0x02, 0, winbox.NsUsr, uint32(info.Size())).
		Build()
	c.send(b)
	log.Printf("OPEN %s -> sid=%d size=%d", name, sid, info.Size())
}

func (c *clientConn) doRead(msg map[uint32]winbox.Value) {
	sid := uint32(msg[winbox.KeySesID].U)
	want := int(msg[winbox.KeyData].U)
	if want <= 0 || want > chunkMax {
		want = chunkMax
	}
	v, ok := c.sessions.Load(sid)
	if !ok {
		return
	}
	sess := v.(*fileSession)
	data := make([]byte, want)
	sess.f.Seek(sess.offset, 0)
	n, _ := sess.f.Read(data)
	data = data[:n]
	sess.offset += int64(n)
	last := sess.offset >= sess.size

	rep := replyHead(msg, 2).Raw(0x05, 0, winbox.NsUsr, data)
	if last {
		rep.Bool(0x06, 0, winbox.NsUsr, true)
	}
	c.send(rep.Build())
	if last {
		sess.f.Close()
		c.sessions.Delete(sid)
		log.Printf("READ %s complete (%d bytes)", c.acct.Username, sess.size)
	}
}

var sidMu sync.Mutex

func atomicNextSID(c *clientConn) uint32 {
	sidMu.Lock()
	defer sidMu.Unlock()
	c.nextSID++
	return c.nextSID
}

func (s *Server) Describe() string {
	out := ""
	for u, a := range s.Accounts {
		pkgs, _ := LoadDir(a.Dir)
		out += fmt.Sprintf("  user %-10s -> %s (%d pkgs)\n", u, a.Dir, len(pkgs))
	}
	return out
}
