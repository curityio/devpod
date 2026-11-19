package devcontainer

import (
	"testing"

	"github.com/curityio/devpod/pkg/devcontainer/config"
	"gotest.tools/assert"
)

func TestFallbackContainerContextAndDockerfile(t *testing.T) {
	testCases := []struct {
		name string

		localFolder  string
		remoteFolder string
		context      string
		dockerfile   string

		expectedContext    string
		expectedDockerfile string
	}{
		{
			name: "simple",

			localFolder:  "/my/local/folder",
			remoteFolder: "/workspaces/test",
			context:      "/my/local/folder/context",
			dockerfile:   "/my/local/folder/Dockerfile",

			expectedContext:    "/workspaces/test/context",
			expectedDockerfile: "/workspaces/test/Dockerfile",
		},
		{
			name: "windows",

			localFolder:  "C:/my/local/folder",
			remoteFolder: "/workspaces/test",
			context:      "C:/my/local/folder",
			dockerfile:   "C:/my/local/folder/Dockerfile",

			expectedContext:    "/workspaces/test",
			expectedDockerfile: "/workspaces/test/Dockerfile",
		},
	}

	for _, testCase := range testCases {
		outContext, outDockerfile := getContainerContextAndDockerfile(testCase.localFolder, testCase.remoteFolder, testCase.context, testCase.dockerfile)
		assert.Equal(t, outContext, testCase.expectedContext, testCase.name)
		assert.Equal(t, outDockerfile, testCase.expectedDockerfile, testCase.name)
	}
}

func TestRunOptionsArchitecture(t *testing.T) {
	r := &runner{ID: "ws"}
	mergedConfig := &config.MergedDevContainerConfig{}
	substitutionContext := &config.SubstitutionContext{WorkspaceMount: "type=bind,source=/src,target=/workspaces/ws"}
	buildInfo := &config.BuildInfo{
		ImageDetails:  &config.ImageDetails{},
		ImageMetadata: &config.ImageMetadataConfig{},
		ImageName:     "registry.example.com/ws:prebuild",
		Architecture:  "arm64",
		Dockerless:    &config.BuildInfoDockerless{},
	}

	options, err := r.getRunOptions(mergedConfig, substitutionContext, buildInfo)
	assert.NilError(t, err)
	assert.Equal(t, options.Architecture, "arm64")

	options, err = r.getDockerlessRunOptions(mergedConfig, substitutionContext, buildInfo)
	assert.NilError(t, err)
	assert.Equal(t, options.Architecture, "arm64")
}
