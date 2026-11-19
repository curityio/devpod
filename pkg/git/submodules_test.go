package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/assert"
	"gotest.tools/assert/cmp"
)

// newRemoteWithSubmodules creates a repository with the submodules at the given paths, which
// are all of the same library repository, and a clone of it with its submodules.
func newRemoteWithSubmodules(t *testing.T, paths ...string) (library, remote, checkout string) {
	t.Helper()
	isolateGit(t)
	// git doesn't clone a submodule from a local path by default
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "protocol.file.allow")
	t.Setenv("GIT_CONFIG_VALUE_0", "always")
	root := t.TempDir()
	library = filepath.Join(root, "library")
	remote = filepath.Join(root, "remote")
	checkout = filepath.Join(root, "checkout")
	newRepository(t, library)
	newRepository(t, remote)
	for _, p := range paths {
		runGit(t, remote, "submodule", "--quiet", "add", library, p)
	}
	if len(paths) > 0 {
		commitAll(t, remote, "add submodules")
	}
	runGit(t, root, "clone", "-q", "--recurse-submodules", remote, checkout)
	return library, remote, checkout
}

// moveSubmodule makes the remote record a new commit of the library for the submodule at path.
func moveSubmodule(t *testing.T, library, remote, path string) string {
	t.Helper()
	writeRepoFiles(t, library, "new-"+filepath.Base(path)+".txt")
	commitAll(t, library, "library commit")
	runGit(t, filepath.Join(remote, filepath.FromSlash(path)), "pull", "-q", "origin", "main")
	commitAll(t, remote, "move "+path)
	return head(t, library)
}

func updateSubmodules(t *testing.T, checkout string, options SubmodulesOptions) *SubmodulesResult {
	t.Helper()
	result, err := UpdateSubmodules(context.Background(), checkout, options)
	assert.NilError(t, err)
	return result
}

func fastForward(t *testing.T, checkout string, expected FastForwardStatus, removed ...string) {
	t.Helper()
	result, err := FastForward(context.Background(), checkout, FastForwardOptions{Removed: removed})
	assert.NilError(t, err)
	assert.Equal(t, result.Status, expected, result.Reason)
}

func TestUpdateSubmodules(t *testing.T) {
	library, remote, checkout := newRemoteWithSubmodules(t, "lib", "with space/lib (copy)")
	assert.DeepEqual(t, updateSubmodules(t, checkout, SubmodulesOptions{}), &SubmodulesResult{})

	// the remote moves a submodule and adds another
	commit := moveSubmodule(t, library, remote, "lib")
	runGit(t, remote, "submodule", "--quiet", "add", library, "added")
	commitAll(t, remote, "add submodule")
	fastForward(t, checkout, FastForwardUpdated)
	_, err := os.Stat(filepath.Join(checkout, "added", "README.md"))
	assert.Assert(t, os.IsNotExist(err))

	// the checkout has submodules, so a new one is cloned without the option
	result := updateSubmodules(t, checkout, SubmodulesOptions{})

	assert.DeepEqual(t, result, &SubmodulesResult{Updated: []string{"added", "lib"}})
	assert.Equal(t, head(t, filepath.Join(checkout, "lib")), commit)
	_, err = os.Stat(filepath.Join(checkout, "added", "README.md"))
	assert.NilError(t, err)
	assert.Equal(t, gitOutput(t, checkout, "status", "--porcelain"), "")
	assert.DeepEqual(t, updateSubmodules(t, checkout, SubmodulesOptions{}), &SubmodulesResult{})
}

func TestUpdateSubmodulesInit(t *testing.T) {
	isolateGit(t)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "protocol.file.allow")
	t.Setenv("GIT_CONFIG_VALUE_0", "always")
	root := t.TempDir()
	library := filepath.Join(root, "library")
	remote := filepath.Join(root, "remote")
	checkout := filepath.Join(root, "checkout")
	newRepository(t, library)
	newRepository(t, remote)
	runGit(t, remote, "submodule", "--quiet", "add", library, "lib")
	commitAll(t, remote, "add submodule")
	// cloned without its submodules
	runGit(t, root, "clone", "-q", remote, checkout)

	assert.DeepEqual(t, updateSubmodules(t, checkout, SubmodulesOptions{}), &SubmodulesResult{})
	_, err := os.Stat(filepath.Join(checkout, "lib", "README.md"))
	assert.Assert(t, os.IsNotExist(err))

	assert.DeepEqual(t, updateSubmodules(t, checkout, SubmodulesOptions{Init: true}), &SubmodulesResult{Updated: []string{"lib"}})
	_, err = os.Stat(filepath.Join(checkout, "lib", "README.md"))
	assert.NilError(t, err)
}

