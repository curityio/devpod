package sshtunnel

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	config2 "github.com/skevetter/devpod/pkg/devcontainer/config"
	"github.com/skevetter/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExecuteCommand_ReturnsWhenContainerDies reproduces a container that
// stops right after setup sent its result: the SSH server helper fails and
// nothing ever answers on the SSH pipes. ExecuteCommand must still return.
func TestExecuteCommand_ReturnsWhenContainerDies(t *testing.T) {
	previousTimeout := cleanupTimeout
	cleanupTimeout = 200 * time.Millisecond
	t.Cleanup(func() { cleanupTimeout = previousTimeout })

	expected := &config2.Result{}
	opts := ExecuteCommandOptions{
		AgentInject: func(context.Context, string, *os.File, *os.File, io.WriteCloser) error {
			return errors.New("command terminated with exit code 137")
		},
		SSHCommand: "ssh-server",
		Command:    "setup",
		Log:        log.Discard,
		TunnelServerFunc: func(context.Context, io.WriteCloser, io.Reader) (*config2.Result, error) {
			return expected, nil
		},
	}

	type executeResult struct {
		result *config2.Result
		err    error
	}
	done := make(chan executeResult, 1)
	go func() {
		result, err := ExecuteCommand(context.Background(), opts)
		done <- executeResult{result: result, err: err}
	}()

	select {
	case r := <-done:
		assert.Same(t, expected, r.result)
		require.Error(t, r.err)
		assert.Contains(t, r.err.Error(), "exit code 137")
	case <-time.After(10 * time.Second):
		t.Fatal("ExecuteCommand did not return after the container died")
	}
}
