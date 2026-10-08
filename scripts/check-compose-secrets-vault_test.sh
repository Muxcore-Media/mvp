#!/usr/bin/env bash
# Render real Compose models: profiles are additive; the final Vault overlay
# replaces the default file provider. No daemon or external Vault is needed.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
command -v docker >/dev/null || { echo "FAIL: Docker Compose with !reset support is required for provider rendering tests" >&2; exit 1; }
command -v python3 >/dev/null || { echo "FAIL: python3 is required" >&2; exit 1; }
docker compose version >/dev/null

python3 - "$ROOT" <<'PY'
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

root = Path(sys.argv[1])
env = os.environ.copy()
for key in ("COMPOSE_FILE", "COMPOSE_PROFILES", "COMPOSE_ENV_FILES"):
    env.pop(key, None)
env.update(MVP_ADMIN_PASSWORD="parse-only", HEALTH_MONITOR_HTTP_TOKEN="parse-only",
           TRANSCODER_HTTP_TOKEN="parse-only", SECRETS_BACKEND="")
variants = [
    ["docker-compose.yml"],
    ["docker-compose.registry.yml"],
    ["docker-compose.registry.yml", "docker-compose.dev.yml"],
]
overlay = "docker-compose.secrets-vault.yml"
count = 0
with tempfile.TemporaryDirectory(prefix="muxcore-provider-test-") as temp:
    fixture = str(Path(temp) / "parse.env")
    subprocess.run(["bash", str(root / "scripts/gen-enrollment.sh"), "--env-file", fixture],
                   check=True, stdout=subprocess.DEVNULL)

    def render(files, backend="", profiles=(), error=None, extra_env=None):
        global count
        args = ["docker", "compose", "--env-file", fixture]
        for file in files:
            args += ["-f", str(root / file)]
        for profile in profiles:
            args += ["--profile", profile]
        result = subprocess.run(args + ["config", "--format", "json"],
                                env=dict(env, SECRETS_BACKEND=backend, **(extra_env or {})),
                                capture_output=True, text=True)
        count += 1
        if error:
            assert result.returncode != 0, f"missing {error} unexpectedly rendered"
            assert error in result.stderr, result.stderr
            return None
        if result.returncode:
            sys.exit("FAIL: compose render " + "+".join(files) + ": " + result.stderr)
        return json.loads(result.stdout)

    def provider(model, expected):
        selected = {s for s in model["services"] if s in {"secrets-file", "secrets-vault"}}
        assert selected == {expected}, f"expected only {expected}, got {selected}"
        service = model["services"][expected]
        assert service["environment"]["MUXCORE_MODULE_ID"] == expected
        return service

    for files in variants:
        file_provider = provider(render(files), "secrets-file")
        # Setting provider credentials alone is not an opt-in.
        provider(render(files, backend="vault"), "secrets-file")
        for profiles in [(), ("secrets-vault",), ("*",)]:
            model = render(files + [overlay], backend="vault", profiles=profiles)
            vault = provider(model, "secrets-vault")
            assert vault["environment"]["SECRETS_BACKEND"] == "vault"
            assert vault["environment"]["SECRETS_GRPC_ADDR"].endswith(":9551")
            if "docker-compose.registry.yml" in files:
                e = vault["environment"]
                assert e["MUXCORE_BOOTSTRAP_TOKEN"].startswith("mct_2_secrets-vault_")
                assert e["MUXCORE_ENROLL_DNS_NAMES"] == "secrets-vault"
                mounts = {v["target"]: v for v in vault["volumes"]}
                assert mounts["/data/mesh-id"]["source"] == "secrets-vault-id"
                assert mounts["/data/mesh-ca"]["read_only"] is True
                dev = "docker-compose.dev.yml" in files
                assert e["MUXCORE_PROFILE"] == ("dev" if dev else "household")
                assert (e.get("MUXCORE_INSECURE_DISABLE_TLS") == "true") == dev
        render(files + [overlay], error="SECRETS_BACKEND")
        # Rolling back to the base reuses the original file mounts/identity.
        assert provider(render(files), "secrets-file")["volumes"] == file_provider["volumes"]
        if "docker-compose.registry.yml" in files:
            # Base interpolation still requires enrollment for the removed service.
            render(files + [overlay], backend="vault", error="MUXCORE_ENROLL_TOKEN_SECRETS_FILE",
                   extra_env={"MUXCORE_ENROLL_TOKEN_SECRETS_FILE": ""})
        # Record the unsupported legacy path; never label it exclusive.
        legacy = render(files, backend="vault", profiles=("secrets-vault",))
        assert {"secrets-file", "secrets-vault"} <= legacy["services"].keys()

    # A later dev override can recreate the removed service: selection is last.
    args = ["docker", "compose", "--env-file", fixture]
    for file in ["docker-compose.registry.yml", overlay, "docker-compose.dev.yml"]:
        args += ["-f", str(root / file)]
    result = subprocess.run(args + ["config", "--format", "json"],
                            env=dict(env, SECRETS_BACKEND="vault"), capture_output=True, text=True)
    count += 1
    if result.returncode == 0:
        assert "secrets-file" in json.loads(result.stdout)["services"]
    else:
        assert "secrets-file" in result.stderr, result.stderr

print(f"OK: {count} compose renders verify default/selected providers, mesh identity and fail-closed config")
print("OK: raw --profile secrets-vault remains additive and unsupported; the final overlay is required")
PY
