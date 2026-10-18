#!/usr/bin/env bash
#
# dev-outreach-demo.sh — one command to run every outreach screen end to end
# against local mocks of Bluesky, Mastodon and ShortLinks (#0580).
#
#   ./scripts/dev-outreach-demo.sh
#
# What it does, in order:
#   1. builds the SPA and two binaries (the app, cmd/devmocks) into .scratch/demo/bin
#   2. rebuilds and migrates a dedicated local database, opencircuit_demo (never
#      your `opencircuit` dev database), and seeds the admin row
#   3. starts the mocks on 127.0.0.1:$DEMO_MOCK_PORT (default 18081)
#   4. seeds the fictional "LED Programming" workshop, four outreach locations
#      and the saved connection values that point Settings at the mocks
#   5. starts the app on 127.0.0.1:$DEMO_PORT (default 18080) with
#      DEV_ADMIN_LOGIN=true and MAILER_NOOP=true — BASE_URL stays
#      http://localhost:$DEMO_PORT, so the DEV_ADMIN_LOGIN loopback guard is
#      untouched
#   6. prints the URLs and blocks; Ctrl-C (or SIGTERM) kills exactly the pids it
#      started
#
# Environment (all optional):
#   DEMO_PORT=18080        app port        DEMO_MOCK_PORT=18081   mocks port
#   DEMO_DB=opencircuit_demo               database name (must start opencircuit)
#   DEMO_KEEP_DB=1         keep the demo database from the last run (default: rebuilt)
#   DEMO_SKIP_BUILD=1      reuse the binaries already in .scratch/demo/bin
#   DEMO_VM_SSH=admin@IP   also open `ssh -N -R` to that Tart guest so its Safari
#                          can browse http://localhost:$DEMO_PORT (key auth only;
#                          see docs/dev.md for the manual form)
#
# Mock credentials: any token/app password works except "bad-token", which
# every mock rejects with 401 (put it in Settings to see a failed Test connection).
#
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO"

PORT="${DEMO_PORT:-18080}"
MOCK_PORT="${DEMO_MOCK_PORT:-18081}"
DB="${DEMO_DB:-opencircuit_demo}"
PGHOST_URL="postgres://opencircuit:opencircuit@localhost:5432"
DSN="$PGHOST_URL/$DB?sslmode=disable"
WORK="$REPO/.scratch/demo"
BIN="$WORK/bin"
MOCK_URL="http://127.0.0.1:$MOCK_PORT"

case "$DB" in
  opencircuit*) ;;
  *) echo "refusing: DEMO_DB must start with 'opencircuit'" >&2; exit 1 ;;
esac
case "$DB" in
  *[!A-Za-z0-9_]*) echo "refusing: DEMO_DB may contain only letters, digits and underscore" >&2; exit 1 ;;
esac
case "$DB" in
  opencircuit|opencircuit_test|opencircuit_live)
    echo "refusing: DEMO_DB=$DB is a database other work relies on; use the default opencircuit_demo" >&2; exit 1 ;;
  opencircuit_test_*)
    echo "refusing: DEMO_DB=$DB is in scripts/testdb.sh's per-agent pool; use the default opencircuit_demo" >&2; exit 1 ;;
esac
for c in go npm psql migrate curl; do
  command -v "$c" >/dev/null 2>&1 || { echo "error: $c not found on PATH" >&2; exit 1; }
done
for p in "$PORT" "$MOCK_PORT"; do
  if lsof -nP -iTCP:"$p" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "error: port $p is already in use (lsof -nP -iTCP:$p); pick another with DEMO_PORT / DEMO_MOCK_PORT" >&2
    exit 1
  fi
done

PIDS=()
cleanup() {
  trap - EXIT INT TERM
  for pid in ${PIDS[@]+"${PIDS[@]}"}; do
    kill "$pid" 2>/dev/null || true
  done
  for pid in ${PIDS[@]+"${PIDS[@]}"}; do
    wait "$pid" 2>/dev/null || true
  done
  echo "demo stopped"
}
trap cleanup EXIT INT TERM

mkdir -p "$BIN" "$WORK/media"

if [ "${DEMO_SKIP_BUILD:-0}" != "1" ]; then
  echo "==> building SPA and binaries"
  # The embedded SPA comes from web/dist, whose index.html is a tracked
  # placeholder that `npm run build` overwrites (AGENTS.md §8b). Put it back
  # once the binary has embedded the built copy, unless it was already dirty.
  dist_clean=0
  git diff --quiet -- web/dist/index.html 2>/dev/null && dist_clean=1
  ( cd web && { [ -d node_modules ] || npm install --silent; } && npm run build >"$WORK/web-build.log" 2>&1 ) \
    || { echo "SPA build failed; see $WORK/web-build.log" >&2; exit 1; }
  go build -o "$BIN/opencircuit" ./cmd/opencircuit
  go build -o "$BIN/devmocks" ./cmd/devmocks
  if [ "$dist_clean" = "1" ]; then
    git show HEAD:web/dist/index.html >web/dist/index.html
  fi
