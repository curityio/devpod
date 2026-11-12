package filetransfer

import (
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/curityio/devpod/pkg/log"
	"github.com/google/go-cmp/cmp"
	"github.com/pkg/sftp"
)

// tree describes a directory's contents: a file's content, "dir/" for a directory, or "-> target" for a symlink.
type tree map[string]string

func writeTree(t *testing.T, root string, files tree) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		var err error
		switch {
		case content == "dir/":
			err = os.MkdirAll(p, 0o755)
		case len(content) > 3 && content[:3] == "-> ":
			err = os.Symlink(content[3:], p)
		default:
			err = os.WriteFile(p, []byte(content), 0o644)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func readTree(t *testing.T, root string) tree {
	t.Helper()
	files := tree{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == root {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			files[rel] = "-> " + target
			return err
		case d.IsDir():
			files[rel] = "dir/"
		default:
			content, err := os.ReadFile(p)
			files[rel] = string(content)
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// newSFTPFS serves the local file system over SFTP in-process, the way the container's SSH server does for devpod
// cp, with a 077 umask until the test ends.
func newSFTPFS(t *testing.T, workDir string) SFTPFS {
	t.Helper()
	setUmask(t, 0o077)
	return newSFTPFSWithoutUmask(t, workDir)
}

func newSFTPFSWithoutUmask(t *testing.T, workDir string) SFTPFS {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	server, err := sftp.NewServer(serverConn)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve() }()
	client, err := sftp.NewClientPipe(clientConn, clientConn, sftp.UseConcurrentWrites(true))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	return SFTPFS{Client: client, WorkDir: workDir}
}

type copyCase struct {
	name     string
	src      tree
	dst      tree
	srcArg   string // relative to the source root, with / separators
	dstArg   string // relative to the destination root, with / separators
	expected tree   // the destination root afterwards
	err      bool
	symlinks bool
}

var copyCases = []copyCase{
	{
		name:     "file into existing directory",
		src:      tree{"a.txt": "a"},
		srcArg:   "a.txt",
		dstArg:   "",
		expected: tree{"a.txt": "a"},
	},
	{
		name:     "file to new name",
		src:      tree{"a.txt": "a"},
		srcArg:   "a.txt",
		dstArg:   "b.txt",
		expected: tree{"b.txt": "a"},
	},
	{
		name:     "file into missing directory written with trailing slash",
		src:      tree{"a.txt": "a"},
		srcArg:   "a.txt",
		dstArg:   "new/dir/",
		expected: tree{"new": "dir/", "new/dir": "dir/", "new/dir/a.txt": "a"},
	},
	{
		name:     "file overwrites existing file",
		src:      tree{"a.txt": "new"},
		dst:      tree{"a.txt": "old content"},
		srcArg:   "a.txt",
		dstArg:   "a.txt",
		expected: tree{"a.txt": "new"},
	},
	{
		name:   "file onto path with trailing slash that is a file",
		src:    tree{"a.txt": "a"},
		dst:    tree{"b.txt": "b"},
		srcArg: "a.txt",
		dstArg: "b.txt/",
		err:    true,
	},
	{
		name:     "directory into existing directory",
		src:      tree{"temp/x.txt": "x", "temp/sub/y.txt": "y", "temp/empty": "dir/"},
		dst:      tree{"existing.txt": "e"},
		srcArg:   "temp/",
		dstArg:   "",
		expected: tree{"existing.txt": "e", "temp": "dir/", "temp/x.txt": "x", "temp/sub": "dir/", "temp/sub/y.txt": "y", "temp/empty": "dir/"},
	},
	{
		name:     "directory to missing nested directory written with trailing slash",
		src:      tree{"temp/x.txt": "x"},
		srcArg:   "temp",
		dstArg:   "a/b/",
		expected: tree{"a": "dir/", "a/b": "dir/", "a/b/x.txt": "x"},
	},
	{
		name:     "directory to new path",
		src:      tree{"temp/x.txt": "x", "temp/sub/y.txt": "y"},
		srcArg:   "temp",
		dstArg:   "copy",
		expected: tree{"copy": "dir/", "copy/x.txt": "x", "copy/sub": "dir/", "copy/sub/y.txt": "y"},
	},
	{
		name:     "directory contents merged into existing directory",
		src:      tree{"temp/x.txt": "x", "temp/sub/y.txt": "y"},
		dst:      tree{"sub/z.txt": "z", "x.txt": "old"},
		srcArg:   "temp/.",
		dstArg:   "",
		expected: tree{"x.txt": "x", "sub": "dir/", "sub/y.txt": "y", "sub/z.txt": "z"},
	},
	{
		name:   "directory onto file",
		src:    tree{"temp/x.txt": "x"},
		dst:    tree{"file": "f"},
		srcArg: "temp",
		dstArg: "file",
		err:    true,
	},
	{
		name:     "symlinks are copied as symlinks",
		src:      tree{"temp/x.txt": "x", "temp/link": "-> x.txt", "temp/dangling": "-> nowhere"},
		dst:      tree{"temp/link": "replaced"},
		srcArg:   "temp",
		dstArg:   "",
		expected: tree{"temp": "dir/", "temp/x.txt": "x", "temp/link": "-> x.txt", "temp/dangling": "-> nowhere"},
		symlinks: true,
	},
}

func runCopyCases(t *testing.T, newDst func(t *testing.T, root string) FS, toDstArg func(root, rel string) string) {
	for _, tc := range copyCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.symlinks && runtime.GOOS == "windows" {
				t.Skip("creating symlinks needs privileges on Windows")
			}
			srcRoot, dstRoot := t.TempDir(), t.TempDir()
			writeTree(t, srcRoot, tc.src)
			writeTree(t, dstRoot, tc.dst)

			srcArg := filepath.Join(srcRoot, filepath.FromSlash(tc.srcArg))
			if len(tc.srcArg) > 0 && tc.srcArg[len(tc.srcArg)-1] == '/' {
				srcArg += string(os.PathSeparator)
			} else if filepath.Base(tc.srcArg) == "." {
				srcArg = filepath.Join(srcRoot, filepath.FromSlash(filepath.Dir(tc.srcArg))) + string(os.PathSeparator) + "."
			}

			_, err := Copy(LocalFS{}, srcArg, newDst(t, dstRoot), toDstArg(dstRoot, tc.dstArg), log.Discard)
			if tc.err {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}

			expected := tree{}
			for name, content := range tc.dst {
				expected[name] = content
			}
			for name, content := range tc.expected {
				expected[name] = content
			}
			if diff := cmp.Diff(expected, readTree(t, dstRoot)); diff != "" {
				t.Errorf("unexpected destination (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCopyLocal(t *testing.T) {
	runCopyCases(t,
		func(t *testing.T, root string) FS { return LocalFS{} },
		func(root, rel string) string {
			p := filepath.Join(root, filepath.FromSlash(rel))
			if rel == "" || rel[len(rel)-1] == '/' {
				p += string(os.PathSeparator)
			}
			return p
		})
}

func TestCopyToSFTP(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the SFTP server is only used in Linux containers")
	}
	// relative to the work dir, as in a workspace
	runCopyCases(t,
		func(t *testing.T, root string) FS { return newSFTPFS(t, root) },
		func(root, rel string) string { return rel })
}

func TestCopyFromSFTP(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the SFTP server is only used in Linux containers")
	}
	remoteRoot, localRoot := t.TempDir(), t.TempDir()
	writeTree(t, remoteRoot, tree{"temp/x.txt": "x", "temp/sub/y.txt": "y", "temp/link": "-> x.txt"})

	stats, err := Copy(newSFTPFS(t, remoteRoot), "temp", LocalFS{}, localRoot, log.Discard)
	if err != nil {
		t.Fatal(err)
	}

	expected := tree{"temp": "dir/", "temp/x.txt": "x", "temp/sub": "dir/", "temp/sub/y.txt": "y", "temp/link": "-> x.txt"}
	if diff := cmp.Diff(expected, readTree(t, localRoot)); diff != "" {
		t.Errorf("unexpected destination (-want +got):\n%s", diff)
	}
	if stats.Files != 3 || stats.Bytes != 2 {
		t.Errorf("expected 3 files of 2 bytes, got %+v", stats)
	}
}

func TestCopyKeepsModeAndModTime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no executable bit")
	}
	srcRoot, dstRoot := t.TempDir(), t.TempDir()
	script := filepath.Join(srcRoot, "dir", "script.sh")
	writeTree(t, srcRoot, tree{"dir/script.sh": "#!/bin/sh"})
	if err := os.Chmod(script, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(srcRoot, "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	mtime := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, p := range []string{script, filepath.Join(srcRoot, "dir")} {
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}

	_, err := Copy(LocalFS{}, filepath.Join(srcRoot, "dir"), newSFTPFS(t, dstRoot), "", log.Discard)
	if err != nil {
		t.Fatal(err)
	}

	for p, mode := range map[string]fs.FileMode{"dir/script.sh": 0o750, "dir": 0o700 | fs.ModeDir} {
		info, err := os.Stat(filepath.Join(dstRoot, p))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode() != mode {
			t.Errorf("%s: expected mode %v, got %v", p, mode, info.Mode())
		}
		if !info.ModTime().Equal(mtime) {
			t.Errorf("%s: expected modification time %v, got %v", p, mtime, info.ModTime())
		}
	}
}

func TestCopyDirectoryIntoItself(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, tree{"dir/x.txt": "x"})

	_, err := Copy(LocalFS{}, filepath.Join(root, "dir"), LocalFS{}, filepath.Join(root, "dir", "inner")+string(os.PathSeparator), log.Discard)
	if err != nil {
		t.Fatal(err)
	}

	expected := tree{"dir": "dir/", "dir/x.txt": "x", "dir/inner": "dir/", "dir/inner/x.txt": "x"}
	if diff := cmp.Diff(expected, readTree(t, root)); diff != "" {
		t.Errorf("unexpected tree (-want +got):\n%s", diff)
	}
}

func TestCopyFileOntoItself(t *testing.T) {
	// as when the workspace folder is the local folder mounted into the container
	root := t.TempDir()
	writeTree(t, root, tree{"a.txt": "content", "dir/b.txt": "b"})

	// devpod cp a.txt foo: and devpod cp dir foo:, from the workspace folder
	for _, p := range []string{"a.txt", "dir"} {
		_, err := Copy(LocalFS{}, filepath.Join(root, p), LocalFS{}, root+string(os.PathSeparator), log.Discard)
		if err != nil {
			t.Fatal(err)
		}
	}

	expected := tree{"a.txt": "content", "dir": "dir/", "dir/b.txt": "b"}
	if diff := cmp.Diff(expected, readTree(t, root)); diff != "" {
		t.Errorf("unexpected tree (-want +got):\n%s", diff)
	}
}

func TestCopyReadOnlyTreeTwice(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no read-only directories")
	}
	srcRoot, dstRoot := t.TempDir(), t.TempDir()
	writeTree(t, srcRoot, tree{"repo/objects/ab/cdef": "object"})
	for p, mode := range map[string]fs.FileMode{"repo/objects/ab/cdef": 0o444, "repo/objects/ab": 0o555} {
		if err := os.Chmod(filepath.Join(srcRoot, p), mode); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		// let t.TempDir remove them
		for _, root := range []string{srcRoot, dstRoot} {
			_ = os.Chmod(filepath.Join(root, "repo/objects/ab"), 0o755)
		}
	})

	for i := 0; i < 2; i++ {
		_, err := Copy(LocalFS{}, filepath.Join(srcRoot, "repo"), newSFTPFS(t, dstRoot), "", log.Discard)
		if err != nil {
			t.Fatalf("copy %d: %v", i+1, err)
		}
	}

	expected := tree{"repo": "dir/", "repo/objects": "dir/", "repo/objects/ab": "dir/", "repo/objects/ab/cdef": "object"}
	if diff := cmp.Diff(expected, readTree(t, dstRoot)); diff != "" {
		t.Errorf("unexpected destination (-want +got):\n%s", diff)
	}
	info, err := os.Stat(filepath.Join(dstRoot, "repo/objects/ab"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o555 {
		t.Errorf("expected the directory to be read-only again, got %v", info.Mode())
	}
}

// strictFS rejects names that start with "bad", as Windows rejects names with a backslash or colon.
type strictFS struct{ LocalFS }

func (f strictFS) ValidName(name string) bool {
	return f.LocalFS.ValidName(name) && !strings.HasPrefix(name, "bad")
}

func TestCopySkipsInvalidNames(t *testing.T) {
	srcRoot, dstRoot := t.TempDir(), t.TempDir()
	writeTree(t, srcRoot, tree{"temp/ok.txt": "ok", "temp/bad.txt": "bad", "temp/bad-dir/x.txt": "x"})

	stats, err := Copy(LocalFS{}, filepath.Join(srcRoot, "temp"), strictFS{}, dstRoot, log.Discard)
	if err != nil {
		t.Fatal(err)
	}

	expected := tree{"temp": "dir/", "temp/ok.txt": "ok"}
	if diff := cmp.Diff(expected, readTree(t, dstRoot)); diff != "" {
		t.Errorf("unexpected destination (-want +got):\n%s", diff)
	}
	if stats.Files != 1 {
		t.Errorf("expected 1 file, got %+v", stats)
	}
}

func TestValidName(t *testing.T) {
	for _, name := range []string{"", ".", "..", "a/b"} {
		if (SFTPFS{}).ValidName(name) || (LocalFS{}).ValidName(name) {
			t.Errorf("expected %q to be invalid", name)
		}
	}
	for _, name := range []string{"a.txt", ".hidden"} {
		if !(SFTPFS{}).ValidName(name) || !(LocalFS{}).ValidName(name) {
			t.Errorf("expected %q to be valid", name)
		}
	}
	// see TestValidWindowsName for the rest of what Windows rejects
	for _, name := range []string{`..\..\evil`, "a:b", "...", "report?.txt", "CON"} {
		if !(SFTPFS{}).ValidName(name) {
			t.Errorf("expected %q to be valid in a container", name)
		}
		if (LocalFS{}).ValidName(name) == (runtime.GOOS == "windows") {
			t.Errorf("expected %q to be valid locally only if not on Windows", name)
		}
	}
}

func TestSFTPCreateNewIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the SFTP server is only used in Linux containers")
	}
	root := t.TempDir()
	remote := newSFTPFS(t, root)
	f, err := remote.CreateNew(filepath.Join(root, "secret"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	info, err := os.Stat(filepath.Join(root, "secret"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("expected a new file to be 0600 until it's written, got %v", info.Mode())
	}
}

func TestSFTPErrorsAreRemote(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the SFTP server is only used in Linux containers")
	}
	root := t.TempDir()
	_, err := Copy(newSFTPFS(t, root), "missing.txt", LocalFS{}, t.TempDir(), log.Discard)
	var remoteErr *RemoteError
	if !errors.As(err, &remoteErr) || !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("expected a remote not-exist error, got %v", err)
	}

	_, err = Copy(LocalFS{}, filepath.Join(root, "missing.txt"), newSFTPFS(t, root), "", log.Discard)
	if errors.As(err, &remoteErr) || !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("expected a local not-exist error, got %v", err)
	}
}

