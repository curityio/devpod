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
// to dir. The files of checked out submodules are listed too, but not those of other repositories or worktrees
// nested in dir, nor anything in .git.
func ListFiles(ctx context.Context, dir string) ([]string, error) {
	// The index tells submodules from files, untracked files have no mode
	tracked, err := output(ctx, dir, "ls-files", "-z", "--cached", "--stage")
	if err != nil {
		return nil, err
	}
	untracked, err := output(ctx, dir, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}

	files := []string{}
	for _, entry := range strings.Split(tracked, "\x00") {
		// <mode> <object> <stage>\t<file>
		info, file, found := strings.Cut(entry, "\t")
		// a file with a merge conflict has an entry for each stage, one after the other
		if !found || (len(files) > 0 && files[len(files)-1] == file) {
			continue
		}
		if !strings.HasPrefix(info, "160000 ") {
			files = append(files, file)
			continue
		}

		// A submodule that isn't checked out has no .git, and git would list the parent repository's files in it
		submodule := filepath.Join(dir, filepath.FromSlash(file))
		if _, err := os.Lstat(filepath.Join(submodule, ".git")); err != nil {
			files = append(files, file)
			continue
		}
		submoduleFiles, err := ListFiles(ctx, submodule)
		if err != nil {
			return nil, fmt.Errorf("list files of submodule %s: %w", file, err)
		}
		for _, submoduleFile := range submoduleFiles {
			files = append(files, path.Join(file, submoduleFile))
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

// IsTopLevel reports whether dir is the top level folder of a git work tree.
func IsTopLevel(ctx context.Context, dir string) bool {
	prefix, err := output(ctx, dir, "rev-parse", "--show-prefix")
	return err == nil && strings.TrimSpace(prefix) == ""
}

// Snapshot creates a shallow repository in target/.git with the commit checked out in the work tree at dir, so that
// a copy of the work tree can be committed and pushed elsewhere. It has the commit and, if the branch has an upstream
// branch, the commits of both since they diverged, but no other history. The branch, its upstream branch and the
// remotes and user of the repository's config are the same as in dir. The index matches the commit, so changes staged
// in dir show as not staged. It returns the checked out branch, empty if HEAD is detached.
func Snapshot(ctx context.Context, dir, target string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}

	// an empty template leaves out the sample hooks
	initArgs := []string{"init", "--quiet", "--template="}
	if objectFormat, err := output(ctx, dir, "rev-parse", "--show-object-format"); err == nil {
		initArgs = append(initArgs, "--object-format="+strings.TrimSpace(objectFormat))
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
	// ahead of the other
	depth, upstreamDepth, upstreamRef := 1, 0, ""
	if branchRef != "" {
		upstreamRef, depth, upstreamDepth = upstream(ctx, dir)
	}

	head, err := fetch(ctx, dir, target, "HEAD", depth)
	if err != nil {
		return err
	}
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

	if upstreamRef != "" {
		commit, err := fetch(ctx, dir, target, upstreamRef, upstreamDepth)
		if err != nil {
			return err
		}
		if _, err := output(ctx, target, "update-ref", upstreamRef, commit); err != nil {
			return err
		}
	}

	// without an index git status shows every file as deleted and untracked
	_, err = output(ctx, target, "read-tree", "HEAD")
	return err
}

// upstream returns the upstream branch of HEAD in dir, and how deep to fetch HEAD and the upstream branch to reach
// the commit they diverged from. It returns an empty ref and a depth of 1 for HEAD if there is no upstream branch or
// no common commit.
func upstream(ctx context.Context, dir string) (string, int, int) {
	ref, err := output(ctx, dir, "rev-parse", "--quiet", "--verify", "--symbolic-full-name", "@{upstream}")
	if err != nil || strings.TrimSpace(ref) == "" {
		return "", 1, 0
	}
	if _, err := output(ctx, dir, "merge-base", "HEAD", "@{upstream}"); err != nil {
		return "", 1, 0
	}
	counts, err := output(ctx, dir, "rev-list", "--left-right", "--count", "HEAD...@{upstream}")
	if err != nil {
		return "", 1, 0
	}
	ahead, behind, _ := strings.Cut(strings.TrimSpace(counts), "\t")
	aheadCount, err := strconv.Atoi(ahead)
	if err != nil {
		return "", 1, 0
	}
	behindCount, err := strconv.Atoi(behind)
	if err != nil {
		return "", 1, 0
	}
	return strings.TrimSpace(ref), aheadCount + 1, behindCount + 1
}

// fetch fetches ref of dir into target with the given depth and returns the fetched commit.
func fetch(ctx context.Context, dir, target, ref string, depth int) (string, error) {
	_, err := output(ctx, target, "fetch", "--quiet", "--no-tags", "--depth="+strconv.Itoa(depth), dir, ref)
	if err != nil {
		return "", err
	}
	commit, err := output(ctx, target, "rev-parse", "--verify", "FETCH_HEAD^{commit}")
	return strings.TrimSpace(commit), err
}

// copyConfig copies the remotes, the user and the settings of the branch from the config of the repository in dir
// to the repository in target.
func copyConfig(ctx context.Context, dir, target, branchRef string) error {
	entries, err := output(ctx, dir, "config", "--local", "--null", "--get-regexp", `^(remote|user|branch)\.`)
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
		key, value, found := strings.Cut(entry, "\n")
		if !found {
			continue
		}
		if strings.HasPrefix(key, "branch.") && (branchRef == "" || !strings.HasPrefix(key, branchPrefix)) {
			continue
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