func TestUpdateSubmodulesNested(t *testing.T) {
	isolateGit(t)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "protocol.file.allow")
	t.Setenv("GIT_CONFIG_VALUE_0", "always")
	root := t.TempDir()
	inner := filepath.Join(root, "inner")
	library := filepath.Join(root, "library")
	remote := filepath.Join(root, "remote")
	checkout := filepath.Join(root, "checkout")
	newRepository(t, inner)
	newRepository(t, library)
	runGit(t, library, "submodule", "--quiet", "add", inner, "inner")
	commitAll(t, library, "add inner")
	newRepository(t, remote)
	runGit(t, remote, "submodule", "--quiet", "add", library, "lib")
	commitAll(t, remote, "add lib")
	runGit(t, root, "clone", "-q", "--recurse-submodules", remote, checkout)

	// the library records a new commit of its submodule, and the remote the new commit of the library
	writeRepoFiles(t, inner, "new.txt")
	commitAll(t, inner, "inner commit")
	runGit(t, filepath.Join(library, "inner"), "pull", "-q", "origin", "main")
	commitAll(t, library, "move inner")
	runGit(t, filepath.Join(remote, "lib"), "pull", "-q", "origin", "main")
	commitAll(t, remote, "move lib")
	fastForward(t, checkout, FastForwardUpdated)

	result := updateSubmodules(t, checkout, SubmodulesOptions{})

	assert.DeepEqual(t, result, &SubmodulesResult{Updated: []string{"lib", "lib/inner"}})
	assert.Equal(t, head(t, filepath.Join(checkout, "lib")), head(t, library))
	assert.Equal(t, head(t, filepath.Join(checkout, "lib", "inner")), head(t, inner))
	assert.Equal(t, gitOutput(t, checkout, "status", "--porcelain"), "")
}

func TestUpdateSubmodulesAfterFailedUpdate(t *testing.T) {
	library, remote, checkout := newRemoteWithSubmodules(t, "lib", "other")
	commit := moveSubmodule(t, library, remote, "lib")
	fastForward(t, checkout, FastForwardUpdated)
	// the update of the submodules failed then: one stayed at its commit, and one is not there
	runGit(t, checkout, "submodule", "--quiet", "deinit", "-f", "other")

	// the remote has a new commit by the next time, which the checkout can't get: a submodule at another
	// commit is a change of the checkout
	writeRepoFiles(t, remote, "remote.txt")
	commitAll(t, remote, "remote commit")
	fastForward(t, checkout, FastForwardSkipped)

	// until the submodules are updated
	result := updateSubmodules(t, checkout, SubmodulesOptions{})

	assert.DeepEqual(t, result, &SubmodulesResult{Updated: []string{"lib", "other"}})
	assert.Equal(t, head(t, filepath.Join(checkout, "lib")), commit)
	_, err := os.Stat(filepath.Join(checkout, "other", "README.md"))
	assert.NilError(t, err)
	fastForward(t, checkout, FastForwardUpdated)
}

func TestUpdateSubmodulesKeepsWork(t *testing.T) {
	for name, test := range map[string]struct {
		work   func(t *testing.T, submodule string)
		reason string
	}{
		"commit on a branch": {
			work: func(t *testing.T, submodule string) {
				runGit(t, submodule, "checkout", "-q", "-b", "work")
				writeRepoFiles(t, submodule, "work.txt")
				commitAll(t, submodule, "work")
			},
			reason: "it has a branch checked out",
		},
		"change that is not committed": {
			work: func(t *testing.T, submodule string) {
				writeRepoFiles(t, submodule, "work.txt")
				commitAll(t, submodule, "work")
				assert.NilError(t, os.WriteFile(filepath.Join(submodule, "README.md"), []byte("work\n"), 0o644))
			},
			reason: "it has changes that are not committed",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, checkout := newRemoteWithSubmodules(t, "lib", "other")
			submodule := filepath.Join(checkout, "lib")
			test.work(t, submodule)
			before := head(t, submodule)
			runGit(t, checkout, "submodule", "--quiet", "deinit", "-f", "other")

			result := updateSubmodules(t, checkout, SubmodulesOptions{})

			assert.Equal(t, len(result.Skipped), 1)
			assert.Equal(t, result.Skipped[0].Path, "lib")
			assert.Assert(t, cmp.Contains(result.Skipped[0].Reason, test.reason))
			assert.Equal(t, head(t, submodule), before)
			content, err := os.ReadFile(filepath.Join(submodule, "work.txt"))
			assert.NilError(t, err)
			assert.Equal(t, string(content), "work.txt\n")
			// the others are updated
			assert.DeepEqual(t, result.Updated, []string{"other"})
			_, err = os.Stat(filepath.Join(checkout, "other", "README.md"))
			assert.NilError(t, err)
		})
	}
}

