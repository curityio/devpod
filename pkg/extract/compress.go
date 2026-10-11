package extract

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/moby/patternmatcher"
	"github.com/pkg/errors"
)

// TarOptions configures WriteTarWithOptions.
type TarOptions struct {
	// Compress gzips the archive.
	Compress bool
	// Excludes are patterns relative to the archived folder, with the same
	// syntax as a .dockerignore file.
	Excludes []string
	// KeepNames are names of folders that are archived whole wherever the walk
	// reaches them, whatever the excludes say.
	KeepNames []string
}

func WriteTarExclude(writer io.Writer, localPath string, compress bool, excludedPaths []string) error {
	return WriteTarWithOptions(writer, localPath, TarOptions{Compress: compress, Excludes: excludedPaths})
}

// WriteTarWithOptions writes the file or the contents of the folder at
// localPath as a tar archive to writer.
func WriteTarWithOptions(writer io.Writer, localPath string, opts TarOptions) error {
	absolute, err := filepath.Abs(localPath)
	if err != nil {
		return errors.Wrap(err, "absolute")
	}

	// Check if target is there
	stat, err := os.Stat(absolute)
	if err != nil {
		return errors.Wrap(err, "stat")
	}

	// Use compression
	gw := writer
	if opts.Compress {
		gwWriter := gzip.NewWriter(writer)
		defer gwWriter.Close()

		gw = gwWriter
	}

	// Create tar writer
	tarWriter := tar.NewWriter(gw)
	defer tarWriter.Close()

	// When its a file we copy the file to the toplevel of the tar. When its a
	// folder we copy the contents and not the folder itself to the toplevel of
	// the tar
	basePath, relativePath := absolute, ""
	if !stat.IsDir() {
		basePath, relativePath = filepath.Dir(absolute), filepath.Base(absolute)
	}

	archiver, err := NewArchiver(basePath, tarWriter, opts.Excludes)
	if err != nil {
		return err
	}
	archiver.keepNames = opts.KeepNames
	return archiver.AddToArchive(relativePath)
}

func WriteTar(writer io.Writer, localPath string, compress bool) error {
	return WriteTarExclude(writer, localPath, compress, nil)
}

// Archiver is responsible for compressing specific files and folders within a target directory
type Archiver struct {
	basePath     string
	writer       *tar.Writer
	writtenFiles map[string]bool

	// excludes holds the exclude patterns, with .dockerignore semantics. It is
	// nil when nothing is excluded.
	excludes *patternmatcher.PatternMatcher
	// reincludes holds the components of each negation (!) pattern.
	reincludes [][]string
	// keepNames are names of folders archived whole, whatever the excludes say.
	keepNames []string
}

// NewArchiver creates a new archiver. excludedPaths are patterns relative to
// basePath, with the same syntax as a .dockerignore file.
func NewArchiver(basePath string, writer *tar.Writer, excludedPaths []string) (*Archiver, error) {
	var excludes *patternmatcher.PatternMatcher
	if len(excludedPaths) > 0 {
		var err error
		excludes, err = patternmatcher.New(excludedPaths)
		if err != nil {
			return nil, errors.Wrap(err, "parse exclude patterns")
		}
	}

	return &Archiver{
		basePath:     basePath,
		writer:       writer,
		writtenFiles: map[string]bool{},
		excludes:     excludes,
		reincludes:   reincludePatterns(excludes),
	}, nil
}

