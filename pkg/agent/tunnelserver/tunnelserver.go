package tunnelserver

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/loft-sh/devpod/pkg/agent/tunnel"
	"github.com/loft-sh/devpod/pkg/devcontainer/config"
	"github.com/loft-sh/devpod/pkg/dockercredentials"
	"github.com/loft-sh/devpod/pkg/extract"
	"github.com/loft-sh/devpod/pkg/git"
	"github.com/loft-sh/devpod/pkg/gitcredentials"
	"github.com/loft-sh/devpod/pkg/gitsshsigning"
	"github.com/loft-sh/devpod/pkg/gpg"
	"github.com/loft-sh/devpod/pkg/log"
	"github.com/loft-sh/devpod/pkg/netstat"
	provider2 "github.com/loft-sh/devpod/pkg/provider"
	"github.com/loft-sh/devpod/pkg/stdio"
	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"
	perrors "github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func RunServicesServer(ctx context.Context, reader io.Reader, writer io.WriteCloser, allowGitCredentials, allowDockerCredentials bool, forwarder netstat.Forwarder, workspace *provider2.Workspace, log log.Logger, options ...Option) error {
	opts := append(options, []Option{
		WithForwarder(forwarder),
		WithAllowGitCredentials(allowGitCredentials),
		WithAllowDockerCredentials(allowDockerCredentials),
		WithWorkspace(workspace),
	}...)
	tunnelServ := New(log, opts...)

	return tunnelServ.Run(ctx, reader, writer)
}

func RunUpServer(ctx context.Context, reader io.Reader, writer io.WriteCloser, allowGitCredentials, allowDockerCredentials bool, workspace *provider2.Workspace, log log.Logger, options ...Option) (*config.Result, error) {
	opts := append(options, []Option{
		WithWorkspace(workspace),
		WithAllowGitCredentials(allowGitCredentials),
		WithAllowDockerCredentials(allowDockerCredentials),
	}...)
	tunnelServ := New(log, opts...)

	return tunnelServ.RunWithResult(ctx, reader, writer)
}

func RunSetupServer(ctx context.Context, reader io.Reader, writer io.WriteCloser, allowGitCredentials, allowDockerCredentials bool, setupInfo *config.Result, log log.Logger, options ...Option) (*config.Result, error) {
	opts := append(options, []Option{
		WithAllowGitCredentials(allowGitCredentials),
		WithAllowDockerCredentials(allowDockerCredentials),
	}...)
	tunnelServ := newSetupServer(setupInfo, log, opts...)

	return tunnelServ.RunWithResult(ctx, reader, writer)
}

func newSetupServer(setupInfo *config.Result, log log.Logger, options ...Option) *tunnelServer {
	opts := append(options, []Option{
		WithMounts(config.GetMounts(setupInfo)),
		WithWorkspaceMount(config.GetWorkspaceMount(setupInfo)),
	}...)
	return New(log, opts...)
}

func New(log log.Logger, options ...Option) *tunnelServer {
	s := &tunnelServer{
		log: log,
	}
	for _, o := range options {
		s = o(s)
	}

	return s
}

type tunnelServer struct {
	tunnel.UnimplementedTunnelServer

	// stream mounts
	mounts []*config.Mount
	// workspaceMount is the mount of the workspace folder, if known
	workspaceMount *config.Mount

	forwarder              netstat.Forwarder
	allowGitCredentials    bool
	allowDockerCredentials bool
	result                 *config.Result
	workspace              *provider2.Workspace
	log                    log.Logger
}

func (t *tunnelServer) RunWithResult(ctx context.Context, reader io.Reader, writer io.WriteCloser) (*config.Result, error) {
	lis := stdio.NewStdioListener(reader, writer, false)
	s := grpc.NewServer()
	tunnel.RegisterTunnelServer(s, t)
	reflection.Register(s)
	errChan := make(chan error, 1)
	go func() {
		errChan <- s.Serve(lis)
	}()

	select {
	case err := <-errChan:
		return nil, err
	case <-ctx.Done():
		return t.result, nil
	}
}

func (t *tunnelServer) Run(ctx context.Context, reader io.Reader, writer io.WriteCloser) error {
	_, err := t.RunWithResult(ctx, reader, writer)
	return err
}

