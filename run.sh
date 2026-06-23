#!/usr/bin/env bash
#
# run.sh — Linux/macOS dev launcher for ServerManager.
#
# Starts the API, agent, and web dev server together with sensible local
# defaults, mirroring run.ps1 (the Windows launcher). This is a deliberately
# simple supervisor: it boots all three services and tears them all down on
# Ctrl+C. It does NOT do run.ps1's file-watch reload or auto-restart — restart
# the script after editing Go sources.
#
# Usage:
#   ./run.sh                 # start everything with defaults
#   ./run.sh --skip-install  # don't run `pnpm install` for the web app
#
# Override any default via environment variables, e.g.:
#   API_PORT=9000 WEB_PORT=4000 ADMIN_PASSWORD=hunter2 ./run.sh

set -euo pipefail

# Resolve the repo root from this script's location, regardless of CWD.
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"

API_DIR="$ROOT/apps/api"
AGENT_DIR="$ROOT/apps/agent"
WEB_DIR="$ROOT/apps/web"

# ── Defaults (override via env) — kept in sync with run.ps1 ──────────────────
API_PORT="${API_PORT:-8081}"
AGENT_PORT="${AGENT_PORT:-8090}"
WEB_PORT="${WEB_PORT:-3000}"
BIND_HOST="${BIND_HOST:-0.0.0.0}"
ADMIN_EMAIL="${ADMIN_EMAIL:-admin@example.com}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-changeme}"
JWT_SECRET="${JWT_SECRET:-local-dev-jwt-secret-change-me}"
AGENT_TOKEN="${AGENT_TOKEN:-dev-agent-token}"
DATABASE_PATH="${DATABASE_PATH:-$API_DIR/mcsm.db}"
SERVER_ROOT="${SERVER_ROOT:-$ROOT/servers}"

# The host the web dev server uses to reach the API. When binding to all
# interfaces, connect over loopback (matches run.ps1's Resolve-LocalConnectHost).
if [[ "$BIND_HOST" == "0.0.0.0" || "$BIND_HOST" == "::" ]]; then
  API_CONNECT_HOST="127.0.0.1"
else
  API_CONNECT_HOST="$BIND_HOST"
fi

SKIP_INSTALL=0
for arg in "$@"; do
  case "$arg" in
    --skip-install) SKIP_INSTALL=1 ;;
    -h|--help)
      grep '^#' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *) echo "Unknown argument: $arg (try --help)" >&2; exit 2 ;;
  esac
done

# ── Preflight: required tools ────────────────────────────────────────────────
missing=0
for cmd in go node pnpm; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "error: required command '$cmd' not found on PATH" >&2
    missing=1
  fi
done
[[ "$missing" -eq 0 ]] || { echo "Install the missing tool(s) and re-run." >&2; exit 1; }

# ── Web dependencies ─────────────────────────────────────────────────────────
if [[ "$SKIP_INSTALL" -eq 0 && ! -d "$WEB_DIR/node_modules" ]]; then
  echo "[setup] installing web dependencies (pnpm install)..."
  (cd "$WEB_DIR" && pnpm install)
else
  echo "[setup] web dependencies present; skipping install (use without --skip-install to force a check)."
fi

mkdir -p "$SERVER_ROOT"

# ── Supervision: track child PIDs, tear everything down on exit ──────────────
# Kept portable (no `setsid`, no `wait -n`) so this also works on macOS's
# default bash 3.2. On Ctrl+C the terminal already signals the whole foreground
# process group; cleanup() is the belt-and-suspenders pass that also reaps
# `go run`/`pnpm` grandchildren.
pids=()
cleanup() {
  trap - INT TERM EXIT
  echo ""
  echo "Stopping ServerManager..."
  [[ ${#pids[@]} -eq 0 ]] && return 0
  for pid in "${pids[@]}"; do
    # Kill direct children first (e.g. the binary `go run` compiled, vite under
    # pnpm), then the tracked process itself.
    pkill -TERM -P "$pid" 2>/dev/null || true
    kill -TERM "$pid" 2>/dev/null || true
  done
  wait 2>/dev/null || true
}
trap cleanup INT TERM EXIT

# Start a service: start_service NAME DIR VAR=value ... -- command args...
start_service() {
  local name="$1"; shift
  local dir="$1"; shift
  local envs=()
  while [[ "$1" != "--" ]]; do envs+=("$1"); shift; done
  shift # drop the --

  ( cd "$dir" && exec env "${envs[@]}" "$@" ) &
  local pid=$!
  pids+=("$pid")
  echo "[start] $name (pid $pid)"
}

echo ""
echo "Starting ServerManager..."
echo "  Web:   http://localhost:$WEB_PORT"
echo "  API:   http://localhost:$API_PORT"
echo "  Agent: http://localhost:$AGENT_PORT"
echo "  Bind:  $BIND_HOST"
echo "  Admin: $ADMIN_EMAIL / $ADMIN_PASSWORD"
echo ""
echo "Press Ctrl+C to stop all services."
echo ""

start_service "agent" "$AGENT_DIR" \
  MCSM_DEV_MODE=1 \
  AGENT_HOST="$BIND_HOST" \
  AGENT_PORT="$AGENT_PORT" \
  AGENT_TOKEN="$AGENT_TOKEN" \
  AGENT_SERVER_ROOT="$SERVER_ROOT" \
  -- go run ./cmd/agent

start_service "api" "$API_DIR" \
  MCSM_DEV_MODE=1 \
  API_HOST="$BIND_HOST" \
  API_PORT="$API_PORT" \
  DATABASE_PATH="$DATABASE_PATH" \
  JWT_SECRET="$JWT_SECRET" \
  ADMIN_EMAIL="$ADMIN_EMAIL" \
  ADMIN_PASSWORD="$ADMIN_PASSWORD" \
  RESET_ADMIN_PASSWORD=1 \
  SERVER_ROOT="$SERVER_ROOT" \
  AUTO_REGISTER_LOCAL_AGENT=1 \
  LOCAL_AGENT_NAME="Local Agent" \
  LOCAL_AGENT_FQDN=localhost \
  LOCAL_AGENT_PORT="$AGENT_PORT" \
  LOCAL_AGENT_SCHEME=http \
  LOCAL_AGENT_TOKEN="$AGENT_TOKEN" \
  -- go run ./cmd/server

start_service "web" "$WEB_DIR" \
  VITE_API_HOST="$API_CONNECT_HOST" \
  VITE_API_PORT="$API_PORT" \
  PORT="$WEB_PORT" \
  -- pnpm dev --host "$BIND_HOST" --port "$WEB_PORT"

# Poll until any service exits, then the EXIT trap (cleanup) stops the rest.
# A plain poll loop avoids `wait -n` (bash 4.3+) so this runs on macOS too.
while true; do
  for pid in "${pids[@]}"; do
    if ! kill -0 "$pid" 2>/dev/null; then
      echo "A service (pid $pid) exited; shutting the rest down."
      exit 1
    fi
  done
  sleep 1
done
