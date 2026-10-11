package tunnelserver

import (
	"context"
	"testing"

	"github.com/loft-sh/devpod/pkg/agent/tunnel"
	"github.com/loft-sh/log"
)

// TestGitCredentials_NoWorkspace checks that a git credentials request to a
// server without a workspace, such as the setup server before it was given
// one, does not dereference the nil workspace.
func TestGitCredentials_NoWorkspace(t *testing.T) {
	// Keep the host's git configuration and credential helpers out of the
	// `git credential fill` the handler falls back to.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	t.Setenv("GIT_ASKPASS", "")
	t.Setenv("SSH_ASKPASS", "")
	t.Setenv("DEVPOD_GIT_HELPER_PORT", "")

	s := New(log.Discard, WithAllowGitCredentials(true))
	request := &tunnel.Message{Message: `{"protocol":"https","host":"github.com","path":"org/repo.git"}`}

	// With no helper and no prompt git cannot answer, so an error is fine;
	// a panic is not.
	_, _ = s.GitCredentials(context.Background(), request)
}
