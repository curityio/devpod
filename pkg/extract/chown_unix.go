//go:build !windows

package extract

import (
	"os"
	"syscall"
)

// chownLike gives name the owner and group of like, if it can.
func chownLike(name string, like os.FileInfo) {
	if stat, ok := like.Sys().(*syscall.Stat_t); ok {
		_ = os.Lchown(name, int(stat.Uid), int(stat.Gid))
	}
}
