package filetransfer

import "testing"

// setUmask does nothing on Windows, which has no umask.
func setUmask(*testing.T, int) {}
