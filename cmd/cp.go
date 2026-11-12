package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"runtime"
	"strings"

	"github.com/alessio/shellescape"
	"github.com/curityio/devpod/cmd/completion"
	"github.com/curityio/devpod/cmd/flags"
	"github.com/curityio/devpod/pkg/agent"
	client2 "github.com/curityio/devpod/pkg/client"
	"github.com/curityio/devpod/pkg/config"
	"github.com/curityio/devpod/pkg/filetransfer"
	"github.com/curityio/devpod/pkg/log"
	devssh "github.com/curityio/devpod/pkg/ssh"
	sshserver "github.com/curityio/devpod/pkg/ssh/server"
	"github.com/curityio/devpod/pkg/tunnel"
	workspace2 "github.com/curityio/devpod/pkg/workspace"
	"github.com/pkg/sftp"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
)

// CpCmd holds the cp cmd flags
type CpCmd struct {
	*flags.GlobalFlags

	User string
}

// NewCpCmd creates a new cp command
func NewCpCmd(f *flags.GlobalFlags) *cobra.Command {
	cmd := &CpCmd{
		GlobalFlags: f,
	}
	cpCmd := &cobra.Command{
		Use:   "cp [flags] SRC_PATH WORKSPACE:DEST_PATH | WORKSPACE:SRC_PATH DEST_PATH",
		Short: "Copies files and directories to or from a workspace",
		Long: `Copies files and directories between the local machine and a workspace's container.

A path in the container is written as WORKSPACE:PATH, like in scp. A relative PATH is relative to the workspace
folder, and an empty one is the workspace folder itself. To copy a local file whose name has a colon in it, start
its path with ./

Directories are copied with everything in them. Like docker cp, a file or directory is copied into DEST_PATH if
that is an existing directory, and to DEST_PATH otherwise. A DEST_PATH that ends with / must be a directory, and is
created if it's missing. A SRC_PATH that ends with /. copies the directory's contents instead of the directory.`,
		Example: `  # copy a file into the workspace folder
  devpod cp my-file.txt my-workspace:

  # copy a file to a path relative to the workspace folder, under another name
  devpod cp ./here/file-here.txt my-workspace:file-in-container.txt

  # copy a directory into an absolute path in the container
  devpod cp ./temp/ my-workspace:/usr/local/

  # copy a file from the container to the current directory
  devpod cp my-workspace:/etc/os-release .`,
		Args: cobra.ExactArgs(2),
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			src, err := parseCpLocation(args[0], runtime.GOOS)
			if err != nil {
				return err
			}
			dst, err := parseCpLocation(args[1], runtime.GOOS)
			if err != nil {
				return err
			}
			if src.workspace != "" && dst.workspace != "" {
				return fmt.Errorf("can't copy between two workspaces: only one of %s and %s can be in a workspace", args[0], args[1])
			} else if src.workspace == "" && dst.workspace == "" {
				return fmt.Errorf("neither %s nor %s is in a workspace: write a path in a workspace as WORKSPACE:PATH", args[0], args[1])
			}
			if src.workspace == "" {
				// fail before connecting to the workspace
				if _, err := os.Stat(src.path); err != nil {
					return err
				}
			}

			devPodConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
			if err != nil {
				return err
			}

			workspaceID := src.workspace + dst.workspace
			ctx := cobraCmd.Context()
			client, err := workspace2.Get(ctx, devPodConfig, []string{workspaceID}, true, true, log.Default.ErrorStreamOnly())
			if err != nil {
				return err
			}

			return cmd.Run(ctx, devPodConfig, client, src, dst, log.Default)
		},
		ValidArgsFunction: func(rootCmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if strings.Contains(toComplete, ":") {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			workspaces, _ := completion.GetWorkspaceSuggestions(rootCmd, cmd.Context, cmd.Provider, nil, toComplete, log.Default)
			for i := range workspaces {
				workspaces[i] += ":"
			}
			return workspaces, cobra.ShellCompDirectiveDefault | cobra.ShellCompDirectiveNoSpace
		},
	}

	cpCmd.Flags().StringVar(&cmd.User, "user", "", "The user in the container to copy as, which owns the files copied into it. Defaults to the workspace's user")
	return cpCmd
}

// cpLocation is a path given to devpod cp, in a workspace if workspace isn't empty.
type cpLocation struct {
	workspace string
	path      string
}

func (l cpLocation) String() string {
	if l.workspace == "" {
		return l.path
	}
	return l.workspace + ":" + l.path
}

