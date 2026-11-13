package command

import (
	"runtime"
	"testing"

	"github.com/curityio/devpod/pkg/shelltest"
	"gotest.tools/assert"
)

func TestAsUser(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	command := Quote([]string{"/bin/echo", shelltest.Awkward})

	assert.Equal(t, AsUser("", command), command)
	assert.Equal(t, AsUser("root", command), command)

	words := shelltest.Words(t, AsUser(shelltest.Awkward, command))
	assert.DeepEqual(t, words, []string{"su", "-c", command, shelltest.Awkward})
	assert.DeepEqual(t, shelltest.Words(t, words[2]), []string{"/bin/echo", shelltest.Awkward})
}
