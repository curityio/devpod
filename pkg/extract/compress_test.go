package extract

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"gotest.tools/assert"
	is "gotest.tools/assert/cmp"
)

const (
	readmeFile  = "README.md"
	emptyFolder = "empty"
	worktrees   = ".claude/worktrees/"
	nodeModules = "**/node_modules/"
)

// testTree is the set of files created in the archived folder by newTestTree.
var testTree = []string{
	readmeFile,
	".claude/settings.json",
	".claude/worktrees/feature/src/main.go",
	".claude/worktrees/feature/keep.txt",
	"node_modules/lib/index.js",
	"web/node_modules/lib/index.js",
	"web/app/node_modules/lib/index.js",
	"web/app/main.js",
	"build/out.bin",
	"core/build/out.bin",
	"buildSrc/plugin.kt",
	"docs/guide.tgz",
	"docs/guide.md",
	"docs/old/archive.tgz",
}

func newTestTree(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	for _, file := range testTree {
		p := filepath.Join(root, filepath.FromSlash(file))
		assert.NilError(t, os.MkdirAll(filepath.Dir(p), 0o750))
		assert.NilError(t, os.WriteFile(p, []byte(file), 0o600))
	}
	assert.NilError(t, os.MkdirAll(filepath.Join(root, emptyFolder), 0o750))

	return root
}

// tarEntries archives root with excludes and returns the names of the regular
// files and empty directories in the archive, sorted.
func tarEntries(t *testing.T, root string, excludes []string) []string {
	t.Helper()

	buf := &bytes.Buffer{}
	assert.NilError(t, WriteTarExclude(buf, root, false, excludes))

	entries := []string{}
	reader := tar.NewReader(buf)
	for {
		hdr, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		assert.NilError(t, err)
		entries = append(entries, hdr.Name)
	}
	sort.Strings(entries)

	return entries
}

func allEntries() []string {
	entries := append([]string{emptyFolder}, testTree...)
	sort.Strings(entries)
	return entries
}

func without(entries []string, excluded ...string) []string {
	result := []string{}
	for _, entry := range entries {
		if !slices.Contains(excluded, entry) {
			result = append(result, entry)
		}
	}
	return result
}

type writeTarExcludeTest struct {
	name     string
	excludes []string
	expected []string
}

var writeTarExcludeTests = []writeTarExcludeTest{
	{
		name:     "nil excludes archive everything",
		excludes: nil,
		expected: allEntries(),
	},
	{
		name:     "empty excludes archive everything",
		excludes: []string{},
		expected: allEntries(),
	},
	{
		name:     "directory pattern excludes the directory and its contents",
		excludes: []string{worktrees},
		expected: without(allEntries(),
			".claude/worktrees/feature/src/main.go",
			".claude/worktrees/feature/keep.txt",
		),
	},
	{
		name:     "double star excludes a directory at any depth",
		excludes: []string{nodeModules},
		expected: without(allEntries(),
			"node_modules/lib/index.js",
			"web/node_modules/lib/index.js",
			"web/app/node_modules/lib/index.js",
		),
	},
	{
		name:     "single star excludes only matching files",
		excludes: []string{"docs/*.tgz"},
		expected: without(allEntries(), "docs/guide.tgz"),
	},
	{
		name:     "a name does not exclude a longer name it is a prefix of",
		excludes: []string{"build"},
		expected: without(allEntries(), "build/out.bin"),
	},
	{
		name:     "double star build excludes nested build folders only",
		excludes: []string{"**/build/"},
		expected: without(allEntries(), "build/out.bin", "core/build/out.bin"),
	},
	{
		name:     "negation re-includes a file inside an excluded directory",
		excludes: []string{worktrees, "!.claude/worktrees/feature/keep.txt"},
		expected: without(allEntries(), ".claude/worktrees/feature/src/main.go"),
	},
	{
		name:     "negation with double star re-includes a file inside an excluded directory",
		excludes: []string{worktrees, "!**/keep.txt"},
		expected: without(allEntries(), ".claude/worktrees/feature/src/main.go"),
	},
	{
		name:     "excluded empty directory is not archived",
		excludes: []string{emptyFolder, "!README.md"},
		expected: without(allEntries(), emptyFolder),
	},
	{
		name: "ignore file of the identity server",
		excludes: []string{
			".claude/worktrees", ".gradle", "**/build", "**/node_modules", "dist",
			"tools/chrome", "tools/firefox", "docs/*.tgz",
		},
		expected: []string{
			".claude/settings.json",
			readmeFile,
			"buildSrc/plugin.kt",
			"docs/guide.md",
			"docs/old/archive.tgz",
			emptyFolder,
			"web/app/main.js",
		},
	},
}

