# Changelog

Changes made in this fork since it was hard-forked from [loft-sh/devpod](https://github.com/loft-sh/devpod)
at `5a0efcbf` (`v0.7.0-alpha.34` plus 2 commits, November 2025).

Add new entries to the top of the "Unreleased" section.

## Unreleased

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
- **Telemetry**: the CLI no longer sends usage events to Loft. The `TELEMETRY` context option, the
  `DEVPOD_DISABLE_TELEMETRY` environment variable and the desktop Telemetry setting are gone, so
  `devpod context set-options -o TELEMETRY=false` now fails.
- **Windows e2e jobs** in `e2e-tests.yaml`: they needed Loft's self-hosted Windows runner.

### Changed

- The documentation is published on this repository's GitHub Pages site,
  [curityio.github.io/devpod](https://curityio.github.io/devpod/), instead of Loft's devpod.sh
  ([#9](https://github.com/curityio/devpod/pull/9)). Links in the README, guides and desktop app point
  there. The Netlify config and Loft's Algolia search are removed, so the docs have no search for now.
- The desktop app checks for updates in `curityio/devpod` GitHub releases instead of Loft's update
  server ([#7](https://github.com/curityio/devpod/pull/7)).
- Links point to `curityio/devpod` instead of `loft-sh/devpod`, the README has a hard-fork note and
  Curity branding, and links to Loft's website, Slack, blog and X accounts are removed.
- The desktop app verifies updates with Curity's signing key instead of Loft's.
- The IDE and built-in provider icons are served from
  [curityio.github.io/devpod/icons](https://curityio.github.io/devpod/icons/) instead of devpod.sh.
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
