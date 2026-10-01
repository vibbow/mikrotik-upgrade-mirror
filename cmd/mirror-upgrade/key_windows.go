//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// readKey reads one key press from the console without waiting for Enter and without
// echoing it. ok is false when stdin is not a console (piped input): the caller then
// falls back to reading a line. Ctrl+C is returned as the byte 0x03.
func readKey() (key byte, ok bool, err error) {
	h := windows.Handle(os.Stdin.Fd())
	var old uint32
	if windows.GetConsoleMode(h, &old) != nil {
		return 0, false, nil
	}
	// no PROCESSED_INPUT either: Ctrl+C then arrives as the byte 0x03 instead of a signal,
	// so the caller can treat it as "quit" without cmd.exe asking to terminate the batch job
	raw := old &^ (windows.ENABLE_LINE_INPUT | windows.ENABLE_ECHO_INPUT | windows.ENABLE_PROCESSED_INPUT)
	if windows.SetConsoleMode(h, raw) != nil {
		return 0, false, nil
	}
	defer windows.SetConsoleMode(h, old)
	var b [1]byte
	n, err := os.Stdin.Read(b[:])
	if n == 0 {
		return 0, true, err
	}
	return b[0], true, nil
}
