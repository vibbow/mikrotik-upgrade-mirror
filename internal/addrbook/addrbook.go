// Package addrbook reads the Winbox address book (Addresses.cdb).
//
// The file is a sequence of length-prefixed M2 messages, one per saved router; the
// first record is preceded by a 4-byte magic (0xC01DF00D, little endian). Logins and
// passwords are stored in clear text.
package addrbook

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"regexp"

	"github.com/local/mikrotik-mirror/internal/winbox"
)

const magic = 0xC01DF00D

// Field keys inside one record.
const (
	keyAddress  = 0x000001
	keyLogin    = 0x000002
	keyPassword = 0x000003
	keyNote     = 0x000004
	keyGroup    = 0x000008
)

// Entry is one saved router.
type Entry struct {
	Address  string // as typed in Winbox: host, host:port or a MAC address
	Login    string
	Password string
	Note     string
	Group    string
}

var macRe = regexp.MustCompile(`^([0-9A-Fa-f]{2}[:-]){5}[0-9A-Fa-f]{2}$`)

// IsMAC reports whether the entry is reached by MAC address (not routable over IP).
func (e Entry) IsMAC() bool { return macRe.MatchString(e.Address) }

// Host returns the address without the Winbox port.
func (e Entry) Host() string {
	if h, _, err := net.SplitHostPort(e.Address); err == nil {
		return h
	}
	return e.Address
}

// Name is a short label for logs.
func (e Entry) Name() string {
	if e.Note != "" {
		return e.Address + " (" + e.Note + ")"
	}
	return e.Address
}

// ReadFile parses an Addresses.cdb file.
func ReadFile(path string) ([]Entry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse decodes the contents of an Addresses.cdb file.
func Parse(data []byte) ([]Entry, error) {
	if len(data) < 4 || binary.LittleEndian.Uint32(data) != magic {
		return nil, fmt.Errorf("not a Winbox address book (bad magic)")
	}
	pos := 4
	var entries []Entry
	for pos+4 <= len(data) {
		n := int(binary.LittleEndian.Uint32(data[pos:]))
		pos += 4
		if n < 0 || pos+n > len(data) {
			return nil, fmt.Errorf("truncated record at offset %d", pos-4)
		}
		m := winbox.Parse(data[pos : pos+n])
		pos += n
		e := Entry{
			Address:  m[keyAddress].S,
			Login:    m[keyLogin].S,
			Password: m[keyPassword].S,
			Note:     m[keyNote].S,
			Group:    m[keyGroup].S,
		}
		if e.Address != "" {
			entries = append(entries, e)
		}
	}
	return entries, nil
}
