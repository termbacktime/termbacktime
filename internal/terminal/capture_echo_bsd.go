//go:build darwin || freebsd

package terminal

import (
	"golang.org/x/sys/unix"
	"os"
)

func captureEcho(f *os.File, on bool) error {
	t, err := unix.IoctlGetTermios(int(f.Fd()), unix.TIOCGETA)
	if err != nil {
		return err
	}
	if on {
		t.Lflag |= unix.ECHO
	} else {
		t.Lflag &^= unix.ECHO
	}
	return unix.IoctlSetTermios(int(f.Fd()), unix.TIOCSETA, t)
}
