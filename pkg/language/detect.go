package language

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
)

// extensionLanguages maps source file extensions to the language they count towards.
var extensionLanguages = map[string]ProgrammingLanguage{
	".js":   JavaScript,
	".ts":   TypeScript,
	".py":   Python,
	".c":    C,
	".cpp":  Cpp,
	".cs":   DotNet,
	".go":   Go,
	".php":  PHP,
	".java": Java,
	".rs":   Rust,
	".rb":   Ruby,
}

// detectionOrder breaks ties between languages with the same number of files.
var detectionOrder = []ProgrammingLanguage{JavaScript, TypeScript, Python, C, Cpp, DotNet, Go, PHP, Java, Rust, Ruby}

// skippedDirs holds directories that usually contain dependencies, generated files or
// documentation rather than the project's own sources.
var skippedDirs = map[string]bool{
	"bower_components": true,
	"build":            true,
	"cache":            true,
	"deps":             true,
	"dist":             true,
	"doc":              true,
	"docs":             true,
	"env":              true,
	"example":          true,
	"examples":         true,
	"godeps":           true,
	"node_modules":     true,
	"out":              true,
	"target":           true,
	"testdata":         true,
	"third_party":      true,
	"thirdparty":       true,
	"vendor":           true,
	"vendors":          true,
	"venv":             true,
}

var errFileLimit = errors.New("file limit reached")

// detect guesses the main language of the project in root by counting source files, looking at
// up to maxFiles files. Hidden files and directories, and directories in skippedDirs, are ignored.
func detect(root string, maxFiles int) (ProgrammingLanguage, error) {
	counts := map[ProgrammingLanguage]int{}
	files := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// skip unreadable entries, but fail if the root itself can't be read
			if path == root {
				return err
			}
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if path == root {
			return nil
		}

		name := d.Name()
		if d.IsDir() {
			if strings.HasPrefix(name, ".") || skippedDirs[strings.ToLower(name)] {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") || isGenerated(name) {
			return nil
		}

		files++
		if lang, ok := extensionLanguages[filepath.Ext(name)]; ok {
			counts[lang]++
		}
		if maxFiles > 0 && files >= maxFiles {
			return errFileLimit
		}
		return nil
	})
	if err != nil && !errors.Is(err, errFileLimit) {
		return None, err
	}

	detected, max := None, 0
	for _, lang := range detectionOrder {
		if counts[lang] > max {
			detected, max = lang, counts[lang]
		}
	}
	return detected, nil
}

// isGenerated reports whether a file name looks like a type declaration or minified bundle.
func isGenerated(name string) bool {
	return strings.HasSuffix(name, ".d.ts") || strings.HasSuffix(name, ".min.js")
}
