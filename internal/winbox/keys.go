package winbox

import (
	"crypto/hmac"
	"crypto/sha1"
)

const (
	magicSend = "On the client side, this is the send key; on the server side, it is the receive key."
	magicRecv = "On the client side, this is the receive key; on the server side, it is the send key."
)

func hkdf(message []byte) []byte {
	zeroKey := make([]byte, 0x40)
	h := hmac.New(sha1.New, zeroKey)
	h.Write(message)
	h1 := h.Sum(nil)

	var h2, res []byte
	for i := 0; i < 2; i++ {
		m := hmac.New(sha1.New, h1)
		m.Write(h2)
		m.Write([]byte{byte(i + 1)})
		h2 = m.Sum(nil)
		res = append(res, h2...)
	}
	return res[:0x24]
}

// StreamKeys derives (sendAES, recvAES, sendHMAC, recvHMAC) for the given role.
func StreamKeys(server bool, z []byte) (sendAES, recvAES, sendHMAC, recvHMAC []byte) {
	build := func(magic string) []byte {
		buf := make([]byte, 0, len(z)+40+len(magic)+40)
		buf = append(buf, z...)
		buf = append(buf, make([]byte, 40)...)
		buf = append(buf, []byte(magic)...)
		for i := 0; i < 40; i++ {
			buf = append(buf, 0xf2)
		}
		s := sha1.Sum(buf)
		return s[:16]
	}
	var tx, rx []byte
	if server {
		tx = build(magicRecv)
		rx = build(magicSend)
	} else {
		tx = build(magicSend)
		rx = build(magicRecv)
	}
	sk := hkdf(tx)
	rk := hkdf(rx)
	return sk[:0x10], rk[:0x10], sk[0x10:], rk[0x10:]
}
