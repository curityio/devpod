package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/alessio/shellescape"
	"github.com/curityio/devpod/cmd/flags"
	"github.com/curityio/devpod/pkg/client"
	"github.com/curityio/devpod/pkg/config"
	"github.com/curityio/devpod/pkg/log"
	provider2 "github.com/curityio/devpod/pkg/provider"
	"github.com/curityio/devpod/pkg/workspace"
	"github.com/spf13/cobra"
)

// LogsDaemonCmd holds the configuration
type LogsDaemonCmd struct {
	*flags.GlobalFlags
}

// NewLogsDaemonCmd creates a new destroy command
func NewLogsDaemonCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &LogsDaemonCmd{
		GlobalFlags: flags,
	}
	startCmd := &cobra.Command{
		Use:   "logs-daemon",
		Short: "Prints the daemon logs on the machine",
		RunE: func(_ *cobra.Command, args []string) error {
			return cmd.Run(context.Background(), args)
		},
	}

	return startCmd
}

// Run runs the command logic
func (cmd *LogsDaemonCmd) Run(ctx context.Context, args []string) error {
	devPodConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}

	workspaceClient, err := workspace.Get(ctx, devPodConfig, args, false, false, log.Default)
	if err != nil {
		return err
	} else if workspaceClient.WorkspaceConfig().Machine.ID == "" {
		return fmt.Errorf("selected workspace is not a machine provider, there is not daemon running")
	}

	_, agentInfo, err := workspaceClient.AgentInfo(provider2.CLIOptions{})
	if err != nil {
		return err
	}

	agentArgs := []string{workspaceClient.AgentPath(), "agent", "workspace", "logs-daemon", "--context", workspaceClient.Context(), "--id", workspaceClient.Workspace()}
	if agentInfo.Agent.DataPath != "" {
		agentArgs = append(agentArgs, "--agent-dir", agentInfo.Agent.DataPath)
	}
	command := shellescape.QuoteCommand(agentArgs)

	// read daemon logs
	return workspaceClient.Command(ctx, client.CommandOptions{
		Command: command,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
	})
}
