//go:build !windows

package container

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/curityio/devpod/cmd/flags"
	v1 "github.com/google/go-containerregistry/pkg/v1"

	"github.com/curityio/devpod/pkg/agent"
	"github.com/curityio/devpod/pkg/agent/tunnel"
	"github.com/curityio/devpod/pkg/agent/tunnelserver"
	"github.com/curityio/devpod/pkg/command"
	"github.com/curityio/devpod/pkg/compress"
	config2 "github.com/curityio/devpod/pkg/config"
	"github.com/curityio/devpod/pkg/copy"
	"github.com/curityio/devpod/pkg/credentials"
	"github.com/curityio/devpod/pkg/devcontainer/config"
	"github.com/curityio/devpod/pkg/devcontainer/setup"
	"github.com/curityio/devpod/pkg/dockercredentials"
	"github.com/curityio/devpod/pkg/envfile"
	"github.com/curityio/devpod/pkg/extract"
	"github.com/curityio/devpod/pkg/git"
	"github.com/curityio/devpod/pkg/ide/jetbrains"
	"github.com/curityio/devpod/pkg/ide/jupyter"
	"github.com/curityio/devpod/pkg/ide/openvscode"
	"github.com/curityio/devpod/pkg/ide/rstudio"
	"github.com/curityio/devpod/pkg/ide/vscode"
	"github.com/curityio/devpod/pkg/log"
	provider2 "github.com/curityio/devpod/pkg/provider"
	"github.com/curityio/devpod/pkg/single"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var DockerlessImageConfigOutput = "/.dockerless/image.json"

// SetupContainerCmd holds the cmd flags
type SetupContainerCmd struct {
	*flags.GlobalFlags

	ChownWorkspace         bool
	StreamMounts           bool
	InjectGitCredentials   bool
	ContainerWorkspaceInfo string
	SetupInfo              string
}

// NewSetupContainerCmd creates a new command
func NewSetupContainerCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &SetupContainerCmd{
		GlobalFlags: flags,
	}
	setupContainerCmd := &cobra.Command{
		Use:   "setup",
		Short: "Sets up a container",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			return cmd.Run(context.Background())
		},
	}
	setupContainerCmd.Flags().BoolVar(&cmd.StreamMounts, "stream-mounts", false, "If true, will try to stream the bind mounts from the host")
	setupContainerCmd.Flags().BoolVar(&cmd.ChownWorkspace, "chown-workspace", false, "If DevPod should chown the workspace to the remote user")
	setupContainerCmd.Flags().BoolVar(&cmd.InjectGitCredentials, "inject-git-credentials", false, "If DevPod should inject git credentials during setup")
	setupContainerCmd.Flags().StringVar(&cmd.ContainerWorkspaceInfo, "container-workspace-info", "", "The container workspace info")
	setupContainerCmd.Flags().StringVar(&cmd.SetupInfo, "setup-info", "", "The container setup info")
	_ = setupContainerCmd.MarkFlagRequired("setup-info")
	return setupContainerCmd
}

