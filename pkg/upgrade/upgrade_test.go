package upgrade

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestParseVersion(t *testing.T) {
	tests := []struct {
		in      string
		numbers []int
		suffix  string
		stable  bool
	}{
		{"v0.8.0", []int{0, 8, 0}, "", true},
		{"1.2.3", []int{1, 2, 3}, "", true},
		{"v10.20.30", []int{10, 20, 30}, "", true},
		{"v0.8.0-alpha.1", []int{0, 8, 0}, "-alpha.1", false},
		{"1.2.3alpha", []int{1, 2, 3}, "alpha", false},
		{"1.2.3-beta-1", []int{1, 2, 3}, "-beta-1", false},
		{"1.2.3.4", []int{1, 2, 3, 4}, "", false},
		{"1.2.3.4-rc1", []int{1, 2, 3, 4}, "-rc1", false},
		{"v0.0.0-dev.20261022101500", []int{0, 0, 0}, "-dev.20261022101500", false},
	}
	for _, tt := range tests {
		v, err := parseVersion(tt.in)
		if err != nil {
			t.Errorf("parseVersion(%q): %v", tt.in, err)
			continue
		}
		if !equalInts(v.numbers, tt.numbers) || v.suffix != tt.suffix {
			t.Errorf("parseVersion(%q) = %v %q, want %v %q", tt.in, v.numbers, v.suffix, tt.numbers, tt.suffix)
		}
		if v.isStable() != tt.stable {
			t.Errorf("parseVersion(%q).isStable() = %v, want %v", tt.in, v.isStable(), tt.stable)
		}
	}
}

func TestParseVersionInvalid(t *testing.T) {
	for _, in := range []string{"", "latest", "v1", "1.2", "1.2.x", "x1.2.3", "99999999999999999999.0.0"} {
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
	withAsset := func(tag string) release {
		return release{TagName: tag, Assets: []releaseAsset{{ID: 1, Name: "DevPod_linux_amd64.AppImage"}, {ID: 2, Name: asset}}}
	}

	draft := withAsset("v0.13.0")
	draft.Draft = true
	prerelease := withAsset("v0.12.0")
	prerelease.Prerelease = true
	releases := []release{
		withAsset("v0.9.0-alpha.1"),
		withAsset("v0.9.0.1"),
		draft,
		prerelease,
		{TagName: "v0.11.0", Assets: []releaseAsset{{Name: cliAssetName("darwin", "arm64")}}},
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
	if !equalInts(v.numbers, []int{0, 10, 0}) {
		t.Errorf("version = %v, want [0 10 0]", v.numbers)
	}

	if rel, _, _ := latestStableRelease(releases[:4], asset); rel != nil {
		t.Errorf("latest release = %s, want none", rel.TagName)
	}
}

func TestFindRelease(t *testing.T) {
	releases := []release{{TagName: "v0.8.0"}, {TagName: "v0.8.0-alpha.1"}}
	for _, tag := range []string{"v0.8.0", "0.8.0"} {
		if rel := findRelease(releases, tag); rel == nil || rel.TagName != "v0.8.0" {
			t.Errorf("findRelease(%q) = %v, want v0.8.0", tag, rel)
		}
	}
	if rel := findRelease(releases, "0.8.0-alpha.1"); rel == nil || rel.TagName != "v0.8.0-alpha.1" {
		t.Errorf("findRelease(0.8.0-alpha.1) = %v, want v0.8.0-alpha.1", rel)
	}
	if rel := findRelease(releases, "0.9.0"); rel != nil {
		t.Errorf("findRelease(0.9.0) = %s, want none", rel.TagName)
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

func TestCheckCanReplace(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "devpod")
	if err := os.WriteFile(bin, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}

	if err := checkCanReplace(bin); err != nil {
		t.Errorf("checkCanReplace in a writable directory: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("checkCanReplace left files behind: %v", entries)
	}

	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions don't stop this user from creating files")
	}
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0755) })
	if err := checkCanReplace(bin); err == nil {
		t.Error("checkCanReplace in a read-only directory succeeded, want an error")
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
