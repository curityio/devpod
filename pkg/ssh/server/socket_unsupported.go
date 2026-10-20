//go:build windows

package server

import "os"

// mayReplaceSocket reports whether this process may remove the stale socket fi.
// Windows doesn't expose a file's owner here, so it never may.
func mayReplaceSocket(fi os.FileInfo) bool {
	return false
}

// checkPrivateDir doesn't check anything on Windows, where Unix file modes
// and owners don't apply.
func checkPrivateDir(dir string) error {
	return nil
}
