package winbox

import (
	"crypto/rand"
	"errors"
	"io"
	"math/big"
)

// ClientHandshake performs the EC-SRP5 client role (what Winbox or a router does) over
// rw and returns the encrypted channel. A wrong password surfaces as an error.
func ClientHandshake(rw io.ReadWriter, user, pass string) (*SecureChannel, error) {
	c := NewCurve()
	aBytes := make([]byte, 32)
	rand.Read(aBytes)
	a := new(big.Int).SetBytes(aBytes)
	Wa, pa := c.ToMontgomery(c.ScalarBaseMult(a))

	hello := append(append(append([]byte(user), 0), Wa...), byte(pa))
	if _, err := rw.Write(append([]byte{byte(len(hello)), 0x06}, hello...)); err != nil {
		return nil, err
	}

	hdr := make([]byte, 2)
	if _, err := io.ReadFull(rw, hdr); err != nil {
		return nil, err
	}
	body := make([]byte, hdr[0])
	if _, err := io.ReadFull(rw, body); err != nil {
		return nil, err
	}
	if len(body) != 49 {
		return nil, errors.New("unexpected challenge size")
	}
	Wb, pb, salt := body[:32], int(body[32]), body[33:49]

	i := new(big.Int).SetBytes(c.PasswordScalar(user, pass, salt))
	v := c.ValidatorPoint(user, pass, salt)
	W := c.Add(c.LiftX(new(big.Int).SetBytes(Wb), pb), v)
	jb := sha256sum(Wa, Wb)
	j := new(big.Int).SetBytes(jb)
	scal := c.FF(new(big.Int).Add(new(big.Int).Mul(i, j), a))
	z, _ := c.ToMontgomery(c.ScalarMult(scal, W))

	cc := sha256sum(jb, z)
	if _, err := rw.Write(append([]byte{byte(len(cc)), 0x06}, cc...)); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(rw, hdr); err != nil {
		return nil, errors.New("server rejected the password")
	}
	cs := make([]byte, hdr[0])
	if _, err := io.ReadFull(rw, cs); err != nil {
		return nil, err
	}
	if !equal(cs, sha256sum(jb, cc, z)) {
		return nil, errors.New("server confirmation mismatch")
	}
	sa, ra, sh, rh := StreamKeys(false, sha256sum(z))
	return NewSecureChannel(sa, ra, sh, rh)
}
