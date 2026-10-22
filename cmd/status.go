package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/loft-sh/devpod/cmd/completion"
	"github.com/loft-sh/devpod/cmd/flags"
	client2 "github.com/loft-sh/devpod/pkg/client"
	"github.com/loft-sh/devpod/pkg/config"
	"github.com/loft-sh/devpod/pkg/log"
	"github.com/loft-sh/devpod/pkg/log/table"
	workspace2 "github.com/loft-sh/devpod/pkg/workspace"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

// maxConcurrentStatus is how many workspace statuses --all retrieves at the same time
const maxConcurrentStatus = 8

// StatusCmd holds the cmd flags
type StatusCmd struct {
	*flags.GlobalFlags
	client2.StatusOptions

	All     bool
	Output  string
	Timeout string
}

// NewStatusCmd creates a new command
func NewStatusCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &StatusCmd{
		GlobalFlags: flags,
	}
	statusCmd := &cobra.Command{
		Use:   "status [flags] [workspace-path|workspace-name]",
		Short: "Shows the status of a workspace",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			if cmd.All && len(args) > 0 {
				return fmt.Errorf("--all shows the status of all workspaces, so it can't be used with a workspace")
			}

			ctx := cobraCmd.Context()
			devPodConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
			if err != nil {
				return err
			}

			logger := log.Default.ErrorStreamOnly()
			if cmd.All {
				return cmd.RunAll(ctx, devPodConfig, logger)
			}

			client, err := workspace2.Get(ctx, devPodConfig, args, false, false, logger)
			if err != nil {
				return err
			}

			return cmd.Run(ctx, client, logger)
		},
		ValidArgsFunction: func(rootCmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			return completion.GetWorkspaceSuggestions(rootCmd, cmd.Context, cmd.Provider, args, toComplete, log.Default)
		},
	}

	statusCmd.Flags().BoolVar(&cmd.All, "all", false, "Shows the status of all workspaces of the context")
	statusCmd.Flags().BoolVar(&cmd.ContainerStatus, "container-status", true, "If enabled shows the workspace container status as well")
	statusCmd.Flags().StringVar(&cmd.Output, "output", "plain", "Status shows the workspace status")
	statusCmd.Flags().StringVar(&cmd.Timeout, "timeout", "30s", "The timeout to wait until the status can be retrieved")
	return statusCmd
}

// Run runs the command logic
func (cmd *StatusCmd) Run(ctx context.Context, client client2.BaseWorkspaceClient, log log.Logger) error {
	ctx, cancel, err := cmd.withTimeout(ctx)
	if err != nil {
		return err
	}
	defer cancel()

	// get instance status
	instanceStatus, err := client.Status(ctx, cmd.StatusOptions)
	if err != nil {
		return err
	}

	if cmd.Output == "plain" {
		if instanceStatus == client2.StatusStopped {
			log.Infof("Workspace '%s' is '%s', you can start it via 'devpod up %s'", client.Workspace(), instanceStatus, client.Workspace())
		} else if instanceStatus == client2.StatusBusy {
			log.Infof("Workspace '%s' is '%s', which means its currently unaccessible. This is usually resolved by waiting a couple of minutes", client.Workspace(), instanceStatus)
		} else if instanceStatus == client2.StatusNotFound {
			log.Infof("Workspace '%s' is '%s', you can create it via 'devpod up %s'", client.Workspace(), instanceStatus, client.Workspace())
		} else {
			log.Infof("Workspace '%s' is '%s'", client.Workspace(), instanceStatus)
		}
	} else if cmd.Output == "json" {
		out, err := json.Marshal(&client2.WorkspaceStatus{
			ID:       client.Workspace(),
			Context:  client.Context(),
			Provider: client.Provider(),
			State:    string(instanceStatus),
		})
		if err != nil {
			return err
		}

		fmt.Print(string(out))
	} else {
		return fmt.Errorf("unexpected output format, choose either json or plain. Got %s", cmd.Output)
	}

	return nil
}

