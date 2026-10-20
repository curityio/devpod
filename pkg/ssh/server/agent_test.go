//go:build !windows

package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSharedAgentSocketIsPrivate(t *testing.T) {
	dir := filepath.Join(shortTempDir(t), "auth-agent-test")
	l, cleanup, err := listenSharedAgentSocket(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	if err := checkPrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %o; want 600", fi.Mode().Perm())
	}
}

func TestSharedAgentSocketRejectsUnsafeDir(t *testing.T) {
	parent := shortTempDir(t)

	open := filepath.Join(parent, "open")
	if err := os.Mkdir(open, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(open, 0o777); err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(parent, "private")
	if err := os.Mkdir(private, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "link")
	if err := os.Symlink(private, link); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(parent, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	for _, dir := range []string{open, link, file} {
		if _, _, err := listenSharedAgentSocket(dir); err == nil {
			t.Fatalf("Expected an error for %s", dir)
		}
	}
}

func TestSharedAgentSocketIsReused(t *testing.T) {
	dir := filepath.Join(shortTempDir(t), "auth-agent-test")
	first, cleanupFirst, err := listenSharedAgentSocket(dir)
	if err != nil {
		t.Fatal(err)
	}

	// only one session at a time can listen on the socket
	if _, _, err := listenSharedAgentSocket(dir); err == nil {
		t.Fatal("Expected an error while the socket is in use")
	}
	if _, err := os.Lstat(first.Addr().String()); err != nil {
		t.Fatalf("Expected the socket to be kept: %v", err)
	}

	// the next session listens on the same path once the first one ended
	cleanupFirst()
	second, cleanupSecond, err := listenSharedAgentSocket(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupSecond()
	if second.Addr().String() != first.Addr().String() {
		t.Fatalf("socket = %s; want %s", second.Addr().String(), first.Addr().String())
	}
}

func TestSharedAgentSocketReplacesStaleSocket(t *testing.T) {
	dir := filepath.Join(shortTempDir(t), "auth-agent-test")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	staleSocket(t, filepath.Join(dir, agentSocketFile))

	_, cleanup, err := listenSharedAgentSocket(dir)
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
}

func TestSharedAgentSocketCleanupKeepsOtherSessionsSocket(t *testing.T) {
	dir := filepath.Join(shortTempDir(t), "auth-agent-test")
	_, cleanup, err := listenSharedAgentSocket(dir)
	if err != nil {
		t.Fatal(err)
	}

	// something else in the directory, like the socket of the next session
	other := filepath.Join(dir, "other")
	if err := os.WriteFile(other, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	cleanup()
	if _, err := os.Lstat(other); err != nil {
		t.Fatalf("Expected %s to be kept: %v", other, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, agentSocketFile)); !os.IsNotExist(err) {
		t.Fatalf("Expected the socket to be removed, got %v", err)
	}
}

func TestSharedAgentSocketCleanupRemovesDir(t *testing.T) {
	dir := filepath.Join(shortTempDir(t), "auth-agent-test")
	_, cleanup, err := listenSharedAgentSocket(dir)
	if err != nil {
		t.Fatal(err)
	}

	cleanup()
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatalf("Expected %s to be removed, got %v", dir, err)
	}
}
