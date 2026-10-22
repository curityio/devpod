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

	root, err := filepath.EvalSymlinks(t.TempDir())
	assert.NilError(t, err)
	globalConfig := filepath.Join(root, ".gitconfig")
	assert.NilError(t, os.WriteFile(globalConfig, []byte("[user]\n\tname = Me\n\temail = me@personal.com\n"), 0600))
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", globalConfig)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	// keep git from finding a repository the temporary directory is in
	t.Setenv("GIT_CEILING_DIRECTORIES", root)
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
	// git's ceiling only stops it above the temporary directory, so not the directory itself
	notRepo := filepath.Join(filepath.Dir(repo), "other")
	assert.NilError(t, os.Mkdir(notRepo, 0755))
	saved := providerpkg.WorkspaceGitUser{Name: "Saved", Email: "saved@company.com"}
	empty := ""
	devpodEmail := "me+devpod@company.com"
	givenName := "Given"

	testCases := []struct {
		name      string
		workspace providerpkg.Workspace
		options   GitUserOptions
		expected  providerpkg.WorkspaceGitUser
	}{
		{
			name:     "new workspace gets the identity of the repository devpod runs in",
			options:  GitUserOptions{Dir: repo},
			expected: providerpkg.WorkspaceGitUser{Name: "Me", Email: "me@company.com"},
		},
		{
			name:      "a local folder wins over the directory devpod runs in",
			workspace: providerpkg.Workspace{Source: providerpkg.WorkspaceSource{LocalFolder: repo}},
			options:   GitUserOptions{Dir: notRepo},
			expected:  providerpkg.WorkspaceGitUser{Name: "Me", Email: "me@company.com"},
		},
		{
			name:    "outside a repository nothing is saved",
			options: GitUserOptions{Dir: notRepo},
		},
		{
			name:    "without a directory nothing is saved",
			options: GitUserOptions{},
		},
		{
			name:      "the saved identity is kept",
			workspace: providerpkg.Workspace{GitUser: saved},
			options:   GitUserOptions{Dir: repo},
			expected:  saved,
		},
		{
			name:      "a given value replaces the saved one",
			workspace: providerpkg.Workspace{GitUser: saved},
			options:   GitUserOptions{Email: &devpodEmail, Dir: notRepo},
			expected:  providerpkg.WorkspaceGitUser{Name: "Saved", Email: devpodEmail},
		},
		{
			name:     "a value that isn't given is looked up",
			options:  GitUserOptions{Name: &givenName, Dir: repo},
			expected: providerpkg.WorkspaceGitUser{Name: givenName, Email: "me@company.com"},
		},
		{
			name:     "a given value is saved outside a repository",
			options:  GitUserOptions{Email: &devpodEmail, Dir: notRepo},
			expected: providerpkg.WorkspaceGitUser{Email: devpodEmail},
		},
		{
			name:      "an empty value removes the saved one, which is looked up again",
			workspace: providerpkg.Workspace{GitUser: saved},
			options:   GitUserOptions{Email: &empty, Dir: repo},
			expected:  providerpkg.WorkspaceGitUser{Name: "Saved", Email: "me@company.com"},
		},
		{
			name:      "an empty value outside a repository removes the saved one",
			workspace: providerpkg.Workspace{GitUser: saved},
			options:   GitUserOptions{Name: &empty, Email: &empty, Dir: notRepo},
		},
	}

	for i, testCase := range testCases {
		workspace := testCase.workspace
		workspace.ID = fmt.Sprintf("test-%d", i)
		workspace.Context = "default"
		assert.NilError(t, ConfigureGitUser(&workspace, testCase.options, log.Discard), testCase.name)
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
