package winbox

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"
)

// The Winbox terminal (handler [76]) carries a RouterOS CLI session over the same
// authenticated connection. Decoded from a capture of Winbox 4:
//
//	open   C->R  to [76] from [0,H] req=true  5=cols 6=rows 8=0 reqid cmd=0xa0065
//	             0b={1:0} 7="vt102" 1=<password>
//	reply  R->C  fe0001=<session id> status=2
//	data   both  fe0001=<session> cmd=0xa0067  2=<bytes>  (C->R also 3=<ack>)
//	close  C->R  fe0001=<session> cmd=0xa0066
//
// 3 is the cumulative number of data bytes the sender has received; the client
// acknowledges every batch of router output.
const (
	termHandler  = 76
	cmdTermOpen  = 0xa0065
	cmdTermClose = 0xa0066
	cmdTermData  = 0xa0067
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

// Terminal is one open CLI session on a router.
type Terminal struct {
	conn    net.Conn
	ch      *SecureChannel
	re      Reassembler
	handle  uint32
	session Value
	recvd   uint32
	reqID   byte
	buf     []byte // router output not yet consumed
}

// OpenTerminal connects to a router's Winbox port, authenticates and opens a terminal.
func OpenTerminal(addr, user, password string, timeout time.Duration) (*Terminal, error) {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	conn.SetDeadline(time.Now().Add(timeout))
	ch, err := ClientHandshake(conn, user, password)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("winbox login: %w", err)
	}
	t := &Terminal{conn: conn, ch: ch, handle: 100}
	t.reqID++
	open := NewM2().
		U32Array(1, 0, 0xff, []uint32{termHandler}).
		U32Array(2, 0, 0xff, []uint32{0, t.handle}).
		Bool(5, 0, 0xff, true).
		U8(5, 0, 0, 255).U8(6, 0, 0, 24).U8(8, 0, 0, 0).
		U8(6, 0, 0xff, t.reqID).
		U32(7, 0, 0xff, cmdTermOpen).
		Msg(0x0b, 0, 0, NewM2().U8(1, 0, 0, 0).Build()).
		String(7, 0, 0, "vt102").
		String(1, 0, 0, password).Build()
	if _, err := conn.Write(t.ch.Encrypt(open)); err != nil {
		conn.Close()
		return nil, err
	}
	for {
		m, err := t.readMsg()
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("open terminal: %w", err)
		}
		if has(m[0xff0002].Arr, termHandler) && m[0xff0003].U == 2 {
			sv, ok := m[0xfe0001]
			if !ok {
				conn.Close()
				return nil, errors.New("open terminal: reply has no session id")
			}
			t.session = sv
			break
		}
	}
	// let the banner and first prompt arrive so typed commands are not lost
	t.waitPrompt(5 * time.Second)
	t.buf = nil
	return t, nil
}

func has(a []uint32, v uint32) bool {
	for _, x := range a {
		if x == v {
			return true
		}
	}
	return false
}

func (t *Terminal) readMsg() (map[uint32]Value, error) {
	tmp := make([]byte, 65536)
	for {
		if _, rec, ok := t.re.Next(); ok {
			msg, err := t.ch.DecryptAssembled(rec)
			if err != nil {
				return nil, err
			}
			return Parse(msg), nil
		}
		n, err := t.conn.Read(tmp)
		if n > 0 {
			t.re.Feed(tmp[:n])
		}
		if err != nil {
			return nil, err
		}
	}
}

func (t *Terminal) sessionField(b *M2Builder) *M2Builder {
	if t.session.Type == 0x09 {
		return b.U8(1, 0, 0xfe, byte(t.session.U))
	}
	return b.U32(1, 0, 0xfe, uint32(t.session.U))
}

func (t *Terminal) sendData(data []byte) error {
	b := NewM2().
		U32Array(1, 0, 0xff, []uint32{termHandler}).
		U32Array(2, 0, 0xff, []uint32{0, t.handle}).
		U32(3, 0, 0, t.recvd)
	t.sessionField(b).U32(7, 0, 0xff, cmdTermData)
	if len(data) > 0 {
		b.Raw(2, 0, 0, data)
	}
	_, err := t.conn.Write(t.ch.Encrypt(b.Build()))
	return err
}

// pump reads one message; terminal output is appended to buf and acknowledged.
func (t *Terminal) pump() error {
	m, err := t.readMsg()
	if err != nil {
		return err
	}
	if m[0xff0007].U == cmdTermData && has(m[0xff0002].Arr, termHandler) {
		d := m[0x000002].B
		t.buf = append(t.buf, d...)
		t.recvd += uint32(len(d))
		return t.sendData(nil)
	}
	return nil
}

func clean(b []byte) string {
	s := ansiRe.ReplaceAllString(string(b), "")
	return strings.ReplaceAll(s, "\r", "")
}

func (t *Terminal) waitPrompt(d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		t.conn.SetReadDeadline(deadline)
		if err := t.pump(); err != nil {
			return
		}
		if strings.HasSuffix(strings.TrimRight(clean(t.buf), " "), "] >") {
			return
		}
	}
}

// Run executes one CLI command and returns its output. A CLI error becomes a Go error.
// Do not put newlines in cmd.
func (t *Terminal) Run(cmd string, timeout time.Duration) (string, error) {
	nb := make([]byte, 4)
	rand.Read(nb)
	n := hex.EncodeToString(nb)
	// the markers are split in the typed text so the echo never contains them verbatim
	line := fmt.Sprintf(`:put ("MKB" . "%s"); :onerror e in={ %s } do={ :put ("MKERR:" . $e) }; :put ("MKE" . "%s")`, n, cmd, n)
	t.buf = nil
	deadline := time.Now().Add(timeout)
	t.conn.SetDeadline(deadline)
	if err := t.sendData([]byte(line + "\r")); err != nil {
		return "", err
	}
	begin, end := "MKB"+n, "MKE"+n
	for {
		if err := t.pump(); err != nil {
			return "", fmt.Errorf("terminal: %w", err)
		}
		text := clean(t.buf)
		bi := strings.Index(text, begin)
		if bi < 0 {
			continue
		}
		rest := text[bi+len(begin):]
		ei := strings.Index(rest, end)
		if ei < 0 {
			continue
		}
		out := strings.Trim(rest[:ei], "\n ")
		if strings.HasPrefix(out, "MKERR:") || strings.Contains(out, "\nMKERR:") {
			i := strings.Index(out, "MKERR:")
			return "", errors.New(strings.TrimSpace(out[i+len("MKERR:"):]))
		}
		return out, nil
	}
}

// Close ends the terminal session and the connection.
func (t *Terminal) Close() {
	b := NewM2().
		U32Array(1, 0, 0xff, []uint32{termHandler}).
		U32Array(2, 0, 0xff, []uint32{0, t.handle})
	t.sessionField(b).U32(7, 0, 0xff, cmdTermClose)
	t.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	t.conn.Write(t.ch.Encrypt(b.Build()))
	t.conn.Close()
}