func TestCopyRejectsInvalidSourceName(t *testing.T) {
	srcRoot, dstRoot := t.TempDir(), t.TempDir()
	writeTree(t, srcRoot, tree{"bad.txt": "bad", "bad-dir/x.txt": "x"})

	// into an existing directory, into a directory written with a trailing slash, and into a missing directory
	for _, src := range []string{"bad.txt", "bad-dir"} {
		for _, dst := range []string{dstRoot, filepath.Join(dstRoot, "new") + string(os.PathSeparator)} {
			_, err := Copy(LocalFS{}, filepath.Join(srcRoot, src), strictFS{}, dst, log.Discard)
			if err == nil || !strings.Contains(err.Error(), "isn't a valid file name") {
				t.Errorf("copy %s to %s: expected an invalid name error, got %v", src, dst, err)
			}
		}
	}
	if files := readTree(t, dstRoot); len(files) > 1 || (len(files) == 1 && files["new"] != "dir/") {
		t.Errorf("expected nothing to be copied, got %v", files)
	}
}

// observingFS records the permissions of the directory each file is created in, at the time it's created.
type observingFS struct {
	LocalFS
	mu    sync.Mutex
	modes map[string]fs.FileMode
}

func (f *observingFS) CreateNew(p string) (io.WriteCloser, error) {
	info, err := os.Stat(filepath.Dir(p))
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.modes[filepath.Dir(p)] = info.Mode().Perm()
	f.mu.Unlock()
	return f.LocalFS.CreateNew(p)
}

