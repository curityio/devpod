# dockerless

Dockerless builds and runs a dev container without a docker daemon. DevPod uses its image,
`ghcr.io/curityio/dockerless`, to build dev containers on Kubernetes: the workspace pod starts
from it, DevPod's agent runs `dockerless build`, which builds the Dockerfile with
[kaniko](https://github.com/osscontainertools/kaniko) straight into the pod's own file system,
and `dockerless start` then runs the container's entrypoint as its user.

It comes from [loft-sh/dockerless](https://github.com/loft-sh/dockerless) v0.2.0, which used
loft-sh's patched copy of Google's kaniko 1.9.2. It now uses the maintained osscontainertools fork,
whose `Materialize` option does what loft-sh's patch did.

WARN: running dockerless on your host can delete key files and destroy your working environment.
It only runs in its own image.

## Development

It is its own Go module, separate from DevPod's, so that kaniko's dependencies stay out of the CLI
and the agent. Its code is Linux only.

```bash
GOOS=linux go build ./...
GOOS=linux golangci-lint run ./...   # golangci-lint v2
docker build -t dockerless:dev .
```

To try a build in it:

```bash
docker run --rm -v "$PWD/example:/workspaces/test" dockerless:dev \
  "/.dockerless/dockerless build --context dir:///workspaces/test --dockerfile /workspaces/test/Dockerfile && cat /.dockerless/image.json"
```

DevPod uses the image that `pkg/devcontainer/single.go` names, unless the kubernetes provider's
`DOCKERLESS_IMAGE` option names another. The e2e tests build this directory's image under that name
and load it into kind. `.github/workflows/dockerless.yaml` checks it on pull requests, and publishes
it when a `dockerless-v<version>` tag is pushed; then update the version in `single.go`.
