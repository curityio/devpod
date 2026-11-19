package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/curityio/devpod/pkg/command"
	"github.com/curityio/devpod/pkg/config"
	"github.com/curityio/devpod/pkg/git"
	"github.com/curityio/devpod/pkg/gitcredentials"
	"github.com/curityio/devpod/pkg/log"
	provider2 "github.com/curityio/devpod/pkg/provider"
	"github.com/curityio/devpod/pkg/util"
	"github.com/moby/patternmatcher/ignorefile"
)

var extraSearchLocations = []string{"/home/devpod/.devpod/agent", "/opt/devpod/agent", "/var/lib/devpod/agent", "/var/devpod/agent"}

var ErrFindAgentHomeFolder = fmt.Errorf("couldn't find devpod home directory")

func GetAgentDaemonLogFolder(agentFolder string) (string, error) {
	return FindAgentHomeFolder(agentFolder)
}

func findDir(agentFolder string, validate func(path string) bool) string {
	// get agent folder
	if agentFolder != "" {
		if !validate(agentFolder) {
			return ""
		}

		return agentFolder
	}

	// check environment
	homeFolder := os.Getenv(config.DEVPOD_HOME)
	if homeFolder != "" {
		homeFolder = filepath.Join(homeFolder, "agent")
		if !validate(homeFolder) {
			return ""
		}

		return homeFolder
	}

	// check home folder first
	homeDir, _ := util.UserHomeDir()
	if homeDir != "" {
		homeDir = filepath.Join(homeDir, ".devpod", "agent")
		if validate(homeDir) {
			return homeDir
		}
	}

	// check root folder
	homeDir, _ = command.GetHome("root")
	if homeDir != "" {
		homeDir = filepath.Join(homeDir, ".devpod", "agent")
		if validate(homeDir) {
			return homeDir
		}
	}

	// check current directory
	execDir, _ := os.Executable()
	if execDir != "" {
		execDir = filepath.Join(filepath.Dir(execDir), "agent")
		if validate(execDir) {
			return execDir
		}
	}

	// check other folders
	for _, dir := range extraSearchLocations {
		if validate(dir) {
			return dir
		}
	}

	return ""
}

func FindAgentHomeFolder(agentFolder string) (string, error) {
	homeDir := findDir(agentFolder, isDevPodHome)
	if homeDir != "" {
		return homeDir, nil
	}

	return "", ErrFindAgentHomeFolder
}

func isDevPodHome(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "contexts"))
	return err == nil
}

func PrepareAgentHomeFolder(agentFolder string) (string, error) {
	// try to find agent home folder first
	homeFolder, err := FindAgentHomeFolder(agentFolder)
	if err == nil {
		return homeFolder, nil
	}

	// try to find an executable directory
	homeDir := findDir(agentFolder, func(path string) bool {
		ok, _ := isDirExecutable(path)
		return ok
	})
	if homeDir != "" {
		return homeDir, nil
	}

	// check if agentFolder is set
	if agentFolder != "" {
		_, err := isDirExecutable(agentFolder)
		return "", err
	}

	// return generic error here
	return "", fmt.Errorf("couldn't find an executable directory")
}

func isDirExecutable(dir string) (bool, error) {
	if !filepath.IsAbs(dir) {
		var err error
		dir, err = filepath.Abs(dir)
		if err != nil {
			return false, err
		}
	}

	err := os.MkdirAll(dir, 0777)
	if err != nil {
		return false, err
	}

	testFile := filepath.Join(dir, "devpod_test.sh")
	err = os.WriteFile(testFile, []byte(`#!/bin/sh
echo DevPod
`), 0755)
	if err != nil {
		return false, err
	}
	defer os.Remove(testFile)
	if runtime.GOOS != "linux" {
		return true, nil
	}

	// try to execute
	out, err := exec.Command(testFile).Output()
	if err != nil {
		return false, err
	} else if strings.TrimSpace(string(out)) != "DevPod" {
		return false, fmt.Errorf("received %s, expected DevPod", strings.TrimSpace(string(out)))
	}

	return true, nil
}

func GetAgentWorkspaceContentDir(workspaceDir string) string {
	return filepath.Join(workspaceDir, "content")
}