func TestCopyKeepsNewDirectoriesPrivateUntilDone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no such permissions")
	}
	srcRoot, dstRoot := t.TempDir(), t.TempDir()
	writeTree(t, srcRoot, tree{"private/x.txt": "x", "private/sub/y.txt": "y"})
	if err := os.Chmod(filepath.Join(srcRoot, "private"), 0o700); err != nil {
		t.Fatal(err)
	}

	dst := &observingFS{modes: map[string]fs.FileMode{}}
	_, err := Copy(LocalFS{}, filepath.Join(srcRoot, "private"), dst, dstRoot, log.Discard)
	if err != nil {
		t.Fatal(err)
	}

	for dir, mode := range dst.modes {
		if mode != 0o700 {
			t.Errorf("expected %s to be 0700 while files were copied into it, got %v", dir, mode)
		}
	}
	if len(dst.modes) != 2 {
		t.Errorf("expected files in 2 directories, got %v", dst.modes)
	}
	for p, mode := range map[string]fs.FileMode{"private": 0o700, "private/sub": 0o755} {
		info, err := os.Stat(filepath.Join(dstRoot, p))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != mode {
			t.Errorf("expected %s to end up %v like its source, got %v", p, mode, info.Mode().Perm())
		}
	}
}

func TestCopyCreatesDestinationParentsWithUsualPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no such permissions")
	}
	srcRoot, dstRoot := t.TempDir(), t.TempDir()
	writeTree(t, srcRoot, tree{"a.txt": "a"})

	_, err := Copy(LocalFS{}, filepath.Join(srcRoot, "a.txt"), newSFTPFS(t, dstRoot), "x/y/", log.Discard)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"x", "x/y"} {
		info, err := os.Stat(filepath.Join(dstRoot, p))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o755 {
			t.Errorf("expected %s to be 0755, got %v", p, info.Mode().Perm())
		}
	}
}

