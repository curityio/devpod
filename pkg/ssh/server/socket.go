package server

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"syscall"
	"time"
)

// removeStaleSocket makes way for a new unix socket listener at path. It only
// removes a socket that nothing listens on and that this process may take
// over. Anything else is an error, so a client can't make the server delete
// arbitrary files or take over another session's socket.
func removeStaleSocket(path string) error {
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}

	if fi.Mode().Type() != fs.ModeSocket {
		return fmt.Errorf("%s exists and is not a socket", path)
	}
	if !mayReplaceSocket(fi) {
		return fmt.Errorf("socket %s belongs to another user", path)
	}

	conn, err := net.DialTimeout("unix", path, time.Second)
	if err == nil {
		_ = conn.Close()
		return fmt.Errorf("socket %s is in use", path)
	} else if errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if !errors.Is(err, syscall.ECONNREFUSED) {
		return fmt.Errorf("check socket %s: %w", path, err)
	}

	err = os.Remove(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	return nil
}
