package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gotest.tools/assert"
)

func TestSSHPrefix(t *testing.T) {
	testCases := []struct {
		in             string
		expectedPrefix string
		expectedHost   string
		expectedOK     bool
	}{
		{in: "git@github.com:curityio/devpod.git", expectedPrefix: "git@github.com:", expectedHost: "github.com", expectedOK: true},
		{in: "github.com:curityio/devpod.git", expectedPrefix: "github.com:", expectedHost: "github.com", expectedOK: true},
		{in: "ssh://git@github.com/curityio/devpod.git", expectedPrefix: "ssh://git@github.com/", expectedHost: "github.com", expectedOK: true},
		{in: "ssh://github.com/curityio/devpod.git", expectedPrefix: "ssh://github.com/", expectedHost: "github.com", expectedOK: true},
		{in: "ssh://git@git.example.com:7999/proj/repo.git", expectedPrefix: "ssh://git@git.example.com:7999/", expectedHost: "git.example.com", expectedOK: true},
		{in: "git+ssh://git@bitbucket.org/team/repo.git", expectedPrefix: "git+ssh://git@bitbucket.org/", expectedHost: "bitbucket.org", expectedOK: true},
		{in: "https://github.com/curityio/devpod.git"},
		{in: "http://github.com/curityio/devpod.git"},
		{in: "file:///tmp/repo"},
		{in: "/tmp/repo"},
		{in: "../repo"},
		{in: "C:/repo"},
		{in: "ssh://git@github.com"},
	}

	for _, testCase := range testCases {
		prefix, host, ok := sshPrefix(testCase.in)
		assert.Equal(t, ok, testCase.expectedOK, testCase.in)
		assert.Equal(t, prefix, testCase.expectedPrefix, testCase.in)
		assert.Equal(t, host, testCase.expectedHost, testCase.in)
	}
}

func TestSSHToHTTPSRewrites(t *testing.T) {
	rewrites := SSHToHTTPSRewrites(
		[]string{
			"git@github.com:curityio/devpod.git",
			"https://bitbucket.org/team/repo.git",
			"ssh://git@git.example.com:7999/proj/repo.git",
		},
		[]string{
			"bitbucket.org",
			"ssh://git@git.example.com:7999/=https://git.example.com/scm/",
		},
	)

	assert.DeepEqual(t, rewrites, map[string]string{
		"git@github.com:":                 "https://github.com/",
		"ssh://git@github.com/":           "https://github.com/",
		"git@bitbucket.org:":              "https://bitbucket.org/",
		"ssh://git@bitbucket.org/":        "https://bitbucket.org/",
		"git@git.example.com:":            "https://git.example.com/",
		"ssh://git@git.example.com/":      "https://git.example.com/",
		"ssh://git@git.example.com:7999/": "https://git.example.com/scm/",
	})
}

func TestFormatSSHToHTTPSConfig(t *testing.T) {
	config := FormatSSHToHTTPSConfig(map[string]string{
		"ssh://git@github.com/": "https://github.com/",
		"git@github.com:":       "https://github.com/",
		"git@bitbucket.org:":    "https://bitbucket.org/",
	})

	assert.Equal(t, config, `# Written by DevPod because GIT_SSH_TO_HTTPS is enabled. Do not edit.
[url "https://bitbucket.org/"]
	insteadOf = git@bitbucket.org:
[url "https://github.com/"]
	insteadOf = git@github.com:
	insteadOf = ssh://git@github.com/
`)
}

func TestSSHToHTTPSAppliedByGit(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	runGit(t, dir, "init", "-q", repo)
	runGit(t, repo, "remote", "add", "origin", "git@github.com:curityio/devpod.git")
	runGit(t, repo, "remote", "add", "server", "ssh://git@git.example.com:7999/proj/repo.git")
	runGit(t, repo, "remote", "add", "web", "https://bitbucket.org/team/repo.git")
	err := os.WriteFile(filepath.Join(repo, ".gitmodules"), []byte(`[submodule "lib"]
	path = lib
	url = git@bitbucket.org:team/lib.git
`), 0644)
	assert.NilError(t, err)

	remoteURLs := RemoteURLs(ctx, repo)
	assert.DeepEqual(t, remoteURLs, []string{
		"git@github.com:curityio/devpod.git",
		"ssh://git@git.example.com:7999/proj/repo.git",
		"https://bitbucket.org/team/repo.git",
		"git@bitbucket.org:team/lib.git",
	})

	rewrites := SSHToHTTPSRewrites(remoteURLs, []string{"ssh://git@git.example.com:7999/=https://git.example.com/scm/"})
	globalConfig := filepath.Join(dir, "gitconfig")
	err = os.WriteFile(globalConfig, []byte(FormatSSHToHTTPSConfig(rewrites)), 0644)
	assert.NilError(t, err)

	for remote, expected := range map[string]string{
		"origin": "https://github.com/curityio/devpod.git",
		"server": "https://git.example.com/scm/proj/repo.git",
		"web":    "https://bitbucket.org/team/repo.git",
	} {
		cmd := exec.Command("git", "remote", "get-url", "--push", remote)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+globalConfig, "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.Output()
		assert.NilError(t, err)
		assert.Equal(t, strings.TrimSpace(string(out)), expected, remote)
	}
}

func TestRemoteURLsOfWorktree(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	runGit(t, dir, "init", "-q", repo)
	runGit(t, repo, "remote", "add", "origin", "git@github.com:curityio/devpod.git")
	runGit(t, repo, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "--allow-empty", "-m", "init")
	worktree := filepath.Join(dir, "worktree")
	runGit(t, repo, "worktree", "add", "-q", worktree)

	assert.DeepEqual(t, RemoteURLs(context.Background(), worktree), []string{"git@github.com:curityio/devpod.git"})
}

func TestRemoteURLsWithoutRepository(t *testing.T) {
	requireGit(t)
	assert.DeepEqual(t, RemoteURLs(context.Background(), t.TempDir()), []string{})
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	emptyConfig := filepath.Join(t.TempDir(), "gitconfig")
	assert.NilError(t, os.WriteFile(emptyConfig, nil, 0644))
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+emptyConfig)
	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, string(out))
}
