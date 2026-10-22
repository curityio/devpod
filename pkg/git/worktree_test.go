package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gotest.tools/assert"
	"gotest.tools/assert/cmp"
)

// isolateGit makes the git commands of the code under test ignore the user's
// and the system's git config.
func isolateGit(t *testing.T) {
	t.Helper()
	requireGit(t)
	emptyConfig := filepath.Join(t.TempDir(), "gitconfig")
	assert.NilError(t, os.WriteFile(emptyConfig, nil, 0644))
	t.Setenv("GIT_CONFIG_GLOBAL", emptyConfig)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, string(out))
	return strings.TrimSpace(string(out))
}

func writeRepoFiles(t *testing.T, root string, files ...string) {
	t.Helper()
	for _, file := range files {
		p := filepath.Join(root, filepath.FromSlash(file))
		assert.NilError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		assert.NilError(t, os.WriteFile(p, []byte(file+"\n"), 0o644))
	}
}

func commitAll(t *testing.T, repo, message string) {
	t.Helper()
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "-m", message)
}

// newRepository creates a repository with a commit of README.md on branch main.
func newRepository(t *testing.T, repo string) {
	t.Helper()
	runGit(t, filepath.Dir(repo), "init", "-q", "-b", "main", repo)
	writeRepoFiles(t, repo, "README.md")
	commitAll(t, repo, "init")
}

func TestListFiles(t *testing.T) {
	isolateGit(t)
	ctx := context.Background()
	dir := t.TempDir()

	lib := filepath.Join(dir, "lib")
	newRepository(t, lib)

	repo := filepath.Join(dir, "repo")
	newRepository(t, repo)
	assert.NilError(t, os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("node_modules/\n*.log\n"), 0o644))
	writeRepoFiles(t, repo, "src/main.go", "forced.log")
	runGit(t, repo, "add", "-f", "forced.log")
	runGit(t, repo, "-c", "protocol.file.allow=always", "submodule", "add", "-q", lib, "modules/lib")
	commitAll(t, repo, "files")

	// ignored and untracked files, a nested repository and a worktree
	writeRepoFiles(t, repo, "app.log", "node_modules/x/index.js", "new.txt", "secret.txt")
	assert.NilError(t, os.WriteFile(filepath.Join(repo, ".git", "info", "exclude"), []byte("secret.txt\n"), 0o644))
	newRepository(t, filepath.Join(repo, "nested"))
	runGit(t, repo, "worktree", "add", "-q", filepath.Join(repo, "worktree"))

	files, err := ListFiles(ctx, repo)
	assert.NilError(t, err)
	sort.Strings(files)
	assert.DeepEqual(t, []string{
		".gitignore",
		".gitmodules",
		"README.md",
		"forced.log",
		"modules/lib/README.md",
		"new.txt",
		"src/main.go",
	}, files)

	files, err = ListFiles(ctx, filepath.Join(repo, "src"))
	assert.NilError(t, err)
	assert.DeepEqual(t, []string{"main.go"}, files)
}

func TestListFilesOfSubmoduleThatIsNotCheckedOut(t *testing.T) {
	isolateGit(t)
	dir := t.TempDir()
	lib := filepath.Join(dir, "lib")
	newRepository(t, lib)
	repo := filepath.Join(dir, "repo")
	newRepository(t, repo)
	runGit(t, repo, "-c", "protocol.file.allow=always", "submodule", "add", "-q", lib, "lib")
	commitAll(t, repo, "submodule")
	runGit(t, repo, "submodule", "deinit", "-q", "lib")

	files, err := ListFiles(context.Background(), repo)
	assert.NilError(t, err)
	sort.Strings(files)
	assert.DeepEqual(t, []string{".gitmodules", "README.md", "lib"}, files)
}

func TestListFilesWithoutRepository(t *testing.T) {
	isolateGit(t)
	_, err := ListFiles(context.Background(), t.TempDir())
	assert.ErrorContains(t, err, "ls-files")
}

func TestIsTopLevel(t *testing.T) {
	isolateGit(t)
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	newRepository(t, repo)
	writeRepoFiles(t, repo, "src/main.go")

	assert.Assert(t, IsTopLevel(ctx, repo))
	assert.Assert(t, !IsTopLevel(ctx, filepath.Join(repo, "src")))
	assert.Assert(t, !IsTopLevel(ctx, t.TempDir()))
}

// copyWorkTree copies the files git lists in repo to target.
func copyWorkTree(t *testing.T, repo, target string) {
	t.Helper()
	files, err := ListFiles(context.Background(), repo)
	assert.NilError(t, err)
	for _, file := range files {
		content, err := os.ReadFile(filepath.Join(repo, file))
		assert.NilError(t, err)
		p := filepath.Join(target, file)
		assert.NilError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		assert.NilError(t, os.WriteFile(p, content, 0o644))
	}
}

