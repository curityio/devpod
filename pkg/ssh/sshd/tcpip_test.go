package sshd

import (
	"bytes"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"

	gossh "golang.org/x/crypto/ssh"
)

var sampleServerResponse = []byte("Hello world")

func sampleSocketServer() net.Listener {
	l := newLocalListener()

	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		_, _ = conn.Write(sampleServerResponse)
		conn.Close()
	}()

	return l
}

func newTestSessionWithForwarding(t *testing.T, forwardingEnabled bool) (net.Listener, *gossh.Client, func()) {
	l := sampleSocketServer()

	_, client, cleanup := newTestSession(t, &Server{
		Handler: func(s Session) {},
		LocalPortForwardingCallback: func(ctx Context, destinationHost string, destinationPort uint32) bool {
			addr := net.JoinHostPort(destinationHost, strconv.FormatInt(int64(destinationPort), 10))
			if addr != l.Addr().String() {
				panic("unexpected destinationHost: " + addr)
			}
			return forwardingEnabled
		},
	}, nil)

	return l, client, func() {
		cleanup()
		l.Close()
	}
}

func TestLocalPortForwardingWorks(t *testing.T) {
	t.Parallel()

	l, client, cleanup := newTestSessionWithForwarding(t, true)
	defer cleanup()

	conn, err := client.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatalf("Error connecting to %v: %v", l.Addr().String(), err)
	}
	result, err := io.ReadAll(conn)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result, sampleServerResponse) {
		t.Fatalf("result = %#v; want %#v", result, sampleServerResponse)
	}
}

func TestLocalPortForwardingRespectsCallback(t *testing.T) {
	t.Parallel()

	l, client, cleanup := newTestSessionWithForwarding(t, false)
	defer cleanup()

	_, err := client.Dial("tcp", l.Addr().String())
	if err == nil {
		t.Fatalf("Expected error connecting to %v but it succeeded", l.Addr().String())
	}
	if !strings.Contains(err.Error(), "port forwarding is disabled") {
		t.Fatalf("Expected permission error but got %#v", err)
	}
}

// newMultiConnServer starts a server that serves any number of connections
// with the given request handlers and returns its address.
func newMultiConnServer(t *testing.T, srv *Server) string {
	l := newLocalListener()
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	return l.Addr().String()
}

func newClient(t *testing.T, addr string) *gossh.Client {
	_, client, cleanup := newClientSession(t, addr, nil)
	t.Cleanup(cleanup)
	return client
}

// expectForwarded checks that a connection to addr reaches the listener l of
// a remote forward.
func expectForwarded(t *testing.T, network, addr string, l net.Listener) {
	t.Helper()
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		_, _ = conn.Write(sampleServerResponse)
		_ = conn.Close()
	}()

	conn, err := net.Dial(network, addr)
	if err != nil {
		t.Fatalf("Error connecting to %v: %v", addr, err)
	}
	result, err := io.ReadAll(conn)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result, sampleServerResponse) {
		t.Fatalf("result = %#v; want %#v", result, sampleServerResponse)
	}
}

func TestReversePortForwardingCancelIsPerConnection(t *testing.T) {
	t.Parallel()

	forwardHandler := &ForwardedTCPHandler{}
	addr := newMultiConnServer(t, &Server{
		Handler: func(s Session) {},
		ReversePortForwardingCallback: func(ctx Context, host string, port uint32) bool {
			return true
		},
		RequestHandlers: map[string]RequestHandler{
			"tcpip-forward":        forwardHandler.HandleSSHRequest,
			"cancel-tcpip-forward": forwardHandler.HandleSSHRequest,
		},
	})
	owner := newClient(t, addr)
	other := newClient(t, addr)

	l, err := owner.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	forwardAddr := l.Addr().(*net.TCPAddr)

	// another connection can't cancel the forward
	ok, _, err := other.SendRequest("cancel-tcpip-forward", true, gossh.Marshal(&remoteForwardCancelRequest{
		BindAddr: "127.0.0.1",
		BindPort: uint32(forwardAddr.Port),
	}))
	if err != nil || !ok {
		t.Fatalf("cancel request failed: %v", err)
	}
	expectForwarded(t, "tcp", forwardAddr.String(), l)

	// the connection that made it can
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if conn, err := net.Dial("tcp", forwardAddr.String()); err == nil {
		_ = conn.Close()
		t.Fatal("Expected the forward to be cancelled")
	}
}