// parseCpLocation splits WORKSPACE:PATH, like scp: an argument is a local path if it has no colon, has a slash
// before its first colon, or (on Windows) starts with a drive letter.
func parseCpLocation(arg, goos string) (cpLocation, error) {
	workspace, p, found := strings.Cut(arg, ":")
	if !found || strings.ContainsAny(workspace, `/\`) || (goos == "windows" && isDriveLetter(workspace)) {
		return cpLocation{path: arg}, nil
	}
	if workspace == "" {
		return cpLocation{}, fmt.Errorf("%s has no workspace before the colon: write a path in a workspace as WORKSPACE:PATH, or a local path that starts with a colon as ./%s", arg, arg)
	}
	return cpLocation{workspace: workspace, path: p}, nil
}

func isDriveLetter(s string) bool {
	return len(s) == 1 && ('a' <= s[0] && s[0] <= 'z' || 'A' <= s[0] && s[0] <= 'Z')
}

// Run copies src to dst, one of which is in the workspace client is for
func (cmd *CpCmd) Run(
	ctx context.Context,
	devPodConfig *config.Config,
	client client2.WorkspaceClient,
	src, dst cpLocation,
	log log.Logger,
) error {
	workspaceConfig := client.WorkspaceConfig()
	user := cmd.User
	if user == "" {
		var err error
		user, err = devssh.GetUser(workspaceConfig.ID, workspaceConfig.SSHConfigPath)
		if err != nil {
			return err
		}
	}

	workdir, err := devssh.GetWorkdir(workspaceConfig.ID, workspaceConfig.SSHConfigPath)
	if err != nil {
		return err
	}
	if workdir == "" {
		workdir = path.Join("/workspaces", client.Workspace())
	}

	// lock the workspace as long as we init the connection
	err = client.Lock(ctx)
	if err != nil {
		return err
	}
	defer client.Unlock()

	err = startWait(ctx, client, false, log)
	if err != nil {
		return err
	}

	return tunnel.NewContainerTunnel(client, log.ErrorStreamOnly()).
		Run(ctx, func(ctx context.Context, containerClient *ssh.Client) error {
			// we have a connection to the container, make sure others can connect as well
			client.Unlock()

			return withContainerSFTP(ctx, containerClient, user, workdir, log, func(sftpClient *sftp.Client) error {
				var srcFS, dstFS filetransfer.FS = filetransfer.LocalFS{}, filetransfer.LocalFS{}
				remote := filetransfer.SFTPFS{Client: sftpClient, WorkDir: workdir}
				if src.workspace != "" {
					srcFS = remote
				} else {
					dstFS = remote
				}

				stats, err := filetransfer.Copy(srcFS, src.path, dstFS, dst.path, log)
				if err != nil {
					var remoteErr *filetransfer.RemoteError
					if user != "root" && errors.As(err, &remoteErr) && errors.Is(remoteErr, fs.ErrPermission) {
						return fmt.Errorf("copy %s to %s as user %s: %w (use --user root to copy as root)", src, dst, user, err)
					}
					return fmt.Errorf("copy %s to %s: %w", src, dst, err)
				}
				log.Donef("Copied %s from %s to %s", stats, src, dst)
				return nil
			})
		}, devPodConfig, nil)
}

// withContainerSFTP starts an SSH server in the container as user, as devpod ssh does, and calls fn with an SFTP
// client connected to it.
func withContainerSFTP(
	ctx context.Context,
	containerClient *ssh.Client,
	user, workdir string,
	log log.Logger,
	fn func(*sftp.Client) error,
) error {
	command := containerSSHServerCommand(user, workdir, log.GetLevel() == logrus.DebugLevel)

	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		return err
	}
	defer stdoutReader.Close()
	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		return err
	}
	defer stdinReader.Close()
	defer stdinWriter.Close()

	writer := log.ErrorStreamOnly().Writer(logrus.InfoLevel, false)
	defer writer.Close()

	serverErr := make(chan error, 1)
	go func() {
		defer stdoutWriter.Close()
		log.Debugf("Run SSH server in container: %s", command)
		serverErr <- devssh.Run(ctx, containerClient, command, stdinReader, stdoutWriter, writer, nil)
	}()

	sshClient, err := devssh.StdioClientWithUser(stdoutReader, stdinWriter, user, false)
	if err != nil {
		select {
		case runErr := <-serverErr:
			if runErr != nil {
				return fmt.Errorf("start SSH server in container: %w", runErr)
			}
		default:
		}
		return fmt.Errorf("connect to SSH server in container: %w", err)
	}
	defer sshClient.Close()

	session, err := sshClient.NewSession()
	if err != nil {
		return fmt.Errorf("start SFTP session: %w", err)
	}
	defer session.Close()
	// create files and directories private, so that nothing is readable before its permissions are set; an older
	// agent ignores this, and Copy then restricts them right after creating them
	err = session.Setenv(sshserver.SFTPUmaskEnv, "077")
	if err != nil {
		log.Debugf("Set %s: %v", sshserver.SFTPUmaskEnv, err)
	}
	sftpIn, err := session.StdinPipe()
	if err != nil {
		return err
	}
	sftpOut, err := session.StdoutPipe()
	if err != nil {
		return err
	}
	err = session.RequestSubsystem("sftp")
	if err != nil {
		return fmt.Errorf("start SFTP session: %w", err)
	}
	sftpClient, err := sftp.NewClientPipe(sftpOut, sftpIn, sftp.UseConcurrentWrites(true))
	if err != nil {
		return fmt.Errorf("start SFTP session: %w", err)
	}
	defer sftpClient.Close()

	return fn(sftpClient)
}

// containerSSHServerCommand returns the shell command that starts an SSH server in the container as user, which
// tracks activity, like devpod ssh's, so that the workspace isn't stopped for inactivity while files are copied.
func containerSSHServerCommand(user, workdir string, debug bool) string {
	args := []string{agent.ContainerDevPodHelperLocation, "helper", "ssh-server", "--stdio", "--track-activity", "--workdir", workdir}
	if debug {
		args = append(args, "--debug")
	}
	command := shellescape.QuoteCommand(args)
	if user != "" && user != "root" {
		command = "su -c " + shellescape.Quote(command) + " " + shellescape.Quote(user)
	}
	return command
}
