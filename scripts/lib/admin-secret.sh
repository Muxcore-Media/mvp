#!/usr/bin/env bash
# shellcheck shell=bash
# First-admin bootstrap secret helpers (FR-INS-004, NFR-SEC-001).
#
# There is no default admin password. The operator supplies MVP_ADMIN_PASSWORD;
# when unset, the dev host loop (run-host.sh) generates a random one into
# <root>/data/auth/admin.password (0600) and prints it exactly once.

# mvp_admin_password_file <root> — path of the generated-secret file.
mvp_admin_password_file() {
  printf '%s' "${MVP_ADMIN_PASSWORD_FILE:-$1/data/auth/admin.password}"
}

# mvp_gen_secret <bytes> — random hex string.
mvp_gen_secret() {
  local n="${1:-16}"
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex "$n"
  else
    od -An -tx1 -N"$n" /dev/urandom | tr -d ' \n'
  fi
}

# mvp_admin_password_load <root> — export MVP_ADMIN_PASSWORD from the
# environment or the generated file. Never generates. Leaves it unset if neither
# exists.
mvp_admin_password_load() {
  [[ -n "${MVP_ADMIN_PASSWORD:-}" ]] && return 0
  local f
  f="$(mvp_admin_password_file "$1")"
  if [[ -s "$f" ]]; then
    MVP_ADMIN_PASSWORD="$(tr -d '[:space:]' <"$f")"
    export MVP_ADMIN_PASSWORD
  fi
  return 0
}

# mvp_admin_password_ensure <root> — like load, but generates (0600 file, 0700
# dir) when nothing exists. The value is printed once, on generation; later runs
# only print the file path.
mvp_admin_password_ensure() {
  [[ -n "${MVP_ADMIN_PASSWORD:-}" ]] && return 0
  local f
  f="$(mvp_admin_password_file "$1")"
  if [[ ! -s "$f" ]]; then
    mkdir -p "$(dirname "$f")"
    chmod 700 "$(dirname "$f")"
    local v
    v="$(mvp_gen_secret 16)"
    (umask 077 && printf '%s\n' "$v" >"$f")
    echo "==> MVP_ADMIN_PASSWORD was not set: generated a one-time admin password." >&2
    echo "    admin password: $v" >&2
    echo "    stored 0600 at $f (not printed again); set MVP_ADMIN_PASSWORD to override." >&2
  else
    echo "==> MVP_ADMIN_PASSWORD not set: using generated password from $f" >&2
  fi
  chmod 600 "$f"
  mvp_admin_password_load "$1"
}

# mvp_require_admin_password <root> — load, or print guidance and return 1.
mvp_require_admin_password() {
  mvp_admin_password_load "$1"
  if [[ -z "${MVP_ADMIN_PASSWORD:-}" ]]; then
    echo "FAIL: MVP_ADMIN_PASSWORD is not set and $(mvp_admin_password_file "$1") does not exist." >&2
    echo "      Set it in .env (openssl rand -hex 16) or start the stack with ./run-host.sh up first." >&2
    return 1
  fi
}
