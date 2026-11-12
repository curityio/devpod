package ssh

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestAddHostSection(t *testing.T) {
	tests := []struct {
		name string
		// goos defaults to linux
		goos      string
		config    string
		execPath  string
		host      string
		user      string
		context   string
		workspace string
		workdir   string
		gpgagent  bool
		// noAgentForwarding is the inverse of addHostSection's forwardAgent, so that the cases default to forwarding
		noAgentForwarding bool
		devPodHome        string
		expected          string
	}{
		{
			name:       "Basic host addition",
			config:     "",
			execPath:   "/path/to/exec",
			host:       "testhost",
			user:       "testuser",
			context:    "testcontext",
			workspace:  "testworkspace",
			workdir:    "",
			gpgagent:   false,
			devPodHome: "",
			expected: `# DevPod Start testhost
Host testhost
  ForwardAgent yes
  LogLevel error
  StrictHostKeyChecking no
  UserKnownHostsFile /dev/null
  HostKeyAlgorithms rsa-sha2-256,rsa-sha2-512,ssh-rsa
  ProxyCommand /path/to/exec ssh --stdio --context testcontext --user testuser testworkspace
  User testuser
# DevPod End testhost`,
		},
		{
			name:              "Host addition without agent forwarding",
			config:            "",
			execPath:          "/path/to/exec",
			host:              "testhost",
			user:              "testuser",
			context:           "testcontext",
			workspace:         "testworkspace",
			noAgentForwarding: true,
			expected: `# DevPod Start testhost
Host testhost
  ForwardAgent no
  LogLevel error
  StrictHostKeyChecking no
  UserKnownHostsFile /dev/null
  HostKeyAlgorithms rsa-sha2-256,rsa-sha2-512,ssh-rsa
  ProxyCommand /path/to/exec ssh --stdio --context testcontext --user testuser testworkspace
  User testuser
# DevPod End testhost`,
		},
		{
			name:       "Basic host addition with DEVPOD_HOME on Windows",
			goos:       "windows",
			config:     "",
			execPath:   "/path/to/exec",
			host:       "testhost",
			user:       "testuser",
			context:    "testcontext",
			workspace:  "testworkspace",
			workdir:    "",
			gpgagent:   false,
			devPodHome: "C:\\\\White Space\\devpod\\test",
			expected: `# DevPod Start testhost
Host testhost
  ForwardAgent yes
  LogLevel error
  StrictHostKeyChecking no
  UserKnownHostsFile /dev/null
  HostKeyAlgorithms rsa-sha2-256,rsa-sha2-512,ssh-rsa
  ProxyCommand "/path/to/exec" ssh --stdio --context testcontext --user testuser testworkspace --devpod-home "C:\\White Space\devpod\test"
  User testuser
# DevPod End testhost`,
		},
		{
			name:       "Host addition with workdir",
			config:     "",
			execPath:   "/path/to/exec",
			host:       "testhost",
			user:       "testuser",
			context:    "testcontext",
			workspace:  "testworkspace",
			workdir:    "/path/to/workdir",
			gpgagent:   false,
			devPodHome: "",
			expected: `# DevPod Start testhost
Host testhost
  ForwardAgent yes
  LogLevel error
  StrictHostKeyChecking no
  UserKnownHostsFile /dev/null
  HostKeyAlgorithms rsa-sha2-256,rsa-sha2-512,ssh-rsa
  ProxyCommand /path/to/exec ssh --stdio --context testcontext --user testuser testworkspace --workdir /path/to/workdir
  User testuser
# DevPod End testhost`,
		},
		{
			name:       "Host addition with gpg agent",
			config:     "",
			execPath:   "/path/to/exec",
			host:       "testhost",
			user:       "testuser",
			context:    "testcontext",
			workspace:  "testworkspace",
			workdir:    "",
			gpgagent:   true,
			devPodHome: "",
			expected: `# DevPod Start testhost
Host testhost
  ForwardAgent yes
  LogLevel error
  StrictHostKeyChecking no
  UserKnownHostsFile /dev/null
  HostKeyAlgorithms rsa-sha2-256,rsa-sha2-512,ssh-rsa
  ProxyCommand /path/to/exec ssh --stdio --context testcontext --user testuser testworkspace --gpg-agent-forwarding
  User testuser
# DevPod End testhost`,
		},
		{
			name: "Host addition to existing config",
			config: `Host existinghost
  User existinguser`,
			execPath:   "/path/to/exec",
			host:       "testhost",
			user:       "testuser",
			context:    "testcontext",
			workspace:  "testworkspace",
			workdir:    "",
			gpgagent:   false,
			devPodHome: "",
			expected: `# DevPod Start testhost
Host testhost
  ForwardAgent yes
  LogLevel error
  StrictHostKeyChecking no
  UserKnownHostsFile /dev/null
  HostKeyAlgorithms rsa-sha2-256,rsa-sha2-512,ssh-rsa
  ProxyCommand /path/to/exec ssh --stdio --context testcontext --user testuser testworkspace
  User testuser
# DevPod End testhost
Host existinghost
  User existinguser`,
		},
		{
			name: "Host addition to existing config with DevPod host",
			config: `# DevPod Start existingtesthost
Host existingtesthost
  ForwardAgent yes
  LogLevel error
  StrictHostKeyChecking no
  UserKnownHostsFile /dev/null
  HostKeyAlgorithms rsa-sha2-256,rsa-sha2-512,ssh-rsa
  ProxyCommand /path/to/exec ssh --stdio --context testcontext --user testuser testworkspace
  User testuser
# DevPod End testhost

Host existinghost
  User existinguser`,
			execPath:   "/path/to/exec",
			host:       "testhost",
			user:       "testuser",
			context:    "testcontext",
			workspace:  "testworkspace",
			workdir:    "",
			gpgagent:   false,
			devPodHome: "",
			expected: `# DevPod Start testhost
Host testhost
  ForwardAgent yes
  LogLevel error
  StrictHostKeyChecking no
  UserKnownHostsFile /dev/null
  HostKeyAlgorithms rsa-sha2-256,rsa-sha2-512,ssh-rsa
  ProxyCommand /path/to/exec ssh --stdio --context testcontext --user testuser testworkspace
  User testuser
# DevPod End testhost
# DevPod Start existingtesthost
Host existingtesthost
  ForwardAgent yes
  LogLevel error
  StrictHostKeyChecking no
  UserKnownHostsFile /dev/null
  HostKeyAlgorithms rsa-sha2-256,rsa-sha2-512,ssh-rsa
  ProxyCommand /path/to/exec ssh --stdio --context testcontext --user testuser testworkspace
  User testuser
# DevPod End testhost

Host existinghost
  User existinguser`,
		},
		{
			name: "Host addition after top level includes",
			config: `Include ~/config1 

Include ~/config2



Include ~/config3`,
			execPath:   "/path/to/exec",
			host:       "testhost",
			user:       "testuser",
			context:    "testcontext",
			workspace:  "testworkspace",
			workdir:    "",
			gpgagent:   false,
			devPodHome: "",
			expected: `Include ~/config1 

Include ~/config2



Include ~/config3
# DevPod Start testhost
Host testhost
  ForwardAgent yes
  LogLevel error
  StrictHostKeyChecking no
  UserKnownHostsFile /dev/null
  HostKeyAlgorithms rsa-sha2-256,rsa-sha2-512,ssh-rsa
  ProxyCommand /path/to/exec ssh --stdio --context testcontext --user testuser testworkspace
  User testuser
# DevPod End testhost`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			goos := tt.goos
			if goos == "" {
				goos = "linux"
			}
			result, err := addHostSection(goos, tt.config, tt.execPath, tt.host, tt.user, tt.context, tt.workspace, tt.workdir, tt.gpgagent, !tt.noAgentForwarding, tt.devPodHome)
			if err != nil {
				t.Errorf("Failed with err: %v", err)
			}

			// addHostSection joins lines with \r\n on Windows
			expected, config := tt.expected, tt.config
			if goos == "windows" {
				expected = strings.ReplaceAll(expected, "\n", "\r\n")
				config = strings.ReplaceAll(config, "\n", "\r\n")
			}

			if result != expected {
				t.Errorf("addHostSection result does not match expected.\nGot:\n%s\nExpected:\n%s", result, expected)
				t.Errorf("addHostSection result does not match expected:\n%s", cmp.Diff(result, expected))
			}

			if !strings.Contains(result, MarkerEndPrefix+tt.host) {
				t.Errorf("Result does not contain the end marker: %s", MarkerEndPrefix+tt.host)
			}

			if !strings.Contains(result, "Host "+tt.host) {
				t.Errorf("Result does not contain the Host line: Host %s", tt.host)
			}

			if !strings.Contains(result, "User "+tt.user) {
				t.Errorf("Result does not contain the User line: User %s", tt.user)
			}

			if tt.workdir != "" && !strings.Contains(result, "--workdir "+tt.workdir) {
				t.Errorf("Result does not contain the workdir: %s", tt.workdir)
			}

			if tt.gpgagent && !strings.Contains(result, "--gpg-agent-forwarding") {
				t.Errorf("Result does not contain gpg-agent-forwarding flag")
			}

			if config != "" && !strings.Contains(result, config) {
				t.Errorf("Result does not contain the original config")
			}
		})
	}
}

