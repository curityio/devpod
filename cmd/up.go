package cmd

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/loft-sh/devpod/cmd/flags"
	"github.com/loft-sh/devpod/pkg/agent"
	"github.com/loft-sh/devpod/pkg/agent/tunnelserver"
	client2 "github.com/loft-sh/devpod/pkg/client"
	"github.com/loft-sh/devpod/pkg/config"
	config2 "github.com/loft-sh/devpod/pkg/devcontainer/config"
	"github.com/loft-sh/devpod/pkg/devcontainer/sshtunnel"
	"github.com/loft-sh/devpod/pkg/ide"
	"github.com/loft-sh/devpod/pkg/ide/jetbrains"
	"github.com/loft-sh/devpod/pkg/ide/jupyter"
	"github.com/loft-sh/devpod/pkg/ide/openvscode"
	"github.com/loft-sh/devpod/pkg/ide/rstudio"
	"github.com/loft-sh/devpod/pkg/ide/vscode"
	"github.com/loft-sh/devpod/pkg/ide/zed"
	open2 "github.com/loft-sh/devpod/pkg/open"
	"github.com/loft-sh/devpod/pkg/port"
	provider2 "github.com/loft-sh/devpod/pkg/provider"
	devssh "github.com/loft-sh/devpod/pkg/ssh"
	"github.com/loft-sh/devpod/pkg/telemetry"
	"github.com/loft-sh/devpod/pkg/tunnel"
	"github.com/loft-sh/devpod/pkg/util"
	workspace2 "github.com/loft-sh/devpod/pkg/workspace"
	"github.com/loft-sh/log"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
)

// UpCmd holds the up cmd flags
type UpCmd struct {
	provider2.CLIOptions
	*flags.GlobalFlags

	Machine string

	ProviderOptions []string

	ConfigureSSH       bool
	GPGAgentForwarding bool
	OpenIDE            bool

	SSHConfigPath string

	DotfilesSource        string
	DotfilesScript        string
	DotfilesScriptEnv     []string // Key=Value to pass to install script
	DotfilesScriptEnvFile []string // Paths to files containing Key=Value pairs to pass to install script
}

