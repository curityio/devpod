package upgrade

import (
	"io/fs"
	"strings"

	"golang.org/x/sys/unix"
)

// The inode flags (see chattr) that stop a file, or the entries of a directory, from being renamed or replaced.
// FS_IOC_GETFLAGS and statx use the same values for them.
const (
	immutableFlag  = unix.STATX_ATTR_IMMUTABLE // chattr +i
	appendOnlyFlag = unix.STATX_ATTR_APPEND    // chattr +a
)

// preventingFlags describes the inode flags (see chattr) that stop path, or the entries of path if it is a
// directory, from being renamed or replaced, or returns "" if it has none or they can't be read. Even root can't
// rename such files.
func preventingFlags(path string, info fs.FileInfo) string {
	flags, ok := inodeFlags(path, info)
	if !ok {
		return ""
	}

	var names []string
	if flags&immutableFlag != 0 {
		names = append(names, "immutable (chattr +i)")
	}
	if flags&appendOnlyFlag != 0 {
		names = append(names, "append-only (chattr +a)")
	}
	return strings.Join(names, " and ")
}

// inodeFlags returns the immutable and append-only flags of path, and whether it could read them.
func inodeFlags(path string, info fs.FileInfo) (uint64, bool) {
	if flags, ok := statxFlags(path); ok {
		return flags, true
	}
	// Links can't have these flags, and can't be opened.
	if info.Mode()&fs.ModeSymlink != 0 {
		return 0, true
	}
	return ioctlFlags(path)
}

// statxFlags reads the flags without opening path, on file systems that report them to statx.
func statxFlags(path string) (uint64, bool) {
	var stx unix.Statx_t
	if err := unix.Statx(unix.AT_FDCWD, path, unix.AT_SYMLINK_NOFOLLOW, 0, &stx); err != nil {
		return 0, false
	}
	if stx.Attributes_mask&(immutableFlag|appendOnlyFlag) != immutableFlag|appendOnlyFlag {
		return 0, false
	}
	return stx.Attributes & (immutableFlag | appendOnlyFlag), true
}

// ioctlFlags reads the flags with FS_IOC_GETFLAGS, as chattr does, which needs path to be readable.
func ioctlFlags(path string) (uint64, bool) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return 0, false
	}
	defer func() { _ = unix.Close(fd) }()
	flags, err := unix.IoctlGetUint32(fd, unix.FS_IOC_GETFLAGS)
	if err != nil {
		return 0, false
	}
	return uint64(flags) & (immutableFlag | appendOnlyFlag), true
}
