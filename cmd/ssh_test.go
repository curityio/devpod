package cmd

import (
	"runtime"
	"testing"

	"github.com/curityio/devpod/pkg/shelltest"
	"gotest.tools/assert"
)

// parseSuCommand checks that command is `su -c COMMAND user` and returns the words of COMMAND.
func parseSuCommand(t *testing.T, command, user string) []string {
	t.Helper()
	words := shelltest.Words(t, command)
	if len(words) != 4 || words[0] != "su" || words[1] != "-c" || words[3] != user {
		t.Fatalf("expected su -c COMMAND %s, got %q", user, words)
	}
	return shelltest.Words(t, words[2])
}

func TestSSHServerCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	workdir := "/workspaces/" + shelltest.Awkward
	server := []string{"/usr/local/bin/devpod", "helper", "ssh-server", "--track-activity", "--stdio", "--workdir", workdir}

	assert.DeepEqual(t, shelltest.Words(t, sshServerCommand("", workdir, "", false)), server)
	assert.DeepEqual(t, shelltest.Words(t, sshServerCommand("root", workdir, "", false)), server)

	assert.DeepEqual(t,
		parseSuCommand(t, sshServerCommand(shelltest.Awkward, workdir, "/tmp/"+shelltest.Awkward, true), shelltest.Awkward),
		append(server, "--reuse-ssh-auth-sock=/tmp/"+shelltest.Awkward, "--debug"),
	)
}

func TestGPGSetupCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	setup := []string{
		"/usr/local/bin/devpod", "agent", "workspace", "setup-gpg",
		"--ownertrust", "b3duZXJ0cnVzdA==",
		"--socketpath", "/run/" + shelltest.Awkward + "/S.gpg-agent.extra",
	}

	assert.DeepEqual(t,
		shelltest.Words(t, gpgSetupCommand("root", "b3duZXJ0cnVzdA==", "/run/"+shelltest.Awkward+"/S.gpg-agent.extra", "", false)),
		setup,
	)

	assert.DeepEqual(t,
		parseSuCommand(t, gpgSetupCommand(shelltest.Awkward, "b3duZXJ0cnVzdA==", "/run/"+shelltest.Awkward+"/S.gpg-agent.extra", shelltest.Awkward, true), shelltest.Awkward),
		append(setup, "--debug", "--gitkey", shelltest.Awkward),
	)
}
