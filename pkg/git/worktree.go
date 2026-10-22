package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrNotInstalled is returned when the git binary is not in the PATH.
var ErrNotInstalled = errors.New("git is not installed")

// ListFiles returns the files of the work tree at dir that git doesn't ignore: tracked files, and untracked files
// that no .gitignore file, .git/info/exclude or core.excludesFile ignores. Paths are slash-separated and relative
// to dir, spelled as git spells them. The files of checked out submodules are listed too, but not those of other
// repositories or worktrees nested in dir, nor anything in .git.
func ListFiles(ctx context.Context, dir string) ([]string, error) {
	entries, err := indexEntries(ctx, dir)
	if err != nil {
		return nil, err
	}
	untracked, err := output(ctx, dir, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}

	files := []string{}
	for _, entry := range entries {
		if !entry.checkedOutSubmodule(dir) {
			files = append(files, entry.path)
			continue
		}
		submoduleFiles, err := ListFiles(ctx, filepath.Join(dir, filepath.FromSlash(entry.path)))
		if err != nil {
			return nil, fmt.Errorf("list files of submodule %s: %w", entry.path, err)
		}
		for _, submoduleFile := range submoduleFiles {
			files = append(files, path.Join(entry.path, submoduleFile))
		}
	}
	for _, file := range strings.Split(untracked, "\x00") {
		// Nested repositories and worktrees are listed as a folder with a trailing slash
		if file != "" && !strings.HasSuffix(file, "/") {
			files = append(files, file)
		}
	}

	return files, nil
}

// indexEntry is a path in the index of a repository.
type indexEntry struct {
	path      string
	submodule bool
}

// checkedOutSubmodule reports whether the entry is a submodule that is checked out in the work tree at dir. One that
// isn't has no .git, and git would take its folder for one of the parent repository.
func (e indexEntry) checkedOutSubmodule(dir string) bool {
	if !e.submodule {
		return false
	}
	_, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(e.path), ".git"))
	return err == nil
}

// indexEntries returns the paths in the index of the work tree at dir.
func indexEntries(ctx context.Context, dir string) ([]indexEntry, error) {
	out, err := output(ctx, dir, "ls-files", "-z", "--cached", "--stage")
	if err != nil {
		return nil, err
	}

	entries := []indexEntry{}
	for _, line := range strings.Split(out, "\x00") {
		// <mode> <object> <stage>\t<file>
		info, file, found := strings.Cut(line, "\t")
		// a file with a merge conflict has an entry for each stage, one after the other
		if !found || (len(entries) > 0 && entries[len(entries)-1].path == file) {
			continue
		}
		entries = append(entries, indexEntry{path: file, submodule: strings.HasPrefix(info, "160000 ")})
	}
	return entries, nil
}

// IsTopLevel reports whether dir is the top level folder of a git work tree.
func IsTopLevel(ctx context.Context, dir string) bool {
	prefix, err := output(ctx, dir, "rev-parse", "--show-prefix")
	return err == nil && strings.TrimSpace(prefix) == ""
}

// IgnoresCase reports whether git ignores the case of file names in the work tree at dir, as it does on a
// case-insensitive file system.
func IgnoresCase(ctx context.Context, dir string) bool {
	value, err := output(ctx, dir, "config", "--bool", "core.ignorecase")
	return err == nil && strings.TrimSpace(value) == "true"
}

// SnapshotResult describes the repositories Snapshot creates.
type SnapshotResult struct {
	// Branch is the checked out branch, empty if HEAD is detached.
	Branch string
	// GitDirs are the .git folders created, slash-separated and relative to the target: .git, and one for each
	// checked out submodule.
	GitDirs []string
}

// Snapshot creates a shallow repository in target/.git with the commit checked out in the work tree at dir, so that
// a copy of the work tree can be committed and pushed elsewhere. It has the commit and, if the branch has an upstream
// branch, the commits of both since they diverged, but no other history. The branch, its upstream branch, the remotes
// and the submodules are the same as in dir. The user is left out, as DevPod gives the workspace the repository's git
// identity in the global config, which a local one would override. The index matches the commit, so changes staged
// in dir show as not staged.
//
// Each checked out submodule gets a snapshot of its own in its folder in target. missing are the files of the work
// tree, as ListFiles returns them, that the copy won't have: they are marked skip-worktree in the index, so that git
// doesn't take them for deleted.
func Snapshot(ctx context.Context, dir, target string, missing []string) (*SnapshotResult, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}

	branch, err := snapshotRepository(ctx, dir, target, missing)
	if err != nil {
		return nil, err
	}
	result := &SnapshotResult{Branch: branch, GitDirs: []string{".git"}}

	entries, err := indexEntries(ctx, dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.checkedOutSubmodule(dir) {
			continue
		}
		var submoduleMissing []string
		for _, file := range missing {
			if rest, found := strings.CutPrefix(file, entry.path+"/"); found {
				submoduleMissing = append(submoduleMissing, rest)
			}
		}
		submoduleTarget := filepath.Join(target, filepath.FromSlash(entry.path))
		if err := os.MkdirAll(submoduleTarget, 0o755); err != nil {
			return nil, err
		}
		submodule, err := Snapshot(ctx, filepath.Join(dir, filepath.FromSlash(entry.path)), submoduleTarget, submoduleMissing)
		if err != nil {
			return nil, fmt.Errorf("snapshot submodule %s: %w", entry.path, err)
		}
		for _, gitDir := range submodule.GitDirs {
			result.GitDirs = append(result.GitDirs, path.Join(entry.path, gitDir))
		}
	}

	return result, nil
}

