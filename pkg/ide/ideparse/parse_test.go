package ideparse

import (
	"testing"

	"github.com/loft-sh/devpod/pkg/config"
	"github.com/loft-sh/devpod/pkg/log"
	"github.com/loft-sh/devpod/pkg/provider"
	"gotest.tools/assert"
)

func newTestConfig(defaultIDE string) *config.Config {
	return &config.Config{
		DefaultContext: config.DefaultContext,
		Contexts: map[string]*config.ContextConfig{
			config.DefaultContext: {DefaultIDE: defaultIDE},
		},
	}
}

func TestRefreshIDEOptionsDiscontinuedIDE(t *testing.T) {
	testCases := []struct {
		Name         string
		DefaultIDE   string
		WorkspaceIDE string
	}{
		{Name: "workspace IDE is fleet", WorkspaceIDE: "fleet"},
		{Name: "default IDE is fleet", DefaultIDE: "fleet"},
		{Name: "default IDE is Fleet in any case", DefaultIDE: "Fleet"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.Name, func(t *testing.T) {
			t.Setenv(config.DEVPOD_HOME, t.TempDir())
			workspace := &provider.Workspace{
				ID:      "test",
				Context: config.DefaultContext,
				IDE:     provider.WorkspaceIDEConfig{Name: testCase.WorkspaceIDE},
			}

			workspace, err := RefreshIDEOptions(newTestConfig(testCase.DefaultIDE), workspace, "", nil, log.Discard)
			assert.NilError(t, err)
			assert.Equal(t, workspace.IDE.Name, string(config.IDENone))
		})
	}
}

func TestRefreshIDEOptionsExplicitFleetFails(t *testing.T) {
	t.Setenv(config.DEVPOD_HOME, t.TempDir())
	workspace := &provider.Workspace{ID: "test", Context: config.DefaultContext}

	_, err := RefreshIDEOptions(newTestConfig(""), workspace, "fleet", nil, log.Discard)
	assert.ErrorContains(t, err, "unrecognized ide 'fleet'")
}