func (t *tunnelServer) ForwardPort(ctx context.Context, portRequest *tunnel.ForwardPortRequest) (*tunnel.ForwardPortResponse, error) {
	if t.forwarder == nil {
		return nil, fmt.Errorf("cannot forward ports")
	}

	err := t.forwarder.Forward(portRequest.Port)
	if err != nil {
		return nil, fmt.Errorf("error forwarding port %s: %w", portRequest.Port, err)
	}

	return &tunnel.ForwardPortResponse{}, nil
}
func (t *tunnelServer) StopForwardPort(ctx context.Context, portRequest *tunnel.StopForwardPortRequest) (*tunnel.StopForwardPortResponse, error) {
	if t.forwarder == nil {
		return nil, fmt.Errorf("cannot forward ports")
	}

	err := t.forwarder.StopForward(portRequest.Port)
	if err != nil {
		return nil, fmt.Errorf("error stop forwarding port %s: %w", portRequest.Port, err)
	}

	return &tunnel.StopForwardPortResponse{}, nil
}

func (t *tunnelServer) DockerCredentials(ctx context.Context, message *tunnel.Message) (*tunnel.Message, error) {
	if !t.allowDockerCredentials {
		return nil, fmt.Errorf("docker credentials forbidden")
	}

	request := &dockercredentials.Request{}
	err := json.Unmarshal([]byte(message.Message), request)
	if err != nil {
		return nil, err
	}

	// check if list or get
	if request.ServerURL != "" {
		credentials, err := dockercredentials.GetAuthConfig(request.ServerURL)
		if err != nil {
			return nil, err
		}

		out, err := json.Marshal(credentials)
		if err != nil {
			return nil, err
		}

		return &tunnel.Message{Message: string(out)}, nil
	}

	// do a list
	listResponse, err := dockercredentials.ListCredentials()
	if err != nil {
		return nil, err
	}

	out, err := json.Marshal(listResponse)
	if err != nil {
		return nil, err
	}

	return &tunnel.Message{Message: string(out)}, nil
}

func (t *tunnelServer) GitUser(ctx context.Context, empty *tunnel.Empty) (*tunnel.Message, error) {
	out, err := json.Marshal(t.gitUser())
	if err != nil {
		return nil, err
	}

	return &tunnel.Message{
		Message: string(out),
	}, nil
}

// gitUser returns the identity `devpod up` saved with the workspace. A value it didn't save,
// because it wasn't given and the workspace doesn't come from a repository, is the global one.
func (t *tunnelServer) gitUser() *gitcredentials.GitUser {
	gitUser := &gitcredentials.GitUser{}
	if t.workspace != nil {
		gitUser.Name = t.workspace.GitUser.Name
		gitUser.Email = t.workspace.GitUser.Email
	}
	if gitUser.Name == "" || gitUser.Email == "" {
		globalUser := gitcredentials.GetGlobalUser()
		if gitUser.Name == "" {
			gitUser.Name = globalUser.Name
		}
		if gitUser.Email == "" {
			gitUser.Email = globalUser.Email
		}
	}

	return gitUser
}

func (t *tunnelServer) GitCredentials(ctx context.Context, message *tunnel.Message) (*tunnel.Message, error) {
	if !t.allowGitCredentials {
		return nil, fmt.Errorf("git credentials forbidden")
	}

	credentials := &gitcredentials.GitCredentials{}
	err := json.Unmarshal([]byte(message.Message), credentials)
	if err != nil {
		return nil, perrors.Wrap(err, "decode git credentials request")
	}

	if t.workspace != nil && t.workspace.Source.GitRepository != "" {
		path, err := gitcredentials.GetHTTPPath(ctx, gitcredentials.GetHttpPathParameters{
			Host:        credentials.Host,
			Protocol:    credentials.Protocol,
			CurrentPath: credentials.Path,
			Repository:  t.workspace.Source.GitRepository,
		})
		if err != nil {
			return nil, fmt.Errorf("get http path: %w", err)
		}
		// Set the credentials `path` field to the path component of the git repository URL.
		// This allows downstream credential helpers to figure out which passwords needs to be fetched
		credentials.Path = path
	}

	response, err := gitcredentials.GetCredentials(credentials)
	if err != nil {
		return nil, perrors.Wrap(err, "get git response")
	}
	credentials = response

	out, err := json.Marshal(credentials)
	if err != nil {
		return nil, err
	}

	return &tunnel.Message{Message: string(out)}, nil
}

func (t *tunnelServer) GitSSHSignature(ctx context.Context, message *tunnel.Message) (*tunnel.Message, error) {
	signatureRequest := &gitsshsigning.GitSSHSignatureRequest{}
	err := json.Unmarshal([]byte(message.Message), signatureRequest)
	if err != nil {
		return nil, perrors.Wrap(err, "git ssh sign request")
	}

	signatureResponse, err := signatureRequest.Sign()
	if err != nil {
		return nil, perrors.Wrap(err, "get git ssh signature")
	}

	out, err := json.Marshal(signatureResponse)
	if err != nil {
		return nil, err
	}

	return &tunnel.Message{Message: string(out)}, nil
}

