package log

import (
	"os"

	"golang.org/x/sys/windows"
)

// enableVirtualTerminal makes the Windows console behind f interpret ANSI escape sequences,
// like colors, if f is a console.
func enableVirtualTerminal(f *os.File) {
	handle := windows.Handle(f.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return
	}
	_ = windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
}
