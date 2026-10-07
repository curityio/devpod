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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testTree is the set of files created in the archived folder by newTestTree.
var testTree = []string{
	"README.md",
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
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o750))
		require.NoError(t, os.WriteFile(p, []byte(file), 0o600))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(root, "empty"), 0o750))

	return root
}

// tarEntries archives root with excludes and returns the names of the regular
// files and empty directories in the archive, sorted.
func tarEntries(t *testing.T, root string, excludes []string) []string {
	t.Helper()

	buf := &bytes.Buffer{}
	require.NoError(t, WriteTarExclude(buf, root, false, excludes))

	entries := []string{}
	reader := tar.NewReader(buf)
	for {
		hdr, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		entries = append(entries, hdr.Name)
	}
	sort.Strings(entries)

	return entries
}

func allEntries() []string {
	entries := append([]string{"empty"}, testTree...)
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
		excludes: []string{".claude/worktrees/"},
		expected: without(allEntries(),
			".claude/worktrees/feature/src/main.go",
			".claude/worktrees/feature/keep.txt",
		),
	},
	{
		name:     "double star excludes a directory at any depth",
		excludes: []string{"**/node_modules/"},
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
		excludes: []string{".claude/worktrees/", "!.claude/worktrees/feature/keep.txt"},
		expected: without(allEntries(), ".claude/worktrees/feature/src/main.go"),
	},
	{
		name:     "excluded empty directory is not archived",
		excludes: []string{"empty", "!README.md"},
		expected: without(allEntries(), "empty"),
	},
	{
		name: "devpodignore of the identity server",
		excludes: []string{
			".claude/worktrees", ".gradle", "**/build", "**/node_modules", "dist",
			"tools/chrome", "tools/firefox", "docs/*.tgz",
		},
		expected: []string{
			".claude/settings.json",
			"README.md",
			"buildSrc/plugin.kt",
			"docs/guide.md",
			"docs/old/archive.tgz",
			"empty",
			"web/app/main.js",
		},
	},
}

func TestWriteTarExclude(t *testing.T) {
	for _, tt := range writeTarExcludeTests {
		t.Run(tt.name, func(t *testing.T) {
			root := newTestTree(t)
			assert.Equal(t, tt.expected, tarEntries(t, root, tt.excludes))
		})
	}
}

func TestWriteTarExcludeSingleFile(t *testing.T) {
	root := newTestTree(t)

	entries := tarEntries(t, filepath.Join(root, "README.md"), []string{"docs"})
	assert.Equal(t, []string{"README.md"}, entries)
}

func TestWriteTarExcludeInvalidPattern(t *testing.T) {
	root := newTestTree(t)

	err := WriteTarExclude(&bytes.Buffer{}, root, false, []string{"[a-"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exclude patterns")
}
