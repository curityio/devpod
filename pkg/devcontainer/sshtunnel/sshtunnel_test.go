package sshtunnel

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	config2 "github.com/loft-sh/devpod/pkg/devcontainer/config"
	"github.com/loft-sh/log"
	"gotest.tools/assert"
)

// TestExecuteCommand_ReturnsWhenContainerDies reproduces a container that
// stops right after setup sent its result: the SSH server helper ends without
// an error, as when it is killed by a signal, and nothing ever answers on the
// SSH pipes. ExecuteCommand must still return.
func TestExecuteCommand_ReturnsWhenContainerDies(t *testing.T) {
	previousTimeout := cleanupTimeout
	cleanupTimeout = 200 * time.Millisecond
	t.Cleanup(func() { cleanupTimeout = previousTimeout })

	expected := &config2.Result{}
	agentInject := func(context.Context, string, *os.File, *os.File, io.WriteCloser) error {
		return nil
	}
	tunnelServer := func(context.Context, io.WriteCloser, io.Reader) (*config2.Result, error) {
		return expected, nil
	}

	type executeResult struct {
		result *config2.Result
		err    error
	}
	done := make(chan executeResult, 1)
	go func() {
		result, err := ExecuteCommand(context.Background(), nil, false, true, agentInject, "ssh-server", "setup", log.Discard, tunnelServer)
		done <- executeResult{result: result, err: err}
	}()

	select {
	case r := <-done:
		assert.NilError(t, r.err)
		assert.Assert(t, r.result == expected)
	case <-time.After(10 * time.Second):
		t.Fatal("ExecuteCommand did not return after the container died")
	}
}