// NewUpCmd creates a new up command
func NewUpCmd(f *flags.GlobalFlags) *cobra.Command {
	cmd := &UpCmd{
		GlobalFlags: f,
	}
	upCmd := &cobra.Command{
		Use:   "up [flags] [workspace-path|workspace-name]",
		Short: "Starts a new workspace",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			devPodConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
			if err != nil {
				return err
			}

			if devPodConfig.ContextOption(config.ContextOptionSSHStrictHostKeyChecking) == "true" {
				cmd.StrictHostKeyChecking = true
			}

			ctx, cancel := WithSignals(cobraCmd.Context())
			defer cancel()

			client, logger, err := cmd.prepareClient(ctx, devPodConfig, args)
			if err != nil {
				return fmt.Errorf("prepare workspace client: %w", err)
			}
			telemetry.CollectorCLI.SetClient(client)

			err = provider2.ApplyContextOptions(ctx, &cmd.CLIOptions, devPodConfig, client.WorkspaceConfig().Source)
			if err != nil {
				return err
			}

			return cmd.Run(ctx, devPodConfig, client, args, logger)
		},
	}
	upCmd.Flags().BoolVar(&cmd.ConfigureSSH, "configure-ssh", true, "If true will configure the ssh config to include the DevPod workspace")
	upCmd.Flags().BoolVar(&cmd.GPGAgentForwarding, "gpg-agent-forwarding", false, "If true forward the local gpg-agent to the DevPod workspace")
	upCmd.Flags().StringVar(&cmd.SSHConfigPath, "ssh-config", "", "The path to the ssh config to modify, if empty will use ~/.ssh/config")
	upCmd.Flags().StringVar(&cmd.DotfilesSource, "dotfiles", "", "The path or url to the dotfiles to use in the container")
	upCmd.Flags().StringVar(&cmd.DotfilesScript, "dotfiles-script", "", "The path in dotfiles directory to use to install the dotfiles, if empty will try to guess")
	upCmd.Flags().StringSliceVar(&cmd.DotfilesScriptEnv, "dotfiles-script-env", []string{}, "Extra environment variables to put into the dotfiles install script. E.g. MY_ENV_VAR=MY_VALUE")
	upCmd.Flags().StringSliceVar(&cmd.DotfilesScriptEnvFile, "dotfiles-script-env-file", []string{}, "The path to files containing environment variables to set for the dotfiles install script")
	upCmd.Flags().StringArrayVar(&cmd.IDEOptions, "ide-option", []string{}, "IDE option in the form KEY=VALUE")
	upCmd.Flags().StringVar(&cmd.DevContainerImage, "devcontainer-image", "", "The container image to use, this will override the devcontainer.json value in the project")
	upCmd.Flags().StringVar(&cmd.DevContainerPath, "devcontainer-path", "", "The path to the devcontainer.json relative to the project")
	upCmd.Flags().StringArrayVar(&cmd.ProviderOptions, "provider-option", []string{}, "Provider option in the form KEY=VALUE")
	upCmd.Flags().BoolVar(&cmd.Recreate, "recreate", false, "If true will remove any existing containers and recreate them")
	upCmd.Flags().BoolVar(&cmd.Reset, "reset", false, "If true will remove any existing containers including sources, and recreate them")
	upCmd.Flags().StringSliceVar(&cmd.PrebuildRepositories, "prebuild-repository", []string{}, "Docker repository that hosts devpod prebuilds for this workspace")
	upCmd.Flags().StringArrayVar(&cmd.WorkspaceEnv, "workspace-env", []string{}, "Extra env variables to put into the workspace. E.g. MY_ENV_VAR=MY_VALUE")
	upCmd.Flags().StringSliceVar(&cmd.WorkspaceEnvFile, "workspace-env-file", []string{}, "The path to files containing a list of extra env variables to put into the workspace. E.g. MY_ENV_VAR=MY_VALUE")
	upCmd.Flags().StringArrayVar(&cmd.InitEnv, "init-env", []string{}, "Extra env variables to inject during the initialization of the workspace. E.g. MY_ENV_VAR=MY_VALUE")
	upCmd.Flags().StringVar(&cmd.ID, "id", "", "The id to use for the workspace")
	upCmd.Flags().StringVar(&cmd.Machine, "machine", "", "The machine to use for this workspace. The machine needs to exist beforehand or the command will fail. If the workspace already exists, this option has no effect")
	upCmd.Flags().StringVar(&cmd.IDE, "ide", "", "The IDE to open the workspace in. If empty will use vscode locally or in browser")
	upCmd.Flags().BoolVar(&cmd.OpenIDE, "open-ide", true, "If this is false and an IDE is configured, DevPod will only install the IDE server backend, but not open it")
	upCmd.Flags().Var(&cmd.GitCloneStrategy, "git-clone-strategy", "The git clone strategy DevPod uses to checkout git based workspaces. Can be full (default), blobless, treeless or shallow")
	upCmd.Flags().BoolVar(&cmd.GitCloneRecursiveSubmodules, "git-clone-recursive-submodules", false, "If true will clone git submodule repositories recursively")
	upCmd.Flags().StringVar(&cmd.GitSSHSigningKey, "git-ssh-signing-key", "", "The ssh key to use when signing git commits. Used to explicitly setup DevPod's ssh signature forwarding with given key. Should be same format as value of `git config user.signingkey`")
	upCmd.Flags().StringVar(&cmd.FallbackImage, "fallback-image", "", "The fallback image to use if no devcontainer configuration has been detected")
	upCmd.Flags().BoolVar(&cmd.DisableDaemon, "disable-daemon", false, "If enabled, will not install a daemon into the target machine to track activity")
	upCmd.Flags().StringVar(&cmd.Source, "source", "", "Optional source for the workspace. E.g. git:https://github.com/my-org/my-repo")

	// testing
	upCmd.Flags().StringVar(&cmd.DaemonInterval, "daemon-interval", "", "TESTING ONLY")
	_ = upCmd.Flags().MarkHidden("daemon-interval")
	upCmd.Flags().BoolVar(&cmd.ForceDockerless, "force-dockerless", false, "TESTING ONLY")
	_ = upCmd.Flags().MarkHidden("force-dockerless")
	return upCmd
}

