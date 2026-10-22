package cmd

import (
	"github.com/loft-sh/devpod/pkg/log"
	"github.com/loft-sh/devpod/pkg/upgrade"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

// UpgradeCmd is a struct that defines a command call for "upgrade"
type UpgradeCmd struct {
	log     log.Logger
	Version string
	DryRun  bool
}

// NewUpgradeCmd creates a new upgrade command
func NewUpgradeCmd() *cobra.Command {
	cmd := &UpgradeCmd{log: log.GetInstance()}
	upgradeCmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade the DevPod CLI to the newest version",
		Args:  cobra.NoArgs,
		RunE:  cmd.Run,
	}

	upgradeCmd.Flags().StringVar(&cmd.Version, "version", "", "The release to install, such as v0.8.0. Defaults to the latest stable release, if it is newer than this CLI")
	upgradeCmd.Flags().BoolVar(&cmd.DryRun, "dry-run", false, "Only check whether a newer version is available and whether the CLI could be replaced, without downloading anything")
	return upgradeCmd
}

// Run executes the command logic
func (cmd *UpgradeCmd) Run(cobraCmd *cobra.Command, _ []string) error {
	err := upgrade.Upgrade(cobraCmd.Context(), upgrade.Options{Version: cmd.Version, DryRun: cmd.DryRun}, cmd.log)
	if err != nil {
		return errors.Errorf("unable to upgrade: %v", err)
	}

	return nil
}
