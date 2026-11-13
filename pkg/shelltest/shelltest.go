// Package shelltest helps tests check the commands DevPod builds for a POSIX shell.
package shelltest

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Awkward is a value with characters a shell treats specially: a space, quotes, expansions and a backslash.
const Awkward = "it's a \"$HOME\" `id` $(id) \\ path"

// Words returns the words sh splits command into, after quote removal and without running the command or any
// expansions in it.
func Words(t testing.TB, command string) []string {
	t.Helper()
	cmd := exec.Command("sh", "-c", `eval "set -- $CMD"; for a; do printf '%s\0' "$a"; done`)
	cmd.Env = append(os.Environ(), "CMD="+command)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("sh couldn't parse %s: %v", command, err)
	}
	return strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
}
