#!/usr/bin/env bash
#
# dev.sh — start Open Circuit SF locally on macOS for fast UI iteration.
#
# No PostgreSQL, no systemd, no migrations needed. Uses STORAGE=json (in-memory
# dev store, internal/devstore) and the dev auto-login middleware
# (internal/middleware.DevAutoLogin) so the account view opens immediately as
# the mock admin.
#
# Usage:
#   ./scripts/dev.sh           # hot-reload: Go API on :8080 + Vite dev server on :5173
#   ./scripts/dev.sh --built   # built-SPA: npm build + go run serving on :8080 only
#
# Open in browser:
#   hot-reload mode:  http://localhost:5173  (Vite proxies /api → :8080)
#   built mode:       http://localhost:8080
#
# Override any env var before calling, e.g.:
#   ADMIN_EMAIL=me@example.com ./scripts/dev.sh
#
# If :8080 or :5173 is already held by another process, dev.sh refuses to
# start rather than killing it (#0117) — it may be another agent's server, or
# the user's own editor preview (CLAUDE.md §8b). To reclaim a port you are
# sure is your own stale dev.sh, e.g. re-running after a terminal was closed
# without Ctrl-C:
#   RECLAIM_PORTS=1 ./scripts/dev.sh
#
# Ctrl-C stops everything cleanly.
#
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO"

# ── Dev environment defaults ─────────────────────────────────────────────────
# All of these can be overridden by setting them in the calling environment.
# DATABASE_URL is intentionally unset: STORAGE=json skips Postgres entirely.
export STORAGE="${STORAGE:-json}"
export BASE_URL="${BASE_URL:-http://localhost:8080}"
export WEBAUTHN_RP_ID="${WEBAUTHN_RP_ID:-localhost}"
export WEBAUTHN_RP_ORIGIN="${WEBAUTHN_RP_ORIGIN:-http://localhost:8080}"
export SESSION_SECRET="${SESSION_SECRET:-dev-session-secret-not-for-production}"
export ADMIN_EMAIL="${ADMIN_EMAIL:-admin@localhost}"
export PORT="${PORT:-8080}"
# AWS_REGION, EMAIL_FROM, and EMAIL_LIST_DOMAIN are unconditionally required by
# config.Load (#0116) even though STORAGE=json's serveDevMode never reads them
# for anything but that validation — dev mode never constructs the SES mailer
# or the send worker. The values below are placeholders that satisfy the
# check without looking like production config: EMAIL_FROM uses a "dev@"
# local part (production is "hello@…") and EMAIL_LIST_DOMAIN uses
# "lists.localhost" rather than the real "lists.opencircuitsf.com" (CLAUDE.md
# §9), so nobody mistakes a dev run for a production one. AWS_REGION is inert
# under STORAGE=json (no AWS SDK call is ever made on this path), so it is
# left at the real SES region for anyone who overrides STORAGE to exercise
# the Postgres path locally.
export AWS_REGION="${AWS_REGION:-us-west-2}"
export EMAIL_FROM="${EMAIL_FROM:-Open Circuit SF <dev@localhost>}"
export EMAIL_LIST_DOMAIN="${EMAIL_LIST_DOMAIN:-lists.localhost}"

step() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }
ok()   { printf '    \033[32m✓\033[0m %s\n' "$*"; }
info() { printf '    %s\n' "$*"; }

# ── Parse flags ──────────────────────────────────────────────────────────────
MODE="hot"
case "${1:-}" in
  --built|-b) MODE="built" ;;
  -h|--help) sed -n '2,30p' "$0"; exit 0 ;;
  "") : ;;
  *) printf 'Unknown flag: %s\n' "$1" >&2; exit 1 ;;
esac

# ── Preflight ────────────────────────────────────────────────────────────────
step "Preflight"
for c in go node npm; do
  command -v "$c" >/dev/null 2>&1 || { printf '  ERROR: %s not found\n' "$c" >&2; exit 1; }
