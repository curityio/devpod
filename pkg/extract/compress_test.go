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

// tarEntriesWithOptions archives root with opts and returns the names of the
// regular files and empty directories in the archive, sorted.
func tarEntriesWithOptions(t *testing.T, root string, opts TarOptions) []string {
	t.Helper()

	buf := &bytes.Buffer{}
	assert.NilError(t, WriteTarWithOptions(buf, root, opts))

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

// gitFiles are the files of testTree git would list with a .gitignore of
// node_modules, build and .claude/worktrees.
var gitFiles = []string{
	readmeFile,
	".claude/settings.json",
	"web/app/main.js",
	"buildSrc/plugin.kt",
	"docs/guide.tgz",
	"docs/guide.md",
	"docs/old/archive.tgz",
}

func sorted(entries ...string) []string {
	entries = slices.Clone(entries)
	sort.Strings(entries)
	return entries
}

func TestWriteTarWithFiles(t *testing.T) {
	tests := []struct {
		name     string
		excludes []string
		// files defaults to gitFiles
		files    []string
		expected []string
	}{
		{
			name:     "only listed files are archived",
			expected: sorted(gitFiles...),
		},
		{
			name:     "exclude patterns leave out listed files",
			excludes: []string{"docs/*.tgz", "web"},
			expected: without(sorted(gitFiles...), "docs/guide.tgz", "web/app/main.js"),
		},
		{
			name:     "negation patterns add files that are not listed",
			excludes: []string{"!build/out.bin", "!**/node_modules/lib"},
			expected: sorted(append(slices.Clone(gitFiles),
				"build/out.bin",
				"node_modules/lib/index.js",
				"web/node_modules/lib/index.js",
				"web/app/node_modules/lib/index.js",
			)...),
		},
		{
			name:     "negation pattern adds a whole folder that is not listed",
			excludes: []string{"!.claude/worktrees"},
			expected: sorted(append(slices.Clone(gitFiles),
				".claude/worktrees/feature/src/main.go",
				".claude/worktrees/feature/keep.txt",
			)...),
		},
		{
			name:     "later exclude pattern wins over negation pattern",
			excludes: []string{"!.claude/worktrees", ".claude/worktrees/feature/src"},
			expected: sorted(append(slices.Clone(gitFiles), ".claude/worktrees/feature/keep.txt")...),
		},
		{
			// git lists a submodule that isn't checked out as a file
			name:     "listed empty folder is archived",
			files:    append(slices.Clone(gitFiles), emptyFolder),
			expected: sorted(append(slices.Clone(gitFiles), emptyFolder)...),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := newTestTree(t)
			files := tt.files
			if files == nil {
				files = gitFiles
			}
			assert.DeepEqual(t, tt.expected, tarEntriesWithOptions(t, root, TarOptions{
				Excludes: tt.excludes,
				Files:    NewFileList(files),
			}))
		})
	}
}

func TestWriteTarWithFilesKeepsFoldersWhole(t *testing.T) {
	root := newTestTree(t)

	entries := tarEntriesWithOptions(t, root, TarOptions{
		Files:     NewFileList(gitFiles),
		KeepNames: []string{"worktrees"},
	})

	assert.Assert(t, is.Contains(entries, ".claude/worktrees/feature/src/main.go"))
	assert.Assert(t, is.Contains(entries, ".claude/worktrees/feature/keep.txt"))
}

func TestWriteTarWithOverlays(t *testing.T) {
	root := newTestTree(t)
	overlayParent := t.TempDir()
	writeTestFiles(t, overlayParent, "docs/new.md", "docs/sub/other.md")
	writeTestFiles(t, root, "web/.git/HEAD")

	entries := tarEntriesWithOptions(t, root, TarOptions{
		Excludes: []string{"!web/.git"},
		Files:    NewFileList(gitFiles),
		Overlays: []string{filepath.Join(overlayParent, "docs")},
	})

	// The overlay replaces the folder at the root only
	assert.DeepEqual(t, sorted(
		readmeFile,
		".claude/settings.json",
		"web/app/main.js",
		"web/.git/HEAD",
		"buildSrc/plugin.kt",
		"docs/new.md",
		"docs/sub/other.md",
	), entries)
}

func writeTestFiles(t *testing.T, root string, files ...string) {
	t.Helper()

	for _, file := range files {
		p := filepath.Join(root, filepath.FromSlash(file))
		assert.NilError(t, os.MkdirAll(filepath.Dir(p), 0o750))
		assert.NilError(t, os.WriteFile(p, []byte(file), 0o600))
	}
}

func TestExcludeRulesMatch(t *testing.T) {
	rules, err := NewExcludeRules([]string{"build", "!build/keep", "!.env", "docs/*.tgz"})
	assert.NilError(t, err)

	tests := map[string]Verdict{
		".":              Unmatched,
		"src/main.go":    Unmatched,
		"build":          Excluded,
		"build/out.bin":  Excluded,
		"build/keep":     Included,
		"build/keep/a":   Included,
		".env":           Included,
		"docs/guide.tgz": Excluded,
		"docs/guide.md":  Unmatched,
	}
	for p, expected := range tests {
		verdict, err := rules.Match(p)
		assert.NilError(t, err)
		assert.Equal(t, expected, verdict, p)
	}

	var none *ExcludeRules
	verdict, err := none.Match("build")
	assert.NilError(t, err)
	assert.Equal(t, Unmatched, verdict)

	rules, err = NewExcludeRules(nil)
	assert.NilError(t, err)
	assert.Assert(t, rules == nil)
}

func TestNewFileList(t *testing.T) {
	list := NewFileList([]string{"a/b/c.txt", "a/d.txt", "e.txt", "./f/../g.txt"})

	assert.Equal(t, 4, list.Len())
	assert.Assert(t, list.hasFolder(""))
	assert.Assert(t, list.hasFile("a/b/c.txt"))
	assert.Assert(t, list.hasFile("g.txt"))
	assert.Assert(t, !list.hasFile("a/b"))
	assert.Assert(t, list.hasFolder("a"))
	assert.Assert(t, list.hasFolder("a/b"))
	assert.Assert(t, !list.hasFolder("f"))
	assert.Assert(t, !list.hasFolder("a/b/c"))

	var all *FileList
	assert.Assert(t, all.hasFile("anything"))
	assert.Assert(t, all.hasFolder("anything"))
}
