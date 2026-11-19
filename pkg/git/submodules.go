package git

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// SubmodulesStatus says what UpdateSubmodules did with the submodules of a checkout.
type SubmodulesStatus int

const (
	// SubmodulesUpToDate means all submodules are at the commits the checkout records.
	SubmodulesUpToDate SubmodulesStatus = iota
	// SubmodulesUpdated means submodules were cloned or moved to the commits the checkout records.
	SubmodulesUpdated
	// SubmodulesSkipped means submodules are not at the commits the checkout records, but were
	// left as they were, because updating them could lose something in one of them.
	SubmodulesSkipped
)

// SubmodulesOptions are the options of UpdateSubmodules.
type SubmodulesOptions struct {
	// Helper is the credential helper to fetch with. It is not saved in the repository.
	Helper string
	// ExtraEnv is added to the environment of git.
	ExtraEnv []string
	// Removed lists paths, relative to the checkout, that were removed from the work tree on
	// purpose. Submodules in them are not cloned again.
	Removed []string
}

// SubmodulesResult is what UpdateSubmodules did.
type SubmodulesResult struct {
	Status SubmodulesStatus
	// Reason says why the submodules were skipped, as the end of "… were not updated because".
	Reason string
}

// UpdateSubmodules brings the submodules of the checkout in dir, and theirs, to the commits the
// checkout records, cloning those it doesn't have yet, like `git submodule update --init
// --recursive`. It only does so when no submodule can lose anything: a submodule that is at
// another commit must have no changes to tracked files and no branch checked out, as git leaves
// it, since work in a submodule is done on a branch. Otherwise all submodules stay as they are
// and the result says why.
//
// It is safe to call when nothing changed, which makes up for an update that failed before.
func UpdateSubmodules(ctx context.Context, dir string, options SubmodulesOptions) (*SubmodulesResult, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	// a submodule is a repository of its own, which may belong to another user as well
	baseArgs := []string{"-c", "safe.directory=*"}
	if options.Helper != "" {
		baseArgs = append(baseArgs, "-c", "credential.helper="+options.Helper)
	}
	run := func(in string, args ...string) (string, error) {
		return gitCommandOutput(ctx, options.ExtraEnv, append(append([]string{"-C", in}, baseArgs...), args...)...)
	}

	roots := removedRoots(options.Removed)
	all, err := submodules(func(args ...string) (string, error) { return run(dir, args...) }, true)
	if err != nil {
		return nil, err
	}
	pending := false
	for _, submodule := range all {
		if rootOf(submodule.path, roots) != "" {
			continue
		}
		folder := filepath.Join(dir, filepath.FromSlash(submodule.path))
		switch submodule.state {
		case submoduleCurrent:
			continue
		case submoduleConflict:
			return &SubmodulesResult{Status: SubmodulesSkipped, Reason: fmt.Sprintf("submodule %s has merge conflicts", submodule.path)}, nil
		case submoduleOtherCommit:
			if _, err := run(folder, "symbolic-ref", "--quiet", "HEAD"); err == nil {
				return &SubmodulesResult{Status: SubmodulesSkipped, Reason: fmt.Sprintf("submodule %s has a branch checked out, at another commit than the one recorded for it", submodule.path)}, nil
			}
			changes, err := run(folder, "status", "--porcelain", "--untracked-files=no", "--ignore-submodules=none")
			if err != nil {
				return nil, err
			} else if changes != "" {
				return &SubmodulesResult{Status: SubmodulesSkipped, Reason: fmt.Sprintf("submodule %s has changes that are not committed", submodule.path)}, nil
			}
		case submoduleMissing:
		}
		pending = true
	}
	if !pending {
		return &SubmodulesResult{Status: SubmodulesUpToDate}, nil
	}

	args := []string{"submodule", "update", "--quiet", "--init", "--recursive"}
	if len(roots) > 0 {
		// name the submodules to update, as git has no way to leave some out
		top, err := submodules(func(args ...string) (string, error) { return run(dir, args...) }, false)
		if err != nil {
			return nil, err
		}
		args = append(args, "--")
		for _, submodule := range top {
			if rootOf(submodule.path, roots) == "" {
				args = append(args, submodule.path)
			}
		}
	}
	if _, err := run(dir, args...); err != nil {
		return nil, err
	}
	return &SubmodulesResult{Status: SubmodulesUpdated}, nil
}

// submoduleState is the first character of a line of `git submodule status`.
type submoduleState byte

const (
	submoduleCurrent     submoduleState = ' '
	submoduleMissing     submoduleState = '-'
	submoduleOtherCommit submoduleState = '+'
	submoduleConflict    submoduleState = 'U'
)

type submodule struct {
	state submoduleState
	// path is relative to the checkout, with slashes
	path string
}

// submodules returns the submodules of the checkout, and with recursive also theirs.
func submodules(run func(args ...string) (string, error), recursive bool) ([]submodule, error) {
	args := []string{"submodule", "status"}
	if recursive {
		args = append(args, "--recursive")
	}
	// gitCommandOutput trims the output, which would take the state of the first line with it
	out, err := run(append([]string{"-c", "core.quotePath=false"}, args...)...)
	if err != nil {
		return nil, err
	}

	result := []submodule{}
	for i, line := range strings.Split(out, "\n") {
		if i == 0 && !strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "U") {
			line = " " + line
		}
		if len(line) < 2 {
			continue
		}
		// a line is "<state><commit> <path>" and, unless the submodule is missing, " (<describe>)"
		_, rest, found := strings.Cut(line[1:], " ")
		if !found {
			continue
		}
		if end := strings.LastIndex(rest, " ("); end != -1 && strings.HasSuffix(rest, ")") {
			rest = rest[:end]
		}
		result = append(result, submodule{state: submoduleState(line[0]), path: rest})
	}
	return result, nil
}
