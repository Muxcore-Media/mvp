#!/usr/bin/env bash
# Fixture test for populate-spool-checksums.sh (ADR-0012): checksums come from a
# clean clone of the tag, are reproducible, and never touch the workspace.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="$SCRIPT_DIR/populate-spool-checksums.sh"

fail() { echo "FAIL: $*" >&2; exit 1; }

if ! command -v go >/dev/null 2>&1 || ! command -v jq >/dev/null 2>&1; then
  echo "skip: go and jq required"
  exit 0
fi
GOV="$(go env GOVERSION)"
GOV="${GOV#go}"
[[ "$GOV" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "skip: unsupported go version $GOV"; exit 0; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null GIT_TERMINAL_PROMPT=0

# make_repo <name> <with-cmd:1|0> <contracts-json>  -> bare repo tagged v0.0.1
make_repo() {
  local name="$1" with_cmd="$2" contracts="$3" w="$TMP/work-$1"
  mkdir -p "$w"
  printf 'module example.com/%s\n\ngo %s\n' "$name" "$GOV" >"$w/go.mod"
  printf '{"name":"%s","version":"0.0.1","contracts":%s}\n' "$name" "$contracts" >"$w/muxcore.json"
  if [[ "$with_cmd" == 1 ]]; then
    mkdir -p "$w/cmd/module"
    printf 'package main\n\nfunc main() { println("hi") }\n' >"$w/cmd/module/main.go"
  fi
  git -C "$w" init -q -b main
  git -C "$w" config user.email test@example.com
  git -C "$w" config user.name test
  git -C "$w" add -A
  git -C "$w" commit -qm init
  git -C "$w" tag v0.0.1
  git clone -q --bare "$w" "$TMP/$name.git"
}

make_tags() { # <file> <name>
  printf '{\n  "name": "t",\n  "modules": [\n    {\n      "repo": "file://%s/%s.git",\n      "version": "v0.0.1",\n      "required": true\n    }\n  ]\n}\n' "$TMP" "$2" >"$1"
}

make_repo good 1 '[]'
make_repo nocmd 0 '[]'
make_repo badcontract 1 '[{"repo":"example.com/contracts-x","version":"v0.2.0"}]'

# Workspace sentinel: the script must never modify it.
WS="$TMP/ws"; mkdir -p "$WS"; printf 'module sentinel\n' >"$WS/go.mod"
before="$(sha256sum "$WS/go.mod")"

make_tags "$TMP/a.json" good
cp "$TMP/a.json" "$TMP/b.json"
(cd "$WS" && bash "$SCRIPT" "$TMP/a.json" >/dev/null 2>&1) || fail "first run failed"
sum1="$(jq -r '.modules[0].checksum' "$TMP/a.json")"
[[ "$sum1" =~ ^sha256:[0-9a-f]{64}$ ]] || fail "bad checksum '$sum1'"
(cd "$WS" && bash "$SCRIPT" "$TMP/a.json" >/dev/null 2>&1) || fail "second run failed"
sum2="$(jq -r '.modules[0].checksum' "$TMP/a.json")"
[[ "$sum1" == "$sum2" ]] || fail "checksum not reproducible: $sum1 vs $sum2"
grep -q '^  "name": "t",$' "$TMP/a.json" || fail "2-space formatting not preserved"

# Multiple tag files in one run share one build and agree.
bash "$SCRIPT" "$TMP/b.json" "$TMP/a.json" >"$TMP/multi.log" 2>&1 || fail "multi-file run failed"
[[ "$(jq -r '.modules[0].checksum' "$TMP/b.json")" == "$sum1" ]] || fail "multi-file checksum differs"
[[ "$(grep -c 'build: ' "$TMP/multi.log")" == 1 ]] || fail "expected a single build for repeated repo@version"

[[ "$(sha256sum "$WS/go.mod")" == "$before" ]] || fail "workspace was modified"

# Missing ./cmd/module fails clearly.
make_tags "$TMP/c.json" nocmd
if bash "$SCRIPT" "$TMP/c.json" >"$TMP/c.log" 2>&1; then fail "missing cmd/module should fail"; fi
grep -q 'cmd/module' "$TMP/c.log" || fail "missing cmd/module message unclear"

# Contracts declared but not required by go.mod fail (ADR-0012 §3).
make_tags "$TMP/d.json" badcontract
if bash "$SCRIPT" "$TMP/d.json" >"$TMP/d.log" 2>&1; then fail "unmet contracts should fail"; fi
grep -q 'contracts-x' "$TMP/d.log" || fail "contracts failure message unclear"

# Unknown version fails.
jq '.modules[0].version = "v9.9.9"' "$TMP/a.json" >"$TMP/e.json"
if bash "$SCRIPT" "$TMP/e.json" >/dev/null 2>&1; then fail "unknown tag should fail"; fi

echo "ok: populate-spool-checksums"
