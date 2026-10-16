# TODO

Follow-up work after hard-forking from [loft-sh/devpod](https://github.com/loft-sh/devpod).

## Migrate the providers to a Curity domain

The built-in and documented providers still live under the `loft-sh` GitHub organization:

- [ ] `devpod-provider-aws`
- [ ] `devpod-provider-azure`
- [ ] `devpod-provider-civo`
- [ ] `devpod-provider-digitalocean`
- [ ] `devpod-provider-gcloud`
- [ ] `devpod-provider-kubernetes`
- [ ] `devpod-provider-ssh`

For each provider we keep: fork it to `curityio`, then update its references in this repository
(provider sources, `providers/*/provider.yaml`, docs, desktop app and e2e tests).
Providers we don't keep should be removed from the code and docs.

## Fix links to content that only exists upstream

These links still point to `loft-sh/devpod` because the issues and releases they reference don't exist
in `curityio/devpod`. Copy the relevant information into our docs (or recreate the issues) and update or remove the links:

- [ ] `docs/pages/troubleshooting/ide-troubleshooting.mdx`: issue [#1187](https://github.com/loft-sh/devpod/issues/1187) (NeoVim `$TERM` workaround)
- [ ] `docs/pages/troubleshooting/linux-troubleshooting.mdx`: issue [#1045](https://github.com/loft-sh/devpod/issues/1045) (SSH connection string too long)
- [ ] `desktop/flatpak/DevPod.metainfo.xml`: release notes for [v0.6.10](https://github.com/loft-sh/devpod/releases/tag/v0.6.10)

## Remove the `pro` features

Remove the DevPod Pro (Loft platform) features and every reference to them, including:

- [ ] CLI: `cmd/pro` and the `pro` subcommands registered in `cmd/root.go`
- [ ] Platform code: `pkg/platform`, `pkg/loftconfig` and the platform parts of the daemon (`pkg/daemon/platform`)
- [ ] Agent setup for platform access (`cmd/agent/container/setup_loft_platform_access.go`)
- [ ] Provider: `providers/pro` and `hack/pro`
- [ ] Desktop app: `desktop/src/lib/pro`, `desktop/src/views/Pro` and the Pro icons/badges
- [ ] Loft endpoints: `charts.loft.sh`, `license.loft.sh`, `loft.sh/*` annotations
- [ ] Docs pages about DevPod Pro
- [ ] Go dependencies that are only needed for the platform (`github.com/loft-sh/api`, `agentapi`, `admin-apis`, `apiserver`, ...), then run `go mod tidy` and `go mod vendor`