func GetAgentBinariesDirFromWorkspaceDir(workspaceDir string) (string, error) {
	// check if it already exists
	_, err := os.Stat(workspaceDir)
	if err == nil {
		return filepath.Join(workspaceDir, "binaries"), nil
	}

	return "", os.ErrNotExist
}

func GetAgentBinariesDir(agentFolder, context, workspaceID string) (string, error) {
	homeFolder, err := FindAgentHomeFolder(agentFolder)
	if err != nil {
		return "", err
	}
	if context == "" {
		context = config.DefaultContext
	}

	// workspace folder
	workspaceDir := filepath.Join(homeFolder, "contexts", context, "workspaces", workspaceID)

	// get from workspace folder
	return GetAgentBinariesDirFromWorkspaceDir(workspaceDir)
}

func GetAgentWorkspaceDir(agentFolder, context, workspaceID string) (string, error) {
	homeFolder, err := FindAgentHomeFolder(agentFolder)
	if err != nil {
		return "", err
	}
	if context == "" {
		context = config.DefaultContext
	}

	// workspace folder
	workspaceDir := filepath.Join(homeFolder, "contexts", context, "workspaces", workspaceID)

	// check if it already exists
	_, err = os.Stat(workspaceDir)
	if err == nil {
		return workspaceDir, nil
	}

	return "", os.ErrNotExist
}

func CreateAgentWorkspaceDir(agentFolder, context, workspaceID string) (string, error) {
	homeFolder, err := PrepareAgentHomeFolder(agentFolder)
	if err != nil {
		return "", err
	}

	// workspace folder
	workspaceDir := filepath.Join(homeFolder, "contexts", context, "workspaces", workspaceID)

	// create workspace folder
	err = os.MkdirAll(workspaceDir, 0755)
	if err != nil {
		return "", err
	}

	return workspaceDir, nil
}

func CloneRepositoryForWorkspace(
	ctx context.Context,
	source *provider2.WorkspaceSource,
	agentConfig *provider2.ProviderAgentConfig,
	workspaceDir, helper string,
	options provider2.CLIOptions,
	log log.Logger,
) error {
	log.Info("Clone repository")
	log.Infof("URL: %s\n", source.GitRepository)
	if source.GitBranch != "" {
		log.Infof("Branch: %s\n", source.GitBranch)
	}
	if source.GitCommit != "" {
		log.Infof("Commit: %s\n", source.GitCommit)
	}
	if source.GitSubPath != "" {
		log.Infof("Subpath: %s\n", source.GitSubPath)
	}
	if source.GitPRReference != "" {
		log.Infof("PR: %s\n", source.GitPRReference)
	}

	// remove the credential helper or otherwise we will receive strange errors within the container
	defer func() {
		if helper != "" {
			if err := gitcredentials.RemoveHelperFromPath(gitcredentials.GetLocalGitConfigPath(workspaceDir)); err != nil {
				log.Errorf("Remove git credential helper: %v", err)
			}
		}
	}()

	// check if command exists
	if !command.Exists("git") {
		local, _ := agentConfig.Local.Bool()
		if local {
			return fmt.Errorf("seems like git isn't installed on your system. Please make sure to install git and make it available in the PATH")
		}
		if err := git.InstallBinary(log); err != nil {
			return err
		}
	}

	// run git command
	gitInfo := git.NewGitInfo(source.GitRepository, source.GitBranch, source.GitCommit, source.GitPRReference, source.GitSubPath)

	err := git.CloneRepositoryWithEnv(ctx, gitInfo, nil, workspaceDir, helper, options.StrictHostKeyChecking, log, getGitOptions(options)...)
	if err != nil {
		// cleanup workspace dir if clone failed, otherwise we won't try to clone again when rebuilding this workspace
		if cleanupErr := cleanupWorkspaceDir(workspaceDir); cleanupErr != nil {
			return fmt.Errorf("clone repository: %w, cleanup workspace: %w", err, cleanupErr)
		}
		return fmt.Errorf("clone repository: %w", err)
	}

	log.Done("Successfully cloned repository")

	removeIgnoredFiles(workspaceDir, log)
	return nil
}

