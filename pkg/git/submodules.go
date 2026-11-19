package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// SubmodulesOptions are the options of UpdateSubmodules.
type SubmodulesOptions struct {
	// Init makes UpdateSubmodules clone the submodules the checkout doesn't have yet, also when
	// it has none of its submodules.
	Init bool
	// Helper is the credential helper to fetch with. It is not saved in the repository.
	Helper string
	// ExtraEnv is added to the environment of git.
	ExtraEnv []string
	// Removed lists paths, relative to the checkout, that were removed from the work tree on
	// purpose. Submodules in them are left alone.
	Removed []string
}

// SubmodulesResult is what UpdateSubmodules did.
type SubmodulesResult struct {
	// Updated are the paths of the submodules that were cloned or moved to another commit.
	Updated []string
	// Skipped are the submodules that are not at the commit recorded for them and were left as
	// they were.
	Skipped []SkippedSubmodule
}

// SkippedSubmodule is a submodule that UpdateSubmodules did not update.
type SkippedSubmodule struct {
	Path string
	// Reason says why, as the end of "… was not updated because".
	Reason string
}

// UpdateSubmodules brings the submodules of the checkout in dir, and theirs, to the commits that
// are recorded for them, like `git submodule update --recursive`, but only where that can't
// lose anything. A submodule that is at another commit is moved if it has no branch checked out,
// as git leaves it, since work in a submodule is done on a branch, and no changes to tracked
// files. Git refuses to move it over files it doesn't track, also those it ignores. A submodule
// that can't be moved is left as it is, with those in it, and the result says why.
//
// Submodules the checkout doesn't have yet are cloned if it has others, or with the Init option.
//
// It is safe to call when nothing changed, which makes up for an update that failed before.
func UpdateSubmodules(ctx context.Context, dir string, options SubmodulesOptions) (*SubmodulesResult, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	// a submodule is a repository of its own, which may belong to another user as well
	baseArgs := []string{"-c", "safe.directory=*", "-c", "core.quotePath=false"}
	if options.Helper != "" {
		baseArgs = append(baseArgs, "-c", "credential.helper="+options.Helper)
	}
	updater := &submoduleUpdater{
		run: func(in string, args ...string) (string, error) {
			return gitCommandOutput(ctx, options.ExtraEnv, append(append([]string{"-C", in}, baseArgs...), args...)...)
		},
		removed: removedRoots(options.Removed),
		result:  &SubmodulesResult{},
	}

	top, err := updater.submodules(dir)
	if err != nil {
		return nil, err
	}
	updater.init = options.Init
	for _, submodule := range top {
		if submodule.state != submoduleMissing && rootOf(submodule.path, updater.removed) == "" {
			// the checkout was cloned with its submodules
			updater.init = true
		}
	}

	updater.update(dir, "", top)
	return updater.result, nil
}

type submoduleUpdater struct {
	// run runs git in a folder
	run     func(in string, args ...string) (string, error)
	removed []string
	init    bool
	result  *SubmodulesResult
}

// update updates the submodules of the repository in repository, whose path in the checkout is
// prefix, and theirs.
func (u *submoduleUpdater) update(repository, prefix string, submodules []submodule) {
	for _, submodule := range submodules {
		path := prefix + submodule.path
		if rootOf(path, u.removed) != "" {
			continue
		}
		folder := filepath.Join(repository, filepath.FromSlash(submodule.path))

		switch submodule.state {
		case submoduleCurrent:
		case submoduleConflict:
			u.skip(path, "it has merge conflicts")
			continue
		case submoduleMissing:
			if !u.init {
				continue
			}
			// git checks the submodule out over the files of a folder that is not a repository
			if entries, err := os.ReadDir(folder); err == nil && len(entries) > 0 {
				u.skip(path, "its folder has files, but is not checked out")
				continue
			}
			if _, err := u.run(repository, "submodule", "update", "--quiet", "--init", "--", submodule.path); err != nil {
				u.skip(path, err.Error())
				continue
			}
			u.result.Updated = append(u.result.Updated, path)
		case submoduleOtherCommit:
			if reason := u.move(repository, folder, submodule.path); reason != "" {
				u.skip(path, reason)
				continue
			}
			u.result.Updated = append(u.result.Updated, path)
		}

		// most submodules have none of their own
		if _, err := os.Stat(filepath.Join(folder, ".gitmodules")); err != nil {
			continue
		}
		nested, err := u.submodules(folder)
		if err != nil {
			u.skip(path, "its submodules can't be listed: "+err.Error())
			continue
		}
		u.update(folder, path+"/", nested)
	}
}

// move checks out the commit that repository records for its submodule in folder, and returns
// why it didn't if it didn't.
func (u *submoduleUpdater) move(repository, folder, path string) string {
	if _, err := u.run(folder, "symbolic-ref", "--quiet", "HEAD"); err == nil {
		return "it has a branch checked out, at another commit than the one recorded for it"
	}
	changes, err := u.run(folder, "status", "--porcelain", "--untracked-files=no", "--ignore-submodules=untracked")
	if err != nil {
		return err.Error()
	} else if changes != "" {
		return "it has changes that are not committed"
	}

	commit, err := u.run(repository, "rev-parse", "--verify", ":"+path)
	if err != nil {
		return err.Error()
	}
	if _, err := u.run(folder, "cat-file", "-e", commit+"^{commit}"); err != nil {
		// the commit of a branch, or one the remote gives out by its hash
		if _, err := u.run(folder, "fetch", "--quiet", "--no-recurse-submodules", "origin"); err != nil {
			return err.Error()
		}
		if _, err := u.run(folder, "cat-file", "-e", commit+"^{commit}"); err != nil {
			if _, err := u.run(folder, "fetch", "--quiet", "--no-recurse-submodules", "origin", commit); err != nil {
				return err.Error()
			}
		}
	}

	// by default git replaces a file it ignores with a file of the commit
	if _, err := u.run(folder, "checkout", "--quiet", "--no-overwrite-ignore", "--detach", commit); err != nil {
		return err.Error()
	}
	return ""
}

func (u *submoduleUpdater) skip(path, reason string) {
	u.result.Skipped = append(u.result.Skipped, SkippedSubmodule{Path: path, Reason: reason})
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
	// path is relative to the repository the submodule is of, with slashes
	path string
}

// submodules returns the submodules of the repository, without theirs.
func (u *submoduleUpdater) submodules(repository string) ([]submodule, error) {
	out, err := u.run(repository, "submodule", "status")
	if err != nil {
		return nil, err
	}

	result := []submodule{}
	for i, line := range strings.Split(out, "\n") {
		// the output is trimmed, which takes the state of the first line with it if it is a space
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
