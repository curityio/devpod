package extract

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gotest.tools/assert"
)

func writeFileAt(t *testing.T, name, content string, modTime time.Time) {
	t.Helper()
	assert.NilError(t, os.MkdirAll(filepath.Dir(name), 0o755))
	assert.NilError(t, os.WriteFile(name, []byte(content), 0o644))
	assert.NilError(t, os.Chtimes(name, modTime, modTime))
}

func readFile(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(name)
	assert.NilError(t, err)
	return string(content)
}

func tarOf(t *testing.T, folder string) *bytes.Buffer {
	t.Helper()
	archive := &bytes.Buffer{}
	assert.NilError(t, WriteTar(archive, folder, false))
	return archive
}

func TestExtractKeepNewer(t *testing.T) {
	source := t.TempDir()
	target := t.TempDir()
	lastCopy := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	yesterday := time.Now().Add(-24 * time.Hour).Truncate(time.Second)
	today := time.Now().Add(-time.Hour).Truncate(time.Second)

	// neither side changed the file since it was copied
	writeFileAt(t, filepath.Join(source, "untouched"), "same", lastCopy)
	writeFileAt(t, filepath.Join(target, "untouched"), "same", lastCopy)
	// e.g. a new login on the host
	writeFileAt(t, filepath.Join(source, "changed-on-host"), "host", today)
	writeFileAt(t, filepath.Join(target, "changed-on-host"), "old", lastCopy)
	// e.g. a token the container rotated
	writeFileAt(t, filepath.Join(source, "changed-in-container"), "old", lastCopy)
	writeFileAt(t, filepath.Join(target, "changed-in-container"), "container", yesterday)
	// the side that changed it last wins
	writeFileAt(t, filepath.Join(source, "changed-on-both"), "host", today)
	writeFileAt(t, filepath.Join(target, "changed-on-both"), "container", yesterday)
	writeFileAt(t, filepath.Join(source, "sub", "new-on-host"), "host", today)
	writeFileAt(t, filepath.Join(target, "sub", "only-in-container"), "container", yesterday)

	assert.NilError(t, Extract(tarOf(t, source), target, KeepNewer()))

	assert.Equal(t, readFile(t, filepath.Join(target, "untouched")), "same")
	assert.Equal(t, readFile(t, filepath.Join(target, "changed-on-host")), "host")
	assert.Equal(t, readFile(t, filepath.Join(target, "changed-in-container")), "container")
	assert.Equal(t, readFile(t, filepath.Join(target, "changed-on-both")), "host")
	assert.Equal(t, readFile(t, filepath.Join(target, "sub", "new-on-host")), "host")
	assert.Equal(t, readFile(t, filepath.Join(target, "sub", "only-in-container")), "container")

	// a copied file has the time of the host's file, so that it only counts as newer once the
	// container changes it
	stat, err := os.Stat(filepath.Join(target, "changed-on-host"))
	assert.NilError(t, err)
	assert.Assert(t, stat.ModTime().Equal(today), "%s", stat.ModTime())
}

func TestExtractReplacesNewerWithoutKeepNewer(t *testing.T) {
	source := t.TempDir()
	target := t.TempDir()
	lastCopy := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	writeFileAt(t, filepath.Join(source, "file"), "host", lastCopy)
	writeFileAt(t, filepath.Join(target, "file"), "container", time.Now())

	assert.NilError(t, Extract(tarOf(t, source), target))

	assert.Equal(t, readFile(t, filepath.Join(target, "file")), "host")
}

func TestExtractOverExistingLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs a privilege on Windows")
	}
	source := t.TempDir()
	target := t.TempDir()
	writeFileAt(t, filepath.Join(source, "a"), "a", time.Now())
	writeFileAt(t, filepath.Join(source, "b"), "b", time.Now())
	assert.NilError(t, os.Symlink("b", filepath.Join(source, "link")))
	assert.NilError(t, os.Symlink("b", filepath.Join(source, "was-a-file")))

	// what an earlier copy left
	writeFileAt(t, filepath.Join(target, "a"), "a", time.Now())
	assert.NilError(t, os.Symlink("a", filepath.Join(target, "link")))
	writeFileAt(t, filepath.Join(target, "was-a-file"), "file", time.Now())

	assert.NilError(t, Extract(tarOf(t, source), target))

	for _, link := range []string{"link", "was-a-file"} {
		linkTarget, err := os.Readlink(filepath.Join(target, link))
		assert.NilError(t, err)
		assert.Equal(t, linkTarget, "b")
	}
}

func TestExtractKeepNewerLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs a privilege on Windows")
	}
	source := t.TempDir()
	target := t.TempDir()
	assert.NilError(t, os.Symlink("host", filepath.Join(source, "link")))
	// the link in the target is created after the one in the archive
	archive := tarOf(t, source)
	time.Sleep(1100 * time.Millisecond)
	assert.NilError(t, os.Symlink("container", filepath.Join(target, "link")))

	assert.NilError(t, Extract(archive, target, KeepNewer()))

	linkTarget, err := os.Readlink(filepath.Join(target, "link"))
	assert.NilError(t, err)
	assert.Equal(t, linkTarget, "container")
}

func TestExtractDoesNotReplaceFolderWithLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs a privilege on Windows")
	}
	source := t.TempDir()
	target := t.TempDir()
	assert.NilError(t, os.Symlink("elsewhere", filepath.Join(source, "data")))
	writeFileAt(t, filepath.Join(target, "data", "file"), "container", time.Now())

	err := Extract(tarOf(t, source), target)

	assert.ErrorContains(t, err, "exists")
	assert.Equal(t, readFile(t, filepath.Join(target, "data", "file")), "container")
}

func TestExtractKeepNewerSkipsUnchangedFiles(t *testing.T) {
	source := t.TempDir()
	target := t.TempDir()
	lastCopy := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	// the content tells the files apart here, a copy has the same
	writeFileAt(t, filepath.Join(source, "same-time-and-size"), "host", lastCopy)
	writeFileAt(t, filepath.Join(target, "same-time-and-size"), "copy", lastCopy)
	writeFileAt(t, filepath.Join(source, "other-size"), "host", lastCopy)
	writeFileAt(t, filepath.Join(target, "other-size"), "a copy", lastCopy)

	assert.NilError(t, Extract(tarOf(t, source), target, KeepNewer()))

	assert.Equal(t, readFile(t, filepath.Join(target, "same-time-and-size")), "copy")
	assert.Equal(t, readFile(t, filepath.Join(target, "other-size")), "host")
}

func TestExtractKeepNewerInterrupted(t *testing.T) {
	source := t.TempDir()
	target := t.TempDir()
	lastCopy := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	today := time.Now().Add(-time.Hour).Truncate(time.Second)
	content := bytes.Repeat([]byte("host"), 64*1024)
	writeFileAt(t, filepath.Join(source, "file"), string(content), today)
	writeFileAt(t, filepath.Join(target, "file"), "old", lastCopy)

	// the stream breaks in the middle of the file
	archive := tarOf(t, source)
	err := Extract(bytes.NewReader(archive.Bytes()[:len(content)/2]), target, KeepNewer())
	assert.Assert(t, err != nil)
	// the file is whole, and not newer than the host's
	assert.Equal(t, readFile(t, filepath.Join(target, "file")), "old")
	stat, err := os.Stat(filepath.Join(target, "file"))
	assert.NilError(t, err)
	assert.Assert(t, stat.ModTime().Equal(lastCopy), "%s", stat.ModTime())

	// so the next time replaces it, and leaves nothing of the first
	assert.NilError(t, Extract(tarOf(t, source), target, KeepNewer()))
	assert.Equal(t, readFile(t, filepath.Join(target, "file")), string(content))
	entries, err := os.ReadDir(target)
	assert.NilError(t, err)
	assert.Equal(t, len(entries), 1)
}

