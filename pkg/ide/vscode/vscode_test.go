package vscode

import (
	"runtime"
	"testing"

	"github.com/curityio/devpod/pkg/shelltest"
	"gotest.tools/assert"
)

func TestInstallExtensionCommandQuotesValues(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}

	binPath := "/home/" + shelltest.Awkward + "/.vscode-server/bin/code-server"
	assert.DeepEqual(t,
		shelltest.Words(t, installExtensionCommand(binPath, shelltest.Awkward)),
		[]string{binPath, "serve-local", "--accept-server-license-terms", "--install-extension", shelltest.Awkward},
	)
}
