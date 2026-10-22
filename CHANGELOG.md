# Changelog

Changes made in this fork since it was hard-forked from [loft-sh/devpod](https://github.com/loft-sh/devpod)
at `5a0efcbf` (`v0.7.0-alpha.34` plus 2 commits, November 2025).

Add new entries to the top of the "Unreleased" section.

## Unreleased

### Added

- `devpod up --git-user-name` and `--git-user-email` set the git identity that the workspace's dev container
  gets, and save it with the workspace. An empty value removes the saved one.
- `devpod status --all` shows the status of all workspaces of the context in a table (or, with `--output json`,
  a list). The statuses are retrieved concurrently, each within `--timeout`, and the status of a machine that
  several workspaces share is only retrieved once. A workspace whose status can't be
  retrieved, including one whose config can't be loaded, is shown as `Error`, and the command exits with an
  error after the others are shown. With `--output json` it has an `error` field instead, and the command
  succeeds so that the other statuses can be used.

### Fixed

- The git identity that DevPod gives a dev container is the one git uses in the repository the workspace comes
  from (its local folder, or the directory `devpod up` runs in), including the repository's config and matching
  `include` and `includeIf` directives. DevPod used to read only `~/.gitconfig` without its includes, an issue
  inherited from upstream, so a work identity kept in an `includeIf` block was ignored and workspaces committed
  with the global (often personal) email. `devpod up` saves the repository's identity with the workspace, so
  later connections from anywhere use the same one; outside a repository it saves nothing, and the container
  gets the global identity (now with its includes) when it connects. A dev container that already has an
  identity keeps it, including those of existing workspaces: use `devpod up --recreate` to give it the new one.
- `devpod up` of a Kubernetes workspace recreates the workspace's pod when it is dead (its phase is `Failed`,
  `Succeeded` or `Unknown`, its dev container has terminated, or it is being deleted), keeping the workspace's
  persistent volume claim. It used to reuse such a pod, for example after the node restarted, and then time out
  waiting for the agent, an issue inherited from upstream.
- `devpod provider add --use=false` and `devpod provider update --use=false` no longer silently discard `-o`
  options, an issue inherited from upstream. The options are saved and the provider is initialized, but the
  default provider is not changed. `devpod provider add --from-existing --use=false` likewise keeps the template
  provider's options.

## v0.8.0 (2026-10-20)

### Added

