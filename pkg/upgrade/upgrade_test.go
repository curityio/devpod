package upgrade

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/loft-sh/devpod/pkg/download"
	"github.com/loft-sh/devpod/pkg/log"
)

func TestParseVersion(t *testing.T) {
	tests := []struct {
		in      string
		numbers []int
		suffix  string
		build   string
		stable  bool
	}{
		{"v0.8.0", []int{0, 8, 0}, "", "", true},
		{"1.2.3", []int{1, 2, 3}, "", "", true},
		{"v10.20.30", []int{10, 20, 30}, "", "", true},
		{"v0.8.0-alpha.1", []int{0, 8, 0}, "-alpha.1", "", false},
		{"1.2.3alpha", []int{1, 2, 3}, "alpha", "", false},
		{"1.2.3-beta-1", []int{1, 2, 3}, "-beta-1", "", false},
		{"1.2.3.4", []int{1, 2, 3, 4}, "", "", false},
		{"1.2.3.4-rc1", []int{1, 2, 3, 4}, "-rc1", "", false},
		{"v0.0.0-dev.20261022101500", []int{0, 0, 0}, "-dev.20261022101500", "", false},
		{"v0.8.0+curity.1", []int{0, 8, 0}, "", "curity.1", false},
		{"v0.8.0-rc.1+abc", []int{0, 8, 0}, "-rc.1", "abc", false},
	}
	for _, tt := range tests {
		v, err := parseVersion(tt.in)
		if err != nil {
			t.Errorf("parseVersion(%q): %v", tt.in, err)
			continue
		}
		if !slices.Equal(v.numbers, tt.numbers) || v.suffix != tt.suffix || v.build != tt.build {
			t.Errorf("parseVersion(%q) = %v %q %q, want %v %q %q", tt.in, v.numbers, v.suffix, v.build, tt.numbers, tt.suffix, tt.build)
		}
		if v.isStable() != tt.stable {
			t.Errorf("parseVersion(%q).isStable() = %v, want %v", tt.in, v.isStable(), tt.stable)
		}
	}
}

