package up

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/loft-sh/devpod/e2e/framework"
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

		ginkgo.It("should make git use https for ssh remotes when ssh-agent forwarding is off", func(ctx context.Context) {
			tempDir, err := framework.CopyToTempDir("tests/up/testdata/docker-git-ssh-to-https")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

			hostGit := func(args ...string) string {
				out, err := exec.Command("git", append([]string{"-C", tempDir}, args...)...).CombinedOutput()
				framework.ExpectNoError(err, string(out))
				return strings.TrimSpace(string(out))
			}
			hostGit("init", "-q")
			hostGit("remote", "add", "origin", "git@github.com:curityio/devpod.git")
			hostGit("config", "--file", ".gitmodules", "submodule.lib.path", "lib")
			hostGit("config", "--file", ".gitmodules", "submodule.lib.url", "git@bitbucket.org:team/lib.git")

			f := framework.NewDefaultFramework(initialDir + "/bin")

			// use a context of our own, so the options don't leak into other tests
			contextName := "git-ssh-to-https"
			_ = f.DevPodContextDelete(ctx, contextName)
			err = f.DevPodContextCreate(ctx, contextName)
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

			// GIT_SSH_TO_HTTPS stays at its default, auto
			err = f.DevPodContextSetOptions(ctx, contextName, "SSH_AGENT_FORWARDING=false")
			framework.ExpectNoError(err)

			ginkgo.DeferCleanup(f.DevPodWorkspaceDelete, context.Background(), tempDir)

			err = f.DevPodUp(ctx, tempDir)
			framework.ExpectNoError(err)

			out, err := f.DevPodSSH(ctx, tempDir, "git config --system --get-all include.path")
			framework.ExpectNoError(err)
			gomega.Expect(out).To(gomega.ContainSubstring(sshToHTTPSConfigPath))

			// the repository was created on the host, so git in the container doesn't trust it without safe.directory
			out, err = f.DevPodSSH(ctx, tempDir, "git -c safe.directory=\"$PWD\" remote get-url --push origin")
			framework.ExpectNoError(err)
			gomega.Expect(strings.TrimSpace(out)).To(gomega.Equal("https://github.com/curityio/devpod.git"))

			out, err = f.DevPodSSH(ctx, tempDir, "git ls-remote --get-url git@bitbucket.org:team/lib.git")
			framework.ExpectNoError(err)
			gomega.Expect(strings.TrimSpace(out)).To(gomega.Equal("https://bitbucket.org/team/lib.git"))

			// the repository on the host keeps its ssh remote
			gomega.Expect(hostGit("config", "--file", filepath.Join(".git", "config"), "remote.origin.url")).
				To(gomega.Equal("git@github.com:curityio/devpod.git"))

			err = f.DevPodContextSetOptions(ctx, contextName, "GIT_SSH_TO_HTTPS=false")
			framework.ExpectNoError(err)

			err = f.DevPodUp(ctx, tempDir)
			framework.ExpectNoError(err)

			out, err = f.DevPodSSH(ctx, tempDir, "git config --system --get-all include.path || true")
			framework.ExpectNoError(err)
			gomega.Expect(out).NotTo(gomega.ContainSubstring(sshToHTTPSConfigPath))

			out, err = f.DevPodSSH(ctx, tempDir, "test -e "+sshToHTTPSConfigPath+" && echo present || echo absent")
			framework.ExpectNoError(err)
			gomega.Expect(strings.TrimSpace(out)).To(gomega.Equal("absent"))

			out, err = f.DevPodSSH(ctx, tempDir, "git -c safe.directory=\"$PWD\" remote get-url --push origin")
			framework.ExpectNoError(err)
			gomega.Expect(strings.TrimSpace(out)).To(gomega.Equal("git@github.com:curityio/devpod.git"))
		}, ginkgo.SpecTimeout(framework.GetTimeout()))
	})
})