// Run runs the command logic
func (cmd *UpCmd) Run(
	ctx context.Context,
	devPodConfig *config.Config,
	client client2.WorkspaceClient,
	args []string,
	log log.Logger,
) error {
	// a reset implies a recreate
	if cmd.Reset {
		cmd.Recreate = true
	}

	// check if we are a browser IDE and need to reuse the SSH_AUTH_SOCK
	targetIDE := client.WorkspaceConfig().IDE.Name
	// Check override
	if cmd.IDE != "" {
		targetIDE = cmd.IDE
	}
	if ide.ReusesAuthSock(targetIDE) {
		cmd.SSHAuthSockID = util.RandStringBytes(10)
		log.Debug("Reusing SSH_AUTH_SOCK", cmd.SSHAuthSockID)
	}

	// run devpod agent up
	result, err := cmd.devPodUp(ctx, devPodConfig, client, log)
	if err != nil {
		return err
	} else if result == nil {
		return fmt.Errorf("didn't receive a result back from agent")
	}

	// get user from result
	user := config2.GetRemoteUser(result)

	var workdir string
	if result.MergedConfig != nil && result.MergedConfig.WorkspaceFolder != "" {
		workdir = result.MergedConfig.WorkspaceFolder
	}
	if client.WorkspaceConfig().Source.GitSubPath != "" {
		result.SubstitutionContext.ContainerWorkspaceFolder = filepath.Join(result.SubstitutionContext.ContainerWorkspaceFolder, client.WorkspaceConfig().Source.GitSubPath)
		workdir = result.SubstitutionContext.ContainerWorkspaceFolder
	}

	// configure container ssh
	if cmd.ConfigureSSH {
		devPodHome := ""
		envDevPodHome, ok := os.LookupEnv("DEVPOD_HOME")
		if ok {
			devPodHome = envDevPodHome
		}
		setupGPGAgentForwarding := cmd.GPGAgentForwarding || devPodConfig.ContextOption(config.ContextOptionGPGAgentForwarding) == "true"

		err = configureSSH(client, cmd.SSHConfigPath, user, workdir, setupGPGAgentForwarding, !cmd.DisableSSHAgentForwarding, devPodHome)
		if err != nil {
			return err
		}

		log.Infof("Run 'ssh %s.devpod' to ssh into the devcontainer", client.Workspace())
	}

	// setup git ssh signature
	if cmd.GitSSHSigningKey != "" {
		err = setupGitSSHSignature(cmd.GitSSHSigningKey, client, log)
		if err != nil {
			return err
		}
	}

	// setup dotfiles in the container
	err = setupDotfiles(cmd.DotfilesSource, cmd.DotfilesScript, cmd.DotfilesScriptEnvFile, cmd.DotfilesScriptEnv, client, devPodConfig, log)
	if err != nil {
		return err
	}

	// open ide
	if cmd.OpenIDE {
		ideConfig := client.WorkspaceConfig().IDE
		switch ideConfig.Name {
		case string(config.IDEVSCode):
			return vscode.Open(
				ctx,
				client.Workspace(),
				result.SubstitutionContext.ContainerWorkspaceFolder,
				vscode.Options.GetValue(ideConfig.Options, vscode.OpenNewWindow) == "true",
				vscode.FlavorStable,
				log,
			)
		case string(config.IDEVSCodeInsiders):
			return vscode.Open(
				ctx,
				client.Workspace(),
				result.SubstitutionContext.ContainerWorkspaceFolder,
				vscode.Options.GetValue(ideConfig.Options, vscode.OpenNewWindow) == "true",
				vscode.FlavorInsiders,
				log,
			)
		case string(config.IDECursor):
			return vscode.Open(
				ctx,
				client.Workspace(),
				result.SubstitutionContext.ContainerWorkspaceFolder,
				vscode.Options.GetValue(ideConfig.Options, vscode.OpenNewWindow) == "true",
				vscode.FlavorCursor,
				log,
			)
		case string(config.IDECodium):
			return vscode.Open(
				ctx,
				client.Workspace(),
				result.SubstitutionContext.ContainerWorkspaceFolder,
				vscode.Options.GetValue(ideConfig.Options, vscode.OpenNewWindow) == "true",
				vscode.FlavorCodium,
				log,
			)
		case string(config.IDEPositron):
			return vscode.Open(
				ctx,
				client.Workspace(),
				result.SubstitutionContext.ContainerWorkspaceFolder,
				vscode.Options.GetValue(ideConfig.Options, vscode.OpenNewWindow) == "true",
				vscode.FlavorPositron,
				log,
			)
		case string(config.IDEWindsurf):
			return vscode.Open(
				ctx,
				client.Workspace(),
				result.SubstitutionContext.ContainerWorkspaceFolder,
				vscode.Options.GetValue(ideConfig.Options, vscode.OpenNewWindow) == "true",
				vscode.FlavorWindsurf,
				log,
			)
		case string(config.IDEOpenVSCode):
			return startVSCodeInBrowser(
				cmd.GPGAgentForwarding,
				ctx,
				devPodConfig,
				client,
				result.SubstitutionContext.ContainerWorkspaceFolder,
				user,
				ideConfig.Options,
				cmd.SSHAuthSockID,
				log,
			)
		case string(config.IDERustRover):
			return jetbrains.NewRustRoverServer(config2.GetRemoteUser(result), ideConfig.Options, log).OpenGateway(result.SubstitutionContext.ContainerWorkspaceFolder, client.Workspace())
		case string(config.IDEGoland):
			return jetbrains.NewGolandServer(config2.GetRemoteUser(result), ideConfig.Options, log).OpenGateway(result.SubstitutionContext.ContainerWorkspaceFolder, client.Workspace())
		case string(config.IDEPyCharm):
			return jetbrains.NewPyCharmServer(config2.GetRemoteUser(result), ideConfig.Options, log).OpenGateway(result.SubstitutionContext.ContainerWorkspaceFolder, client.Workspace())
		case string(config.IDEPhpStorm):
			return jetbrains.NewPhpStorm(config2.GetRemoteUser(result), ideConfig.Options, log).OpenGateway(result.SubstitutionContext.ContainerWorkspaceFolder, client.Workspace())
		case string(config.IDEIntellij):
			return jetbrains.NewIntellij(config2.GetRemoteUser(result), ideConfig.Options, log).OpenGateway(result.SubstitutionContext.ContainerWorkspaceFolder, client.Workspace())
		case string(config.IDECLion):
			return jetbrains.NewCLionServer(config2.GetRemoteUser(result), ideConfig.Options, log).OpenGateway(result.SubstitutionContext.ContainerWorkspaceFolder, client.Workspace())
		case string(config.IDERider):
			return jetbrains.NewRiderServer(config2.GetRemoteUser(result), ideConfig.Options, log).OpenGateway(result.SubstitutionContext.ContainerWorkspaceFolder, client.Workspace())
		case string(config.IDERubyMine):
			return jetbrains.NewRubyMineServer(config2.GetRemoteUser(result), ideConfig.Options, log).OpenGateway(result.SubstitutionContext.ContainerWorkspaceFolder, client.Workspace())
		case string(config.IDEWebStorm):
			return jetbrains.NewWebStormServer(config2.GetRemoteUser(result), ideConfig.Options, log).OpenGateway(result.SubstitutionContext.ContainerWorkspaceFolder, client.Workspace())
		case string(config.IDEDataSpell):
			return jetbrains.NewDataSpellServer(config2.GetRemoteUser(result), ideConfig.Options, log).OpenGateway(result.SubstitutionContext.ContainerWorkspaceFolder, client.Workspace())
		case string(config.IDEZed):
			return zed.Open(ctx, ideConfig.Options, config2.GetRemoteUser(result), result.SubstitutionContext.ContainerWorkspaceFolder, client.Workspace(), log)
		case string(config.IDEJupyterNotebook):
			return startJupyterNotebookInBrowser(
				cmd.GPGAgentForwarding,
				ctx,
				devPodConfig,
				client,
				user,
				ideConfig.Options,
				cmd.SSHAuthSockID,
				log,
			)
		case string(config.IDERStudio):
			return startRStudioInBrowser(
				cmd.GPGAgentForwarding,
				ctx,
				devPodConfig,
				client,
				user,
				ideConfig.Options,
				cmd.SSHAuthSockID,
				log,
			)
		}
	}

	return nil
}

