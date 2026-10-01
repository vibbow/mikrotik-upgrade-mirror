package winbox

// Curve25519 in short-Weierstrass form, as used by Winbox EC-SRP5.
// Parameters and formulas mirror the validated Python (wincrypto.py / ecdsa lib):
//
//	p        = 2^255 - 19
//	A (mont) = 486662
//	a (weier)= 0x2aaa...4914a144
//	b (weier)= 0x7b42...0c864
//	conv     = (p - A/3) mod p  (Montgomery x -> Weierstrass x and back)
//
// Points are affine (X,Y) with a point-at-infinity flag. Only the operations the
// handshake needs are implemented: scalar mult, add, negate, lift_x, the
// Montgomery<->Weierstrass x conversion, and REDP1.

import (
	"crypto/sha256"
	"math/big"
)

type Curve struct {
	P         *big.Int
	R         *big.Int // group order
	A         *big.Int // weierstrass a
	B         *big.Int // weierstrass b
	montA     *big.Int
	convFromM *big.Int // + A/3  (mont x -> weier x)
	conv      *big.Int // - A/3  (weier x -> mont x)
	G         *Point
}

type Point struct {
	X, Y *big.Int
	Inf  bool
}

func hx(s string) *big.Int { n, _ := new(big.Int).SetString(s, 16); return n }

func NewCurve() *Curve {
	c := &Curve{
		P: hx("7fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffed"),
		R: hx("1000000000000000000000000000000014def9dea2f79cd65812631a5cf5d3ed"),
		A: hx("2aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa984914a144"),
		B: hx("7b425ed097b425ed097b425ed097b425ed097b4260b5e9c7710c864"),
	}
	c.montA = big.NewInt(486662)
	inv3 := new(big.Int).ModInverse(big.NewInt(3), c.P)
	aOver3 := new(big.Int).Mul(c.montA, inv3)
	aOver3.Mod(aOver3, c.P)
	c.convFromM = new(big.Int).Set(aOver3)
	c.conv = new(big.Int).Sub(c.P, aOver3)
	c.conv.Mod(c.conv, c.P)
	c.G = c.LiftX(big.NewInt(9), 0)
	return c
}

func (c *Curve) mod(x *big.Int) *big.Int {
	r := new(big.Int).Mod(x, c.P)
	if r.Sign() < 0 {
		r.Add(r, c.P)
	}
	return r
}

// sqrtMod returns the two square roots of a mod p (p % 4 == 3 fast path applies:
// p = 2^255-19, p % 4 == 1, so use Tonelli-Shanks). Returns nil if non-residue.
func (c *Curve) sqrtMod(a *big.Int) (*big.Int, *big.Int) {
	a = c.mod(a)
	if a.Sign() == 0 {
		return big.NewInt(0), big.NewInt(0)
	}
	// Euler criterion
	exp := new(big.Int).Sub(c.P, big.NewInt(1))
	exp.Rsh(exp, 1)
	if new(big.Int).Exp(a, exp, c.P).Cmp(big.NewInt(1)) != 0 {
		return nil, nil
	}
	// p % 4 == 1 for 2^255-19, so full Tonelli-Shanks.
	// Write p-1 = q * 2^s
	q := new(big.Int).Sub(c.P, big.NewInt(1))
	s := 0
	for q.Bit(0) == 0 {
		q.Rsh(q, 1)
		s++
	}
	// find a non-residue z
	z := big.NewInt(2)
	for {
		if new(big.Int).Exp(z, exp, c.P).Cmp(new(big.Int).Sub(c.P, big.NewInt(1))) == 0 {
			break
		}
		z.Add(z, big.NewInt(1))
	}
	m := s
	cc := new(big.Int).Exp(z, q, c.P)
	t := new(big.Int).Exp(a, q, c.P)
	qp1 := new(big.Int).Add(q, big.NewInt(1))
	qp1.Rsh(qp1, 1)
	r := new(big.Int).Exp(a, qp1, c.P)
	for t.Cmp(big.NewInt(1)) != 0 {
		i := 0
		tt := new(big.Int).Set(t)
		for i = 1; i < m; i++ {
			tt.Mul(tt, tt)
			tt.Mod(tt, c.P)
			if tt.Cmp(big.NewInt(1)) == 0 {
				break
			}
		}
		b := new(big.Int).Set(cc)
		for j := 0; j < m-i-1; j++ {
			b.Mul(b, b)
			b.Mod(b, c.P)
		}
		r.Mul(r, b)
		r.Mod(r, c.P)
		cc.Mul(b, b)
		cc.Mod(cc, c.P)
		t.Mul(t, cc)
		t.Mod(t, c.P)
		m = i
	}
	r2 := new(big.Int).Sub(c.P, r)
	return r, r2
}

