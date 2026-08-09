# GitHub Actions self-hosted runner (gringotts)

Billing spend is **not** required. Core workflows already use `runs-on: self-hosted`.

## Current setup (repo-scoped → `Muxcore-Media/core`)

On gringotts:

| Item | Value |
|------|--------|
| Package | `nixpkgs#github-runner` (avoids glibc/`libstdc++` issues with the official tarball on NixOS) |
| State dir | `~/.github-runner-gringotts` (`RUNNER_ROOT`) |
| Unit | `systemctl --user status github-actions-runner.service` |
| Labels | `self-hosted`, `Linux`, `X64`, `gringotts` |
| Name | `gringotts` |

```bash
systemctl --user status github-actions-runner.service
journalctl --user -u github-actions-runner.service -f
```

`DISABLE_RUNNER_UPDATE=1` is set so the runner does not replace the Nix store binary with an upstream tarball.

## Toolchain (user nix profile + unit env)

Installed into `~/.nix-profile` for jobs that expect a fuller Linux CI image:

| Tool | Why |
|------|-----|
| `gcc` / `gnumake` / `pkg-config` | `go test -race` needs CGO |
| `syft` | GoReleaser SBOM step |
| `podman` + `docker` (client) | Docker Build / GHCR jobs |
| `cosign` | GoReleaser artifact signing |
| `~/.local/bin/docker` wrapper | `docker build` → `podman build` (buildx+podman otherwise drops tags) |
| `~/.config/containers/policy.json` | Required for rootless pulls/builds |

Runner unit env:

- `CGO_ENABLED=1`
- `PATH` includes `%h/.nix-profile/bin`
- `DOCKER_HOST=unix:///run/user/1001/podman/podman.sock`
- `podman.socket` enabled (`systemctl --user enable --now podman.socket`)

`loginctl enable-linger ender` still needs root/sudo once so the user unit survives logout.

## Reconfigure / reinstall

```bash
# From a machine with gh (repo admin on Muxcore-Media/core):
TOKEN=$(gh api -X POST repos/Muxcore-Media/core/actions/runners/registration-token --jq .token)

ssh gringotts 'bash -s' <<EOF
set -euo pipefail
export NIX_CONFIG=\$'substituters = https://cache.nixos.org/\\ntrusted-public-keys = cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY='
STORE=\$(nix build --no-link --print-out-paths nixpkgs#github-runner)
export RUNNER_ROOT=\$HOME/.github-runner-gringotts
mkdir -p "\$RUNNER_ROOT"
"\$STORE/bin/config.sh" remove --token "$TOKEN" || true
"\$STORE/bin/config.sh" --unattended \
  --url https://github.com/Muxcore-Media/core \
  --token "$TOKEN" \
  --name gringotts \
  --labels self-hosted,Linux,X64,gringotts \
  --work "\$RUNNER_ROOT/_work" \
  --replace
# Ensure unit ExecStart points at \$STORE/bin/Runner.Listener
systemctl --user restart github-actions-runner.service
EOF
```

## Org-wide runner (optional)

Repo registration only serves **core**. For all Muxcore-Media repos:

1. Grant `gh` the `admin:org` scope once:  
   `gh auth refresh -h github.com -s admin:org,repo,workflow,read:org`
2. Or paste a token from  
   https://github.com/organizations/Muxcore-Media/settings/actions/runners/new
3. Re-run config with `--url https://github.com/Muxcore-Media` instead of the core repo URL.

Until then, modules that still use `runs-on: ubuntu-latest` will stay queued; point them at `self-hosted` (see `wave1/GOLDEN_CI.yml.template`).
