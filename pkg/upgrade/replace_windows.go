//go:build windows

package upgrade

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
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

// checkCanReplace fails if the current user can't rename path or replace it with another file, for the reasons
// that being able to create and remove files in its directory (see checkCanCreateIn) doesn't rule out. It opens
// path with the DELETE access that renaming it needs, which fails if an access control list denies it or another
// process has path open without sharing that access. It never moves or changes path.
func checkCanReplace(path string) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return fmt.Errorf("the current user can't replace %s: %w", path, err)
	}
	h, err := windows.CreateFile(p, windows.DELETE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return fmt.Errorf("the current user can't replace %s: %w", path, err)
	}
	_ = windows.CloseHandle(h)

	// replaceFile moves a binary aside before putting the new one in its place, which works for read-only files.
	// A link is replaced by renaming a new link over it, which Windows refuses if the old one is read-only.
	attrs, err := windows.GetFileAttributes(p)
	if err != nil {
		return fmt.Errorf("the current user can't replace %s: %w", path, err)
	}
	if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 && attrs&windows.FILE_ATTRIBUTE_READONLY != 0 {
		return fmt.Errorf("%s can't be replaced: it is read-only", path)
	}
	return nil
}

// copyOwner does nothing: Windows files don't have Unix owners.
func copyOwner(string, string, fs.FileInfo) error {
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
