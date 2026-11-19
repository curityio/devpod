//go:build !windows

package container

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/curityio/devpod/pkg/agent/tunnel"
	"github.com/curityio/devpod/pkg/agent/tunnelserver"
	"github.com/curityio/devpod/pkg/devcontainer/config"
	"github.com/curityio/devpod/pkg/extract"
	"github.com/curityio/devpod/pkg/log"
	provider2 "github.com/curityio/devpod/pkg/provider"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"gotest.tools/assert"
	"gotest.tools/assert/cmp"
)

func TestCopyOfMount(t *testing.T) {
	const holdsWork, other = true, false
	const hasFiles, empty = true, false
	created := provider2.CLIOptions{}
	recreated := provider2.CLIOptions{Recreate: true}
	// --reset implies --recreate
	reset := provider2.CLIOptions{Recreate: true, Reset: true}

	for _, test := range []struct {
		name      string
		options   provider2.CLIOptions
		holdsWork bool
		hasFiles  bool
		expected  mountCopy
	}{
		{"new workspace folder", created, holdsWork, empty, mountReplace},
		{"new mount", created, other, empty, mountReplace},
		{"restarted workspace folder", created, holdsWork, hasFiles, mountKeep},
		{"restarted mount", created, other, hasFiles, mountKeep},
		{"recreated workspace folder keeps the user's work", recreated, holdsWork, hasFiles, mountKeep},
		{"recreated mount gets the host's changes", recreated, other, hasFiles, mountRefresh},
		{"mount added to devcontainer.json", recreated, other, empty, mountReplace},
		{"repository added to devcontainer.json", recreated, holdsWork, empty, mountReplace},
		{"reset workspace folder", reset, holdsWork, hasFiles, mountReplace},
		{"reset mount", reset, other, hasFiles, mountReplace},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, copyOfMount(test.options, test.holdsWork, test.hasFiles), test.expected)
		})
	}
}

