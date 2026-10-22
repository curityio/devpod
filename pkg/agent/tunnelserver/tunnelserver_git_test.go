package tunnelserver

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/loft-sh/devpod/pkg/devcontainer/config"
	"github.com/loft-sh/devpod/pkg/log"
	"gotest.tools/assert"
	is "gotest.tools/assert/cmp"
)

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, string(out))
}

// newGitSetupInfo returns a setup result whose workspace folder is a git
// repository with ignored files, and an ignore file with the given content.
func newGitSetupInfo(t *testing.T, ignore string) *config.Result {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	emptyConfig := filepath.Join(t.TempDir(), "gitconfig")
	assert.NilError(t, os.WriteFile(emptyConfig, nil, 0o600))
	t.Setenv("GIT_CONFIG_GLOBAL", emptyConfig)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	workspaceFolder := t.TempDir()
	runGit(t, workspaceFolder, "init", "-q", "-b", "main")
	writeFiles(t, workspaceFolder, srcMainGo, ".gitignore")
	assert.NilError(t, os.WriteFile(
		filepath.Join(workspaceFolder, ".gitignore"), []byte("node_modules/\n.env\nbuild/\n"), 0o600))
	runGit(t, workspaceFolder, "add", "-A")
	runGit(t, workspaceFolder, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "-m", "init")
	writeFiles(t, workspaceFolder,
		"new.txt",
		".env",
		"build/out.bin",
		"web/node_modules/lib/index.js",
	)
	if ignore != "" {
		assert.NilError(t, os.WriteFile(
			filepath.Join(workspaceFolder, ignoreFileName), []byte(ignore), 0o600))
	}

	return &config.Result{
		SubstitutionContext: &config.SubstitutionContext{
			WorkspaceMount: "type=bind,src=" + workspaceFolder + ",dst=/workspaces/project",
		},
		MergedConfig: &config.MergedDevContainerConfig{},
	}
}

// split returns the entries in .git and the others.
func split(entries []string) ([]string, []string) {
	var gitEntries, others []string
	for _, entry := range entries {
		if strings.HasPrefix(entry, gitFolder+"/") {
			gitEntries = append(gitEntries, entry)
		} else {
			others = append(others, entry)
		}
	}
	return gitEntries, others
}

func TestStreamMount_GitWorkspaceUploadsWhatGitDoesNotIgnore(t *testing.T) {
	setupInfo := newGitSetupInfo(t, "")
	server := newSetupServer(setupInfo, log.Discard)

	gitEntries, others := split(streamMountEntries(t, server, config.GetWorkspaceMount(setupInfo)))

	assert.DeepEqual(t, []string{".gitignore", "new.txt", srcMainGo}, others)
	// a shallow copy of .git
	assert.Assert(t, is.Contains(gitEntries, ".git/HEAD"))
	assert.Assert(t, is.Contains(gitEntries, ".git/index"))
	assert.Assert(t, is.Contains(gitEntries, ".git/shallow"))
}

func TestStreamMount_IgnoreFileTakesPrecedenceOverGit(t *testing.T) {
	setupInfo := newGitSetupInfo(t, "new.txt\n!.env\n!build/\nbuild/*.tmp\n")
	writeFiles(t, config.GetWorkspaceMount(setupInfo).Source, "build/out.tmp")
	server := newSetupServer(setupInfo, log.Discard)

	_, others := split(streamMountEntries(t, server, config.GetWorkspaceMount(setupInfo)))

	assert.DeepEqual(t, []string{ignoreFileName, ".env", ".gitignore", "build/out.bin", srcMainGo}, others)
}

func TestStreamMount_IgnoreFileExcludesGitFolder(t *testing.T) {
	setupInfo := newGitSetupInfo(t, ".git\n")
	server := newSetupServer(setupInfo, log.Discard)

	gitEntries, _ := split(streamMountEntries(t, server, config.GetWorkspaceMount(setupInfo)))

	assert.Equal(t, len(gitEntries), 0)
}

func TestStreamMount_IgnoreFileIncludesWholeGitFolder(t *testing.T) {
	setupInfo := newGitSetupInfo(t, "!.git\n")
	server := newSetupServer(setupInfo, log.Discard)

	gitEntries, _ := split(streamMountEntries(t, server, config.GetWorkspaceMount(setupInfo)))

	assert.Assert(t, is.Contains(gitEntries, ".git/HEAD"))
	assert.Assert(t, !slices.Contains(gitEntries, ".git/shallow"))
}

func TestStreamMount_GitSubfolderHasNoGitFolder(t *testing.T) {
	setupInfo := newGitSetupInfo(t, "")
	workspaceMount := config.GetWorkspaceMount(setupInfo)
	setupInfo.SubstitutionContext.WorkspaceMount = "type=bind,src=" + filepath.Join(workspaceMount.Source, "src") + ",dst=/workspaces/project"
	server := newSetupServer(setupInfo, log.Discard)

	entries := streamMountEntries(t, server, config.GetWorkspaceMount(setupInfo))

	assert.DeepEqual(t, []string{"main.go"}, entries)
}
