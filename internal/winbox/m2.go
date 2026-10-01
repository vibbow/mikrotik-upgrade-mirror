package winbox

import (
	"encoding/binary"
)

// M2 TLV namespaces.
const (
	NsSys = 0xFF
	NsSes = 0xFE
	NsUsr = 0x00
)

// Full keys we use (namespace<<16 | high<<8 | low).
const (
	KeySysTo    = 0xFF0001
	KeySysFrom  = 0xFF0002
	KeySysReq   = 0xFF0005
	KeySysReqID = 0xFF0006
	KeySysCmd   = 0xFF0007
	KeySysStat  = 0xFF0003
	KeySesID    = 0xFE0001
	KeyData     = 0x000002 // request chunk size / file size
	KeyFileData = 0x000005
	KeyFileEOF  = 0x000006
	KeyName     = 0x000001
	KeyMsgArray = 0xFE0002
)

// M2Builder assembles an M2 message.
type M2Builder struct{ b []byte }

func NewM2() *M2Builder { return &M2Builder{b: []byte("M2")} }

func (m *M2Builder) hdr(low, high, ns byte) { m.b = append(m.b, low, high, ns) }

func (m *M2Builder) Bool(low, high, ns byte, v bool) *M2Builder {
	m.hdr(low, high, ns)
	if v {
		m.b = append(m.b, 0x01)
	} else {
		m.b = append(m.b, 0x00)
	}
	return m
}

func (m *M2Builder) U8(low, high, ns, v byte) *M2Builder {
	m.hdr(low, high, ns)
	m.b = append(m.b, 0x09, v)
	return m
}

func (m *M2Builder) U32(low, high, ns byte, v uint32) *M2Builder {
	m.hdr(low, high, ns)
	m.b = append(m.b, 0x08)
	m.b = binary.LittleEndian.AppendUint32(m.b, v)
	return m
}

func (m *M2Builder) U64(low, high, ns byte, v uint64) *M2Builder {
	m.hdr(low, high, ns)
	m.b = append(m.b, 0x10)
	m.b = binary.LittleEndian.AppendUint64(m.b, v)
	return m
}

func (m *M2Builder) U32Array(low, high, ns byte, vals []uint32) *M2Builder {
	m.hdr(low, high, ns)
	m.b = append(m.b, 0x88)
	m.b = binary.LittleEndian.AppendUint16(m.b, uint16(len(vals)))
	for _, v := range vals {
		m.b = binary.LittleEndian.AppendUint32(m.b, v)
	}
	return m
}

func (m *M2Builder) String(low, high, ns byte, s string) *M2Builder {
	b := []byte(s)
	m.hdr(low, high, ns)
	if len(b) < 256 {
		m.b = append(m.b, 0x21, byte(len(b)))
	} else {
		m.b = append(m.b, 0x20)
		m.b = binary.LittleEndian.AppendUint16(m.b, uint16(len(b)))
	}
	m.b = append(m.b, b...)
	return m
}

func (m *M2Builder) Raw(low, high, ns byte, b []byte) *M2Builder {
	m.hdr(low, high, ns)
	if len(b) < 256 {
		m.b = append(m.b, 0x31, byte(len(b)))
	} else {
		m.b = append(m.b, 0x30)
		m.b = binary.LittleEndian.AppendUint16(m.b, uint16(len(b)))
	}
	m.b = append(m.b, b...)
	return m
}

// StrArray packs a string array (0xA0). Used empty for the 0xff001c marker.
func (m *M2Builder) StrArray(low, high, ns byte, items []string) *M2Builder {
	m.hdr(low, high, ns)
	m.b = append(m.b, 0xA0)
	m.b = binary.LittleEndian.AppendUint16(m.b, uint16(len(items)))
	for _, s := range items {
		m.b = binary.LittleEndian.AppendUint16(m.b, uint16(len(s)))
		m.b = append(m.b, []byte(s)...)
	}
	return m
}

// MsgArray packs complete M2 submessages (each starting with "M2").
func (m *M2Builder) MsgArray(low, high, ns byte, subs [][]byte) *M2Builder {
	m.hdr(low, high, ns)
	m.b = append(m.b, 0xA8)
	m.b = binary.LittleEndian.AppendUint16(m.b, uint16(len(subs)))
	for _, s := range subs {
		m.b = binary.LittleEndian.AppendUint16(m.b, uint16(len(s)))
		m.b = append(m.b, s...)
	}
	return m
}

func (m *M2Builder) Build() []byte { return m.b }

