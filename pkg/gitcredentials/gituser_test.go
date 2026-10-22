package gitcredentials

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"gotest.tools/assert"
)

// setupIncludeIfConfig gives git a global config with a personal identity and an includeIf that
// gives repositories under the returned work directory a work email, as in the bug report.
func setupIncludeIfConfig(t *testing.T) (workDir string) {
	t.Helper()

	// resolve symlinks (such as macOS's /var) so the gitdir pattern matches
	root, err := filepath.EvalSymlinks(t.TempDir())
	assert.NilError(t, err)
	home := filepath.Join(root, "home")
	workDir = filepath.Join(home, "work")
	assert.NilError(t, os.MkdirAll(workDir, 0755))

	// gitdir/i because Windows paths are case insensitive
	globalConfig := filepath.Join(home, ".gitconfig")
	writeFile(t, globalConfig, `[user]
	name = Me
	email = me@personal.com
[includeIf "gitdir/i:`+configPath(workDir)+`/"]
	path = `+configPath(filepath.Join(workDir, ".gitconfig"))+"\n")
	writeFile(t, filepath.Join(workDir, ".gitconfig"), "[user]\n\temail = me@company.com\n")

	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", globalConfig)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	return workDir
}

// configPath writes path for a git config file: git takes a backslash as an escape character, and
// gitdir patterns use forward slashes on Windows too.
func configPath(path string) string {
	return filepath.ToSlash(path)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	assert.NilError(t, os.WriteFile(path, []byte(content), 0600))
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	out, err := exec.Command("git", "init", "--quiet", dir).CombinedOutput()
	assert.NilError(t, err, string(out))
}

func TestGetHostUser_FollowsIncludeIfOfRepository(t *testing.T) {
	repo := filepath.Join(setupIncludeIfConfig(t), "repo")
	gitInit(t, repo)

	assert.DeepEqual(t, GetHostUser(repo), &GitUser{Name: "Me", Email: "me@company.com"})
	// a subdirectory of the repository is in it too
	subDir := filepath.Join(repo, "sub")
	assert.NilError(t, os.Mkdir(subDir, 0755))
	assert.DeepEqual(t, GetHostUser(subDir), &GitUser{Name: "Me", Email: "me@company.com"})
}

func TestGetHostUser_UsesRepositoryConfig(t *testing.T) {
	repo := filepath.Join(setupIncludeIfConfig(t), "..", "other")
	gitInit(t, repo)
	out, err := exec.Command("git", "-C", repo, "config", "user.email", "me@repo.com").CombinedOutput()
	assert.NilError(t, err, string(out))

	assert.DeepEqual(t, GetHostUser(repo), &GitUser{Name: "Me", Email: "me@repo.com"})
}

func TestGetHostUser_OutsideRepositoryUsesGlobalConfig(t *testing.T) {
	workDir := setupIncludeIfConfig(t)

	// the includeIf only matches repositories
	assert.DeepEqual(t, GetHostUser(workDir), &GitUser{Name: "Me", Email: "me@personal.com"})
	assert.DeepEqual(t, GetHostUser(filepath.Join(workDir, "missing")), &GitUser{Name: "Me", Email: "me@personal.com"})
	assert.DeepEqual(t, GetHostUser(""), &GitUser{Name: "Me", Email: "me@personal.com"})
}

func TestGetHostUser_GlobalFollowsIncludes(t *testing.T) {
	workDir := setupIncludeIfConfig(t)
	writeFile(t, os.Getenv("GIT_CONFIG_GLOBAL"), "[include]\n\tpath = "+configPath(filepath.Join(workDir, ".gitconfig"))+"\n")

	assert.DeepEqual(t, GetHostUser(""), &GitUser{Email: "me@company.com"})
}
