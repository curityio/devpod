package agent

import (
	"path/filepath"
	"testing"

	provider2 "github.com/loft-sh/devpod/pkg/provider"
	"gotest.tools/assert"
)

func TestWriteWorkspaceInfoDoesNotSaveCLIOptions(t *testing.T) {
	file := filepath.Join(t.TempDir(), provider2.WorkspaceConfigFile)
	workspaceInfo := &provider2.AgentWorkspaceInfo{
		Workspace:  &provider2.Workspace{ID: "test"},
		CLIOptions: provider2.CLIOptions{WorkspaceEnv: []string{"TOKEN=secret"}, GitSSHToHTTPS: "true"},
	}

	assert.NilError(t, writeWorkspaceInfo(file, workspaceInfo))

	saved, err := ParseAgentWorkspaceInfo(file)
	assert.NilError(t, err)
	assert.Equal(t, saved.Workspace.ID, "test")
	assert.DeepEqual(t, saved.CLIOptions, provider2.CLIOptions{})
	// the caller's copy is untouched
	assert.Equal(t, workspaceInfo.CLIOptions.GitSSHToHTTPS, "true")
}