// snapshotRepository creates the snapshot of the repository at dir, without its submodules, and returns its branch.
func snapshotRepository(ctx context.Context, dir, target string, missing []string) (string, error) {
	// An empty template leaves out the sample hooks. The formats are those of dir rather than the defaults of the
	// user's config, which may be too new for git in the workspace.
	initArgs := []string{"init", "--quiet", "--template="}
	if objectFormat, err := output(ctx, dir, "rev-parse", "--show-object-format"); err == nil {
		initArgs = append(initArgs, "--object-format="+strings.TrimSpace(objectFormat))
	}
	// git before 2.45 has no ref formats other than files
	if refFormat, err := output(ctx, dir, "rev-parse", "--show-ref-format"); err == nil {
		initArgs = append(initArgs, "--ref-format="+strings.TrimSpace(refFormat))
	}
	if _, err := output(ctx, target, initArgs...); err != nil {
		return "", err
	}
	// git init sets these for a case-insensitive file system, as on macOS, but the workspace's file system is not.
	// It also sets core.filemode and core.symlinks, which match how the files of the work tree are uploaded.
	for _, key := range []string{"core.ignorecase", "core.precomposeunicode"} {
		_, _ = output(ctx, target, "config", "--local", "--unset-all", key)
	}

	branchRef, err := output(ctx, dir, "symbolic-ref", "--quiet", "HEAD")
	branchRef = strings.TrimSpace(branchRef)
	if err != nil {
		branchRef = ""
	}

	// a new repository has no commit to fetch
	_, err = output(ctx, dir, "rev-parse", "--quiet", "--verify", "HEAD")
	if err == nil {
		if err := fetchCommits(ctx, dir, target, branchRef); err != nil {
			return "", err
		}
		if err := skipWorktree(ctx, target, missing); err != nil {
			return "", err
		}
	} else if branchRef != "" {
		if _, err := output(ctx, target, "symbolic-ref", "HEAD", branchRef); err != nil {
			return "", err
		}
	}

	if err := copyConfig(ctx, dir, target, branchRef); err != nil {
		return "", err
	}

	// info/exclude ignores files in git status, like .gitignore files
	exclude, err := output(ctx, dir, "rev-parse", "--path-format=absolute", "--git-path", "info/exclude")
	if err == nil {
		if content, err := os.ReadFile(strings.TrimSpace(exclude)); err == nil {
			infoDir := filepath.Join(target, ".git", "info")
			if err := os.MkdirAll(infoDir, 0o755); err != nil {
				return "", err
			}
			if err := os.WriteFile(filepath.Join(infoDir, "exclude"), content, 0o644); err != nil {
				return "", err
			}
		}
	}

	_ = os.Remove(filepath.Join(target, ".git", "FETCH_HEAD"))
	return strings.TrimPrefix(branchRef, "refs/heads/"), nil
}