func TestParseVersionInvalid(t *testing.T) {
	for _, in := range []string{"", "dev", "latest", "v1", "1.2", "1.2.x", "x1.2.3", "99999999999999999999.0.0"} {
		if _, err := parseVersion(in); err == nil {
			t.Errorf("parseVersion(%q) succeeded, want an error", in)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0},
		{"v1.2.3", "1.2.3", 0},
		{"1.2.3", "1.2.4", -1},
		{"1.3.0", "1.2.9", 1},
		{"2.0.0", "1.99.99", 1},
		{"0.10.0", "0.9.0", 1},
		{"1.2.3.4", "1.2.3", 1},
		{"1.2.3.0", "1.2.3", 0},
		{"1.2.3-alpha.1", "1.2.3", -1},
		{"1.2.3", "1.2.3-beta-1", 1},
		{"1.2.3-alpha", "1.2.3-beta", 0},
		{"1.2.4-alpha", "1.2.3", 1},
		{"0.0.0-dev.20261022101500", "0.8.0", -1},
		{"1.2.3+abc", "1.2.3", 0},
		{"1.2.3-rc.1+abc", "1.2.3", -1},
	}
	for _, tt := range tests {
		a, err := parseVersion(tt.a)
		if err != nil {
			t.Fatal(err)
		}
		b, err := parseVersion(tt.b)
		if err != nil {
			t.Fatal(err)
		}
		if got := a.compare(b); got != tt.want {
			t.Errorf("compare(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestLatestStableRelease(t *testing.T) {
	asset := cliAssetName("linux", "amd64")
	withAsset := func(tag string) download.GithubRelease {
		return download.GithubRelease{TagName: tag, Assets: []download.GithubReleaseAsset{{ID: 1, Name: "DevPod_linux_amd64.AppImage"}, {ID: 2, Name: asset}}}
	}

	draft := withAsset("v0.13.0")
	draft.Draft = true
	prerelease := withAsset("v0.12.0")
	prerelease.Prerelease = true
	releases := []download.GithubRelease{
		withAsset("v0.9.0-alpha.1"),
		withAsset("v0.9.0.1"),
		withAsset("v0.9.0+build.1"),
		draft,
		prerelease,
		{TagName: "v0.11.0", Assets: []download.GithubReleaseAsset{{Name: cliAssetName("darwin", "arm64")}}},
		withAsset("nightly"),
		withAsset("v0.10.0"),
		withAsset("v0.8.0"),
	}

	rel, a, v := latestStableRelease(releases, asset)
	if rel == nil {
		t.Fatal("no release found")
	}
	if rel.TagName != "v0.10.0" {
		t.Errorf("latest release = %s, want v0.10.0", rel.TagName)
	}
	if a.Name != asset {
		t.Errorf("asset = %s, want %s", a.Name, asset)
	}
	if !slices.Equal(v.numbers, []int{0, 10, 0}) {
		t.Errorf("version = %v, want [0 10 0]", v.numbers)
	}

	if rel, _, _ := latestStableRelease(releases[:5], asset); rel != nil {
		t.Errorf("latest release = %s, want none", rel.TagName)
	}
}

func TestSameTag(t *testing.T) {
	if !sameTag("v0.8.0", "0.8.0") || !sameTag("0.8.0", "0.8.0") || !sameTag("v0.8.0", "v0.8.0") {
		t.Error("sameTag doesn't ignore the leading v")
	}
	if sameTag("v0.8.0", "v0.8.0-alpha.1") {
		t.Error("sameTag(v0.8.0, v0.8.0-alpha.1) = true")
	}
}

func TestCliAssetName(t *testing.T) {
	if got := cliAssetName("darwin", "arm64"); got != "devpod-darwin-arm64" {
		t.Errorf("cliAssetName(darwin, arm64) = %s", got)
	}
	if got := cliAssetName("windows", "amd64"); got != "devpod-windows-amd64.exe" {
		t.Errorf("cliAssetName(windows, amd64) = %s", got)
	}
}

func TestCheckCanCreateIn(t *testing.T) {
	dir := t.TempDir()
	if err := checkCanCreateIn(dir); err != nil {
		t.Errorf("checkCanCreateIn in a writable directory: %v", err)
	}
	if entries := readDir(t, dir); len(entries) != 0 {
		t.Errorf("checkCanCreateIn left files behind: %v", entries)
	}

	skipIfPermissionsDontApply(t)
	readOnly(t, dir)
	if err := checkCanCreateIn(dir); err == nil {
		t.Error("checkCanCreateIn in a read-only directory succeeded, want an error")
	}
}

func TestInvokedBinary(t *testing.T) {
	skipIfNoSymlinks(t)
	dir := t.TempDir()
	exe := writeFile(t, filepath.Join(dir, "devpod-mine"), "binary")
	other := writeFile(t, filepath.Join(dir, "other"), "binary")
	link := filepath.Join(dir, "devpod")
	if err := os.Symlink("devpod-mine", link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	tests := []struct {
		name, arg0, want string
	}{
		{"link by path", link, link},
		{"link found in PATH", "devpod", link},
		{"the binary itself", exe, exe},
		{"another file", other, exe},
		{"another name in PATH", "other", exe},
		{"not found", "missing", exe},
		{"nonsense", "", exe},
	}
	for _, tt := range tests {
		// exe is what os.Executable returns on Linux, where it resolves links.
		if got := invokedBinary(tt.arg0, exe); got != tt.want {
			t.Errorf("%s: invokedBinary(%q) = %s, want %s", tt.name, tt.arg0, got, tt.want)
		}
	}
}

func TestCheckCanReplace(t *testing.T) {
	dir := t.TempDir()
	bin := writeFile(t, filepath.Join(dir, "devpod"), "binary")
	before, err := os.Lstat(bin)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkCanReplace(bin); err != nil {
		t.Errorf("checkCanReplace in a writable directory: %v", err)
	}
	assertContent(t, bin, "binary")
	if !os.SameFile(before, lstat(t, bin)) {
		t.Error("checkCanReplace replaced the file")
	}
	if got := readDir(t, dir); !slices.Equal(got, []string{"devpod"}) {
		t.Errorf("checkCanReplace left %v", got)
	}

	if err := checkCanReplace(filepath.Join(dir, "missing")); err == nil {
		t.Error("checkCanReplace of a missing file succeeded, want an error")
	}
}

func TestCheckPermissionsLeavesBinaryInPlace(t *testing.T) {
	skipIfNoSymlinks(t)
	root := t.TempDir()
	releases := filepath.Join(root, "releases")
	binDir := filepath.Join(root, "bin")
	mkdirs(t, releases, binDir)
	previous := writeFile(t, filepath.Join(releases, "devpod-mine"), "old")
	link := filepath.Join(binDir, "devpod")
	if err := os.Symlink(previous, link); err != nil {
		t.Fatal(err)
	}
	in, err := planInstall(link, "v0.8.0")
	if err != nil {
		t.Fatal(err)
	}

	// Several checks at once, as with concurrent dry runs, don't get in each other's way, and the link and the
	// binary stay where they are all along.
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range cap(errs) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				if err := in.checkPermissions(); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(errs)
		close(done)
	}()
	for running := true; running; {
		select {
		case <-done:
			running = false
		default:
		}
		for _, path := range []string{link, previous} {
			if _, err := os.Lstat(path); err != nil {
				t.Fatalf("%s is gone while checking permissions: %v", path, err)
			}
		}
	}
	for err := range errs {
		t.Errorf("checkPermissions: %v", err)
	}
	if got := readDir(t, binDir); !slices.Equal(got, []string{"devpod"}) {
		t.Errorf("checkPermissions left %v in %s", got, binDir)
	}
	if got := readDir(t, releases); !slices.Equal(got, []string{"devpod-mine"}) {
		t.Errorf("checkPermissions left %v in %s", got, releases)
	}
}

func TestCreateLinkNamesAreUnique(t *testing.T) {
	skipIfNoSymlinks(t)
	dir := t.TempDir()
	// A link that an interrupted upgrade left behind doesn't get in the way.
	if err := os.Symlink("devpod-v0.7.0", filepath.Join(dir, linkPrefix+"leftover")); err != nil {
		t.Fatal(err)
	}
	a, err := createLink(dir, linkPrefix, "devpod-v0.8.0")
	if err != nil {
		t.Fatal(err)
	}
	b, err := createLink(dir, linkPrefix, "devpod-v0.8.0")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Errorf("createLink returned %s twice", a)
	}
	if got := readDir(t, dir); len(got) != 3 {
		t.Errorf("files = %v, want 3 links", got)
	}
}

func TestCause(t *testing.T) {
	dir := t.TempDir()
	_, statErr := os.Lstat(filepath.Join(dir, "missing"))
	renameErr := os.Rename(filepath.Join(dir, "missing"), filepath.Join(dir, "other"))
	for _, err := range []error{statErr, renameErr, fmt.Errorf("check: %w", statErr)} {
		got := cause(err)
		// The system error, without the operation and the path.
		if !errors.Is(got, fs.ErrNotExist) || strings.Contains(got.Error(), dir) {
			t.Errorf("cause(%v) = %v, want the system error", err, got)
		}
	}

	err := fmt.Errorf("download: %w", context.Canceled)
	if got := cause(err); !errors.Is(got, context.Canceled) || got.Error() != err.Error() {
		t.Errorf("cause(%v) = %v, want it unchanged", err, got)
	}
}

func TestUpgradeFailsBeforeDownloadForImmutableBinary(t *testing.T) {
	dir := t.TempDir()
	bin := writeFile(t, filepath.Join(dir, "devpod"), "old")
	setImmutable(t, bin)
	gh := newFakeGitHub("v0.8.0")
	setUp(t, gh, "v0.7.0", bin)

	for _, opts := range []Options{{}, {DryRun: true}} {
		err := Upgrade(context.Background(), opts, log.Discard)
		if err == nil || !strings.Contains(err.Error(), "immutable") {
			t.Errorf("Upgrade(%+v) of an immutable binary = %v, want an immutable error", opts, err)
		}
	}
	assertNoDownloads(t, gh)
	assertContent(t, bin, "old")
	if got := readDir(t, dir); !slices.Equal(got, []string{"devpod"}) {
		t.Errorf("files = %v, want only the binary", got)
	}
}

func TestCheckCanCreateLink(t *testing.T) {
	skipIfNoSymlinks(t)
	dir := t.TempDir()
	if err := checkCanCreateLink(dir, "devpod-v0.8.0"); err != nil {
		t.Errorf("checkCanCreateLink: %v", err)
	}
	if entries := readDir(t, dir); len(entries) != 0 {
		t.Errorf("checkCanCreateLink left files behind: %v", entries)
	}
}

func TestRunningBinary(t *testing.T) {
	path, err := runningBinary()
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if !sameFile(t, path, exe) {
		t.Errorf("runningBinary() = %s, which isn't the running binary %s", path, exe)
	}
}

func TestUpgradeReplacesBinary(t *testing.T) {
	gh := newFakeGitHub("v0.8.0", "v0.9.0-alpha.1")
	bin := writeFile(t, filepath.Join(t.TempDir(), "devpod"), "old")
	setUp(t, gh, "v0.7.0", bin)

	if err := Upgrade(context.Background(), Options{}, log.Discard); err != nil {
		t.Fatal(err)
	}
	assertContent(t, bin, "devpod v0.8.0")
	if entries := readDir(t, filepath.Dir(bin)); len(entries) != 1 {
		t.Errorf("upgrade left files behind: %v", entries)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(bin)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0111 == 0 {
			t.Errorf("mode = %v, want an executable", info.Mode())
		}
	}
}

func TestUpgradeWhenReleaseListIsEmpty(t *testing.T) {
	gh := newFakeGitHub("v0.7.0", "v0.8.0")
	gh.add("v0.9.0-alpha.1", true)
	gh.emptyList = true
	bin := writeFile(t, filepath.Join(t.TempDir(), "devpod"), "old")
	setUp(t, gh, "v0.7.0", bin)

	if err := Upgrade(context.Background(), Options{}, log.Discard); err != nil {
		t.Fatal(err)
	}
	assertContent(t, bin, "devpod v0.8.0")

	gh = newFakeGitHub()
	gh.add("v0.9.0-alpha.1", true)
	setUp(t, gh, "v0.8.0", bin)
	if err := Upgrade(context.Background(), Options{}, log.Discard); err == nil || !strings.Contains(err.Error(), "no stable release") {
		t.Errorf("Upgrade() without stable releases = %v, want a no stable release error", err)
	}
}

func TestUpgradeWithoutNewerRelease(t *testing.T) {
	for _, current := range []string{"v0.8.0", "0.8.0", "v0.8.0+build.1", "v0.9.0", "v0.8.0.1"} {
		t.Run(current, func(t *testing.T) {
			gh := newFakeGitHub("v0.8.0", "v0.9.0-alpha.1", "v0.9.0.1")
			gh.add("v0.10.0", true)
			bin := writeFile(t, filepath.Join(t.TempDir(), "devpod"), "old")
			setUp(t, gh, current, bin)

			if err := Upgrade(context.Background(), Options{}, log.Discard); err != nil {
				t.Fatal(err)
			}
			assertContent(t, bin, "old")
			assertNoDownloads(t, gh)
		})
	}
}

func TestUpgradeDryRun(t *testing.T) {
	gh := newFakeGitHub("v0.8.0")
	bin := writeFile(t, filepath.Join(t.TempDir(), "devpod"), "old")
	setUp(t, gh, "v0.8.0-alpha.1", bin)

	for _, opts := range []Options{{DryRun: true}, {DryRun: true, Version: "v0.8.0"}} {
		if err := Upgrade(context.Background(), opts, log.Discard); err != nil {
			t.Fatal(err)
		}
	}
	assertContent(t, bin, "old")
	assertNoDownloads(t, gh)
}

func TestUpgradeChecksPermissionsBeforeDownload(t *testing.T) {
	skipIfPermissionsDontApply(t)
	gh := newFakeGitHub("v0.8.0")
	bin := writeFile(t, filepath.Join(t.TempDir(), "devpod"), "old")
	readOnly(t, filepath.Dir(bin))
	setUp(t, gh, "v0.7.0", bin)

	err := Upgrade(context.Background(), Options{}, log.Discard)
	if err == nil || !strings.Contains(err.Error(), "can't create files in") {
		t.Fatalf("Upgrade() = %v, want a permission error", err)
	}
	assertContent(t, bin, "old")
	assertNoDownloads(t, gh)

	// A dry run fails too, so that scripts can tell.
	err = Upgrade(context.Background(), Options{DryRun: true}, log.Discard)
	if err == nil || !strings.Contains(err.Error(), "would fail") {
		t.Errorf("dry run = %v, want an error saying the upgrade would fail", err)
	}
	assertNoDownloads(t, gh)
}

func TestUpgradeKeepsMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows files don't have Unix modes")
	}
	gh := newFakeGitHub("v0.8.0")
	bin := writeFile(t, filepath.Join(t.TempDir(), "devpod"), "old")
	if err := os.Chmod(bin, 0750); err != nil {
		t.Fatal(err)
	}
	setUp(t, gh, "v0.7.0", bin)

	if err := Upgrade(context.Background(), Options{}, log.Discard); err != nil {
		t.Fatal(err)
	}
	assertContent(t, bin, "devpod v0.8.0")
	info, err := os.Stat(bin)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0750 {
		t.Errorf("mode = %v, want 0750", info.Mode().Perm())
	}
}