// Run runs the command logic
func (cmd *SetupContainerCmd) Run(ctx context.Context) error {
	// create a grpc client
	tunnelClient, err := tunnelserver.NewTunnelClient(os.Stdin, os.Stdout, true, 0)
	if err != nil {
		return fmt.Errorf("error creating tunnel client: %w", err)
	}

	// create debug logger
	logger := tunnelserver.NewTunnelLogger(ctx, tunnelClient, cmd.Debug)
	logger.Debugf("Created logger")

	// this message serves as a ping to the client
	_, err = tunnelClient.Ping(ctx, &tunnel.Empty{})
	if err != nil {
		return errors.Wrap(err, "ping client")
	}

	// start setting up container
	logger.Debugf("Start setting up container...")
	workspaceInfo, _, err := agent.DecodeContainerWorkspaceInfo(cmd.ContainerWorkspaceInfo)
	if err != nil {
		return err
	}

	decompressed, err := compress.Decompress(cmd.SetupInfo)
	if err != nil {
		return err
	}

	setupInfo := &config.Result{}
	err = json.Unmarshal([]byte(decompressed), setupInfo)
	if err != nil {
		return err
	}

	// sync mounts
	if cmd.StreamMounts {
		mounts := config.GetMounts(setupInfo)
		logger.Debug("Syncing mounts... ", mounts)
		for i, m := range mounts {
			// the first mount is the workspace folder
			switch copyOfMount(workspaceInfo.CLIOptions, i == 0, hasFiles(m.Target)) {
			case mountKeep:
				logger.Debug("Skip stream mount ", m.Target, " because it's not empty")
			case mountReplace:
				logger.Infof("Copy %s into DevContainer %s", m.Source, m.Target)
				err = streamMount(ctx, m, tunnelClient, logger)
			case mountRefresh:
				logger.Infof("Copy %s into DevContainer %s again, keeping the files that were changed there since", m.Source, m.Target)
				err = streamMount(ctx, m, tunnelClient, logger, extract.KeepNewer())
			}
			if err != nil {
				return err
			}
		}

		if workspaceInfo.CLIOptions.Recreate && !workspaceInfo.CLIOptions.Reset {
			warnIfWorkspaceFolderDiffers(workspaceInfo.ContentCommit, config.GetWorkspaceMount(setupInfo).Target, logger)
		}
	}

	// do dockerless build
	err = dockerlessBuild(ctx, setupInfo, workspaceInfo, tunnelClient, cmd.Debug, logger)
	if err != nil {
		return fmt.Errorf("dockerless build: %w", err)
	}

	// fill container env
	err = fillContainerEnv(setupInfo)
	if err != nil {
		return err
	}

	if cmd.InjectGitCredentials {
		// configure git credentials
		cancelCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		cleanupFunc, err := configureSystemGitCredentials(cancelCtx, cancel, tunnelClient, logger)
		if err != nil {
			logger.Errorf("Error configuring git credentials: %v", err)
		} else {
			defer cleanupFunc()
		}
	}

	// make git use https instead of ssh before any lifecycle hook runs
	err = configureGitSSHToHTTPS(ctx, setupInfo, workspaceInfo, logger)
	if err != nil {
		logger.Errorf("Error configuring git to use https instead of ssh: %v", err)
	}

	// the workspace folder isn't chowned for docker on macOS and Windows, so git would refuse to use the repository
	if command.Exists("git") {
		err = git.AddSafeDirectory(ctx, setupInfo.SubstitutionContext.ContainerWorkspaceFolder)
		if err != nil {
			logger.Errorf("Error adding the workspace folder to git's safe.directory: %v", err)
		}
	}

	// setup container
	err = setup.SetupContainer(ctx, setupInfo, workspaceInfo.CLIOptions.WorkspaceEnv, cmd.ChownWorkspace, logger)
	if err != nil {
		return err
	}

	// install IDE
	err = cmd.installIDE(setupInfo, &workspaceInfo.IDE, logger)
	if err != nil {
		return err
	}

	// start container daemon if necessary
	if !workspaceInfo.CLIOptions.DisableDaemon && workspaceInfo.ContainerTimeout != "" {
		err = single.Single("devpod.daemon.pid", func() (*exec.Cmd, error) {
			logger.Debugf("Start DevPod Container Daemon with Inactivity Timeout %s", workspaceInfo.ContainerTimeout)
			binaryPath, err := os.Executable()
			if err != nil {
				return nil, err
			}

			return exec.Command(binaryPath, "agent", "container", "daemon", "--timeout", workspaceInfo.ContainerTimeout), nil
		})
		if err != nil {
			return err
		}
	}

	out, err := json.Marshal(setupInfo)
	if err != nil {
		return fmt.Errorf("marshal setup info: %w", err)
	}

	_, err = tunnelClient.SendResult(ctx, &tunnel.Message{Message: string(out)})
	if err != nil {
		return fmt.Errorf("send result: %w", err)
	}

	return nil
}

