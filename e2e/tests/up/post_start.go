package up

import (
	"context"
	"os"
	"strings"

	"github.com/loft-sh/devpod/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = DevPodDescribe("devpod up postStartCommand test suite", func() {
	ginkgo.Context("testing postStartCommand", ginkgo.Label("up-docker"), ginkgo.Ordered, func() {
		var initialDir string

		ginkgo.BeforeEach(func() {
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)
		})

		ginkgo.It("should run postStartCommand again when devpod ssh starts a stopped container", func(ctx context.Context) {
			tempDir, err := framework.CopyToTempDir("tests/up/testdata/docker-post-start")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

			f := framework.NewDefaultFramework(initialDir + "/bin")
			_ = f.DevPodProviderDelete(ctx, "docker")
			err = f.DevPodProviderAdd(ctx, "docker")
			framework.ExpectNoError(err)
			err = f.DevPodProviderUse(ctx, "docker")
			framework.ExpectNoError(err)

			ginkgo.DeferCleanup(f.DevPodWorkspaceDelete, context.Background(), tempDir)

			err = f.DevPodUp(ctx, tempDir)
			framework.ExpectNoError(err)

			postStartRuns := func() int {
				out, err := f.DevPodSSH(ctx, tempDir, "cat /tmp/post-start.log")
				framework.ExpectNoError(err)
				return strings.Count(out, "started")
			}
			gomega.Expect(postStartRuns()).To(gomega.Equal(1))

			// the next ssh session finds the container stopped and starts it again
			err = f.DevPodStop(ctx, tempDir)
			framework.ExpectNoError(err)
			gomega.Expect(postStartRuns()).To(gomega.Equal(2))

			// a session in the running container doesn't run it again
			gomega.Expect(postStartRuns()).To(gomega.Equal(2))

			// devpod up starts a stopped container the same way
			err = f.DevPodStop(ctx, tempDir)
			framework.ExpectNoError(err)
			err = f.DevPodUp(ctx, tempDir)
			framework.ExpectNoError(err)
			gomega.Expect(postStartRuns()).To(gomega.Equal(3))
		}, ginkgo.SpecTimeout(framework.GetTimeout()))
	})
})