func (cmd *UpCmd) devPodUp(
	ctx context.Context,
	devPodConfig *config.Config,
	client client2.WorkspaceClient,
	log log.Logger,
) (*config2.Result, error) {
	err := client.Lock(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Unlock()

	// get result
	result, err := cmd.devPodUpMachine(ctx, devPodConfig, client, log)
	if err != nil {
		return nil, err
	}

	// save result to file
	err = provider2.SaveWorkspaceResult(client.WorkspaceConfig(), result)
	if err != nil {
		return nil, fmt.Errorf("save workspace result: %w", err)
	}

	return result, nil
}

func (cmd *UpCmd) devPodUpMachine(
	ctx context.Context,
	devPodConfig *config.Config,
	client client2.WorkspaceClient,
	log log.Logger,
) (*config2.Result, error) {
	err := startWait(ctx, client, true, log)
	if err != nil {
		return nil, err
	}

	// compress info
	workspaceInfo, wInfo, err := client.AgentInfo(cmd.CLIOptions)
	if err != nil {
		return nil, err
	}

	// create container etc.
	log.Infof("Creating devcontainer...")
	defer log.Debugf("Done creating devcontainer")

	// ssh tunnel command
	sshTunnelCmd := fmt.Sprintf("'%s' helper ssh-server --stdio", client.AgentPath())
	if log.GetLevel() == logrus.DebugLevel {
		sshTunnelCmd += " --debug"
	}

	// create agent command
	agentCommand := fmt.Sprintf(
		"'%s' agent workspace up --workspace-info '%s'",
		client.AgentPath(),
		workspaceInfo,
	)

	if log.GetLevel() == logrus.DebugLevel {
		agentCommand += " --debug"
	}

	agentInjectFunc := func(cancelCtx context.Context, sshCmd string, sshTunnelStdinReader, sshTunnelStdoutWriter *os.File, writer io.WriteCloser) error {
		return agent.InjectAgentAndExecute(
			cancelCtx,
			func(ctx context.Context, command string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
				return client.Command(ctx, client2.CommandOptions{
					Command: command,
					Stdin:   stdin,
					Stdout:  stdout,
					Stderr:  stderr,
				})
			},
			client.AgentLocal(),
			client.AgentPath(),
			sshCmd,
			sshTunnelStdinReader,
			sshTunnelStdoutWriter,
			writer,
			log.ErrorStreamOnly(),
			wInfo.InjectTimeout,
		)
	}

	return sshtunnel.ExecuteCommand(
		ctx,
		client,
		devPodConfig.ContextOption(config.ContextOptionSSHAddPrivateKeys) == "true",
		// the agent on the machine may need it to clone the source, it isn't forwarded into the container when
		// SSH_AGENT_FORWARDING is false
		true,
		agentInjectFunc,
		sshTunnelCmd,
		agentCommand,
		log,
		func(ctx context.Context, stdin io.WriteCloser, stdout io.Reader) (*config2.Result, error) {
			return tunnelserver.RunUpServer(
				ctx,
				stdout,
				stdin,
				client.AgentInjectGitCredentials(cmd.CLIOptions),
				client.AgentInjectDockerCredentials(cmd.CLIOptions),
				client.WorkspaceConfig(),
				log,
			)
		},
	)
}

func startJupyterNotebookInBrowser(
	forwardGpg bool,
	ctx context.Context,
	devPodConfig *config.Config,
	client client2.BaseWorkspaceClient,
	user string,
	ideOptions map[string]config.OptionValue,
	authSockID string,
	logger log.Logger,
) error {
	if forwardGpg {
		err := performGpgForwarding(client, logger)
		if err != nil {
			return err
		}
	}

	// determine port
	jupyterAddress, jupyterPort, err := parseAddressAndPort(
		jupyter.Options.GetValue(ideOptions, jupyter.BindAddressOption),
		jupyter.DefaultServerPort,
	)
	if err != nil {
		return err
	}

	// wait until reachable then open browser
	targetURL := fmt.Sprintf("http://localhost:%d/lab", jupyterPort)
	if jupyter.Options.GetValue(ideOptions, jupyter.OpenOption) == "true" {
		go func() {
			err = open2.Open(ctx, targetURL, logger)
			if err != nil {
				logger.Errorf("error opening jupyter notebook: %v", err)
			}

			logger.Infof(
				"Successfully started jupyter notebook in browser mode. Please keep this terminal open as long as you use Jupyter Notebook",
			)
		}()
	}

	// start in browser
	logger.Infof("Starting jupyter notebook in browser mode at %s", targetURL)
	extraPorts := []string{fmt.Sprintf("%s:%d", jupyterAddress, jupyter.DefaultServerPort)}
	return startBrowserTunnel(
		ctx,
		devPodConfig,
		client,
		user,
		targetURL,
		false,
		extraPorts,
		authSockID,
		logger,
	)
}

func startRStudioInBrowser(
	forwardGpg bool,
	ctx context.Context,
	devPodConfig *config.Config,
	client client2.BaseWorkspaceClient,
	user string,
	ideOptions map[string]config.OptionValue,
	authSockID string,
	logger log.Logger,
) error {
	if forwardGpg {
		err := performGpgForwarding(client, logger)
		if err != nil {
			return err
		}
	}

	// determine port
	addr, port, err := parseAddressAndPort(
		rstudio.Options.GetValue(ideOptions, rstudio.BindAddressOption),
		rstudio.DefaultServerPort,
	)
	if err != nil {
		return err
	}

	// wait until reachable then open browser
	targetURL := fmt.Sprintf("http://localhost:%d", port)
	if rstudio.Options.GetValue(ideOptions, rstudio.OpenOption) == "true" {
		go func() {
			err = open2.Open(ctx, targetURL, logger)
			if err != nil {
				logger.Errorf("error opening rstudio: %v", err)
			}

			logger.Infof(
				"Successfully started RStudio Server in browser mode. Please keep this terminal open as long as you use it",
			)
		}()
	}

	// start in browser
	logger.Infof("Starting RStudio server in browser mode at %s", targetURL)
	extraPorts := []string{fmt.Sprintf("%s:%d", addr, rstudio.DefaultServerPort)}
	return startBrowserTunnel(
		ctx,
		devPodConfig,
		client,
		user,
		targetURL,
		false,
		extraPorts,
		authSockID,
		logger,
	)
}

func startVSCodeInBrowser(
	forwardGpg bool,
	ctx context.Context,
	devPodConfig *config.Config,
	client client2.BaseWorkspaceClient,
	workspaceFolder, user string,
	ideOptions map[string]config.OptionValue,
	authSockID string,
	logger log.Logger,
) error {
	if forwardGpg {
		err := performGpgForwarding(client, logger)
		if err != nil {
			return err
		}
	}

	// determine port
	vscodeAddress, vscodePort, err := parseAddressAndPort(
		openvscode.Options.GetValue(ideOptions, openvscode.BindAddressOption),
		openvscode.DefaultVSCodePort,
	)
	if err != nil {
		return err
	}

	// wait until reachable then open browser
	targetURL := fmt.Sprintf("http://localhost:%d/?folder=%s", vscodePort, workspaceFolder)
	if openvscode.Options.GetValue(ideOptions, openvscode.OpenOption) == "true" {
		go func() {
			err = open2.Open(ctx, targetURL, logger)
			if err != nil {
				logger.Errorf("error opening vscode: %v", err)
			}

			logger.Infof(
				"Successfully started vscode in browser mode. Please keep this terminal open as long as you use VSCode browser version",
			)
		}()
	}

	// start in browser
	logger.Infof("Starting vscode in browser mode at %s", targetURL)
	forwardPorts := openvscode.Options.GetValue(ideOptions, openvscode.ForwardPortsOption) == "true"
	extraPorts := []string{fmt.Sprintf("%s:%d", vscodeAddress, openvscode.DefaultVSCodePort)}
	return startBrowserTunnel(
		ctx,
		devPodConfig,
		client,
		user,
		targetURL,
		forwardPorts,
		extraPorts,
		authSockID,
		logger,
	)
}

func parseAddressAndPort(bindAddressOption string, defaultPort int) (string, int, error) {
	var (
		err      error
		address  string
		portName int
	)
	if bindAddressOption == "" {
		portName, err = port.FindAvailablePort(defaultPort)
		if err != nil {
			return "", 0, err
		}

		address = fmt.Sprintf("%d", portName)
	} else {
		address = bindAddressOption
		_, port, err := net.SplitHostPort(address)
		if err != nil {
			return "", 0, fmt.Errorf("parse host:port: %w", err)
		} else if port == "" {
			return "", 0, fmt.Errorf("parse ADDRESS: expected host:port, got %s", address)
		}

		portName, err = strconv.Atoi(port)
		if err != nil {
			return "", 0, fmt.Errorf("parse host:port: %w", err)
		}
	}

	return address, portName, nil
}

// setupBackhaul sets up a long running command in the container to ensure an SSH connection is kept alive
func setupBackhaul(client client2.BaseWorkspaceClient, authSockId string, log log.Logger) error {
	execPath, err := os.Executable()
	if err != nil {
		return err
	}

	remoteUser, err := devssh.GetUser(client.WorkspaceConfig().ID, client.WorkspaceConfig().SSHConfigPath)
	if err != nil {
		remoteUser = "root"
	}

	dotCmd := exec.Command(
		execPath,
		"ssh",
		"--agent-forwarding=true",
		fmt.Sprintf("--reuse-ssh-auth-sock=%s", authSockId),
		"--start-services=false",
		"--user",
		remoteUser,
		"--context",
		client.Context(),
		client.Workspace(),
		"--log-output=raw",
		"--command",
		"while true; do sleep 6000000; done", // sleep infinity is not available on all systems
	)

	if log.GetLevel() == logrus.DebugLevel {
		dotCmd.Args = append(dotCmd.Args, "--debug")
	}

	log.Info("Setting up backhaul SSH connection")

	writer := log.Writer(logrus.InfoLevel, false)

	dotCmd.Stdout = writer
	dotCmd.Stderr = writer

	err = dotCmd.Run()
	if err != nil {
		return err
	}

	log.Infof("Done setting up backhaul")

	return nil
}

func startBrowserTunnel(
	ctx context.Context,
	devPodConfig *config.Config,
	client client2.BaseWorkspaceClient,
	user, targetURL string,
	forwardPorts bool,
	extraPorts []string,
	authSockID string,
	logger log.Logger,
) error {
	// Setup a backhaul SSH connection using the remote user so there is an AUTH SOCK to use
	// With normal IDEs this would be the SSH connection made by the IDE
	if authSockID != "" {
		go func() {
			if err := setupBackhaul(client, authSockID, logger); err != nil {
				logger.Error("Failed to setup backhaul SSH connection: ", err)
			}
		}()
	}

	err := tunnel.NewTunnel(
		ctx,
		func(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
			writer := logger.Writer(logrus.DebugLevel, false)
			defer writer.Close()

			cmd, err := createSSHCommand(ctx, client, logger, []string{
				"--log-output=raw",
				fmt.Sprintf("--reuse-ssh-auth-sock=%s", authSockID),
				"--stdio",
			})
			if err != nil {
				return err
			}
			cmd.Stdout = stdout
			cmd.Stdin = stdin
			cmd.Stderr = writer
			return cmd.Run()
		},
		func(ctx context.Context, containerClient *ssh.Client) error {
			// print port to console
			streamLogger, ok := logger.(*log.StreamLogger)
			if ok {
				streamLogger.JSON(logrus.InfoLevel, map[string]string{
					"url":  targetURL,
					"done": "true",
				})
			}

			configureDockerCredentials := devPodConfig.ContextOption(config.ContextOptionSSHInjectDockerCredentials) == "true"
			configureGitCredentials := devPodConfig.ContextOption(config.ContextOptionSSHInjectGitCredentials) == "true"
			configureGitSSHSignatureHelper := devPodConfig.ContextOption(config.ContextOptionGitSSHSignatureForwarding) == "true"

			// run in container
			err := tunnel.RunServices(
				ctx,
				devPodConfig,
				containerClient,
				user,
				forwardPorts,
				extraPorts,
				client.WorkspaceConfig(),
				configureDockerCredentials,
				configureGitCredentials,
				configureGitSSHSignatureHelper,
				logger,
			)
			if err != nil {
				return fmt.Errorf("run credentials server in browser tunnel: %w", err)
			}

			<-ctx.Done()
			return nil
		},
	)
	if err != nil {
		return err
	}

	return nil
}

func configureSSH(client client2.BaseWorkspaceClient, sshConfigPath, user, workdir string, gpgagent, forwardAgent bool, devPodHome string) error {
	path, err := devssh.ResolveSSHConfigPath(sshConfigPath)
	if err != nil {
		return errors.Wrap(err, "Invalid ssh config path")
	}
	sshConfigPath = path

	err = devssh.ConfigureSSHConfig(
		sshConfigPath,
		client.Context(),
		client.Workspace(),
		user,
		workdir,
		gpgagent,
		forwardAgent,
		devPodHome,
		log.Default,
	)
	if err != nil {
		return err
	}

	return nil
}

func mergeEnvFromFiles(baseOptions *provider2.CLIOptions) error {
	var variables []string
	for _, file := range baseOptions.WorkspaceEnvFile {
		envFromFile, err := config2.ParseKeyValueFile(file)
		if err != nil {
			return err
		}
		variables = append(variables, envFromFile...)
	}
	baseOptions.WorkspaceEnv = append(baseOptions.WorkspaceEnv, variables...)

	return nil
}

func createSSHCommand(
	ctx context.Context,
	client client2.BaseWorkspaceClient,
	logger log.Logger,
	extraArgs []string,
) (*exec.Cmd, error) {
	execPath, err := os.Executable()
	if err != nil {
		return nil, err
	}

	args := []string{
		"ssh",
		"--user=root",
		"--agent-forwarding=false",
		"--start-services=false",
		"--context",
		client.Context(),
		client.Workspace(),
	}
	if logger.GetLevel() == logrus.DebugLevel {
		args = append(args, "--debug")
	}
	args = append(args, extraArgs...)

	return exec.CommandContext(ctx, execPath, args...), nil
}

func setupDotfiles(
	dotfiles, script string,
	envFiles, envKeyValuePairs []string,
	client client2.BaseWorkspaceClient,
	devPodConfig *config.Config,
	log log.Logger,
) error {
	dotfilesRepo := devPodConfig.ContextOption(config.ContextOptionDotfilesURL)
	if dotfiles != "" {
		dotfilesRepo = dotfiles
	}

	dotfilesScript := devPodConfig.ContextOption(config.ContextOptionDotfilesScript)
	if script != "" {
		dotfilesScript = script
	}

	if dotfilesRepo == "" {
		log.Debug("No dotfiles repo specified, skipping")
		return nil
	}

	log.Infof("Dotfiles git repository %s specified", dotfilesRepo)
	log.Debug("Cloning dotfiles into the devcontainer...")

	dotCmd, err := buildDotCmd(devPodConfig, dotfilesRepo, dotfilesScript, envFiles, envKeyValuePairs, client, log)
	if err != nil {
		return err
	}
	if log.GetLevel() == logrus.DebugLevel {
		dotCmd.Args = append(dotCmd.Args, "--debug")
	}

	log.Debugf("Running dotfiles setup command: %v", dotCmd.Args)

	writer := log.Writer(logrus.InfoLevel, false)

	dotCmd.Stdout = writer
	dotCmd.Stderr = writer

	err = dotCmd.Run()
	if err != nil {
		return err
	}

	log.Infof("Done setting up dotfiles into the devcontainer")

	return nil
}

func buildDotCmdAgentArguments(devPodConfig *config.Config, dotfilesRepo, dotfilesScript string, log log.Logger) []string {
	agentArguments := []string{
		"agent",
		"workspace",
		"install-dotfiles",
		"--repository",
		dotfilesRepo,
	}

	if devPodConfig.ContextOption(config.ContextOptionSSHStrictHostKeyChecking) == "true" {
		agentArguments = append(agentArguments, "--strict-host-key-checking")
	}

	if log.GetLevel() == logrus.DebugLevel {
		agentArguments = append(agentArguments, "--debug")
	}

	if dotfilesScript != "" {
		log.Infof("Dotfiles script %s specified", dotfilesScript)
		agentArguments = append(agentArguments, "--install-script", dotfilesScript)
	}

	return agentArguments
}

func buildDotCmd(devPodConfig *config.Config, dotfilesRepo, dotfilesScript string, envFiles, envKeyValuePairs []string, client client2.BaseWorkspaceClient, log log.Logger) (*exec.Cmd, error) {
	sshCmd := []string{
		"ssh",
		"--agent-forwarding=true",
		"--start-services=true",
	}

	envFilesKeyValuePairs, err := collectDotfilesScriptEnvKeyvaluePairs(envFiles)
	if err != nil {
		return nil, err
	}

	// Collect file-based and CLI options env variables names (aka keys) and
	// configure ssh env var passthrough with send-env
	allEnvKeyValuesPairs := slices.Concat(envFilesKeyValuePairs, envKeyValuePairs)
	allEnvKeys := extractKeysFromEnvKeyValuePairs(allEnvKeyValuesPairs)
	for _, envKey := range allEnvKeys {
		sshCmd = append(sshCmd, "--send-env", envKey)
	}

	remoteUser, err := devssh.GetUser(client.WorkspaceConfig().ID, client.WorkspaceConfig().SSHConfigPath)
	if err != nil {
		remoteUser = "root"
	}

	agentArguments := buildDotCmdAgentArguments(devPodConfig, dotfilesRepo, dotfilesScript, log)
	sshCmd = append(sshCmd,
		"--user",
		remoteUser,
		"--context",
		client.Context(),
		client.Workspace(),
		"--log-output=raw",
		"--command",
		agent.ContainerDevPodHelperLocation+" "+strings.Join(agentArguments, " "),
	)
	execPath, err := os.Executable()
	if err != nil {
		return nil, err
	}

	dotCmd := exec.Command(
		execPath,
		sshCmd...,
	)

	dotCmd.Env = append(dotCmd.Environ(), allEnvKeyValuesPairs...)
	return dotCmd, nil
}

func extractKeysFromEnvKeyValuePairs(envKeyValuePairs []string) []string {
	keys := []string{}
	for _, env := range envKeyValuePairs {
		keyValue := strings.SplitN(env, "=", 2)
		if len(keyValue) == 2 {
			keys = append(keys, keyValue[0])
		}
	}
	return keys
}

func collectDotfilesScriptEnvKeyvaluePairs(envFiles []string) ([]string, error) {
	keyValues := []string{}
	for _, file := range envFiles {
		envFromFile, err := config2.ParseKeyValueFile(file)
		if err != nil {
			return nil, err
		}
		keyValues = append(keyValues, envFromFile...)
	}
	return keyValues, nil
}

func setupGitSSHSignature(signingKey string, client client2.BaseWorkspaceClient, log log.Logger) error {
	execPath, err := os.Executable()
	if err != nil {
		return err
	}

	remoteUser, err := devssh.GetUser(client.WorkspaceConfig().ID, client.WorkspaceConfig().SSHConfigPath)
	if err != nil {
		remoteUser = "root"
	}

	err = exec.Command(
		execPath,
		"ssh",
		"--agent-forwarding=true",
		"--start-services=true",
		"--user",
		remoteUser,
		"--context",
		client.Context(),
		client.Workspace(),
		"--command", fmt.Sprintf("devpod agent git-ssh-signature-helper %s", signingKey),
	).Run()
	if err != nil {
		log.Error("failure in setting up git ssh signature helper")
	}
	return nil
}

func performGpgForwarding(
	client client2.BaseWorkspaceClient,
	log log.Logger,
) error {
	log.Debug("gpg forwarding enabled, performing immediately")

	execPath, err := os.Executable()
	if err != nil {
		return err
	}

	remoteUser, err := devssh.GetUser(client.WorkspaceConfig().ID, client.WorkspaceConfig().SSHConfigPath)
	if err != nil {
		remoteUser = "root"
	}

	log.Info("forwarding gpg-agent")

	// perform in background an ssh command forwarding the
	// gpg agent, in order to have it immediately take effect
	go func() {
		err = exec.Command(
			execPath,
			"ssh",
			"--gpg-agent-forwarding=true",
			"--agent-forwarding=true",
			"--start-services=true",
			"--user",
			remoteUser,
			"--context",
			client.Context(),
			client.Workspace(),
			"--log-output=raw",
			"--command", "sleep infinity",
		).Run()
		if err != nil {
			log.Error("failure in forwarding gpg-agent")
		}
	}()

	return nil
}

func (cmd *UpCmd) prepareClient(ctx context.Context, devPodConfig *config.Config, args []string) (client2.WorkspaceClient, log.Logger, error) {
	var logger log.Logger = log.Default

	if err := mergeEnvFromFiles(&cmd.CLIOptions); err != nil {
		return nil, logger, err
	}

	var source *provider2.WorkspaceSource
	if cmd.Source != "" {
		source = provider2.ParseWorkspaceSource(cmd.Source)
		if source == nil {
			return nil, nil, fmt.Errorf("workspace source is missing")
		}
	}

	if cmd.SSHConfigPath == "" {
		cmd.SSHConfigPath = devPodConfig.ContextOption(config.ContextOptionSSHConfigPath)
	}

	client, err := workspace2.Resolve(
		ctx,
		devPodConfig,
		cmd.IDE,
		cmd.IDEOptions,
		args,
		cmd.ID,
		cmd.Machine,
		cmd.ProviderOptions,
		cmd.DevContainerImage,
		cmd.DevContainerPath,
		cmd.SSHConfigPath,
		source,
		cmd.UID,
		true,
		logger,
	)
	if err != nil {
		return nil, logger, err
	}

	return client, logger, nil
}

func WithSignals(ctx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGQUIT)
	go func() {
		select {
		case <-signals:
			cancel()
		case <-ctx.Done():
		}
	}()

	go func() {
		<-ctx.Done()
		<-signals
		// force shutdown if context is done and we receive another signal
		os.Exit(1)
	}()

	return ctx, func() {
		cancel()
		signal.Stop(signals)
	}
}
