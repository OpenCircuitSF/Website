#!/usr/bin/env bash
#
# db-reset.sh — rebuild a LOCAL database from migrations/ and seed the admin.
#
#     scripts/db-reset.sh                # reset the dev database (opencircuit)
#     scripts/db-reset.sh opencircuit_test
#     scripts/db-reset.sh --no-seed opencircuit
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
# GUARDS: refuses any host that is not localhost/127.0.0.1, and refuses a
# database whose name does not start with 'opencircuit'. Both are deliberate —
# a reset script that can reach production is a loaded gun.

set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PGHOST_URL="${PGHOST_URL:-postgres://opencircuit:opencircuit@localhost:5432}"

SEED=1
[ "${1:-}" = "--no-seed" ] && { SEED=0; shift; }
DB="${1:-opencircuit}"

case "$PGHOST_URL" in
  *@localhost:*|*@127.0.0.1:*) ;;
  *) echo "refusing: PGHOST_URL is not localhost — this script only resets local databases" >&2; exit 1 ;;
esac
case "$DB" in
  opencircuit*) ;;
  *) echo "refusing: '$DB' does not start with 'opencircuit'" >&2; exit 1 ;;
esac

DSN="$PGHOST_URL/$DB?sslmode=disable"
command -v migrate >/dev/null || { echo "error: golang-migrate not on PATH (brew install golang-migrate)" >&2; exit 1; }

echo "resetting $DB"
psql "$PGHOST_URL/postgres" -qc "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname='$DB' AND pid <> pg_backend_pid();" >/dev/null
psql "$PGHOST_URL/postgres" -qc "DROP DATABASE IF EXISTS $DB;"
psql "$PGHOST_URL/postgres" -qc "CREATE DATABASE $DB;"
migrate -path "$REPO/migrations" -database "$DSN" up
echo "$DB is at migration $(psql "$DSN" -tAc 'select version from schema_migrations')"

if [ "$SEED" = "1" ]; then
  echo "seeding admin"
  ( cd "$REPO" && DATABASE_URL="$DSN" \
      BASE_URL="${BASE_URL:-http://localhost:8080}" \
      WEBAUTHN_RP_ID="${WEBAUTHN_RP_ID:-localhost}" \
      WEBAUTHN_RP_ORIGIN="${WEBAUTHN_RP_ORIGIN:-http://localhost:8080}" \
      SESSION_SECRET="${SESSION_SECRET:-dev-session-secret-not-for-production}" \
      ADMIN_EMAIL="${ADMIN_EMAIL:-admin@localhost}" \
      AWS_REGION="${AWS_REGION:-us-west-2}" \
      EMAIL_FROM="${EMAIL_FROM:-Open Circuit SF <dev@localhost>}" \
      EMAIL_LIST_DOMAIN="${EMAIL_LIST_DOMAIN:-lists.localhost}" \
      go run ./cmd/opencircuit seed )
fi

if [ "$DB" = "opencircuit_test" ]; then
  echo "note: the per-agent template is separate — rebuild it with scripts/testdb.sh template"
fi
