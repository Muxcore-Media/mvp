# Backup and restore (household install)

How to back up a MuxCore household install with `backup-local`, restore it onto a fresh install, and prove the procedure works with the automated restore drill. This covers FR-BAK-004 (roadmap T-M2-05). The design is [ADR-0013](https://github.com/Muxcore-Media/umbrella/blob/main/docs/adr/0013-backup-coverage-model.md).

## What is backed up

[`household-manifest.yaml`](../household-manifest.yaml) `state:` is the source of truth. Each stateful module has an entry giving its compose volume, its host-mode directory under `data/` (`host:`), and whether it is archived. To list the mapping for your deployment:

```bash
scripts/restore-drill.sh --mode host --dry-run      # run-host.sh: data/<dir>
scripts/restore-drill.sh --mode compose --dry-run   # registry compose: named volumes
```

| Kind | Examples | In the archive? |
|------|----------|-----------------|
| Module state | `auth-local` accounts, `request-media` requests, `userdata-local` progress/favorites, library databases, BFF/admin-ui JSON | Yes: `data/<prefix>/…` |
| Key material (`backup: false`) | `encryption-aesgcm` keyring (`data/encryption`, `encryption-aesgcm-data`); compose `SECRETS_MASTER_KEY` in `.env` | **No.** Escrow it separately, for example in a password manager or an offline copy. A restored `secrets.json` cannot be read without its key. |
| Library media, downloads | `data/library`, `data/downloads`, the `movies`/`shows`/`downloads` volumes | **No.** Back these up with your own media strategy. |

The archive prefix is the module ID in compose (backup-local mounts each volume at `/source/<module-id>`). In host mode it is the `host:` directory name, because `run-host.sh` passes those directories as `BACKUP_SOURCE_DIRS`. In host mode, `admin-ui` and `media-ui` share `data/media-ui`.

The archive contains password hashes, sessions and household personal data. Store it like a secret.

Host-mode caveats:
- `secrets-file` keeps its key in `data/secrets/master.key`, inside the archived directory. Compose keeps it outside, in `SECRETS_MASTER_KEY`.
- `media-music` (`data/music`) and `media-transcoder` (`data/transcoder`) also keep state in host mode. They are not in the manifest yet, so they are not archived.

## Take a consistent backup

backup-local copies files without coordinating with their writers (ADR-0013 §3), so writers must be stopped first:

1. Stop every service except `core` and `backup-local`:
   - **Host:** run `./run-host.sh stop-one <name>` for each running service.
   - **Compose:** run `docker compose -f docker-compose.registry.yml stop <services…>`.
2. Create the backup and verify it. Use the admin UI / BFF (`POST /api/backups`), or call gRPC directly:
   - **Host:** `bin/backupctl create`, then `bin/backupctl verify -id <id> -restore-test`. Build it with `go build -o bin/backupctl ./cmd/backupctl`.
   - **Compose:** use the same subcommands through `scripts/lib/registry-smoke.sh` `registry_smoke_cmd_backupctl`. This runs grpcurl on the compose network with the vendored `proto/muxcore/backup/v1/backup.proto`.
3. Copy `<id>.tar.gz` off the host. It is in `data/backup/` (host) or the `backup-data` volume (compose).
4. Start the stack again: `./run-host.sh up` or `docker compose … --profile backup-local up -d`.

For host mode, `MVP_ENABLE_BACKUP_LOCAL=1` enables backup-local. Its default sources are the backed-up `state:` host directories; deriving them needs yq v4 and jq. For compose, use `--profile backup-local`.

## Restore onto a fresh install

1. Install the same stack version with the same `.env`. In compose, that includes `SECRETS_MASTER_KEY`.
2. Make sure no module has started on empty state:
   - **Host:** run `./run-host.sh stop`.
   - **Compose:** run `docker compose … down`. If the volumes were already initialized, remove them.
3. Start **only** `core` and `backup-local`:
   - **Host:** run `./run-host.sh restart core`, then `MVP_ENABLE_BACKUP_LOCAL=1 ./run-host.sh restart backup-local`.
   - **Compose:** run `docker compose … --profile backup-local create`, then `… up -d --no-deps core backup-local`.
4. Copy `<id>.tar.gz` into the new backup dir (`data/backup/` or the `backup-data` volume). backup-local re-indexes archives it finds there that are missing from its index. Use `backupctl list` to confirm it is indexed.
5. Run `RestoreBackup` into a staging directory:
   - **Host:** `bin/backupctl restore -id <id> -target data/restore/<name>`.
   - **Compose:** use target `/data/restore/<name>`, which is in the `backup-restore-data` volume.
6. Stop `core` and `backup-local`. Then copy each `data/<prefix>/` from the staging dir into that module's state location, using the mapping from `--dry-run`:
   - Replace the directory or volume contents.
   - In compose, keep the volume owner: the images run as uid 1000.
   - Put the escrowed key material back in place.
7. Start everything and log in with your **original** admin password. auth-local only bootstraps `MVP_ADMIN_PASSWORD` when its database is empty, so a restored database keeps the old accounts.

## Restore drill (automated, fixtures only)

`scripts/restore-drill.sh` runs the whole cycle and checks the result. **It wipes all module state**, so use it only on a fixture or test install. It refuses to run without `--confirm-wipe`.

```bash
# Host mode (needs Go, yq v4, jq, curl; module binaries in bin/ as for run-host.sh)
MVP_ENABLE_ACQUISITION=1 SMOKE_REQUIRE_ACQUISITION=1 \
  scripts/restore-drill.sh --mode host --confirm-wipe

# Registry compose (docker/podman; MUXCORE_COMPOSE / MUXCORE_CONTAINER_CLI as for smoke)
scripts/restore-drill.sh --mode compose --confirm-wipe
```

| Step | What the drill does |
|------|---------------------|
| 1. Seed + snapshot | Brings the stack up with backup-local and runs `smoke.sh` to seed fixture data (movies, TV, the acquisition fixture, a request). Writes a `restore-drill` userdata entry. Logs in with the **original** admin password and snapshots `/api/me`, `/api/movies`, `/api/tv`, `/api/requests` and the userdata entry. |
| 2. Backup | Stops everything except `core` + `backup-local` and runs `CreateBackup`. Checks the archive: every non-empty backed-up state location has an entry (host mode), the seeded modules are present, and there are no unmapped or key-material prefixes. Then runs `VerifyBackup{restore_test: true}`, copies the archive to the work dir, and escrows `backup: false` state. |
| 3. Wipe | Stops the stack and deletes only the state locations, plus backup-local's archive and restore dirs. Library media and downloads stay. |
| 4. Fresh restore | Starts `core` + `backup-local` only and copies the archive in. Checks that backup-local re-indexed it, runs `RestoreBackup` into a staging dir, stops the stack, and copies `data/<prefix>/` back by the manifest mapping (step 6 above). |
| 5. Prove it | Starts everything with a **decoy** bootstrap password. Checks that the decoy is refused and the original password works, which proves auth-local did not re-bootstrap. Then diffs the snapshot and runs `smoke.sh` again. |

On success it prints `PASS: restore drill (N modules restored, snapshot equal)` and removes its work dir (unless you pass `--keep-workdir`). On failure the work dir is kept. It is 0700 and holds the archive copy, escrow, snapshots and the original password.

Useful environment variables:

| Variable | Purpose | Default |
|----------|---------|---------|
| `SMOKE_MEDIA_UI_URL` | BFF URL | `http://127.0.0.1:5173` |
| `RESTORE_DRILL_AUTH_URL` | auth-local URL | `http://127.0.0.1:9401` |
| `RESTORE_DRILL_TIMEOUT_SEC` | Readiness timeout | `240` |
| `RESTORE_DRILL_SETTLE_SEC` | Wait after the stack answers | `15` |
| `RESTORE_DRILL_VOLUME_OWNER` | Owner for compose volumes that had none recorded | `1000:1000` |

`smoke.sh` variables (`SMOKE_CATALOG`, `SMOKE_REQUIRE_ACQUISITION`, `MVP_ENABLE_*`) pass through. Offline tests: `scripts/restore-drill_test.sh`, which `scripts/run-script-tests.sh` runs.

After a compose drill, auth-local runs with the decoy bootstrap env. Run `docker compose … up -d` to recreate it from `.env`. Bootstrap is a no-op on the restored database.
