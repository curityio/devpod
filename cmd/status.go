package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
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

// RunAll shows the status of all workspaces of the context. The statuses are retrieved concurrently,
// and a workspace whose status can't be retrieved doesn't stop the others from being shown.
func (cmd *StatusCmd) RunAll(ctx context.Context, devPodConfig *config.Config, logger log.Logger) error {
	if cmd.Output != "plain" && cmd.Output != "json" {
		return fmt.Errorf("unexpected output format, choose either json or plain. Got %s", cmd.Output)
	}

	ctx, cancel, err := cmd.withTimeout(ctx)
	if err != nil {
		return err
	}
	defer cancel()

	workspaces, err := workspace2.List(ctx, devPodConfig, logger)
	if err != nil {
		return err
	}
	sort.SliceStable(workspaces, func(i, j int) bool {
		return workspaces[i].LastUsedTimestamp.Time.Unix() > workspaces[j].LastUsedTimestamp.Time.Unix()
	})

	statuses := make([]client2.WorkspaceStatus, len(workspaces))
	limit := make(chan struct{}, maxConcurrentStatus)
	wg := sync.WaitGroup{}
	for i, workspace := range workspaces {
		statuses[i] = client2.WorkspaceStatus{
			ID:       workspace.ID,
			Context:  workspace.Context,
			Provider: workspace.Provider.Name,
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			limit <- struct{}{}
			defer func() { <-limit }()

			state, err := cmd.workspaceStatus(ctx, devPodConfig, workspace.ID, logger)
			if err != nil {
				statuses[i].Error = err.Error()
				return
			}
			statuses[i].State = string(state)
		}()
	}
	wg.Wait()

	failed := 0
	for _, status := range statuses {
		if status.Error != "" {
			failed++
		}
	}

	if cmd.Output == "json" {
		out, err := json.Marshal(statuses)
		if err != nil {
			return err
		}

		fmt.Print(string(out))
	} else if len(statuses) == 0 {
		logger.Infof("There are no workspaces, you can create one via 'devpod up'")
	} else {
		tableEntries := [][]string{}
		for _, status := range statuses {
			state := status.State
			if status.Error != "" {
				state = "Error"
				logger.Warnf("Couldn't get the status of workspace '%s': %s", status.ID, status.Error)
			}
			tableEntries = append(tableEntries, []string{status.ID, status.Provider, state})
		}

		table.PrintTable(log.Default, []string{
			"Name",
			"Provider",
			"Status",
		}, tableEntries)
	}

	if failed > 0 {
		return fmt.Errorf("couldn't get the status of %d of %d workspaces", failed, len(statuses))
	}

	return nil
}

func (cmd *StatusCmd) workspaceStatus(ctx context.Context, devPodConfig *config.Config, workspaceID string, logger log.Logger) (client2.Status, error) {
	client, err := workspace2.GetByID(devPodConfig, workspaceID, logger)
	if err != nil {
		return "", err
	}

	return client.Status(ctx, cmd.StatusOptions)
}

// withTimeout applies --timeout to the context
func (cmd *StatusCmd) withTimeout(ctx context.Context) (context.Context, context.CancelFunc, error) {
	if cmd.Timeout == "" {
		return ctx, func() {}, nil
	}

	duration, err := time.ParseDuration(cmd.Timeout)
	if err != nil {
		return nil, nil, errors.Wrap(err, "parse --timeout")
	}

	ctx, cancel := context.WithTimeout(ctx, duration)
	return ctx, cancel, nil
}