func TestWriteTarExclude(t *testing.T) {
	for _, tt := range writeTarExcludeTests {
		t.Run(tt.name, func(t *testing.T) {
			root := newTestTree(t)
			assert.DeepEqual(t, tt.expected, tarEntries(t, root, tt.excludes))
		})
	}
}

func TestWriteTarExcludeSingleFile(t *testing.T) {
	root := newTestTree(t)

	assert.DeepEqual(
		t,
		[]string{readmeFile},
		tarEntries(t, filepath.Join(root, readmeFile), []string{"docs"}),
	)
	assert.Equal(t, len(tarEntries(t, filepath.Join(root, readmeFile), []string{readmeFile})), 0)
}

func TestArchiverMayReincludeBelow(t *testing.T) {
	archiver, err := NewArchiver(t.TempDir(), nil, []string{
		worktrees, nodeModules, "!.claude/worktrees/feature/keep.txt",
	})
	assert.NilError(t, err)

	assert.Assert(t, archiver.mayReincludeBelow(".claude/worktrees"))
	assert.Assert(t, !archiver.mayReincludeBelow("web/node_modules"))
}

func TestPatternMayMatchBelow(t *testing.T) {
	const keep = "a/b/keep.txt"
	const ab = "a/b"
	tests := []struct {
		pattern  string
		dir      string
		expected bool
	}{
		{pattern: keep, dir: "a", expected: true},
		{pattern: keep, dir: ab, expected: true},
		{pattern: "a/*/keep.txt", dir: ab, expected: true},
		{pattern: "**/keep.txt", dir: "x/y", expected: true},
		{pattern: "a/**", dir: "z", expected: true},
		{pattern: "a/[b/keep.txt", dir: "a/c", expected: true},
		{pattern: keep, dir: "c", expected: false},
		{pattern: keep, dir: "a/c", expected: false},
		{pattern: ab, dir: ab, expected: false},
		{pattern: "keep.txt", dir: "a", expected: false},
		{pattern: keep, dir: keep, expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+" below "+tt.dir, func(t *testing.T) {
			assert.Equal(t, tt.expected, patternMayMatchBelow(
				strings.Split(tt.pattern, "/"), strings.Split(tt.dir, "/")))
		})
	}
}

func TestWriteTarWithOptionsKeepsFoldersWhole(t *testing.T) {
	root := newTestTree(t)

	buf := &bytes.Buffer{}
	assert.NilError(t, WriteTarWithOptions(buf, root, TarOptions{
		Excludes:  []string{nodeModules, "web/"},
		KeepNames: []string{"node_modules"},
	}))

	entries := []string{}
	reader := tar.NewReader(buf)
	for {
		hdr, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		assert.NilError(t, err)
		entries = append(entries, hdr.Name)
	}

	// The kept folder is archived whole, but not inside a folder that is
	// skipped before the walk reaches it
	assert.Assert(t, is.Contains(entries, "node_modules/lib/index.js"))
	assert.Assert(t, !slices.Contains(entries, "web/node_modules/lib/index.js"))
	assert.Assert(t, !slices.Contains(entries, "web/app/node_modules/lib/index.js"))
}

func TestWriteTarExcludeInvalidPattern(t *testing.T) {
	root := newTestTree(t)

	err := WriteTarExclude(&bytes.Buffer{}, root, false, []string{"[a-"})
	assert.ErrorContains(t, err, "exclude patterns")
}
