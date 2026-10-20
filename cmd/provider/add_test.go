package provider

import (
	"context"
	"testing"

	"github.com/loft-sh/devpod/cmd/flags"
	"github.com/loft-sh/devpod/pkg/config"
	"gotest.tools/assert"
)

func addProvider(t *testing.T, cmd *AddCmd, args ...string) *config.Config {
	t.Helper()
	devPodConfig, err := config.LoadConfig("", "")
	assert.NilError(t, err)
	assert.NilError(t, cmd.Run(context.Background(), devPodConfig, args))

	devPodConfig, err = config.LoadConfig("", "")
	assert.NilError(t, err)
	return devPodConfig
}

func TestAddWithoutUseSavesOptions(t *testing.T) {
	t.Setenv(config.DEVPOD_HOME, t.TempDir())

	devPodConfig := addProvider(t, &AddCmd{
		GlobalFlags: &flags.GlobalFlags{},
		Use:         true,
		Name:        "first",
	}, "docker")
	assert.Equal(t, devPodConfig.Current().DefaultProvider, "first")

	devPodConfig = addProvider(t, &AddCmd{
		GlobalFlags: &flags.GlobalFlags{},
		Use:         false,
		Name:        "second",
		Options:     []string{"DOCKER_PATH=/opt/docker"},
	}, "docker")
	assert.Equal(t, devPodConfig.Current().DefaultProvider, "first")
	assert.Equal(t, devPodConfig.ProviderOptions("second")["DOCKER_PATH"].Value, "/opt/docker")
	assert.Assert(t, devPodConfig.Current().Providers["second"].Initialized)

	// --from-existing copies the template's options and applies the new ones on top
	devPodConfig = addProvider(t, &AddCmd{
		GlobalFlags:  &flags.GlobalFlags{},
		Use:          false,
		Name:         "third",
		FromExisting: "second",
		Options:      []string{"INACTIVITY_TIMEOUT=10m"},
	})
	assert.Equal(t, devPodConfig.Current().DefaultProvider, "first")
	assert.Equal(t, devPodConfig.ProviderOptions("third")["DOCKER_PATH"].Value, "/opt/docker")
	assert.Equal(t, devPodConfig.ProviderOptions("third")["INACTIVITY_TIMEOUT"].Value, "10m")
	assert.Assert(t, devPodConfig.Current().Providers["third"].Initialized)
}

func TestAddWithoutUseOrOptionsSkipsConfiguration(t *testing.T) {
	t.Setenv(config.DEVPOD_HOME, t.TempDir())

	devPodConfig := addProvider(t, &AddCmd{
		GlobalFlags: &flags.GlobalFlags{},
		Use:         false,
	}, "docker")
	assert.Equal(t, devPodConfig.Current().DefaultProvider, "")
	assert.Assert(t, !devPodConfig.Current().Providers["docker"].Initialized)
}
