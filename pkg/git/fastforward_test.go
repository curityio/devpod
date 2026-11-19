package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gotest.tools/assert"
	"gotest.tools/assert/cmp"
)

// newRemoteAndCheckout creates a repository with a commit on branch main, and a clone of it.
func newRemoteAndCheckout(t *testing.T) (remote, checkout string) {
	t.Helper()
	isolateGit(t)
	root := t.TempDir()
	remote = filepath.Join(root, "remote")
	checkout = filepath.Join(root, "checkout")
	newRepository(t, remote)
	runGit(t, root, "clone", "-q", remote, checkout)
	return remote, checkout
}

func head(t *testing.T, repo string) string {
	t.Helper()
	return gitOutput(t, repo, "rev-parse", "HEAD")
}

func TestFastForwardUpdatesCleanCheckout(t *testing.T) {
	remote, checkout := newRemoteAndCheckout(t)
	before := head(t, checkout)
	writeRepoFiles(t, remote, ".devcontainer/devcontainer.json")
	commitAll(t, remote, "add devcontainer.json")

	// files git doesn't track are no reason to keep the old commit
	writeRepoFiles(t, checkout, "notes.txt")

	result, err := FastForward(context.Background(), checkout, FastForwardOptions{})
	assert.NilError(t, err)
	assert.Equal(t, result.Status, FastForwardUpdated)
	assert.Assert(t, strings.HasPrefix(before, result.From), "from %s", result.From)
	assert.Assert(t, strings.HasPrefix(head(t, remote), result.To), "to %s", result.To)
	assert.Equal(t, head(t, checkout), head(t, remote))
	_, err = os.Stat(filepath.Join(checkout, ".devcontainer", "devcontainer.json"))
	assert.NilError(t, err)
	_, err = os.Stat(filepath.Join(checkout, "notes.txt"))
	assert.NilError(t, err)
}

func TestFastForwardUpToDate(t *testing.T) {
	_, checkout := newRemoteAndCheckout(t)

	result, err := FastForward(context.Background(), checkout, FastForwardOptions{})
	assert.NilError(t, err)
	assert.Equal(t, result.Status, FastForwardUpToDate)
}

func TestFastForwardKeepsUnpushedCommits(t *testing.T) {
	_, checkout := newRemoteAndCheckout(t)
	writeRepoFiles(t, checkout, "local.txt")
	commitAll(t, checkout, "local commit")
	before := head(t, checkout)

	// nothing to fetch, so the commit that was not pushed is no reason to warn
	result, err := FastForward(context.Background(), checkout, FastForwardOptions{})
	assert.NilError(t, err)
	assert.Equal(t, result.Status, FastForwardUpToDate)
	assert.Equal(t, head(t, checkout), before)
}

func TestFastForwardSkipsDivergedBranch(t *testing.T) {
	remote, checkout := newRemoteAndCheckout(t)
	writeRepoFiles(t, remote, "remote.txt")
	commitAll(t, remote, "remote commit")
	writeRepoFiles(t, checkout, "local.txt")
	commitAll(t, checkout, "local commit")
	before := head(t, checkout)

	result, err := FastForward(context.Background(), checkout, FastForwardOptions{})
	assert.NilError(t, err)
	assert.Equal(t, result.Status, FastForwardSkipped)
	assert.Assert(t, cmp.Contains(result.Reason, "diverged"))
	assert.Assert(t, cmp.Contains(result.Reason, "origin/main"))
	assert.Equal(t, head(t, checkout), before)
}

func TestFastForwardSkipsCheckoutWithChanges(t *testing.T) {
	for name, change := range map[string]func(t *testing.T, checkout string){
		"modified": func(t *testing.T, checkout string) {
			assert.NilError(t, os.WriteFile(filepath.Join(checkout, "README.md"), []byte("changed\n"), 0o644))
		},
		"staged": func(t *testing.T, checkout string) {
			assert.NilError(t, os.WriteFile(filepath.Join(checkout, "README.md"), []byte("changed\n"), 0o644))
			runGit(t, checkout, "add", "README.md")
		},
		"deleted": func(t *testing.T, checkout string) {
			assert.NilError(t, os.Remove(filepath.Join(checkout, "README.md")))
		},
	} {
		t.Run(name, func(t *testing.T) {
			remote, checkout := newRemoteAndCheckout(t)
			// the remote commit doesn't touch the changed file: git itself would merge it
			writeRepoFiles(t, remote, "remote.txt")
			commitAll(t, remote, "remote commit")
			before := head(t, checkout)
			change(t, checkout)

			result, err := FastForward(context.Background(), checkout, FastForwardOptions{})
			assert.NilError(t, err)
			assert.Equal(t, result.Status, FastForwardSkipped)
			assert.Assert(t, cmp.Contains(result.Reason, "README.md"))
			assert.Equal(t, head(t, checkout), before)
			_, err = os.Stat(filepath.Join(checkout, "remote.txt"))
			assert.Assert(t, os.IsNotExist(err))
		})
	}
}

