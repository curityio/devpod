package upgrade

import (
	"path/filepath"
	"testing"
)

func TestInodeFlags(t *testing.T) {
	dir := t.TempDir()
	bin := writeFile(t, filepath.Join(dir, "devpod"), "binary")
	for name, read := range map[string]func(string) (uint64, bool){"statx": statxFlags, "ioctl": ioctlFlags} {
		if flags, ok := read(bin); ok && flags != 0 {
			t.Errorf("%s: flags of a plain file = %#x, want none", name, flags)
		}
	}

	setImmutable(t, bin)
	setAppendOnly(t, dir)
	n := 0
	for name, readFlags := range map[string]func(string) (uint64, bool){"statx": statxFlags, "ioctl": ioctlFlags} {
		flags, ok := readFlags(bin)
		if !ok {
			t.Logf("%s can't read the flags on this file system", name)
			continue
		}
		n++
		if flags != immutableFlag {
			t.Errorf("%s: flags of an immutable file = %#x, want %#x", name, flags, immutableFlag)
		}
		if flags, ok := readFlags(dir); ok && flags != appendOnlyFlag {
			t.Errorf("%s: flags of an append-only directory = %#x, want %#x", name, flags, appendOnlyFlag)
		}
	}
	if n == 0 {
		t.Error("neither statx nor FS_IOC_GETFLAGS can read the flags chattr set")
	}
}
