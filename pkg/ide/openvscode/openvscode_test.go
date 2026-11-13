package openvscode

import (
	"runtime"
	"testing"

	"github.com/curityio/devpod/pkg/shelltest"
	"gotest.tools/assert"
)

func TestCommandsQuoteValues(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}

	binaryPath := "/home/" + shelltest.Awkward + "/.openvscode-server/bin/openvscode-server"
	assert.DeepEqual(t,
		shelltest.Words(t, installExtensionCommand(binaryPath, shelltest.Awkward)),
		[]string{binaryPath, "--install-extension", shelltest.Awkward},
	)
	assert.DeepEqual(t,
		shelltest.Words(t, serverCommand(binaryPath, "0.0.0.0'; id; '", "10800 `id`")),
		[]string{binaryPath, "server-local", "--without-connection-token", "--host", "0.0.0.0'; id; '", "--port", "10800 `id`"},
	)
}
