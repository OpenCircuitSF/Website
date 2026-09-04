#!/usr/bin/env bash
#
# db-reset.sh — rebuild a LOCAL database from migrations/ and seed the admin.
#
#     scripts/db-reset.sh                     # reset the dev database (opencircuit)
#     scripts/db-reset.sh opencircuit_test
#     scripts/db-reset.sh --no-seed opencircuit
#     scripts/db-reset.sh --force opencircuit_test_0055   # explicit opt-in — see GUARDS
#
# WHY THIS IS SAFE TO HAVE, AND WHEN IT STOPS BEING SAFE
#
# This project is greenfield until the first production deploy (PRD §6.2,
# CLAUDE.md §1): nothing is live but the static placeholder, no PostgreSQL
# instance on AWS holds real data, and no subscriber has ever signed up. So
# dropping and recreating a local database costs nothing, and editing a
# migration in place then resetting is the cheap, correct move.
#
# That ends at the first deploy that applies a migration to production. After
# that, migrations are append-only and this script must never be pointed at
# anything but a local scratch database.
#
# GUARDS (#0207): refuses any host that is not localhost/127.0.0.1 — that
# guard is unconditional, no override exists, because a reset script that can
# reach production is a loaded gun. Two further guards default to refusing
# and require the explicit opt-in --force to bypass, the same shape #0150
# settled on for `testdb.sh gc` (a script that assumes it's alone is what
# caused that incident):
#
#   * NAME SCOPE — this script manages exactly two databases, opencircuit and
#     opencircuit_test. Anything else starting with "opencircuit" (including
#     a per-agent scratch database like opencircuit_test_0055, which the old
#     'opencircuit*' glob also admitted) is refused without --force — that
#     name shape belongs to scripts/testdb.sh (drop/reset/gc), not here.
#   * LIVE CONNECTIONS — a bare invocation used to pg_terminate_backend EVERY
#     other connection to the target database unconditionally. Now it
#     refuses and reports who holds it unless --force is given. This matters
#     more here than it did for testdb.sh gc: 'opencircuit'/'opencircuit_test'
#     are the databases every agent falls back to sharing when it cannot get
#     its own (CLAUDE.md §5a), and 'opencircuit' is also the user's own dev
#     database — one agent's reflexive reset can destroy another agent's
#     in-flight work and whatever the user had loaded.
#
# $DB is validated against a strict identifier charset (letters, digits,
# underscore) before anything else runs, and is always double-quoted when it
# appears in a DROP/CREATE DATABASE statement — never interpolated raw.

set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PGHOST_URL="${PGHOST_URL:-postgres://opencircuit:opencircuit@localhost:5432}"

# #0250: flags are recognized in ANY position, not just before the database
# name. The old option loop broke at the first positional argument, so a
# flag placed AFTER the database name (`db-reset.sh opencircuit --force`)
# was silently dropped — bad enough on its own, and worse because the
# connection-refusal message below tells the user to run exactly that.
# Looping over every argument instead means order doesn't matter. An
# unrecognized "--..." flag is now rejected outright rather than silently
# treated as the database name. And a database-name argument that is the
# empty string is rejected too, rather than falling through to the
# "opencircuit" default via bash's "${1:-opencircuit}" — an empty string
# arriving from an unset variable is exactly how a caller would accidentally
# target the main database instead of skipping the argument.
SEED=1
FORCE=0
DB=""
DB_SET=0
for arg in "$@"; do
  case "$arg" in
    --no-seed) SEED=0 ;;
    --force)   FORCE=1 ;;
    --*)
      echo "refusing: unrecognized flag '$arg'" >&2
      exit 1
      ;;
    *)
      if [ "$DB_SET" = "1" ]; then
        echo "refusing: multiple positional arguments given ('$DB' and '$arg')" >&2
        exit 1
      fi
      DB="$arg"
      DB_SET=1
      ;;
  esac
done
if [ "$DB_SET" = "1" ] && [ -z "$DB" ]; then
  echo "refusing: empty database name argument — pass a name, or omit the argument entirely to reset the default ('opencircuit')" >&2
  exit 1
fi
[ "$DB_SET" = "1" ] || DB="opencircuit"

case "$PGHOST_URL" in
  *@localhost:*|*@127.0.0.1:*) ;;
  *) echo "refusing: PGHOST_URL is not localhost — this script only resets local databases" >&2; exit 1 ;;
esac

