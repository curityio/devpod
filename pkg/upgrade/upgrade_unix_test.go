//go:build !windows

package upgrade

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/loft-sh/devpod/pkg/log"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

func TestCheckStickyRule(t *testing.T) {
	const alice, bob, carol = 1001, 1002, 1003
	tests := []struct {
		name            string
		sticky          bool
		owner, dirOwner uint32
		euid            int
		wantErr         bool
	}{
		{"not sticky", false, alice, bob, carol, false},
		{"owns the file", true, alice, bob, alice, false},
		{"owns the directory", true, alice, bob, bob, false},
		{"root", true, alice, bob, 0, false},
		{"owns neither", true, alice, bob, carol, true},
	}
	for _, tt := range tests {
		dirMode := fs.ModeDir | 0777
		if tt.sticky {
			dirMode |= fs.ModeSticky
		}
		err := checkStickyRule(fakeInfo{mode: 0755, uid: tt.owner}, fakeInfo{mode: dirMode, uid: tt.dirOwner}, tt.euid)
		if (err != nil) != tt.wantErr {
			t.Errorf("%s: checkStickyRule = %v, want error %v", tt.name, err, tt.wantErr)
		}
		if err != nil && !strings.Contains(err.Error(), "sticky bit") {
			t.Errorf("%s: checkStickyRule = %v, want a sticky bit error", tt.name, err)
		}
	}

	// The error names who may, each once.
	err := checkStickyRule(fakeInfo{mode: 0755}, fakeInfo{mode: fs.ModeDir | fs.ModeSticky | 0777}, carol)
	if want := "only " + userName(0) + " may"; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("checkStickyRule = %v, want it to say %q", err, want)
	}
}

func TestCheckCanReplaceInStickyDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0777|fs.ModeSticky); err != nil {
		t.Fatal(err)
	}
	if lstat(t, dir).Mode()&fs.ModeSticky == 0 {
		t.Skip("the file system doesn't keep the sticky bit")
	}
	bin := writeFile(t, filepath.Join(dir, "devpod"), "binary")
	// The current user owns both.
	if err := checkCanReplace(bin); err != nil {
		t.Errorf("checkCanReplace in a sticky directory of the current user: %v", err)
	}
}

func TestCheckCanReplaceAppendOnly(t *testing.T) {
	dir := t.TempDir()
	bin := writeFile(t, filepath.Join(dir, "devpod"), "binary")
	setAppendOnly(t, bin)
	if err := checkCanReplace(bin); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Errorf("checkCanReplace of an append-only file = %v, want an append-only error", err)
	}
}

func TestCheckCanCreateInAppendOnlyDirectory(t *testing.T) {
	dir := t.TempDir()
	setAppendOnly(t, dir)
	// Files could be created there but not renamed or removed, so the probe file isn't even created.
	if err := checkCanCreateIn(dir); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Errorf("checkCanCreateIn of an append-only directory = %v, want an append-only error", err)
	}
	if got := readDir(t, dir); len(got) != 0 {
		t.Errorf("checkCanCreateIn left %v", got)
	}
}

func TestCopyOwner(t *testing.T) {
	dir := t.TempDir()
	mine := writeFile(t, filepath.Join(dir, "mine"), "")
	other := writeFile(t, filepath.Join(dir, "other"), "")
	if err := copyOwner(other, other, lstat(t, mine)); err != nil {
		t.Errorf("copyOwner of the current user's file: %v", err)
	}

	// Another group the current user is in is kept.
	groups, _ := os.Getgroups()
	for _, gid := range groups {
		if gid == os.Getegid() || os.Lchown(mine, -1, gid) != nil {
			continue
		}
		if err := copyOwner(other, other, lstat(t, mine)); err != nil {
			t.Errorf("copyOwner with group %d: %v", gid, err)
		}
		if got := lstat(t, other).Sys().(*syscall.Stat_t).Gid; got != uint32(gid) {
			t.Errorf("group = %d, want %d", got, gid)
		}
		break
	}
}

func TestApplyWarnsWhenOwnerIsLost(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root may give files to anyone")
	}
	rootOwned := lstat(t, "/")
	if rootOwned.Sys().(*syscall.Stat_t).Uid != 0 {
		t.Skip("/ isn't owned by root")
	}
	dir := t.TempDir()
	bin := writeFile(t, filepath.Join(dir, "devpod"), "old")
	downloaded := writeFile(t, filepath.Join(dir, downloadPrefix+"1"), "new")

	var out bytes.Buffer
	in := &install{binary: bin, like: rootOwned}
	if err := in.apply(downloaded, log.NewStreamLogger(&out, &out, logrus.InfoLevel)); err != nil {
		t.Fatal(err)
	}
	assertContent(t, bin, "new")
	if got := out.String(); !strings.Contains(got, "owner "+userName(uint32(os.Geteuid()))+" and group") || !strings.Contains(got, "chown "+userName(0)) || !strings.Contains(got, bin) {
		t.Errorf("apply logged %q, want a warning that the owner isn't kept", got)
	}
	if got := readDir(t, dir); !slices.Equal(got, []string{"devpod"}) {
		t.Errorf("files = %v, want only the binary", got)
	}
}

// fakeInfo is a file owned by uid.
type fakeInfo struct {
	mode fs.FileMode
	uid  uint32
}

func (f fakeInfo) Name() string       { return "fake" }
func (f fakeInfo) Size() int64        { return 0 }
func (f fakeInfo) Mode() fs.FileMode  { return f.mode }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return f.mode.IsDir() }
func (f fakeInfo) Sys() any           { return &syscall.Stat_t{Uid: f.uid} }

// setImmutable makes path immutable until the test ends, or skips the test if it can't.
func setImmutable(t *testing.T, path string) {
	t.Helper()
	switch runtime.GOOS {
	case "darwin":
		setFlag(t, path, []string{"chflags", "uchg"}, []string{"chflags", "nouchg"})
	case "linux":
		setFlag(t, path, []string{"chattr", "+i"}, []string{"chattr", "-i"})
	default:
		t.Skip("doesn't know how to make files immutable on " + runtime.GOOS)
	}
}

// setAppendOnly makes path append-only until the test ends, or skips the test if it can't.
func setAppendOnly(t *testing.T, path string) {
	t.Helper()
	switch runtime.GOOS {
	case "darwin":
		setFlag(t, path, []string{"chflags", "uappnd"}, []string{"chflags", "nouappnd"})
	case "linux":
		setFlag(t, path, []string{"chattr", "+a"}, []string{"chattr", "-a"})
	default:
		t.Skip("doesn't know how to make files append-only on " + runtime.GOOS)
	}
}

func setFlag(t *testing.T, path string, set, unset []string) {
	t.Helper()
	// Linux only lets root set these flags, and not on every file system.
	if out, err := exec.Command(set[0], append(set[1:], path)...).CombinedOutput(); err != nil {
		t.Skipf("%s: %v: %s", strings.Join(set, " "), err, out)
	}
	t.Cleanup(func() { _ = exec.Command(unset[0], append(unset[1:], path)...).Run() })
}

func setLinkTime(t *testing.T, path string, mtime time.Time) {
	t.Helper()
	tv := unix.NsecToTimeval(mtime.UnixNano())
	if err := unix.Lutimes(path, []unix.Timeval{tv, tv}); err != nil {
		t.Fatal(err)
	}
}
