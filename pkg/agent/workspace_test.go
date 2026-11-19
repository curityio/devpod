package agent

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/curityio/devpod/pkg/log"
	provider2 "github.com/curityio/devpod/pkg/provider"
	"github.com/sirupsen/logrus"
	"gotest.tools/assert"
	"gotest.tools/assert/cmp"
)

func testGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=test", "-c", "user.email=test@example.com"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, string(out))
	return strings.TrimSpace(string(out))
}

func writeTestFile(t *testing.T, root, file, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(file))
	assert.NilError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	assert.NilError(t, os.WriteFile(p, []byte(content), 0o644))
}

// newRemoteAndCheckout creates a repository with the files on branch main, and a clone of it.
func newRemoteAndCheckout(t *testing.T, files map[string]string) (remote, checkout string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	emptyConfig := filepath.Join(t.TempDir(), "gitconfig")
	assert.NilError(t, os.WriteFile(emptyConfig, nil, 0o644))
	t.Setenv("GIT_CONFIG_GLOBAL", emptyConfig)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	root := t.TempDir()
	remote = filepath.Join(root, "remote")
	checkout = filepath.Join(root, "checkout")
	testGit(t, root, "init", "-q", "-b", "main", remote)
	writeTestFile(t, remote, "README.md", "readme\n")
	for file, content := range files {
		writeTestFile(t, remote, file, content)
	}
	testGit(t, remote, "add", "-A")
	testGit(t, remote, "commit", "-q", "-m", "init")
	testGit(t, root, "clone", "-q", remote, checkout)
	return remote, checkout
}

func commitTestFile(t *testing.T, repo, file, content string) {
	t.Helper()
	writeTestFile(t, repo, file, content)
	testGit(t, repo, "add", "-A")
	testGit(t, repo, "commit", "-q", "-m", "change "+file)
}

func updateRepository(ctx context.Context, source *provider2.WorkspaceSource, checkout string) string {
	logs := &bytes.Buffer{}
	logger := log.NewStreamLogger(logs, logs, logrus.DebugLevel)
	UpdateRepositoryForWorkspace(ctx, source, checkout, "", provider2.CLIOptions{}, logger)
	return logs.String()
}

func TestUpdateRepositoryForWorkspace(t *testing.T) {
	remote, checkout := newRemoteAndCheckout(t, nil)
	commitTestFile(t, remote, ".devcontainer/devcontainer.json", "{}\n")

	logs := updateRepository(context.Background(), &provider2.WorkspaceSource{GitRepository: remote}, checkout)

	assert.Assert(t, cmp.Contains(logs, "Updated the workspace's checkout from commit"))
	assert.Equal(t, testGit(t, checkout, "rev-parse", "HEAD"), testGit(t, remote, "rev-parse", "HEAD"))
}

func TestUpdateRepositoryForWorkspaceKeepsChanges(t *testing.T) {
	remote, checkout := newRemoteAndCheckout(t, nil)
	before := testGit(t, checkout, "rev-parse", "HEAD")
	commitTestFile(t, remote, ".devcontainer/devcontainer.json", "{}\n")
	writeTestFile(t, checkout, "README.md", "my work\n")

	logs := updateRepository(context.Background(), &provider2.WorkspaceSource{GitRepository: remote}, checkout)

	assert.Assert(t, cmp.Contains(logs, "was not updated because it has changes that are not committed, e.g. to README.md"))
	assert.Assert(t, cmp.Contains(logs, "--reset"))
	assert.Equal(t, testGit(t, checkout, "rev-parse", "HEAD"), before)
	content, err := os.ReadFile(filepath.Join(checkout, "README.md"))
	assert.NilError(t, err)
	assert.Equal(t, string(content), "my work\n")
}

func TestUpdateRepositoryForWorkspaceKeepsPinnedCommit(t *testing.T) {
	remote, checkout := newRemoteAndCheckout(t, nil)
	before := testGit(t, checkout, "rev-parse", "HEAD")
	commitTestFile(t, remote, "new.txt", "new\n")

	logs := updateRepository(context.Background(), &provider2.WorkspaceSource{GitRepository: remote, GitCommit: before}, checkout)

	assert.Assert(t, cmp.Contains(logs, "keeping its checkout"))
	assert.Equal(t, testGit(t, checkout, "rev-parse", "HEAD"), before)
}

