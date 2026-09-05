# GitHub ↔ Forgejo mirror (self-hosted CI)

Self-hosted GitHub Actions runners on vault fetch git through **Forgejo** (`git.zem.systems`). After a merge on GitHub, CI starts immediately with `github.sha`, but Forgejo may still lack that commit → checkout fails:

```text
fatal: remote error: upload-pack: not our ref <github-sha>
Forgejo: Failed to execute git command
```

Sibling jobs can sit **queued** behind the failed workspace. This is mirror lag, not missing source on GitHub.

## Fix layers (use together)

| Layer | Artifact | When |
|-------|----------|------|
| **1. Proactive mirror** | [`scripts/mirror-github-to-forgejo.sh`](../scripts/mirror-github-to-forgejo.sh) | On every GitHub default-branch push (webhook or cron) |
| **2. Audit / alert** | [`scripts/audit-forgejo-mirrors.sh`](../scripts/audit-forgejo-mirrors.sh) | Desk cron; exits 1 when GitHub tip missing on Forgejo |
| **3. CI fallback checkout** | [`scripts/ci-checkout-self-hosted.sh`](../scripts/ci-checkout-self-hosted.sh) | Self-hosted `.github/workflows` jobs (immediate unblock) |

Reverse direction (Forgejo origin → public GitHub): [`scripts/mirror-forgejo-to-github.sh`](../scripts/mirror-forgejo-to-github.sh).

Legacy desk bundle push (subset of modules): [`scripts/mirror-github-only-to-forgejo.sh`](../scripts/mirror-github-only-to-forgejo.sh) — prefer `mirror-github-to-forgejo.sh` for full org sync.

## Operator: mirror on GitHub push

On **desk** (or any host with `gh auth login` + Forgejo SSH):

```bash
cd _mvp   # or mvp checkout
./scripts/mirror-github-to-forgejo.sh              # full org
./scripts/mirror-github-to-forgejo.sh core         # one repo after merge
./scripts/mirror-github-to-forgejo.sh --check core # verify tip synced (exit 1 if behind)
```

**Required credentials (existing patterns):**

- `GITHUB_TOKEN` or `gh auth token` — read private `Muxcore-Media/*`
- SSH as `forgejo@git.zem.systems:2222` — push to `muxcore/*` (same as other mirror scripts)

**Recommended automation:** GitHub org webhook `push` → small listener on desk/vault that runs:

```bash
repo="${GITHUB_REPO_NAME}"   # from webhook payload
_mvp/scripts/mirror-github-to-forgejo.sh "$repo"
```

Or systemd timer / cron every 5 minutes:

```bash
_mvp/scripts/mirror-github-to-forgejo.sh
_mvp/scripts/audit-forgejo-mirrors.sh || mail -s 'Forgejo mirror lag' ops@…
```

Webhook wiring is **external** to this repo (no invented credentials); scripts are idempotent.

## Operator: audit divergence

```bash
export GITHUB_TOKEN="$(gh auth token)"
./scripts/audit-forgejo-mirrors.sh /path/to/MuxCore/workspace
```

Reports repos where GitHub default-branch tip is not on Forgejo. Exit code 1 → run `mirror-github-to-forgejo.sh` for listed repos.

Disable live GitHub checks: `AUDIT_GITHUB_TIPS=0 ./scripts/audit-forgejo-mirrors.sh`

## Module repos: self-hosted checkout (follow-up PRs)

**Workflow changes belong in each module repo** (`core`, `auth-local`, …), not in `mvp`. Replace bare `actions/checkout` on `runs-on: self-hosted` jobs.

### Option A — composite action (from `mvp`)

After this PR merges to `mvp` `main`:

```yaml
- name: Checkout (Forgejo mirror + GitHub fallback)
  uses: Muxcore-Media/mvp/.github/actions/checkout-mirror@main
  with:
    token: ${{ secrets.MUXCORE_CI_TOKEN }}
```

Requires `MUXCORE_CI_TOKEN` (same secret already used for `GOPRIVATE` module fetch).

### Option B — run script from checked-out `mvp` sibling

If the workspace vendors `_mvp/scripts`:

```yaml
- name: Checkout (Forgejo mirror + GitHub fallback)
  env:
    MUXCORE_CI_TOKEN: ${{ secrets.MUXCORE_CI_TOKEN }}
  run: bash _mvp/scripts/ci-checkout-self-hosted.sh
```

(Only works when `_mvp` is already present — usually Option A or copy script into the module.)

### Option C — strict mirror-first (slower)

```yaml
- uses: Muxcore-Media/mvp/.github/actions/checkout-mirror@main
  with:
    token: ${{ secrets.MUXCORE_CI_TOKEN }}
    mirror-sync-first: "1"
```

Fails fast with mirror-sync timeout if desk webhook is broken.

## Verification

1. Merge a trivial commit to `Muxcore-Media/core` `master`.
2. Before webhook: `./scripts/mirror-github-to-forgejo.sh --check core` → exit 1 with `BEHIND`.
3. Run `./scripts/mirror-github-to-forgejo.sh core` → exit 0.
4. `--check core` → `OK` with matching SHA prefix.
5. Re-run failed GitHub Actions run on self-hosted runner — with module workflow using checkout-mirror, job passes even if step 3 was skipped (GitHub fallback + backfill).

## Related

- Forgejo origin CI (`.forgejo/workflows`, `runs-on: native`): [`FORGEJO-RUNNER.md`](FORGEJO-RUNNER.md)
- Retired GitHub-hosted runners: [`GITHUB-RUNNER.md`](GITHUB-RUNNER.md)
- Tracking: [umbrella#25](https://github.com/Muxcore-Media/umbrella/issues/25)
