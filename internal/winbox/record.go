package winbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"errors"
)

// SecureChannel is the AES-128-CBC + HMAC-SHA1 record layer over the chunk framing.
type SecureChannel struct {
	sendAES, recvAES   cipher.Block
	sendHMAC, recvHMAC []byte
}

func NewSecureChannel(sendAES, recvAES, sendHMAC, recvHMAC []byte) (*SecureChannel, error) {
	sb, err := aes.NewCipher(sendAES)
	if err != nil {
		return nil, err
	}
	rb, err := aes.NewCipher(recvAES)
	if err != nil {
		return nil, err
	}
	return &SecureChannel{sb, rb, sendHMAC, recvHMAC}, nil
}

// Encrypt frames one M2 message into chunked, encrypted wire bytes.
func (s *SecureChannel) Encrypt(msg []byte) []byte {
	mac := hmac.New(sha1.New, s.sendHMAC)
	mac.Write(msg)
	h := mac.Sum(nil)

	iv := make([]byte, 16)
	rand.Read(iv)

	plain := append(append([]byte{}, msg...), h...)
	pad := 0xF - (len(plain) % 0x10)
	for i := 0; i <= pad; i++ {
		plain = append(plain, byte(pad))
	}
	ct := make([]byte, len(plain))
	cipher.NewCBCEncrypter(s.sendAES, iv).CryptBlocks(ct, plain)

	full := make([]byte, 2+16+len(ct))
	binary.BigEndian.PutUint16(full, uint16(len(ct)))
	copy(full[2:], iv)
	copy(full[18:], ct)

	return chunkFrame(full, 0x06)
}

// DecryptAssembled decrypts one reassembled 0x06 record payload.
func (s *SecureChannel) DecryptAssembled(assembled []byte) ([]byte, error) {
	if len(assembled) < 2+16 {
		return nil, errors.New("short record")
	}
	body := assembled[2:] // drop 2-byte enc_len
	iv := body[:16]
	ct := body[16:]
	if len(ct) == 0 || len(ct)%16 != 0 {
		return nil, errors.New("bad ct length")
	}
	pt := make([]byte, len(ct))
	cipher.NewCBCDecrypter(s.recvAES, iv).CryptBlocks(pt, ct)

	p := int(pt[len(pt)-1])
	if p+1 > len(pt) {
		return nil, errors.New("bad padding")
	}
	pt = pt[:len(pt)-(p+1)]
	if len(pt) < 20 {
		return nil, errors.New("missing hmac")
	}
	msg := pt[:len(pt)-20]
	gotMac := pt[len(pt)-20:]
	mac := hmac.New(sha1.New, s.recvHMAC)
	mac.Write(msg)
	if !hmac.Equal(gotMac, mac.Sum(nil)) {
		return nil, errors.New("hmac mismatch")
	}
	return msg, nil
}

// chunkFrame splits data into [len][tag] chunks; first tag given, rest 0xFF.
func chunkFrame(data []byte, firstTag byte) []byte {
	var out []byte
	rem := data
	tag := firstTag
	for {
		n := len(rem)
		if n >= 0xFF {
			n = 0xFF
		}
		out = append(out, byte(n), tag)
		out = append(out, rem[:n]...)
		if len(rem) >= 0xFF {
			rem = rem[0xFF:]
			tag = 0xFF
		} else {
			break
		}
	}
	return out
}

// Reassembler accumulates wire bytes and yields complete records.
type Reassembler struct{ buf []byte }

func (r *Reassembler) Feed(b []byte) { r.buf = append(r.buf, b...) }

// Next returns the next assembled record payload and its first tag, or ok=false.
func (r *Reassembler) Next() (tag byte, payload []byte, ok bool) {
	buf := r.buf
	if len(buf) < 2 {
		return 0, nil, false
	}
	pos := 0
	var assembled []byte
	var firstTag byte
	first := true
	for pos < len(buf) {
		if pos+2 > len(buf) {
			return 0, nil, false
		}
		clen := int(buf[pos])
		t := buf[pos+1]
		if first {
			firstTag = t
			first = false
		}
		pos += 2
		if pos+clen > len(buf) {
			return 0, nil, false
		}
		assembled = append(assembled, buf[pos:pos+clen]...)
		pos += clen
		if clen < 0xFF {
			break
		}
	}
	r.buf = r.buf[pos:]
	return firstTag, assembled, true
}