# Strict identifier charset first: this is what makes it safe to embed $DB in
# a DDL statement below. A Postgres identifier can't be bound as a query
# parameter the way a literal can, so a charset allowlist plus a quoted
# identifier is the standard way to make that safe. Anything outside
# [A-Za-z0-9_] is rejected outright rather than silently stripped — silently
# editing the name could point this script at a DIFFERENT database than the
# caller meant.
case "$DB" in
  *[!A-Za-z0-9_]*)
    echo "refusing: '$DB' contains a character other than letters, digits, or underscore — not a safe database identifier" >&2
    exit 1
    ;;
esac

case "$DB" in
  opencircuit|opencircuit_test) ;;
  opencircuit*)
    if [ "$FORCE" != "1" ]; then
      cat >&2 <<EOF
refusing: '$DB' is not one of the two databases this script manages (opencircuit, opencircuit_test).

It looks like a per-agent scratch database (CLAUDE.md §5a) — those belong to
scripts/testdb.sh, not this script:

    scripts/testdb.sh drop <id>     # drop your own
    scripts/testdb.sh list          # see what exists

If you really mean to reset '$DB' with this script, pass --force.
EOF
      exit 1
    fi
    ;;
  *)
    echo "refusing: '$DB' does not start with 'opencircuit'" >&2
    exit 1
    ;;
esac

DSN="$PGHOST_URL/$DB?sslmode=disable"
command -v migrate >/dev/null || { echo "error: golang-migrate not on PATH (brew install golang-migrate)" >&2; exit 1; }
# #0250 item 2: psql is used below for the live-connection check and for
# every DROP/CREATE statement — check for it the same way migrate is
# checked, rather than letting its absence surface later as a confusing
# failure (or, worse, as a false "no connections" — see item 1 below).
command -v psql >/dev/null    || { echo "error: psql not on PATH" >&2; exit 1; }

# Refuse to kick another session off '$DB' unless asked. See GUARDS above for
# why this script's blast radius made this the priority over #0150's gc fix.
#
# #0250 item 1: this must fail CLOSED, not open. The query used to be
# wrapped in `2>/dev/null || true`, so ANY psql failure — wrong socket, bad
# credentials, server down, a transient fault — produced the same empty
# string as "no connections", and the reset proceeded anyway. The one
# condition under which a destructive script most needs to stop is the one
# where it cannot tell what is happening. This check now runs unconditionally,
# even under --force: --force means "I know someone is connected, proceed
# anyway," not "proceed even if you can't tell whether anyone is connected."
#
# #0309: TWO DELIBERATELY DIFFERENT fragments below, not one shared between
# them (that was #0309's first attempt, and it was wrong — see the review on
# this issue for the measurement).
#
#   CLIENT_WHERE — what this script may TERMINATE. Scoped to client backends
#   owned by this script's OWN role ($PGHOST_URL's role, opencircuit by
#   convention). The unscoped query used to catch every backend attached to
#   '$DB' — most plausibly an autovacuum worker, which runs superuser-owned:
#   fed into the terminate SELECT below, one such row made the WHOLE
#   statement raise a SUPERUSER-only permission error, aborting the reset
#   under `set -e` (the failure #0309 tracked, correlated with load only
#   because autovacuum is likelier to attach to a freshly created, freshly
#   written database under concurrent agent activity — not a timing flake).
#   A non-superuser role cannot pg_terminate_backend a superuser-owned
#   backend at all, so this must never widen — it must never reach for
#   superuser to work around that (see GUARDS above).
#
#   HOLDER_WHERE — what COUNTS as a holder for the refusal decision below.
#   Deliberately NOT the same fragment as CLIENT_WHERE: narrowing what may be
#   terminated is safe, but narrowing what counts as a holder makes the
#   script MORE willing to proceed — the opposite of what #0207's refusal
#   exists to guarantee. pg_stat_activity NULLs backend_type for any backend
#   the querying role does not own, so `backend_type = 'client backend'`
#   alone would silently drop a real client session belonging to any OTHER
#   role — including the user's own interactive psql, which authenticates as
#   their login role, not 'opencircuit'. `usename IS NOT NULL` is the
#   distinguisher that actually holds at this privilege level: a background
#   process (autovacuum, the background writer, checkpointer, …) is never
#   logged in as anyone, so it is always excluded, while every real client
#   session — this role's or any other's — always has a usename.
#   `backend_type IS DISTINCT FROM 'parallel worker'` (not `<>`: backend_type
#   is NULL for some foreign backends, and `<>` against NULL is NULL, never
#   true) excludes parallel workers from the holder count, since their
#   leader session is itself already counted; this is cosmetic, not a safety
#   requirement. This keeps the same side benefit the narrower fragment gives
#   the terminate step: autovacuum (usename NULL) still never causes a
#   spurious refusal.
CLIENT_WHERE="datname = '$DB' and pid <> pg_backend_pid() and backend_type = 'client backend' and usename = current_user"
HOLDER_WHERE="datname = '$DB' and pid <> pg_backend_pid() and usename is not null and backend_type is distinct from 'parallel worker'"
if ! CONNS="$(psql "$PGHOST_URL/postgres" -tAc "select pid || '  ' || coalesce(usename,'?') || '  ' || coalesce(application_name,'') || '  ' || coalesce(client_addr::text,'local') from pg_stat_activity where $HOLDER_WHERE")"; then
  echo "refusing: could not determine whether '$DB' has other active connection(s) — the check above failed (see psql's error above this line), so refusing rather than assuming none. This runs even with --force." >&2
  exit 1
