package extract

import (
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/moby/patternmatcher"
	"github.com/pkg/errors"
)

// Verdict is what exclude patterns say about a path.
type Verdict int

const (
	// Unmatched means no pattern matches the path or one of its parent folders.
	Unmatched Verdict = iota
	// Excluded means the last pattern that matches the path or one of its
	// parent folders is an exclude pattern.
	Excluded
	// Included means the last pattern that matches the path or one of its
	// parent folders is a negation (!) pattern.
	Included
)

// ExcludeRules matches paths against exclude patterns with the syntax of a
// .dockerignore file.
type ExcludeRules struct {
	excludes *patternmatcher.PatternMatcher
	// all holds the patterns after one excluding everything, which tells a
	// path no pattern matches from one a negation pattern includes.
	all *patternmatcher.PatternMatcher
	// reincludes holds the components of each negation (!) pattern.
	reincludes [][]string
}

// NewExcludeRules parses patterns relative to an archived folder. It returns
// nil if there are no patterns.
func NewExcludeRules(patterns []string) (*ExcludeRules, error) {
	if len(patterns) == 0 {
		return nil, nil
	}

	excludes, err := patternmatcher.New(patterns)
	if err != nil {
		return nil, errors.Wrap(err, "parse exclude patterns")
	}
	all, err := patternmatcher.New(append([]string{"**"}, patterns...))
	if err != nil {
		return nil, errors.Wrap(err, "parse exclude patterns")
	}

	return &ExcludeRules{
		excludes:   excludes,
		all:        all,
		reincludes: reincludePatterns(excludes),
	}, nil
}

// reincludePatterns returns the components of each negation pattern.
func reincludePatterns(excludes *patternmatcher.PatternMatcher) [][]string {
	if !excludes.Exclusions() {
		return nil
	}

	var reincludes [][]string
	for _, p := range excludes.Patterns() {
		if p.Exclusion() {
			reincludes = append(reincludes, strings.Split(filepath.ToSlash(p.String()), "/"))
		}
	}
	return reincludes
}

// Match returns the verdict of the patterns for relativePath, a path relative
// to the archived folder.
func (r *ExcludeRules) Match(relativePath string) (Verdict, error) {
	verdict, _, err := r.match(relativePath, patternmatcher.MatchInfo{}, true)
	return verdict, err
}

// match returns the verdict of the patterns for relativePath, and the match
// results to pass down to the path's children. parentInfo holds the results of
// the parent folder, or the zero value when they are unknown. If unmatched is
// false, it returns Unmatched instead of Included, which is cheaper.
func (r *ExcludeRules) match(relativePath string, parentInfo patternmatcher.MatchInfo, unmatched bool) (Verdict, patternmatcher.MatchInfo, error) {
	if r == nil {
		return Unmatched, patternmatcher.MatchInfo{}, nil
	}

	// Patterns are relative to the archive root, so the root itself never matches
	relativePath = path.Clean(filepath.ToSlash(relativePath))
	if relativePath == "." || relativePath == "/" {
		return Unmatched, patternmatcher.MatchInfo{}, nil
	}

	excluded, info, err := r.excludes.MatchesUsingParentResults(relativePath, parentInfo)
	if err != nil {
		return Unmatched, info, errors.Wrapf(err, "match %s against exclude patterns", relativePath)
	}
	if excluded {
		return Excluded, info, nil
	}
	if !unmatched || !r.excludes.Exclusions() {
		return Unmatched, info, nil
	}

	// MatchesOrParentMatches is exact but slower than using the parent's
	// results. The archiver only asks for it with a file list, which keeps it
	// out of folders the list leaves out.
	all, err := r.all.MatchesOrParentMatches(relativePath)
	if err != nil {
		return Unmatched, info, errors.Wrapf(err, "match %s against exclude patterns", relativePath)
	}
	if !all {
		return Included, info, nil
	}
	return Unmatched, info, nil
}

// mayReincludeBelow reports whether a negation pattern could match a path
// inside the folder dir. It may return true for a pattern that turns out not
// to match, but never false for one that does.
func (r *ExcludeRules) mayReincludeBelow(dir string) bool {
	if r == nil {
		return false
	}

	dirComponents := strings.Split(path.Clean(filepath.ToSlash(dir)), "/")
	for _, pattern := range r.reincludes {
		if patternMayMatchBelow(pattern, dirComponents) {
			return true
		}
	}
	return false
}

// patternMayMatchBelow reports whether pattern could match a path below the
// folder dir, both given as path components.
func patternMayMatchBelow(pattern, dir []string) bool {
	// "**" matches any number of folders, so do not try to rule it out
	if slices.Contains(pattern, "**") {
		return true
	}
	// A pattern matching dir itself, or one of its parents, was already
	// applied to dir, which is still excluded
	if len(pattern) <= len(dir) {
		return false
	}
	for i, component := range dir {
		matched, err := path.Match(pattern[i], component)
		if err != nil || !matched {
			return err != nil
		}
	}
	return true
}

// FileList is a set of files to archive, relative to the archived folder.
type FileList struct {
	files map[string]bool
	// folders holds the folders that have a listed file below them.
	folders map[string]bool
}

// NewFileList returns the list of files, given as slash-separated paths
// relative to the archived folder.
func NewFileList(files []string) *FileList {
	l := &FileList{files: map[string]bool{}, folders: map[string]bool{}}
	for _, file := range files {
		file = path.Clean(file)
		l.files[file] = true
		for dir := path.Dir(file); dir != "." && dir != "/" && !l.folders[dir]; dir = path.Dir(dir) {
			l.folders[dir] = true
		}
	}
	return l
}

// Len returns the number of files in the list.
func (l *FileList) Len() int {
	return len(l.files)
}

// hasFile reports whether the file relativePath is listed. A nil list lists
// every file.
func (l *FileList) hasFile(relativePath string) bool {
	return l == nil || l.files[relativePath]
}

// hasFolder reports whether a file in the folder relativePath is listed, or
// the folder itself is, as git lists a submodule that isn't checked out. A nil
// list lists every folder, and the root is always listed.
func (l *FileList) hasFolder(relativePath string) bool {
	return l == nil || relativePath == "" || relativePath == "." || l.folders[relativePath] || l.files[relativePath]
}
