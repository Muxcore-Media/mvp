# Forgejo Actions runner (vault)

Origin CI runs on **Forgejo** (`git.zem.systems`), not GitHub. GitHub remains the later **public** mirror for homelabbers (Releases / GHCR). Do not register GitHub Actions runners for MuxCore.

## Current setup

Host runner on `vault` (no Docker). NixOS: `zemdregon/nix-production/modules/forgejo-runner.nix`.

| Item | Value |
|------|--------|
| Unit | `gitea-runner-vault.service` |
| Binary | `forgejo-runner` v12+ |
| URL | `http://127.0.0.1:3000` (same host as Forgejo) |
| Labels | `native` (`native:host`) |
| Name | `vault` |
| Scope | instance (token from `forgejo actions generate-runner-token` / sops `forgejo/runnerToken`) |

```bash
ssh vault 'systemctl is-active gitea-runner-vault.service forgejo.service'
sudo journalctl -u gitea-runner-vault -f
```

Jobs must use `runs-on: native`. Workflows live in **`.forgejo/workflows/`** (Forgejo does not treat `.github/workflows` as origin CI).

## Golden module CI

Template: [`_wave1/GOLDEN_FORGEJO_CI.yml`](../../_wave1/GOLDEN_FORGEJO_CI.yml). Copy to `.forgejo/workflows/ci.yml`.

- `actions/checkout@v4` and `actions/setup-go@v5` resolve via Forgejo `DEFAULT_ACTIONS_URL` (`https://data.forgejo.org`).
- `GOPRIVATE=github.com/Muxcore-Media/*` plus `insteadOf` to `https://git.zem.systems/muxcore/` so Go still uses the GitHub import path while **fetching origin from Forgejo**.
- No Docker / GHCR jobs on this runner. Public image publish is a later GitHub-mirror step.

## Adding Go / extra tools

The host runner packages are declared in `forgejo-runner.nix` (`hostPackages`). `actions/setup-go` downloads a toolchain into the job workspace; a nix rebuild is not required for Go CI.

## GitHub (public, later)

When the public org exists again, origin (Forgejo) pushes to GitHub. Consumers `go get` / pull GHCR from GitHub. Until then, operators clone `ssh://forgejo@git.zem.systems:2222/muxcore/<repo>.git`.
