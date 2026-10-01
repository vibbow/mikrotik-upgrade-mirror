package main

import (
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/local/mikrotik-mirror/internal/winbox"
)

// A client talks to a fake router through the proxy; the message must arrive intact
// and show up decoded in the log.
func TestProxyRelaysAndLogs(t *testing.T) {
	dir := t.TempDir()
	jf, _ := os.Create(dir + "/c.jsonl")
	tf, _ := os.Create(dir + "/c.txt")
	lg := &logger{jsonl: jf, txt: tf, start: time.Now()}
	t.Cleanup(func() { jf.Close(); tf.Close() })

	// fake router: echo each message back
	rln, _ := net.Listen("tcp", "127.0.0.1:0")
	go func() {
		c, _ := rln.Accept()
		ch, _, err := winbox.ServerHandshake(c, "admin", "pw")
		if err != nil {
			return
		}
		var re winbox.Reassembler
		buf := make([]byte, 4096)
		for {
			n, err := c.Read(buf)
			if err != nil {
				return
			}
			re.Feed(buf[:n])
			if _, rec, ok := re.Next(); ok {
				msg, _ := ch.DecryptAssembled(rec)
				c.Write(ch.Encrypt(msg))
			}
		}
	}()

	pln, _ := net.Listen("tcp", "127.0.0.1:0")
	go func() {
		c, _ := pln.Accept()
		handle(c, rln.Addr().String(), "admin", "pw", lg)
	}()

	cc, err := net.Dial("tcp", pln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()
	ch, err := winbox.ClientHandshake(cc, "admin", "pw")
	if err != nil {
		t.Fatal(err)
	}
	want := winbox.NewM2().String(1, 0, 0, "hello").U32(2, 0, 0, 7).Build()
	cc.Write(ch.Encrypt(want))
	var re winbox.Reassembler
	buf := make([]byte, 4096)
	cc.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		n, err := cc.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		re.Feed(buf[:n])
		if _, rec, ok := re.Next(); ok {
			got, err := ch.DecryptAssembled(rec)
			if err != nil || string(got) != string(want) {
				t.Fatalf("echo mismatch: %v %x", err, got)
			}
			break
		}
	}
	txt, _ := os.ReadFile(dir + "/c.txt")
	if !strings.Contains(string(txt), `000001 str "hello"`) || !strings.Contains(string(txt), "winbox->router") || !strings.Contains(string(txt), "router->winbox") {
		t.Fatalf("log missing content:\n%s", txt)
	}
}
