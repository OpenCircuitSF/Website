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
#   ./scripts/dev.sh           # hot-reload: Go API on :$PORT + Vite dev server on :5173
#   ./scripts/dev.sh --built   # built-SPA: npm build + go run serving on :$PORT only
#
# Open in browser:
#   hot-reload mode:  http://localhost:5173  (Vite proxies /api → :$PORT)
#   built mode:       http://localhost:$PORT
#
# Override any env var before calling, e.g.:
#   ADMIN_EMAIL=me@example.com ./scripts/dev.sh
#   PORT=9090 ./scripts/dev.sh   # threaded into BASE_URL, WEBAUTHN_RP_ORIGIN,
#                                 # and Vite's proxy targets (#0213) — the whole
#                                 # stack moves, not just the Go server
#
# $PORT defaults to 8080 and :5173 is Vite's own fixed port (not
# configurable — it is spelled the same way in web/vite.config.ts, README.md,
# and docs/dev.md, and changing it here alone would desync those).
#
# If :$PORT or :5173 is already held by another process, dev.sh refuses to
# start rather than killing it (#0117) — it may be another agent's server, or
# the user's own editor preview (CLAUDE.md §8b). To reclaim a port you are
# sure is your own stale dev.sh, e.g. re-running after a terminal was closed
# without Ctrl-C:
#   RECLAIM_PORTS=1 ./scripts/dev.sh
#
# dev.sh waits for its OWN Go server to bind :$PORT before continuing, and
# gives up with an error if that never happens. On a very cold build cache
# raise the ceiling:
#   DEV_READY_TIMEOUT=600 ./scripts/dev.sh
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
# PORT must be set before BASE_URL/WEBAUTHN_RP_ORIGIN so both can default off
# of it (#0213 — they used to hardcode :8080 regardless of $PORT). It is also
# exported before `npm run dev` runs so web/vite.config.ts can read it via
# process.env at config-evaluation time — Vite has no other way to see a
# shell variable, since the config file is evaluated once, before any request
# arrives.
export PORT="${PORT:-8080}"
export BASE_URL="${BASE_URL:-http://localhost:${PORT}}"
export WEBAUTHN_RP_ID="${WEBAUTHN_RP_ID:-localhost}"
# In --built mode the front end and API share one origin (:$PORT), so this
# value is exactly what a browser would send. In hot-reload mode the browser's
# real origin is :5173 (Vite), not :$PORT — but that mismatch is inert here:
# internal/middleware.DevAutoLogin bypasses WebAuthn entirely under
# STORAGE=json (see the header comment), so no real ceremony ever checks this
# value against a request Origin. It is threaded to $PORT anyway so --built
# mode is correct and so the value stays honest about which port dev.sh
# actually started, rather than silently naming a fixed 8080 (CLAUDE.md §7).
export WEBAUTHN_RP_ORIGIN="${WEBAUTHN_RP_ORIGIN:-http://localhost:${PORT}}"
export SESSION_SECRET="${SESSION_SECRET:-dev-session-secret-not-for-production}"
export ADMIN_EMAIL="${ADMIN_EMAIL:-admin@localhost}"
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
  -h|--help) sed -n '2,40p' "$0"; exit 0 ;;
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

# ── Process ownership (#0117) ────────────────────────────────────────────────
# Ownership is derived from the fork, never from who happens to be on a port.
# `lsof -ti tcp:P` matches ANY TCP endpoint on P — a left-open browser tab, a
# stray curl, a health-check poller with an ESTABLISHED connection to our own
# server all answer it — and none of that is evidence about who STARTED the
# process. Inferring ownership from the port made dev.sh kill a stranger twice
# (#0117, both review passes). A pid is ours only if it is $GO_PID or a
# descendant of it.
#
# Every port lookup in this script is scoped to listeners (`-sTCP:LISTEN`), so
# a mere client connection can neither be enrolled as owned nor block startup.
port_listeners() { lsof -ti tcp:"$1" -sTCP:LISTEN 2>/dev/null || true; }

# Pids are recorded with their start time, so a pid the OS recycled during a
# long session can never be mistaken for the process we forked. The trailing
# `|| true` matters: `set -o pipefail` is on, so without it `ps` failing on a
# dead pid would make `x="$(proc_start …)"` a failing command and errexit
# would kill the script mid-shutdown.
proc_start() { ps -o lstart= -p "$1" 2>/dev/null | tr -s ' ' '_' | tr -d '\n' || true; }

descendant_pids() {  # <root-pid> — the root plus every live descendant, one per line
  ps -Ao pid=,ppid= | awk -v root="$1" '
    { p[NR] = $1; pp[NR] = $2; n = NR }
    END {
      own[root] = 1
      for (k = 0; k < n; k++) {              # ps output is not topological — iterate to a fixed point
        changed = 0
        for (i = 1; i <= n; i++) if (!own[p[i]] && own[pp[i]]) { own[p[i]] = 1; changed = 1 }
        if (!changed) break
      }
      for (i = 1; i <= n; i++) if (own[p[i]]) print p[i]
    }'
}

# own_pids <root-pid> — which pids does THIS run own? The single line in its
# body IS the ownership model; scripts/dev_guard_test.sh mutates exactly that
# line back to the discredited "whoever is on the port" answer to prove its
# assertions are sensitive to the #0117 regression rather than vacuous.
own_pids() {
  descendant_pids "$1"
}