func TestUpdateRepositoryForWorkspaceWithoutRemote(t *testing.T) {
	remote, checkout := newRemoteAndCheckout(t, nil)
	before := testGit(t, checkout, "rev-parse", "HEAD")
	assert.NilError(t, os.RemoveAll(remote))

	logs := updateRepository(context.Background(), &provider2.WorkspaceSource{GitRepository: remote}, checkout)

	assert.Assert(t, cmp.Contains(logs, "The workspace's checkout was not updated: git fetch"))
	assert.Assert(t, cmp.Contains(logs, "recreated with the files it has"))
	assert.Equal(t, testGit(t, checkout, "rev-parse", "HEAD"), before)
}

func TestUpdateRepositoryForWorkspaceIgnoredFiles(t *testing.T) {
	remote, checkout := newRemoteAndCheckout(t, map[string]string{
		".devpodignore": "data\n",
		"data/big.bin":  "old\n",
		"other/big.bin": "old\n",
	})
	// what cloning the repository leaves
	removeIgnoredFiles(checkout, log.Discard)
	_, err := os.Stat(filepath.Join(checkout, "data"))
	assert.Assert(t, os.IsNotExist(err))

	// the remote branch changes an ignored file, and ignores another folder
	writeTestFile(t, remote, "data/big.bin", "new\n")
	commitTestFile(t, remote, ".devpodignore", "data\nother\n")

	logs := updateRepository(context.Background(), &provider2.WorkspaceSource{GitRepository: remote}, checkout)

	assert.Assert(t, cmp.Contains(logs, "Updated the workspace's checkout from commit"))
	assert.Equal(t, testGit(t, checkout, "rev-parse", "HEAD"), testGit(t, remote, "rev-parse", "HEAD"))
	// the ignored files don't come back
	_, err = os.Stat(filepath.Join(checkout, "data"))
	assert.Assert(t, os.IsNotExist(err))
	// only cloning removes files: the workspace may use those of a folder that is ignored later
	_, err = os.Stat(filepath.Join(checkout, "other", "big.bin"))
	assert.NilError(t, err)
}

func TestUpdateRepositoryForWorkspaceKeepsFilesInIgnoredPaths(t *testing.T) {
	remote, checkout := newRemoteAndCheckout(t, map[string]string{
		".devpodignore": "data\nnode_modules\n",
		"data/big.bin":  "old\n",
	})
	removeIgnoredFiles(checkout, log.Discard)
	// what the user put there since
	writeTestFile(t, checkout, "data/notes.txt", "mine\n")
	writeTestFile(t, checkout, "node_modules/lib/index.js", "installed\n")

	// whether there is nothing to update
	logs := updateRepository(context.Background(), &provider2.WorkspaceSource{GitRepository: remote}, checkout)
	assert.Assert(t, cmp.Contains(logs, "up to date"))
	// or something
	commitTestFile(t, remote, "data/big.bin", "new\n")
	logs = updateRepository(context.Background(), &provider2.WorkspaceSource{GitRepository: remote}, checkout)
	assert.Assert(t, cmp.Contains(logs, "Updated the workspace's checkout from commit"))

	for file, expected := range map[string]string{
		"data/notes.txt":            "mine\n",
		"node_modules/lib/index.js": "installed\n",
	} {
		content, err := os.ReadFile(filepath.Join(checkout, filepath.FromSlash(file)))
		assert.NilError(t, err)
		assert.Equal(t, string(content), expected)
	}
	_, err := os.Stat(filepath.Join(checkout, "data", "big.bin"))
	assert.Assert(t, os.IsNotExist(err))
}

func TestUpdateRepositoryForWorkspaceOfTag(t *testing.T) {
	remote, checkout := newRemoteAndCheckout(t, nil)
	testGit(t, checkout, "checkout", "-q", "--detach")
	commitTestFile(t, remote, "new.txt", "new\n")

	logs := updateRepository(context.Background(), &provider2.WorkspaceSource{GitRepository: remote}, checkout)

	assert.Assert(t, cmp.Contains(logs, "was not updated because it has no branch checked out"))
	// nothing the user can do about it
	assert.Assert(t, !strings.Contains(logs, "--reset"))
}
