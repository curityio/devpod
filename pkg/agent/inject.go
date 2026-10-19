package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	"github.com/loft-sh/devpod/pkg/agent/agentbin"
	"github.com/loft-sh/devpod/pkg/inject"
	"github.com/loft-sh/devpod/pkg/shell"
	"github.com/loft-sh/devpod/pkg/version"
	"github.com/loft-sh/log"
)

var waitForInstanceConnectionTimeout = time.Minute * 5

// InjectAgent installs the agent into a dev container, replacing an agent of a different version. As the
// container was then set up by another DevPod version, it warns the user that they may want to re-create the
// workspace.
func InjectAgent(
	ctx context.Context,
	exec inject.ExecFunc,
	remoteAgentPath string,
	log log.Logger,
	timeout time.Duration,
) error {
	previousVersion, err := injectAgentAndExecute(ctx, exec, remoteAgentPath, "", nil, nil, nil, log, timeout)
	if err != nil {
		return err
	}

	if previousVersion != "" {
		if previousVersion == "unknown" {
			previousVersion = "an unknown version"
		}
		log.Warnf(
			"The dev container was set up by DevPod %s, but this is DevPod %s. DevPod updated its agent in the container, "+
				"but to avoid issues you may want to re-create the workspace with `devpod up --recreate`.",
			previousVersion, version.GetVersion(),
		)
	}

	return nil
}

// InjectAgentAndExecute installs the agent at remoteAgentPath unless it is already there in this version, and then
// runs command. If local is true, it runs command locally instead.
func InjectAgentAndExecute(
	ctx context.Context,
	exec inject.ExecFunc,
	local bool,
	remoteAgentPath,
	command string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
	log log.Logger,
	timeout time.Duration,
) error {
	// should execute locally?
	if local {
		if command == "" {
			return nil
		}

		log.Debugf("Execute command locally")
		return shell.RunEmulatedShell(ctx, command, stdin, stdout, stderr, nil)
	}

	previousVersion, err := injectAgentAndExecute(ctx, exec, remoteAgentPath, command, stdin, stdout, stderr, log, timeout)
	if previousVersion != "" {
		log.Debugf("Replaced agent version %s at %s", previousVersion, remoteAgentPath)
	}
	return err
}

// injectAgentAndExecute returns the version of the agent it replaced, if any.
func injectAgentAndExecute(
	ctx context.Context,
	exec inject.ExecFunc,
	remoteAgentPath,
	command string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
	log log.Logger,
	timeout time.Duration,
) (string, error) {
	defer log.Debugf("Done InjectAgentAndExecute")
	if remoteAgentPath == "" {
		remoteAgentPath = RemoteDevPodHelperLocation
	}

	versionCheck := fmt.Sprintf(`[ "$(%s version 2>/dev/null || echo 'false')" != "%s" ]`, remoteAgentPath, version.GetVersion())

	// install devpod into the target
	// do a simple hello world to check if we can get something
	now := time.Now()
	lastMessage := time.Now()
	previousVersion := ""
	for {
		buf := &bytes.Buffer{}
		if stderr != nil {
			stderr = io.MultiWriter(stderr, buf)
		} else {
			stderr = buf
		}

		scriptParams := &inject.Params{
			Command:         command,
			AgentRemotePath: remoteAgentPath,
			ExistsCheck:     versionCheck,
			ShouldChmodPath: true,
		}

		var binaryErr error
		wasExecuted, err := inject.InjectAndExecute(
			ctx,
			exec,
			func(request inject.BinaryRequest) (io.ReadCloser, error) {
				if request.PreviousVersion != "" {
					previousVersion = request.PreviousVersion
				}

				binary, err := agentBinary(request.Arm)
				binaryErr = err
				return binary, err
			},
			scriptParams,
			stdin,
			stdout,
			stderr,
			timeout,
			log,
		)
		if err != nil {
			if binaryErr != nil {
				// retrying won't help
				return "", binaryErr
			} else if time.Since(now) > waitForInstanceConnectionTimeout {
				return "", fmt.Errorf("timeout waiting for instance connection: %w", err)
			} else if wasExecuted {
				return "", fmt.Errorf("agent error: %s: %w", buf.String(), err)
			}

			if time.Since(lastMessage) > time.Second*5 {
				log.Infof("Waiting for devpod agent to come up...")
				lastMessage = time.Now()
			}

			log.Debugf("Inject Error: %s%v", buf.String(), err)
			time.Sleep(time.Second * 3)
			continue
		}

		break
	}

	return previousVersion, nil
}

// agentBinary returns the agent binary for linux/amd64 or linux/arm64: the one embedded in this binary or, if
// there is none, this binary itself when it is built for that platform. This is how an agent, which embeds no
// other agents, injects itself into the dev container.
//
// The agent is never downloaded, so that it always matches the CLI that injects it.
func agentBinary(arm bool) (io.ReadCloser, error) {
	targetArch := "amd64"
	if arm {
		targetArch = "arm64"
	}

	binary, err := agentbin.Open(targetArch)
	if err == nil {
		return binary, nil
	} else if !errors.Is(err, agentbin.ErrNotEmbedded) {
		return nil, err
	}

	if runtime.GOOS == "linux" && runtime.GOARCH == targetArch {
		self, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("get executable: %w", err)
		}

		file, err := os.Open(self)
		if err != nil {
			return nil, fmt.Errorf("open agent binary: %w", err)
		}

		return file, nil
	}

	if agentbin.IsAgent {
		return nil, fmt.Errorf("the DevPod agent (%s/%s) can only install itself, not an agent for linux/%s", runtime.GOOS, runtime.GOARCH, targetArch)
	}
	return nil, fmt.Errorf("this DevPod build has no agent for linux/%s: run hack/build-agents.sh before building DevPod", targetArch)
}
