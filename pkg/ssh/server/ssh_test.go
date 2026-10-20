//go:build !windows

package server

import (
	"bytes"
	"io"
	"net"
	"path/filepath"
	"testing"

	"github.com/loft-sh/devpod/pkg/log"
	gossh "golang.org/x/crypto/ssh"
)

func newTestClient(t *testing.T, addr string) *gossh.Client {
	client, err := gossh.Dial("tcp", addr, &gossh.ClientConfig{
		User:            "test",
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestReverseUnixForwardingKeepsOtherSessionsSocket(t *testing.T) {
	srv, err := NewServer("", nil, nil, "", "", log.Discard)
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = l.Close() })

	socketPath := filepath.Join(shortTempDir(t), "S.gpg-agent")
	first, err := newTestClient(t, l.Addr().String()).Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := first.Accept()
			if err != nil {
				return
			}
			_, _ = conn.Write([]byte("first"))
			_ = conn.Close()
		}
	}()

	// a second session can't take over the socket
	second := newTestClient(t, l.Addr().String())
	if _, err := second.Listen("unix", socketPath); err == nil {
		t.Fatal("Expected the second forward to be refused")
	}
	_ = second.Close()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Error connecting to %v: %v", socketPath, err)
	}
	result, err := io.ReadAll(conn)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result, []byte("first")) {
		t.Fatalf("result = %q; want %q", result, "first")
	}
}