func TestFastForwardSkipsDetachedHead(t *testing.T) {
	remote, checkout := newRemoteAndCheckout(t)
	runGit(t, checkout, "checkout", "-q", "--detach")
	writeRepoFiles(t, remote, "remote.txt")
	commitAll(t, remote, "remote commit")

	result, err := FastForward(context.Background(), checkout, FastForwardOptions{})
	assert.NilError(t, err)
	assert.Equal(t, result.Status, FastForwardSkipped)
	assert.Assert(t, cmp.Contains(result.Reason, "no branch"))
}

func TestFastForwardSkipsBranchWithoutUpstream(t *testing.T) {
	_, checkout := newRemoteAndCheckout(t)
	runGit(t, checkout, "checkout", "-q", "-b", "local-only")

	result, err := FastForward(context.Background(), checkout, FastForwardOptions{})
	assert.NilError(t, err)
	assert.Equal(t, result.Status, FastForwardSkipped)
	assert.Assert(t, cmp.Contains(result.Reason, "local-only has no upstream"))
}

func TestFastForwardUpdatesTheCheckedOutBranch(t *testing.T) {
	remote, checkout := newRemoteAndCheckout(t)
	runGit(t, remote, "checkout", "-q", "-b", "feature")
	writeRepoFiles(t, remote, "feature.txt")
	commitAll(t, remote, "feature commit")
	runGit(t, checkout, "fetch", "-q")
	runGit(t, checkout, "checkout", "-q", "feature")
	writeRepoFiles(t, remote, "feature2.txt")
	commitAll(t, remote, "second feature commit")

	result, err := FastForward(context.Background(), checkout, FastForwardOptions{})
	assert.NilError(t, err)
	assert.Equal(t, result.Status, FastForwardUpdated)
	assert.Equal(t, gitOutput(t, checkout, "symbolic-ref", "--short", "HEAD"), "feature")
	assert.Equal(t, head(t, checkout), head(t, remote))
}

func TestFastForwardShallowClone(t *testing.T) {
	isolateGit(t)
	root := t.TempDir()
	remote := filepath.Join(root, "remote")
	checkout := filepath.Join(root, "checkout")
	newRepository(t, remote)
	writeRepoFiles(t, remote, "second.txt")
	commitAll(t, remote, "second commit")
	// a local path is only cloned shallow as a file:// url
	url := "file://" + filepath.ToSlash(remote)
	if !strings.HasPrefix(remote, "/") {
		// a Windows path has no leading slash
		url = "file:///" + filepath.ToSlash(remote)
	}
	runGit(t, root, "clone", "-q", "--depth=1", url, checkout)
	writeRepoFiles(t, remote, "third.txt")
	commitAll(t, remote, "third commit")

	result, err := FastForward(context.Background(), checkout, FastForwardOptions{})
	assert.NilError(t, err)
	assert.Equal(t, result.Status, FastForwardUpdated)
	assert.Equal(t, head(t, checkout), head(t, remote))
}

func TestFastForwardPullRequest(t *testing.T) {
	remote, checkout := newRemoteAndCheckout(t)
	const pr = "pull/7/head"
	// a pull request is a reference of the remote that is no branch
	runGit(t, remote, "checkout", "-q", "-b", "contribution")
	writeRepoFiles(t, remote, "contribution.txt")
	commitAll(t, remote, "contribution")
	runGit(t, remote, "update-ref", "refs/"+pr, "HEAD")
	runGit(t, checkout, "fetch", "-q", "origin", pr+":"+GetBranchNameForPR(pr))
	runGit(t, checkout, "switch", "-q", GetBranchNameForPR(pr))

	writeRepoFiles(t, remote, "contribution2.txt")
	commitAll(t, remote, "second contribution")
	runGit(t, remote, "update-ref", "refs/"+pr, "HEAD")

	result, err := FastForward(context.Background(), checkout, FastForwardOptions{PR: pr})
	assert.NilError(t, err)
	assert.Equal(t, result.Status, FastForwardUpdated)
	assert.Equal(t, head(t, checkout), head(t, remote))
	assert.Equal(t, gitOutput(t, checkout, "symbolic-ref", "--short", "HEAD"), GetBranchNameForPR(pr))
}

