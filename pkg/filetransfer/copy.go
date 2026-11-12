package filetransfer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sync"

	"github.com/curityio/devpod/pkg/log"
	"github.com/docker/go-units"
	"golang.org/x/sync/errgroup"
)

// concurrency is how many files are copied at once, which hides the latency of each SFTP request.
const concurrency = 8

// Stats says what Copy copied.
type Stats struct {
	Files int
	Bytes int64
}

// String summarizes stats, such as "3 files (1.2kB)".
func (s Stats) String() string {
	files := "files"
	if s.Files == 1 {
		files = "file"
	}
	return fmt.Sprintf("%d %s (%s)", s.Files, files, units.HumanSize(float64(s.Bytes)))
}

// Copy copies srcPath on src to dstPath on dst, following the rules of `docker cp`:
//
//   - A file is copied into dstPath if that is a directory, and to dstPath otherwise.
//   - A directory is copied into dstPath if that is a directory, and to dstPath otherwise. A srcPath that ends with
//     "/." copies the directory's contents into dstPath instead.
//   - A dstPath that ends with a separator must be a directory, and is created, with any missing parents, if it
//     doesn't exist.
//
// Within a directory, symlinks are copied as symlinks, and files that aren't regular files, directories or symlinks
// are skipped. The permission bits and modification times are kept. Existing files are replaced: each file is
// written to a temporary file next to it and renamed over it, so that copying a file onto itself (through a mount)
// doesn't truncate it, and read-only files can be replaced.
func Copy(src FS, srcPath string, dst FS, dstPath string, log log.Logger) (Stats, error) {
	contentsOnly := endsWithDot(src, srcPath)
	dstMustBeDir := dstPath == "" || dst.IsSeparator(dstPath[len(dstPath)-1])

	srcPath, err := src.Abs(srcPath)
	if err != nil {
		return Stats{}, err
	}
	dstPath, err = dst.Abs(dstPath)
	if err != nil {
		return Stats{}, err
	}

	srcInfo, err := src.Stat(srcPath)
	if err != nil {
		return Stats{}, err
	}
	dstInfo, err := dst.Stat(dstPath)
	dstExists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Stats{}, err
	}

	if dstExists && !dstInfo.IsDir() && (dstMustBeDir || srcInfo.IsDir()) {
		return Stats{}, fmt.Errorf("%s is not a directory", dstPath)
	}
	// the source's name, when it's copied into dstPath, is checked like the names in a directory, so that it can't
	// refer to another directory on the destination
	join := func() (string, error) {
		if src.Dir(srcPath) == srcPath {
			// the root directory, which has no name: copy its contents
			return dstPath, nil
		}
		name := src.Base(srcPath)
		if !dst.ValidName(name) {
			return "", fmt.Errorf("can't copy %s: %q isn't a valid file name on the destination", srcPath, name)
		}
		return dst.Join(dstPath, name), nil
	}

	target := dstPath
	err = nil
	switch {
	case dstExists && dstInfo.IsDir():
		if !srcInfo.IsDir() || !contentsOnly {
			target, err = join()
		}
	case dstMustBeDir && srcInfo.IsDir():
		// dstPath becomes the copy of the directory, so only its parents are created here
		err = makeDirs(dst, dst.Dir(dstPath))
	case dstMustBeDir:
		err = makeDirs(dst, dstPath)
		if err == nil {
			target, err = join()
		}
	}
	if err != nil {
		return Stats{}, err
	}

	// List everything before writing anything, so that copying a directory into itself (which is possible when the
	// container's workspace folder is a local folder mounted into it) doesn't go on forever.
	c := &copier{src: src, dst: dst, log: log, created: map[string]bool{}}
	entries, err := c.walk(srcPath, srcInfo, target)
	if err != nil {
		return Stats{}, err
	}
	// a destination directory that was given, rather than derived from the source's name, may be a symlink to one,
	// such as a symlinked workspace folder
	entries[0].follow = target == dstPath

	// Directories first, parents before children, so that files can then be copied in any order.
	for _, e := range entries {
		if e.info.IsDir() {
			err = c.copyDir(e)
			if err != nil {
				c.restoreRelaxed()
				return c.stats, err
			}
		}
	}

	group, ctx := errgroup.WithContext(context.Background())
	group.SetLimit(concurrency)
	for _, e := range entries {
		if !e.info.IsDir() {
			group.Go(func() error {
				// stop at the first failure
				if ctx.Err() != nil {
					return nil
				}
				return c.copy(e)
			})
		}
	}
	err = group.Wait()
	if err != nil {
		c.restoreRelaxed()
		return c.stats, err
	}

	// Set directories' metadata last, as writing into them changes their modification time, and a read-only
	// directory can't be written into. Children go before their parents.
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].info.IsDir() {
			c.setDirMetadata(entries[i])
		}
	}

	return c.stats, nil
}