func TestSnapshot(t *testing.T) {
	isolateGit(t)
	ctx := context.Background()
	dir := t.TempDir()

	// repo is a commit ahead of and a commit behind origin/main
	origin := filepath.Join(dir, "origin.git")
	runGit(t, dir, "init", "-q", "--bare", "-b", "main", origin)
	other := filepath.Join(dir, "other")
	newRepository(t, other)
	writeRepoFiles(t, other, "history.txt")
	commitAll(t, other, "history")
	runGit(t, other, "push", "-q", origin, "main")
	repo := filepath.Join(dir, "repo")
	runGit(t, dir, "clone", "-q", origin, repo)
	runGit(t, repo, "config", "user.email", "repo@example.com")
	writeRepoFiles(t, repo, "local.txt")
	commitAll(t, repo, "local")
	writeRepoFiles(t, other, "remote.txt")
	commitAll(t, other, "remote")
	runGit(t, other, "push", "-q", origin, "main")
	runGit(t, repo, "fetch", "-q")
	// uncommitted changes
	writeRepoFiles(t, repo, "new.txt")
	assert.NilError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("changed\n"), 0o644))
	assert.NilError(t, os.WriteFile(filepath.Join(repo, ".git", "info", "exclude"), []byte("*.tmp\n"), 0o644))

	target := t.TempDir()
	branch, err := Snapshot(ctx, repo, target)
	assert.NilError(t, err)
	assert.Equal(t, "main", branch)
	copyWorkTree(t, repo, target)
	writeRepoFiles(t, target, "excluded.tmp")

	assert.Equal(t, "refs/heads/main", gitOutput(t, target, "symbolic-ref", "HEAD"))
	assert.Equal(t, gitOutput(t, repo, "rev-parse", "HEAD"), gitOutput(t, target, "rev-parse", "HEAD"))
	assert.Equal(t, "true", gitOutput(t, target, "rev-parse", "--is-shallow-repository"))
	// the local commit and the one it diverged from origin/main
	assert.Equal(t, "2", gitOutput(t, target, "rev-list", "--count", "HEAD"))
	assert.Equal(t, origin, gitOutput(t, target, "remote", "get-url", "origin"))
	// DevPod gives the workspace the repository's identity in the global git config, which a local one would override
	cmd := exec.Command("git", "config", "--local", "user.email")
	cmd.Dir = target
	assert.Assert(t, cmd.Run() != nil, "the snapshot has a user")
	assert.Equal(t, "origin/main", gitOutput(t, target, "rev-parse", "--abbrev-ref", "@{upstream}"))
	status := gitOutput(t, target, "status", "--porcelain=v2", "--branch")
	assert.Assert(t, cmp.Contains(status, "# branch.ab +1 -1"))
	assert.Assert(t, cmp.Contains(status, "1 .M N... 100644 100644 100644"))
	assert.Assert(t, cmp.Contains(status, "README.md"))
	assert.Assert(t, cmp.Contains(status, "? new.txt"))
	assert.Assert(t, !strings.Contains(status, "excluded.tmp"), status)
	assert.Assert(t, !strings.Contains(status, "local.txt"), status)
	_, err = os.Stat(filepath.Join(target, ".git", "hooks"))
	assert.Assert(t, os.IsNotExist(err))
	config, err := os.ReadFile(filepath.Join(target, ".git", "config"))
	assert.NilError(t, err)
	assert.Assert(t, !strings.Contains(string(config), "ignorecase"), string(config))

	// the snapshot can be committed and pushed
	commitAll(t, target, "from snapshot")
	runGit(t, target, "push", "-q", "origin", "main:snapshot")
	assert.Equal(t, gitOutput(t, target, "rev-parse", "HEAD"), gitOutput(t, origin, "rev-parse", "snapshot"))
}

func TestSnapshotOfDetachedHead(t *testing.T) {
	isolateGit(t)
	repo := filepath.Join(t.TempDir(), "repo")
	newRepository(t, repo)
	runGit(t, repo, "checkout", "-q", "--detach")

	target := t.TempDir()
	branch, err := Snapshot(context.Background(), repo, target)
	assert.NilError(t, err)
	assert.Equal(t, "", branch)
	assert.Equal(t, gitOutput(t, repo, "rev-parse", "HEAD"), gitOutput(t, target, "rev-parse", "HEAD"))
	cmd := exec.Command("git", "symbolic-ref", "-q", "HEAD")
	cmd.Dir = target
	assert.Assert(t, cmd.Run() != nil, "HEAD is not detached")
}

func TestSnapshotOfWorktree(t *testing.T) {
	isolateGit(t)
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	newRepository(t, repo)
	worktree := filepath.Join(dir, "worktree")
	runGit(t, repo, "worktree", "add", "-q", "-b", "feature", worktree)
	writeRepoFiles(t, worktree, "feature.txt")
	commitAll(t, worktree, "feature")

	target := t.TempDir()
	branch, err := Snapshot(context.Background(), worktree, target)
	assert.NilError(t, err)
	assert.Equal(t, "feature", branch)
	assert.Equal(t, gitOutput(t, worktree, "rev-parse", "HEAD"), gitOutput(t, target, "rev-parse", "HEAD"))
	assert.Equal(t, "1", gitOutput(t, target, "rev-list", "--count", "HEAD"))
}

func TestSnapshotWithoutCommits(t *testing.T) {
	isolateGit(t)
	repo := filepath.Join(t.TempDir(), "repo")
	runGit(t, filepath.Dir(repo), "init", "-q", "-b", "trunk", repo)

	target := t.TempDir()
	branch, err := Snapshot(context.Background(), repo, target)
	assert.NilError(t, err)
	assert.Equal(t, "trunk", branch)
	assert.Equal(t, "refs/heads/trunk", gitOutput(t, target, "symbolic-ref", "HEAD"))
}