func TestUpgradeCleansUpDownloads(t *testing.T) {
	gh := newFakeGitHub("v0.8.0")
	gh.stalled = make(chan struct{})
	dir := t.TempDir()
	bin := writeFile(t, filepath.Join(dir, "devpod"), "old")
	stale := writeFile(t, filepath.Join(dir, downloadPrefix+"111"), "partial")
	makeOld(t, stale)
	recent := writeFile(t, filepath.Join(dir, downloadPrefix+"222"), "partial")
	setUp(t, gh, "v0.7.0", bin)

	// The download stalls halfway, until the context is cancelled, as with Ctrl-C.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-gh.stalled:
			cancel()
		case <-ctx.Done():
		}
	}()
	err := Upgrade(ctx, Options{}, log.Discard)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Upgrade() of a stalled download = %v, want it cancelled", err)
	}
	if got := gh.downloads.Load(); got != 1 {
		t.Errorf("downloads = %d, want 1", got)
	}
	assertContent(t, bin, "old")
	got := readDir(t, dir)
	want := []string{filepath.Base(recent), "devpod"}
	if !slices.Equal(got, want) {
		t.Errorf("files = %v, want %v", got, want)
	}
}

func TestUpgradeVersionFlag(t *testing.T) {
	gh := newFakeGitHub("v0.8.0")
	gh.add("v0.8.0-alpha.1", true)
	bin := writeFile(t, filepath.Join(t.TempDir(), "devpod"), "old")
	// The current version doesn't matter when the version to install is given.
	setUp(t, gh, "dev", bin)

	if err := Upgrade(context.Background(), Options{Version: "0.8.0-alpha.1"}, log.Discard); err != nil {
		t.Fatal(err)
	}
	assertContent(t, bin, "devpod v0.8.0-alpha.1")

	err := Upgrade(context.Background(), Options{Version: "v0.9.9"}, log.Discard)
	if err == nil || !strings.Contains(err.Error(), "couldn't be found") {
		t.Errorf("Upgrade(v0.9.9) = %v, want a not found error", err)
	}

	currentVersion = func() string { return "v0.8.0" }
	if err := Upgrade(context.Background(), Options{Version: "0.8.0"}, log.Discard); err != nil {
		t.Fatal(err)
	}
	assertContent(t, bin, "devpod v0.8.0-alpha.1")
	if got := gh.downloads.Load(); got != 1 {
		t.Errorf("downloads = %d, want 1", got)
	}

	// Without --version, the current version must be known.
	currentVersion = func() string { return "dev" }
	if err := Upgrade(context.Background(), Options{}, log.Discard); err == nil {
		t.Error("Upgrade() of an unknown version succeeded, want an error")
	}
}

