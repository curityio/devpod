// Package filetransfer copies files and directories between two file systems, such as the local one and a dev
// container's, reached over SFTP.
package filetransfer

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/pkg/sftp"
)

// ErrSymlinkNotPermitted is returned by Symlink on Windows when the user may not create symlinks, which needs
// Developer Mode or an administrator.
var ErrSymlinkNotPermitted = errors.New("creating symlinks isn't permitted")

// FS is a file system that files can be copied from or to.
type FS interface {
	// Abs makes p absolute.
	Abs(p string) (string, error)
	Join(elem ...string) string
	Base(p string) string
	Dir(p string) string
	IsSeparator(c byte) bool
	// ValidName reports whether name can be used as a single file name, so that it can't refer to another
	// directory.
	ValidName(name string) bool
	// ToSlash and FromSlash convert a symlink target between this file system's separator and /.
	ToSlash(p string) string
	FromSlash(p string) string

	Stat(p string) (fs.FileInfo, error)
	Lstat(p string) (fs.FileInfo, error)
	// ReadDir lists a directory without following symlinks.
	ReadDir(p string) ([]fs.FileInfo, error)
	Readlink(p string) (string, error)

	Open(p string) (io.ReadCloser, error)
	// CreateNew creates a file that doesn't exist yet, which only its owner can read and write.
	CreateNew(p string) (io.WriteCloser, error)
	// Rename moves oldPath to newPath, replacing newPath if it exists and isn't a directory.
	Rename(oldPath, newPath string) error
	// Mkdir creates a directory that only its owner can use, until its permissions are set.
	Mkdir(p string) error
	Remove(p string) error
	Symlink(target, p string) error
	Chmod(p string, mode fs.FileMode) error
	Chtimes(p string, mtime time.Time) error
}

// LocalFS is the file system of the machine DevPod runs on.
type LocalFS struct{}

var _ FS = LocalFS{}

func (LocalFS) Abs(p string) (string, error)            { return filepath.Abs(p) }
func (LocalFS) Join(elem ...string) string              { return filepath.Join(elem...) }
func (LocalFS) Base(p string) string                    { return filepath.Base(p) }
func (LocalFS) Dir(p string) string                     { return filepath.Dir(p) }
func (LocalFS) IsSeparator(c byte) bool                 { return os.IsPathSeparator(c) }
func (LocalFS) ToSlash(p string) string                 { return filepath.ToSlash(p) }
func (LocalFS) FromSlash(p string) string               { return filepath.FromSlash(p) }
func (LocalFS) Readlink(p string) (string, error)       { return os.Readlink(p) }
func (LocalFS) Open(p string) (io.ReadCloser, error)    { return os.Open(p) }
func (LocalFS) Mkdir(p string) error                    { return os.Mkdir(p, 0o700) }
func (LocalFS) Remove(p string) error                   { return os.Remove(p) }
func (LocalFS) Chmod(p string, mode fs.FileMode) error  { return os.Chmod(p, mode) }
func (LocalFS) Chtimes(p string, mtime time.Time) error { return os.Chtimes(p, mtime, mtime) }

func (LocalFS) ValidName(name string) bool {
	invalid := `/`
	if runtime.GOOS == "windows" {
		// a colon names an alternate data stream
		invalid = `/\:`
	}
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, invalid)
}

func (LocalFS) CreateNew(p string) (io.WriteCloser, error) {
	return os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
}

func (LocalFS) Rename(oldPath, newPath string) error {
	err := os.Rename(oldPath, newPath)
	if err != nil && runtime.GOOS == "windows" {
		// Windows can't replace a read-only file
		if info, statErr := os.Lstat(newPath); statErr == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o200 == 0 {
			if os.Chmod(newPath, 0o600) == nil {
				return os.Rename(oldPath, newPath)
			}
		}
	}
	return err
}

func (LocalFS) Symlink(target, p string) error {
	err := os.Symlink(target, p)
	// ERROR_PRIVILEGE_NOT_HELD
	if err != nil && runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(1314)) {
		return errors.Join(ErrSymlinkNotPermitted, err)
	}
	return err
}

func (LocalFS) Stat(p string) (fs.FileInfo, error) {
	info, err := os.Stat(p)
	return localInfo(info), err
}

func (LocalFS) Lstat(p string) (fs.FileInfo, error) {
	info, err := os.Lstat(p)
	return localInfo(info), err
}

