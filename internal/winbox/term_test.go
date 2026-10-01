package winbox

import (
	"net"
	"regexp"
	"strings"
	"testing"
	"time"
)

// fakeTerminalRouter speaks just enough of the terminal protocol: it answers open,
// prints a prompt, and for each typed command line prints the marker-delimited output
// chosen by respond. It records the highest ack it saw.
func fakeTerminalRouter(t *testing.T, respond func(line string) string) (addr string, lastAck *uint32) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	lastAck = new(uint32)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		ch, _, err := ServerHandshake(c, "admin", "pw")
		if err != nil {
			return
		}
		var re Reassembler
		buf := make([]byte, 65536)
		send := func(data string) {
			b := NewM2().U32Array(1, 0, 0xff, []uint32{0, 100}).U32Array(2, 0, 0xff, []uint32{termHandler}).
				U8(1, 0, 0xfe, 16).U32(7, 0, 0xff, cmdTermData).Raw(2, 0, 0, []byte(data))
			c.Write(ch.Encrypt(b.Build()))
		}
		markerRe := regexp.MustCompile(`"MKB" \. "([0-9a-f]+)"`)
		for {
			n, err := c.Read(buf)
			if err != nil {
				return
			}
			re.Feed(buf[:n])
			for {
				_, rec, ok := re.Next()
				if !ok {
					break
				}
				msg, err := ch.DecryptAssembled(rec)
				if err != nil {
					return
				}
				m := Parse(msg)
				switch m[0xff0007].U {
				case cmdTermOpen:
					reply := NewM2().U32Array(1, 0, 0xff, []uint32{0, 100}).U32Array(2, 0, 0xff, []uint32{termHandler}).
						U8(1, 0, 0xfe, 16).U8(3, 0, 0xff, 2).U8(6, 0, 0xff, byte(m[0xff0006].U))
					c.Write(ch.Encrypt(reply.Build()))
					send("RouterOS 7\r\n\x1b[m[admin@Test] > ")
				case cmdTermData:
					*lastAck = uint32(m[0x000003].U)
					line := string(m[0x000002].B)
					if mm := markerRe.FindStringSubmatch(line); mm != nil {
						out := respond(line)
						send(line[:20] + "\r\n") // echo noise
						send("MKB" + mm[1] + "\r\n" + out + "MKE" + mm[1] + "\r\n\x1b[m[admin@Test] > ")
					}
				}
			}
		}
	}()
	return ln.Addr().String(), lastAck
}

func TestTerminalRunsCommands(t *testing.T) {
	addr, lastAck := fakeTerminalRouter(t, func(line string) string {
		if strings.Contains(line, "boom") {
			return "MKERR:failure: no such item\r\n"
		}
		return "7.24.4 (stable)\r\n"
	})
	term, err := OpenTerminal(addr, "admin", "pw", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	out, err := term.Run(":put [/system/resource/get version]", 3*time.Second)
	if err != nil || out != "7.24.4 (stable)" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if _, err := term.Run("/boom", 3*time.Second); err == nil || !strings.Contains(err.Error(), "no such item") {
		t.Fatalf("want CLI error, got %v", err)
	}
	if *lastAck == 0 {
		t.Error("client never acknowledged router output")
	}
}

func TestTerminalWrongPassword(t *testing.T) {
	addr, _ := fakeTerminalRouter(t, func(string) string { return "" })
	if _, err := OpenTerminal(addr, "admin", "bad", 3*time.Second); err == nil {
		t.Fatal("expected login failure")
	}
}
