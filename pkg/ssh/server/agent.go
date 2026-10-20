package server

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"

	"github.com/loft-sh/devpod/pkg/ssh/sshd"
)

// agentSocketFile is the name of the agent socket in its directory, the same
// name sshd.NewAgentListener uses.
const agentSocketFile = "listener.sock"

// setupAgentListener creates the unix socket that forwards SSH agent
// connections to the client. The returned function closes the listener and
// removes what it created.
func setupAgentListener(reuseSock string) (net.Listener, func(), error) {
	// on some systems (like containers) /tmp may not exists, this ensures
	// that we have a compliant directory structure
	err := os.MkdirAll("/tmp", 0o777)
	if err != nil {
		return nil, nil, fmt.Errorf("create /tmp dir: %w", err)
	}

	// Check if we should create a "shared" socket to be reused by clients
	// used for browser tunnels such as openvscode, since the IDE itself doesn't create an SSH connection it uses a "backhaul" connection and uses the existing socket
	if reuseSock != "" {
		return listenSharedAgentSocket(filepath.Join(os.TempDir(), fmt.Sprintf("auth-agent-%s", reuseSock)))
	}

	l, tmpDir, err := sshd.NewAgentListener("")
	if err != nil {
		return nil, nil, fmt.Errorf("new agent listener: %w", err)
	}
	cleanup := func() {
		_ = l.Close()
		_ = os.RemoveAll(tmpDir)
	}

	err = os.Chmod(l.Addr().String(), 0o600)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("chmod agent socket: %w", err)
	}

	return l, cleanup, nil
}

// listenSharedAgentSocket listens on the agent socket in dir, a directory with
// a well-known name in /tmp that sessions with the same reuse id share, one
// at a time. Processes that outlive a session, like the openvscode server,
// keep using the socket path, and the next session listens on it again.
func listenSharedAgentSocket(dir string) (net.Listener, func(), error) {
	socketPath := filepath.Join(dir, agentSocketFile)
	for attempt := 0; ; attempt++ {
		err := os.Mkdir(dir, 0o700)
		if err == nil {
			// don't depend on the umask
			err = os.Chmod(dir, 0o700)
		} else if errors.Is(err, fs.ErrExist) {
			err = nil
		}
		if err != nil {
			return nil, nil, fmt.Errorf("create SSH_AUTH_SOCK dir in /tmp: %w", err)
		}

		// anyone can create the directory in /tmp before us
		err = checkPrivateDir(dir)
		if err != nil {
			return nil, nil, fmt.Errorf("check SSH_AUTH_SOCK dir: %w", err)
		}

		// a session that was killed leaves its socket behind
		err = removeStaleSocket(socketPath)
		if err != nil {
			return nil, nil, fmt.Errorf("new agent listener: %w", err)
		}

		l, err := net.Listen("unix", socketPath)
		if errors.Is(err, fs.ErrNotExist) && attempt == 0 {
			// a session that just ended removed the directory, create it again
			continue
		} else if err != nil {
			return nil, nil, fmt.Errorf("new agent listener: %w", err)
		}

		cleanup := func() {
			// Closing the listener removes the socket. Remove the directory
			// only if it's empty: the next session may already listen in it.
			_ = l.Close()
			_ = os.Remove(dir)
		}

		err = os.Chmod(socketPath, 0o600)
		if err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("chmod agent socket: %w", err)
		}

		return l, cleanup, nil
	}
}
