# CI runners — GitHub-hosted

MuxCore CI runs on **GitHub-hosted `ubuntu-latest` runners** in each `github.com/Muxcore-Media/<repo>` repository ([ADR-0002](https://github.com/Muxcore-Media/umbrella/blob/main/docs/adr/0002-github-sole-origin.md), [ADR-0003](https://github.com/Muxcore-Media/umbrella/blob/main/docs/adr/0003-ci-on-github-hosted-runners.md)). No self-hosted runner is used.

- Workflows check out with plain `actions/checkout`; there is no mirror or fallback checkout.
- Go module workflows (`.github/workflows/ci.yml`) are generated from the umbrella `scripts/ci-templates/` by `scripts/sync-ci.sh` — edit the template, not the copy.
- Private `github.com/Muxcore-Media/*` modules are fetched with the per-repo secret `MUXCORE_CI_TOKEN` via a `url.<github>.insteadOf` rewrite inside the job. Locally, run `gh auth setup-git` (or export `GH_TOKEN`); never embed tokens in URLs or committed files.
- This repo also runs `.github/workflows/script-tests.yml` (`scripts/run-script-tests.sh`); umbrella-only checks are skipped when the repo is checked out on its own.
