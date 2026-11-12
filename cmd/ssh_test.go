package cmd

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"gotest.tools/assert"
)

// awkwardValue contains characters that the shell treats specially, in and out of quotes.
const awkwardValue = "o'brien \"quoted\" $HOME `id` \\ ; *"

// parseShellWords returns the words a POSIX shell splits command into, after quote removal, without running it.
func parseShellWords(t *testing.T, command string) []string {
	t.Helper()
	cmd := exec.Command("sh", "-c", `eval "set -- $CMD"; for a; do printf '%s\0' "$a"; done`)
	cmd.Env = append(os.Environ(), "CMD="+command)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("sh couldn't parse %s: %v", command, err)
	}
	return strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
}

// parseSuCommand checks that command is `su -c COMMAND user` and returns the words of COMMAND.
func parseSuCommand(t *testing.T, command, user string) []string {
	t.Helper()
	words := parseShellWords(t, command)
	if len(words) != 4 || words[0] != "su" || words[1] != "-c" || words[3] != user {
		t.Fatalf("expected su -c COMMAND %s, got %q", user, words)
	}
	return parseShellWords(t, words[2])
}

func TestSSHServerCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	workdir := "/workspaces/" + awkwardValue
	server := []string{"/usr/local/bin/devpod", "helper", "ssh-server", "--track-activity", "--stdio", "--workdir", workdir}

	assert.DeepEqual(t, parseShellWords(t, sshServerCommand("", workdir, "", false)), server)
	assert.DeepEqual(t, parseShellWords(t, sshServerCommand("root", workdir, "", false)), server)

	assert.DeepEqual(t,
		parseSuCommand(t, sshServerCommand(awkwardValue, workdir, "/tmp/"+awkwardValue, true), awkwardValue),
		append(server, "--reuse-ssh-auth-sock=/tmp/"+awkwardValue, "--debug"),
	)
}

func TestGPGSetupCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	setup := []string{
		"/usr/local/bin/devpod", "agent", "workspace", "setup-gpg",
		"--ownertrust", "b3duZXJ0cnVzdA==",
		"--socketpath", "/run/" + awkwardValue + "/S.gpg-agent.extra",
	}

	assert.DeepEqual(t,
		parseShellWords(t, gpgSetupCommand("root", "b3duZXJ0cnVzdA==", "/run/"+awkwardValue+"/S.gpg-agent.extra", "", false)),
		setup,
	)

	assert.DeepEqual(t,
		parseSuCommand(t, gpgSetupCommand(awkwardValue, "b3duZXJ0cnVzdA==", "/run/"+awkwardValue+"/S.gpg-agent.extra", awkwardValue, true), awkwardValue),
		append(setup, "--debug", "--gitkey", awkwardValue),
	)
}
