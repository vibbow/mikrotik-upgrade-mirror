package mirror

import (
	"errors"
	"io"
	"net"
)

// teeConn buffers the initial bytes read from the connection so that, after we
// extract the username from the EC-SRP5 hello, those same bytes can be replayed
// into winbox.ServerHandshake (which expects to read the hello itself).
type teeConn struct {
	net.Conn
	captured []byte
	replay   bool
	rpos     int
}

func (t *teeConn) Read(p []byte) (int, error) {
	if t.replay && t.rpos < len(t.captured) {
		n := copy(p, t.captured[t.rpos:])
		t.rpos += n
		return n, nil
	}
	return t.Conn.Read(p)
}

func (t *teeConn) readCapture(n int) ([]byte, error) {
	buf := make([]byte, n)
	if _, err := io.ReadFull(t.Conn, buf); err != nil {
		return nil, err
	}
	t.captured = append(t.captured, buf...)
	return buf, nil
}

// peekUsername reads [len][tag][user\0...] from the hello without consuming it
// from the handshake's perspective (bytes are buffered for replay).
func peekUsername(t *teeConn) (string, error) {
	hdr, err := t.readCapture(2)
	if err != nil {
		return "", err
	}
	if hdr[1] != 0x06 {
		return "", errors.New("not an EC-SRP5 hello")
	}
	body, err := t.readCapture(int(hdr[0]))
	if err != nil {
		return "", err
	}
	nul := -1
	for i, b := range body {
		if b == 0 {
			nul = i
			break
		}
	}
	if nul < 0 {
		return "", errors.New("no username terminator")
	}
	return string(body[:nul]), nil
}
