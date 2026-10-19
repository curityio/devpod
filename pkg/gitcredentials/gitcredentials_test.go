package gitcredentials

import (
	"testing"

	"gotest.tools/assert"
)

func TestRemoveCredentialHelper(t *testing.T) {
	testCases := []struct {
		name     string
		in       string
		expected string
	}{
		{
			name: "removes DevPod's section",
			in: `[user]
	name = Test

[credential]
        helper = !'/usr/local/bin/devpod' agent git-credentials --port 12049
[core]
	editor = vim`,
			expected: `[user]
	name = Test

[core]
	editor = vim`,
		},
		{
			name: "keeps other helpers in the same section",
			in: `[credential]
	helper = store --file /home/vscode/.git-credentials
        helper = !'/usr/local/bin/devpod' agent git-credentials --port 12049`,
			expected: `[credential]
	helper = store --file /home/vscode/.git-credentials`,
		},
		{
			name: "keeps sections without DevPod's helper",
			in: `[credential]
	helper = store
[credential "https://github.com"]
	helper = !gh auth git-credential
[credential]
        helper = !'/usr/local/bin/devpod' agent git-credentials`,
			expected: `[credential]
	helper = store
[credential "https://github.com"]
	helper = !gh auth git-credential`,
		},
		{
			name:     "leaves config without credentials alone",
			in:       "[user]\n\tname = Test",
			expected: "[user]\n\tname = Test",
		},
	}

	for _, testCase := range testCases {
		assert.Equal(t, removeCredentialHelper(testCase.in), testCase.expected, testCase.name)
	}
}
