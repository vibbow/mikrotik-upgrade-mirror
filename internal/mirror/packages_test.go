package mirror

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVersionCode(t *testing.T) {
	cases := map[string]uint32{
		"7.24.4":  119039492, // taken from a real RouterOS source's package object
		"7.24":    0x07186600,
		"7.20.7":  0x07146607,
		"7.23.7":  0x07176607,
		"6.49.18": 0x06316612,
		"junk":    0,
		"7.x.1":   0,
	}
	for v, want := range cases {
		if got := VersionCode(v); got != want {
			t.Errorf("VersionCode(%q) = %#x, want %#x", v, got, want)
		}
	}
}

func TestParsePackageName(t *testing.T) {
	cases := []struct {
		file, name, version, arch string
	}{
		{"routeros-7.24.4-arm64.npk", "system", "7.24.4", "arm64"},
		{"routeros-7.24.4-smips.npk", "system", "7.24.4", "smips"},
		{"routeros-7.24.4.npk", "system", "7.24.4", "x86"}, // x86 main: no suffix
		{"wireless-7.24.4-smips.npk", "wireless", "7.24.4", "smips"},
		{"wifi-qcom-be-7.24.4-arm64.npk", "wifi-qcom-be", "7.24.4", "arm64"},
		{"iot-bt-extra-7.24.4-arm64.npk", "iot-bt-extra", "7.24.4", "arm64"},
		{"container-7.24.4.npk", "container", "7.24.4", "x86"}, // x86 extra: no suffix
		{"user-manager-7.23.7-mipsbe.npk", "user-manager", "7.23.7", "mipsbe"},
		{"routeros-7.25beta3-arm.npk", "system", "7.25beta3", "arm"},
	}
	dir := t.TempDir()
	for _, c := range cases {
		path := filepath.Join(dir, c.file)
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		info, _ := os.Stat(path)
		p := parsePackage(path, info)
		if p.Name != c.name || p.Version != c.version || p.Arch != c.arch {
			t.Errorf("%s: got name=%q version=%q arch=%q, want %q %q %q",
				c.file, p.Name, p.Version, p.Arch, c.name, c.version, c.arch)
		}
	}
}
