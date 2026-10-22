package upgrade

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/loft-sh/devpod/pkg/log"
)

// install describes where Upgrade puts a new release.
type install struct {
	// binary is the file Upgrade writes: the running binary itself, or a new file next to the one the link points
	// to.
	binary string
	// link is the symlink the binary was started through, if it was, and linkTarget the value Upgrade points it
	// to.
	link, linkTarget string
	// previous is the binary the link pointed to, which Upgrade leaves alone.
	previous string
	// overwrites reports whether binary exists already although it isn't the running binary: a link's target
	// directory can already have a file named after the release.
	overwrites bool
	// like is the running binary, whose mode and owner the new one gets.
	like fs.FileInfo
}

// planInstall decides where to install release tag, given the path the running binary was started from. A regular
// file is replaced. A symlink to a regular file is pointed to a new file named after the release, next to that
// file. A symlink to another symlink isn't supported.
func planInstall(path, tag string) (*install, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("find the devpod binary: %w", err)
	}
	if info.Mode()&fs.ModeSymlink == 0 {
		return &install{binary: path, like: info}, nil
	}

	rawTarget, err := os.Readlink(path)
	if err != nil {
		return nil, fmt.Errorf("read the link %s: %w", path, err)
	}
	target := rawTarget
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	targetInfo, err := os.Lstat(target)
	if err != nil {
		return nil, fmt.Errorf("find the binary %s links to: %w", path, err)
	}
	if targetInfo.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s links to %s, which is another link; upgrading through more than one link isn't supported", path, target)
	}
	if !targetInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("%s links to %s, which isn't a file", path, target)
	}

	name := "devpod-" + tag
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if filepath.Base(name) != name {
		return nil, fmt.Errorf("release tag %q can't be used as a file name", tag)
	}
	binary := filepath.Join(filepath.Dir(target), name)
	if filepath.Clean(binary) == filepath.Clean(target) {
		return nil, fmt.Errorf("%s links to %s, which is where devpod %s would be downloaded to", path, target, tag)
	}

	_, err = os.Lstat(binary)
	return &install{
		binary:     binary,
		link:       path,
		linkTarget: filepath.Join(filepath.Dir(rawTarget), name),
		previous:   target,
		overwrites: err == nil,
		like:       targetInfo,
	}, nil
}

// dir is the directory the new release is downloaded to.
func (in *install) dir() string {
	return filepath.Dir(in.binary)
}

