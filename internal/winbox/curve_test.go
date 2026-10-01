package winbox

import (
	"encoding/hex"
	"math/big"
	"testing"
)

// Vectors emitted by winmirror/gen_vectors.py (validated Python crypto).
const (
	vSalt    = "000102030405060708090a0b0c0d0e0f"
	vGx      = "0000000000000000000000000000000000000000000000000000000000000009"
	vGpubX   = "41833861bd88e6f65072fafbba2684cbd333841db3c284aadcf80cf4d2effd90"
	vGpubPar = 1
	vVx      = "45297e4ade8302992a107996c5ee67f2661c862997e5b48a48935ae0bc021ca0"
	vVpar    = 1
	vI       = "844d1098e667eb63bd300a4b1962a1f8a23141103ced71dae91dd63b09e9c47b"
	vKx      = "18debf3ca6714c72423cfdfceee2ee3293c083ccbb8ae7c80424b9a2102f333a"
	vKpar    = 0
	vWa      = "260b82b791cd6fe3b7d8567d05c6e6be24b590d4407d9d92bb048ed97fb355dc"
	vWbSent  = "61d7aaeb168259237f2913ad2e6f05ccca0ad0ebfe2fb9a5d647c1687fbea2b5"
	vZ       = "006d73177025f5377141a06105782471eef878e8f808b7a01781248e9df2d686"
	vSecret  = "5329dc0eded10a06e0df8bb508296e8d7bb8b3d5b7cbdc6979bba890acf3e855"
	vSendAes = "ff3403fe60b61c362e9c44de7bd0d914"
	vRecvAes = "c34b67ca1073493c2a1ceca7157ba849"
	vSendMac = "3a0ecb0416131e08c6de25a3e1b22d0cd37f8644"
	vRecvMac = "b30b402d360ae5d251793b73dda4d8bb53389725"
)

func mustHex(s string) []byte { b, _ := hex.DecodeString(s); return b }

func TestBasePoint(t *testing.T) {
	c := NewCurve()
	x, par := c.ToMontgomery(c.G)
	if hex.EncodeToString(x) != vGx || par != 0 {
		t.Fatalf("G mont x = %x par %d", x, par)
	}
}

func TestScalarMult(t *testing.T) {
	c := NewCurve()
	k := new(big.Int).SetBytes(mustHex("0707070707070707070707070707070707070707070707070707070707070707"))
	x, par := c.ToMontgomery(c.ScalarBaseMult(k))
	if hex.EncodeToString(x) != vKx || par != vKpar {
		t.Fatalf("k*G = %x par %d, want %s", x, par, vKx)
	}
}

func TestPasswordScalarAndValidator(t *testing.T) {
	c := NewCurve()
	salt := mustHex(vSalt)
	i := c.PasswordScalar("test", "test123", salt)
	if hex.EncodeToString(i) != vI {
		t.Fatalf("i = %x", i)
	}
	gx, gpar := c.ToMontgomery(c.Gpub("test", "test123", salt))
	if hex.EncodeToString(gx) != vGpubX || gpar != vGpubPar {
		t.Fatalf("gpub = %x par %d", gx, gpar)
	}
	vx, vpar := c.ToMontgomery(c.ValidatorPoint("test", "test123", salt))
	if hex.EncodeToString(vx) != vVx || vpar != vVpar {
		t.Fatalf("v = %x par %d", vx, vpar)
	}
}

func TestServerZ(t *testing.T) {
	c := NewCurve()
	salt := mustHex(vSalt)
	a := new(big.Int).SetBytes([]byte{3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3})
	b := new(big.Int).SetBytes([]byte{5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5})
	v := c.ValidatorPoint("test", "test123", salt)
	gpub := c.Gpub("test", "test123", salt)

	Wa, paA := c.ToMontgomery(c.ScalarBaseMult(a))
	if hex.EncodeToString(Wa) != vWa {
		t.Fatalf("Wa = %x", Wa)
	}
	WbSentPt := c.Add(c.ScalarBaseMult(b), c.Neg(v))
	WbSent, _ := c.ToMontgomery(WbSentPt)
	if hex.EncodeToString(WbSent) != vWbSent {
		t.Fatalf("WbSent = %x", WbSent)
	}
	j := sha256sum(Wa, WbSent)
	jint := c.FF(new(big.Int).SetBytes(j))
	WaPt := c.LiftX(new(big.Int).SetBytes(Wa), paA)
	combined := c.Add(WaPt, c.ScalarMult(jint, gpub))
	z, _ := c.ToMontgomery(c.ScalarMult(b, combined))
	if hex.EncodeToString(z) != vZ {
		t.Fatalf("z = %x, want %s", z, vZ)
	}
	secret := sha256sum(z)
	if hex.EncodeToString(secret) != vSecret {
		t.Fatalf("secret = %x", secret)
	}
	sa, ra, sh, rh := StreamKeys(true, secret)
	if hex.EncodeToString(sa) != vSendAes || hex.EncodeToString(ra) != vRecvAes ||
		hex.EncodeToString(sh) != vSendMac || hex.EncodeToString(rh) != vRecvMac {
		t.Fatalf("stream keys mismatch:\n sa %x\n ra %x\n sh %x\n rh %x", sa, ra, sh, rh)
	}
}
