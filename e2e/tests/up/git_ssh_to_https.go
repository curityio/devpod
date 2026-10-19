package up

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/loft-sh/devpod/e2e/framework"
	"github.com/loft-sh/devpod/pkg/devcontainer/config"
	docker "github.com/loft-sh/devpod/pkg/docker"
	"github.com/loft-sh/log"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const sshToHTTPSConfigPath = "/var/devpod/ssh-to-https.gitconfig"

var _ = DevPodDescribe("devpod up git ssh to https test suite", func() {
	ginkgo.Context("testing GIT_SSH_TO_HTTPS", ginkgo.Label("up-docker", "git-ssh-to-https"), ginkgo.Ordered, func() {
		var initialDir string

		ginkgo.BeforeEach(func() {
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)
		})

		// useOwnContext switches to a new context with the docker provider, so the options don't leak into other tests
		useOwnContext := func(ctx context.Context, f *framework.Framework, options ...string) string {
			contextName := "git-ssh-to-https"
			_ = f.DevPodContextDelete(ctx, contextName)
			err := f.DevPodContextCreate(ctx, contextName)
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(f.DevPodContextDelete, context.Background(), contextName)
			err = f.DevPodContextUse(ctx, contextName)
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(f.DevPodContextUse, context.Background(), "default")

			_ = f.DevPodProviderDelete(ctx, "docker")
			err = f.DevPodProviderAdd(ctx, "docker")
			framework.ExpectNoError(err)
			err = f.DevPodProviderUse(ctx, "docker")
			framework.ExpectNoError(err)

			err = f.DevPodContextSetOptions(ctx, contextName, options...)
			framework.ExpectNoError(err)
			return contextName
		}

		hostGit := func(dir string, args ...string) string {
			out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
			framework.ExpectNoError(err, string(out))
			return strings.TrimSpace(string(out))
		}

		ginkgo.It("should make git use https for ssh remotes when ssh-agent forwarding is off", func(ctx context.Context) {
			tempDir, err := framework.CopyToTempDir("tests/up/testdata/docker-git-ssh-to-https")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

			hostGit(tempDir, "init", "-q")
			hostGit(tempDir, "remote", "add", "origin", "git@github.com:curityio/devpod.git")
			hostGit(tempDir, "config", "--file", ".gitmodules", "submodule.lib.path", "lib")
			hostGit(tempDir, "config", "--file", ".gitmodules", "submodule.lib.url", "git@bitbucket.org:team/lib.git")

			f := framework.NewDefaultFramework(initialDir + "/bin")
			// GIT_SSH_TO_HTTPS stays at its default, auto
			contextName := useOwnContext(ctx, f, "SSH_AGENT_FORWARDING=false")

			ginkgo.DeferCleanup(f.DevPodWorkspaceDelete, context.Background(), tempDir)

			sshConfig := filepath.Join(tempDir, "ssh-config")
			err = f.DevPodUp(ctx, tempDir, "--ssh-config", sshConfig)
			framework.ExpectNoError(err)

			out, err := f.DevPodSSH(ctx, tempDir, "git config --system --get-all include.path")
			framework.ExpectNoError(err)
			gomega.Expect(out).To(gomega.ContainSubstring(sshToHTTPSConfigPath))

			// the repository was created on the host and isn't owned by the container user, git trusts it anyway
			out, err = f.DevPodSSH(ctx, tempDir, "git remote get-url --push origin")
			framework.ExpectNoError(err)
			gomega.Expect(strings.TrimSpace(out)).To(gomega.Equal("https://github.com/curityio/devpod.git"))

			out, err = f.DevPodSSH(ctx, tempDir, "git ls-remote --get-url git@bitbucket.org:team/lib.git")
			framework.ExpectNoError(err)
			gomega.Expect(strings.TrimSpace(out)).To(gomega.Equal("https://bitbucket.org/team/lib.git"))

			// the repository on the host keeps its ssh remote
			gomega.Expect(hostGit(tempDir, "config", "--file", filepath.Join(".git", "config"), "remote.origin.url")).
				To(gomega.Equal("git@github.com:curityio/devpod.git"))

			// the ssh-agent is forwarded neither into the lifecycle commands nor into IDE connections
			out, err = f.DevPodSSH(ctx, tempDir, "cat /tmp/post-create-ssh-auth-sock")
			framework.ExpectNoError(err)
			gomega.Expect(strings.TrimSpace(out)).To(gomega.Equal("none"))
			sshConfigContent, err := os.ReadFile(sshConfig)
			framework.ExpectNoError(err)
			gomega.Expect(string(sshConfigContent)).To(gomega.ContainSubstring("ForwardAgent no"))
			gomega.Expect(string(sshConfigContent)).NotTo(gomega.ContainSubstring("ForwardAgent yes"))

			// when devpod ssh has to start the container again, it keeps the rewrites
			workspace, err := f.FindWorkspace(ctx, tempDir)
			framework.ExpectNoError(err)
			dockerHelper := &docker.DockerHelper{DockerCommand: "docker", Log: log.Default}
			ids, err := dockerHelper.FindContainer(ctx, []string{fmt.Sprintf("%s=%s", config.DockerIDLabel, workspace.UID)})
			framework.ExpectNoError(err)
			gomega.Expect(ids).To(gomega.HaveLen(1))
			err = dockerHelper.Stop(ctx, ids[0])
			framework.ExpectNoError(err)

			out, err = f.DevPodSSH(ctx, tempDir, "git remote get-url --push origin")
			framework.ExpectNoError(err)
			gomega.Expect(strings.TrimSpace(out)).To(gomega.Equal("https://github.com/curityio/devpod.git"))

			err = f.DevPodContextSetOptions(ctx, contextName, "GIT_SSH_TO_HTTPS=false")
			framework.ExpectNoError(err)

			err = f.DevPodUp(ctx, tempDir, "--ssh-config", sshConfig)
			framework.ExpectNoError(err)

			out, err = f.DevPodSSH(ctx, tempDir, "git config --system --get-all include.path || true")
			framework.ExpectNoError(err)
			gomega.Expect(out).NotTo(gomega.ContainSubstring(sshToHTTPSConfigPath))

			out, err = f.DevPodSSH(ctx, tempDir, "test -e "+sshToHTTPSConfigPath+" && echo present || echo absent")
			framework.ExpectNoError(err)
			gomega.Expect(strings.TrimSpace(out)).To(gomega.Equal("absent"))

			out, err = f.DevPodSSH(ctx, tempDir, "git remote get-url --push origin")
			framework.ExpectNoError(err)
			gomega.Expect(strings.TrimSpace(out)).To(gomega.Equal("git@github.com:curityio/devpod.git"))
		}, ginkgo.SpecTimeout(framework.GetTimeout()))

		ginkgo.It("should find the remotes of a git worktree", func(ctx context.Context) {
			repoDir, err := framework.CopyToTempDir("tests/up/testdata/docker-git-ssh-to-https")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, repoDir)

			hostGit(repoDir, "init", "-q")
			hostGit(repoDir, "remote", "add", "origin", "git@github.com:curityio/devpod.git")
			hostGit(repoDir, "add", ".devcontainer.json")
			hostGit(repoDir, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-q", "-m", "init")

			// the worktree's git dir is in repoDir, which isn't mounted into the container
			worktreeParent, err := os.MkdirTemp("", "temp-*")
			framework.ExpectNoError(err)
			worktreeParent, err = filepath.EvalSymlinks(worktreeParent)
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(os.RemoveAll, worktreeParent)
			worktree := filepath.Join(worktreeParent, "worktree")
			hostGit(repoDir, "worktree", "add", "-q", worktree)

			f := framework.NewDefaultFramework(initialDir + "/bin")
			useOwnContext(ctx, f, "SSH_AGENT_FORWARDING=false")

			ginkgo.DeferCleanup(f.DevPodWorkspaceDelete, context.Background(), worktree)

			err = f.DevPodUp(ctx, worktree, "--ssh-config", filepath.Join(worktreeParent, "ssh-config"))
			framework.ExpectNoError(err)

			out, err := f.DevPodSSH(ctx, worktree, "cd / && git ls-remote --get-url git@github.com:curityio/devpod.git")
			framework.ExpectNoError(err)
			gomega.Expect(strings.TrimSpace(out)).To(gomega.Equal("https://github.com/curityio/devpod.git"))
		}, ginkgo.SpecTimeout(framework.GetTimeout()))
	})
})
