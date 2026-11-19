package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// FastForwardStatus says what FastForward did with a checkout.
type FastForwardStatus int

const (
	// FastForwardUpToDate means the remote branch has no commit the checkout lacks.
	FastForwardUpToDate FastForwardStatus = iota
	// FastForwardUpdated means the checkout was moved to the commit of the remote branch.
	FastForwardUpdated
	// FastForwardSkipped means the checkout was left as it was, because updating it could lose
	// something or there is nothing to update it from.
	FastForwardSkipped
)

// FastForwardOptions are the options of FastForward.
type FastForwardOptions struct {
	// PR is the pull request reference the checkout was created from, e.g. pull/12/head. Its
	// branch has no upstream branch, so it is updated from the reference.
	PR string
	// Helper is the credential helper to fetch with. It is not saved in the repository.
	Helper string
	// ExtraEnv is added to the environment of git.
	ExtraEnv []string
	// Removed lists paths, relative to the checkout, that were removed from the work tree on
	// purpose. Their files don't count as changes, and they are checked out again before the
	// update, so the caller has to remove them again afterwards.
	Removed []string
}

// FastForwardResult is what FastForward did.
type FastForwardResult struct {
	Status FastForwardStatus
	// Reason says why the checkout was skipped, as the end of "… was not updated because".
	Reason string
	// From and To are the abbreviated commits the checkout was moved between.
	From string
	To   string
}