func fillContainerEnv(setupInfo *config.Result) error {
	// set remote-env
	if setupInfo.MergedConfig.RemoteEnv == nil {
		setupInfo.MergedConfig.RemoteEnv = make(map[string]string)
	}

	if _, ok := setupInfo.MergedConfig.RemoteEnv["PATH"]; !ok {
		setupInfo.MergedConfig.RemoteEnv["PATH"] = "${containerEnv:PATH}"
	}

	// merge config
	newMergedConfig := &config.MergedDevContainerConfig{}
	err := config.SubstituteContainerEnv(config.ListToObject(os.Environ()), setupInfo.MergedConfig, newMergedConfig)
	if err != nil {
		return errors.Wrap(err, "substitute container env")
	}
	setupInfo.MergedConfig = newMergedConfig
	return nil
}

// mountCopy says what the setup does with a bind mount of a container that has copies of the host's folders,
// rather than the folders themselves.
type mountCopy int

const (
	// mountKeep leaves the files in the container as they are.
	mountKeep mountCopy = iota
	// mountReplace copies the host's files over those in the container.
	mountReplace
	// mountRefresh copies the host's files over those in the container, except over the files that were
	// changed in the container after the host's.
	mountRefresh
)

// copyOfMount returns what to do with a bind mount. A mount without files is new. A mount with files is only
// copied again when the workspace is reset or recreated: a bind mount is meant to show the host's files, so
// recreating brings in what changed on the host, but not over what the container changed later, e.g. a token
// it renewed. The workspace folder holds the user's work, so only resetting replaces its files.
func copyOfMount(options provider2.CLIOptions, isWorkspaceMount, hasFiles bool) mountCopy {
	switch {
	case options.Reset, !hasFiles:
		return mountReplace
	case options.Recreate && !isWorkspaceMount:
		return mountRefresh
	default:
		return mountKeep
	}
}

// hasFiles returns whether dir is a folder with something in it.
func hasFiles(dir string) bool {
	files, err := os.ReadDir(dir)
	return err == nil && len(files) > 0
}

// warnIfWorkspaceFolderDiffers warns if the repository in the workspace folder of the container is at another
// commit than contentCommit, the one of the checkout that devcontainer.json was read from. Recreating updates
// that checkout but leaves the copy in the container alone, and an image built in the container takes the files
// it copies from there.
func warnIfWorkspaceFolderDiffers(contentCommit, workspaceFolder string, log log.Logger) {
	if contentCommit == "" {
		return
	}
	// git may not be installed here, e.g. before the image is built
	folderCommit := git.ReadHeadCommit(workspaceFolder)
	if folderCommit == "" {
		log.Debugf("Can't tell the commit of %s to compare it to %s", workspaceFolder, contentCommit)
		return
	} else if folderCommit == contentCommit {
		return
	}

	log.Warnf("The workspace folder in the container is at commit %s, but devcontainer.json was read from commit %s, "+
		"where DevPod's checkout of the repository on your machine is. Recreating doesn't change the folder in the container, "+
		"which may hold work that was not pushed, so an image built now takes its files from there. "+
		"To have the same commit in both, push what the workspace has and run `git pull` in it, then recreate it again, "+
		"or use --reset to copy a new clone over the folder",
		shortCommit(folderCommit), shortCommit(contentCommit))
}

func shortCommit(commit string) string {
	if len(commit) > 8 {
		return commit[:8]
	}
	return commit
}

// refreshBuildInfo replaces the files of the image build in buildInfoDir with those the host has now. The host
// writes them into the build context when it reads devcontainer.json, and they reach the container with the
// workspace folder, which is only copied once. Without this, a recreated container builds with the Dockerfile
// of its first build, or of a build that failed.
func refreshBuildInfo(ctx context.Context, client tunnel.TunnelClient, workspaceMount *config.Mount, buildInfoDir string, log log.Logger) error {
	subPath, err := filepath.Rel(workspaceMount.Target, buildInfoDir)
	if err != nil || !filepath.IsLocal(subPath) {
		return fmt.Errorf("build context %s is not in the workspace folder %s", filepath.Dir(buildInfoDir), workspaceMount.Target)
	}

	stream, err := client.StreamMount(ctx, &tunnel.StreamMountRequest{
		Mount:   workspaceMount.String(),
		SubPath: filepath.ToSlash(subPath),
	})
	if err != nil {
		return fmt.Errorf("init stream build folder: %w", err)
	}

	// the old files stay if the new ones don't arrive
	newDir := buildInfoDir + ".new"
	_ = os.RemoveAll(newDir)
	defer os.RemoveAll(newDir)
	err = os.MkdirAll(newDir, 0o755)
	if err != nil {
		return err
	}
	err = extract.Extract(tunnelserver.NewStreamReader(stream, log), newDir)
	if err != nil {
		return fmt.Errorf("stream build folder: %w", err)
	}

	err = os.RemoveAll(buildInfoDir)
	if err != nil {
		return err
	}
	return os.Rename(newDir, buildInfoDir)
}