fi
[ -x "$BIN/opencircuit" ] && [ -x "$BIN/devmocks" ] || { echo "error: binaries missing in $BIN (unset DEMO_SKIP_BUILD)" >&2; exit 1; }

echo "==> database $DB"
# The mocks keep their state in memory, so a database left from an earlier run
# would reference links and posts the new mocks no longer know. The demo
# database is therefore rebuilt every run unless DEMO_KEEP_DB=1.
exists="$(psql "$PGHOST_URL/postgres" -tAc "select 1 from pg_database where datname='$DB'")"
if [ "$exists" = "1" ] && [ "${DEMO_KEEP_DB:-0}" != "1" ]; then
  psql "$PGHOST_URL/postgres" -qc "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '$DB' AND pid <> pg_backend_pid();" >/dev/null
  psql "$PGHOST_URL/postgres" -qc "DROP DATABASE \"$DB\";"
  exists=""
fi
[ "$exists" = "1" ] || psql "$PGHOST_URL/postgres" -qc "CREATE DATABASE \"$DB\";"
migrate -path "$REPO/migrations" -database "$DSN" up

export DATABASE_URL="$DSN"
export BASE_URL="http://localhost:$PORT"
export PORT
export WEBAUTHN_RP_ID=localhost
export WEBAUTHN_RP_ORIGIN="http://localhost:$PORT"
export SESSION_SECRET="dev-session-secret-not-for-production"
export ADMIN_EMAIL="${ADMIN_EMAIL:-admin@localhost}"
export AWS_REGION=us-east-1
export EMAIL_FROM="Open Circuit SF <dev@localhost>"
export EMAIL_LIST_DOMAIN=lists.localhost
export DEV_ADMIN_LOGIN=true
export MAILER_NOOP=true
export OUTREACH_TRACKER_INTERVAL="${OUTREACH_TRACKER_INTERVAL:-20s}"   # engagement shows within seconds
export MEDIA_DIR="$WORK/media"
unset STORAGE SHORTLINKS_BASE_URL SHORTLINKS_API_TOKEN BLUESKY_HANDLE BLUESKY_APP_PASSWORD BLUESKY_PDS_URL MASTODON_BASE_URL MASTODON_TOKEN

"$BIN/opencircuit" seed

echo "==> mocks on $MOCK_URL"
"$BIN/devmocks" serve -addr "127.0.0.1:$MOCK_PORT" >"$WORK/mocks.log" 2>&1 &
PIDS+=("$!")
for _ in $(seq 1 50); do
  curl -fs "$MOCK_URL/healthz" >/dev/null 2>&1 && break
  kill -0 "${PIDS[0]}" 2>/dev/null || { echo "mocks exited; see $WORK/mocks.log" >&2; exit 1; }
  sleep 0.2
done
curl -fs "$MOCK_URL/healthz" >/dev/null || { echo "mocks did not come up; see $WORK/mocks.log" >&2; exit 1; }

"$BIN/devmocks" seed -mock-url "$MOCK_URL" -media-dir "$MEDIA_DIR"

echo "==> app on http://localhost:$PORT"
"$BIN/opencircuit" serve >"$WORK/app.log" 2>&1 &
APP_PID=$!
PIDS+=("$APP_PID")
for _ in $(seq 1 100); do
  curl -fs -o /dev/null "http://localhost:$PORT/api/interests" 2>/dev/null && break
  kill -0 "$APP_PID" 2>/dev/null || { echo "app exited; see $WORK/app.log" >&2; exit 1; }
  sleep 0.3
done

if [ -n "${DEMO_VM_SSH:-}" ]; then
  echo "==> reverse tunnel to $DEMO_VM_SSH (guest localhost:$PORT -> host localhost:$PORT)"
  ssh -N -o BatchMode=yes -o ExitOnForwardFailure=yes -o StrictHostKeyChecking=accept-new \
    -R "127.0.0.1:$PORT:127.0.0.1:$PORT" "$DEMO_VM_SSH" &
  PIDS+=("$!")
fi

cat <<INFO

DEMO READY
  Admin (host):       http://localhost:$PORT/admin#/outreach   (signed in automatically as admin@localhost)
  Settings:           http://localhost:$PORT/admin#/settings   (Outreach connections)
  Workshops:          http://localhost:$PORT/admin#/workshops  (LED Programming is published)
  Mocks:              $MOCK_URL   (/bluesky /mastodon /shortlinks; token "bad-token" is rejected with 401)
  Logs:               $WORK/app.log  $WORK/mocks.log
  Tart guest Safari:  http://localhost:$PORT  after: ssh -N -R 127.0.0.1:$PORT:127.0.0.1:$PORT <guest-user>@\$(tart ip <vm>)
                      (or start this script with DEMO_VM_SSH=<guest-user>@<guest-ip>)
Ctrl-C stops everything.
INFO

wait "$APP_PID"