func TestUpgradeThroughLink(t *testing.T) {
	skipIfNoSymlinks(t)
	for _, relative := range []bool{false, true} {
		t.Run("relative="+strconv.FormatBool(relative), func(t *testing.T) {
			root := t.TempDir()
			releases := filepath.Join(root, "releases")
			binDir := filepath.Join(root, "bin")
			mkdirs(t, releases, binDir)
			previous := writeFile(t, filepath.Join(releases, "devpod-mine"), "old")
			linkTarget := previous
			if relative {
				linkTarget = filepath.Join("..", "releases", "devpod-mine")
			}
			link := filepath.Join(binDir, "devpod")
			if err := os.Symlink(linkTarget, link); err != nil {
				t.Fatal(err)
			}
			// What interrupted upgrades left in both directories is removed, if it is old enough.
			makeOld(t, writeFile(t, filepath.Join(releases, downloadPrefix+"111"), "partial"))
			makeOld(t, writeFile(t, filepath.Join(binDir, permissionCheckPrefix+"111"), ""))
			recent := writeFile(t, filepath.Join(binDir, downloadPrefix+"222"), "partial")
			staleLink := filepath.Join(binDir, linkPrefix+"111")
			if err := os.Symlink("devpod-v0.7.0", staleLink); err != nil {
				t.Fatal(err)
			}
			makeOld(t, staleLink)

			gh := newFakeGitHub("v0.8.0")
			setUp(t, gh, "v0.7.0", link)
			if err := Upgrade(context.Background(), Options{}, log.Discard); err != nil {
				t.Fatal(err)
			}

			assertContent(t, previous, "old")
			assertContent(t, filepath.Join(releases, "devpod-v0.8.0"), "devpod v0.8.0")
			wantTarget := filepath.Join(filepath.Dir(linkTarget), "devpod-v0.8.0")
			if got, err := os.Readlink(link); err != nil || got != wantTarget {
				t.Errorf("link points to %q (%v), want %q", got, err, wantTarget)
			}
			assertContent(t, link, "devpod v0.8.0")
			if got, want := readDir(t, binDir), []string{filepath.Base(recent), "devpod"}; !slices.Equal(got, want) {
				t.Errorf("files in %s = %v, want %v", binDir, got, want)
			}
			if entries := readDir(t, releases); len(entries) != 2 {
				t.Errorf("upgrade left files behind in %s: %v", releases, entries)
			}
		})
	}
}

