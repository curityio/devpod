package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/assert"
)

func TestReadHeadCommit(t *testing.T) {
	isolateGit(t)
	repo := filepath.Join(t.TempDir(), "repo")
	newRepository(t, repo)
	commit := head(t, repo)

	fromGit, err := HeadCommit(context.Background(), repo)
	assert.NilError(t, err)
	assert.Equal(t, fromGit, commit)

	// a branch with a file of its own
	assert.Equal(t, ReadHeadCommit(repo), commit)

	// a branch in packed-refs
	runGit(t, repo, "pack-refs", "--all")
	_, err = os.Stat(filepath.Join(repo, ".git", "refs", "heads", "main"))
	assert.Assert(t, os.IsNotExist(err))
	assert.Equal(t, ReadHeadCommit(repo), commit)

	// a tag before the branch in packed-refs adds a line that is no reference
	runGit(t, repo, "-c", "user.name=test", "-c", "user.email=test@example.com", "tag", "-a", "-m", "tag", "a-tag")
	runGit(t, repo, "pack-refs", "--all")
	assert.Equal(t, ReadHeadCommit(repo), commit)

	// a detached HEAD
	runGit(t, repo, "checkout", "-q", "--detach")
	assert.Equal(t, ReadHeadCommit(repo), commit)
}

func TestReadHeadCommitOfSnapshot(t *testing.T) {
	isolateGit(t)
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	newRepository(t, repo)
	// what the workspace folder of a container has when DevPod copied it there
	target := filepath.Join(root, "target")
	assert.NilError(t, os.MkdirAll(target, 0o755))
	_, err := Snapshot(context.Background(), repo, target, nil)
	assert.NilError(t, err)

	assert.Equal(t, ReadHeadCommit(target), head(t, repo))
}

func TestReadHeadCommitUnknown(t *testing.T) {
	isolateGit(t)
	root := t.TempDir()

	// no repository
	assert.Equal(t, ReadHeadCommit(root), "")

	// no commit yet
	empty := filepath.Join(root, "empty")
	runGit(t, root, "init", "-q", "-b", "main", empty)
	assert.Equal(t, ReadHeadCommit(empty), "")

	for name, headFile := range map[string]string{
		"not a hash":           "main\n",
		"reference outside":    "ref: refs/../../outside\n",
		"not a reference":      "ref: HEAD\n",
		"reftable":             "ref: refs/heads/.invalid\n",
		"upper case":           "ref: refs/heads/upper\n",
		"hash of wrong length": "abcdef\n",
	} {
		t.Run(name, func(t *testing.T) {
			repo := filepath.Join(t.TempDir(), "repo")
			assert.NilError(t, os.MkdirAll(filepath.Join(repo, ".git", "refs", "heads"), 0o755))
			assert.NilError(t, os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte(headFile), 0o644))
			assert.NilError(t, os.WriteFile(filepath.Join(repo, ".git", "refs", "heads", "upper"),
				[]byte("ABCDEF0123456789ABCDEF0123456789ABCDEF01\n"), 0o644))
			assert.Equal(t, ReadHeadCommit(repo), "")
		})
	}

	// a work tree has a file instead of the folder
	worktree := filepath.Join(root, "worktree")
	assert.NilError(t, os.MkdirAll(worktree, 0o755))
	assert.NilError(t, os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: /elsewhere\n"), 0o644))
	assert.Equal(t, ReadHeadCommit(worktree), "")
}
