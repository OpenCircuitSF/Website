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
# empty database. Rebuild it explicitly (`scripts/testdb.sh template`) whenever
# you add a migration and want the fast clone path back. `create` never hands
# you a stale schema: if the template's version doesn't match this working
# tree's migrations/ — in EITHER direction, e.g. because another agent or git
# worktree moved one side without you — it provisions that one database
# directly from migrations/ instead of cloning (slower, ~2s instead of ~0.2s,
# but correct), and says so on stderr. It never rebuilds the shared template
# on its own; that stays a separate, explicit act. See #0315.

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
  # #0315 criterion 5: rebuilding the template doesn't drop or touch anyone
  # else's scratch database (unlike `gc`, so this is a warning, not a
  # refusal) but it does move what every FUTURE `create` clones from. An
  # agent whose migrations/ still matches the OLD template version loses the
  # fast path the moment this runs and falls back to the slower direct-
  # provision path above until they catch up or someone rebuilds again.
  # Surface that instead of leaving it to be discovered as "why did create
  # suddenly get slower".
  scratch="$(psql_admin -tAc "select datname from pg_database where datname like '${PREFIX}%' and datname <> '$TEMPLATE'")"
  if [ -n "$scratch" ]; then
    echo "note: other scratch databases exist — rebuilding $TEMPLATE does not touch them, but any agent whose migrations/ still matches the current template loses the fast clone path (falls back to direct provisioning, #0315) until they catch up:" >&2
    echo "$scratch" | sed 's/^/  /' >&2
  fi
  echo "building $TEMPLATE from migrations/ ..."
  psql_admin -qc "DROP DATABASE IF EXISTS $TEMPLATE;"
  psql_admin -qc "CREATE DATABASE $TEMPLATE;"
  migrate -path "$REPO/migrations" -database "$(dsn "$TEMPLATE")" up
  echo "$TEMPLATE is at migration $(template_version)"
}

template_matches_disk() {
  [ "$(template_version)" = "$(disk_version)" ]
}

# #0315: this used to be check_template_fresh(), which hard-errored the
# moment the template's migration version differed from this working tree's
# migrations/ in EITHER direction, then told you to run `template` to fix it.
# That "fix" is exactly the thing that must NOT happen implicitly: rebuilding
# the shared template clobbers it out from under any other agent (or git
# worktree — each worktree has its own migrations/ directory on disk, so an
# uncommitted new migration in one worktree never shows up in `ls` from
# another) that is relying on the version currently there. #0123's
# migrations/000025 plus a template rebuild did exactly that to #0129's
# reviewer, and scripts/check.sh aborted before running a single test.
#
# The guard's actual job — never test against the wrong schema (acceptance
# criterion 2) — does not require cloning the template at all. It only
# requires that whatever database a create hands back was migrated up from
# THIS working tree's migrations/, exactly. So on a mismatch we stop trying
# to clone and instead provision directly: create an empty database and run
# `migrate up` against it from $REPO/migrations, same as build_template does
# for the template itself. That is correct regardless of which side is ahead,
# unblocks the agent instead of erroring, and never touches the shared
# resource — rebuilding it stays a separate, explicit act
# (`scripts/testdb.sh template`, criterion 4).
#
# Slower per creation (a full `migrate up`, not the ~0.2s clone the template
# exists to give you) but only for as long as the mismatch lasts — once
# someone rebuilds the template to match, `create` goes back to cloning.
warn_template_mismatch() {
  local db="$1" tv dv
  tv="$(template_version)"; dv="$(disk_version)"
  if [ "$tv" = "none" ]; then
    cat >&2 <<EOF
note: template $TEMPLATE does not exist yet — provisioning $db directly from
migrations/ (this working tree is at $dv). This does not touch the shared
template; build it once, explicitly, to get the fast clone path for everyone:

    scripts/testdb.sh template
EOF
    return
  fi
  local side
  if [ "$tv" -gt "$dv" ] 2>/dev/null; then
    side="the shared template ($TEMPLATE) is ahead of this working tree's migrations/ — template is at $tv, migrations/ is at $dv"
  else
    side="this working tree's migrations/ is ahead of the shared template ($TEMPLATE) — migrations/ is at $dv, template is at $tv"
  fi
  cat >&2 <<EOF
note: $side.
Provisioning $db directly from migrations/ instead of cloning $TEMPLATE, so
the schema matches THIS working tree exactly (slower: a full 'migrate up'
instead of the ~0.2s clone, but correct regardless of which side moved — see
CLAUDE.md §5a and issue #0315).

This is expected, not a broken environment: another agent or git worktree has
moved migrations/ forward without you (or vice versa). Nothing here rebuilds
the shared template. If you are the one who moved it and want the fast path
back for everyone, do that explicitly — only once you are sure it will not
pull the schema out from under a concurrent agent:

    scripts/testdb.sh template
EOF
}

provision_from_migrations() {
  local db="$1"
  command -v migrate >/dev/null || { echo "error: golang-migrate not on PATH (brew install golang-migrate)" >&2; exit 1; }
  psql_admin -qc "CREATE DATABASE $db;" >&2
  migrate -path "$REPO/migrations" -database "$(dsn "$db")" up >&2
}

name_for() {
  [ -n "${1:-}" ] || { echo "error: need an id (usually the issue number, e.g. 0123)" >&2; exit 1; }
  # #0208: Postgres folds an unquoted identifier to lowercase, so a mixed-case
  # token like "0140revX" used to make `create` hand back a DSN naming
  # "...0140revX" while the database Postgres actually created was
  # "...0140revx". Every DB-backed test then failed with SQLSTATE 3D000
  # (database does not exist) -- which reads exactly like a real test
  # failure and cost #0140's reviewer a false alarm mid-review -- and `drop`
  # silently failed to find the differently-cased name, leaking the real
  # database. Lower-case the token here so the name this function returns
  # always matches what Postgres actually created or will create.
  echo "${PREFIX}$(echo "$1" | tr -cd '[:alnum:]_' | tr '[:upper:]' '[:lower:]')"
}

cmd="${1:-}"; shift || true
case "$cmd" in
  create)
    require_createdb
    db="$(name_for "${1:-}")"
    db_exists "$db" && psql_admin -qc "DROP DATABASE $db;"
    if template_matches_disk; then
      psql_admin -qc "CREATE DATABASE $db TEMPLATE $TEMPLATE;"
    else
      # #0315: no hard error, no implicit rebuild of the shared template —
      # provision this one database straight from migrations/ instead.
      warn_template_mismatch "$db"
      provision_from_migrations "$db"
    fi
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
