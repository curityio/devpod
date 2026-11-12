//go:build !windows

package filetransfer

import (
	"syscall"
	"testing"
)

// setUmask sets the process's umask until the test ends.
func setUmask(t *testing.T, mask int) {
	old := syscall.Umask(mask)
	t.Cleanup(func() { syscall.Umask(old) })
}