func TestUpgradeThroughLinkChecksPermissions(t *testing.T) {
	skipIfNoSymlinks(t)
	skipIfPermissionsDontApply(t)
	for _, readOnlyDir := range []string{"releases", "bin"} {
		t.Run(readOnlyDir, func(t *testing.T) {
			root := t.TempDir()
			mkdirs(t, filepath.Join(root, "releases"), filepath.Join(root, "bin"))
			previous := writeFile(t, filepath.Join(root, "releases", "devpod-mine"), "old")
			link := filepath.Join(root, "bin", "devpod")
			if err := os.Symlink(previous, link); err != nil {
				t.Fatal(err)
			}
			readOnly(t, filepath.Join(root, readOnlyDir))

			gh := newFakeGitHub("v0.8.0")
			setUp(t, gh, "v0.7.0", link)
			if err := Upgrade(context.Background(), Options{}, log.Discard); err == nil {
				t.Fatal("Upgrade() succeeded, want a permission error")
			}
			assertNoDownloads(t, gh)
			if got, _ := os.Readlink(link); got != previous {
				t.Errorf("link points to %q, want %q", got, previous)
			}
		})
	}
}

func TestUpgradeThroughLinkToLinkFails(t *testing.T) {
	skipIfNoSymlinks(t)
	dir := t.TempDir()
	bin := writeFile(t, filepath.Join(dir, "devpod-mine"), "old")
	middle := filepath.Join(dir, "devpod-current")
	link := filepath.Join(dir, "devpod")
	if err := os.Symlink(bin, middle); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(middle, link); err != nil {
		t.Fatal(err)
	}

	gh := newFakeGitHub("v0.8.0")
	setUp(t, gh, "v0.7.0", link)
	for _, opts := range []Options{{}, {DryRun: true}} {
		err := Upgrade(context.Background(), opts, log.Discard)
		if err == nil || !strings.Contains(err.Error(), "another link") {
			t.Errorf("Upgrade(%+v) = %v, want a link to link error", opts, err)
		}
	}
	assertNoDownloads(t, gh)
	assertContent(t, bin, "old")
}