// reincludePatterns returns the components of each negation pattern.
func reincludePatterns(excludes *patternmatcher.PatternMatcher) [][]string {
	if excludes == nil || !excludes.Exclusions() {
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

// AddToArchive adds a new path to the archive
func (a *Archiver) AddToArchive(relativePath string) error {
	return a.addToArchive(relativePath, patternmatcher.MatchInfo{})
}

// addToArchive adds a path to the archive. parentInfo holds the exclude
// results of the parent directory, or the zero value when they are unknown.
func (a *Archiver) addToArchive(relativePath string, parentInfo patternmatcher.MatchInfo) error {
	if a.writtenFiles[relativePath] {
		return nil
	}

	// We skip files that are suddenly not there anymore
	stat, err := os.Lstat(path.Join(a.basePath, relativePath))
	if err != nil {
		// config.Logf("[Upstream] Couldn't stat file %s: %s\n", absFilepath, err.Error())
		return nil
	}

	if stat.IsDir() {
		return a.addFolder(relativePath, stat, parentInfo)
	}

	excluded, _, err := a.isExcluded(relativePath, parentInfo)
	if err != nil {
		return err
	}
	if excluded {
		return nil
	}
	return a.tarFile(relativePath, stat)
}

// addFolder adds a folder and what is in it to the archive.
func (a *Archiver) addFolder(relativePath string, stat os.FileInfo, parentInfo patternmatcher.MatchInfo) error {
	if a.isKept(relativePath) {
		return a.tarFolderWhole(relativePath, stat)
	}

	excluded, info, err := a.isExcluded(relativePath, parentInfo)
	if err != nil {
		return err
	}

	// Skip an excluded folder without reading it, unless a negation
	// pattern could re-include something below it.
	if excluded && !a.mayReincludeBelow(relativePath) {
		return nil
	}

	// Recursively tar folder
	return a.tarFolder(relativePath, stat, excluded, info)
}

// isKept reports whether the folder relativePath is archived whole.
func (a *Archiver) isKept(relativePath string) bool {
	return a.excludes != nil && slices.Contains(a.keepNames, path.Base(relativePath))
}

// tarFolderWhole archives a folder and everything in it, ignoring the excludes.
func (a *Archiver) tarFolderWhole(relativePath string, stat os.FileInfo) error {
	whole := &Archiver{basePath: a.basePath, writer: a.writer, writtenFiles: a.writtenFiles}
	return whole.tarFolder(relativePath, stat, false, patternmatcher.MatchInfo{})
}

// mayReincludeBelow reports whether a negation pattern could match a path
// inside the excluded folder dir. It may return true for a pattern that turns
// out not to match, but never false for one that does.
func (a *Archiver) mayReincludeBelow(dir string) bool {
	dirComponents := strings.Split(path.Clean(filepath.ToSlash(dir)), "/")
	for _, pattern := range a.reincludes {
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

// isExcluded matches relativePath against the exclude patterns. It also
// returns the match results to pass down to the path's children.
func (a *Archiver) isExcluded(relativePath string, parentInfo patternmatcher.MatchInfo) (bool, patternmatcher.MatchInfo, error) {
	if a.excludes == nil {
		return false, patternmatcher.MatchInfo{}, nil
	}

	// Patterns are relative to the archive root, so the root itself never matches
	relativePath = path.Clean(filepath.ToSlash(relativePath))
	if relativePath == "." || relativePath == "/" {
		return false, patternmatcher.MatchInfo{}, nil
	}

	excluded, info, err := a.excludes.MatchesUsingParentResults(relativePath, parentInfo)
	if err != nil {
		return false, info, errors.Wrapf(err, "match %s against exclude patterns", relativePath)
	}
	return excluded, info, nil
}

func (a *Archiver) tarFolder(target string, targetStat os.FileInfo, excluded bool, matchInfo patternmatcher.MatchInfo) error {
	filePath := path.Join(a.basePath, target)
	files, err := os.ReadDir(filePath)
	if err != nil {
		// config.Logf("[Upstream] Couldn't read dir %s: %s\n", filepath, err.Error())
		return nil
	}

	if len(files) == 0 && target != "" && !excluded {
		// Case empty directory
		hdr, _ := tar.FileInfoHeader(targetStat, filePath)
		hdr.Uid = 0
		hdr.Gid = 0
		hdr.Mode = fillGo18FileTypeBits(int64(chmodTarEntry(os.FileMode(hdr.Mode))), targetStat)
		hdr.Name = target
		if err := a.writer.WriteHeader(hdr); err != nil {
			return errors.Wrap(err, "tar write header")
		}
		a.writtenFiles[target] = true
	}

	for _, dirEntry := range files {
		f, err := dirEntry.Info()
		if err != nil {
			continue
		}

		if err = a.addToArchive(path.Join(target, f.Name()), matchInfo); err != nil {
			return errors.Wrap(err, "recursive tar "+f.Name())
		}
	}

	return nil
}

func (a *Archiver) tarFile(target string, targetStat os.FileInfo) error {
	var err error
	filepath := path.Join(a.basePath, target)

	// don't resolve symlinks
	linkName := ""
	if targetStat.Mode()&os.ModeSymlink == os.ModeSymlink {
		linkName, err = os.Readlink(filepath)
		if err != nil {
			return nil
		}
	}

	hdr, err := tar.FileInfoHeader(targetStat, linkName)
	if err != nil {
		return errors.Wrap(err, "create tar file info header")
	}
	hdr.Name = target
	hdr.Uid = 0
	hdr.Gid = 0
	hdr.Mode = fillGo18FileTypeBits(int64(chmodTarEntry(os.FileMode(hdr.Mode))), targetStat)
	hdr.ModTime = time.Unix(targetStat.ModTime().Unix(), 0)

	if err := a.writer.WriteHeader(hdr); err != nil {
		return errors.Wrap(err, "tar write header")
	}

	// nothing more to do for non-regular
	if !targetStat.Mode().IsRegular() {
		a.writtenFiles[target] = true
		return nil
	}

	// Case regular file
	f, err := os.Open(filepath)
	if err != nil {
		// We ignore open file and just treat it as okay
		return nil
	}
	defer f.Close()
	copied, err := io.CopyN(a.writer, f, targetStat.Size())
	if err != nil {
		return errors.Wrap(err, "tar copy file")
	} else if copied != targetStat.Size() {
		return errors.New("tar: file truncated during read")
	}

	a.writtenFiles[target] = true
	return nil
}

const (
	modeISDIR  = 040000  // Directory
	modeISFIFO = 010000  // FIFO
	modeISREG  = 0100000 // Regular file
	modeISLNK  = 0120000 // Symbolic link
	modeISBLK  = 060000  // Block special file
	modeISCHR  = 020000  // Character special file
	modeISSOCK = 0140000 // Socket
)

// chmodTarEntry is used to adjust the file permissions used in tar header based
// on the platform the archival is done.
func chmodTarEntry(perm os.FileMode) os.FileMode {
	if runtime.GOOS != "windows" {
		return perm
	}

	// perm &= 0755 // this 0-ed out tar flags (like link, regular file, directory marker etc.)
	permPart := perm & os.ModePerm
	noPermPart := perm &^ os.ModePerm
	// Add the x bit: make everything +x from windows
	permPart |= 0111
	permPart &= 0755

	return noPermPart | permPart
}

// fillGo18FileTypeBits fills type bits which have been removed on Go 1.9 archive/tar
// https://github.com/golang/go/commit/66b5a2f
func fillGo18FileTypeBits(mode int64, fi os.FileInfo) int64 {
	fm := fi.Mode()
	switch {
	case fm.IsRegular():
		mode |= modeISREG
	case fi.IsDir():
		mode |= modeISDIR
	case fm&os.ModeSymlink != 0:
		mode |= modeISLNK
	case fm&os.ModeDevice != 0:
		if fm&os.ModeCharDevice != 0 {
			mode |= modeISCHR
		} else {
			mode |= modeISBLK
		}
	case fm&os.ModeNamedPipe != 0:
		mode |= modeISFIFO
	case fm&os.ModeSocket != 0:
		mode |= modeISSOCK
	}
	return mode
}