func dockerlessBuild(
	ctx context.Context,
	setupInfo *config.Result,
	workspaceInfo *provider2.ContainerWorkspaceInfo,
	client tunnel.TunnelClient,
	debug bool,
	log log.Logger,
) error {
	dockerlessOptions := &workspaceInfo.Dockerless
	if os.Getenv("DOCKERLESS") != "true" {
		return nil
	}

	_, err := os.Stat(DockerlessImageConfigOutput)
	if err == nil {
		log.Debugf("Skip dockerless build, because container was built already")
		return nil
	}

	buildContext := os.Getenv("DOCKERLESS_CONTEXT")
	if buildContext == "" {
		log.Debugf("Build context is missing for dockerless build")
		return nil
	}

	// check if build info is there
	fallbackDir := filepath.Join(config.DevPodDockerlessBuildInfoFolder, config.DevPodContextFeatureFolder)
	buildInfoDir := filepath.Join(buildContext, config.DevPodContextFeatureFolder)
	// when resetting, the build info came with the workspace folder
	if workspaceInfo.CLIOptions.Recreate && !workspaceInfo.CLIOptions.Reset {
		log.Debugf("Get the build files for %s from the host", buildInfoDir)
		err = refreshBuildInfo(ctx, client, config.GetWorkspaceMount(setupInfo), buildInfoDir, log)
		if err != nil {
			log.Warnf("Could not get the build files that match devcontainer.json, building with those of the last build: %v", err)
		}
	}
	_, err = os.Stat(buildInfoDir)
	if err != nil {
		// try to rename from fallback dir
		err = copy.RenameDirectory(fallbackDir, buildInfoDir)
		if err != nil {
			return fmt.Errorf("rename dir: %w", err)
		}

		_, err = os.Stat(buildInfoDir)
		if err != nil {
			return fmt.Errorf("couldn't find build dir %s: %w", buildInfoDir, err)
		}
	}

	binaryPath, err := os.Executable()
	if err != nil {
		return err
	}

	// configure credentials
	if dockerlessOptions.DisableDockerCredentials != "true" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()

		// configure the docker credentials
		dockerCredentialsDir, err := configureDockerCredentials(ctx, cancel, client, log)
		if err != nil {
			log.Errorf("Error configuring docker credentials: %v", err)
		} else {
			defer func() {
				_ = os.Unsetenv("DOCKER_CONFIG")
				_ = os.RemoveAll(dockerCredentialsDir)
			}()
		}
	}

	// build args
	args := []string{"build", "--ignore-path", binaryPath}
	args = append(args, parseIgnorePaths(dockerlessOptions.IgnorePaths)...)
	args = append(args, "--build-arg", "TARGETOS="+runtime.GOOS)
	args = append(args, "--build-arg", "TARGETARCH="+runtime.GOARCH)
	if dockerlessOptions.RegistryCache != "" {
		log.Debug("Appending registry cache to dockerless build arguments ", dockerlessOptions.RegistryCache)
		args = append(args, "--registry-cache", dockerlessOptions.RegistryCache)
	}
	if dockerlessOptions.Insecure == "true" {
		args = append(args, "--insecure")
	}

	// ignore mounts
	args = append(args, "--ignore-path", setupInfo.SubstitutionContext.ContainerWorkspaceFolder)
	for _, m := range setupInfo.MergedConfig.Mounts {
		// check if there already, then we don't touch it
		files, err := os.ReadDir(m.Target)
		if err == nil && len(files) > 0 {
			args = append(args, "--ignore-path", m.Target)
		}
	}

	// write output to log
	errWriter := log.Writer(logrus.InfoLevel, false)
	defer errWriter.Close()

	// start building
	log.Infof("Start dockerless building %s %s", "/.dockerless/dockerless", strings.Join(args, " "))
	cmd := exec.CommandContext(ctx, "/.dockerless/dockerless", args...)

	if debug {
		debugWriter := log.Writer(logrus.DebugLevel, false)
		defer debugWriter.Close()
		cmd.Stdout = debugWriter
	}
	cmd.Stderr = errWriter
	cmd.Env = os.Environ()
	err = cmd.Run()
	if err != nil {
		return err
	}

	// add container env to envfile.json
	rawConfig, err := os.ReadFile(DockerlessImageConfigOutput)
	if err != nil {
		return err
	}

	// parse config file
	configFile := &v1.ConfigFile{}
	err = json.Unmarshal(rawConfig, configFile)
	if err != nil {
		return fmt.Errorf("parse container config: %w", err)
	}

	// apply env
	envfile.MergeAndApply(config.ListToObject(configFile.Config.Env), log)

	// rename build path
	_ = os.RemoveAll(fallbackDir)
	err = copy.RenameDirectory(buildInfoDir, fallbackDir)
	if err != nil {
		log.Debugf("Error renaming dir %s: %v", buildInfoDir, err)
		return nil
	}

	return nil
}

