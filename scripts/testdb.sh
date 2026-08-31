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
#
# The version number alone cannot catch an in-place edit to an existing
# migration file (same filename, same number, different contents) — #0320.
# So the freshness check also compares a content digest of migrations/*.sql,
# stored as the template database's COMMENT ON DATABASE when the template is
# built. A digest mismatch routes to the same direct-provision fallback as a
# version mismatch; it still never rebuilds the shared template implicitly.

set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PGHOST_URL="${PGHOST_URL:-postgres://opencircuit:opencircuit@localhost:5432}"
PREFIX="opencircuit_test_"
# #0327: the REAL default template name, independent of any TEMPLATE_DB
# override. Defined from PREFIX (not a second hardcoded literal) so it
# always names the actual shared template even when $TEMPLATE below points
# somewhere else — `gc --all` must never drop this one no matter what an
# agent has set TEMPLATE_DB to.
DEFAULT_TEMPLATE="${PREFIX}template"
TEMPLATE="${TEMPLATE_DB:-$DEFAULT_TEMPLATE}"

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

# #0328: version and digest used to be two separate functions, each
# preceded by its own db_exists() probe and each opening its own
# connection — four psql invocations total for what template_matches_disk
# needs on every `create` (measured by #0320's review: ~0.04s of the
# digest's ~0.06s added cost was exactly this). schema_migrations lives
# inside the template database, and pg_shdescription/pg_database are
# shared catalogs visible from ANY database in the cluster — so both can be
# read in one query over one connection to the template itself. No
# up-front existence probe is needed either: connecting to a template that
# doesn't exist simply fails, and this function's own control flow (the
# `|| true` on the assignment below, #0331) is what turns that failure into
# the "none"/"none" pair, unconditionally — not, as an earlier version of
# this comment claimed, luck of how the caller happens to invoke it.
#
# #0331: the assignment below used to have no `|| true`. Under `set -e`, a
# DIRECT call to this function (not inside an `if` condition, which
# suspends -e, and not inside a command substitution, where a bash 3.2
# quirk happens to swallow the failure) aborted the whole script silently
# the moment $TEMPLATE didn't exist — psql exits 2, and a failing command
# substitution assigned to a plain `var=$(...)` propagates that exit status
# to the simple command containing it. The two current call sites
# (`template_matches_disk`, always used as an `if` condition, and
# `template_version`/`template_digest`, always reached through their own
# command substitution) never happened to trigger it, but nothing about
# `template_state`'s own body guaranteed that for a future caller. The
# `|| true` makes the function safe by construction: a failed psql call now
# always leaves `row` empty (never aborts the shell), and the explicit
# `[ -n ... ] || …="none"` lines below do the rest, exactly as intended.
#
# Sets TSTATE_VERSION and TSTATE_DIGEST as a side effect (this shell is
# 3.2.57 — no nameref/`local -n` to return two values properly). Fields are
# joined with ASCII US (chr(31)), which cannot appear in a migration
# version number or a hex digest, so a real value can never be mistaken for
# the separator.
template_state() {
  local row
  row="$(psql "$PGHOST_URL/$TEMPLATE" -tAc "select coalesce((select version::text from schema_migrations), 'none') || chr(31) || coalesce((select description from pg_shdescription ds join pg_database db on ds.objoid = db.oid and ds.classoid = 'pg_database'::regclass where db.datname = current_database()), 'none')" 2>/dev/null)" || true
  TSTATE_VERSION="${row%%$'\x1f'*}"
  TSTATE_DIGEST="${row#*$'\x1f'}"
  [ -n "$TSTATE_VERSION" ] || TSTATE_VERSION="none"
  [ -n "$TSTATE_DIGEST" ] || TSTATE_DIGEST="none"
}

template_version() {
  template_state
  echo "$TSTATE_VERSION"
}

require_shasum() {
  command -v shasum >/dev/null || { echo "error: shasum not on PATH (needed to fingerprint migrations/, #0320)" >&2; exit 1; }
}

# #0320: a digest is "valid" only if it is exactly 64 lowercase hex chars —
# sha256's output shape. #0301 found that a missing preflight let a digest
# check compare two empty strings and silently report a match having
# measured nothing; this guard exists so an empty or malformed digest is a
# hard error, never a quiet false "matches".
valid_digest() {
  printf '%s' "$1" | LC_ALL=C grep -Eq '^[0-9a-f]{64}$'
}

# A digest of every migrations/*.sql file's contents (both .up.sql and
# .down.sql), keyed by relative filename only (never the absolute path) and
# sorted byte-wise (LC_ALL=C) so the result is identical across machines and
# independent of directory listing order. #0320.
#
# Hashes the sorted filename list, then the file contents concatenated in
# that same order (via one batched `xargs cat` rather than a per-file
# printf+cat loop) — a rename changes the name list, an edit changes the
# content stream, either changes the digest. The batching matters: an
# earlier per-file loop forked ~100 processes for 50 migrations and measured
# ~0.13s on this machine (criterion 6's whole fast-path budget), against
# ~0.02s batched — the per-`create` cost this function adds must not erase
# the clone path's advantage over a full `migrate up`.
#
# #0328: enumeration and content-reading are NUL-delimited end to end
# (`find -print0`, `xargs -0 cat`), so a migration filename containing a
# space or a quote — the #0320 review's fifth stray-file case, which used
# to abort `xargs` with "unterminated quote" — cannot break word-splitting.
# Names are collected via a `read -d ''` loop (a bash builtin, no per-file
# fork) into an array, then that array is sorted by piping it through
# `sort` newline-delimited — safe for SORTING (not for content-reading)
# because no migration filename contains a newline, which is a different
# hazard than the one #0320 found. This machine's bash is 3.2.57, which has
# no `mapfile`/`readarray` to build an array from a NUL stream directly.
disk_digest() {
  require_shasum
  local dir="$REPO/migrations" out
  [ -d "$dir" ] || { echo "none"; return; }

  local names=()
  while IFS= read -r -d '' f; do
    names+=("${f#./}")
  done < <(cd "$dir" && find . -maxdepth 1 -type f -name '*.sql' -print0)
  [ "${#names[@]}" -gt 0 ] || { echo "none"; return; }

  local sorted=()
  while IFS= read -r line; do
    sorted+=("$line")
  done < <(printf '%s\n' "${names[@]}" | LC_ALL=C sort)

  out="$(
    { printf '%s\n' "${sorted[@]}"
      printf '%s\0' "${sorted[@]}" | ( cd "$dir" && xargs -0 cat )
    } | shasum -a 256 | awk '{print $1}'
  )"
  if ! valid_digest "$out"; then
    echo "error: shasum did not produce a valid digest for migrations/ (got: '$out')" >&2
    exit 1
  fi
  echo "$out"
}