func TestFastForwardRemovedPaths(t *testing.T) {
	isolateGit(t)
	root := t.TempDir()
	remote := filepath.Join(root, "remote")
	checkout := filepath.Join(root, "checkout")
	newRepository(t, remote)
	writeRepoFiles(t, remote, "data/big.bin", "data/sub/other.bin", "single.bin")
	commitAll(t, remote, "add data")
	runGit(t, root, "clone", "-q", remote, checkout)

	// what .devpodignore does to a clone
	removed := []string{"data/", "./single.bin", "missing", "../outside"}
	assert.NilError(t, os.RemoveAll(filepath.Join(checkout, "data")))
	assert.NilError(t, os.Remove(filepath.Join(checkout, "single.bin")))

	// the remote changes a removed file, which git refuses to merge into a work tree without it
	assert.NilError(t, os.WriteFile(filepath.Join(remote, "data", "big.bin"), []byte("new\n"), 0o644))
	commitAll(t, remote, "change data")

	result, err := FastForward(context.Background(), checkout, FastForwardOptions{Removed: removed})
	assert.NilError(t, err)
	assert.Equal(t, result.Status, FastForwardUpdated)
	assert.Equal(t, head(t, checkout), head(t, remote))
	// the removed paths are checked out again, for the caller to remove
	content, err := os.ReadFile(filepath.Join(checkout, "data", "big.bin"))
	assert.NilError(t, err)
	assert.Equal(t, string(content), "new\n")
	assert.Equal(t, gitOutput(t, checkout, "status", "--porcelain"), "")
}

func TestFastForwardRemovedPathsDoNotHideOtherChanges(t *testing.T) {
	remote, checkout := newRemoteAndCheckout(t)
	writeRepoFiles(t, remote, "data/big.bin", "kept.txt")
	commitAll(t, remote, "add files")
	runGit(t, checkout, "pull", "-q")
	writeRepoFiles(t, remote, "remote.txt")
	commitAll(t, remote, "remote commit")

	assert.NilError(t, os.RemoveAll(filepath.Join(checkout, "data")))
	assert.NilError(t, os.Remove(filepath.Join(checkout, "kept.txt")))

	result, err := FastForward(context.Background(), checkout, FastForwardOptions{Removed: []string{"data"}})
	assert.NilError(t, err)
	assert.Equal(t, result.Status, FastForwardSkipped)
	assert.Assert(t, cmp.Contains(result.Reason, "kept.txt"))
	// nothing is checked out again when the checkout is skipped
	_, err = os.Stat(filepath.Join(checkout, "data"))
	assert.Assert(t, os.IsNotExist(err))
}

func TestFastForwardFailsWithoutRemote(t *testing.T) {
	remote, checkout := newRemoteAndCheckout(t)
	before := head(t, checkout)
	assert.NilError(t, os.RemoveAll(remote))

	_, err := FastForward(context.Background(), checkout, FastForwardOptions{})
	assert.ErrorContains(t, err, "git fetch")
	assert.Equal(t, head(t, checkout), before)
}

func TestFastForwardFailsOutsideRepository(t *testing.T) {
	isolateGit(t)

	_, err := FastForward(context.Background(), t.TempDir(), FastForwardOptions{})
	assert.ErrorContains(t, err, "git rev-parse")
}

func TestUpdateSubmodules(t *testing.T) {
	isolateGit(t)
	// git doesn't clone a submodule from a local path by default
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "protocol.file.allow")
	t.Setenv("GIT_CONFIG_VALUE_0", "always")
	root := t.TempDir()
	library := filepath.Join(root, "library")
	remote := filepath.Join(root, "remote")
	checkout := filepath.Join(root, "checkout")
	newRepository(t, library)
	newRepository(t, remote)
	runGit(t, root, "clone", "-q", remote, checkout)

	runGit(t, remote, "submodule", "--quiet", "add", library, "lib")
	commitAll(t, remote, "add submodule")

	result, err := FastForward(context.Background(), checkout, FastForwardOptions{})
	assert.NilError(t, err)
	assert.Equal(t, result.Status, FastForwardUpdated)
	_, err = os.Stat(filepath.Join(checkout, "lib", "README.md"))
	assert.Assert(t, os.IsNotExist(err))

	assert.NilError(t, UpdateSubmodules(context.Background(), checkout, "", nil))
	_, err = os.Stat(filepath.Join(checkout, "lib", "README.md"))
	assert.NilError(t, err)
}