func parseIgnorePaths(ignorePaths string) []string {
	if strings.TrimSpace(ignorePaths) == "" {
		return nil
	}

	retPaths := []string{}
	splitted := strings.Split(ignorePaths, ",")
	for _, s := range splitted {
		retPaths = append(retPaths, "--ignore-path", strings.TrimSpace(s))
	}

	return retPaths
}

func configureDockerCredentials(
	ctx context.Context,
	cancel context.CancelFunc,
	client tunnel.TunnelClient,
	log log.Logger,
) (string, error) {
	serverPort, err := credentials.StartCredentialsServer(ctx, cancel, client, log)
	if err != nil {
		return "", err
	}

	dockerCredentials, err := dockercredentials.ConfigureCredentialsDockerless("/.dockerless/.docker", serverPort, log)
	if err != nil {
		return "", err
	}

	return dockerCredentials, nil
}

func (cmd *SetupContainerCmd) installIDE(setupInfo *config.Result, ide *provider2.WorkspaceIDEConfig, log log.Logger) error {
	switch ide.Name {
	case string(config2.IDENone):
		return nil
	case string(config2.IDEVSCode):
		return cmd.setupVSCode(setupInfo, ide.Options, vscode.FlavorStable, log)
	case string(config2.IDEVSCodeInsiders):
		return cmd.setupVSCode(setupInfo, ide.Options, vscode.FlavorInsiders, log)
	case string(config2.IDECursor):
		return cmd.setupVSCode(setupInfo, ide.Options, vscode.FlavorCursor, log)
	case string(config2.IDEPositron):
		return cmd.setupVSCode(setupInfo, ide.Options, vscode.FlavorPositron, log)
	case string(config2.IDECodium):
		return cmd.setupVSCode(setupInfo, ide.Options, vscode.FlavorCodium, log)
	case string(config2.IDEWindsurf):
		return cmd.setupVSCode(setupInfo, ide.Options, vscode.FlavorWindsurf, log)
	case string(config2.IDEOpenVSCode):
		return cmd.setupOpenVSCode(setupInfo, ide.Options, log)
	case string(config2.IDEGoland):
		return jetbrains.NewGolandServer(config.GetRemoteUser(setupInfo), ide.Options, log).Install()
	case string(config2.IDERustRover):
		return jetbrains.NewRustRoverServer(config.GetRemoteUser(setupInfo), ide.Options, log).Install()
	case string(config2.IDEPyCharm):
		return jetbrains.NewPyCharmServer(config.GetRemoteUser(setupInfo), ide.Options, log).Install()
	case string(config2.IDEPhpStorm):
		return jetbrains.NewPhpStorm(config.GetRemoteUser(setupInfo), ide.Options, log).Install()
	case string(config2.IDEIntellij):
		return jetbrains.NewIntellij(config.GetRemoteUser(setupInfo), ide.Options, log).Install()
	case string(config2.IDECLion):
		return jetbrains.NewCLionServer(config.GetRemoteUser(setupInfo), ide.Options, log).Install()
	case string(config2.IDERider):
		return jetbrains.NewRiderServer(config.GetRemoteUser(setupInfo), ide.Options, log).Install()
	case string(config2.IDERubyMine):
		return jetbrains.NewRubyMineServer(config.GetRemoteUser(setupInfo), ide.Options, log).Install()
	case string(config2.IDEWebStorm):
		return jetbrains.NewWebStormServer(config.GetRemoteUser(setupInfo), ide.Options, log).Install()
	case string(config2.IDEDataSpell):
		return jetbrains.NewDataSpellServer(config.GetRemoteUser(setupInfo), ide.Options, log).Install()
	case string(config2.IDEJupyterNotebook):
		return jupyter.NewJupyterNotebookServer(setupInfo.SubstitutionContext.ContainerWorkspaceFolder, config.GetRemoteUser(setupInfo), ide.Options, log).Install()
	case string(config2.IDERStudio):
		err := rstudio.NewRStudioServer(setupInfo.SubstitutionContext.ContainerWorkspaceFolder, config.GetRemoteUser(setupInfo), ide.Options, log).Install()
		if err != nil {
			log.Errorf("could not install rstudio with error: %w", err)
		}
	}

	return nil
}

