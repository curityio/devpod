//go:build windows

package upgrade

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// replaceFile moves src to dst, replacing dst if it exists. Windows can't replace a running binary, but it can
// rename it, so dst is moved out of the way first, to a hidden .old file named after the time. A running binary
// can't be deleted either, so .old files are removed when they no longer run, here or on a later upgrade.
func replaceFile(src, dst string) error {
	if _, err := os.Lstat(dst); err != nil {
		return os.Rename(src, dst)
	}

	oldPattern := filepath.Join(filepath.Dir(dst), "."+filepath.Base(dst)+".old*")
	if olds, err := filepath.Glob(oldPattern); err == nil {
		for _, old := range olds {
			_ = os.Remove(old)
		}
	}

	old := filepath.Join(filepath.Dir(dst), fmt.Sprintf(".%s.old-%d", filepath.Base(dst), time.Now().UnixNano()))
	if err := os.Rename(dst, old); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err != nil {
		if rollbackErr := os.Rename(old, dst); rollbackErr != nil {
			return fmt.Errorf("%w; restoring the previous binary from %s also failed: %w", err, old, rollbackErr)
		}
		return err
	}
	if err := os.Remove(old); err != nil {
		hideFile(old)
	}
	return nil
}

// copyOwner does nothing: Windows files don't have Unix owners.
func copyOwner(string, os.FileInfo) {}

func hideFile(path string) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return
	}
	attrs, err := syscall.GetFileAttributes(p)
	if err != nil {
		return
	}
	_ = syscall.SetFileAttributes(p, attrs|syscall.FILE_ATTRIBUTE_HIDDEN)
}
