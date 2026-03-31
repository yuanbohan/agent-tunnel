package client

import (
	"os"

	"golang.org/x/term"
)

// EnterRawMode puts stdin into raw mode and returns a restore function.
// Always call the restore function (e.g. via defer) to reset the terminal.
func EnterRawMode() (restore func(), err error) {
	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return func() {}, err
	}
	return func() { term.Restore(fd, oldState) }, nil
}