# The digest recorded on the template when it was last built, read from
# COMMENT ON DATABASE via template_state() above (#0320, folded into the
# single combined query by #0328 — this used to open its own second
# connection into `postgres`, with its own db_exists() probe first, and its
# pg_shdescription join lacked the classoid qualifier now in
# template_state()'s query; that catalog is keyed (objoid, classoid) and
# also holds role/tablespace comments, so an oid collision across catalogs
# could have matched the wrong row).
template_digest() {
  template_state
  local d
  d="$(printf '%s' "$TSTATE_DIGEST" | tr -d '[:space:]')"
  [ -n "$d" ] || { echo "none"; return; }
  echo "$d"
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
  # #0320: record a content digest alongside the version, so an in-place
  # edit to an existing migration (same number, different bytes) is visible
  # to template_matches_disk even though the version number alone would
  # look unchanged.
  local digest
  digest="$(disk_digest)"
  psql_admin -qc "COMMENT ON DATABASE $TEMPLATE IS '$digest';"
  echo "$TEMPLATE is at migration $(template_version), digest ${digest:0:12}..."
}

# #0320: version alone cannot catch an in-place edit to an existing
# migration file, since the filename (and therefore the number) does not
# change. The digest comparison is what catches that case; the version
# comparison is kept too since it is cheaper to explain in the mismatch
# message and catches the common case (a migration added or removed) without
# needing to read every file.
template_matches_disk() {
  # #0328: one template_state() call (one psql invocation) instead of the
  # separate template_version() + template_digest() calls this used to
  # make, each of which re-ran its own existence probe and opened its own
  # connection.
  template_state
  [ "$TSTATE_VERSION" = "$(disk_version)" ] && [ "$TSTATE_DIGEST" = "$(disk_digest)" ]
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
  if [ "$tv" != "$dv" ]; then
    if [ "$tv" -gt "$dv" ] 2>/dev/null; then
      side="the shared template ($TEMPLATE) is ahead of this working tree's migrations/ — template is at $tv, migrations/ is at $dv"
    else
      side="this working tree's migrations/ is ahead of the shared template ($TEMPLATE) — migrations/ is at $dv, template is at $tv"
    fi
  else
    # #0320: same version on both sides, but the content digest disagrees —
    # an existing migration file was edited in place without a number bump.
    side="the shared template ($TEMPLATE) and this working tree's migrations/ both report migration $dv, but the content digest disagrees — a migration file was likely edited in place without changing its number (#0320)"
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
    # #0327: exclude the currently-selected template ($TEMPLATE, which
    # TEMPLATE_DB may have overridden), the real default template (always,
    # regardless of the override), and anything that merely looks like a
    # template by name — an agent following #0315's TEMPLATE_DB advice may
    # create its own "..._template"-suffixed scratch database of their own.
    #
    # #0332: that third clause used to be `not ilike '%template%'` — an
    # UNANCHORED substring match. Any scratch database whose name merely
    # contains "template" anywhere (opencircuit_test_template_probe,
    # opencircuit_test_0315template) was invisible to BOTH of gc's paths: not
    # swept by `gc --all`, and not even named in the bare-`gc` refusal
    # listing, so nobody was told it existed to clean up by hand. A leak, not
    # a loss (`list` and `drop <id>` still worked on it), but a silent one.
    # `!~* '_template$'` anchors it to the actual naming shape #0315 teaches
    # (a "..._template" SUFFIX) using a POSIX regex end-anchor rather than
    # SQL LIKE, deliberately — LIKE's `_` is a single-character WILDCARD, not
    # a literal underscore, so `ilike '%\_template'` would need an ESCAPE
    # clause to mean what it looks like it means; the regex form has no such
    # trap. Verified against exactly the two leak-shaped names from this
    # issue's own description: 'opencircuit_test_template_probe' and
    # 'opencircuit_test_0315template' both now match neither the old nor the
    # new pattern's PROTECTED set — they are swept, as intended — while
    # 'opencircuit_test_template' and any "..._template" name still are.
    #
    # Kept on one line, and each of the three predicates written so a `sed`
    # targeting one clause's own literal text cannot also touch another's —
    # so the mutation-proof guards in testdb_gc_guard_test.sh (Part 4,
    # #0327's original proof, and Part 6, #0332's) can neuter DEFAULT_TEMPLATE
    # and the anchored pattern INDEPENDENTLY, not just both at once.
    exclude_clause="datname <> '$TEMPLATE' and datname <> '$DEFAULT_TEMPLATE' and datname !~* '_template\$'"
    dbs=$(psql_admin -tAc "select datname from pg_database where datname like '${PREFIX}%' and $exclude_clause")
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
