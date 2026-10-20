//go:build !windows

package log

import "os"

// enableVirtualTerminal makes f interpret ANSI escape sequences. Only Windows consoles need it.
func enableVirtualTerminal(*os.File) {}
