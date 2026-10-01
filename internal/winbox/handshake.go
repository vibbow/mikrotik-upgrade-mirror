package winbox

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"math/big"
)

// ServerHandshake performs the EC-SRP5 server role and returns a SecureChannel
// plus the authenticated username. rw must be the raw client connection.
//
// We hold the plaintext password of the account(s) clients use. For a real
// RouterOS this would be the stored verifier; holding the password lets us
// serve any configured account the same way.
func ServerHandshake(rw io.ReadWriter, username, password string) (*SecureChannel, string, error) {
	c := NewCurve()

	// Phase 1: client hello [len][0x06][user\0][Wa:32][parityA:1]
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(rw, hdr); err != nil {
		return nil, "", err
	}
	if hdr[1] != 0x06 {
		return nil, "", fmt.Errorf("client did not offer EC-SRP5 (tag=%#x)", hdr[1])
	}
	body := make([]byte, hdr[0])
	if _, err := io.ReadFull(rw, body); err != nil {
		return nil, "", err
	}
	nul := -1
	for i, b := range body {
		if b == 0 {
			nul = i
			break
		}
	}
	if nul < 0 || len(body) < nul+1+33 {
		return nil, "", errors.New("malformed client hello")
	}
	user := string(body[:nul])
	rest := body[nul+1:]
	Wa := rest[:32]
	paA := int(rest[32])

	// The configured credential this server authenticates against.
	salt := make([]byte, 16)
	rand.Read(salt)
	bBytes := make([]byte, 32)
	rand.Read(bBytes)
	b := new(big.Int).SetBytes(bBytes)

	vRedp1 := c.ValidatorPoint(username, password, salt)
	gpub := c.Gpub(username, password, salt)

	WbSentPt := c.Add(c.ScalarBaseMult(b), c.Neg(vRedp1))
	WbSent, pb := c.ToMontgomery(WbSentPt)

	reply := append(append(append([]byte{}, WbSent...), byte(pb)), salt...)
	if _, err := rw.Write(append([]byte{byte(len(reply)), 0x06}, reply...)); err != nil {
		return nil, "", err
	}

	jBytes := sha256sum(Wa, WbSent)
	jint := c.FF(new(big.Int).SetBytes(jBytes))
	WaPt := c.LiftX(new(big.Int).SetBytes(Wa), paA)
	combined := c.Add(WaPt, c.ScalarMult(jint, gpub))
	z, _ := c.ToMontgomery(c.ScalarMult(b, combined))
	secret := sha256sum(z)

	// Phase 2: client confirmation
	ch := make([]byte, 2)
	if _, err := io.ReadFull(rw, ch); err != nil {
		return nil, "", err
	}
	cc := make([]byte, ch[0])
	if _, err := io.ReadFull(rw, cc); err != nil {
		return nil, "", err
	}
	expect := sha256sum(jBytes, z)
	if !equal(cc, expect) {
		return nil, "", fmt.Errorf("client confirmation mismatch (user=%s): the router most likely entered a different password than the server's --password", user)
	}
	cs := sha256sum(jBytes, cc, z)
	if _, err := rw.Write(append([]byte{byte(len(cs)), 0x06}, cs...)); err != nil {
		return nil, "", err
	}

	sa, ra, sh, rh := StreamKeys(true, secret)
	sc, err := NewSecureChannel(sa, ra, sh, rh)
	return sc, user, err
}

func equal(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
