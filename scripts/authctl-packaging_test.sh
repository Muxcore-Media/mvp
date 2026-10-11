#!/usr/bin/env bash
# Offline packaging fixtures: execute the real host/publisher scripts and the
# Dockerfile's authctl RUN command with recording Go/container executables.
# These checks do not compile Go or build/run a container image.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
python3 - "$ROOT" <<'PY'
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

SOURCE = Path(sys.argv.pop())


class AuthctlPackaging(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="authctl packaging ")
        self.addCleanup(self.temp.cleanup)
        self.ws = Path(self.temp.name)
        self.mvp = self.ws / "mvp"
        (self.mvp / "scripts").mkdir(parents=True)
        shutil.copytree(SOURCE / "dockerfiles", self.mvp / "dockerfiles")
        for name in ("build-module-binaries.sh", "publish-module-images.sh"):
            shutil.copy2(SOURCE / "scripts" / name, self.mvp / "scripts" / name)
        for module in ("auth-local", "api-rest", "userdata-local"):
            (self.ws / module / "cmd/module").mkdir(parents=True)
        (self.ws / "auth-local/cmd/authctl").mkdir()
        # A decoy command must not change non-auth module packaging.
        (self.ws / "api-rest/cmd/authctl").mkdir()
        (self.ws / "userdata-local/cmd/userdata-health").mkdir()
        (self.ws / "go.work").write_text("go 1.26.6\n")
        self.bin = self.ws / "tools"
        self.bin.mkdir()
        self.out = self.ws / "prebuilt"
        self.go_log = self.ws / "go.jsonl"
        self.runtime_log = self.ws / "runtime.jsonl"
        self.env = dict(os.environ, PATH=str(self.bin) + os.pathsep + os.environ["PATH"],
                        MUXCORE_GO_WORK=str(self.ws / "go.work"), BUILD_JOBS="1",
                        AUTHCTL_TEST_GO_LOG=str(self.go_log),
                        AUTHCTL_TEST_RUNTIME_LOG=str(self.runtime_log),
                        AUTHCTL_TEST_ASSETS=str(self.ws / "assets"),
                        CONTAINER_RUNTIME=str(self.bin / "runtime"), BUILD_ONLY="1",
                        MUXCORE_REGISTRY="fixture.invalid/muxcore", GOTOOLCHAIN="local")
        for key in ("MODULES", "PREBUILT_DIR", "PREBUILT_NO_BUILD",
                    "AUTHCTL_TEST_FAIL_GO", "AUTHCTL_TEST_SKIP_OUTPUT"):
            self.env.pop(key, None)
        self.executable(self.bin / "go", '''#!/usr/bin/env python3
import json, os, pathlib, sys
args = sys.argv[1:]
assert args[0] == "build", args
package = args[-1]
output = args[args.index("-o") + 1]
with open(os.environ["AUTHCTL_TEST_GO_LOG"], "a") as log:
    log.write(json.dumps({"cwd": os.getcwd(), "args": args, "package": package,
                          "output": output, "cgo": os.environ.get("CGO_ENABLED")}) + "\\n")
if not pathlib.Path(package).is_dir():
    sys.exit(18)
if package.rstrip("/") == "./cmd/authctl":
    if os.environ.get("AUTHCTL_TEST_FAIL_GO") == "1":
        sys.exit(17)
    if os.environ.get("AUTHCTL_TEST_SKIP_OUTPUT") == "1":
        sys.exit(0)
path = pathlib.Path(output)
if path.parent == pathlib.Path("/assets"):
    path = pathlib.Path(os.environ["AUTHCTL_TEST_ASSETS"]) / path.name
path.parent.mkdir(parents=True, exist_ok=True)
path.write_text("#!/bin/sh\\nexit 0\\n")
path.chmod(0o755)
''')
        self.executable(self.bin / "runtime", '''#!/usr/bin/env python3
import json, os, sys
with open(os.environ["AUTHCTL_TEST_RUNTIME_LOG"], "a") as log:
    log.write(json.dumps(sys.argv[1:]) + "\\n")
''')

    @staticmethod
    def executable(path, text="#!/bin/sh\nexit 0\n"):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text)
        path.chmod(0o755)

    @staticmethod
    def records(path):
        return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []

    def run_script(self, name, *args, **extra):
        return subprocess.run(["bash", str(self.mvp / "scripts" / name), *map(str, args)],
                              env=dict(self.env, **extra), text=True, capture_output=True, timeout=15)

    def build(self, *modules, **extra):
        return self.run_script("build-module-binaries.sh", self.out, *modules, **extra)

    def publish(self, modules, **extra):
        return self.run_script("publish-module-images.sh", "fixture", MODULES=modules,
                               PREBUILT_DIR=str(self.out), **extra)

    def assert_success(self, result):
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def assert_refused_before_container(self, result):
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(self.records(self.runtime_log), [], "container called with incomplete artifacts")

    def test_host_builds_authctl_and_preserves_other_modules(self):
        self.assert_success(self.build("auth-local", "api-rest", "userdata-local"))
        actual = [(Path(r["cwd"]).name, r["package"], Path(r["output"]).relative_to(self.out).as_posix())
                  for r in self.records(self.go_log)]
        self.assertEqual(actual, [
            ("auth-local", "./cmd/module/", "auth-local/module"),
            ("auth-local", "./cmd/authctl/", "auth-local/authctl"),
            ("api-rest", "./cmd/module/", "api-rest/module"),
            ("userdata-local", "./cmd/module/", "userdata-local/module"),
            ("userdata-local", "./cmd/userdata-health/", "userdata-local/userdata-health")])
        for record in self.records(self.go_log):
            self.assertEqual(record["cgo"], "0")
            for flag in ("-trimpath", "-buildvcs=false", "-ldflags=-buildid="):
                self.assertIn(flag, record["args"])
        self.assertTrue(os.access(self.out / "auth-local/authctl", os.X_OK))
        self.assertFalse((self.out / "api-rest/authctl").exists())

    def test_host_refuses_missing_authctl_source(self):
        (self.ws / "auth-local/cmd/authctl").rmdir()
        self.assertNotEqual(self.build("auth-local").returncode, 0)

    def test_host_propagates_authctl_build_failure(self):
        self.assertNotEqual(self.build("auth-local", AUTHCTL_TEST_FAIL_GO="1").returncode, 0)

    def test_prebuilt_refuses_missing_or_nonexecutable_authctl_before_any_image(self):
        for module in ("api-rest", "auth-local"):
            self.executable(self.out / module / "module")
        for mode in ("missing", "nonexecutable"):
            with self.subTest(mode=mode):
                if mode == "nonexecutable":
                    (self.out / "auth-local/authctl").write_text("not executable")
                self.assert_refused_before_container(self.publish("api-rest auth-local", PREBUILT_NO_BUILD="1"))
        self.assertEqual(self.records(self.go_log), [])

    def test_complete_auth_and_non_auth_prebuilt_artifacts_are_usable(self):
        for module in ("api-rest", "auth-local"):
            self.executable(self.out / module / "module")
        self.executable(self.out / "auth-local/authctl")
        self.assert_success(self.publish("auth-local api-rest", PREBUILT_NO_BUILD="1"))
        calls = self.records(self.runtime_log)
        self.assertEqual(len(calls), 2)
        for call, module in zip(calls, ("auth-local", "api-rest")):
            self.assertEqual(call[0], "build")
            self.assertEqual(call[-1], str(self.out / module))
            self.assertEqual(Path(call[call.index("-f") + 1]).name, "module-prebuilt.Dockerfile")
        self.assertEqual(self.records(self.go_log), [])

    def test_prebuilt_rebuilds_auth_when_only_authctl_is_missing(self):
        self.executable(self.out / "auth-local/module")
        self.assert_success(self.publish("auth-local"))
        self.assertTrue(os.access(self.out / "auth-local/authctl", os.X_OK))
        self.assertEqual([r["package"] for r in self.records(self.go_log)], ["./cmd/module/", "./cmd/authctl/"])
        self.assertEqual(len(self.records(self.runtime_log)), 1)

    def test_prebuilt_rechecks_output_before_packaging(self):
        self.executable(self.out / "auth-local/module")
        self.assert_refused_before_container(self.publish("auth-local", AUTHCTL_TEST_SKIP_OUTPUT="1"))

    def docker_authctl_command(self):
        dockerfile = (self.mvp / "dockerfiles/module.Dockerfile").read_text()
        commands = [line[4:] for line in dockerfile.replace("\\\n", " ").splitlines()
                    if line.startswith("RUN ") and "./cmd/authctl" in line]
        self.assertEqual(len(commands), 1, "Docker build must explicitly package cmd/authctl")
        # The exercised /assets/authctl output must be copied into /app/authctl.
        self.assertIn("WORKDIR /app\nCOPY --from=builder /module ./module\nCOPY --from=builder /assets/ ./", dockerfile)
        return commands[0]

    def run_docker_command(self, module, **extra):
        return subprocess.run(["sh", "-eu", "-c", self.docker_authctl_command()],
                              cwd=self.ws / module, env=dict(self.env, MODULE=module, **extra),
                              text=True, capture_output=True, timeout=15)

    def test_docker_build_command_packages_only_auth_cli(self):
        self.assert_success(self.run_docker_command("api-rest"))
        self.assertEqual(self.records(self.go_log), [])
        self.assert_success(self.run_docker_command("auth-local"))
        calls = self.records(self.go_log)
        self.assertEqual(len(calls), 1)
        self.assertEqual(calls[0]["package"], "./cmd/authctl")
        self.assertEqual(calls[0]["output"], "/assets/authctl")
        self.assertEqual(calls[0]["cgo"], "0")
        self.assertTrue(os.access(self.ws / "assets/authctl", os.X_OK))

    def test_docker_build_propagates_authctl_build_failure(self):
        self.assertNotEqual(self.run_docker_command("auth-local", AUTHCTL_TEST_FAIL_GO="1").returncode, 0)

    def test_docker_build_refuses_missing_authctl_source(self):
        (self.ws / "auth-local/cmd/authctl").rmdir()
        self.assertNotEqual(self.run_docker_command("auth-local").returncode, 0)


unittest.main(verbosity=2)
PY