// UpdateRepositoryForWorkspace brings the checkout of a workspace that is recreated up to date with its remote
// branch, when that can't lose anything in it: it fetches the branch and fast-forwards the checkout if it has no
// changes to tracked files and no commit the remote branch lacks. Otherwise the checkout stays as it is and a
// warning says why. It never fails, as recreating a workspace also has to work without the remote.
func UpdateRepositoryForWorkspace(
	ctx context.Context,
	source *provider2.WorkspaceSource,
	workspaceDir, helper string,
	options provider2.CLIOptions,
	log log.Logger,
) {
	if source.GitCommit != "" {
		log.Infof("The workspace uses commit %s, keeping its checkout", source.GitCommit)
		return
	}
	if !command.Exists("git") {
		log.Warn("The workspace's checkout was not updated because git is not installed. " + recreateKeepsCheckoutHint)
		return
	}

	log.Infof("Fetch %s to update the workspace's checkout", source.GitRepository)
	result, err := git.FastForward(ctx, workspaceDir, git.FastForwardOptions{
		PR:       source.GitPRReference,
		Helper:   helper,
		ExtraEnv: git.GetDefaultExtraEnv(options.StrictHostKeyChecking),
		// DevPod removed these from the checkout itself when it cloned the repository
		Removed: readIgnoredFiles(workspaceDir, log),
	})
	if err != nil {
		log.Warnf("The workspace's checkout was not updated: %v. %s", err, recreateKeepsCheckoutHint)
		return
	}

	switch result.Status {
	case git.FastForwardUpdated:
		log.Donef("Updated the workspace's checkout from commit %s to %s", result.From, result.To)
		if options.GitCloneRecursiveSubmodules {
			err = git.UpdateSubmodules(ctx, workspaceDir, helper, git.GetDefaultExtraEnv(options.StrictHostKeyChecking))
			if err != nil {
				log.Warnf("The submodules of the workspace's checkout were not updated: %v", err)
			}
		}
	case git.FastForwardSkipped:
		log.Warnf("The workspace's checkout was not updated because %s. %s", result.Reason, recreateKeepsCheckoutHint)
	case git.FastForwardNotApplicable:
		log.Infof("The workspace's checkout was not updated because %s", result.Reason)
	case git.FastForwardUpToDate:
		log.Info("The workspace's checkout is up to date with its remote branch")
	}
}

// recreateKeepsCheckoutHint says what happens to a checkout that was not updated, and what to do about it.
const recreateKeepsCheckoutHint = "The workspace is recreated with the files it has. " +
	"To update them, run `git pull` in the workspace and recreate it again, " +
	"or use --reset to replace them with a new clone, which discards what was not pushed"

// ignoreFileName is the name of the file listing the paths DevPod removes from a cloned repository.
const ignoreFileName = ".devpodignore"

// readIgnoredFiles returns the paths the ignore file of the checkout in workspaceDir lists.
func readIgnoredFiles(workspaceDir string, log log.Logger) []string {
	f, err := os.Open(filepath.Join(workspaceDir, ignoreFileName))
	if err != nil {
		return nil
	}
	defer f.Close()

	excludes, err := ignorefile.ReadAll(f)
	if err != nil {
		log.Warn(ignoreFileName+" file is invalid : ", err)
		return nil
	}
	return excludes
}

// removeIgnoredFiles removes the paths the ignore file lists from the checkout in workspaceDir.
func removeIgnoredFiles(workspaceDir string, log log.Logger) {
	excludes := readIgnoredFiles(workspaceDir, log)
	if len(excludes) == 0 {
		return
	}
	for _, exclude := range excludes {
		_ = os.RemoveAll(filepath.Join(workspaceDir, exclude))
	}
	log.Debug("Ignore files from "+ignoreFileName+" ", excludes)
}

func getGitOptions(options provider2.CLIOptions) []git.Option {
	gitOpts := []git.Option{}
	if options.GitCloneStrategy != "" {
		gitOpts = append(gitOpts, git.WithCloneStrategy(options.GitCloneStrategy))
	}
	if options.GitCloneRecursiveSubmodules {
		gitOpts = append(gitOpts, git.WithRecursiveSubmodules())
	}
	return gitOpts
}

func cleanupWorkspaceDir(workspaceDir string) error {
	return os.RemoveAll(workspaceDir)
}