func (t *tunnelServer) GPGPublicKeys(ctx context.Context, message *tunnel.Message) (*tunnel.Message, error) {
	rawPubKeys, err := gpg.GetHostPubKey()
	if err != nil {
		return nil, fmt.Errorf("get gpg host public keys: %w", err)
	}

	pubKeyArgument := base64.StdEncoding.EncodeToString(rawPubKeys)

	return &tunnel.Message{Message: pubKeyArgument}, nil
}

func (t *tunnelServer) SendResult(ctx context.Context, result *tunnel.Message) (*tunnel.Empty, error) {
	parsedResult := &config.Result{}
	err := json.Unmarshal([]byte(result.Message), parsedResult)
	if err != nil {
		return nil, err
	}

	t.result = parsedResult
	return &tunnel.Empty{}, nil
}

func (t *tunnelServer) Ping(context.Context, *tunnel.Empty) (*tunnel.Empty, error) {
	t.log.Debugf("Received ping from agent")
	return &tunnel.Empty{}, nil
}

func (t *tunnelServer) Log(ctx context.Context, message *tunnel.LogMessage) (*tunnel.Empty, error) {
	if message.LogLevel == tunnel.LogLevel_DEBUG {
		t.log.Debug(strings.TrimSpace(message.Message))
	} else if message.LogLevel == tunnel.LogLevel_INFO {
		t.log.Info(strings.TrimSpace(message.Message))
	} else if message.LogLevel == tunnel.LogLevel_WARNING {
		t.log.Warn(strings.TrimSpace(message.Message))
	} else if message.LogLevel == tunnel.LogLevel_ERROR {
		t.log.Error(strings.TrimSpace(message.Message))
	} else if message.LogLevel == tunnel.LogLevel_DONE {
		t.log.Done(strings.TrimSpace(message.Message))
	}

	return &tunnel.Empty{}, nil
}

func (t *tunnelServer) StreamWorkspace(message *tunnel.Empty, stream tunnel.Tunnel_StreamWorkspaceServer) error {
	if t.workspace == nil {
		return fmt.Errorf("workspace is nil")
	}

	opts, cleanup := t.workspaceTarOptions(stream.Context(), t.workspace.Source.LocalFolder)
	defer cleanup()

	buf := bufio.NewWriterSize(NewStreamWriter(stream, t.log), 10*1024)
	err := extract.WriteTarWithOptions(buf, t.workspace.Source.LocalFolder, opts)
	if err != nil {
		return err
	}

	// make sure buffer is flushed
	return buf.Flush()
}

func (t *tunnelServer) StreamMount(message *tunnel.StreamMountRequest, stream tunnel.Tunnel_StreamMountServer) error {
	var mount *config.Mount
	for _, m := range t.mounts {
		if m.String() == message.Mount {
			mount = m
			break
		}
	}
	if mount == nil {
		return fmt.Errorf("mount %s is not allowed to download", message.Mount)
	}

	// The ignore files only apply to the workspace folder, other bind mounts
	// are streamed whole
	opts := extract.TarOptions{}
	if t.workspaceMount != nil && mount.String() == t.workspaceMount.String() {
		var cleanup func()
		opts, cleanup = t.workspaceTarOptions(stream.Context(), mount.Source)
		defer cleanup()
	} else {
		t.log.Debugf("Stream mount %s without excludes, it is not the workspace mount", mount.String())
	}

	buf := bufio.NewWriterSize(NewStreamWriter(stream, t.log), 10*1024)
	err := extract.WriteTarWithOptions(buf, mount.Source, opts)
	if err != nil {
		return err
	}

	// make sure buffer is flushed
	return buf.Flush()
}

// ignoreFileName is the name of the file listing the paths not to stream.
const ignoreFileName = ".devpodignore"

// readIgnoreFile returns the exclude patterns of the ignore file in folder, or
// nil if there is none or it is invalid.
func (t *tunnelServer) readIgnoreFile(folder string) []string {
	ignoreFile := filepath.Join(folder, ignoreFileName)
	f, err := os.Open(ignoreFile)
	if err != nil {
		if os.IsNotExist(err) {
			t.log.Debugf("No %s found, nothing is excluded", ignoreFile)
		} else {
			t.log.Warnf("Error opening %s, nothing is excluded: %v", ignoreFile, err)
		}
		return nil
	}
	defer f.Close()

	excludes, err := ignorefile.ReadAll(f)
	if err == nil {
		_, err = patternmatcher.New(excludes)
	}
	if err != nil {
		t.log.Warnf("Error reading %s, nothing is excluded: %v", ignoreFile, err)
		return nil
	}

	t.log.Debugf("Loaded %d exclude patterns from %s: %v", len(excludes), ignoreFile, excludes)
	return excludes
}

