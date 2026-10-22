//go:build windows

package upgrade

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// checkCanRename fails if the current user can't rename path. Windows decides that with access control lists, so
// the check renames the file and back.
func checkCanRename(path string) error {
	probe := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".permission-check")
	if err := os.Rename(path, probe); err != nil {
		return fmt.Errorf("the current user can't replace %s: %w", path, err)
	}
	if err := os.Rename(probe, path); err != nil {
		return fmt.Errorf("checking whether %s can be replaced, it was renamed to %s and couldn't be renamed back: %w", path, probe, err)
	}
	return nil
}

// replaceFile moves src to dst, replacing dst if it exists. Windows can't replace a running binary, but it can
// rename it, so dst is moved out of the way first. It can't be deleted while it runs either, so it is left as a
// hidden .old file, which the next upgrade removes.
func replaceFile(src, dst string) error {
	if _, err := os.Lstat(dst); err != nil {
		return os.Rename(src, dst)
	}

	old := filepath.Join(filepath.Dir(dst), "."+filepath.Base(dst)+".old")
	_ = os.Remove(old)
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
