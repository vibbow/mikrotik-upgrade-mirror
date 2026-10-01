package winbox

import (
	"net"
	"strings"
	"testing"
)

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
	cliCh, cliErr = ClientHandshake(cs, clientUser, clientPass)
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
