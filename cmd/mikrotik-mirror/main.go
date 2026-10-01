// mikrotik-mirror serves RouterOS packages to routers over the Winbox
// local-update protocol. It never downloads anything itself: packages are put in
// the channel directories by mirror-push (run from a machine with a fast link).
package main

import (
	"flag"
	"log"

	"github.com/local/mikrotik-mirror/internal/mirror"
)

func main() {
	listen := flag.String("listen", "0.0.0.0:8291", "winbox listen address")
	stableDir := flag.String("stable-dir", "packages/stable", "stable channel package dir")
	ltDir := flag.String("longterm-dir", "packages/long-term", "long-term channel package dir")
	stableUser := flag.String("stable-user", "stable", "username that selects the stable channel")
	ltUser := flag.String("longterm-user", "longterm", "username that selects the long-term channel")
	stablePass := flag.String("stable-password", "", "password for the stable account (default: same as its username)")
	ltPass := flag.String("longterm-password", "", "password for the long-term account (default: same as its username)")
	flag.Parse()

	// The Winbox EC-SRP5 handshake needs the server to know each account's password
	// (it derives the session keys from it), so a router can only connect with exactly
	// that password. The packages are public and signed, so by default each password is
	// simply the username: stable/stable and longterm/longterm.
	sPass, lPass := *stablePass, *ltPass
	if sPass == "" {
		sPass = *stableUser
	}
	if lPass == "" {
		lPass = *ltUser
	}
	if *stableUser == "" || *ltUser == "" || *stableUser == *ltUser {
		log.Fatal("--stable-user and --longterm-user must be non-empty and different from each other")
	}

	srv := &mirror.Server{
		Listen: *listen,
		Accounts: map[string]mirror.Account{
			*stableUser: {Username: *stableUser, Password: sPass, Dir: *stableDir},
			*ltUser:     {Username: *ltUser, Password: lPass, Dir: *ltDir},
		},
	}
	log.Printf("channels:\n%s", srv.Describe())
	log.Fatal(srv.Run())
}
