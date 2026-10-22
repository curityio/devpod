//go:build !darwin && !linux

package upgrade

import "io/fs"

// preventingFlags returns "": file flags that stop a file from being renamed are only looked at on macOS and
// Linux. Windows files have no such flags; its read-only attribute is handled where it matters.
func preventingFlags(string, fs.FileInfo) string {
	return ""
}
