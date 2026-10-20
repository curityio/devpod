//go:build !windows

package server

import (
	"fmt"
	"os"
	"syscall"
)

// mayReplaceSocket reports whether this process may remove the stale socket fi.
// Root may remove any user's: the GPG agent forwarding hands its socket to the
// remote user, and a socket is left behind when the container stops.
func mayReplaceSocket(fi os.FileInfo) bool {
	uid := os.Getuid()
	return uid == 0 || ownerUID(fi) == uid
}

// checkPrivateDir returns an error unless dir is a directory, not a symlink,
// that is owned by this process's user and only accessible to it.
func checkPrivateDir(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}

	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if ownerUID(fi) != os.Getuid() {
		return fmt.Errorf("%s belongs to another user", dir)
	}
	if fi.Mode().Perm() != 0o700 {
		return fmt.Errorf("%s has mode %o, expected 700", dir, fi.Mode().Perm())
	}

	return nil
}

func ownerUID(fi os.FileInfo) int {
	stat, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return -1
	}

	return int(stat.Uid)
}