func TestUpgradeThroughLinkReplacesFileNamedAfterRelease(t *testing.T) {
	skipIfNoSymlinks(t)
	dir := t.TempDir()
	previous := writeFile(t, filepath.Join(dir, "devpod-mine"), "old")
	existing := writeFile(t, filepath.Join(dir, "devpod-v0.8.0"), "something else")
	link := filepath.Join(dir, "devpod")
	if err := os.Symlink(previous, link); err != nil {
		t.Fatal(err)
	}

	gh := newFakeGitHub("v0.8.0")
	setUp(t, gh, "v0.7.0", link)
	in, err := planInstall(link, "v0.8.0")
	if err != nil {
		t.Fatal(err)
	}
	if !in.overwrites {
		t.Error("planInstall doesn't report that the existing file would be replaced")
	}
	if err := Upgrade(context.Background(), Options{}, log.Discard); err != nil {
		t.Fatal(err)
	}
	assertContent(t, existing, "devpod v0.8.0")
	assertContent(t, previous, "old")
}

func TestUpgradeThroughLinkToReleaseFileName(t *testing.T) {
	skipIfNoSymlinks(t)
	dir := t.TempDir()
	bin := writeFile(t, filepath.Join(dir, "devpod-v0.8.0"), "old")
	link := filepath.Join(dir, "devpod")
	if err := os.Symlink(bin, link); err != nil {
		t.Fatal(err)
	}

	gh := newFakeGitHub("v0.8.0")
	setUp(t, gh, "v0.7.0", link)
	if err := Upgrade(context.Background(), Options{}, log.Discard); err == nil {
		t.Error("Upgrade() succeeded, want an error")
	}
	assertNoDownloads(t, gh)
	assertContent(t, bin, "old")
}