// makeDirs creates directory p and any missing parents, which aren't copies of source directories, so they are
// given the usual 0755 permissions instead of the private ones Mkdir creates directories with.
func makeDirs(f FS, p string) error {
	info, err := f.Stat(p)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("%s is not a directory", p)
		}
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	if parent := f.Dir(p); parent != p {
		err = makeDirs(f, parent)
		if err != nil {
			return err
		}
	}
	err = f.Mkdir(p)
	if err != nil {
		return fmt.Errorf("create directory %s: %w", p, err)
	}
	return f.Chmod(p, 0o755)
}

// endsWithDot reports whether p ends with a separator followed by ".", which means a directory's contents.
func endsWithDot(f FS, p string) bool {
	return len(p) >= 2 && p[len(p)-1] == '.' && f.IsSeparator(p[len(p)-2])
}

type entry struct {
	info             fs.FileInfo
	srcPath, dstPath string
	// dstDir is the directory dstPath is in.
	dstDir string
	// follow is set when an existing dstPath may be a symlink to a directory.
	follow bool
}

// relaxedDir is an existing directory that was made writable to copy into it.
type relaxedDir struct {
	path string
	mode fs.FileMode
}

type copier struct {
	src, dst FS
	log      log.Logger

	// created holds the directories this copy created, which can't have anything in them yet. It's only written
	// to before files are copied concurrently.
	created map[string]bool
	// relaxed holds the existing directories that were made writable, to restore them if the copy fails (otherwise
	// they get the source's permissions).
	relaxed []relaxedDir

	mu    sync.Mutex
	stats Stats
}

// walk lists srcPath and everything in it, skipping what can't be named on the destination.
func (c *copier) walk(srcPath string, srcInfo fs.FileInfo, target string) ([]entry, error) {
	entries := []entry{{info: srcInfo, srcPath: srcPath, dstPath: target, dstDir: c.dst.Dir(target)}}
	for i := 0; i < len(entries); i++ {
		parent := entries[i]
		if !parent.info.IsDir() {
			continue
		}
		children, err := c.src.ReadDir(parent.srcPath)
		if err != nil {
			return nil, fmt.Errorf("read directory %s: %w", parent.srcPath, err)
		}
		for _, child := range children {
			name := child.Name()
			if !c.src.ValidName(name) || !c.dst.ValidName(name) {
				c.log.Warnf("Skipping %q in %s, which isn't a valid file name on the destination", name, parent.srcPath)
				continue
			}
			entries = append(entries, entry{
				info:    child,
				srcPath: c.src.Join(parent.srcPath, name),
				dstPath: c.dst.Join(parent.dstPath, name),
				dstDir:  parent.dstPath,
			})
		}
	}
	return entries, nil
}

func (c *copier) copy(e entry) error {
	mode := e.info.Mode()
	switch {
	case mode&fs.ModeSymlink != 0:
		return c.copySymlink(e)
	case mode.IsRegular():
		return c.copyFile(e)
	default:
		c.log.Warnf("Skipping %s, which is not a regular file, directory or symlink", e.srcPath)
		return nil
	}
}

func (c *copier) copyDir(e entry) error {
	if !c.created[e.dstDir] {
		stat := c.dst.Lstat
		if e.follow {
			stat = c.dst.Stat
		}
		existing, err := stat(e.dstPath)
		switch {
		case err == nil && !existing.IsDir():
			return fmt.Errorf("%s is not a directory", e.dstPath)
		case err == nil:
			// a read-only directory, maybe from an earlier copy, has to be writable until its metadata is set
			if mode := existing.Mode().Perm(); mode&0o200 == 0 {
				if c.dst.Chmod(e.dstPath, mode|0o700) == nil {
					c.relaxed = append(c.relaxed, relaxedDir{path: e.dstPath, mode: mode})
				}
			}
			return nil
		case !errors.Is(err, fs.ErrNotExist):
			return err
		}
	}

	c.log.Debugf("Creating directory %s", e.dstPath)
	err := c.dst.Mkdir(e.dstPath)
	if err != nil {
		return fmt.Errorf("create directory %s: %w", e.dstPath, err)
	}
	c.created[e.dstPath] = true
	return nil
}

