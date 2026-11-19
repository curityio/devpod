package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// HeadCommit returns the commit that the repository in dir has checked out.
func HeadCommit(ctx context.Context, dir string) (string, error) {
	return gitCommandOutput(ctx, nil, "-C", dir, "rev-parse", "--verify", "HEAD^{commit}")
}

// ReadHeadCommit returns the commit that the repository in dir has checked out, as its files say,
// for where git may not be installed. It returns an empty string if it can't tell: dir is not
// the top folder of a repository, the repository has no commit, or it stores its references in
// another way than files.
func ReadHeadCommit(dir string) string {
	gitDir := filepath.Join(dir, ".git")
	head, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}

	value := strings.TrimSpace(string(head))
	ref, isRef := strings.CutPrefix(value, "ref:")
	if !isRef {
		// a detached HEAD
		return commitHash(value)
	}
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, "refs/") || !filepath.IsLocal(filepath.FromSlash(ref)) {
		return ""
	}

	loose, err := os.ReadFile(filepath.Join(gitDir, filepath.FromSlash(ref)))
	if err == nil {
		return commitHash(strings.TrimSpace(string(loose)))
	}

	packed, err := os.ReadFile(filepath.Join(gitDir, "packed-refs"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(packed), "\n") {
		// lines are "<hash> <reference>", besides a header (#) and the commits of tags (^)
		hash, name, found := strings.Cut(strings.TrimSpace(line), " ")
		if found && name == ref {
			return commitHash(hash)
		}
	}
	return ""
}

// commitHash returns value if it is a SHA-1 or SHA-256 hash, and an empty string otherwise.
func commitHash(value string) string {
	if len(value) != 40 && len(value) != 64 {
		return ""
	}
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return ""
		}
	}
	return value
}
