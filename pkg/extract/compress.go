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
	"time"

	"github.com/moby/patternmatcher"
	"github.com/pkg/errors"
)

// TarOptions configures WriteTarWithOptions.
type TarOptions struct {
	// Compress gzips the archive.
	Compress bool
	// Excludes are patterns relative to the archived folder, with the same
	// syntax as a .dockerignore file. They take precedence over Files: an
	// exclude pattern leaves out a listed file, and a negation (!) pattern
	// adds a file that isn't listed.
	Excludes []string
	// Files, when not nil, lists the files to archive. A file no exclude
	// pattern matches is only archived if it is listed.
	Files *FileList
	// KeepNames are names of folders that are archived whole wherever the walk
	// reaches them, whatever the excludes and files say.
	KeepNames []string
	// Overlays are folders archived whole at the root of the archive under
	// their own name, in place of the archived folder's entry of that name.
	Overlays []string
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
	archiver.files = opts.Files
	archiver.keepNames = opts.KeepNames
	for _, overlay := range opts.Overlays {
		archiver.overlaid = append(archiver.overlaid, filepath.Base(overlay))
	}
	if err := archiver.AddToArchive(relativePath); err != nil {
		return err
	}

	for _, overlay := range opts.Overlays {
		whole := &Archiver{basePath: filepath.Dir(overlay), writer: tarWriter, writtenFiles: map[string]bool{}}
		if err := whole.AddToArchive(filepath.Base(overlay)); err != nil {
			return errors.Wrapf(err, "archive %s", overlay)
		}
	}
	return nil
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
	excludes *ExcludeRules
	// files lists the files archived when no exclude pattern matches them. It
	// is nil when they are all archived.
	files *FileList
	// keepNames are names of folders archived whole, whatever the excludes say.
	keepNames []string
	// overlaid are the names at the root that overlays replace.
	overlaid []string
}

// NewArchiver creates a new archiver. excludedPaths are patterns relative to
// basePath, with the same syntax as a .dockerignore file.
func NewArchiver(basePath string, writer *tar.Writer, excludedPaths []string) (*Archiver, error) {
	excludes, err := NewExcludeRules(excludedPaths)
	if err != nil {
		return nil, err
	}

	return &Archiver{
		basePath:     basePath,
		writer:       writer,
		writtenFiles: map[string]bool{},
		excludes:     excludes,
	}, nil
}

// AddToArchive adds a new path to the archive
func (a *Archiver) AddToArchive(relativePath string) error {
	return a.addToArchive(relativePath, patternmatcher.MatchInfo{})
}

// addToArchive adds a path to the archive. parentInfo holds the exclude
// results of the parent directory, or the zero value when they are unknown.
func (a *Archiver) addToArchive(relativePath string, parentInfo patternmatcher.MatchInfo) error {
	if a.writtenFiles[relativePath] || slices.Contains(a.overlaid, relativePath) {
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

	verdict, _, err := a.excludes.match(relativePath, parentInfo, a.files != nil)
	if err != nil {
		return err
	}
	if verdict == Excluded || (verdict == Unmatched && !a.files.hasFile(relativePath)) {
		return nil
	}
	return a.tarFile(relativePath, stat)
}

// addFolder adds a folder and what is in it to the archive.
func (a *Archiver) addFolder(relativePath string, stat os.FileInfo, parentInfo patternmatcher.MatchInfo) error {
	if a.isKept(relativePath) {
		return a.tarFolderWhole(relativePath, stat)
	}

	verdict, info, err := a.excludes.match(relativePath, parentInfo, a.files != nil)
	if err != nil {
		return err
	}
	archived := verdict == Included || (verdict == Unmatched && a.files.hasFolder(relativePath))

	// Skip a folder that isn't archived without reading it, unless a negation
	// pattern could re-include something below it.
	if !archived && !a.mayReincludeBelow(relativePath) {
		return nil
	}

	// Recursively tar folder
	return a.tarFolder(relativePath, stat, archived, info)
}

// isKept reports whether the folder relativePath is archived whole.
func (a *Archiver) isKept(relativePath string) bool {
	return (a.excludes != nil || a.files != nil) && slices.Contains(a.keepNames, path.Base(relativePath))
}

// tarFolderWhole archives a folder and everything in it, ignoring the excludes.
func (a *Archiver) tarFolderWhole(relativePath string, stat os.FileInfo) error {
	whole := &Archiver{basePath: a.basePath, writer: a.writer, writtenFiles: a.writtenFiles}
	return whole.tarFolder(relativePath, stat, true, patternmatcher.MatchInfo{})
}

// mayReincludeBelow reports whether a negation pattern could match a path
// inside the folder dir.
func (a *Archiver) mayReincludeBelow(dir string) bool {
	return a.excludes.mayReincludeBelow(dir)
}

// tarFolder archives what is in a folder. archived tells whether the folder
// itself is archived, which matters when it is empty.
func (a *Archiver) tarFolder(target string, targetStat os.FileInfo, archived bool, matchInfo patternmatcher.MatchInfo) error {
	filePath := path.Join(a.basePath, target)
	files, err := os.ReadDir(filePath)
	if err != nil {
		// config.Logf("[Upstream] Couldn't read dir %s: %s\n", filepath, err.Error())
		return nil
	}

	if len(files) == 0 && target != "" && archived {
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
