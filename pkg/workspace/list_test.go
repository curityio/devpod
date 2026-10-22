package workspace

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/loft-sh/devpod/pkg/config"
	"github.com/loft-sh/devpod/pkg/log"
	providerpkg "github.com/loft-sh/devpod/pkg/provider"
	"github.com/loft-sh/devpod/pkg/types"
	"gotest.tools/assert"
	"gotest.tools/assert/cmp"
)

func saveWorkspace(t *testing.T, id string, providerName string, lastUsed time.Time) {
	t.Helper()

	err := providerpkg.SaveWorkspaceConfig(&providerpkg.Workspace{
		ID:                id,
		Context:           config.DefaultContext,
		Provider:          providerpkg.WorkspaceProviderConfig{Name: providerName},
		LastUsedTimestamp: types.NewTime(lastUsed),
	})
	assert.NilError(t, err)
}

func TestListClients(t *testing.T) {
	t.Setenv(config.DEVPOD_HOME, t.TempDir())

	err := providerpkg.SaveProviderConfig(config.DefaultContext, &providerpkg.ProviderConfig{
		Name: "test",
		Exec: providerpkg.ProviderCommands{Command: types.StrArray{"sh", "-c", "${COMMAND}"}},
	})
	assert.NilError(t, err)

	now := time.Now()
	saveWorkspace(t, "old", "test", now.Add(-3*time.Hour))
	saveWorkspace(t, "missing-provider", "missing", now.Add(-2*time.Hour))
	saveWorkspace(t, "new", "test", now.Add(-1*time.Hour))

	corruptDir, err := providerpkg.GetWorkspaceDir(config.DefaultContext, "corrupt")
	assert.NilError(t, err)
	assert.NilError(t, os.MkdirAll(corruptDir, 0755))
	assert.NilError(t, os.WriteFile(filepath.Join(corruptDir, providerpkg.WorkspaceConfigFile), []byte("{not json"), 0644))

	devPodConfig, err := config.LoadConfig("", "")
	assert.NilError(t, err)

	listed, err := ListClients(devPodConfig, log.Discard)
	assert.NilError(t, err)

	ids := []string{}
	for _, l := range listed {
		ids = append(ids, l.ID)
	}
	// most recently used first, and those that couldn't be loaded last
	assert.DeepEqual(t, ids, []string{"new", "missing-provider", "old", "corrupt"})

	for _, l := range []ListedClient{listed[0], listed[2]} {
		assert.NilError(t, l.Err)
		assert.Assert(t, l.Client != nil)
		assert.Equal(t, l.Client.Workspace(), l.ID)
	}

	assert.ErrorContains(t, listed[1].Err, "couldn't find provider with name missing")
	assert.Equal(t, listed[1].Workspace.Provider.Name, "missing")
	assert.Assert(t, listed[1].Client == nil)

	assert.ErrorContains(t, listed[3].Err, "load workspace config")
	assert.Assert(t, cmp.Nil(listed[3].Workspace))
	assert.Assert(t, listed[3].Client == nil)
}

func TestListClientsWithoutWorkspaces(t *testing.T) {
	t.Setenv(config.DEVPOD_HOME, t.TempDir())

	devPodConfig, err := config.LoadConfig("", "")
	assert.NilError(t, err)

	listed, err := ListClients(devPodConfig, log.Discard)
	assert.NilError(t, err)
	assert.Equal(t, len(listed), 0)
}

func TestSortByLastUsed(t *testing.T) {
	now := time.Now()
	workspaces := []*providerpkg.Workspace{
		{ID: "old", LastUsedTimestamp: types.NewTime(now.Add(-time.Hour))},
		{ID: "new", LastUsedTimestamp: types.NewTime(now)},
		{ID: "never"},
	}

	SortByLastUsed(workspaces)

	assert.DeepEqual(t, []string{workspaces[0].ID, workspaces[1].ID, workspaces[2].ID}, []string{"new", "old", "never"})
}