func TestParseWorkdirFlag(t *testing.T) {
	workdirs := append([]string{"", "/workspaces/foo", "/home/me/my project"}, awkwardProxyCommandValues...)
	for _, goos := range []string{"linux", "darwin", "windows"} {
		for _, workdir := range workdirs {
			config, err := addHostSection(goos, "", "/path/to/my devpod", "foo.devpod", "me", "default", "foo", workdir, false, true, "/home/me/.devpod")
			if err != nil {
				t.Fatal(err)
			}
			got := ""
			for _, line := range strings.Split(config, "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "ProxyCommand ") {
					got = parseWorkdirFlag(goos, line)
				}
			}
			if got != workdir {
				t.Errorf("%s: expected workdir %q, got %q from:\n%s", goos, workdir, got, config)
			}
		}
	}
}

// TestParseWorkdirFlagOldFormat reads the ProxyCommand lines older versions of DevPod wrote, with the values in
// double quotes.
func TestParseWorkdirFlagOldFormat(t *testing.T) {
	tests := []struct{ goos, line, expected string }{
		{"linux", `  ProxyCommand "/usr/local/bin/devpod" ssh --stdio --context default --user root foo --devpod-home "/home/me/.devpod" --workdir "/workspaces/my project" --gpg-agent-forwarding`, "/workspaces/my project"},
		{"darwin", `  ProxyCommand "/usr/local/bin/devpod" ssh --stdio --context default --user root foo --workdir "/workspaces/o'brien"`, "/workspaces/o'brien"},
		{"windows", `  ProxyCommand "C:\Program Files\DevPod\devpod.exe" ssh --stdio --context default --user root foo --devpod-home "C:\Users\me\.devpod" --workdir "/workspaces/my project"`, "/workspaces/my project"},
		{"linux", `  ProxyCommand "/usr/local/bin/devpod" ssh --stdio --context default --user root foo`, ""},
	}
	for _, tt := range tests {
		if got := parseWorkdirFlag(tt.goos, tt.line); got != tt.expected {
			t.Errorf("%s: expected workdir %q, got %q from %s", tt.goos, tt.expected, got, tt.line)
		}
	}
}