// fetchCommits fetches HEAD of dir into target with as little history as possible, and its upstream branch if
// there is one.
func fetchCommits(ctx context.Context, dir, target, branchRef string) error {
	// Fetch the commits since HEAD and the upstream branch diverged, so that git status counts the commits each is
	// ahead of the other. Both are fetched at once, as a second shallow fetch would cut the history of the first.
	const headRef, fetchedUpstreamRef = "refs/devpod-snapshot/head", "refs/devpod-snapshot/upstream"
	upstreamRef, depth := "", 1
	if branchRef != "" {
		upstreamRef, depth = upstream(ctx, dir)
	}
	args := []string{"fetch", "--quiet", "--no-tags", "--depth=" + strconv.Itoa(depth), dir, "+HEAD:" + headRef}
	if upstreamRef != "" {
		args = append(args, "+"+upstreamRef+":"+fetchedUpstreamRef)
	}
	if _, err := output(ctx, target, args...); err != nil {
		return err
	}

	head, err := output(ctx, target, "rev-parse", "--verify", headRef+"^{commit}")
	if err != nil {
		return err
	}
	head = strings.TrimSpace(head)
	if branchRef == "" {
		_, err = output(ctx, target, "update-ref", "--no-deref", "HEAD", head)
		if err != nil {
			return err
		}
	} else {
		if _, err := output(ctx, target, "update-ref", branchRef, head); err != nil {
			return err
		}
		if _, err := output(ctx, target, "symbolic-ref", "HEAD", branchRef); err != nil {
			return err
		}
	}
	if _, err := output(ctx, target, "update-ref", "-d", headRef); err != nil {
		return err
	}

	if upstreamRef != "" {
		commit, err := output(ctx, target, "rev-parse", "--verify", fetchedUpstreamRef+"^{commit}")
		if err != nil {
			return err
		}
		if _, err := output(ctx, target, "update-ref", upstreamRef, strings.TrimSpace(commit)); err != nil {
			return err
		}
		if _, err := output(ctx, target, "update-ref", "-d", fetchedUpstreamRef); err != nil {
			return err
		}
	}

	// without an index git status shows every file as deleted and untracked
	_, err = output(ctx, target, "read-tree", "HEAD")
	return err
}

// upstream returns the upstream branch of HEAD in dir, and how deep to fetch HEAD and the upstream branch for both to
// reach the commit they diverged from. It returns an empty ref and a depth of 1 if there is no upstream branch or no
// common commit.
func upstream(ctx context.Context, dir string) (string, int) {
	ref, err := output(ctx, dir, "rev-parse", "--quiet", "--verify", "--symbolic-full-name", "@{upstream}")
	if err != nil || strings.TrimSpace(ref) == "" {
		return "", 1
	}
	if _, err := output(ctx, dir, "merge-base", "HEAD", "@{upstream}"); err != nil {
		return "", 1
	}
	counts, err := output(ctx, dir, "rev-list", "--left-right", "--count", "HEAD...@{upstream}")
	if err != nil {
		return "", 1
	}
	ahead, behind, _ := strings.Cut(strings.TrimSpace(counts), "\t")
	aheadCount, err := strconv.Atoi(ahead)
	if err != nil {
		return "", 1
	}
	behindCount, err := strconv.Atoi(behind)
	if err != nil {
		return "", 1
	}
	return strings.TrimSpace(ref), max(aheadCount, behindCount) + 1
}

// skipWorktree marks the files of the index of the repository in target that are missing as skip-worktree.
func skipWorktree(ctx context.Context, target string, missing []string) error {
	if len(missing) == 0 {
		return nil
	}

	entries, err := indexEntries(ctx, target)
	if err != nil {
		return err
	}
	inIndex := map[string]bool{}
	for _, entry := range entries {
		inIndex[entry.path] = true
	}
	stdin := &bytes.Buffer{}
	for _, file := range missing {
		if inIndex[file] {
			stdin.WriteString(file + "\x00")
		}
	}
	if stdin.Len() == 0 {
		return nil
	}

	cmd := CommandContext(ctx, nil, "-C", target, "update-index", "-z", "--skip-worktree", "--stdin")
	cmd.Stdin = stdin
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git update-index --skip-worktree: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// copyConfig copies the remotes, the submodules and the settings of the branch from the config of the repository in
// dir to the repository in target.
func copyConfig(ctx context.Context, dir, target, branchRef string) error {
	entries, err := output(ctx, dir, "config", "--local", "--null", "--get-regexp", `^(remote|submodule|branch)\.`)
	if err != nil {
		// exit code 1 means there are no such entries
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil
		}
		return err
	}

	branchPrefix := "branch." + strings.TrimPrefix(branchRef, "refs/heads/") + "."
	for _, entry := range strings.Split(entries, "\x00") {
		if entry == "" {
			continue
		}
		key, value, found := strings.Cut(entry, "\n")
		// a key without a value is a boolean that is true
		if !found {
			value = "true"
		}
		// the settings of branch a.b start with branch.a. too
		if strings.HasPrefix(key, "branch.") {
			variable, ok := strings.CutPrefix(key, branchPrefix)
			if branchRef == "" || !ok || strings.Contains(variable, ".") {
				continue
			}
		}
		if _, err := output(ctx, target, "config", "--local", "--add", key, value); err != nil {
			return err
		}
	}
	return nil
}

// output runs git in dir and returns its standard output.
func output(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := CommandContext(ctx, nil, append([]string{"-C", dir}, args...)...)
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	out, err := cmd.Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", ErrNotInstalled
		}
		return string(out), fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(stderr.String()), err)
	}
	return string(out), nil
}
