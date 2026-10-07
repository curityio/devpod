package tunnelserver

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/skevetter/devpod/pkg/agent/tunnel"
	pkgconfig "github.com/skevetter/devpod/pkg/config"
	"github.com/skevetter/devpod/pkg/devcontainer/config"
	"github.com/skevetter/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

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
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o750))
		require.NoError(t, os.WriteFile(p, []byte(file), 0o600))
	}
}

func streamMountEntries(t *testing.T, server *tunnelServer, mount *config.Mount) []string {
	t.Helper()

	stream := &mockStreamMountServer{}
	require.NoError(
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
		require.NoError(t, err)
		entries = append(entries, hdr.Name)
	}
	sort.Strings(entries)

	return entries
}

// newSetupInfo returns a setup result with a workspace folder and another bind
// mount, both with a .devpodignore that excludes node_modules.
func newSetupInfo(t *testing.T) *config.Result {
	t.Helper()

	ignore := "**/node_modules/\n.claude/worktrees/\n"

	workspaceFolder := t.TempDir()
	writeFiles(t, workspaceFolder,
		"src/main.go",
		"web/node_modules/lib/index.js",
		".claude/worktrees/feature/main.go",
	)
	require.NoError(t, os.WriteFile(
		filepath.Join(workspaceFolder, pkgconfig.IgnoreFileName), []byte(ignore), 0o600))

	otherFolder := t.TempDir()
	writeFiles(t, otherFolder, "config.json", "node_modules/lib/index.js")
	require.NoError(t, os.WriteFile(
		filepath.Join(otherFolder, pkgconfig.IgnoreFileName), []byte(ignore), 0o600))

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

	require.Len(t, server.mounts, 2)
	require.NotNil(t, server.workspaceMount)
	assert.Equal(t, config.GetWorkspaceMount(setupInfo).String(), server.workspaceMount.String())
	assert.Equal(t, server.mounts[0].String(), server.workspaceMount.String())
}

func TestStreamMount_WorkspaceMountHonoursIgnoreFile(t *testing.T) {
	setupInfo := newSetupInfo(t)
	server := newSetupServer(setupInfo, log.Discard)

	entries := streamMountEntries(t, server, config.GetWorkspaceMount(setupInfo))

	assert.Equal(t, []string{pkgconfig.IgnoreFileName, "src/main.go"}, entries)
}

func TestStreamMount_OtherBindMountIsStreamedWhole(t *testing.T) {
	setupInfo := newSetupInfo(t)
	server := newSetupServer(setupInfo, log.Discard)

	entries := streamMountEntries(t, server, setupInfo.MergedConfig.Mounts[0])

	assert.Equal(
		t,
		[]string{pkgconfig.IgnoreFileName, "config.json", "node_modules/lib/index.js"},
		entries,
	)
}

func TestStreamMount_WithoutWorkspaceMountNothingIsExcluded(t *testing.T) {
	setupInfo := newSetupInfo(t)
	server := New(log.Discard, WithMounts(config.GetMounts(setupInfo)))

	entries := streamMountEntries(t, server, config.GetWorkspaceMount(setupInfo))

	assert.Equal(t, []string{
		".claude/worktrees/feature/main.go",
		pkgconfig.IgnoreFileName,
		"src/main.go",
		"web/node_modules/lib/index.js",
	}, entries)
}

func TestStreamMount_UnknownMountIsRejected(t *testing.T) {
	setupInfo := newSetupInfo(t)
	server := newSetupServer(setupInfo, log.Discard)

	err := server.StreamMount(
		&tunnel.StreamMountRequest{Mount: "type=bind,src=/etc,dst=/etc"},
		&mockStreamMountServer{},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not allowed")
}
