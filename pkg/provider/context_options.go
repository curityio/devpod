package provider

import (
	"context"
	"strconv"

	"github.com/loft-sh/devpod/pkg/config"
	"github.com/loft-sh/devpod/pkg/git"
)

// ApplyContextOptions sets the CLI options that the agent needs from the context options, so that every command
// that starts the container, `devpod up` as well as `devpod ssh` on a stopped workspace, sets it up the same way.
// It reads the remote urls of a local folder source on the host, because the repository's git dir can be outside
// of the folder (e.g. for a worktree), where the container can't see it.
func ApplyContextOptions(ctx context.Context, options *CLIOptions, devPodConfig *config.Config, source WorkspaceSource) error {
	hosts := devPodConfig.GitSSHToHTTPSHosts()
	for _, host := range hosts {
		if err := git.ValidateSSHToHTTPSHost(host); err != nil {
			return err
		}
	}

	options.GitSSHToHTTPS = strconv.FormatBool(devPodConfig.GitSSHToHTTPS())
	options.GitSSHToHTTPSHosts = hosts
	options.GitSSHToHTTPSRemotes = nil
	if devPodConfig.GitSSHToHTTPS() && source.LocalFolder != "" {
		options.GitSSHToHTTPSRemotes = git.RemoteURLs(ctx, source.LocalFolder)
	}
	options.DisableSSHAgentForwarding = devPodConfig.ContextOption(config.ContextOptionSSHAgentForwarding) == "false"
	return nil
}
