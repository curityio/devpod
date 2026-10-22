//go:build !windows

package upgrade

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// checkCanRename fails if the current user can't rename or replace path, assuming it can create files in its
// directory. In a directory with the sticky bit set (such as /tmp), only the owner of a file or of the directory
// can do that.
func checkCanRename(path string) error {
	dir := filepath.Dir(path)
	dirInfo, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if dirInfo.Mode()&os.ModeSticky == 0 {
		return nil
	}

	uid := os.Geteuid()
	if uid == 0 {
		return nil
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if ownerOf(info) == uid || ownerOf(dirInfo) == uid {
		return nil
	}
	return fmt.Errorf("the current user can't replace %s: it doesn't own it, and %s has the sticky bit set", path, dir)
}

func ownerOf(info os.FileInfo) int {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return int(stat.Uid)
	}
	return -1
}

// replaceFile moves src to dst, replacing dst if it exists. A running binary can be replaced this way.
func replaceFile(src, dst string) error {
	return os.Rename(src, dst)
}