func TestCopyLongFileName(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the SFTP server is only used in Linux containers")
	}
	// file systems limit names to 255 bytes
	name := strings.Repeat("n", 250)
	localRoot, remoteRoot := t.TempDir(), t.TempDir()
	writeTree(t, localRoot, tree{name: "up"})
	writeTree(t, remoteRoot, tree{"down/" + name: "down"})

	_, err := Copy(LocalFS{}, filepath.Join(localRoot, name), newSFTPFS(t, remoteRoot), "", log.Discard)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Copy(newSFTPFS(t, remoteRoot), "down", LocalFS{}, localRoot, log.Discard)
	if err != nil {
		t.Fatal(err)
	}

	if got := readTree(t, remoteRoot)[name]; got != "up" {
		t.Errorf("expected the upload to be copied, got %q", got)
	}
	if got := readTree(t, localRoot)["down/"+name]; got != "down" {
		t.Errorf("expected the download to be copied, got %q", got)
	}
}

func TestCopyIntoSymlinkedDestination(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs privileges on Windows")
	}
	srcRoot, dstRoot := t.TempDir(), t.TempDir()
	writeTree(t, srcRoot, tree{"temp/x.txt": "x"})
	writeTree(t, dstRoot, tree{"real": "dir/", "linked": "-> real", "workspace": "-> real"})

	// a destination directory that's a symlink, and a workspace folder that's one
	_, err := Copy(LocalFS{}, filepath.Join(srcRoot, "temp")+"/.", LocalFS{}, filepath.Join(dstRoot, "linked")+"/", log.Discard)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Copy(LocalFS{}, filepath.Join(srcRoot, "temp")+"/.", newSFTPFS(t, filepath.Join(dstRoot, "workspace")), "", log.Discard)
	if err != nil {
		t.Fatal(err)
	}

	expected := tree{"real": "dir/", "real/x.txt": "x", "linked": "-> real", "workspace": "-> real"}
	if diff := cmp.Diff(expected, readTree(t, dstRoot)); diff != "" {
		t.Errorf("unexpected destination (-want +got):\n%s", diff)
	}
}

