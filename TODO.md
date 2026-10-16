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

## Replace the desktop app update signing key

The Tauri updater verifies every update against `plugins.updater.pubkey` in `desktop/src-tauri/tauri.conf.json`,
which is still Loft's public key. Our releases are signed with the `TAURI_PRIVATE_KEY` secret, so the app will reject them
unless the keys match:

- [ ] Generate a new key pair with `tauri signer generate`
- [ ] Store the private key and its password as the `TAURI_PRIVATE_KEY` and `TAURI_KEY_PASSWORD` repository secrets
- [ ] Replace `plugins.updater.pubkey` in `desktop/src-tauri/tauri.conf.json` with the new public key

## Stop sending telemetry to Loft

Release builds send CLI usage events to Loft's `analytics.loft.rocks` (through `github.com/loft-sh/analytics-client`,
used in `pkg/telemetry`) unless the context option `TELEMETRY=false` or `DEVPOD_DISABLE_TELEMETRY=true` is set:

- [ ] Remove telemetry (and the `analytics-client` dependency), or point it at a Curity endpoint
- [ ] Update the `TELEMETRY` context option and the docs that mention telemetry accordingly
