# AGENTS.md

Guidance for AI agents working in this repository.

This is Curity's hard fork of [loft-sh/devpod](https://github.com/loft-sh/devpod): a client-only tool that
creates dev environments from a `devcontainer.json` on a "provider" backend (local Docker, Kubernetes, SSH, cloud
VMs). [CHANGELOG.md](CHANGELOG.md) lists what the fork changed and [TODO.md](TODO.md) lists known follow-up work.

## Repository layout

| Path | What it is |
| --- | --- |
| `main.go`, `cmd/` | The `devpod` CLI (cobra). `cmd/agent/` and `cmd/helper/` hold the hidden commands run on the remote machine and inside the container. |
| `pkg/` | Core Go code: `devcontainer/` (parsing, building and running dev containers), `driver/` (docker, kubernetes, custom), `kubeclient/` (minimal Kubernetes API client), `workspace/`, `provider/`, `client/`, `agent/` (agent injection and the gRPC tunnel between CLI and agent), `ide/`, `ssh/`, `credentials/`, `config/`. |
| `providers/` | The built-in `docker` and `kubernetes` provider definitions, embedded into the binary. Other providers live in separate repos. |
| `desktop/` | The desktop app: Tauri 2 (Rust, `desktop/src-tauri`) with a React/TypeScript UI (`desktop/src`). It runs the `devpod` CLI as a sidecar. |
| `docs/` | Docusaurus documentation site, deployed to [devpod.curityio.net](https://devpod.curityio.net/) (GitHub Pages with a custom domain, so `baseUrl` is `/`) by `.github/workflows/docs.yaml` on pushes to `main`. |
| `e2e/` | Ginkgo end-to-end tests that run the real CLI against Docker, Podman and a kind cluster. |
| `hack/` | Build and test scripts. |
| `examples/` | Sample devcontainer projects, also used by the e2e tests. |
| `vendor/` | Vendored Go dependencies (committed). |

## Build and test

Go (CLI):

```bash
go build ./...
go vet ./...
./hack/unit-tests.sh          # what CI runs; `go test ./pkg/... ./cmd/...` is the quick version
make build                    # local CLI + desktop build via hack/rebuild.sh
./hack/build-agents.sh        # the Linux agents the CLI embeds; run before `go build` of a CLI you want to use
```

Lint, the same way CI does (golangci-lint v1.60.1 cannot be built with the newest Go):

```bash
GOTOOLCHAIN=go1.23.5 go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.60.1
GOTOOLCHAIN=go1.23.5 golangci-lint run --timeout 30m --max-same-issues 0 --max-issues-per-linter 0
```

e2e tests (slow; need Docker, and some labels need Podman or a kind cluster):

```bash
cd e2e && BUILDDIR=bin SRCDIR=".." ../hack/build-e2e.sh
cd e2e && go test -v -ginkgo.v -timeout 3600s --ginkgo.label-filter=up-docker
```

Desktop app (from `desktop/`):

```bash
yarn install
yarn types:check
yarn lint:ci
yarn format:check
cd src-tauri && cargo +stable check
```

## Things to beware of

- **Pull requests go to `curityio/devpod` `main` only, never upstream.** The `upstream` remote is read-only
  reference.
- **The Go module path is still `github.com/loft-sh/devpod`.** Imports were deliberately not renamed; don't
  "fix" them. Likewise the desktop app identifier `sh.loft.devpod` is kept because it is tied to the `devpod://`
  deep link scheme, the Flatpak id and existing installs.
- **Dependencies are vendored.** Builds use `-mod=vendor`. After changing imports or `go.mod`, run
  `GOFLAGS=-mod=mod go mod tidy && go mod vendor` and commit `vendor/`.
- **The agent is embedded in the CLI.** `hack/build-agents.sh` builds the Linux agents (amd64 and arm64, with the
  `devpod_agent` build tag) into `pkg/agent/agentbin/bin/`, and the CLI embeds them; it never downloads an agent.
  The agent injects itself from the machine into the container. Without that script, `go build` makes a CLI with
  no agents, which can't create workspaces (unless it's built for Linux, where it uses itself). DevPod replaces an
  agent of another version, but state an older agent wrote on a machine or in a container (e.g. the serialized
  `AgentWorkspaceInfo` and `ContainerWorkspaceInfo`) is still read by the new one.
- **Builds without a version keep a container's agent.** DevPod only replaces an agent whose version differs.
  `hack/rebuild.sh` and `hack/build-e2e.sh` give each build its own version (`v0.0.0-dev.<UTC time>`, or `VERSION`),
  shared by the CLI and its agents. A plain `hack/build-agents.sh` and `go build` both use `v0.0.0`, so a container
  that already has a `v0.0.0` agent keeps it.
- **Don't import `k8s.io/client-go/kubernetes`** (or its typed clients, or anything that uses them, such as
  go-containerregistry's `authn/kubernetes`). Its scheme links every Kubernetes API group and adds over 20 MB to
  the binary, which also runs as the agent. Use `pkg/kubeclient`, which only registers core/v1 and rbac/v1; add a
  resource or group there if you need one. `go list -deps . | grep '^k8s.io/client-go/kubernetes'` must print
  nothing.
- **gRPC tunnel code is generated.** After editing `pkg/agent/tunnel/tunnel.proto`, regenerate with
  `hack/build-grpc.sh` (needs `protoc`; existing code uses protoc-gen-go v1.36.4 and protoc-gen-go-grpc v1.5.1).
- **CI quirks:**
  - golangci-lint in CI only shows 3 issues of the same kind; run it locally with the flags above to see all.
  - `go-licenses-check` only runs when `go.mod` changes, so it can surface old breakage.
  - There are no Windows e2e tests: GitHub's Windows runners can't run Linux containers. `windows-tests.yaml`
    only builds the CLI and runs the unit tests on Windows.
- **Desktop quirks:**
  - `yarn lint:ci` has 5 errors that already existed at the fork point.
  - The default `cargo` may be too old for the v4 `Cargo.lock`; use `cargo +stable`.
  - `cargo check` needs `desktop/dist/` and the sidecar `desktop/src-tauri/bin/devpod-cli-<target-triple>` to
    exist; empty placeholders are enough (both are gitignored).

## Non-obvious findings

- **DevPod Pro is gone.** The workspace client in `pkg/client/clientimplementation` is the only workspace client
  left, and there are no proxy or daemon providers. Code or docs from upstream that mention Pro, the platform,
  `devpod pro`, proxy providers or Tailscale no longer apply.
- **Telemetry is disabled.** `pkg/telemetry` only has a no-op collector; the hooks stay so it can be re-enabled
  when Curity has a backend for it (tracked in `TODO.md`).
- **Icons are served from the docs site.** The IDE and provider icon URLs point at `docs/static/icons/`, published at
  `https://devpod.curityio.net/icons/`. Add new icons there (Docusaurus uses `/assets/` for its own output).
- **External providers are still upstream's.** Providers other than `docker` and `kubernetes` (aws, gcloud, ssh,
  ...) are still under the `loft-sh` GitHub organization (tracked in `TODO.md`).
- **Where state lives.** Local state is in `~/.devpod` (override with `DEVPOD_HOME`): contexts, provider configs,
  and per-workspace `workspace.json` files. These files are shared by concurrent devpod processes and are written
  atomically.