// LiftX recovers a Weierstrass point from a Montgomery x and desired y-parity.
func (c *Curve) LiftX(x *big.Int, parity int) *Point {
	x = c.mod(x)
	// y^2 = x^3 + A x^2 + x  (Montgomery)
	x2 := new(big.Int).Mul(x, x)
	x2.Mod(x2, c.P)
	x3 := new(big.Int).Mul(x2, x)
	x3.Mod(x3, c.P)
	ax2 := new(big.Int).Mul(c.montA, x2)
	ax2.Mod(ax2, c.P)
	y2 := new(big.Int).Add(x3, ax2)
	y2.Add(y2, x)
	y2.Mod(y2, c.P)

	wx := new(big.Int).Add(x, c.convFromM)
	wx.Mod(wx, c.P)

	y1, yo := c.sqrtMod(y2)
	if y1 == nil {
		return nil
	}
	p1 := &Point{X: new(big.Int).Set(wx), Y: y1}
	p2 := &Point{X: new(big.Int).Set(wx), Y: yo}
	if p1.Y.Bit(0) == 1 && parity != 0 {
		return p1
	}
	if p2.Y.Bit(0) == 1 && parity != 0 {
		return p2
	}
	if p1.Y.Bit(0) == 0 && parity == 0 {
		return p1
	}
	return p2
}

// ToMontgomery returns the 32-byte big-endian Montgomery x and y-parity.
func (c *Curve) ToMontgomery(p *Point) ([]byte, int) {
	x := new(big.Int).Add(p.X, c.conv)
	x.Mod(x, c.P)
	return leftPad(x.Bytes(), 32), int(p.Y.Bit(0))
}

func (c *Curve) Add(p, q *Point) *Point {
	if p.Inf {
		return q
	}
	if q.Inf {
		return p
	}
	if p.X.Cmp(q.X) == 0 {
		sum := new(big.Int).Add(p.Y, q.Y)
		sum.Mod(sum, c.P)
		if sum.Sign() == 0 {
			return &Point{Inf: true}
		}
		return c.double(p)
	}
	// lambda = (qY - pY)/(qX - pX)
	num := new(big.Int).Sub(q.Y, p.Y)
	den := new(big.Int).Sub(q.X, p.X)
	den.ModInverse(den, c.P)
	lam := new(big.Int).Mul(num, den)
	lam.Mod(lam, c.P)
	return c.fromLambda(p, q, lam)
}

func (c *Curve) double(p *Point) *Point {
	if p.Inf || p.Y.Sign() == 0 {
		return &Point{Inf: true}
	}
	// lambda = (3xx + a) / (2y)
	xx := new(big.Int).Mul(p.X, p.X)
	xx.Mod(xx, c.P)
	num := new(big.Int).Mul(big.NewInt(3), xx)
	num.Add(num, c.A)
	num.Mod(num, c.P)
	den := new(big.Int).Lsh(p.Y, 1)
	den.ModInverse(den, c.P)
	lam := new(big.Int).Mul(num, den)
	lam.Mod(lam, c.P)
	return c.fromLambda(p, p, lam)
}

func (c *Curve) fromLambda(p, q *Point, lam *big.Int) *Point {
	x3 := new(big.Int).Mul(lam, lam)
	x3.Sub(x3, p.X)
	x3.Sub(x3, q.X)
	x3.Mod(x3, c.P)
	y3 := new(big.Int).Sub(p.X, x3)
	y3.Mul(y3, lam)
	y3.Sub(y3, p.Y)
	y3.Mod(y3, c.P)
	if y3.Sign() < 0 {
		y3.Add(y3, c.P)
	}
	return &Point{X: x3, Y: y3}
}

func (c *Curve) Neg(p *Point) *Point {
	if p.Inf {
		return p
	}
	y := new(big.Int).Sub(c.P, p.Y)
	return &Point{X: new(big.Int).Set(p.X), Y: y}
}

func (c *Curve) ScalarMult(k *big.Int, p *Point) *Point {
	res := &Point{Inf: true}
	add := p
	kk := new(big.Int).Set(k)
	for kk.Sign() > 0 {
		if kk.Bit(0) == 1 {
			res = c.Add(res, add)
		}
		add = c.double(add)
		kk.Rsh(kk, 1)
	}
	return res
}

func (c *Curve) ScalarBaseMult(k *big.Int) *Point { return c.ScalarMult(k, c.G) }

// REDP1 maps a byte string to a curve point with the given parity (used for v).
func (c *Curve) REDP1(x []byte, parity int) *Point {
	h := sha256.Sum256(x)
	cur := h[:]
	for {
		h2 := sha256.Sum256(cur)
		pt := c.LiftX(new(big.Int).SetBytes(h2[:]), parity)
		if pt != nil {
			return pt
		}
		n := new(big.Int).SetBytes(cur)
		n.Add(n, big.NewInt(1))
		cur = leftPad(n.Bytes(), 32)
	}
}

// FF reduces mod the group order.
func (c *Curve) FF(a *big.Int) *big.Int {
	return new(big.Int).Mod(a, c.R)
}

func leftPad(b []byte, n int) []byte {
	if len(b) >= n {
		return b[len(b)-n:]
	}
	out := make([]byte, n)
	copy(out[n-len(b):], b)
	return out
}
