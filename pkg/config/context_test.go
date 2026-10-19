package config

import (
	"testing"

	"gotest.tools/assert"
)

func TestGitSSHToHTTPS(t *testing.T) {
	testCases := []struct {
		gitSSHToHTTPS      string
		sshAgentForwarding string
		expectedSSHToHTTPS bool
	}{
		{expectedSSHToHTTPS: false},
		{sshAgentForwarding: "true", expectedSSHToHTTPS: false},
		{sshAgentForwarding: "false", expectedSSHToHTTPS: true},
		{gitSSHToHTTPS: "auto", sshAgentForwarding: "false", expectedSSHToHTTPS: true},
		{gitSSHToHTTPS: "true", expectedSSHToHTTPS: true},
		{gitSSHToHTTPS: "false", sshAgentForwarding: "false", expectedSSHToHTTPS: false},
	}

	for _, testCase := range testCases {
		config := configWithOptions(map[string]string{
			ContextOptionGitSSHToHTTPS:      testCase.gitSSHToHTTPS,
			ContextOptionSSHAgentForwarding: testCase.sshAgentForwarding,
		})
		assert.Equal(t, config.GitSSHToHTTPS(), testCase.expectedSSHToHTTPS, "%+v", testCase)
	}
}

func TestGitSSHToHTTPSHosts(t *testing.T) {
	assert.DeepEqual(t, configWithOptions(nil).GitSSHToHTTPSHosts(), []string{})

	config := configWithOptions(map[string]string{
		ContextOptionGitSSHToHTTPSHosts: " github.com, ,ssh://git@git.example.com:7999/=https://git.example.com/scm/ ",
	})
	assert.DeepEqual(t, config.GitSSHToHTTPSHosts(), []string{"github.com", "ssh://git@git.example.com:7999/=https://git.example.com/scm/"})
}

func configWithOptions(options map[string]string) *Config {
	values := map[string]OptionValue{}
	for name, value := range options {
		values[name] = OptionValue{Value: value}
	}
	return &Config{
		DefaultContext: DefaultContext,
		Contexts: map[string]*ContextConfig{
			DefaultContext: {Options: values},
		},
	}
}
