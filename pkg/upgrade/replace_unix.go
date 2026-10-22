//go:build !windows

package upgrade

import (
	"os"
	"syscall"
)

// replaceFile moves src to dst, replacing dst if it exists. A running binary can be replaced this way.
func replaceFile(src, dst string) error {
	return os.Rename(src, dst)
}

// copyOwner gives path the owner and group of like, as far as the current user may. Without root, that's at most
// another group the user is in, so failures are ignored.
func copyOwner(path string, like os.FileInfo) {
	if stat, ok := like.Sys().(*syscall.Stat_t); ok {
		_ = os.Lchown(path, int(stat.Uid), int(stat.Gid))
	}
}
