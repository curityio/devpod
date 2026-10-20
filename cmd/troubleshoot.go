package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/loft-sh/devpod/cmd/completion"
	"github.com/loft-sh/devpod/cmd/flags"
	"github.com/loft-sh/devpod/cmd/provider"
	"github.com/loft-sh/devpod/pkg/client"
	"github.com/loft-sh/devpod/pkg/config"
	"github.com/loft-sh/devpod/pkg/log"
	pkgprovider "github.com/loft-sh/devpod/pkg/provider"
	"github.com/loft-sh/devpod/pkg/version"
	"github.com/loft-sh/devpod/pkg/workspace"
	"github.com/spf13/cobra"
)

type TroubleshootCmd struct {
	*flags.GlobalFlags
}

func NewTroubleshootCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &TroubleshootCmd{
		GlobalFlags: flags,
	}
	troubleshootCmd := &cobra.Command{
		Use:   "troubleshoot [workspace-path|workspace-name]",
		Short: "Prints the workspaces troubleshooting information",
		Run: func(cobraCmd *cobra.Command, args []string) {
			cmd.Run(cobraCmd.Context(), args)
		},
		ValidArgsFunction: func(rootCmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			return completion.GetWorkspaceSuggestions(rootCmd, cmd.Context, cmd.Provider, args, toComplete, log.Default)
		},
		Hidden: true,
	}

	return troubleshootCmd
}

func (cmd *TroubleshootCmd) Run(ctx context.Context, args []string) {
	// (ThomasK33): We're creating an anonymous struct here, so that we group
	// everything and then we can serialize it in one call.
	var info struct {
		CLIVersion      string
		Config          *config.Config
		Providers       map[string]provider.ProviderWithDefault
		Workspace       *pkgprovider.Workspace
		WorkspaceStatus client.Status

		Errors []PrintableError `json:",omitempty"`
	}
	info.CLIVersion = version.GetVersion()

	// (ThomasK33): We are defering the printing here, as we want to make sure
	// that we will always print, even in the case of a panic.
	defer func() {
		out, err := json.MarshalIndent(info, "", "  ")
		if err == nil {
			fmt.Print(string(out))
		} else {
			fmt.Print(err)
			fmt.Print(info)
		}
	}()

	// NOTE(ThomasK33): Since this is a troubleshooting command, we want to
	// collect as many relevant information as possible.
	// For this reason we may not return with an error early.
	// We are fine with a partially filled TrbouelshootInfo struct, as this
	// already provides us with more information then before.
	var err error
	info.Config, err = config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		info.Errors = append(info.Errors, PrintableError{fmt.Errorf("load config: %w", err)})
		// (ThomasK33): It's fine to return early here, as without the devpod config
		// we cannot do any further troubleshooting.
		return
	}

	logger := log.Default.ErrorStreamOnly()
	info.Providers, err = collectProviders(info.Config, logger)
	if err != nil {
		info.Errors = append(info.Errors, PrintableError{fmt.Errorf("collect providers: %w", err)})
	}

	workspaceClient, err := workspace.Get(ctx, info.Config, args, false, false, logger)
	if err == nil {
		info.Workspace = workspaceClient.WorkspaceConfig()
		info.WorkspaceStatus, err = workspaceClient.Status(ctx, client.StatusOptions{})
		if err != nil {
			info.Errors = append(info.Errors, PrintableError{fmt.Errorf("workspace status: %w", err)})
		}
	} else {
		info.Errors = append(info.Errors, PrintableError{fmt.Errorf("get workspace: %w", err)})
	}
}

// collectProviders collects and configures providers based on the given devPodConfig.
// It returns a map of providers with their default settings and an error if any occurs.
func collectProviders(devPodConfig *config.Config, logger log.Logger) (map[string]provider.ProviderWithDefault, error) {
	providers, err := workspace.LoadAllProviders(devPodConfig, logger)
	if err != nil {
		return nil, err
	}

	configuredProviders := devPodConfig.Current().Providers
	if configuredProviders == nil {
		configuredProviders = map[string]*config.ProviderConfig{}
	}

	retMap := map[string]provider.ProviderWithDefault{}
	for k, entry := range providers {
		if configuredProviders[entry.Config.Name] == nil {
			continue
		}

		srcOptions := provider.MergeDynamicOptions(entry.Config.Options, configuredProviders[entry.Config.Name].DynamicOptions)
		entry.Config.Options = srcOptions
		retMap[k] = provider.ProviderWithDefault{
			ProviderWithOptions: *entry,
			Default:             devPodConfig.Current().DefaultProvider == entry.Config.Name,
		}
	}

	return retMap, nil
}

// (ThomasK33): Little type embedding here, so that we can
// serialize the error strings when invoking json.Marshal.
type PrintableError struct{ error }

func (p PrintableError) MarshalJSON() ([]byte, error) { return json.Marshal(p.Error()) }
