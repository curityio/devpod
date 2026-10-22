package upgrade

import (
	"io/fs"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// preventingFlags describes the file flags (see chflags) that stop path, or the entries of path if it is a
// directory, from being renamed or replaced, or returns "" if it has none. Even root can't rename such files.
func preventingFlags(_ string, info fs.FileInfo) string {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}

	var flags []string
	for _, f := range []struct {
		flag uint32
		name string
	}{
		{unix.UF_IMMUTABLE, "immutable (chflags uchg)"},
		{unix.SF_IMMUTABLE, "immutable (chflags schg)"},
		{unix.UF_APPEND, "append-only (chflags uappnd)"},
		{unix.SF_APPEND, "append-only (chflags sappnd)"},
		{unix.SF_RESTRICTED, "protected by System Integrity Protection"},
	} {
		if stat.Flags&f.flag != 0 {
			flags = append(flags, f.name)
		}
	}
	if !info.IsDir() && stat.Flags&unix.SF_NOUNLINK != 0 {
		flags = append(flags, "not removable (chflags sunlnk)")
	}
	return strings.Join(flags, " and ")
}
