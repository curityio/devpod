package command

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"gotest.tools/assert"
)

// awkward contains characters that the shell treats specially, in and out of quotes.
const awkward = "o'brien \"quoted\" $HOME `id` \\ ; *"

// shellWords returns the words a POSIX shell splits command into, after quote removal, without running it.
func shellWords(t *testing.T, command string) []string {
	t.Helper()
	cmd := exec.Command("sh", "-c", `eval "set -- $CMD"; for a; do printf '%s\0' "$a"; done`)
	cmd.Env = append(os.Environ(), "CMD="+command)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("sh couldn't parse %s: %v", command, err)
	}
	return strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
}

func TestAsUser(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	command := Quote([]string{"/bin/echo", awkward})

	assert.Equal(t, AsUser("", command), command)
	assert.Equal(t, AsUser("root", command), command)

	words := shellWords(t, AsUser(awkward, command))
	assert.DeepEqual(t, words, []string{"su", "-c", command, awkward})
	assert.DeepEqual(t, shellWords(t, words[2]), []string{"/bin/echo", awkward})
}
