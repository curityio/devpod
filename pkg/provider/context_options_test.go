package provider

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/loft-sh/devpod/pkg/config"
	"gotest.tools/assert"
)

func TestApplyContextOptions(t *testing.T) {
	testCases := []struct {
		name            string
		options         map[string]string
		expectedEnabled string
		expectedNoAgent bool
	}{
		{name: "defaults", expectedEnabled: "false"},
		{name: "auto without agent forwarding", options: map[string]string{config.ContextOptionSSHAgentForwarding: "false"}, expectedEnabled: "true", expectedNoAgent: true},
		{name: "explicitly off", options: map[string]string{config.ContextOptionSSHAgentForwarding: "false", config.ContextOptionGitSSHToHTTPS: "false"}, expectedEnabled: "false", expectedNoAgent: true},
		{name: "explicitly on", options: map[string]string{config.ContextOptionGitSSHToHTTPS: "true"}, expectedEnabled: "true"},
	}

	for _, testCase := range testCases {
		options := &CLIOptions{}
		err := ApplyContextOptions(context.Background(), options, configWithOptions(testCase.options), WorkspaceSource{})
		assert.NilError(t, err, testCase.name)
		assert.Equal(t, options.GitSSHToHTTPS, testCase.expectedEnabled, testCase.name)
		assert.Equal(t, options.DisableSSHAgentForwarding, testCase.expectedNoAgent, testCase.name)
	}
}

func TestApplyContextOptionsRejectsInvalidHosts(t *testing.T) {
	err := ApplyContextOptions(context.Background(), &CLIOptions{}, configWithOptions(map[string]string{
		config.ContextOptionGitSSHToHTTPSHosts: "github.com,https://gitlab.com",
	}), WorkspaceSource{})
	assert.ErrorContains(t, err, `"https://gitlab.com"`)
}

// The git dir of a worktree is outside of the worktree, so the container only sees the remotes that the CLI reads.
func TestApplyContextOptionsReadsRemotesOfWorktreeOnHost(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	worktree := filepath.Join(dir, "worktree")
	runGit(t, dir, "init", "-q", repo)
	runGit(t, repo, "remote", "add", "origin", "git@github.com:curityio/devpod.git")
	runGit(t, repo, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", "init")
	runGit(t, repo, "worktree", "add", "-q", worktree)

	options := &CLIOptions{}
	err := ApplyContextOptions(context.Background(), options, configWithOptions(map[string]string{
		config.ContextOptionGitSSHToHTTPS: "true",
	}), WorkspaceSource{LocalFolder: worktree})
	assert.NilError(t, err)
	assert.DeepEqual(t, options.GitSSHToHTTPSRemotes, []string{"git@github.com:curityio/devpod.git"})
}

func configWithOptions(options map[string]string) *config.Config {
	values := map[string]config.OptionValue{}
	for name, value := range options {
		values[name] = config.OptionValue{Value: value}
	}
	return &config.Config{
		DefaultContext: config.DefaultContext,
		Contexts:       map[string]*config.ContextConfig{config.DefaultContext: {Options: values}},
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	emptyConfig := filepath.Join(t.TempDir(), "gitconfig")
	assert.NilError(t, os.WriteFile(emptyConfig, nil, 0644))
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+emptyConfig)
	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, string(out))
}
