package inject

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/alessio/shellescape"
	"github.com/curityio/devpod/pkg/log"
	"github.com/curityio/devpod/pkg/shelltest"
	"gotest.tools/assert"
)

func TestParseBinaryRequest(t *testing.T) {
	assert.DeepEqual(t, parseBinaryRequest("ARM-true v1.2.3"), BinaryRequest{Arm: true, PreviousVersion: "v1.2.3"})
	assert.DeepEqual(t, parseBinaryRequest("ARM-false "), BinaryRequest{Arm: false})
	assert.DeepEqual(t, parseBinaryRequest("ARM-false"), BinaryRequest{Arm: false})
}

func TestInjectReplacesAgentOfOtherVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("inject.sh needs a POSIX shell")
	}

	agentPath := filepath.Join(t.TempDir(), "devpod")
	assert.NilError(t, os.WriteFile(agentPath, fakeAgent("v1.0.0"), 0755))

	requests, err := injectFakeAgent(agentPath, "v2.0.0")
	assert.NilError(t, err)
	assert.Equal(t, len(requests), 1)
	assert.Equal(t, requests[0].PreviousVersion, "v1.0.0")

	installed, err := os.ReadFile(agentPath)
	assert.NilError(t, err)
	assert.DeepEqual(t, installed, fakeAgent("v2.0.0"))

	// now that the version matches, nothing is injected
	requests, err = injectFakeAgent(agentPath, "v2.0.0")
	assert.NilError(t, err)
	assert.Equal(t, len(requests), 0)
}

func TestInjectInstallsMissingAgent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("inject.sh needs a POSIX shell")
	}

	agentPath := filepath.Join(t.TempDir(), "devpod")
	requests, err := injectFakeAgent(agentPath, "v2.0.0")
	assert.NilError(t, err)
	assert.Equal(t, len(requests), 1)
	assert.Equal(t, requests[0].PreviousVersion, "")
}

func TestInjectQuotesAgentPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("inject.sh needs a POSIX shell")
	}

	parent := t.TempDir()
	agentDir := filepath.Join(parent, shelltest.Awkward)
	agentPath := filepath.Join(agentDir, "dev pod $1")

	requests, err := injectFakeAgent(agentPath, "v2.0.0")
	assert.NilError(t, err)
	assert.Equal(t, len(requests), 1)

	installed, err := os.ReadFile(agentPath)
	assert.NilError(t, err)
	assert.DeepEqual(t, installed, fakeAgent("v2.0.0"))

	// the install created nothing besides the agent, such as files named after part of the path
	entries, err := os.ReadDir(parent)
	assert.NilError(t, err)
	assert.Equal(t, len(entries), 1)
	assert.Equal(t, entries[0].Name(), shelltest.Awkward)
	entries, err = os.ReadDir(agentDir)
	assert.NilError(t, err)
	assert.Equal(t, len(entries), 1)
	assert.Equal(t, entries[0].Name(), "dev pod $1")

	// the installed agent is found and runs
	requests, err = injectFakeAgent(agentPath, "v2.0.0")
	assert.NilError(t, err)
	assert.Equal(t, len(requests), 0)
}

// injectFakeAgent runs inject.sh locally to install a fake agent of the given version at agentPath.
func injectFakeAgent(agentPath, version string) ([]BinaryRequest, error) {
	var requests []BinaryRequest
	_, err := InjectAndExecute(
		context.Background(),
		func(ctx context.Context, command string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
			cmd := exec.CommandContext(ctx, "sh", "-c", command)
			cmd.Stdin = stdin
			cmd.Stdout = stdout
			cmd.Stderr = stderr
			return cmd.Run()
		},
		func(request BinaryRequest) (io.ReadCloser, error) {
			requests = append(requests, request)
			return io.NopCloser(bytes.NewReader(fakeAgent(version))), nil
		},
		&Params{
			AgentRemotePath: agentPath,
			ExistsCheck:     fmt.Sprintf(`[ "$(%s version 2>/dev/null || echo 'false')" != %s ]`, shellescape.Quote(agentPath), shellescape.Quote(version)),
			ShouldChmodPath: true,
		},
		nil,
		nil,
		nil,
		10*time.Second,
		log.Discard,
	)
	return requests, err
}

func fakeAgent(version string) []byte {
	return []byte("#!/bin/sh\nprintf '%s' '" + version + "'\n")
}
