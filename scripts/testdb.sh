#!/usr/bin/env bash
#
# testdb.sh — per-agent PostgreSQL test databases, cloned from a template.
#
# WHY THIS EXISTS
#
# internal/testdb.Lock() takes a session-level advisory lock on a single fixed
# key (0x53484F52544C4B), and every DB-backed package's TestMain calls it. That
# lock is scoped to whatever database TEST_DATABASE_URL names — so two agents
# pointed at the SAME database serialise behind each other, and two agents
# pointed at DIFFERENT databases do not contend at all.
#
# That is the whole reason CLAUDE.md §5a capped concurrency at "only one
# subagent may be running tests". Give each agent its own database and the cap
# lifts. Cloning from a template makes it cheap: ~0.2s, versus ~2s to run 19
# migrations.
#
#     scripts/testdb.sh create 0123      # -> opencircuit_test_0123, prints the DSN
#     scripts/testdb.sh drop   0123
#     scripts/testdb.sh reset  0123      # drop + create
#     scripts/testdb.sh template         # (re)build the template from migrations/
#     scripts/testdb.sh list             # every scratch database that exists
#     scripts/testdb.sh gc --all         # drop ALL scratch databases (yours AND other agents')
#
# Typical use inside a subagent:
#
#     export TEST_DATABASE_URL="$(scripts/testdb.sh create 0123)"
#     go test ./internal/handlers/... -p 2
#     scripts/testdb.sh drop 0123
#
# The template is opencircuit_test_template, built by applying migrations/ to an
# empty database. Rebuild it whenever migrations change — `create` refuses to
# run if the template is behind migrations/, rather than silently handing you a
# stale schema.

set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PGHOST_URL="${PGHOST_URL:-postgres://opencircuit:opencircuit@localhost:5432}"
TEMPLATE="${TEMPLATE_DB:-opencircuit_test_template}"
PREFIX="opencircuit_test_"

psql_admin() { psql "$PGHOST_URL/postgres" "$@"; }
dsn() { echo "$PGHOST_URL/$1?sslmode=disable"; }

# The highest migration number on disk, e.g. 19 for 000019_*.up.sql.
disk_version() {
  ls "$REPO"/migrations/*.up.sql 2>/dev/null \
    | sed 's|.*/0*\([0-9]*\)_.*|\1|' | sort -n | tail -1
}

db_exists() {
  [ "$(psql_admin -tAc "select 1 from pg_database where datname='$1'")" = "1" ]
}

template_version() {
  db_exists "$TEMPLATE" || { echo "none"; return; }
  psql "$PGHOST_URL/$TEMPLATE" -tAc 'select version from schema_migrations' 2>/dev/null || echo "none"
}

require_createdb() {
  local can
  can="$(psql_admin -tAc "select rolcreatedb from pg_roles where rolname=current_user")"
  if [ "$can" != "t" ]; then
    cat >&2 <<EOF
error: the '$(psql_admin -tAc 'select current_user')' role cannot create databases.

This is a one-time local grant. Run it as a superuser, then retry:

    psql -d postgres -c 'ALTER ROLE opencircuit CREATEDB;'

Do NOT work around this by falling back to a superuser connection for the run —
that hides the missing grant from the next agent, who will hit it again.
EOF
    exit 1
  fi
}

build_template() {
  require_createdb
  command -v migrate >/dev/null || { echo "error: golang-migrate not on PATH (brew install golang-migrate)" >&2; exit 1; }
  echo "building $TEMPLATE from migrations/ ..."
  psql_admin -qc "DROP DATABASE IF EXISTS $TEMPLATE;"
  psql_admin -qc "CREATE DATABASE $TEMPLATE;"
  migrate -path "$REPO/migrations" -database "$(dsn "$TEMPLATE")" up
  echo "$TEMPLATE is at migration $(template_version)"
}

check_template_fresh() {
  local tv dv
  tv="$(template_version)"; dv="$(disk_version)"
  if [ "$tv" = "none" ]; then
    echo "template $TEMPLATE does not exist — building it once" >&2
    build_template >&2
  elif [ "$tv" != "$dv" ]; then
    cat >&2 <<EOF
error: template $TEMPLATE is at migration $tv but migrations/ is at $dv.

Rebuild it before running tests, or every scratch database you create will
carry a stale schema and fail for a reason that has nothing to do with your
change:

    scripts/testdb.sh template
EOF
    exit 1
  fi
}

name_for() {
  [ -n "${1:-}" ] || { echo "error: need an id (usually the issue number, e.g. 0123)" >&2; exit 1; }
  echo "${PREFIX}$(echo "$1" | tr -cd '[:alnum:]_')"
}

cmd="${1:-}"; shift || true
case "$cmd" in
  create)
    require_createdb; check_template_fresh
    db="$(name_for "${1:-}")"
    db_exists "$db" && psql_admin -qc "DROP DATABASE $db;"
    psql_admin -qc "CREATE DATABASE $db TEMPLATE $TEMPLATE;"
    dsn "$db"                     # stdout is ONLY the DSN, so it is safe to $( ) into a var
    ;;
  drop)
    # #0228: the old `A && B && C || D` chain reported "$db does not exist"
    # whenever B (the DROP) failed for ANY reason, not just when A (existence)
    # was false — so a database that exists but is owned by another role
    # (e.g. one restore.sh created with RESTORE_CREATE=1 as a different OS
    # user) hit `ERROR: must be owner of database`, printed the misleading
    # "does not exist" message, and exited 0, leaving the stray database
    # behind. Existence and drop-success are now checked separately, and a
    # real drop failure is reported as a failure with a non-zero exit.
    db="$(name_for "${1:-}")"
    if ! db_exists "$db"; then
      echo "$db does not exist"
      exit 0
    fi
    if psql_admin -qc "DROP DATABASE $db;"; then
      echo "dropped $db"
    else
      echo "error: failed to drop $db (see the psql error above — commonly: you are not its owner)" >&2
      exit 1
    fi
    ;;
  reset)
    "$0" drop "${1:-}" >/dev/null; "$0" create "${1:-}"
    ;;
  template) build_template ;;
  list)
    psql_admin -tAc "select datname from pg_database where datname like '${PREFIX}%' order by 1"
    ;;
  gc)
    # Sweeping is destructive to OTHER agents, not just to your own leftovers:
    # every scratch database belongs to somebody, and a bare `gc` used to drop
    # all of them. It must be asked for explicitly now. Use `drop NNNN` for
    # your own; `gc --all` only when you know you are alone.
    dbs=$(psql_admin -tAc "select datname from pg_database where datname like '${PREFIX}%' and datname <> '$TEMPLATE'")
    if [ "${1:-}" != "--all" ]; then
      if [ -z "$dbs" ]; then echo "no scratch databases"; exit 0; fi
      echo "refusing to sweep — these belong to somebody, possibly another agent:"
      echo "$dbs" | sed 's/^/  /'
      echo
      echo "drop your own:  scripts/testdb.sh drop <ISSUE>"
      echo "sweep them all: scripts/testdb.sh gc --all   (only when you are alone)"
      exit 1
    fi
    for db in $dbs; do
      conns=$(psql_admin -tAc "select count(*) from pg_stat_activity where datname = '$db'")
      if [ "${conns:-0}" -gt 0 ]; then
        echo "skipped $db — $conns active connection(s), someone is using it"
        continue
      fi
      psql_admin -qc "DROP DATABASE $db;" && echo "dropped $db"
    done
    ;;
  *)
    sed -n '2,40p' "$0" | sed 's/^# \{0,1\}//'
    exit 1
    ;;
esac
