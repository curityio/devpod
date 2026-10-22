package upgrade

import (
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestCheckCanReplaceOpenFile(t *testing.T) {
	bin := writeFile(t, filepath.Join(t.TempDir(), "devpod.exe"), "binary")
	if err := checkCanReplace(bin); err != nil {
		t.Fatalf("checkCanReplace: %v", err)
	}

	// Another process that has the file open without sharing delete access stops it from being renamed.
	p, err := windows.UTF16PtrFromString(bin)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	if err := checkCanReplace(bin); err == nil {
		t.Error("checkCanReplace of a file open without FILE_SHARE_DELETE succeeded, want an error")
	}
	assertContent(t, bin, "binary")
}

func setImmutable(t *testing.T, _ string) {
	t.Skip("Windows files can't be made immutable")
}

func setLinkTime(t *testing.T, _ string, _ time.Time) {
	t.Skip("the tests don't create symlinks on Windows")
}