func TestUpdateSubmodulesKeepsFilesGitDoesNotTrack(t *testing.T) {
	for name, ignored := range map[string]bool{"ignored": true, "untracked": false} {
		t.Run(name, func(t *testing.T) {
			library, remote, checkout := newRemoteWithSubmodules(t, "lib")
			submodule := filepath.Join(checkout, "lib")
			if ignored {
				// the .git of a submodule is a file that says where its repository is
				exclude := gitOutput(t, submodule, "rev-parse", "--path-format=absolute", "--git-path", "info/exclude")
				assert.NilError(t, os.MkdirAll(filepath.Dir(exclude), 0o755))
				assert.NilError(t, os.WriteFile(exclude, []byte("local.env\n"), 0o644))
			}
			assert.NilError(t, os.WriteFile(filepath.Join(submodule, "local.env"), []byte("mine\n"), 0o644))
			before := head(t, submodule)

			// the library starts to track the file
			assert.NilError(t, os.WriteFile(filepath.Join(library, "local.env"), []byte("theirs\n"), 0o644))
			commitAll(t, library, "track local.env")
			runGit(t, filepath.Join(remote, "lib"), "pull", "-q", "origin", "main")
			commitAll(t, remote, "move lib")
			fastForward(t, checkout, FastForwardUpdated)

			result := updateSubmodules(t, checkout, SubmodulesOptions{})

			assert.Equal(t, len(result.Updated), 0)
			assert.Equal(t, len(result.Skipped), 1)
			assert.Assert(t, cmp.Contains(result.Skipped[0].Reason, "git checkout"))
			assert.Equal(t, head(t, submodule), before)
			content, err := os.ReadFile(filepath.Join(submodule, "local.env"))
			assert.NilError(t, err)
			assert.Equal(t, string(content), "mine\n")
		})
	}
}

func TestUpdateSubmodulesKeepsFilesOfMissingSubmodule(t *testing.T) {
	_, _, checkout := newRemoteWithSubmodules(t, "lib")
	runGit(t, checkout, "submodule", "--quiet", "deinit", "-f", "lib")
	writeRepoFiles(t, checkout, "lib/README.md")
	assert.NilError(t, os.WriteFile(filepath.Join(checkout, "lib", "README.md"), []byte("mine\n"), 0o644))

	result := updateSubmodules(t, checkout, SubmodulesOptions{Init: true})

	assert.Equal(t, len(result.Updated), 0)
	assert.Equal(t, len(result.Skipped), 1)
	assert.Assert(t, cmp.Contains(result.Skipped[0].Reason, "its folder has files"))
	content, err := os.ReadFile(filepath.Join(checkout, "lib", "README.md"))
	assert.NilError(t, err)
	assert.Equal(t, string(content), "mine\n")
}

func TestFastForwardSkipsCheckoutWithChangedSubmodule(t *testing.T) {
	library, remote, checkout := newRemoteWithSubmodules(t, "lib")
	submodule := filepath.Join(checkout, "lib")
	runGit(t, submodule, "checkout", "-q", "-b", "work")
	writeRepoFiles(t, submodule, "work.txt")
	commitAll(t, submodule, "work")
	before := head(t, checkout)
	moveSubmodule(t, library, remote, "lib")

	result, err := FastForward(context.Background(), checkout, FastForwardOptions{})

	assert.NilError(t, err)
	assert.Equal(t, result.Status, FastForwardSkipped)
	assert.Assert(t, cmp.Contains(result.Reason, "e.g. to lib"))
	assert.Equal(t, head(t, checkout), before)
}

func TestUpdateSubmodulesKeepsBranchAtRecordedCommit(t *testing.T) {
	_, _, checkout := newRemoteWithSubmodules(t, "lib")
	// a branch at the commit that is recorded is no difference
	runGit(t, filepath.Join(checkout, "lib"), "checkout", "-q", "-b", "work")

	assert.DeepEqual(t, updateSubmodules(t, checkout, SubmodulesOptions{}), &SubmodulesResult{})
	assert.Equal(t, gitOutput(t, filepath.Join(checkout, "lib"), "symbolic-ref", "--short", "HEAD"), "work")
}

func TestUpdateSubmodulesLeavesRemovedPaths(t *testing.T) {
	library, remote, checkout := newRemoteWithSubmodules(t, "lib", "vendor/lib", "vendor/other")
	// what .devpodignore does to a clone
	removed := []string{"vendor/lib", "missing"}
	assert.NilError(t, os.RemoveAll(filepath.Join(checkout, "vendor", "lib")))

	// nothing to do for the submodules that are left
	assert.DeepEqual(t, updateSubmodules(t, checkout, SubmodulesOptions{Init: true, Removed: removed}), &SubmodulesResult{})
	_, err := os.Stat(filepath.Join(checkout, "vendor", "lib"))
	assert.Assert(t, os.IsNotExist(err))

	commit := moveSubmodule(t, library, remote, "vendor/other")
	moveSubmodule(t, library, remote, "vendor/lib")
	fastForward(t, checkout, FastForwardUpdated, removed...)

	result := updateSubmodules(t, checkout, SubmodulesOptions{Init: true, Removed: removed})

	assert.DeepEqual(t, result, &SubmodulesResult{Updated: []string{"vendor/other"}})
	assert.Equal(t, head(t, filepath.Join(checkout, "vendor", "other")), commit)
	_, err = os.Stat(filepath.Join(checkout, "vendor", "lib", "README.md"))
	assert.Assert(t, os.IsNotExist(err))
}

func TestUpdateSubmodulesWithoutSubmodules(t *testing.T) {
	_, _, checkout := newRemoteWithSubmodules(t)

	assert.DeepEqual(t, updateSubmodules(t, checkout, SubmodulesOptions{Init: true}), &SubmodulesResult{})
}
