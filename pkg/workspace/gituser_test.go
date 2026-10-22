package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/loft-sh/devpod/pkg/config"
	"github.com/loft-sh/devpod/pkg/log"
	providerpkg "github.com/loft-sh/devpod/pkg/provider"
	"gotest.tools/assert"
)

// setupGitConfig gives git a global identity and returns a repository with its own email.
func setupGitConfig(t *testing.T) (repo string) {
	t.Helper()

	root := t.TempDir()
	globalConfig := filepath.Join(root, ".gitconfig")
	assert.NilError(t, os.WriteFile(globalConfig, []byte("[user]\n\tname = Me\n\temail = me@personal.com\n"), 0600))
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", globalConfig)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv(config.DEVPOD_HOME, filepath.Join(root, ".devpod"))

	repo = filepath.Join(root, "repo")
	out, err := exec.Command("git", "init", "--quiet", repo).CombinedOutput()
	assert.NilError(t, err, string(out))
	out, err = exec.Command("git", "-C", repo, "config", "user.email", "me@company.com").CombinedOutput()
	assert.NilError(t, err, string(out))

	return repo
}

func TestConfigureGitUser(t *testing.T) {
	repo := setupGitConfig(t)

	testCases := []struct {
		name      string
		workspace providerpkg.Workspace
		given     providerpkg.WorkspaceGitUser
		dir       string
		expected  providerpkg.WorkspaceGitUser
	}{
		{
			name:     "new workspace gets the identity of the directory devpod runs in",
			dir:      repo,
			expected: providerpkg.WorkspaceGitUser{Name: "Me", Email: "me@company.com"},
		},
		{
			name:      "a local folder wins over the directory devpod runs in",
			workspace: providerpkg.Workspace{Source: providerpkg.WorkspaceSource{LocalFolder: repo}},
			expected:  providerpkg.WorkspaceGitUser{Name: "Me", Email: "me@company.com"},
		},
		{
			name:     "outside a repository the global identity is used",
			dir:      t.TempDir(),
			expected: providerpkg.WorkspaceGitUser{Name: "Me", Email: "me@personal.com"},
		},
		{
			name:      "the saved identity is kept",
			workspace: providerpkg.Workspace{GitUser: providerpkg.WorkspaceGitUser{Name: "Saved", Email: "saved@company.com"}},
			dir:       repo,
			expected:  providerpkg.WorkspaceGitUser{Name: "Saved", Email: "saved@company.com"},
		},
		{
			name:      "the given identity replaces the saved one",
			workspace: providerpkg.Workspace{GitUser: providerpkg.WorkspaceGitUser{Name: "Saved", Email: "saved@company.com"}},
			given:     providerpkg.WorkspaceGitUser{Email: "me+devpod@company.com"},
			dir:       repo,
			expected:  providerpkg.WorkspaceGitUser{Name: "Saved", Email: "me+devpod@company.com"},
		},
		{
			name:     "a value that isn't given is resolved",
			given:    providerpkg.WorkspaceGitUser{Name: "Given"},
			dir:      repo,
			expected: providerpkg.WorkspaceGitUser{Name: "Given", Email: "me@company.com"},
		},
	}

	for i, testCase := range testCases {
		workspace := testCase.workspace
		workspace.ID = fmt.Sprintf("test-%d", i)
		workspace.Context = "default"
		assert.NilError(t, ConfigureGitUser(&workspace, testCase.given, testCase.dir, log.Discard), testCase.name)
		assert.Equal(t, workspace.GitUser, testCase.expected, testCase.name)

		saved, err := providerpkg.LoadWorkspaceConfig(workspace.Context, workspace.ID)
		if testCase.expected == testCase.workspace.GitUser {
			// nothing changed, so nothing was saved
			assert.Assert(t, err != nil, testCase.name)
		} else {
			assert.NilError(t, err, testCase.name)
			assert.Equal(t, saved.GitUser, testCase.expected, testCase.name)
		}
	}
}