// fakeGitHub serves the GitHub API endpoints Upgrade uses. Each release has a CLI for the current platform whose
// content is "devpod <tag>".
type fakeGitHub struct {
	releases  []download.GithubRelease
	assets    map[int]string
	downloads atomic.Int32
	// emptyList makes the release list empty, as GitHub sometimes answers.
	emptyList bool
	// stalled, if set, makes downloads send half of the asset, close stalled and then wait until the client gives
	// up.
	stalled     chan struct{}
	stalledOnce sync.Once
}

func newFakeGitHub(tags ...string) *fakeGitHub {
	gh := &fakeGitHub{assets: map[int]string{}}
	for _, tag := range tags {
		gh.add(tag, false)
	}
	return gh
}

func (gh *fakeGitHub) add(tag string, prerelease bool) {
	id := len(gh.assets) + 1
	content := "devpod " + tag
	gh.assets[id] = content
	gh.releases = append(gh.releases, download.GithubRelease{
		TagName:    tag,
		Prerelease: prerelease,
		Assets:     []download.GithubReleaseAsset{{ID: id, Name: cliAssetName(runtime.GOOS, runtime.GOARCH), Size: int64(len(content))}},
	})
}

func (gh *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	prefix := "/repos/curityio/devpod/releases"
	switch path := r.URL.Path; {
	case path == prefix:
		if gh.emptyList {
			_, _ = w.Write([]byte("[]"))
		} else {
			_ = json.NewEncoder(w).Encode(gh.releases)
		}
		return
	case path == prefix+"/latest":
		for i := len(gh.releases) - 1; i >= 0; i-- {
			if !gh.releases[i].Prerelease {
				_ = json.NewEncoder(w).Encode(gh.releases[i])
				return
			}
		}
	case strings.HasPrefix(path, prefix+"/tags/"):
		tag := strings.TrimPrefix(path, prefix+"/tags/")
		for _, rel := range gh.releases {
			if rel.TagName == tag {
				_ = json.NewEncoder(w).Encode(rel)
				return
			}
		}
	case strings.HasPrefix(path, prefix+"/assets/"):
		id, _ := strconv.Atoi(strings.TrimPrefix(path, prefix+"/assets/"))
		if content, ok := gh.assets[id]; ok && r.Header.Get("Accept") == "application/octet-stream" {
			gh.downloads.Add(1)
			if gh.stalled != nil {
				w.Header().Set("Content-Length", strconv.Itoa(len(content)))
				_, _ = w.Write([]byte(content[:len(content)/2]))
				w.(http.Flusher).Flush()
				gh.stalledOnce.Do(func() { close(gh.stalled) })
				<-r.Context().Done()
				return
			}
			_, _ = w.Write([]byte(content))
			return
		}
	}
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`{"message":"Not Found"}`))
}

