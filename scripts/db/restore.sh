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
# walks every table/sequence/view/materialized view/function/procedure in
# every non-system schema and reassigns ownership to RESTORE_OWNER, which —
# because ownership implies full privileges on an object — also replaces what
# --no-privileges stripped. This step needs to run as a role that can
# reassign ownership to RESTORE_OWNER (a superuser can always do this;
# BACKUP_RUN_AS=postgres satisfies it on the documented production path).
#
# #0235: object ownership alone is not enough — `#0228` fixed every table,
# sequence, and view, but left the `public` **schema** itself owned by
# `pg_database_owner` (PostgreSQL 15+'s default: a pseudo-role that resolves
# to whoever owns the *database*, i.e. the restoring superuser, not the app
# role). SELECT/INSERT on existing tables still worked, so a restore looked
# fine and traffic kept serving — but CREATE TABLE, and therefore
# `migrate up`, failed with "permission denied for schema public" at the
# next deploy, when nobody was thinking about the restore any more. This
# script now also reassigns every non-system schema itself, not just the
# objects inside it.
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
# must actually skip the ownership fix (schema included, #0235), not
# silently fall back to the default.
RESTORE_OWNER="${RESTORE_OWNER-opencircuit}"

# #0235: validate RESTORE_OWNER *before* the restore runs, not after — a
# malformed value used to be caught only inside reassign_ownership, which
# runs last, so a typo cost a full restore before the script exited 2. An
# empty value is a deliberate, valid opt-out (see reassign_ownership below)
# and is not an error.
validate_owner() {
  local owner="$1"
  [ -z "$owner" ] && return 0
  case "$owner" in
    *[!a-zA-Z0-9_]*)
      echo "ERROR: RESTORE_OWNER='$owner' is not a plain identifier (letters, digits, underscore only)" >&2
      exit 2
      ;;
  esac
}
validate_owner "$RESTORE_OWNER"

[ -f "$DUMP" ] || { echo "ERROR: dump file not found: $DUMP" >&2; exit 2; }

runner=()
if [ -n "$BACKUP_RUN_AS" ] && [ "$BACKUP_RUN_AS" != "$(id -un)" ]; then
  runner=(sudo -u "$BACKUP_RUN_AS")
fi

cd /tmp

# reassign_ownership TARGET OWNER — ALTER every non-system schema (#0235) and
# every table/sequence/view/materialized view/function/procedure within one
# in TARGET to OWNER. No-op if OWNER is empty. OWNER was already validated as
# a plain identifier by validate_owner before the restore ran; quote_ident()
# double-protects it at the SQL level too, since this is still spliced into
# generated SQL text (see the psql interpolation note below).
#
# Materialized views, standalone composite types / enums / domains, and
# functions/procedures are covered (#0235); table/view/matview row types are
# skipped deliberately (their ownership follows the table/view/matview
# itself — ALTER TYPE on them is unsupported and unnecessary), as are array
# types (Postgres auto-creates one per named type; not independently
# ownable) and aggregates (`prokind = 'a'`; would need `ALTER AGGREGATE`'s
# distinct signature syntax rather than `ALTER FUNCTION`/`ALTER PROCEDURE`).
# None of the last three exist in migrations/ today (verified:
# `grep -rniE 'CREATE (OR REPLACE )?(TYPE|AGGREGATE)' migrations/` finds
# nothing), so this is future-proofing rather than closing an active gap —
# left unhandled and documented here rather than either silently omitted or
# built out for something that cannot occur yet.
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
    echo "RESTORE_OWNER is empty — skipping post-restore ownership reassignment (schema included)."
    return 0
  fi
  echo "Reassigning ownership of schema(s) and every table/sequence/view/matview/function in '$target' to '$owner' ..."
  ${runner[@]+"${runner[@]}"} psql -v ON_ERROR_STOP=1 -d "$target" <<SQL
DO \$\$
DECLARE
  r record;
BEGIN
  -- #0235: the schema itself, not just the objects inside it -- this is
  -- what CREATE TABLE (and therefore a real migration run) actually checks.
  FOR r IN SELECT nspname FROM pg_namespace
             WHERE nspname NOT IN ('pg_catalog', 'information_schema')
               AND nspname NOT LIKE 'pg\_%' LOOP
    EXECUTE format('ALTER SCHEMA %I OWNER TO ', r.nspname) || quote_ident('$owner');
  END LOOP;
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
  -- #0235: materialized views — unreachable today (no migration creates
  -- one), covered anyway since the query is no more complex than the view
  -- loop above.
  FOR r IN SELECT schemaname, matviewname FROM pg_matviews
             WHERE schemaname NOT IN ('pg_catalog', 'information_schema') LOOP
    EXECUTE format('ALTER MATERIALIZED VIEW %I.%I OWNER TO ', r.schemaname, r.matviewname) || quote_ident('$owner');
  END LOOP;
  -- #0235: standalone types — enums ('e'), domains ('d'), and composite
  -- types declared with CREATE TYPE ... AS (...) ('c'). Excludes the
  -- implicit row type every table/view/matview already has (that ownership
  -- follows the relation itself, altered above) and the array type
  -- Postgres auto-creates per named type (typname prefixed with '_',
  -- not independently ownable).
  FOR r IN SELECT n.nspname AS schemaname, t.typname AS typename
             FROM pg_type t
             JOIN pg_namespace n ON n.oid = t.typnamespace
             WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
               AND t.typtype IN ('e', 'd', 'c')
               AND t.typname NOT LIKE '\_%'
               AND NOT EXISTS (
                 SELECT 1 FROM pg_class c
                  WHERE c.oid = t.typrelid AND c.relkind IN ('r', 'v', 'm', 'p', 'f')
               ) LOOP
    EXECUTE format('ALTER TYPE %I.%I OWNER TO ', r.schemaname, r.typename) || quote_ident('$owner');
  END LOOP;
  -- #0235: functions and procedures, keyed by identity signature since
  -- overloads share a name. Excludes aggregates (prokind = 'a') — see the
  -- function-level comment above.
  FOR r IN SELECT n.nspname AS schemaname, p.proname AS procname,
                  pg_get_function_identity_arguments(p.oid) AS args,
                  p.prokind AS kind
             FROM pg_proc p
             JOIN pg_namespace n ON n.oid = p.pronamespace
             WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
               AND p.prokind IN ('f', 'p', 'w') LOOP
    IF r.kind = 'p' THEN
      EXECUTE format('ALTER PROCEDURE %I.%I(%s) OWNER TO ', r.schemaname, r.procname, r.args) || quote_ident('$owner');
    ELSE
      EXECUTE format('ALTER FUNCTION %I.%I(%s) OWNER TO ', r.schemaname, r.procname, r.args) || quote_ident('$owner');
    END IF;
  END LOOP;
END
\$\$;
SQL
  echo "Ownership reassigned to '$owner' (schema and objects)."
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