// --- parser ---

type Value struct {
	Type byte
	U    uint64
	S    string
	B    []byte
	Bool bool
	Arr  []uint32
}

// Parse decodes an M2 message into a map keyed by full key.
func Parse(data []byte) map[uint32]Value {
	res := map[uint32]Value{}
	if len(data) < 2 || data[0] != 'M' || data[1] != '2' {
		return res
	}
	pos := 2
	for pos+4 <= len(data) {
		low := data[pos]
		high := data[pos+1]
		ns := data[pos+2]
		typ := data[pos+3]
		pos += 4
		key := uint32(ns)<<16 | uint32(high)<<8 | uint32(low)
		switch typ {
		case 0x00:
			res[key] = Value{Type: typ, Bool: false}
		case 0x01:
			res[key] = Value{Type: typ, Bool: true}
		case 0x09:
			if pos < len(data) {
				res[key] = Value{Type: typ, U: uint64(data[pos])}
				pos++
			}
		case 0x08:
			if pos+4 <= len(data) {
				res[key] = Value{Type: typ, U: uint64(binary.LittleEndian.Uint32(data[pos:]))}
				pos += 4
			}
		case 0x10:
			if pos+8 <= len(data) {
				res[key] = Value{Type: typ, U: binary.LittleEndian.Uint64(data[pos:])}
				pos += 8
			}
		case 0x88:
			if pos+2 <= len(data) {
				n := int(binary.LittleEndian.Uint16(data[pos:]))
				pos += 2
				arr := make([]uint32, 0, n)
				for i := 0; i < n && pos+4 <= len(data); i++ {
					arr = append(arr, binary.LittleEndian.Uint32(data[pos:]))
					pos += 4
				}
				res[key] = Value{Type: typ, Arr: arr}
			}
		case 0x20:
			if pos+2 <= len(data) {
				n := int(binary.LittleEndian.Uint16(data[pos:]))
				pos += 2
				if pos+n <= len(data) {
					res[key] = Value{Type: typ, S: string(data[pos : pos+n])}
					pos += n
				}
			}
		case 0x21:
			if pos < len(data) {
				n := int(data[pos])
				pos++
				if pos+n <= len(data) {
					res[key] = Value{Type: typ, S: string(data[pos : pos+n])}
					pos += n
				}
			}
		case 0x30:
			if pos+2 <= len(data) {
				n := int(binary.LittleEndian.Uint16(data[pos:]))
				pos += 2
				if pos+n <= len(data) {
					res[key] = Value{Type: typ, B: data[pos : pos+n]}
					pos += n
				}
			}
		case 0x31:
			if pos < len(data) {
				n := int(data[pos])
				pos++
				if pos+n <= len(data) {
					res[key] = Value{Type: typ, B: data[pos : pos+n]}
					pos += n
				}
			}
		case 0x28: // nested message, u16 length (skipped)
			if pos+2 <= len(data) {
				n := int(binary.LittleEndian.Uint16(data[pos:]))
				pos += 2 + n
				res[key] = Value{Type: typ}
			}
		case 0x29: // nested message, u8 length (skipped)
			if pos < len(data) {
				n := int(data[pos])
				pos += 1 + n
				res[key] = Value{Type: typ}
			}
		case 0xA0: // str_array: [count][ (len u16)(bytes) ... ]
			if pos+2 <= len(data) {
				n := int(binary.LittleEndian.Uint16(data[pos:]))
				pos += 2
				for i := 0; i < n && pos+2 <= len(data); i++ {
					l := int(binary.LittleEndian.Uint16(data[pos:]))
					pos += 2 + l
				}
				res[key] = Value{Type: typ}
			}
		case 0xA8: // msg_array: [count][ (len u16)(M2 bytes) ... ]
			if pos+2 <= len(data) {
				n := int(binary.LittleEndian.Uint16(data[pos:]))
				pos += 2
				for i := 0; i < n && pos+2 <= len(data); i++ {
					l := int(binary.LittleEndian.Uint16(data[pos:]))
					pos += 2 + l
				}
				res[key] = Value{Type: typ}
			}
		default:
			return res // unknown type: stop
		}
	}
	return res
}

// Msg packs a nested M2 message (type 0x29, u8 length).
func (m *M2Builder) Msg(low, high, ns byte, sub []byte) *M2Builder {
	m.hdr(low, high, ns)
	m.b = append(m.b, 0x29, byte(len(sub)))
	m.b = append(m.b, sub...)
	return m
}
