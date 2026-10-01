//go:build linux

package tui

import (
	"fmt"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func openThemePTY(t *testing.T) (terminal, peer *os.File) {
	t.Helper()
	masterFD, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Skipf("pty unavailable: %v", err)
	}
	master := os.NewFile(uintptr(masterFD), "theme-pty-master")
	if err := unix.IoctlSetInt(masterFD, unix.TIOCSPTLCK, 0); err != nil {
		_ = master.Close()
		t.Skipf("cannot unlock pty: %v", err)
	}
	ptyNumber, err := unix.IoctlGetInt(masterFD, unix.TIOCGPTN)
	if err != nil {
		_ = master.Close()
		t.Skipf("cannot identify pty: %v", err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", ptyNumber), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		_ = master.Close()
		t.Skipf("cannot open pty slave: %v", err)
	}
	t.Cleanup(func() {
		_ = slave.Close()
		_ = master.Close()
	})
	return slave, master
}
