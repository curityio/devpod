package upgrade

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
		return &install{binary: path}, nil
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

	return &install{
		binary:     binary,
		link:       path,
		linkTarget: filepath.Join(filepath.Dir(rawTarget), name),
		previous:   target,
	}, nil
}

// dir is the directory the new release is downloaded to.
func (in *install) dir() string {
	return filepath.Dir(in.binary)
}

// checkPermissions fails if the current user can't do the install, so that Upgrade can stop before downloading
// anything.
func (in *install) checkPermissions() error {
	if err := checkCanCreateIn(in.dir()); err != nil {
		return err
	}
	if _, err := os.Lstat(in.binary); err == nil {
		if err := checkCanRename(in.binary); err != nil {
			return err
		}
	}
	if in.link != "" {
		if err := checkCanCreateIn(filepath.Dir(in.link)); err != nil {
			return err
		}
		if err := checkCanRename(in.link); err != nil {
			return err
		}
	}
	return nil
}

// apply moves the downloaded release into place and, if there is a link, points the link to it.
func (in *install) apply(downloaded string) error {
	if err := replaceFile(downloaded, in.binary); err != nil {
		return fmt.Errorf("install %s: %w", in.binary, err)
	}
	if in.link == "" {
		return nil
	}

	tmpLink := filepath.Join(filepath.Dir(in.link), "."+filepath.Base(in.link)+".new-link")
	_ = os.Remove(tmpLink)
	if err := os.Symlink(in.linkTarget, tmpLink); err != nil {
		return fmt.Errorf("point %s to %s: %w", in.link, in.binary, err)
	}
	if err := os.Rename(tmpLink, in.link); err != nil {
		_ = os.Remove(tmpLink)
		return fmt.Errorf("point %s to %s: %w", in.link, in.binary, err)
	}
	return nil
}

// checkCanCreateIn fails if the current user can't create files in dir.
func checkCanCreateIn(dir string) error {
	f, err := os.CreateTemp(dir, ".devpod-permission-check-*")
	if err != nil {
		var pathErr *fs.PathError
		if errors.As(err, &pathErr) {
			err = pathErr.Err
		}
		return fmt.Errorf("the current user can't create files in %s: %w", dir, err)
	}
	_ = f.Close()
	_ = os.Remove(f.Name())
	return nil
}

// runningBinary returns the path the running binary was started from. If it was started through a symlink, that
// is the link, which os.Executable resolves on some platforms (such as Linux).
func runningBinary() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("find the devpod binary: %w", err)
	}

	invoked := os.Args[0]
	if filepath.Base(invoked) == invoked {
		if invoked, err = exec.LookPath(invoked); err != nil {
			return exe, nil
		}
	}
	if invoked, err = filepath.Abs(invoked); err != nil {
		return exe, nil
	}

	// os.Args[0] can be anything, so only trust it if it leads to the running binary.
	invokedInfo, err := os.Stat(invoked)
	if err != nil {
		return exe, nil
	}
	exeInfo, err := os.Stat(exe)
	if err != nil || !os.SameFile(invokedInfo, exeInfo) {
		return exe, nil
	}
	return invoked, nil
}