func TestIsRepository(t *testing.T) {
	dir := t.TempDir()
	assert.Assert(t, !isRepository(dir))
	assert.Assert(t, !isRepository(filepath.Join(dir, "missing")))

	// a work tree has a file instead of the folder
	assert.NilError(t, os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /elsewhere\n"), 0o600))
	assert.Assert(t, isRepository(dir))
}

func TestHasFiles(t *testing.T) {
	dir := t.TempDir()
	assert.Assert(t, !hasFiles(dir))
	assert.Assert(t, !hasFiles(filepath.Join(dir, "missing")))

	file := filepath.Join(dir, "file")
	assert.NilError(t, os.WriteFile(file, nil, 0o600))
	assert.Assert(t, hasFiles(dir))
	assert.Assert(t, !hasFiles(file))
}

// hostTunnelClient answers StreamMount like the host does, with the tunnel server.
type hostTunnelClient struct {
	tunnel.TunnelClient

	server tunnel.TunnelServer
}

func (c *hostTunnelClient) StreamMount(ctx context.Context, in *tunnel.StreamMountRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[tunnel.Chunk], error) {
	sent := &sentChunks{ctx: ctx}
	err := c.server.StreamMount(in, sent)
	return &receivedChunks{chunks: sent.chunks, err: err}, nil
}

// sentChunks keeps the chunks the server sends.
type sentChunks struct {
	grpc.ServerStream

	ctx    context.Context
	chunks []*tunnel.Chunk
}

func (s *sentChunks) Context() context.Context { return s.ctx }

func (s *sentChunks) Send(chunk *tunnel.Chunk) error {
	s.chunks = append(s.chunks, &tunnel.Chunk{Content: bytes.Clone(chunk.Content)})
	return nil
}

// receivedChunks gives the client the chunks the server sent, and then the error it returned.
type receivedChunks struct {
	grpc.ClientStream

	chunks []*tunnel.Chunk
	err    error
}

func (s *receivedChunks) Recv() (*tunnel.Chunk, error) {
	if len(s.chunks) > 0 {
		chunk := s.chunks[0]
		s.chunks = s.chunks[1:]
		return chunk, nil
	} else if s.err != nil {
		return nil, s.err
	}
	return nil, io.EOF
}

func writeFile(t *testing.T, name, content string) {
	t.Helper()
	assert.NilError(t, os.MkdirAll(filepath.Dir(name), 0o755))
	assert.NilError(t, os.WriteFile(name, []byte(content), 0o600))
}

func readFile(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(name)
	assert.NilError(t, err)
	return string(content)
}

// newHost returns a workspace folder and another folder on the host, their mounts into folders
// standing in for those of the container, and a client that streams them.
func newHost(t *testing.T) (workspaceMount, otherMount *config.Mount, client tunnel.TunnelClient) {
	t.Helper()
	workspaceMount = &config.Mount{Type: "bind", Source: t.TempDir(), Target: t.TempDir()}
	otherMount = &config.Mount{Type: "bind", Source: t.TempDir(), Target: t.TempDir()}
	server := tunnelserver.New(log.Discard,
		tunnelserver.WithMounts([]*config.Mount{workspaceMount, otherMount}),
		tunnelserver.WithWorkspaceMount(workspaceMount),
	)
	return workspaceMount, otherMount, &hostTunnelClient{server: server}
}

func TestRefreshBuildInfo(t *testing.T) {
	workspaceMount, _, client := newHost(t)
	buildFolder := filepath.Join(".devcontainer", config.DevPodContextFeatureFolder)
	// the host read the new devcontainer.json
	writeFile(t, filepath.Join(workspaceMount.Source, buildFolder, "Dockerfile-with-features"), "FROM new")
	writeFile(t, filepath.Join(workspaceMount.Source, buildFolder, "feature-b", "install.sh"), "new")
	writeFile(t, filepath.Join(workspaceMount.Source, ".devcontainer", "Dockerfile"), "FROM new")
	// the container has what a build that failed left
	buildInfoDir := filepath.Join(workspaceMount.Target, buildFolder)
	writeFile(t, filepath.Join(buildInfoDir, "Dockerfile-with-features"), "FROM old")
	writeFile(t, filepath.Join(buildInfoDir, "feature-a", "install.sh"), "old")
	writeFile(t, filepath.Join(workspaceMount.Target, ".devcontainer", "Dockerfile"), "FROM mine")

	err := refreshBuildInfo(context.Background(), client, workspaceMount, buildInfoDir, log.Discard)

	assert.NilError(t, err)
	assert.Equal(t, readFile(t, filepath.Join(buildInfoDir, "Dockerfile-with-features")), "FROM new")
	assert.Equal(t, readFile(t, filepath.Join(buildInfoDir, "feature-b", "install.sh")), "new")
	_, err = os.Stat(filepath.Join(buildInfoDir, "feature-a"))
	assert.Assert(t, os.IsNotExist(err))
	_, err = os.Stat(buildInfoDir + ".new")
	assert.Assert(t, os.IsNotExist(err))
	// nothing else of the workspace folder is touched
	assert.Equal(t, readFile(t, filepath.Join(workspaceMount.Target, ".devcontainer", "Dockerfile")), "FROM mine")
}

func TestRefreshBuildInfoKeepsOldFilesOnError(t *testing.T) {
	workspaceMount, _, client := newHost(t)
	// the host has no build folder
	buildInfoDir := filepath.Join(workspaceMount.Target, config.DevPodContextFeatureFolder)
	writeFile(t, filepath.Join(buildInfoDir, "Dockerfile-with-features"), "FROM old")

	err := refreshBuildInfo(context.Background(), client, workspaceMount, buildInfoDir, log.Discard)

	assert.ErrorContains(t, err, "stream build folder")
	assert.ErrorContains(t, err, config.DevPodContextFeatureFolder)
	assert.Equal(t, readFile(t, filepath.Join(buildInfoDir, "Dockerfile-with-features")), "FROM old")
	_, err = os.Stat(buildInfoDir + ".new")
	assert.Assert(t, os.IsNotExist(err))
}

func TestRefreshBuildInfoOutsideWorkspaceFolder(t *testing.T) {
	workspaceMount, _, client := newHost(t)
	buildInfoDir := filepath.Join(t.TempDir(), config.DevPodContextFeatureFolder)

	err := refreshBuildInfo(context.Background(), client, workspaceMount, buildInfoDir, log.Discard)

	assert.ErrorContains(t, err, "is not in the workspace folder")
}

func TestStreamMountKeepNewer(t *testing.T) {
	_, mount, client := newHost(t)
	lastCopy := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	chtimes := func(name string, modTime time.Time) {
		assert.NilError(t, os.Chtimes(name, modTime, modTime))
	}
	// the user logged in again on the host
	writeFile(t, filepath.Join(mount.Source, "host-token"), "new")
	writeFile(t, filepath.Join(mount.Target, "host-token"), "used")
	chtimes(filepath.Join(mount.Target, "host-token"), lastCopy)
	// the container renewed its token
	writeFile(t, filepath.Join(mount.Source, "container-token"), "used")
	chtimes(filepath.Join(mount.Source, "container-token"), lastCopy)
	writeFile(t, filepath.Join(mount.Target, "container-token"), "renewed")
	writeFile(t, filepath.Join(mount.Target, "only-in-container"), "kept")

	err := streamMount(context.Background(), mount, client, log.Discard, extract.KeepNewer())

	assert.NilError(t, err)
	assert.Equal(t, readFile(t, filepath.Join(mount.Target, "host-token")), "new")
	assert.Equal(t, readFile(t, filepath.Join(mount.Target, "container-token")), "renewed")
	assert.Equal(t, readFile(t, filepath.Join(mount.Target, "only-in-container")), "kept")

	// resetting takes the host's files
	err = streamMount(context.Background(), mount, client, log.Discard)

	assert.NilError(t, err)
	assert.Equal(t, readFile(t, filepath.Join(mount.Target, "container-token")), "used")
	assert.Equal(t, readFile(t, filepath.Join(mount.Target, "only-in-container")), "kept")
}

func TestWarnIfWorkspaceFolderDiffers(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	emptyConfig := filepath.Join(t.TempDir(), "gitconfig")
	assert.NilError(t, os.WriteFile(emptyConfig, nil, 0o600))
	t.Setenv("GIT_CONFIG_GLOBAL", emptyConfig)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	git := func(dir string, args ...string) string {
		cmd := exec.Command("git", append([]string{"-c", "user.name=test", "-c", "user.email=test@example.com"}, args...)...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		assert.NilError(t, err, string(out))
		return strings.TrimSpace(string(out))
	}
	folder := t.TempDir()
	git(folder, "init", "-q", "-b", "main")
	git(folder, "commit", "-q", "--allow-empty", "-m", "first")
	first := git(folder, "rev-parse", "HEAD")
	git(folder, "commit", "-q", "--allow-empty", "-m", "second")
	second := git(folder, "rev-parse", "HEAD")

	warnings := func(contentCommit, workspaceFolder string) string {
		logs := &bytes.Buffer{}
		warnIfWorkspaceFolderDiffers(contentCommit, workspaceFolder, log.NewStreamLogger(logs, logs, logrus.InfoLevel))
		return logs.String()
	}

	assert.Equal(t, warnings(second, folder), "")
	// the driver mounts the content folder, or the workspace is no git repository
	assert.Equal(t, warnings("", folder), "")
	// e.g. a sub folder of a repository
	assert.Equal(t, warnings(first, t.TempDir()), "")

	warning := warnings(first, folder)
	assert.Assert(t, cmp.Contains(warning, "is at commit "+second[:8]))
	assert.Assert(t, cmp.Contains(warning, "was read from commit "+first[:8]))
	assert.Assert(t, cmp.Contains(warning, "git pull"))
	assert.Assert(t, cmp.Contains(warning, "--reset"))
}