done
ok "repo:    $REPO"
ok "storage: $STORAGE (no Postgres)"
ok "admin:   $ADMIN_EMAIL"
ok "port:    $PORT"

# Free the dev ports if a previous run left a server bound. `go run` leaks its
# compiled child process when its parent is killed, so a stale server can keep
# holding :8080 — which both blocks startup ("address already in use") AND keeps
# serving an OLD build.
#
# But a held port is not necessarily OUR stale process (#0117): it may be
# another agent's dev.sh (CLAUDE.md §5a permits several running at once), or
# the user's own editor preview (§8b — "never bind a fixed, shared port ...
# without verifying ownership" applies just as much to killing one). Default
# to refusing and naming the holder, the same shape §4/#0150 settled on for
# `testdb.sh gc`: a destructive default that assumes you're alone is what
# caused that incident, so don't repeat it here. RECLAIM_PORTS=1 is the
# explicit opt-in for the one case that's still common — a developer's own
# earlier dev.sh left an orphaned child (see header comment).
#
# `force=1` (equivalently RECLAIM_PORTS=1) is an explicit, user-requested
# override: the caller is asserting the holder is theirs to kill. Once asked,
# actually verify it worked — a swallowed `kill -9` failure followed by
# "freeing port ..." would claim success while the port stays held and the
# run fails later with a confusing "address already in use" (#0117 review).
RECLAIM_PORTS="${RECLAIM_PORTS:-0}"
free_port() {
  local p="$1" force="${2:-0}" pids pid cmd still
  pids="$(lsof -ti tcp:"$p" 2>/dev/null || true)"
  [ -z "$pids" ] && return 0

  if [ "$force" = "1" ] || [ "$RECLAIM_PORTS" = "1" ]; then
    info "freeing port $p (stale process: $(printf '%s' "$pids" | tr '\n' ' '))"
    # shellcheck disable=SC2086
    kill -9 $pids 2>/dev/null || true
    sleep 1
    still="$(lsof -ti tcp:"$p" 2>/dev/null || true)"
    if [ -n "$still" ]; then
      printf '  ERROR: port %s still held after kill -9 (pid(s): %s) — could not reclaim it.\n' "$p" "$(printf '%s' "$still" | tr '\n' ' ')" >&2
      exit 1
    fi
    return 0
  fi

  printf '  ERROR: port %s is already in use — dev.sh will not kill a process it did not start.\n' "$p" >&2
  for pid in $pids; do
    cmd="$(ps -p "$pid" -o command= 2>/dev/null || true)"
    [ -z "$cmd" ] && cmd="(process exited before it could be inspected)"
    printf '    pid %s: %s\n' "$pid" "$cmd" >&2
  done
  printf '  This may be another agent'"'"'s dev.sh (CLAUDE.md section 5a) or an unrelated process — not necessarily yours to kill.\n' >&2
  printf '  Options:\n' >&2
  printf '    - if it is genuinely yours (e.g. an orphaned dev.sh from a closed terminal), stop it: kill %s\n' "$(printf '%s' "$pids" | tr '\n' ' ')" >&2
  printf '    - or force dev.sh to reclaim it: RECLAIM_PORTS=1 ./scripts/dev.sh\n' >&2
  printf '    - or find out whose it is first: lsof -i tcp:%s\n' "$p" >&2
  exit 1
}
free_port "$PORT"
free_port 5173