// dirs returns the directories the upgrade writes to, and has written to in the past: the one the new release is
// downloaded to, the link's, and the one of the binary the link points to (or of the running binary itself). That
// is where an interrupted upgrade can have left files behind.
func (in *install) dirs() []string {
	var dirs []string
	for _, path := range []string{in.binary, in.link, in.previous} {
		if path == "" {
			continue
		}
		if dir := filepath.Dir(path); !slices.Contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

// checkPermissions fails if the current user can't do the install, so that Upgrade can stop before downloading
// anything. It doesn't move or change the binary or the link: it creates and removes files next to them, and looks
// at what else could stop them from being replaced.
func (in *install) checkPermissions() error {
	if err := checkCanCreateIn(in.dir()); err != nil {
		return err
	}
	if _, err := os.Lstat(in.binary); err == nil {
		if err := checkCanReplace(in.binary); err != nil {
			return err
		}
	}
	if in.link != "" {
		if err := checkCanCreateIn(filepath.Dir(in.link)); err != nil {
			return err
		}
		if err := checkCanCreateLink(filepath.Dir(in.link), in.linkTarget); err != nil {
			return err
		}
		if err := checkCanReplace(in.link); err != nil {
			return err
		}
	}
	return nil
}

// apply moves the downloaded release into place and, if there is a link, points the link to it.
func (in *install) apply(downloaded string, log log.Logger) error {
	if err := copyOwner(downloaded, in.binary, in.like); err != nil {
		log.Warnf("The new %s %v", in.binary, err)
	}
	if err := os.Chmod(downloaded, in.like.Mode().Perm()); err != nil {
		return fmt.Errorf("install %s: %w", in.binary, err)
	}
	if err := replaceFile(downloaded, in.binary); err != nil {
		return fmt.Errorf("install %s: %w", in.binary, err)
	}
	if in.link == "" {
		return nil
	}

	tmpLink, err := createLink(filepath.Dir(in.link), linkPrefix, in.linkTarget)
	if err != nil {
		return fmt.Errorf("point %s to %s: %w", in.link, in.binary, err)
	}
	if err := os.Rename(tmpLink, in.link); err != nil {
		_ = os.Remove(tmpLink)
		return fmt.Errorf("point %s to %s: %w", in.link, in.binary, err)
	}
	return nil
}

// Names of the hidden files Upgrade creates, followed by a random part. Upgrade removes those an interrupted
// upgrade left behind (see removeStaleFiles).
const (
	downloadPrefix        = ".devpod-download-"
	permissionCheckPrefix = ".devpod-permission-check-"
	linkPrefix            = ".devpod-link-"
)

// checkCanCreateIn fails if the current user can't create, rename and remove files in dir. A directory flag such
// as append-only only lets files be created, so it is looked at first, to not leave the probe file behind.
func checkCanCreateIn(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("the current user can't create files in %s: %w", dir, cause(err))
	}
	if flags := preventingFlags(dir, info); flags != "" {
		return fmt.Errorf("files in %s can't be renamed or removed: it is %s", dir, flags)
	}

	f, err := os.CreateTemp(dir, permissionCheckPrefix+"*")
	if err != nil {
		return fmt.Errorf("the current user can't create files in %s: %w", dir, cause(err))
	}
	_ = f.Close()
	if err := os.Remove(f.Name()); err != nil {
		return fmt.Errorf("the current user can create files in %s, but not remove them: %w", dir, cause(err))
	}
	return nil
}

// checkCanCreateLink fails if the current user can't create a symlink to target in dir. Windows only lets users
// with a privilege, or in Developer Mode, do that.
func checkCanCreateLink(dir, target string) error {
	probe, err := createLink(dir, permissionCheckPrefix, target)
	if err != nil {
		return fmt.Errorf("the current user can't create links in %s: %w", dir, cause(err))
	}
	_ = os.Remove(probe)
	return nil
}

// createLink creates a symlink to target in dir, named prefix followed by a random part, so that concurrent
// upgrades, and links an interrupted one left behind, don't get in the way. It returns the link's path.
func createLink(dir, prefix, target string) (string, error) {
	var err error
	for range 10 {
		var random [8]byte
		if _, err = rand.Read(random[:]); err != nil {
			return "", err
		}
		link := filepath.Join(dir, prefix+hex.EncodeToString(random[:]))
		err = os.Symlink(target, link)
		if err == nil {
			return link, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", err
		}
	}
	return "", err
}

// cause returns the error of the system call behind err, without the operation and path that os adds, for
// messages that already name them.
func cause(err error) error {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err
	}
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		return linkErr.Err
	}
	return err
}

// runningBinary returns the path the running binary was started from. If it was started through a symlink, that
// is the link, which os.Executable resolves on some platforms (such as Linux).
func runningBinary() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("find the devpod binary: %w", err)
	}

	return invokedBinary(os.Args[0], exe), nil
}

// invokedBinary returns the path a binary was started from, given its os.Args[0] (a path, or a name found in
// PATH) and the path os.Executable returns. os.Args[0] can be anything, so it is only used if it leads to exe.
func invokedBinary(arg0, exe string) string {
	invoked := arg0
	if filepath.Base(invoked) == invoked {
		var err error
		if invoked, err = exec.LookPath(invoked); err != nil {
			return exe
		}
	}
	invoked, err := filepath.Abs(invoked)
	if err != nil {
		return exe
	}

	invokedInfo, err := os.Stat(invoked)
	if err != nil {
		return exe
	}
	exeInfo, err := os.Stat(exe)
	if err != nil || !os.SameFile(invokedInfo, exeInfo) {
		return exe
	}
	return invoked
}
