//go:build !windows

package sshd

import (
	"bytes"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gossh "golang.org/x/crypto/ssh"
)

// shortTempDir returns a temporary directory with a path short enough for a unix socket.
func shortTempDir(t *testing.T) string {
	dir, err := os.MkdirTemp("", "sshd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func newTestSessionWithUnixForwarding(t *testing.T, allow bool) *gossh.Client {
	forwardedUnixHandler := &ForwardedUnixHandler{}
	srv := &Server{
		Handler: func(s Session) {},
		LocalUnixForwardingCallback: func(ctx Context, socketPath string) bool {
			return allow
		},
		ReverseUnixForwardingCallback: func(ctx Context, socketPath string) bool {
			return allow
		},
		ChannelHandlers: map[string]ChannelHandler{
			"session":                        DefaultSessionHandler,
			"direct-streamlocal@openssh.com": DirectStreamLocalHandler,
		},
		RequestHandlers: map[string]RequestHandler{
			"streamlocal-forward@openssh.com":        forwardedUnixHandler.HandleSSHRequest,
			"cancel-streamlocal-forward@openssh.com": forwardedUnixHandler.HandleSSHRequest,
		},
	}
	// not newTestSession, as its serveOnce replaces the channel handlers
	l := newLocalListener()
	go func() { _ = srv.Serve(l) }()
	_, client, cleanup := newClientSession(t, l.Addr().String(), nil)
	t.Cleanup(func() {
		cleanup()
		_ = srv.Close()
	})
	return client
}

func TestLocalUnixForwardingWorks(t *testing.T) {
	t.Parallel()

	socketPath := filepath.Join(shortTempDir(t), "s.sock")
	l, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		_, _ = conn.Write(sampleServerResponse)
		_ = conn.Close()
	}()

	client := newTestSessionWithUnixForwarding(t, true)
	conn, err := client.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Error connecting to %v: %v", socketPath, err)
	}
	result, err := io.ReadAll(conn)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result, sampleServerResponse) {
		t.Fatalf("result = %#v; want %#v", result, sampleServerResponse)
	}
}

func TestLocalUnixForwardingRespectsCallback(t *testing.T) {
	t.Parallel()

	client := newTestSessionWithUnixForwarding(t, false)
	_, err := client.Dial("unix", filepath.Join(shortTempDir(t), "s.sock"))
	if err == nil {
		t.Fatal("Expected error connecting but it succeeded")
	}
	if !strings.Contains(err.Error(), "unix forwarding is disabled") {
		t.Fatalf("Expected permission error but got %#v", err)
	}
}

func TestReverseUnixForwardingWorks(t *testing.T) {
	t.Parallel()

	// the server creates missing parent directories of the socket
	socketPath := filepath.Join(shortTempDir(t), "sub", "s.sock")
	client := newTestSessionWithUnixForwarding(t, true)
	l, err := client.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Error listening on %v: %v", socketPath, err)
	}
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		_, _ = conn.Write(sampleServerResponse)
		_ = conn.Close()
	}()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Error connecting to %v: %v", socketPath, err)
	}
	result, err := io.ReadAll(conn)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result, sampleServerResponse) {
		t.Fatalf("result = %#v; want %#v", result, sampleServerResponse)
	}

	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReverseUnixForwardingRespectsCallback(t *testing.T) {
	t.Parallel()

	client := newTestSessionWithUnixForwarding(t, false)
	_, err := client.Listen("unix", filepath.Join(shortTempDir(t), "s.sock"))
	if err == nil {
		t.Fatal("Expected error listening but it succeeded")
	}
}

func TestReverseUnixForwardingCancelIsPerConnection(t *testing.T) {
	t.Parallel()

	forwardedUnixHandler := &ForwardedUnixHandler{}
	addr := newMultiConnServer(t, &Server{
		Handler: func(s Session) {},
		ReverseUnixForwardingCallback: func(ctx Context, socketPath string) bool {
			return true
		},
		RequestHandlers: map[string]RequestHandler{
			"streamlocal-forward@openssh.com":        forwardedUnixHandler.HandleSSHRequest,
			"cancel-streamlocal-forward@openssh.com": forwardedUnixHandler.HandleSSHRequest,
		},
	})
	owner := newClient(t, addr)
	other := newClient(t, addr)

	socketPath := filepath.Join(shortTempDir(t), "s.sock")
	l, err := owner.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}

	// another connection can't cancel the forward
	ok, _, err := other.SendRequest("cancel-streamlocal-forward@openssh.com", true, gossh.Marshal(&remoteUnixForwardRequest{
		SocketPath: socketPath,
	}))
	if err != nil || !ok {
		t.Fatalf("cancel request failed: %v", err)
	}
	expectForwarded(t, "unix", socketPath, l)

	// the connection that made it can
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if conn, err := net.Dial("unix", socketPath); err == nil {
		_ = conn.Close()
		t.Fatal("Expected the forward to be cancelled")
	}
}