// FastForward fetches the branch that dir has checked out and moves the checkout to it, like
// `git pull --ff-only`, but only when the checkout has no changes to tracked files and no
// commit the remote branch lacks. It never discards anything: in every other case the checkout
// is left as it is and the result says why.
func FastForward(ctx context.Context, dir string, options FastForwardOptions) (*FastForwardResult, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	// The checkout may belong to another user, e.g. the container's, which git refuses to use.
	// git wants forward slashes in safe.directory on Windows too
	baseArgs := []string{"-C", dir, "-c", "safe.directory=" + filepath.ToSlash(dir)}
	// not only fetching needs credentials: a partial clone downloads files when it checks them out
	if options.Helper != "" {
		baseArgs = append(baseArgs, "-c", "credential.helper="+options.Helper)
	}
	run := func(args ...string) (string, error) {
		return gitCommandOutput(ctx, options.ExtraEnv, append(append([]string{}, baseArgs...), args...)...)
	}
	fetch := func(args ...string) error {
		_, err := run(append([]string{"fetch", "--quiet"}, args...)...)
		return err
	}
	skipped := func(format string, args ...any) (*FastForwardResult, error) {
		return &FastForwardResult{Status: FastForwardSkipped, Reason: fmt.Sprintf(format, args...)}, nil
	}

	if bare, err := run("rev-parse", "--is-bare-repository"); err != nil {
		return nil, err
	} else if bare == "true" {
		return skipped("it is a bare repository")
	}
	head, err := run("rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return nil, err
	}
	branch, err := run("symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return skipped("it has no branch checked out")
	}

	target := "@{upstream}"
	targetName := ""
	if options.PR != "" && branch == GetBranchNameForPR(options.PR) {
		targetName = options.PR
		if err := fetch("origin", options.PR); err != nil {
			return nil, err
		}
		target = "FETCH_HEAD"
	} else {
		targetName, err = run("rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
		if err != nil {
			return skipped("branch %s has no upstream branch", branch)
		}
		remote, err := run("config", "--get", "branch."+branch+".remote")
		if err != nil {
			return nil, err
		}
		// the upstream of a branch can be another local branch
		if remote != "." {
			if err := fetch(remote); err != nil {
				return nil, err
			}
		}
	}

	targetCommit, err := run("rev-parse", "--verify", target+"^{commit}")
	if err != nil {
		return nil, err
	}
	if targetCommit == head {
		return &FastForwardResult{Status: FastForwardUpToDate}, nil
	}
	ahead, behind, err := aheadBehind(run, head, targetCommit)
	if err != nil {
		return nil, err
	}
	if behind == 0 {
		// only commits that were not pushed yet
		return &FastForwardResult{Status: FastForwardUpToDate}, nil
	}
	if ahead > 0 {
		return skipped("branch %s and %s have diverged: it has %d commit(s) that %s does not have", branch, targetName, ahead, targetName)
	}

	changed, removed, err := changedFiles(run, options.Removed)
	if err != nil {
		return nil, err
	}
	if len(changed) > 0 {
		return skipped("it has changes that are not committed, e.g. to %s", changed[0])
	}
	if len(removed) > 0 {
		// git would refuse to update a removed file that the remote branch changed
		if _, err := run(append([]string{"checkout", "--quiet", "HEAD", "--"}, removed...)...); err != nil {
			return nil, err
		}
	}

	if _, err := run("merge", "--quiet", "--ff-only", targetCommit); err != nil {
		return nil, err
	}
	return &FastForwardResult{
		Status: FastForwardUpdated,
		From:   abbreviate(run, head),
		To:     abbreviate(run, targetCommit),
	}, nil
}

// UpdateSubmodules checks out the commits of the submodules that the checkout in dir records,
// cloning the submodules it doesn't have yet.
func UpdateSubmodules(ctx context.Context, dir, helper string, extraEnv []string) error {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	args := []string{"-C", dir, "-c", "safe.directory=*"}
	if helper != "" {
		args = append(args, "-c", "credential.helper="+helper)
	}
	args = append(args, "submodule", "update", "--quiet", "--init", "--recursive")
	_, err = gitCommandOutput(ctx, extraEnv, args...)
	return err
}

// aheadBehind returns how many commits head has that target lacks, and the other way round.
func aheadBehind(run func(args ...string) (string, error), head, target string) (int, int, error) {
	out, err := run("rev-list", "--left-right", "--count", head+"..."+target)
	if err != nil {
		return 0, 0, err
	}
	counts := strings.Fields(out)
	if len(counts) != 2 {
		return 0, 0, fmt.Errorf("unexpected output of git rev-list: %q", out)
	}
	ahead, err := strconv.Atoi(counts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("unexpected output of git rev-list: %q", out)
	}
	behind, err := strconv.Atoi(counts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("unexpected output of git rev-list: %q", out)
	}
	return ahead, behind, nil
}

// changedFiles returns the tracked files that differ from HEAD, in the index or the work tree,
// and separately the paths of removedOnPurpose that have deleted files, which are not changes.
func changedFiles(run func(args ...string) (string, error), removedOnPurpose []string) (changed []string, removed []string, err error) {
	out, err := run("diff", "--name-status", "--no-renames", "-z", "HEAD")
	if err != nil {
		return nil, nil, err
	}

	roots := make([]string, 0, len(removedOnPurpose))
	for _, p := range removedOnPurpose {
		p = path.Clean(filepath.ToSlash(p))
		if p == "." || p == ".." || strings.HasPrefix(p, "../") || path.IsAbs(p) {
			continue
		}
		roots = append(roots, p)
	}

	seen := map[string]bool{}
	// with -z the output is a status and a path, each ended by a NUL
	fields := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		status, file := fields[i], fields[i+1]
		root := ""
		if status == "D" {
			for _, r := range roots {
				if file == r || strings.HasPrefix(file, r+"/") {
					root = r
					break
				}
			}
		}
		if root == "" {
			changed = append(changed, file)
		} else if !seen[root] {
			seen[root] = true
			removed = append(removed, root)
		}
	}
	return changed, removed, nil
}

func abbreviate(run func(args ...string) (string, error), commit string) string {
	short, err := run("rev-parse", "--short", commit)
	if err != nil || short == "" {
		return commit
	}
	return short
}

// gitCommandOutput runs git and returns its trimmed standard output. The error has what git wrote to
// standard error.
func gitCommandOutput(ctx context.Context, extraEnv []string, args ...string) (string, error) {
	cmd := CommandContext(ctx, extraEnv, args...)
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	out, err := cmd.Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", ErrNotInstalled
		}
		return "", fmt.Errorf("git %s: %s: %w", strings.Join(gitSubcommand(args), " "), strings.TrimSpace(stderr.String()), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// gitSubcommand returns args without the leading options of git itself, which would only make
// an error message harder to read (and may hold a credential helper).
func gitSubcommand(args []string) []string {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-C", "-c":
			i++
		default:
			return args[i:]
		}
	}
	return nil
}
