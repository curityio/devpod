package tunnelserver

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"testing"

	"github.com/curityio/devpod/pkg/agent/tunnel"
	"github.com/curityio/devpod/pkg/devcontainer/config"
	"github.com/curityio/devpod/pkg/log"
	"google.golang.org/grpc"
	"gotest.tools/assert"
	is "gotest.tools/assert/cmp"
)

const srcMainGo = "src/main.go"

// mockStreamMountServer collects the chunks sent by StreamMount.
type mockStreamMountServer struct {
	grpc.ServerStream

	content bytes.Buffer
}

func (m *mockStreamMountServer) Send(chunk *tunnel.Chunk) error {
	m.content.Write(chunk.Content)
	return nil
}

func (m *mockStreamMountServer) Context() context.Context {
	return context.Background()
}

func writeFiles(t *testing.T, root string, files ...string) {
	t.Helper()

	for _, file := range files {
		p := filepath.Join(root, filepath.FromSlash(file))
		assert.NilError(t, os.MkdirAll(filepath.Dir(p), 0o750))
		assert.NilError(t, os.WriteFile(p, []byte(file), 0o600))
	}
}

func streamMountEntries(t *testing.T, server *tunnelServer, mount *config.Mount) []string {
	t.Helper()

	stream := &mockStreamMountServer{}
	assert.NilError(
		t,
		server.StreamMount(&tunnel.StreamMountRequest{Mount: mount.String()}, stream),
	)

	entries := []string{}
	reader := tar.NewReader(&stream.content)
	for {
		hdr, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		assert.NilError(t, err)
		entries = append(entries, hdr.Name)
	}
	sort.Strings(entries)

	return entries
}

// newSetupInfo returns a setup result with a workspace folder and another bind
// mount, both with an ignore file that excludes node_modules.
func newSetupInfo(t *testing.T) *config.Result {
	t.Helper()

	ignore := "**/node_modules/\n.claude/worktrees/\n"

	workspaceFolder := t.TempDir()
	writeFiles(t, workspaceFolder,
		srcMainGo,
		"web/node_modules/lib/index.js",
		".claude/worktrees/feature/main.go",
	)
	assert.NilError(t, os.WriteFile(
		filepath.Join(workspaceFolder, ignoreFileName), []byte(ignore), 0o600))

	otherFolder := t.TempDir()
	writeFiles(t, otherFolder, "config.json", "node_modules/lib/index.js")
	assert.NilError(t, os.WriteFile(
		filepath.Join(otherFolder, ignoreFileName), []byte(ignore), 0o600))

	return &config.Result{
		SubstitutionContext: &config.SubstitutionContext{
			WorkspaceMount: "type=bind,src=" + workspaceFolder + ",dst=/workspaces/project",
		},
		MergedConfig: &config.MergedDevContainerConfig{
			NonComposeBase: config.NonComposeBase{
				Mounts: []*config.Mount{
					{Type: "bind", Source: otherFolder, Target: "/home/user/.other"},
					{Type: "volume", Source: "cache", Target: "/cache"},
				},
			},
		},
	}
}

func TestNewSetupServer_MarksWorkspaceMount(t *testing.T) {
	setupInfo := newSetupInfo(t)

	server := newSetupServer(setupInfo, log.Discard)

	assert.Equal(t, len(server.mounts), 2)
	assert.Assert(t, server.workspaceMount != nil)
	assert.Equal(t, config.GetWorkspaceMount(setupInfo).String(), server.workspaceMount.String())
	assert.Equal(t, server.mounts[0].String(), server.workspaceMount.String())
}

func TestStreamMount_WorkspaceMountHonoursIgnoreFile(t *testing.T) {
	setupInfo := newSetupInfo(t)
	server := newSetupServer(setupInfo, log.Discard)

	entries := streamMountEntries(t, server, config.GetWorkspaceMount(setupInfo))

	assert.DeepEqual(t, []string{ignoreFileName, srcMainGo}, entries)
}

func TestStreamMount_OtherBindMountIsStreamedWhole(t *testing.T) {
	setupInfo := newSetupInfo(t)
	server := newSetupServer(setupInfo, log.Discard)

	entries := streamMountEntries(t, server, setupInfo.MergedConfig.Mounts[0])

	assert.DeepEqual(
		t,
		[]string{ignoreFileName, "config.json", "node_modules/lib/index.js"},
		entries,
	)
}

func TestStreamMount_WithoutWorkspaceMountNothingIsExcluded(t *testing.T) {
	setupInfo := newSetupInfo(t)
	server := New(log.Discard, WithMounts(config.GetMounts(setupInfo)))

	entries := streamMountEntries(t, server, config.GetWorkspaceMount(setupInfo))

	assert.DeepEqual(t, []string{
		".claude/worktrees/feature/main.go",
		ignoreFileName,
		srcMainGo,
		"web/node_modules/lib/index.js",
	}, entries)
}

func TestStreamMount_InvalidIgnoreFileExcludesNothing(t *testing.T) {
	setupInfo := newSetupInfo(t)
	workspaceMount := config.GetWorkspaceMount(setupInfo)
	assert.NilError(t, os.WriteFile(
		filepath.Join(
			workspaceMount.Source,
			ignoreFileName,
		),
		[]byte("**/node_modules/\n!\n"),
		0o600,
	))
	server := newSetupServer(setupInfo, log.Discard)

	entries := streamMountEntries(t, server, workspaceMount)

	assert.DeepEqual(t, []string{
		".claude/worktrees/feature/main.go",
		ignoreFileName,
		srcMainGo,
		"web/node_modules/lib/index.js",
	}, entries)
}

