package tunnelserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/loft-sh/devpod/pkg/agent/tunnel"
	"github.com/loft-sh/devpod/pkg/log"
	provider2 "github.com/loft-sh/devpod/pkg/provider"
	"gotest.tools/assert"
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

// TestGitUser checks that the container gets the identity saved with the
// workspace, and the global one for what the workspace doesn't have.
func TestGitUser(t *testing.T) {
	home := t.TempDir()
	globalConfig := filepath.Join(home, ".gitconfig")
	err := os.WriteFile(globalConfig, []byte("[user]\n\tname = Global\n\temail = global@personal.com\n"), 0600)
	assert.NilError(t, err)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", globalConfig)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	testCases := []struct {
		name      string
		workspace *provider2.Workspace
		expected  string
	}{
		{
			name:      "saved identity",
			workspace: &provider2.Workspace{GitUser: provider2.WorkspaceGitUser{Name: "Work", Email: "me@company.com"}},
			expected:  `{"name":"Work","email":"me@company.com"}`,
		},
		{
			name:      "partly saved identity",
			workspace: &provider2.Workspace{GitUser: provider2.WorkspaceGitUser{Email: "me@company.com"}},
			expected:  `{"name":"Global","email":"me@company.com"}`,
		},
		{
			name:      "workspace without a saved identity",
			workspace: &provider2.Workspace{},
			expected:  `{"name":"Global","email":"global@personal.com"}`,
		},
		{
			name:     "no workspace",
			expected: `{"name":"Global","email":"global@personal.com"}`,
		},
	}

	for _, testCase := range testCases {
		s := New(log.Discard, WithWorkspace(testCase.workspace))
		response, err := s.GitUser(context.Background(), &tunnel.Empty{})
		assert.NilError(t, err, testCase.name)
		assert.Equal(t, response.Message, testCase.expected, testCase.name)
	}
}
