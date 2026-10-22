package gitcredentials

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	// keep git from finding a repository the temporary directory is in
	t.Setenv("GIT_CEILING_DIRECTORIES", root)

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

func TestGetRepositoryUser_FollowsIncludeIf(t *testing.T) {
	repo := filepath.Join(setupIncludeIfConfig(t), "repo")
	gitInit(t, repo)

	gitUser, err := GetRepositoryUser(repo)
	assert.NilError(t, err)
	assert.DeepEqual(t, gitUser, &GitUser{Name: "Me", Email: "me@company.com"})

	// a subdirectory of the repository is in it too
	subDir := filepath.Join(repo, "sub")
	assert.NilError(t, os.Mkdir(subDir, 0755))
	gitUser, err = GetRepositoryUser(subDir)
	assert.NilError(t, err)
	assert.DeepEqual(t, gitUser, &GitUser{Name: "Me", Email: "me@company.com"})
}

func TestGetRepositoryUser_UsesRepositoryConfig(t *testing.T) {
	repo := filepath.Join(setupIncludeIfConfig(t), "..", "other")
	gitInit(t, repo)
	out, err := exec.Command("git", "-C", repo, "config", "user.email", "me@repo.com").CombinedOutput()
	assert.NilError(t, err, string(out))

	gitUser, err := GetRepositoryUser(repo)
	assert.NilError(t, err)
	assert.DeepEqual(t, gitUser, &GitUser{Name: "Me", Email: "me@repo.com"})
}

func TestGetRepositoryUser_FailsOutsideRepository(t *testing.T) {
	workDir := setupIncludeIfConfig(t)

	for _, dir := range []string{workDir, filepath.Join(workDir, "missing"), ""} {
		_, err := GetRepositoryUser(dir)
		assert.Assert(t, err != nil, dir)
	}
}

func TestGetRepositoryUser_FailsForUntrustedRepository(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Git for Windows checks the owner of a repository differently")
	}

	repo := filepath.Join(setupIncludeIfConfig(t), "repo")
	gitInit(t, repo)
	// git then takes the repository to belong to another user
	t.Setenv("GIT_TEST_ASSUME_DIFFERENT_OWNER", "1")

	_, err := GetRepositoryUser(repo)
	assert.ErrorContains(t, err, "dubious ownership")
}

func TestGetGlobalUser(t *testing.T) {
	workDir := setupIncludeIfConfig(t)
	assert.DeepEqual(t, GetGlobalUser(), &GitUser{Name: "Me", Email: "me@personal.com"})

	writeFile(t, os.Getenv("GIT_CONFIG_GLOBAL"), "[include]\n\tpath = "+configPath(filepath.Join(workDir, ".gitconfig"))+"\n")
	assert.DeepEqual(t, GetGlobalUser(), &GitUser{Email: "me@company.com"})
}

func TestSetUser_TakesValuesLiterally(t *testing.T) {
	setupIncludeIfConfig(t)

	// SetUser and GetUser use $XDG_CONFIG_HOME/git/config, whose directory doesn't exist yet
	gitUser := &GitUser{Name: `Ann "Nan' $HOME`, Email: "`me`@company.com"}
	assert.NilError(t, SetUser("", gitUser))

	saved, err := GetUser("")
	assert.NilError(t, err)
	assert.DeepEqual(t, saved, gitUser)
}