# The EXIT-trap backstop (cleanup(), below) must never repeat this issue's own
# defect on the way out: re-deriving "whoever holds :$PORT right now" from
# lsof at exit time and killing it unconditionally is exactly what let a
# foreign process that took over the port after this run's Go server died
# (crash, stray pkill) get killed by an ordinary Ctrl-C (#0117 review). So the
# trap kills only pids THIS run is known to have bound — tracked in
# OWNED_PIDS, populated once the Go server is confirmed up, below — and warns
# (without killing) about anything else it finds holding the port at exit.
release_owned_port() {
  local p="$1" owned="$2" current c o matched ours="" foreign="" pid cmd remaining
  current="$(lsof -ti tcp:"$p" 2>/dev/null || true)"
  [ -z "$current" ] && return 0

  for c in $current; do
    matched=0
    for o in $owned; do
      [ "$c" = "$o" ] && { matched=1; break; }
    done
    if [ "$matched" = "1" ]; then
      ours="$ours $c"
    else
      foreign="$foreign $c"
    fi
  done

  if [ -n "$ours" ]; then
    info "freeing port $p (own process:$ours)"
    # shellcheck disable=SC2086
    kill -9 $ours 2>/dev/null || true
    sleep 1
    remaining=""
    for pid in $ours; do
      kill -0 "$pid" 2>/dev/null && remaining="$remaining $pid"
    done
    if [ -n "$remaining" ]; then
      printf '  WARNING: port %s still held after kill -9 (pid(s):%s) — a later run may fail with "address already in use".\n' "$p" "$remaining" >&2
    fi
  fi

  if [ -n "$foreign" ]; then
    printf '  WARNING: port %s is now held by a process this run did not start — leaving it alone.\n' "$p" >&2
    for pid in $foreign; do
      cmd="$(ps -p "$pid" -o command= 2>/dev/null || true)"
      [ -z "$cmd" ] && cmd="(process exited before it could be inspected)"
      printf '    pid %s: %s\n' "$pid" "$cmd" >&2
    done
  fi
}

# ── Built-SPA mode ───────────────────────────────────────────────────────────
if [ "$MODE" = "built" ]; then
  step "Building Svelte SPA (web/)"
  ( cd web && { [ -f package-lock.json ] && npm ci --silent || npm install --silent; } && npm run build )
  ok "SPA built into web/dist/"

  step "Starting Go server (embedded SPA) on http://localhost:${PORT}"
  info "Press Ctrl-C to stop."
  printf '\n'
  exec go run ./cmd/opencircuit serve
fi

# ── Hot-reload mode (default) ─────────────────────────────────────────────────
step "Starting Go API server on http://localhost:${PORT}"

# Install npm deps if node_modules is absent or stale.
if [ ! -d web/node_modules ]; then
  info "Installing npm dependencies…"
  ( cd web && npm install --silent )
  ok "npm deps installed"
fi

# Start Go server in background; capture PID for cleanup.
go run ./cmd/opencircuit serve &
GO_PID=$!
OWNED_PIDS=""   # pids THIS run bound to :$PORT — populated below once confirmed up; the EXIT trap only ever kills pids in this set (#0117)

cleanup() {
  printf '\n'
  step "Shutting down…"
  kill "$GO_PID" 2>/dev/null || true
  pkill -P "$GO_PID" 2>/dev/null || true      # the go-run child server (go run leaks it otherwise)
  release_owned_port "$PORT" "$OWNED_PIDS"    # backstop so :PORT is released — but only pids we started, never a stranger's (#0117)
  # Vite (npm run dev) is the foreground process; it handles its own SIGINT.
  ok "stopped"
}
trap cleanup EXIT INT TERM

# Give the Go server a moment to bind, then validate it's up.
sleep 1
if ! kill -0 "$GO_PID" 2>/dev/null; then
  printf '  ERROR: Go server exited unexpectedly.\n' >&2
  exit 1
fi
ok "Go API server started (pid $GO_PID)"
OWNED_PIDS="$(lsof -ti tcp:"$PORT" 2>/dev/null || true)"

step "Starting Vite dev server on http://localhost:5173"
info "Vite proxies /api /auth /account /admin → http://localhost:${PORT}"
printf '\n'
printf '\033[1m  Open: http://localhost:5173\033[0m\n'
printf '  (logs in automatically as %s)\n' "$ADMIN_EMAIL"
printf '\n'

# Run Vite in foreground — Ctrl-C naturally kills it, then EXIT trap fires.
cd web && npm run dev
