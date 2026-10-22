//go:build !windows

package upgrade

import (
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
)

// replaceFile moves src to dst, replacing dst if it exists. A running binary can be replaced this way.
func replaceFile(src, dst string) error {
	return os.Rename(src, dst)
}

// checkCanReplace fails if the current user can't rename path or replace it with another file, for the reasons
// that being able to create and remove files in its directory (see checkCanCreateIn) doesn't rule out: the sticky
// bit of the directory, and flags such as immutable on path. It only looks at them, so it never moves or changes
// path.
func checkCanReplace(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("the current user can't replace %s: %w", path, cause(err))
	}
	dir := filepath.Dir(path)
	dirInfo, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("the current user can't replace %s: %w", path, cause(err))
	}

	if err := checkStickyRule(info, dirInfo, os.Geteuid()); err != nil {
		return fmt.Errorf("the current user can't replace %s: %w", path, err)
	}
	if flags := preventingFlags(path, info); flags != "" {
		return fmt.Errorf("%s can't be replaced: it is %s", path, flags)
	}
	return nil
}

// checkStickyRule fails if the file described by info is in a directory with the sticky bit (such as /tmp), where
// only the owner of the file or of the directory, or root, may rename or replace it, and user euid is none of
// them.
func checkStickyRule(info, dirInfo fs.FileInfo, euid int) error {
	if dirInfo.Mode()&fs.ModeSticky == 0 || euid == 0 {
		return nil
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	dirStat, dirOK := dirInfo.Sys().(*syscall.Stat_t)
	if !ok || !dirOK {
		return nil
	}
	if uint64(stat.Uid) == uint64(euid) || uint64(dirStat.Uid) == uint64(euid) {
		return nil
	}
	// The owners of the file and of the directory, and root.
	who := []string{userName(stat.Uid)}
	for _, name := range []string{userName(dirStat.Uid), userName(0)} {
		if !slices.Contains(who, name) {
			who = append(who, name)
		}
	}
	names := who[len(who)-1]
	if len(who) > 1 {
		names = strings.Join(who[:len(who)-1], ", ") + " or " + names
	}
	return fmt.Errorf("its directory has the sticky bit set, so only %s may", names)
}

// copyOwner gives path, which is installed as installed, the owner and group of like. Without root, the current
// user can't give a file to another user, and can only give it a group they are in; then path keeps as much as it
// can, and copyOwner returns an error that says what it lost.
func copyOwner(path, installed string, like fs.FileInfo) error {
	prev, ok := like.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	err := os.Lchown(path, int(prev.Uid), int(prev.Gid))
	if err == nil {
		return nil
	}
	_ = os.Lchown(path, -1, int(prev.Gid))

	info, statErr := os.Lstat(path)
	if statErr != nil {
		return fmt.Errorf("may not have the owner and group of the previous binary: %w", cause(err))
	}
	got, ok := info.Sys().(*syscall.Stat_t)
	if !ok || (got.Uid == prev.Uid && got.Gid == prev.Gid) {
		return nil
	}

	var have, want []string
	if got.Uid != prev.Uid {
		have, want = append(have, "owner "+userName(got.Uid)), append(want, userName(prev.Uid))
	}
	if got.Gid != prev.Gid {
		have, want = append(have, "group "+groupName(got.Gid)), append(want, groupName(prev.Gid))
	}
	return fmt.Errorf("has %s instead of %s like the previous binary (%w). It has the previous binary's mode; to fix it, run `chown %s:%s %s` as a user who may",
		strings.Join(have, " and "), strings.Join(want, " and "), cause(err), userName(prev.Uid), groupName(prev.Gid), installed)
}

// userName returns the name of the user with the given id, or the id if it has no name.
func userName(uid uint32) string {
	id := strconv.FormatUint(uint64(uid), 10)
	if u, err := user.LookupId(id); err == nil {
		return u.Username
	}
	return id
}

// groupName returns the name of the group with the given id, or the id if it has no name.
func groupName(gid uint32) string {
	id := strconv.FormatUint(uint64(gid), 10)
	if g, err := user.LookupGroupId(id); err == nil {
		return g.Name
	}
	return id
}