- Workspaces can keep using git while nobody is connected, e.g. for AI agents. With the new `GIT_SSH_TO_HTTPS`
  context option (on by default when `SSH_AGENT_FORWARDING` is `false`), git in the workspace uses https instead
  of ssh for the workspace's hosts, with credentials that the dev container provides. `GIT_SSH_TO_HTTPS_HOSTS`
  adds hosts and explicit rewrites. See
  [Run Workspaces Unattended](https://devpod.curityio.net/developing-in-workspaces/unattended-workspaces)
  ([#17](https://github.com/curityio/devpod/pull/17)).

### Fixed

- Security hardening of the SSH server that DevPod runs in the dev container ([#22](https://github.com/curityio/devpod/pull/22)), for issues
  inherited from upstream:
  - A reverse forward of a unix socket (as GPG agent forwarding uses) no longer deletes whatever is at the
    socket path. DevPod only replaces a socket that nothing listens on and that belongs to the server's user (or
    to anyone, when the server runs as root), and refuses the forward otherwise. Before, a second session
    forwarding the same socket took it over, and ending either session removed it for both.
  - The directory of a browser IDE's shared ssh-agent socket (`/tmp/auth-agent-<id>`) must be a real directory
    owned by the server's user with mode 0700, and the socket has mode 0600. Before, the server used whatever
    was at that path, so another user in the container could create it first. Ending a session removes the
    directory only if it is empty, so it doesn't take the socket from the next session.
  - One SSH connection can no longer cancel another connection's TCP or unix socket reverse forward: forwards
    are tracked per connection instead of by address only.
- Automatic language detection ([#22](https://github.com/curityio/devpod/pull/22)), used when `devcontainer.json` has no image, Dockerfile or Compose file, now
  recognizes C++, C# and PHP projects, and no longer ignores files whose names merely contain `env`, `dist` or
  `cache`.
- When a required provider option without a list of allowed values has no value, DevPod prompts for it in the
  terminal again ([#22](https://github.com/curityio/devpod/pull/22)). Before, the prompt failed with "please provide options to select from".
- `devpod` log files (such as the agent daemon's) are rotated at 10 MB as intended, instead of 10 TB ([#22](https://github.com/curityio/devpod/pull/22)).
- `devpod upgrade` and the CLI's newer-version check use this fork's releases on `curityio/devpod`
  ([#20](https://github.com/curityio/devpod/pull/20)). Before,
  they used upstream `loft-sh/devpod`, so `devpod upgrade` replaced the CLI with an upstream binary.
- `postStartCommand` now runs again when DevPod starts a stopped dev container, e.g. on `devpod ssh` or
  `devpod up` ([#18](https://github.com/curityio/devpod/pull/18)). It was skipped because DevPod compared the container's start time from before it was started.
- Ending a session no longer removes credential helpers that the user or the dev container added to the
  `[credential]` section of the global git config; only DevPod's own helper is removed ([#17](https://github.com/curityio/devpod/pull/17)). While a session is
  active, git asks those helpers before DevPod's.
- `SSH_AGENT_FORWARDING=false` now also stops forwarding the ssh-agent into the dev container's lifecycle
  commands and into IDE connections ([#17](https://github.com/curityio/devpod/pull/17)): the workspace's entry in `~/.ssh/config` gets `ForwardAgent no`.
  Before, it only applied to `devpod ssh` sessions.
- git in the dev container no longer refuses to use the workspace folder with "detected dubious ownership" when
  the folder isn't owned by the container user, e.g. with Docker Desktop on macOS and Windows ([#17](https://github.com/curityio/devpod/pull/17)). DevPod adds the
  workspace folder to `safe.directory` in the system git config.
- The workspace config that the agent saves on the machine no longer contains the CLI options of the last
  `devpod up`, such as `--workspace-env` values, as was intended ([#17](https://github.com/curityio/devpod/pull/17)).

### Changed

- DevPod no longer depends on any `github.com/loft-sh` Go module ([#22](https://github.com/curityio/devpod/pull/22)), as these are no longer maintained. The logger
  (`loft-sh/log`) is now part of `pkg/log`, the SSH server (`loft-sh/ssh`, a fork of `gliderlabs/ssh`) is
  `pkg/ssh/sshd`, and language detection (`loft-sh/programming-language-detection`) is in `pkg/language`. The
  unmaintained libraries they used are gone too: terminal prompts use `charmbracelet/huh` instead of the archived
  `AlecAivazis/survey`, tables are rendered by DevPod instead of an old `olekukonko/tablewriter`, and
  `mgutz/ansi`, `acarl005/stripansi`, `k0kubun/go-ansi` and `anmitsu/go-shlex` are replaced by a few lines of
  code.
- The DevPod CLI embeds the Linux agent for amd64 and arm64 ([#19](https://github.com/curityio/devpod/pull/19)),
  whatever platform it runs on, and copies it into machines and dev containers itself. It no longer downloads the
  agent from GitHub releases, so the agent always has the same version as the CLI, and workspaces can be created
  without access to GitHub. The `AGENT_URL` context option, the `DEVPOD_AGENT_URL` environment variable, the
  provider's `agent.downloadURL` and the desktop app's "Agent URL" setting are removed.
- When DevPod replaces the agent of an existing dev container that a different DevPod version set up, it warns that
  you may want to re-create the workspace with `devpod up --recreate`
  ([#19](https://github.com/curityio/devpod/pull/19)).
- The `devpod` binary is about 40% smaller ([#21](https://github.com/curityio/devpod/pull/21)): the stripped
  linux/amd64 build went from 68 MB to 41 MB (20 MB to 14 MB gzipped). The Kubernetes driver and the in-cluster
  registry keychain now use a small client for the core/v1 and rbac/v1 APIs instead of client-go's clientset,
  which linked every Kubernetes API group, and the built-in Amazon ECR keychain is gone (see Removed).
- The Windows e2e tests, which needed Loft's self-hosted Windows runner, are replaced by
  `windows-tests.yaml` ([#16](https://github.com/curityio/devpod/pull/16)). It builds the CLI and runs
  the unit tests on GitHub's `windows-latest` runners, which can't run the Linux containers the e2e
  tests use.

### Removed

- The hidden `devpod agent container ssh-server` command ([#22](https://github.com/curityio/devpod/pull/22)). Only DevPod Pro started it, and it accepted any
  SSH client without authentication and ran its shell as whichever user the client named.
- The built-in Amazon ECR registry keychain ([#21](https://github.com/curityio/devpod/pull/21)), which DevPod used when it ran inside a Kubernetes pod, together
  with the AWS SDK it needed. To use ECR, configure
  [`docker-credential-ecr-login`](https://github.com/awslabs/amazon-ecr-credential-helper) as a credential
  helper in the Docker config; see
  [Private registries](https://devpod.curityio.net/how-it-works/deploy-k8s#private-registries). The Azure
  Container Registry and Google keychains are kept.

## v0.8.0-alpha.1 (2026-10-16)

First release from `curityio/devpod`.

### Removed

- **DevPod Pro (Loft platform)**
  ([#7](https://github.com/curityio/devpod/pull/7)): every Pro feature and reference to it is gone.
  - CLI: `devpod pro`, `devpod ping`, `devpod up --reconfigure`, `devpod list --skip-pro`
    and the hidden global `--owner` flag.
  - The built-in `pro` provider, and provider types that only Pro used (`exec.proxy` and
    `exec.daemon` in `provider.yaml`).
  - Platform code:
    - The platform client, Pro instances and Pro workspace metadata.
    - Tailscale networking between the user's machine and Pro workspaces.
    - The platform git cache, remote builder and environment templates.
    - Platform credentials and kubeconfig injection into containers.
    - The `LoftConfig` and `KubeConfig` agent tunnel calls.
  - Desktop app: the Pro views, login, deep links (`devpod://import`, `devpod://pro/...`),
    tray menu, daemon and settings.
  - Go dependencies only needed for the platform, including the Loft API clients
    (`loft-sh/api`, `agentapi`, `apiserver`, `admin-apis`), Loft's Tailscale fork and parts of the
    Kubernetes server libraries. This removed 98 modules (about 1.1 million lines) from `vendor/`.
- **JetBrains Fleet support**: JetBrains discontinued Fleet. Workspaces saved with `fleet` as their
  IDE fall back to `none` with a warning, and `--ide fleet` now fails.
- **Telemetry** ([#14](https://github.com/curityio/devpod/pull/14)): the CLI no longer sends usage
  events to Loft. The `TELEMETRY` context option, the `DEVPOD_DISABLE_TELEMETRY` environment variable
  and the desktop Telemetry setting are gone, so `devpod context set-options -o TELEMETRY=false` now
  fails.
- **Windows e2e jobs** in `e2e-tests.yaml`: they needed Loft's self-hosted Windows runner.

### Changed

- The documentation is published on this repository's GitHub Pages site,
  [devpod.curityio.net](https://devpod.curityio.net/), instead of Loft's devpod.sh
  ([#9](https://github.com/curityio/devpod/pull/9), [#15](https://github.com/curityio/devpod/pull/15)).
  Links in the README, guides and desktop app point there. The Netlify config and Loft's Algolia
  search are removed, so the docs have no search for now.
- The desktop app checks for updates in `curityio/devpod` GitHub releases instead of Loft's update
  server ([#7](https://github.com/curityio/devpod/pull/7)).
- Links point to `curityio/devpod` instead of `loft-sh/devpod`, the README has a hard-fork note and
  Curity branding, and links to Loft's website, Slack, blog and X accounts are removed.
- The desktop app verifies updates with Curity's signing key instead of Loft's
  ([#14](https://github.com/curityio/devpod/pull/14)).
- The IDE and built-in provider icons are served from
  [devpod.curityio.net/icons](https://devpod.curityio.net/icons/) instead of devpod.sh
  ([#14](https://github.com/curityio/devpod/pull/14)).
- Release installers and binaries are not code-signed, because the fork has no Apple or Windows signing
  certificates yet. macOS and Windows show a warning the first time the desktop app is opened or installed.
- Kubernetes image pull secrets use `noreply@curity.io` as their email.

### Fixed

- `.devpodignore` is applied when the workspace folder is streamed into the container as a mount
  (e.g. with the Kubernetes provider).
- `devpod up` no longer hangs when the container dies after setup.
- `devpod ssh` no longer logs a spurious error when the container tunnel is closed.
- The CLI no longer panics when the container requests git credentials during setup (e.g. a
  `postStartCommand` that clones over HTTPS).
- Config files are written atomically, so concurrent devpod commands no longer fail with
  "unexpected end of JSON input".
- CI:
  - e2e tests accept `CAP_`-prefixed capabilities.
  - e2e test images are built on Debian bookworm.
  - The `go-licenses` workflows work with the toolchain selected by `go.mod`.
