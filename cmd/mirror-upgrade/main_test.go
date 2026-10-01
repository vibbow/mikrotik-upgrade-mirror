package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadIgnore(t *testing.T) {
	f := filepath.Join(t.TempDir(), "ignore.txt")
	os.WriteFile(f, []byte("# comment\n192.168.1.1\r\n  Router.Example.com  # trailing note\n\n10.0.0.5:8291\n"), 0o644)
	set, err := loadIgnore(f, "172.16.0.1, 10.9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"192.168.1.1", "router.example.com", "10.0.0.5:8291", "172.16.0.1", "10.9.9.9"} {
		if !set[want] {
			t.Errorf("missing %q in %v", want, set)
		}
	}
	if len(set) != 5 {
		t.Errorf("unexpected entries: %v", set)
	}
	if _, err := loadIgnore(filepath.Join(t.TempDir(), "nope.txt"), ""); err == nil {
		t.Error("a missing ignore file must be an error, not silently ignored")
	}
}