func (c *copier) copySymlink(e entry) error {
	target, err := c.src.Readlink(e.srcPath)
	if err != nil {
		return fmt.Errorf("read symlink %s: %w", e.srcPath, err)
	}
	target = c.dst.FromSlash(c.src.ToSlash(target))

	// created under a temporary name and renamed over the destination, so that what's there is only replaced
	// once the symlink exists
	c.log.Debugf("Creating symlink %s -> %s", e.dstPath, target)
	tmp := c.dst.Join(e.dstDir, ".devpod-cp-"+randomSuffix())
	err = c.dst.Symlink(target, tmp)
	if errors.Is(err, ErrSymlinkNotPermitted) {
		c.log.Warnf("Skipping symlink %s -> %s: %v", e.srcPath, target, err)
		return nil
	} else if err != nil {
		return fmt.Errorf("create symlink %s: %w", e.dstPath, err)
	}
	err = c.replace(tmp, e.dstPath)
	if err != nil {
		return err
	}
	c.count(0)
	return nil
}

// replace renames tmp over dstPath, and removes tmp if that fails.
func (c *copier) replace(tmp, dstPath string) error {
	err := c.dst.Rename(tmp, dstPath)
	if err == nil {
		return nil
	}
	_ = c.dst.Remove(tmp)
	if existing, statErr := c.dst.Lstat(dstPath); statErr == nil && existing.IsDir() {
		return fmt.Errorf("%s is a directory", dstPath)
	}
	return fmt.Errorf("replace %s: %w", dstPath, err)
}

// restoreRelaxed restores the permissions of the existing directories that were made writable, after a failure.
func (c *copier) restoreRelaxed() {
	for _, dir := range c.relaxed {
		err := c.dst.Chmod(dir.path, dir.mode)
		if err != nil {
			c.log.Warnf("Restore permissions of %s: %v", dir.path, err)
		}
	}
}

func (c *copier) copyFile(e entry) (err error) {
	c.log.Debugf("Copying %s to %s", e.srcPath, e.dstPath)
	in, err := c.src.Open(e.srcPath)
	if err != nil {
		return err
	}
	defer in.Close()

	// a short name, which stays within the file system's name length limit however long the file's name is
	tmp := c.dst.Join(e.dstDir, ".devpod-cp-"+randomSuffix())
	out, err := c.dst.CreateNew(tmp)
	if err != nil {
		return fmt.Errorf("create %s: %w", tmp, err)
	}
	defer func() {
		if err != nil {
			_ = c.dst.Remove(tmp)
		}
	}()

	n, err := io.Copy(out, in)
	closeErr := out.Close()
	// Windows can't replace a file that's open, which the source is when copying a file onto itself
	_ = in.Close()
	if err != nil {
		return fmt.Errorf("copy %s to %s: %w", e.srcPath, e.dstPath, err)
	} else if closeErr != nil {
		return fmt.Errorf("write %s: %w", tmp, closeErr)
	}

	err = c.dst.Chmod(tmp, e.info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("set permissions of %s: %w", tmp, err)
	}
	if chtimesErr := c.dst.Chtimes(tmp, e.info.ModTime()); chtimesErr != nil {
		c.log.Warnf("Set modification time of %s: %v", e.dstPath, chtimesErr)
	}

	err = c.replace(tmp, e.dstPath)
	if err != nil {
		return err
	}

	c.count(n)
	return nil
}

func (c *copier) count(bytes int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stats.Files++
	c.stats.Bytes += bytes
}

// setDirMetadata copies a directory's permission bits and modification time. It only warns of failures: an existing
// directory that belongs to another user can be written to, but not changed in these ways.
func (c *copier) setDirMetadata(e entry) {
	err := c.dst.Chmod(e.dstPath, e.info.Mode().Perm())
	if err != nil {
		c.log.Warnf("Set permissions of %s: %v", e.dstPath, err)
	}
	err = c.dst.Chtimes(e.dstPath, e.info.ModTime())
	if err != nil {
		c.log.Warnf("Set modification time of %s: %v", e.dstPath, err)
	}
}

func randomSuffix() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