// TestStreamMount_KeepsDockerlessBuildContext guards against #1108: the
// dockerless build fails if the ignore file strips its build context.
func TestStreamMount_KeepsDockerlessBuildContext(t *testing.T) {
	setupInfo := newSetupInfo(t)
	workspaceMount := config.GetWorkspaceMount(setupInfo)
	writeFiles(t, workspaceMount.Source,
		"run.sh",
		config.DevPodContextFeatureFolder+"/Dockerfile-without-features",
		config.DevPodContextFeatureFolder+"/feature/install.sh",
	)
	assert.NilError(t, os.WriteFile(
		filepath.Join(workspaceMount.Source, ignoreFileName),
		[]byte("**/*.sh\n"+config.DevPodContextFeatureFolder+"\n"), 0o600))
	server := newSetupServer(setupInfo, log.Discard)

	entries := streamMountEntries(t, server, workspaceMount)

	assert.Assert(t, is.Contains(entries, config.DevPodContextFeatureFolder+"/Dockerfile-without-features"))
	assert.Assert(t, is.Contains(entries, config.DevPodContextFeatureFolder+"/feature/install.sh"))
	assert.Assert(t, !slices.Contains(entries, "run.sh"))
}

func TestStreamMount_UnknownMountIsRejected(t *testing.T) {
	setupInfo := newSetupInfo(t)
	server := newSetupServer(setupInfo, log.Discard)

	err := server.StreamMount(
		&tunnel.StreamMountRequest{Mount: "type=bind,src=/etc,dst=/etc"},
		&mockStreamMountServer{},
	)
	assert.ErrorContains(t, err, "is not allowed")
}

func streamBuildFolder(server *tunnelServer, mount *config.Mount, subPath string) ([]string, error) {
	stream := &mockStreamMountServer{}
	err := server.StreamMount(&tunnel.StreamMountRequest{Mount: mount.String(), SubPath: subPath}, stream)
	if err != nil {
		return nil, err
	}

	entries := []string{}
	reader := tar.NewReader(&stream.content)
	for {
		hdr, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, err
		}
		entries = append(entries, hdr.Name)
	}
	sort.Strings(entries)

	return entries, nil
}

func TestStreamMount_BuildFolder(t *testing.T) {
	setupInfo := newSetupInfo(t)
	workspaceMount := config.GetWorkspaceMount(setupInfo)
	buildFolder := ".devcontainer/" + config.DevPodContextFeatureFolder
	writeFiles(t, workspaceMount.Source,
		".devcontainer/Dockerfile",
		buildFolder+"/Dockerfile-with-features",
		buildFolder+"/feature/install.sh",
	)
	// the ignore file doesn't apply to the build folder
	assert.NilError(t, os.WriteFile(
		filepath.Join(workspaceMount.Source, ignoreFileName), []byte("**/*.sh\n"), 0o600))
	server := newSetupServer(setupInfo, log.Discard)

	entries, err := streamBuildFolder(server, workspaceMount, buildFolder)

	assert.NilError(t, err)
	assert.DeepEqual(t, []string{"Dockerfile-with-features", "feature/install.sh"}, entries)
}

func TestStreamMount_OnlyBuildFolderOfWorkspaceMount(t *testing.T) {
	setupInfo := newSetupInfo(t)
	workspaceMount := config.GetWorkspaceMount(setupInfo)
	otherMount := setupInfo.MergedConfig.Mounts[0]
	writeFiles(t, workspaceMount.Source, config.DevPodContextFeatureFolder+"/Dockerfile")
	writeFiles(t, otherMount.Source, config.DevPodContextFeatureFolder+"/Dockerfile")
	outside := t.TempDir()
	writeFiles(t, outside, config.DevPodContextFeatureFolder+"/secret")
	if err := os.Symlink(outside, filepath.Join(workspaceMount.Source, "link")); err != nil {
		t.Skipf("can't create a symlink: %v", err)
	}
	server := newSetupServer(setupInfo, log.Discard)

	for name, test := range map[string]struct {
		mount   *config.Mount
		subPath string
		err     string
	}{
		// the ignore file excludes it from the workspace mount
		"ignored folder":       {workspaceMount, "web/node_modules", "not allowed"},
		"other folder":         {workspaceMount, "src", "not allowed"},
		"outside":              {workspaceMount, "../" + config.DevPodContextFeatureFolder, "not allowed"},
		"absolute":             {workspaceMount, filepath.Join(outside, config.DevPodContextFeatureFolder), "not allowed"},
		"through a symlink":    {workspaceMount, "link/" + config.DevPodContextFeatureFolder, "outside of the workspace folder"},
		"missing":              {workspaceMount, "missing/" + config.DevPodContextFeatureFolder, "build folder"},
		"of another mount":     {otherMount, config.DevPodContextFeatureFolder, "not the workspace mount"},
		"of an unknown mount":  {&config.Mount{Type: "bind", Source: outside, Target: "/outside"}, config.DevPodContextFeatureFolder, "not allowed"},
		"file instead of dir":  {workspaceMount, config.DevPodContextFeatureFolder + "/Dockerfile/" + config.DevPodContextFeatureFolder, "build folder"},
		"workspace mount root": {workspaceMount, ".", "not allowed"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := streamBuildFolder(server, test.mount, test.subPath)
			assert.ErrorContains(t, err, test.err)
		})
	}

	// and the allowed one, as a check of the test itself
	entries, err := streamBuildFolder(server, workspaceMount, config.DevPodContextFeatureFolder)
	assert.NilError(t, err)
	assert.DeepEqual(t, []string{"Dockerfile"}, entries)
}
