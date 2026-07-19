#!/usr/bin/env bash
#
# deploy.sh — build and (re)start this 3x-ui fork from source on a server, and
# persist XUI_SECRET_KEY so you never have to `export` it before each run.
#
# It builds in place with the frontend `dist/` that must already be present
# (this repo's `internal/web/dist/` is git-ignored and embedded at compile time;
# a plain `git clone` does NOT contain it). Ship it via the deploy tarball
# (git archive HEAD + internal/web/dist) as before, extract, then run this.
#
# XUI_SECRET_KEY handling — READ THIS:
#   The panel encrypts stored SSH credentials with XUI_SECRET_KEY. If the key
#   changes, every already-stored credential becomes undecryptable. So this
#   script NEVER generates or overwrites the key. It only WRITES the key into
#   the env file when that file has none yet, using the value you provide via
#   the XUI_SECRET_KEY environment variable (or --secret <value>). Once written,
#   later runs need nothing — the panel auto-loads /etc/default/x-ui on start.
#
# Usage (first deploy, one time):
#   XUI_SECRET_KEY=<your-fixed-key> ./deploy.sh
# Usage (subsequent deploys — key already persisted):
#   ./deploy.sh
#
set -euo pipefail

ENV_FILE="/etc/default/x-ui"      # auto-loaded by the panel on every start
BIN_NAME="x-ui"
SECRET=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --secret) SECRET="$2"; shift 2 ;;
    --env-file) ENV_FILE="$2"; shift 2 ;;
    -h|--help)
      grep '^#' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done
: "${SECRET:=${XUI_SECRET_KEY:-}}"

log() { printf '\033[0;32m[deploy]\033[0m %s\n' "$*"; }
err() { printf '\033[0;31m[deploy]\033[0m %s\n' "$*" >&2; }

# --- 1. persist XUI_SECRET_KEY into the env file (write only if absent) --------
ensure_secret() {
  if [[ -f "$ENV_FILE" ]] && grep -q '^XUI_SECRET_KEY=' "$ENV_FILE"; then
    log "XUI_SECRET_KEY already set in $ENV_FILE — leaving it unchanged."
    return
  fi
  if [[ -z "$SECRET" ]]; then
    err "XUI_SECRET_KEY is not set in $ENV_FILE and none was provided."
    err "Provide it once so it can be persisted (it will NOT be generated,"
    err "because a new key would make existing encrypted SSH credentials"
    err "undecryptable):"
    err "    XUI_SECRET_KEY=<your-key> ./deploy.sh"
    exit 1
  fi
  # A key with unusual characters could break the env-file line, but the panel's
  # keys are hex, so a plain KEY=VALUE line is fine.
  umask 077
  touch "$ENV_FILE"
  printf 'XUI_SECRET_KEY=%s\n' "$SECRET" >> "$ENV_FILE"
  chmod 600 "$ENV_FILE"
  log "Wrote XUI_SECRET_KEY to $ENV_FILE (chmod 600). Future runs need no export."
}

# --- 2. build ------------------------------------------------------------------
build() {
  if [[ ! -f internal/web/dist/index.html ]]; then
    err "internal/web/dist/ is missing — this build embeds the frontend, so it"
    err "must be present. Ship it via the deploy tarball and extract here first."
    exit 1
  fi
  command -v go >/dev/null || { err "go not on PATH (try: export PATH=\$PATH:/usr/local/go/bin)"; exit 1; }
  log "Building $BIN_NAME (CGO_ENABLED=1)…"
  CGO_ENABLED=1 go build -o "$BIN_NAME" .
  log "Build OK."
}

# --- 3. (re)start --------------------------------------------------------------
restart() {
  pkill -f "$BIN_NAME" 2>/dev/null || true
  log "Starting $BIN_NAME…"
  # Load the persisted env file for THIS shell too, so `run` sees the key even
  # on the very first deploy in the same session.
  set -a; [[ -f "$ENV_FILE" ]] && . "$ENV_FILE"; set +a
  exec "./$BIN_NAME" run
}

ensure_secret
build
restart