func (LocalFS) ReadDir(p string) ([]fs.FileInfo, error) {
	entries, err := os.ReadDir(p)
	if err != nil {
		return nil, err
	}
	infos := make([]fs.FileInfo, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		infos = append(infos, localInfo(info))
	}
	return infos, nil
}

// localInfo drops the group and world write bits on Windows, which reports every writable file as 0666 and every
// directory as 0777, so that files copied into a container aren't writable by everyone.
func localInfo(info fs.FileInfo) fs.FileInfo {
	if info == nil || runtime.GOOS != "windows" {
		return info
	}
	return windowsInfo{info}
}

type windowsInfo struct{ fs.FileInfo }

func (i windowsInfo) Mode() fs.FileMode { return i.FileInfo.Mode() &^ 0o022 }

// RemoteError is an error from an SFTPFS, so that it can be told apart from a local one.
type RemoteError struct {
	Err error
}

func (e *RemoteError) Error() string { return e.Err.Error() }
func (e *RemoteError) Unwrap() error { return e.Err }

func remote(err error) error {
	if err == nil {
		return nil
	}
	return &RemoteError{Err: err}
}

// SFTPFS is a file system reached over SFTP, such as a dev container's.
type SFTPFS struct {
	Client *sftp.Client
	// WorkDir is what relative paths are relative to.
	WorkDir string
}

var _ FS = SFTPFS{}

func (s SFTPFS) Abs(p string) (string, error) {
	if path.IsAbs(p) {
		return path.Clean(p), nil
	}
	return path.Join(s.WorkDir, p), nil
}

func (SFTPFS) Join(elem ...string) string { return path.Join(elem...) }
func (SFTPFS) Base(p string) string       { return path.Base(p) }
func (SFTPFS) Dir(p string) string        { return path.Dir(p) }
func (SFTPFS) IsSeparator(c byte) bool    { return c == '/' }
func (SFTPFS) ToSlash(p string) string    { return p }
func (SFTPFS) FromSlash(p string) string  { return p }

func (SFTPFS) ValidName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.Contains(name, "/")
}

func (s SFTPFS) Stat(p string) (fs.FileInfo, error) {
	info, err := s.Client.Stat(p)
	return info, remote(err)
}

func (s SFTPFS) Lstat(p string) (fs.FileInfo, error) {
	info, err := s.Client.Lstat(p)
	return info, remote(err)
}

func (s SFTPFS) ReadDir(p string) ([]fs.FileInfo, error) {
	infos, err := s.Client.ReadDir(p)
	return infos, remote(err)
}

func (s SFTPFS) Readlink(p string) (string, error) {
	target, err := s.Client.ReadLink(p)
	return target, remote(err)
}

func (s SFTPFS) Open(p string) (io.ReadCloser, error) {
	f, err := s.Client.Open(p)
	if err != nil {
		return nil, remote(err)
	}
	return f, nil
}

func (s SFTPFS) CreateNew(p string) (io.WriteCloser, error) {
	f, err := s.Client.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return nil, remote(err)
	}
	// the server creates files as 0644, so restrict it before writing anything
	err = f.Chmod(0o600)
	if err != nil {
		_ = f.Close()
		_ = s.Client.Remove(p)
		return nil, remote(err)
	}
	return f, nil
}

func (s SFTPFS) Rename(oldPath, newPath string) error {
	return remote(s.Client.PosixRename(oldPath, newPath))
}

func (s SFTPFS) Mkdir(p string) error {
	err := s.Client.Mkdir(p)
	if err != nil {
		return remote(err)
	}
	// The server creates directories as 0755 unless its umask is restricted, which the client of devpod cp asks
	// for but an older agent doesn't do; in that case, restrict it before anything is written into it.
	return remote(s.Client.Chmod(p, 0o700))
}
func (s SFTPFS) Remove(p string) error          { return remote(s.Client.Remove(p)) }
func (s SFTPFS) Symlink(target, p string) error { return remote(s.Client.Symlink(target, p)) }
func (s SFTPFS) Chmod(p string, m fs.FileMode) error {
	return remote(s.Client.Chmod(p, m))
}
func (s SFTPFS) Chtimes(p string, mtime time.Time) error {
	return remote(s.Client.Chtimes(p, mtime, mtime))
}
