#!/usr/bin/env bash
#
# restore.sh — restore a PostgreSQL backup produced by backup.sh into a target
# database. Handles both formats: custom (.dump → pg_restore) and plain
# (.sql.gz → gunzip | psql).
#
#     # restore the newest shortlinks dump into a scratch DB (a "restore drill")
#     sudo bash scripts/db/restore.sh /var/backups/postgres/shortlinks/shortlinks-latest.dump shortlinks_restore_test
#
#     # restore a specific dump over the live database (DANGEROUS — see below)
#     sudo bash scripts/db/restore.sh /var/backups/postgres/shortlinks/shortlinks-20260625-073001Z.dump shortlinks
#
# Arguments:
#   $1  path to the dump file (.dump or .sql.gz)
#   $2  target database name
#
# Configuration:
#   BACKUP_RUN_AS   OS user to run psql/pg_restore as (default: postgres).
#                   Set empty / to the current user to skip sudo (local dev).
#   RESTORE_CREATE  If "1", create the target database first (must not already
#                   exist). Default: 0 (assume it exists / restore into it).
#   RESTORE_OWNER   Role to own every restored table, sequence, and view.
#                   Default: opencircuit (the app role from scripts/db/create.sql).
#                   Set empty to skip this step and leave ownership as pg_restore
#                   / psql left it.
#
# #0228: both dump formats strip ownership and grants (`pg_dump
# --no-owner --no-privileges` in backup.sh), so after a restore every object is
# owned by whichever role ran the restore — on the documented production path
# that is `postgres` (via `sudo -u postgres`), not the application role. Left
# alone, the app's first query after a "successful" restore gets `permission
# denied`. This script closes that gap itself: after the restore completes, it
# walks every table/sequence/view in every non-system schema and reassigns
# ownership to RESTORE_OWNER, which — because ownership implies full
# privileges on an object — also replaces what --no-privileges stripped. This
# step needs to run as a role that can reassign ownership to RESTORE_OWNER
# (a superuser can always do this; BACKUP_RUN_AS=postgres satisfies it on the
# documented production path).
#
# Safety: this script never drops databases. For a clean-slate restore, create a
# fresh empty target (RESTORE_CREATE=1) or drop/recreate it yourself first.
# Restoring over a live database is destructive at the object level — only do it
# when you mean to.

set -euo pipefail

DUMP="${1:?usage: restore.sh <dump-file> <target-db>}"
TARGET="${2:?usage: restore.sh <dump-file> <target-db>}"
#0062: `-` (unset-only), not `:-` (unset-or-empty) — see backup.sh's matching
# comment. BACKUP_RUN_AS="" must actually skip sudo, not silently fall back
# to "postgres".
BACKUP_RUN_AS="${BACKUP_RUN_AS-postgres}"
RESTORE_CREATE="${RESTORE_CREATE:-0}"
#0228: unset-only, same reasoning as BACKUP_RUN_AS above — RESTORE_OWNER=""
# must actually skip the ownership fix, not silently fall back to the default.
RESTORE_OWNER="${RESTORE_OWNER-opencircuit}"

[ -f "$DUMP" ] || { echo "ERROR: dump file not found: $DUMP" >&2; exit 2; }

runner=()
if [ -n "$BACKUP_RUN_AS" ] && [ "$BACKUP_RUN_AS" != "$(id -un)" ]; then
  runner=(sudo -u "$BACKUP_RUN_AS")
fi

cd /tmp

# reassign_ownership TARGET OWNER — ALTER every table/sequence/view in every
# non-system schema of TARGET to OWNER. No-op if OWNER is empty. OWNER is
# restricted to a plain identifier (validated below) before being spliced
# into the generated SQL text below, and quote_ident() double-protects it at
# the SQL level too.
#
# Note: psql's `:'var'` interpolation does NOT reach inside a $$-quoted
# string (verified empirically against psql 16.14 while writing this — a
# `DO $$ ... :'owner' ... $$` body raises "syntax error at or near :" because
# psql's lexer treats the dollar-quoted body as an opaque string it does not
# scan for `:name` tokens), so OWNER is substituted by bash into the heredoc
# text instead, via an *unquoted* heredoc — hence the `\$\$` below, which must
# stay backslash-escaped or bash will read it as this shell's PID.
reassign_ownership() {
  local target="$1" owner="$2"
  if [ -z "$owner" ]; then
    echo "RESTORE_OWNER is empty — skipping post-restore ownership reassignment."
    return 0
  fi
  case "$owner" in
    *[!a-zA-Z0-9_]*)
      echo "ERROR: RESTORE_OWNER='$owner' is not a plain identifier (letters, digits, underscore only)" >&2
      exit 2
      ;;
  esac
  echo "Reassigning ownership of every table/sequence/view in '$target' to '$owner' ..."
  ${runner[@]+"${runner[@]}"} psql -v ON_ERROR_STOP=1 -d "$target" <<SQL
DO \$\$
DECLARE
  r record;
BEGIN
  FOR r IN SELECT schemaname, tablename FROM pg_tables
             WHERE schemaname NOT IN ('pg_catalog', 'information_schema') LOOP
    EXECUTE format('ALTER TABLE %I.%I OWNER TO ', r.schemaname, r.tablename) || quote_ident('$owner');
  END LOOP;
  FOR r IN SELECT schemaname, sequencename FROM pg_sequences
             WHERE schemaname NOT IN ('pg_catalog', 'information_schema') LOOP
    EXECUTE format('ALTER SEQUENCE %I.%I OWNER TO ', r.schemaname, r.sequencename) || quote_ident('$owner');
  END LOOP;
  FOR r IN SELECT schemaname, viewname FROM pg_views
             WHERE schemaname NOT IN ('pg_catalog', 'information_schema') LOOP
    EXECUTE format('ALTER VIEW %I.%I OWNER TO ', r.schemaname, r.viewname) || quote_ident('$owner');
  END LOOP;
END
\$\$;
SQL
  echo "Ownership reassigned to '$owner'."
}

echo "Restoring '$DUMP' → database '$TARGET' (as ${BACKUP_RUN_AS:-$(id -un)})"

# ${runner[@]+...} guards an empty array against `set -u` on bash 3.2 (macOS).
if [ "$RESTORE_CREATE" = "1" ]; then
  echo "  creating database '$TARGET'"
  ${runner[@]+"${runner[@]}"} createdb "$TARGET"
fi

case "$DUMP" in
  *.sql.gz)
    gunzip -c "$DUMP" | ${runner[@]+"${runner[@]}"} psql -v ON_ERROR_STOP=1 -d "$TARGET"
    ;;
  *.dump|*)
    # Custom-format archive. --clean --if-exists makes re-restores idempotent.
    ${runner[@]+"${runner[@]}"} pg_restore --no-owner --no-privileges --clean --if-exists \
      -d "$TARGET" "$DUMP"
    ;;
esac

reassign_ownership "$TARGET" "$RESTORE_OWNER"

echo "Done. Verify with: psql -d $TARGET -c '\\dt'"
