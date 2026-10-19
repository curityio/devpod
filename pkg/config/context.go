package config

import "strings"

const (
	ContextOptionSSHAddPrivateKeys          = "SSH_ADD_PRIVATE_KEYS"
	ContextOptionGPGAgentForwarding         = "GPG_AGENT_FORWARDING"
	ContextOptionGitSSHSignatureForwarding  = "GIT_SSH_SIGNATURE_FORWARDING"
	ContextOptionSSHInjectDockerCredentials = "SSH_INJECT_DOCKER_CREDENTIALS"
	ContextOptionSSHInjectGitCredentials    = "SSH_INJECT_GIT_CREDENTIALS"
	ContextOptionExitAfterTimeout           = "EXIT_AFTER_TIMEOUT"
	ContextOptionDotfilesURL                = "DOTFILES_URL"
	ContextOptionDotfilesScript             = "DOTFILES_SCRIPT"
	ContextOptionSSHAgentForwarding         = "SSH_AGENT_FORWARDING"
	ContextOptionSSHConfigPath              = "SSH_CONFIG_PATH"
	ContextOptionAgentInjectTimeout         = "AGENT_INJECT_TIMEOUT"
	ContextOptionRegistryCache              = "REGISTRY_CACHE"
	ContextOptionSSHStrictHostKeyChecking   = "SSH_STRICT_HOST_KEY_CHECKING"
	ContextOptionGitSSHToHTTPS              = "GIT_SSH_TO_HTTPS"
	ContextOptionGitSSHToHTTPSHosts         = "GIT_SSH_TO_HTTPS_HOSTS"
)

var ContextOptions = []ContextOption{
	{
		Name:        ContextOptionSSHAddPrivateKeys,
		Description: "Specifies if DevPod should automatically add ssh-keys to the ssh-agent",
		Default:     "true",
		Enum:        []string{"true", "false"},
	},
	{
		Name:        ContextOptionExitAfterTimeout,
		Description: "Specifies if DevPod should exit the process after the browser has been idle for a minute",
		Default:     "true",
		Enum:        []string{"true", "false"},
	},
	{
		Name:        ContextOptionGPGAgentForwarding,
		Description: "Specifies if DevPod should do gpg-agent forwarding by default for ssh",
		Default:     "false",
		Enum:        []string{"true", "false"},
	},
	{
		Name:        ContextOptionGitSSHSignatureForwarding,
		Description: "Specifies if DevPod should automatically detect ssh signature git setting and inject ssh signature helper",
		Default:     "true",
		Enum:        []string{"true", "false"},
	},
	{
		Name:        ContextOptionSSHInjectDockerCredentials,
		Description: "Specifies if DevPod should inject docker credentials into the workspace",
		Default:     "true",
		Enum:        []string{"true", "false"},
	},
	{
		Name:        ContextOptionSSHInjectGitCredentials,
		Description: "Specifies if DevPod should inject git credentials into the workspace",
		Default:     "true",
		Enum:        []string{"true", "false"},
	},
	{
		Name:        ContextOptionSSHAgentForwarding,
		Description: "Specifies if DevPod should do agent forwarding by default into the workspace",
		Default:     "true",
		Enum:        []string{"true", "false"},
	},
	{
		Name:        ContextOptionDotfilesURL,
		Description: "Specifies the dotfiles repo url to use for DevPod",
	},
	{
		Name:        ContextOptionDotfilesScript,
		Description: "Specifies the script to run after cloning dotfiles repo to install them",
	},
	{
		Name:        ContextOptionSSHConfigPath,
		Description: "Specifies the path where the ssh config should be written to",
	},
	{
		Name:        ContextOptionAgentInjectTimeout,
		Description: "Specifies the timeout to inject the agent",
		Default:     "20",
	},
	{
		Name:        ContextOptionRegistryCache,
		Description: "Specifies the registry to use as a build cache, e.g. gcr.io/my-project/my-dev-env",
		Default:     "",
	},
	{
		Name:        ContextOptionSSHStrictHostKeyChecking,
		Description: "Enables strict ssh host key checking for all operations",
		Default:     "false",
		Enum:        []string{"true", "false"},
	},
	{
		Name:        ContextOptionGitSSHToHTTPS,
		Description: "Specifies if git inside the workspace should use https instead of ssh for remotes. auto does this when SSH_AGENT_FORWARDING is false",
		Default:     "auto",
		Enum:        []string{"auto", "true", "false"},
	},
	{
		Name:        ContextOptionGitSSHToHTTPSHosts,
		Description: "Comma separated list of extra hosts (e.g. git.example.com) or explicit rewrites (e.g. ssh://git@git.example.com:7999/=https://git.example.com/scm/) used by GIT_SSH_TO_HTTPS",
		Default:     "",
	},
}

// GitSSHToHTTPS reports if git inside workspaces should use https instead of ssh for remotes.
func (c *Config) GitSSHToHTTPS() bool {
	switch c.ContextOption(ContextOptionGitSSHToHTTPS) {
	case "true":
		return true
	case "false":
		return false
	default:
		return c.ContextOption(ContextOptionSSHAgentForwarding) == "false"
	}
}

// GitSSHToHTTPSHosts returns the extra hosts and explicit rewrites configured for GitSSHToHTTPS.
func (c *Config) GitSSHToHTTPSHosts() []string {
	hosts := []string{}
	for _, host := range strings.Split(c.ContextOption(ContextOptionGitSSHToHTTPSHosts), ",") {
		if host = strings.TrimSpace(host); host != "" {
			hosts = append(hosts, host)
		}
	}
	return hosts
}
