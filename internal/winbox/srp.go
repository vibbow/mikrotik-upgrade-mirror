package winbox

import (
	"crypto/sha256"
	"math/big"
)

// PasswordScalar: i = SHA256(salt || SHA256("user:pass"))
func (c *Curve) PasswordScalar(user, pass string, salt []byte) []byte {
	inner := sha256.Sum256([]byte(user + ":" + pass))
	h := sha256.New()
	h.Write(salt)
	h.Write(inner[:])
	return h.Sum(nil)
}

// ValidatorPoint: v = REDP1(pub_x(i), 1), where pub_x(i) is the Montgomery x of i*G.
func (c *Curve) ValidatorPoint(user, pass string, salt []byte) *Point {
	i := c.PasswordScalar(user, pass, salt)
	pub, _ := c.ToMontgomery(c.ScalarBaseMult(new(big.Int).SetBytes(i)))
	return c.REDP1(pub, 1)
}

// Gpub: i*G, the verifier point used in the shared secret.
func (c *Curve) Gpub(user, pass string, salt []byte) *Point {
	i := c.PasswordScalar(user, pass, salt)
	return c.ScalarBaseMult(new(big.Int).SetBytes(i))
}

func sha256sum(parts ...[]byte) []byte {
	h := sha256.New()
	for _, p := range parts {
		h.Write(p)
	}
	return h.Sum(nil)
}