func setUp(t *testing.T, gh *fakeGitHub, current, binary string) {
	t.Helper()
	server := httptest.NewServer(gh)
	t.Cleanup(server.Close)

	oldAPIURL, oldCurrentVersion, oldFindBinary := apiURL, currentVersion, findBinary
	t.Cleanup(func() { apiURL, currentVersion, findBinary = oldAPIURL, oldCurrentVersion, oldFindBinary })
	apiURL = server.URL
	currentVersion = func() string { return current }
	findBinary = func() (string, error) { return binary, nil }
}

func assertNoDownloads(t *testing.T, gh *fakeGitHub) {
	t.Helper()
	if got := gh.downloads.Load(); got != 0 {
		t.Errorf("downloads = %d, want 0", got)
	}
}

func assertContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("%s contains %q, want %q", path, got, want)
	}
}

func writeFile(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

// makeOld makes path look as if an interrupted upgrade left it behind two hours ago.
func makeOld(t *testing.T, path string) {
	t.Helper()
	old := time.Now().Add(-2 * time.Hour)
	if info := lstat(t, path); info.Mode()&fs.ModeSymlink != 0 {
		setLinkTime(t, path, old)
		return
	}
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

func lstat(t *testing.T, path string) fs.FileInfo {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func mkdirs(t *testing.T, dirs ...string) {
	t.Helper()
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
}

func readDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	return names
}

func readOnly(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0755) })
}

func sameFile(t *testing.T, a, b string) bool {
	t.Helper()
	ai, err := os.Stat(a)
	if err != nil {
		t.Fatal(err)
	}
	bi, err := os.Stat(b)
	if err != nil {
		t.Fatal(err)
	}
	return os.SameFile(ai, bi)
}

func skipIfPermissionsDontApply(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions don't stop this user from creating files")
	}
}

func skipIfNoSymlinks(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks on Windows needs a privilege the tests may not have")
	}
}
