//go:build !windows

package main

// readKey is only implemented for the Windows console; elsewhere answers are read as a
// line (type the letter and press Enter).
func readKey() (key byte, ok bool, err error) { return 0, false, nil }