func TestCopyRejectsSymlinkInDestinationTree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs privileges on Windows")
	}
	srcRoot, dstRoot := t.TempDir(), t.TempDir()
	writeTree(t, srcRoot, tree{"temp/sub/x.txt": "x"})
	writeTree(t, dstRoot, tree{"elsewhere": "dir/", "temp/sub": "-> ../elsewhere"})

	_, err := Copy(LocalFS{}, filepath.Join(srcRoot, "temp"), LocalFS{}, dstRoot, log.Discard)
	if err == nil || !strings.Contains(err.Error(), "is not a directory") {
		t.Errorf("expected a symlink in the destination tree to be rejected, got %v", err)
	}
	if files := readTree(t, filepath.Join(dstRoot, "elsewhere")); len(files) != 0 {
		t.Errorf("expected nothing to be written through the symlink, got %v", files)
	}
}

func TestCopyRestoresDirectoryPermissionsWhenItFails(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs read-only directories and a user that can't read everything")
	}
	srcRoot, dstRoot := t.TempDir(), t.TempDir()
	writeTree(t, srcRoot, tree{"repo/a.txt": "a", "repo/unreadable.txt": "u"})
	writeTree(t, dstRoot, tree{"repo/a.txt": "old"})
	if err := os.Chmod(filepath.Join(srcRoot, "repo/unreadable.txt"), 0); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dstRoot, "repo"), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dstRoot, "repo"), 0o755) })

	_, err := Copy(LocalFS{}, filepath.Join(srcRoot, "repo"), LocalFS{}, dstRoot, log.Discard)
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("expected the unreadable file to fail the copy, got %v", err)
	}

	info, err := os.Stat(filepath.Join(dstRoot, "repo"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o555 {
		t.Errorf("expected the directory to be read-only again, got %v", info.Mode().Perm())
	}
}

// noSymlinkFS can't create symlinks, like Windows without Developer Mode.
type noSymlinkFS struct{ LocalFS }

func (noSymlinkFS) Symlink(target, p string) error {
	return errors.Join(ErrSymlinkNotPermitted, errors.New("a required privilege is not held by the client"))
}

func TestCopySkippedSymlinkKeepsDestination(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs privileges on Windows")
	}
	srcRoot, dstRoot := t.TempDir(), t.TempDir()
	writeTree(t, srcRoot, tree{"temp/x.txt": "x", "temp/link": "-> x.txt"})
	writeTree(t, dstRoot, tree{"temp/link": "keep me"})

	_, err := Copy(LocalFS{}, filepath.Join(srcRoot, "temp"), noSymlinkFS{}, dstRoot, log.Discard)
	if err != nil {
		t.Fatal(err)
	}

	expected := tree{"temp": "dir/", "temp/x.txt": "x", "temp/link": "keep me"}
	if diff := cmp.Diff(expected, readTree(t, dstRoot)); diff != "" {
		t.Errorf("unexpected destination (-want +got):\n%s", diff)
	}
}

