package addrbook

import (
	"encoding/binary"
	"testing"

	"github.com/local/mikrotik-mirror/internal/winbox"
)

func record(addr, login, pass, note, group string) []byte {
	return winbox.NewM2().
		String(1, 0, 0, addr).String(2, 0, 0, login).String(3, 0, 0, pass).
		String(4, 0, 0, note).String(8, 0, 0, group).Build()
}

func TestParse(t *testing.T) {
	var data []byte
	data = binary.LittleEndian.AppendUint32(data, magic)
	for _, r := range [][]byte{
		record("192.168.1.1", "admin", "pw", "office", "My"),
		record("10.0.0.1:8761", "root", "", "", ""),
		record("08:55:31:06:E2:EA", "admin", "", "", ""),
	} {
		data = binary.LittleEndian.AppendUint32(data, uint32(len(r)))
		data = append(data, r...)
	}
	es, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 3 {
		t.Fatalf("got %d entries", len(es))
	}
	if es[0].Login != "admin" || es[0].Password != "pw" || es[0].Note != "office" || es[0].Group != "My" {
		t.Errorf("entry 0: %+v", es[0])
	}
	if es[1].Host() != "10.0.0.1" || es[1].IsMAC() {
		t.Errorf("entry 1: %+v host=%q", es[1], es[1].Host())
	}
	if !es[2].IsMAC() {
		t.Errorf("entry 2 should be a MAC")
	}
}

func TestParseBadMagic(t *testing.T) {
	if _, err := Parse([]byte("nope....")); err == nil {
		t.Fatal("expected error")
	}
}