func TestExtractKeepNewerReplacesLinkWithFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs a privilege on Windows")
	}
	source := t.TempDir()
	target := t.TempDir()
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	writeFileAt(t, elsewhere, "elsewhere", time.Now())
	assert.NilError(t, os.Symlink(elsewhere, filepath.Join(target, "file")))
	// the host's file is newer than the link
	writeFileAt(t, filepath.Join(source, "file"), "host", time.Now().Add(time.Hour))

	assert.NilError(t, Extract(tarOf(t, source), target, KeepNewer()))

	assert.Equal(t, readFile(t, filepath.Join(target, "file")), "host")
	stat, err := os.Lstat(filepath.Join(target, "file"))
	assert.NilError(t, err)
	assert.Assert(t, stat.Mode().IsRegular())
	// the file is not written through the link
	assert.Equal(t, readFile(t, elsewhere), "elsewhere")
}

func TestExtractKeepNewerFileOverFolderFails(t *testing.T) {
	source := t.TempDir()
	target := t.TempDir()
	writeFileAt(t, filepath.Join(source, "data"), "host", time.Now())
	writeFileAt(t, filepath.Join(target, "data", "file"), "container", time.Now())

	err := Extract(tarOf(t, source), target, KeepNewer())

	assert.ErrorContains(t, err, "replace")
	assert.Equal(t, readFile(t, filepath.Join(target, "data", "file")), "container")
	// and nothing is left of the file that was to replace it
	entries, err := os.ReadDir(target)
	assert.NilError(t, err)
	assert.Equal(t, len(entries), 1)
}

func TestExtractKeepNewerWritesNoExistingFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs a privilege on Windows")
	}
	source := t.TempDir()
	target := t.TempDir()
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	writeFileAt(t, elsewhere, "elsewhere", time.Now())
	lastCopy := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	writeFileAt(t, filepath.Join(source, "token"), "host", time.Now())
	writeFileAt(t, filepath.Join(target, "token"), "old", lastCopy)
	// files of the container with names a copy could go through
	others := []string{"token.devpod-partial", ".token.devpod-", ".token.devpod-1", "token.tmp", "token~"}
	for _, other := range others {
		writeFileAt(t, filepath.Join(target, other), "container", time.Now())
	}
	assert.NilError(t, os.Symlink(elsewhere, filepath.Join(target, ".token.devpod-link")))

	assert.NilError(t, Extract(tarOf(t, source), target, KeepNewer()))

	assert.Equal(t, readFile(t, filepath.Join(target, "token")), "host")
	for _, other := range others {
		assert.Equal(t, readFile(t, filepath.Join(target, other)), "container", other)
	}
	assert.Equal(t, readFile(t, elsewhere), "elsewhere")
	entries, err := os.ReadDir(target)
	assert.NilError(t, err)
	assert.Equal(t, len(entries), len(others)+2)
}

func TestExtractKeepNewerFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no such permissions")
	}
	source := t.TempDir()
	target := t.TempDir()
	lastCopy := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	writeFileAt(t, filepath.Join(source, "script"), "host", time.Now())
	assert.NilError(t, os.Chmod(filepath.Join(source, "script"), 0o755))
	writeFileAt(t, filepath.Join(source, "new"), "host", time.Now())
	writeFileAt(t, filepath.Join(target, "script"), "old", lastCopy)

	assert.NilError(t, Extract(tarOf(t, source), target, KeepNewer()))

	// as for a file that is written in place
	for file, expected := range map[string]os.FileMode{"script": 0o755, "new": 0o644} {
		stat, err := os.Stat(filepath.Join(target, file))
		assert.NilError(t, err)
		assert.Equal(t, stat.Mode().Perm(), expected, file)
	}
}

func TestExtractSkip(t *testing.T) {
	source := t.TempDir()
	target := t.TempDir()
	writeFileAt(t, filepath.Join(source, "kept", "file"), "host", time.Now())
	writeFileAt(t, filepath.Join(source, "skipped", "file"), "host", time.Now())
	writeFileAt(t, filepath.Join(source, "skipped", "sub", "file"), "host", time.Now())
	skipped := filepath.ToSlash(filepath.Join(target, "skipped"))

	assert.NilError(t, Extract(tarOf(t, source), target, Skip(func(name string) bool {
		return name == skipped || strings.HasPrefix(name, skipped+"/")
	})))

	assert.Equal(t, readFile(t, filepath.Join(target, "kept", "file")), "host")
	// not even its folders are created
	_, err := os.Stat(filepath.Join(target, "skipped"))
	assert.Assert(t, os.IsNotExist(err))
}