func (cmd *SetupContainerCmd) setupVSCode(setupInfo *config.Result, ideOptions map[string]config2.OptionValue, flavor vscode.Flavor, log log.Logger) error {
	log.Debugf("Setup %s...", flavor.DisplayName())
	vsCodeConfiguration := config.GetVSCodeConfiguration(setupInfo.MergedConfig)
	settings := ""
	if len(vsCodeConfiguration.Settings) > 0 {
		out, err := json.Marshal(vsCodeConfiguration.Settings)
		if err != nil {
			return err
		}

		settings = string(out)
	}

	user := config.GetRemoteUser(setupInfo)
	err := vscode.NewVSCodeServer(vsCodeConfiguration.Extensions, settings, user, ideOptions, flavor, log).Install()
	if err != nil {
		return err
	}

	// don't install code-server if we don't have settings or extensions
	if len(vsCodeConfiguration.Settings) == 0 && len(vsCodeConfiguration.Extensions) == 0 {
		return nil
	}

	if len(vsCodeConfiguration.Extensions) == 0 {
		return nil
	}

	return single.Single(fmt.Sprintf("%s-async.pid", flavor), func() (*exec.Cmd, error) {
		log.Infof("Install extensions '%s' in the background", strings.Join(vsCodeConfiguration.Extensions, ","))
		binaryPath, err := os.Executable()
		if err != nil {
			return nil, err
		}

		args := []string{
			"agent", "container", "vscode-async",
			"--setup-info", cmd.SetupInfo,
			"--release-channel", string(flavor),
		}

		return exec.Command(binaryPath, args...), nil
	})
}

func (cmd *SetupContainerCmd) setupOpenVSCode(setupInfo *config.Result, ideOptions map[string]config2.OptionValue, log log.Logger) error {
	log.Debugf("Setup openvscode...")
	vsCodeConfiguration := config.GetVSCodeConfiguration(setupInfo.MergedConfig)
	settings := ""
	if len(vsCodeConfiguration.Settings) > 0 {
		out, err := json.Marshal(vsCodeConfiguration.Settings)
		if err != nil {
			return err
		}

		settings = string(out)
	}

	user := config.GetRemoteUser(setupInfo)
	openVSCode := openvscode.NewOpenVSCodeServer(vsCodeConfiguration.Extensions, settings, user, "0.0.0.0", strconv.Itoa(openvscode.DefaultVSCodePort), ideOptions, log)

	// install open vscode
	err := openVSCode.Install()
	if err != nil {
		return err
	}

	// install extensions in background
	if len(vsCodeConfiguration.Extensions) > 0 {
		err = single.Single("openvscode-async.pid", func() (*exec.Cmd, error) {
			log.Infof("Install extensions '%s' in the background", strings.Join(vsCodeConfiguration.Extensions, ","))
			binaryPath, err := os.Executable()
			if err != nil {
				return nil, err
			}

			return exec.Command(binaryPath, "agent", "container", "openvscode-async", "--setup-info", cmd.SetupInfo), nil
		})
		if err != nil {
			return errors.Wrap(err, "install extensions")
		}
	}

	// start the server in the background
	return openVSCode.Start()
}

