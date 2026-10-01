package winbox

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
)

// Dump renders an M2 message as readable text, in wire order, keeping duplicate keys
// and nested message arrays that Parse flattens. Used for protocol capture/decoding.
func Dump(data []byte) string {
	var sb strings.Builder
	dump(&sb, data, 0)
	return sb.String()
}

func dump(sb *strings.Builder, data []byte, depth int) {
	ind := strings.Repeat("  ", depth)
	if len(data) < 2 || data[0] != 'M' || data[1] != '2' {
		fmt.Fprintf(sb, "%s<not M2: %s>\n", ind, short(data))
		return
	}
	pos := 2
	for pos+4 <= len(data) {
		low, high, ns, typ := data[pos], data[pos+1], data[pos+2], data[pos+3]
		pos += 4
		key := fmt.Sprintf("%02x%02x%02x", ns, high, low)
		switch typ {
		case 0x00, 0x01:
			fmt.Fprintf(sb, "%s%s bool %v\n", ind, key, typ == 1)
		case 0x09:
			if pos >= len(data) {
				return
			}
			fmt.Fprintf(sb, "%s%s u8 %d\n", ind, key, data[pos])
			pos++
		case 0x08:
			if pos+4 > len(data) {
				return
			}
			v := binary.LittleEndian.Uint32(data[pos:])
			fmt.Fprintf(sb, "%s%s u32 %d (0x%x)\n", ind, key, v, v)
			pos += 4
		case 0x10:
			if pos+8 > len(data) {
				return
			}
			fmt.Fprintf(sb, "%s%s u64 %d\n", ind, key, binary.LittleEndian.Uint64(data[pos:]))
			pos += 8
		case 0x88:
			if pos+2 > len(data) {
				return
			}
			n := int(binary.LittleEndian.Uint16(data[pos:]))
			pos += 2
			var vs []string
			for i := 0; i < n && pos+4 <= len(data); i++ {
				vs = append(vs, fmt.Sprint(binary.LittleEndian.Uint32(data[pos:])))
				pos += 4
			}
			fmt.Fprintf(sb, "%s%s u32[] [%s]\n", ind, key, strings.Join(vs, ", "))
		case 0x20, 0x30:
			if pos+2 > len(data) {
				return
			}
			n := int(binary.LittleEndian.Uint16(data[pos:]))
			pos += 2
			if pos+n > len(data) {
				return
			}
			sb.WriteString(fmtBlob(ind, key, typ, data[pos:pos+n]))
			pos += n
		case 0x21, 0x31:
			if pos >= len(data) {
				return
			}
			n := int(data[pos])
			pos++
			if pos+n > len(data) {
				return
			}
			sb.WriteString(fmtBlob(ind, key, typ, data[pos:pos+n]))
			pos += n
		case 0x28, 0x29: // nested M2 with u16 / u8 length
			var n int
			if typ == 0x28 {
				if pos+2 > len(data) {
					return
				}
				n = int(binary.LittleEndian.Uint16(data[pos:]))
				pos += 2
			} else {
				if pos >= len(data) {
					return
				}
				n = int(data[pos])
				pos++
			}
			if pos+n > len(data) {
				return
			}
			fmt.Fprintf(sb, "%s%s msg\n", ind, key)
			dump(sb, data[pos:pos+n], depth+1)
			pos += n
		case 0xA0:
			if pos+2 > len(data) {
				return
			}
			n := int(binary.LittleEndian.Uint16(data[pos:]))
			pos += 2
			var vs []string
			for i := 0; i < n && pos+2 <= len(data); i++ {
				l := int(binary.LittleEndian.Uint16(data[pos:]))
				pos += 2
				if pos+l > len(data) {
					return
				}
				vs = append(vs, fmt.Sprintf("%q", data[pos:pos+l]))
				pos += l
			}
			fmt.Fprintf(sb, "%s%s str[] [%s]\n", ind, key, strings.Join(vs, ", "))
		case 0xA8:
			if pos+2 > len(data) {
				return
			}
			n := int(binary.LittleEndian.Uint16(data[pos:]))
			pos += 2
			fmt.Fprintf(sb, "%s%s msg[] (%d)\n", ind, key, n)
			for i := 0; i < n && pos+2 <= len(data); i++ {
				l := int(binary.LittleEndian.Uint16(data[pos:]))
				pos += 2
				if pos+l > len(data) {
					return
				}
				fmt.Fprintf(sb, "%s  -[%d]\n", ind, i)
				dump(sb, data[pos:pos+l], depth+2)
				pos += l
			}
		default:
			fmt.Fprintf(sb, "%s%s <unknown type 0x%02x> rest=%s\n", ind, key, typ, short(data[pos:]))
			return
		}
	}
}

func fmtBlob(ind, key string, typ byte, b []byte) string {
	printable := true
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			printable = false
			break
		}
	}
	name := "str"
	if typ == 0x30 || typ == 0x31 {
		name = "raw"
	}
	if printable || typ == 0x20 || typ == 0x21 {
		return fmt.Sprintf("%s%s %s %q\n", ind, key, name, b)
	}
	return fmt.Sprintf("%s%s %s %s\n", ind, key, name, short(b))
}

func short(b []byte) string {
	if len(b) > 64 {
		return fmt.Sprintf("%s...(%d bytes)", hex.EncodeToString(b[:64]), len(b))
	}
	return hex.EncodeToString(b)
}