func TestValidWindowsName(t *testing.T) {
	for _, name := range []string{"a.txt", ".hidden", "...a", "con-notes.txt", "COM10", "my file.txt", "\u00e9t\u00e9.txt"} {
		if !validWindowsName(name) {
			t.Errorf("expected %q to be valid on Windows", name)
		}
	}
	for _, name := range []string{
		"", ".", "..", `..\..\evil`, "a/b", "a:b", "report?.txt", "a*b", `a"b`, "a<b", "a>b", "a|b", "tab\there",
		"trailing.", "trailing ", "CON", "con.txt", "Nul", "COM1.log", "lpt9", "AUX .txt", "COM\u00b9",
	} {
		if validWindowsName(name) {
			t.Errorf("expected %q to be invalid on Windows", name)
		}
	}
}

func TestSFTPRefusesFilesThatArentPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the SFTP server is only used in Linux containers")
	}
	srcRoot, dstRoot := t.TempDir(), t.TempDir()
	writeTree(t, srcRoot, tree{"a.txt": "a", "dir/b.txt": "b"})
	setUmask(t, 0o022)

	for _, src := range []string{"a.txt", "dir"} {
		_, err := Copy(LocalFS{}, filepath.Join(srcRoot, src), newSFTPFSWithoutUmask(t, dstRoot), "", log.Discard)
		if !errors.Is(err, ErrNotPrivate) {
			t.Errorf("copy %s: expected ErrNotPrivate from a server without the umask, got %v", src, err)
		}
	}
	if files := readTree(t, dstRoot); len(files) != 0 {
		t.Errorf("expected nothing to be left behind, got %v", files)
	}
}