// RunAll shows the status of all workspaces of the context. The statuses are retrieved concurrently, each
// within --timeout, and a workspace whose status can't be retrieved doesn't stop the others from being shown.
// In JSON the failures are part of the output, so only the plain output makes the command fail because of them.
func (cmd *StatusCmd) RunAll(ctx context.Context, devPodConfig *config.Config, logger log.Logger) error {
	if cmd.Output != "plain" && cmd.Output != "json" {
		return fmt.Errorf("unexpected output format, choose either json or plain. Got %s", cmd.Output)
	}

	timeout, err := cmd.parseTimeout()
	if err != nil {
		return err
	}

	workspaces, err := workspace2.ListClients(devPodConfig, logger)
	if err != nil {
		return err
	}

	statuses := cmd.allStatuses(ctx, devPodConfig.DefaultContext, workspaces, timeout)
	if cmd.Output == "json" {
		out, err := json.Marshal(statuses)
		if err != nil {
			return err
		}

		fmt.Print(string(out))
		return nil
	}

	return printStatuses(statuses, log.Default, logger)
}

// allStatuses retrieves the statuses of the workspaces concurrently, keeping their order
func (cmd *StatusCmd) allStatuses(ctx context.Context, contextName string, workspaces []workspace2.ListedClient, timeout time.Duration) []client2.WorkspaceStatus {
	options := cmd.StatusOptions
	options.MachineStatuses = &client2.MachineStatusCache{}

	statuses := make([]client2.WorkspaceStatus, len(workspaces))
	limit := make(chan struct{}, maxConcurrentStatus)
	wg := sync.WaitGroup{}
	for i, workspace := range workspaces {
		statuses[i] = client2.WorkspaceStatus{
			ID:      workspace.ID,
			Context: contextName,
		}
		if workspace.Workspace != nil {
			statuses[i].Provider = workspace.Workspace.Provider.Name
		}
		if workspace.Err != nil {
			statuses[i].Error = workspace.Err.Error()
			continue
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case limit <- struct{}{}:
				defer func() { <-limit }()
			case <-ctx.Done():
				statuses[i].Error = ctx.Err().Error()
				return
			}

			state, err := status(ctx, workspace.Client, options, timeout)
			if err != nil {
				statuses[i].Error = err.Error()
				return
			}
			statuses[i].State = string(state)
		}()
	}
	wg.Wait()

	return statuses
}

// status retrieves a workspace's status, giving up after the timeout (if it isn't 0)
func status(ctx context.Context, client client2.BaseWorkspaceClient, options client2.StatusOptions, timeout time.Duration) (client2.Status, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	status, err := client.Status(ctx, options)
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("timed out after %s, you can wait longer via --timeout", timeout)
	}

	return status, err
}

// printStatuses prints the statuses as a table to out, and why a status couldn't be retrieved to logger. It fails
// if a status couldn't be retrieved.
func printStatuses(statuses []client2.WorkspaceStatus, out log.Logger, logger log.Logger) error {
	if len(statuses) == 0 {
		out.Infof("There are no workspaces, you can create one via 'devpod up'")
		return nil
	}

	failed := 0
	tableEntries := [][]string{}
	for _, status := range statuses {
		state := status.State
		if status.Error != "" {
			failed++
			state = "Error"
			logger.Warnf("Couldn't get the status of workspace '%s': %s", status.ID, status.Error)
		}
		tableEntries = append(tableEntries, []string{status.ID, status.Provider, state})
	}

	table.PrintTable(out, []string{
		"Name",
		"Provider",
		"Status",
	}, tableEntries)

	if failed > 0 {
		return fmt.Errorf("couldn't get the status of %d of %d workspaces", failed, len(statuses))
	}

	return nil
}

// withTimeout applies --timeout to the context
func (cmd *StatusCmd) withTimeout(ctx context.Context) (context.Context, context.CancelFunc, error) {
	timeout, err := cmd.parseTimeout()
	if err != nil {
		return nil, nil, err
	} else if timeout == 0 {
		return ctx, func() {}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	return ctx, cancel, nil
}

// parseTimeout parses --timeout, which is 0 if it isn't set
func (cmd *StatusCmd) parseTimeout() (time.Duration, error) {
	if cmd.Timeout == "" {
		return 0, nil
	}

	timeout, err := time.ParseDuration(cmd.Timeout)
	if err != nil {
		return 0, errors.Wrap(err, "parse --timeout")
	}

	return timeout, nil
}
