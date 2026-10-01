package routeros

import "testing"

func TestParseTerse(t *testing.T) {
	out := "0    name=routeros version=7.23.5 build-time=2026-09-04 05:32:46 size=10.2MiB\n" +
		"1 XA name=calea size=20.1KiB\n" +
		"Flags: X - DISABLED\n" +
		"2  A name=wireless version=7.23.7 status=available\n"
	rows := parseTerse(out)
	if len(rows) != 3 {
		t.Fatalf("%v", rows)
	}
	if rows[0][".id"] != "0" || rows[0]["name"] != "routeros" || rows[0]["version"] != "7.23.5" {
		t.Errorf("%v", rows[0])
	}
	if rows[2]["status"] != "available" || rows[1]["name"] != "calea" {
		t.Errorf("%v", rows)
	}
}