// configureGitSSHToHTTPS makes git use https instead of ssh for the hosts of the workspace's remotes when
// GIT_SSH_TO_HTTPS is enabled, and removes the rewrites again when it is disabled. When the CLI didn't say, e.g.
// because an older CLI started the container, it leaves the rewrites as they are.
func configureGitSSHToHTTPS(ctx context.Context, setupInfo *config.Result, workspaceInfo *provider2.ContainerWorkspaceInfo, log log.Logger) error {
	enabled := workspaceInfo.CLIOptions.GitSSHToHTTPS
	if enabled == "" {
		return nil
	}

	if !command.Exists("git") {
		if enabled == "true" {
			log.Warn("GIT_SSH_TO_HTTPS is enabled but git is not installed in the container, skipping")
		}
		return nil
	}

	if enabled != "true" {
		return git.RemoveSSHToHTTPS(ctx)
	}

	remoteURLs := []string{}
	if workspaceInfo.Source.GitRepository != "" {
		remoteURLs = append(remoteURLs, workspaceInfo.Source.GitRepository)
	}
	remoteURLs = append(remoteURLs, workspaceInfo.CLIOptions.GitSSHToHTTPSRemotes...)
	remoteURLs = append(remoteURLs, git.RemoteURLs(ctx, setupInfo.SubstitutionContext.ContainerWorkspaceFolder)...)
	rewrites := git.SSHToHTTPSRewrites(remoteURLs, workspaceInfo.CLIOptions.GitSSHToHTTPSHosts)
	if len(rewrites) == 0 {
		log.Debug("GIT_SSH_TO_HTTPS is enabled but no ssh hosts were found")
	}
	log.Debugf("Configuring git to use https instead of ssh: %v", rewrites)
	return git.ConfigureSSHToHTTPS(ctx, rewrites)
}

func configureSystemGitCredentials(ctx context.Context, cancel context.CancelFunc, client tunnel.TunnelClient, log log.Logger) (func(), error) {
	if !command.Exists("git") {
		return nil, errors.New("git not found")
	}

	serverPort, err := credentials.StartCredentialsServer(ctx, cancel, client, log)
	if err != nil {
		return nil, err
	}

	binaryPath, err := os.Executable()
	if err != nil {
		return nil, err
	}

	gitCredentials := fmt.Sprintf("!'%s' agent git-credentials --port %d", binaryPath, serverPort)
	_ = os.Setenv("DEVPOD_GIT_HELPER_PORT", strconv.Itoa(serverPort))

	err = git.CommandContext(ctx, git.GetDefaultExtraEnv(false), "config", "--system", "--add", "credential.helper", gitCredentials).Run()
	if err != nil {
		return nil, fmt.Errorf("add git credential helper: %w", err)
	}

	cleanup := func() {
		log.Debug("Unset setup system credential helper")
		err = git.CommandContext(ctx, git.GetDefaultExtraEnv(false), "config", "--system", "--unset", "credential.helper").Run()
		if err != nil {
			log.Errorf("unset system credential helper %v", err)
		}
	}

	return cleanup, nil
}

func streamMount(ctx context.Context, m *config.Mount, tunnelClient tunnel.TunnelClient, logger log.Logger, options ...extract.Option) error {
	// stream mount
	stream, err := tunnelClient.StreamMount(ctx, &tunnel.StreamMountRequest{Mount: m.String()})
	if err != nil {
		return fmt.Errorf("init stream mount %s: %w", m.String(), err)
	}

	// target folder
	err = extract.Extract(tunnelserver.NewStreamReader(stream, logger), m.Target, options...)
	if err != nil {
		return fmt.Errorf("stream mount %s: %w", m.String(), err)
	}

	return nil
}
