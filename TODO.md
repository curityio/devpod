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

## Store the desktop app update signing key in GitHub

`plugins.updater.pubkey` in `desktop/src-tauri/tauri.conf.json` is our own key. The release workflow signs updates with
its private key, so the key must be available to it:

- [ ] Store the private key and its password as the `TAURI_PRIVATE_KEY` and `TAURI_KEY_PASSWORD` repository secrets

## Enable telemetry again

Telemetry is disabled: `pkg/telemetry` only has a no-op collector, and the hooks in `cmd/root.go` and `cmd/up.go` call it.
Once a Curity server can collect usage events:

- [ ] Implement a collector that sends events to it, with an opt-out (context option and environment variable)
- [ ] Add the opt-out back to the desktop settings and describe the data in `docs/pages/other-topics/telemetry.mdx`
