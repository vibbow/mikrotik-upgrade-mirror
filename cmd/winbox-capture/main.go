// winbox-capture is a man-in-the-middle for the Winbox protocol, used to learn which
// M2 messages Winbox sends for an operation (so mirror-upgrade can do it over 8291).
//
// Point Winbox at this program instead of the router and log in with the router's real
// credentials. The program authenticates the Winbox side as a server and the router side
// as a client (both with --user/--password), then relays every message while writing the
// decrypted contents to a log.
//
//	winbox-capture --upstream 192.168.1.1:8291 --user admin --password ... --out capture
//
// then connect Winbox to 127.0.0.1 (port 8291 by default). Writes <out>.jsonl (one
// hex message per line) and <out>.txt (readable dump).
package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"github.com/local/mikrotik-mirror/internal/winbox"
)

type logger struct {
	mu    sync.Mutex
	jsonl *os.File
	txt   *os.File
	start time.Time
	n     int
}

func (l *logger) record(dir string, msg []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.n++
	t := time.Since(l.start).Seconds()
	line, _ := json.Marshal(map[string]any{"n": l.n, "t": t, "dir": dir, "hex": hex.EncodeToString(msg)})
	l.jsonl.Write(append(line, '\n'))
	fmt.Fprintf(l.txt, "#%d %.3fs %s (%d bytes)\n%s\n", l.n, t, dir, len(msg), winbox.Dump(msg))
}

func (l *logger) note(format string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.txt, "### %s\n\n", fmt.Sprintf(format, a...))
}

func main() {
	var (
		listen   = flag.String("listen", "0.0.0.0:8291", "where Winbox connects")
		upstream = flag.String("upstream", "", "router address host:8291 (required)")
		user     = flag.String("user", "", "router login (Winbox must log in with the same)")
		pass     = flag.String("password", "", "router password")
		out      = flag.String("out", "capture", "output file prefix")
		dumpF    = flag.String("dump", "", "print this .jsonl capture as text and exit")
		from     = flag.Int("from", 0, "with --dump: first message number")
		to       = flag.Int("to", 0, "with --dump: last message number")
	)
	flag.Parse()
	if *dumpF != "" {
		if err := redump(*dumpF, *from, *to, false); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *upstream == "" || *user == "" {
		log.Fatal("--upstream and --user are required")
	}
	jf, err := os.Create(*out + ".jsonl")
	if err != nil {
		log.Fatal(err)
	}
	tf, err := os.Create(*out + ".txt")
	if err != nil {
		log.Fatal(err)
	}
	lg := &logger{jsonl: jf, txt: tf, start: time.Now()}

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("listening on %s, relaying to %s; writing %s.jsonl/.txt", *listen, *upstream, *out)
	for {
		c, err := ln.Accept()
		if err != nil {
			log.Printf("accept: %v", err)
			continue
		}
		go handle(c, *upstream, *user, *pass, lg)
	}
}

func handle(cli net.Conn, upstream, user, pass string, lg *logger) {
	defer cli.Close()
	cliCh, _, err := winbox.ServerHandshake(cli, user, pass)
	if err != nil {
		log.Printf("winbox side handshake failed: %v", err)
		return
	}
	up, err := net.DialTimeout("tcp", upstream, 10*time.Second)
	if err != nil {
		log.Printf("connect router: %v", err)
		return
	}
	defer up.Close()
	upCh, err := winbox.ClientHandshake(up, user, pass)
	if err != nil {
		log.Printf("router side handshake failed: %v", err)
		return
	}
	log.Printf("session established")
	lg.note("session established")

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer up.Close()
		defer cli.Close()
		pump(cli, cliCh, up, upCh, "winbox->router", lg)
	}()
	go func() {
		defer wg.Done()
		defer up.Close()
		defer cli.Close()
		pump(up, upCh, cli, cliCh, "router->winbox", lg)
	}()
	wg.Wait()
	log.Printf("session closed")
	lg.note("session closed")
}

func pump(src net.Conn, srcCh *winbox.SecureChannel, dst net.Conn, dstCh *winbox.SecureChannel, dir string, lg *logger) {
	var re winbox.Reassembler
	buf := make([]byte, 65536)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			re.Feed(buf[:n])
			for {
				_, rec, ok := re.Next()
				if !ok {
					break
				}
				msg, derr := srcCh.DecryptAssembled(rec)
				if derr != nil {
					log.Printf("%s: decrypt: %v", dir, derr)
					return
				}
				lg.record(dir, msg)
				if _, werr := dst.Write(dstCh.Encrypt(msg)); werr != nil {
					return
				}
			}
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("%s: %v", dir, err)
			}
			return
		}
	}
}

// redump prints a .jsonl capture as readable text (optionally only messages n1..n2).
func redump(path string, from, to int, redact bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	for dec.More() {
		var m struct {
			N   int     `json:"n"`
			T   float64 `json:"t"`
			Dir string  `json:"dir"`
			Hex string  `json:"hex"`
		}
		if err := dec.Decode(&m); err != nil {
			return err
		}
		if m.N < from || (to > 0 && m.N > to) {
			continue
		}
		b, _ := hex.DecodeString(m.Hex)
		fmt.Printf("#%d %.3fs %s (%d bytes)\n%s\n", m.N, m.T, m.Dir, len(b), winbox.Dump(b))
	}
	return nil
}
