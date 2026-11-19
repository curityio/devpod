package extract

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path"
	"strings"
	"time"

	perrors "github.com/pkg/errors"
)

type Options struct {
	StripLevels int
	KeepNewer   bool

	Perm *os.FileMode
	UID  *int
	GID  *int
}

type Option func(o *Options)

func StripLevels(levels int) Option {
	return func(o *Options) {
		o.StripLevels = levels
	}
}

// KeepNewer makes Extract keep a file, symlink or hard link that already exists and was modified
// after the one in the archive, instead of replacing it. A file it does replace is only replaced
// once it is written completely, so that an extraction that is interrupted doesn't leave a part
// of a file that looks newer the next time, and it keeps its owner.
func KeepNewer() Option {
	return func(o *Options) {
		o.KeepNewer = true
	}
}

func Extract(origReader io.Reader, destFolder string, options ...Option) error {
	extractOptions := &Options{}
	for _, o := range options {
		o(extractOptions)
	}

	// read ahead
	bufioReader := bufio.NewReaderSize(origReader, 1024*1024)
	testBytes, err := bufioReader.Peek(2) // read 2 bytes
	if err != nil {
		return err
	}

	// is gzipped?
	var reader io.Reader
	if testBytes[0] == 31 && testBytes[1] == 139 {
		gzipReader, err := gzip.NewReader(bufioReader)
		if err != nil {
			return perrors.Errorf("error decompressing: %v", err)
		}
		defer gzipReader.Close()

		reader = gzipReader
	} else {
		reader = bufioReader
	}

	tarReader := tar.NewReader(reader)
	for {
		shouldContinue, err := extractNext(tarReader, destFolder, extractOptions)
		if err != nil {
			return perrors.Wrap(err, "decompress")
		} else if !shouldContinue {
			return nil
		}
	}
}

func extractNext(tarReader *tar.Reader, destFolder string, options *Options) (bool, error) {
	header, err := tarReader.Next()
	if err != nil {
		if !errors.Is(err, io.EOF) {
			return false, perrors.Wrap(err, "tar reader next")
		}

		return false, nil
	}

	relativePath := getRelativeFromFullPath("/"+header.Name, "")
	if options.StripLevels > 0 {
		for i := 0; i < options.StripLevels; i++ {
			relativePath = strings.TrimPrefix(relativePath, "/")
			index := strings.Index(relativePath, "/")
			if index == -1 {
				break
			}

			relativePath = relativePath[index+1:]
		}

		relativePath = "/" + relativePath
	}
	outFileName := path.Join(destFolder, relativePath)
	baseName := path.Dir(outFileName)

	dirPerm := os.ModePerm
	if options.Perm != nil {
		dirPerm = *options.Perm
	}

	// Check if newer file is there and then don't override?
	if err := os.MkdirAll(baseName, dirPerm); err != nil {
		return false, err
	}

	// whats the file perm?
	filePerm := os.FileMode(0644)
	if options.Perm != nil {
		filePerm = *options.Perm
	}

	// Is dir?
	if header.Typeflag == tar.TypeDir {
		if err := os.MkdirAll(outFileName, dirPerm); err != nil {
			return false, err
		}

		return true, nil
	}

	var existing os.FileInfo
	if options.KeepNewer {
		existing, _ = os.Lstat(outFileName)
		if existing != nil && keepExisting(existing, header) {
			return true, nil
		}
	}

	if header.Typeflag == tar.TypeSymlink {
		err := replaceExisting(outFileName, func() error { return os.Symlink(header.Linkname, outFileName) })
		if err != nil {
			return false, err
		}

		return true, nil
	} else if header.Typeflag == tar.TypeLink {
		err := replaceExisting(outFileName, func() error { return os.Link(header.Linkname, outFileName) })
		if err != nil {
			return false, err
		}

		return true, nil
	}

	// Create / Override file
	writeName := outFileName
	if options.KeepNewer {
		writeName = outFileName + partialSuffix
	}
	outFile, err := os.OpenFile(writeName, os.O_RDWR|os.O_CREATE|os.O_TRUNC, filePerm)
	if err != nil {
		// Try again after 5 seconds
		time.Sleep(time.Second * 5)
		outFile, err = os.OpenFile(writeName, os.O_RDWR|os.O_CREATE|os.O_TRUNC, filePerm)
		if err != nil {
			return false, perrors.Wrapf(err, "create %s", writeName)
		}
	}
	defer outFile.Close()

	if _, err := io.Copy(outFile, tarReader); err != nil {
		return false, perrors.Wrapf(err, "io copy tar reader %s", outFileName)
	}
	if err := outFile.Close(); err != nil {
		return false, perrors.Wrapf(err, "out file close %s", outFileName)
	}

	// Set permissions
	if options.Perm == nil {
		_ = os.Chmod(writeName, header.FileInfo().Mode()|0600)
	}

	// Set mod time from tar header
	_ = os.Chtimes(writeName, time.Now(), header.FileInfo().ModTime())

	if writeName != outFileName {
		if existing != nil && existing.Mode().IsRegular() {
			chownLike(writeName, existing)
		}
		if err := os.Rename(writeName, outFileName); err != nil {
			_ = os.Remove(writeName)
			return false, perrors.Wrapf(err, "replace %s", outFileName)
		}
	}

	return true, nil
}

// partialSuffix ends the name of a file that is being written, before it replaces the file.
const partialSuffix = ".devpod-partial"

// keepExisting returns whether existing, which is where an entry of the archive goes, is kept: it
// was modified after the entry, or it is a file of the same time and size, which is taken to be
// the same file. A directory is never kept, so that a file can't replace it.
func keepExisting(existing os.FileInfo, header *tar.Header) bool {
	if existing.IsDir() {
		return false
	}
	modTime := header.FileInfo().ModTime()
	if existing.ModTime().After(modTime) {
		return true
	}
	return header.FileInfo().Mode().IsRegular() && existing.Mode().IsRegular() &&
		existing.ModTime().Equal(modTime) && existing.Size() == header.Size
}

// replaceExisting runs create, which creates a link at name, and if something that is not a
// directory with files is already there, removes that and runs create again: unlike a file, a
// link can't be written over.
func replaceExisting(name string, create func() error) error {
	err := create()
	if err == nil || !os.IsExist(err) {
		return err
	}
	if removeErr := os.Remove(name); removeErr != nil {
		return err
	}
	return create()
}

func getRelativeFromFullPath(fullpath string, prefix string) string {
	return strings.TrimPrefix(strings.ReplaceAll(strings.ReplaceAll(fullpath[len(prefix):], "\\", "/"), "//", "/"), ".")
}
