package mirror

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// npk filename: <name>-<version>[-<arch>].npk
// MikroTik publishes x86 packages without an architecture suffix
// (e.g. routeros-7.24.4.npk), so the arch group is optional. Their npk headers name
// the architecture "i386" (smips packages say "smips", arm64 say "arm64"), and that
// is the label a router matches against, so a missing suffix maps to i386.
var npkRE = regexp.MustCompile(`^([a-z0-9_\-]+?)-(\d[\w.]*?)(?:-([a-z0-9_]+))?\.npk$`)

// The main system package is published to clients under the object name "system".
var nameAlias = map[string]string{"routeros": "system"}

// VersionCode encodes "major.minor[.patch]" the way a RouterOS source does in the
// package object's 0x67 field, which the router uses to show the version:
//
//	major<<24 | minor<<16 | 0x66<<8 | patch      e.g. 7.24.4 -> 0x07186604 = 119039492
//
// 0x66 is the marker seen on final releases. Only final stable / long-term releases
// are mirrored; a version that does not parse yields 0.
func VersionCode(version string) uint32 {
	parts := strings.Split(version, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return 0
	}
	nums := [3]uint32{}
	for i, p := range parts {
		n, err := strconv.ParseUint(p, 10, 8)
		if err != nil {
			return 0
		}
		nums[i] = uint32(n)
	}
	return nums[0]<<24 | nums[1]<<16 | 0x66<<8 | nums[2]
}

type Package struct {
	Path    string
	Fname   string
	Name    string
	Version string
	Arch    string
	Size    int64
	MTime   int64
}

func parsePackage(path string, info os.FileInfo) Package {
	p := Package{
		Path:  path,
		Fname: filepath.Base(path),
		Size:  info.Size(),
		MTime: info.ModTime().Unix(),
	}
	m := npkRE.FindStringSubmatch(p.Fname)
	if m == nil {
		p.Name = p.Fname
		p.Version = "0.0"
		return p
	}
	raw := m[1]
	if a, ok := nameAlias[raw]; ok {
		p.Name = a
	} else {
		p.Name = raw
	}
	p.Version = m[2]
	p.Arch = m[3]
	if p.Arch == "" {
		p.Arch = "i386" // no suffix means x86; "i386" is what the npk header carries
	}
	return p
}

// LoadDir returns every .npk in dir, sorted by filename.
func LoadDir(dir string) ([]Package, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var pkgs []Package
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".npk") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		pkgs = append(pkgs, parsePackage(filepath.Join(dir, e.Name()), info))
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Fname < pkgs[j].Fname })
	return pkgs, nil
}