// gitFolder is the folder of a git repository in its work tree.
const gitFolder = ".git"

// workspaceTarOptions returns the options to stream the workspace folder into
// the container, and a function that removes what they need once streamed.
//
// If the folder is in a git work tree, only the files git doesn't ignore are
// streamed, and the ignore file takes precedence: it can leave out files git
// doesn't ignore, and re-include files git ignores with negation (!) patterns.
// If the folder is the top level of the work tree, its .git is replaced by a
// shallow copy unless the ignore file has a pattern matching .git. The
// dockerless build context is always streamed whole, as the build fails
// without it.
func (t *tunnelServer) workspaceTarOptions(ctx context.Context, folder string) (extract.TarOptions, func()) {
	opts := extract.TarOptions{
		Excludes:  t.readIgnoreFile(folder),
		KeepNames: []string{config.DevPodContextFeatureFolder},
	}
	noCleanup := func() {}

	files, err := git.ListFiles(ctx, folder)
	if err != nil {
		if errors.Is(err, git.ErrNotInstalled) {
			t.log.Warnf("Git is not installed, so all files of %s not excluded by %s are uploaded, including those .gitignore files ignore", folder, ignoreFileName)
		} else {
			t.log.Debugf("Upload all files of %s not excluded by %s, git can't list its files: %v", folder, ignoreFileName, err)
		}
		return opts, noCleanup
	}
	// e.g. the folder is in a repository of dotfiles that ignores everything
	if len(files) == 0 {
		t.log.Debugf("Upload all files of %s not excluded by %s, git doesn't list any", folder, ignoreFileName)
		return opts, noCleanup
	}
	opts.Files = extract.NewFileList(files, git.IgnoresCase(ctx, folder))
	t.log.Debugf("Upload the %d files of %s git doesn't ignore, and those %s re-includes", opts.Files.Len(), folder, ignoreFileName)

	if !git.IsTopLevel(ctx, folder) {
		return opts, noCleanup
	}
	// readIgnoreFile only returns valid patterns
	rules, _ := extract.NewExcludeRules(opts.Excludes)
	if verdict := gitFolderVerdict(rules); verdict != extract.Unmatched {
		t.log.Debugf("Upload %s as %s says, instead of a shallow copy", gitFolder, ignoreFileName)
		return opts, noCleanup
	}

	snapshot, err := os.MkdirTemp("", "devpod-git-")
	if err != nil {
		t.log.Warnf("Upload no %s, can't create a folder for a shallow copy: %v", gitFolder, err)
		return opts, noCleanup
	}
	cleanup := func() { _ = os.RemoveAll(snapshot) }
	result, err := git.Snapshot(ctx, folder, snapshot, excludedFiles(rules, files))
	if err != nil {
		cleanup()
		t.log.Warnf("Upload no %s, can't create a shallow copy: %v. Add !%s to %s to upload the whole %s", gitFolder, err, gitFolder, ignoreFileName, gitFolder)
		return opts, noCleanup
	}
	if result.Branch != "" {
		t.log.Infof("Upload a shallow %s with branch %s", gitFolder, result.Branch)
	} else {
		t.log.Infof("Upload a shallow %s with the checked out commit", gitFolder)
	}
	opts.Overlays = map[string]string{}
	for _, gitDir := range result.GitDirs {
		opts.Overlays[gitDir] = filepath.Join(snapshot, filepath.FromSlash(gitDir))
	}
	return opts, cleanup
}

// excludedFiles returns the files the exclude rules leave out.
func excludedFiles(rules *extract.ExcludeRules, files []string) []string {
	var excluded []string
	for _, file := range files {
		if verdict, err := rules.Match(file); err == nil && verdict == extract.Excluded {
			excluded = append(excluded, file)
		}
	}
	return excluded
}

// gitFolderVerdict returns what the exclude rules say about the .git folder
// at the root.
func gitFolderVerdict(rules *extract.ExcludeRules) extract.Verdict {
	verdict, err := rules.Match(gitFolder)
	if err != nil {
		return extract.Unmatched
	}
	return verdict
}
