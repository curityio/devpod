//go:build windows

package extract

import "os"

// chownLike does nothing: files have no owner to set on Windows.
func chownLike(_ string, _ os.FileInfo) {}