own_fingerprints() {  # <root-pid> — "pid:start-time" for every owned pid
  local p s
  for p in $(own_pids "$1"); do
    s="$(proc_start "$p")"
    if [ -n "$s" ]; then printf '%s:%s\n' "$p" "$s"; fi
  done
  return 0
}

is_owned() {  # <pid> <fingerprint-list> — true only for an exact pid+start-time match
  local s
  s="$(proc_start "$1")"
  [ -n "$s" ] || return 1
  printf '%s\n' "$2" | grep -qxF "$1:$s"
}

own_holds_port() {  # <port> <root-pid> — true when a pid WE forked is LISTENing there; records the owned set
  local p="$1" root="$2" c
  OWNED_PIDS="$(own_fingerprints "$root")"
  for c in $(port_listeners "$p"); do
    if is_owned "$c" "$OWNED_PIDS"; then return 0; fi
  done
  return 1
}

# Free the dev ports if a previous run left a server bound. `go run` leaks its
# compiled child process when its parent is killed, so a stale server can keep
# holding :$PORT — which both blocks startup ("address already in use") AND
# keeps serving an OLD build.
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
  pids="$(port_listeners "$p")"
  [ -z "$pids" ] && return 0

  if [ "$force" = "1" ] || [ "$RECLAIM_PORTS" = "1" ]; then
    info "freeing port $p (stale process: $(printf '%s' "$pids" | tr '\n' ' '))"
    # shellcheck disable=SC2086
    kill -9 $pids 2>/dev/null || true
    sleep 1
    still="$(port_listeners "$p")"
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
# trap kills only pids THIS run FORKED — $GO_PID and its descendants, recorded
# as pid+start-time fingerprints — and warns, without killing, about anything
# else it finds holding the port at exit.
release_owned_port() {  # <port> <owned-fingerprints>
  local p="$1" owned="$2" current c ours="" foreign="" pid cmd remaining
  current="$(port_listeners "$p")"
  [ -z "$current" ] && return 0

  for c in $current; do
    if is_owned "$c" "$owned"; then
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
GO_FP="$GO_PID:$(proc_start "$GO_PID")"   # pins the fork itself, so a recycled $GO_PID is never mistaken for it (#0117)
OWNED_PIDS=""   # "pid:start-time" for every pid THIS run forked; the EXIT trap kills nothing outside this set (#0117)

cleanup() {
  local live=""
  printf '\n'
  step "Shutting down…"
  # Gather the lineage evidence BEFORE signalling anything: once $GO_PID is
  # killed, a leaked grandchild is reparented to launchd and the tree can no
  # longer be walked. Skip the whole thing if $GO_PID is no longer the process
  # we forked — `pkill -P` against a recycled pid would kill another program's
  # children, which is this issue's defect wearing a different hat.
  if [ "$GO_PID:$(proc_start "$GO_PID")" = "$GO_FP" ]; then
    live="$(own_fingerprints "$GO_PID")"
    kill "$GO_PID" 2>/dev/null || true
    pkill -P "$GO_PID" 2>/dev/null || true    # the go-run child server (go run leaks it otherwise)
  fi
  OWNED_PIDS="$(printf '%s\n%s\n' "$OWNED_PIDS" "$live")"
  release_owned_port "$PORT" "$OWNED_PIDS"    # backstop so :PORT is released — but only pids we forked, never a stranger's (#0117)
  # Vite (npm run dev) is the foreground process; it handles its own SIGINT.
  ok "stopped"
}
trap cleanup EXIT INT TERM

# Wait for OUR OWN process to actually bind :$PORT. The old check —
# `sleep 1` then `kill -0 "$GO_PID"` — only said that `go run` had not exited
# yet; it said nothing about who held the port. On any build slower than that
# one second (a recompile, a cold cache) it passed while nothing of ours was
# bound, and whatever `lsof` then reported got recorded as ours and killed at
# exit (#0117 second review, repro B). Poll for a LISTENer that is a
# descendant of $GO_PID instead, and fail loudly if that never happens.
DEV_READY_TIMEOUT="${DEV_READY_TIMEOUT:-180}"
ready_waited=0
until own_holds_port "$PORT" "$GO_PID"; do
  if ! kill -0 "$GO_PID" 2>/dev/null; then
    printf '  ERROR: Go server exited unexpectedly.\n' >&2
    exit 1
  fi
  ready_waited=$((ready_waited + 1))
  if [ "$ready_waited" -ge "$DEV_READY_TIMEOUT" ]; then
    printf '  ERROR: Go server did not bind 127.0.0.1:%s within %ss — giving up.\n' "$PORT" "$DEV_READY_TIMEOUT" >&2
    printf '    Either something else holds the port, or the build is slower than the timeout (raise DEV_READY_TIMEOUT).\n' >&2
    exit 1
  fi
  sleep 1
done
ok "Go API server started (pid $GO_PID)"

step "Starting Vite dev server on http://localhost:5173"
info "Vite proxies /api /auth /account /admin → http://localhost:${PORT}"
printf '\n'
printf '\033[1m  Open: http://localhost:5173\033[0m\n'
printf '  (logs in automatically as %s)\n' "$ADMIN_EMAIL"
printf '\n'

# Run Vite in foreground — Ctrl-C naturally kills it, then EXIT trap fires.
cd web && npm run dev