fi
if [ -n "$CONNS" ] && [ "$FORCE" != "1" ]; then
  cat >&2 <<EOF
refusing: '$DB' has other active connection(s) — this may be another agent (CLAUDE.md §5a) or the user's own session:

$(echo "$CONNS" | sed 's/^/  pid /')

If you're certain it's safe to disconnect them, pass --force.
EOF
  exit 1
fi

# #0309: report any background backend attached to '$DB' — one HOLDER_WHERE
# deliberately does NOT count as a holder (autovacuum launcher/worker,
# background writer, checkpointer, walsender, parallel workers, …) — purely
# as information. It is never counted above and never targeted by the
# terminate step below: this script's job is clearing CLIENT connections, and
# a background process is not an obstacle to that. Unlike the row this note
# used to also catch, a live user session from a foreign role is now a
# HOLDER (see above) and will already have refused above, or been reported in
# $CONNS above under --force — so nothing this note prints is a session the
# script could have signalled or should have counted. A failure to run this
# check is not fatal to the reset — it is a courtesy note, not a gate.
if OTHER="$(psql "$PGHOST_URL/postgres" -tAc "select pid || '  ' || coalesce(backend_type,'?') || '  ' || coalesce(usename,'?') from pg_stat_activity where datname = '$DB' and pid <> pg_backend_pid() and not ($HOLDER_WHERE)" 2>/dev/null)" && [ -n "$OTHER" ]; then
  echo "note: '$DB' also has background backend(s) attached (autovacuum, parallel workers, etc.) — not something this script can signal, and not counted as a connection above:" >&2
  echo "$OTHER" | sed 's/^/  pid /' >&2
fi

echo "resetting $DB"
psql "$PGHOST_URL/postgres" -qc "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE $CLIENT_WHERE;" >/dev/null
psql "$PGHOST_URL/postgres" -qc "DROP DATABASE IF EXISTS \"$DB\";"
psql "$PGHOST_URL/postgres" -qc "CREATE DATABASE \"$DB\";"
migrate -path "$REPO/migrations" -database "$DSN" up
echo "$DB is at migration $(psql "$DSN" -tAc 'select version from schema_migrations')"

# AWS_REGION is only presence-checked by config.Load for `seed` (it never
# constructs a mailer or makes an AWS call here) — same inertness dev.sh's
# equivalent documents at length. Left at the real SES region for consistency
# rather than the true-until-2026-09-03 us-west-2 planning value: us-east-1,
# corrected 2026-09-04 (#0421; dev.sh's own default was corrected 2026-09-03,
# #0418, and the two had since diverged).
if [ "$SEED" = "1" ]; then
  echo "seeding admin"
  ( cd "$REPO" && DATABASE_URL="$DSN" \
      BASE_URL="${BASE_URL:-http://localhost:8080}" \
      WEBAUTHN_RP_ID="${WEBAUTHN_RP_ID:-localhost}" \
      WEBAUTHN_RP_ORIGIN="${WEBAUTHN_RP_ORIGIN:-http://localhost:8080}" \
      SESSION_SECRET="${SESSION_SECRET:-dev-session-secret-not-for-production}" \
      ADMIN_EMAIL="${ADMIN_EMAIL:-admin@localhost}" \
      AWS_REGION="${AWS_REGION:-us-east-1}" \
      EMAIL_FROM="${EMAIL_FROM:-Open Circuit SF <dev@localhost>}" \
      EMAIL_LIST_DOMAIN="${EMAIL_LIST_DOMAIN:-lists.localhost}" \
      go run ./cmd/opencircuit seed )
fi

if [ "$DB" = "opencircuit_test" ]; then
  echo "note: the per-agent template is separate — rebuild it with scripts/testdb.sh template"
fi
