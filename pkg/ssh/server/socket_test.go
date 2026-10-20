//go:build !windows

package server

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// shortTempDir returns a temporary directory with a path short enough for a unix socket.
func shortTempDir(t *testing.T) string {
	dir, err := os.MkdirTemp("", "server")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// staleSocket creates a socket at path that nothing listens on.
func staleSocket(t *testing.T, path string) {
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	l.SetUnlinkOnClose(false)
	_ = l.Close()
}

func TestRemoveStaleSocketMissing(t *testing.T) {
	if err := removeStaleSocket(filepath.Join(shortTempDir(t), "s.sock")); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveStaleSocketRemovesStaleSocket(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "s.sock")
	staleSocket(t, path)

	if err := removeStaleSocket(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("Expected %s to be removed, got %v", path, err)
	}
}

func TestRemoveStaleSocketKeepsSocketInUse(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "s.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	if err := removeStaleSocket(path); err == nil {
		t.Fatal("Expected an error for a socket in use")
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("Expected %s to be kept: %v", path, err)
	}
}

func TestRemoveStaleSocketKeepsOtherFiles(t *testing.T) {
	dir := shortTempDir(t)
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{file, link, dir} {
		if err := removeStaleSocket(path); err == nil {
			t.Fatalf("Expected an error for %s", path)
		}
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("Expected %s to be kept: %v", path, err)
		}
	}
}
