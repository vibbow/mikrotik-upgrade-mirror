package winbox

import (
	"crypto/rand"
	"errors"
	"io"
	"math/big"
	"net"
	"strings"
	"testing"
)

// clientHandshake is a minimal EC-SRP5 client (what a router does), used only to
// exercise ServerHandshake end to end in-process.
func clientHandshake(rw io.ReadWriter, user, pass string) (*SecureChannel, error) {
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

// runPair connects a client and a server over an in-memory pipe.
func runPair(t *testing.T, serverUser, serverPass, clientUser, clientPass string) (cliCh, srvCh *SecureChannel, cliErr, srvErr error) {
	t.Helper()
	cs, ss := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		srvCh, _, srvErr = ServerHandshake(ss, serverUser, serverPass)
		if srvErr != nil {
			ss.Close() // unblock a client waiting for the confirmation
		}
	}()
	cliCh, cliErr = clientHandshake(cs, clientUser, clientPass)
	<-done
	return
}

func TestHandshakeUserEqualsPassword(t *testing.T) {
	for _, name := range []string{"stable", "longterm"} {
		cli, srv, cerr, serr := runPair(t, name, name, name, name)
		if cerr != nil || serr != nil {
			t.Fatalf("%s/%s: client err=%v server err=%v", name, name, cerr, serr)
		}
		// the two sides must derive compatible record layers
		msg := []byte("M2 hello from the router")
		clientToServer := cli.Encrypt(msg)
		var r Reassembler
		r.Feed(clientToServer)
		_, assembled, ok := r.Next()
		if !ok {
			t.Fatal("no complete record")
		}
		got, err := srv.DecryptAssembled(assembled)
		if err != nil || string(got) != string(msg) {
			t.Fatalf("%s: server could not decrypt client record: %v %q", name, err, got)
		}
	}
}

func TestHandshakeWrongPasswordRejected(t *testing.T) {
	// router types a password other than the account's: must not authenticate
	_, _, cerr, serr := runPair(t, "stable", "stable", "stable", "something-else")
	if cerr == nil {
		t.Fatal("client unexpectedly authenticated with the wrong password")
	}
	if serr == nil || !strings.Contains(serr.Error(), "confirmation mismatch") {
		t.Fatalf("server error = %v, want a confirmation mismatch", serr)
	}
}
