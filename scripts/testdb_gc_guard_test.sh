#!/usr/bin/env bash
#
# testdb_gc_guard_test.sh — guard test for `scripts/testdb.sh gc` (issue #0150).
#
# #0150: `gc` used to drop EVERY opencircuit_test_* database unconditionally.
# An agent ran it as a cleanup step and destroyed a concurrently running
# agent's database. The fix made `gc` refuse without --all, and made
# `gc --all` skip any database with a live connection. This script is the
# guard that keeps that fix from quietly regressing (the same shape of gap
# that let #0140's `pipefail` regress silently).
#
# What it proves, in order:
#   1. Bare `gc` (no --all) against the REAL scripts/testdb.sh refuses and
#      does not drop a scratch database that exists.
#   2. `gc --all` skips a database that has a live connection, and still
#      drops one that doesn't.
#   3. Mutation proof: with the refusal guard removed, bare `gc` DOES sweep —
#      i.e. assertion (1) is actually sensitive to the #0150 regression, not
#      vacuously true.
#
# SAFETY DESIGN — read this before changing the test
#
# CLAUDE.md §8b and docs/obstacles.md §4 are explicit: a mutation test that
# disables a guard must run against databases this test creates and owns,
# never against a real agent's database, and never by editing the shared
# tracked script file in place while other agents might invoke it.
#
# So steps 2 and 3 never run scripts/testdb.sh's real `gc --all` against the
# real 'opencircuit_test_' prefix (which would match every OTHER agent's
# scratch database too, live-connection or not — running it, even correctly
# guarded, is still touching shared state we don't own). Instead they run a
# private COPY of testdb.sh, repointed at a test-only prefix
# (TESTPREFIX, below — "opencircuit_test_${RUNID}gt_", #0253's per-run
# namespace, not a fixed string) that no real agent's issue id can ever
# produce (name_for() strips to alnum/underscore, and no real issue is filed
# under a "<RUNID>gt" id). All destructive work in steps 2 and 3 is scoped to
# that prefix, so it can only ever touch databases this script itself
# created — including from a CONCURRENT run of this same script, since each
# run's RUNID (and therefore its TESTPREFIX) differs.
#
# The "restore, verify byte-identity with shasum -a 256" requirement in
# #0150's acceptance criteria is satisfied by hashing the real, tracked
# scripts/testdb.sh at the start and end of this run and asserting they are
# identical — proving this test process makes zero net change to the shared
# script, which is true because it never touches that file at all, only a
# private copy of it.
#
# Usage: scripts/testdb_gc_guard_test.sh
# Exit 0 = all guards hold. Exit 1 = a regression was detected (message names it).

set -uo pipefail  # NOT -e: several commands here are *expected* to fail (that's the assertion)

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REAL_SCRIPT="$REPO/scripts/testdb.sh"
PGHOST_URL="${PGHOST_URL:-postgres://opencircuit:opencircuit@localhost:5432}"

# #0253: TESTPREFIX used to be the fixed "opencircuit_test_0150gt_" for every
# run, so two concurrent runs collided on it silently (createdb_raw() below
# swallowed the resulting CREATE DATABASE error) and Part 2's `gc --all`
# actively swept the other run's databases mid-test, which then misattributed
# the damage to Part 4's leak census as a false "REGRESSION #0223". Namespace
# per run using the project's existing ISSUE convention (falling back to $$,
# the same convention #0247 used for the restore drill) rather than a second
# scheme. Lower-case and alnum-only, the same fold #0208 applied to
# testdb.sh's own name_for() — TESTPREFIX is interpolated into raw SQL
# identifiers the same way, so a new call site here must not reintroduce a
# mixed-case bug.
RUNID_RAW="${ISSUE:-$$}"
RUNID="$(printf '%s' "$RUNID_RAW" | tr -cd '[:alnum:]' | tr '[:upper:]' '[:lower:]')"
[ -n "$RUNID" ] || { echo "error: empty run id (derived from ISSUE=$RUNID_RAW)" >&2; exit 1; }
TESTPREFIX="opencircuit_test_${RUNID}gt_"   # scoped prefix; never matches a real agent's database

WORKDIR="$(mktemp -d)"
FAILURES=0
CREATED_DBS=()   # raw databases this script created, for cleanup
BG_PIDS=()

psql_admin() { psql "$PGHOST_URL/postgres" "$@"; }
db_exists() { [ "$(psql_admin -tAc "select 1 from pg_database where datname='$1'" 2>/dev/null)" = "1" ]; }
# #0253: this used to swallow every error (>/dev/null 2>&1, no exit check),
# so a CREATE DATABASE collision with a concurrent run sharing the same
# prefix was silent — run B would carry on against run A's database instead
# of failing loudly. Namespacing TESTPREFIX above makes that collision
# unlikely; this makes it impossible to miss if it happens anyway.
createdb_raw() {
  local db="$1" out rc
  out="$(psql_admin -qc "CREATE DATABASE $db;" 2>&1)"
  rc=$?
  if [ "$rc" -ne 0 ]; then
    echo "FATAL: CREATE DATABASE $db failed (exit $rc) — treating this as a collision with a concurrent run rather than silently continuing against a database this run may not own. psql output:" >&2
    printf '%s\n' "$out" >&2
    exit 1
  fi
  CREATED_DBS+=("$db")
}
dropdb_raw() { psql_admin -qc "DROP DATABASE IF EXISTS $1;" >/dev/null 2>&1 || true; }

fail() {
  FAILURES=$((FAILURES + 1))
  printf 'FAIL: %s\n' "$1" >&2
}
pass() { printf 'PASS: %s\n' "$1"; }

# #0223: killing the local psql client only stops that local process — it
# does not, by itself, stop the backend it was talking to. Measured directly
# while fixing this: a backend blocked inside `select pg_sleep(20)` keeps
# running for however long the sleep has left even after its client is
# killed, because Postgres does not poll the client socket while blocked
# inside a long-running function call (client_connection_check_interval is
# unset here, so nothing does). So the backend can still be attached — and
# `dropdb_raw`'s `DROP DATABASE IF EXISTS ... || true` can still silently
# swallow a "database is being accessed by other users" failure — long after
# the local kill returned. terminate_backends() below stops the backend
# itself; wait_for_disconnect() polls pg_stat_activity (bounded, not a blind
# sleep) so a caller can confirm that actually happened before dropping.
terminate_backends() {
  local db="$1"
  psql_admin -tAc "select pg_terminate_backend(pid) from pg_stat_activity where datname='$db' and pid <> pg_backend_pid()" >/dev/null 2>&1 || true
}

wait_for_disconnect() {
  local db="$1" timeout="${2:-20}" waited=0
  while [ "$waited" -lt "$timeout" ]; do
    local n
    n="$(psql_admin -tAc "select count(*) from pg_stat_activity where datname='$db'" 2>/dev/null)"
    [ "$n" = "0" ] && return 0
    sleep 0.5
    waited=$((waited + 1))
  done
  local pid
  pid="$(psql_admin -tAc "select pid from pg_stat_activity where datname='$db' limit 1" 2>/dev/null)"
  echo "warning: connection to $db did not close within $(awk "BEGIN{print $timeout*0.5}")s (surviving pid: ${pid:-unknown})" >&2
  return 1
}

# Drop $1 and verify it is actually gone afterwards, rather than trusting
# dropdb_raw's swallowed `|| true` (the other half of #0223).
drop_and_verify() {
  local db="$1"
  dropdb_raw "$db"
  if db_exists "$db"; then
    fail "cleanup failed to drop $db (still exists after DROP DATABASE IF EXISTS)"
    return 1
  fi
  return 0
}

cleanup() {
  for pid in "${BG_PIDS[@]:-}"; do
    [ -n "$pid" ] && kill "$pid" >/dev/null 2>&1 || true
  done
  for pid in "${BG_PIDS[@]:-}"; do
    [ -n "$pid" ] && wait "$pid" 2>/dev/null || true
  done
  for db in "${CREATED_DBS[@]:-}"; do
    [ -n "$db" ] || continue
    terminate_backends "$db"
    wait_for_disconnect "$db" 20 || true
    dropdb_raw "$db"
    if db_exists "$db"; then
      echo "warning: cleanup trap could not confirm $db was dropped — it may still exist (see any surviving-pid warning above)" >&2
    fi
  done
  rm -rf "$WORKDIR"
}
trap cleanup EXIT

command -v psql >/dev/null || { echo "error: psql not on PATH" >&2; exit 1; }
[ -f "$REAL_SCRIPT" ] || { echo "error: $REAL_SCRIPT not found" >&2; exit 1; }

# ── Byte-identity bookend: prove this run never left the tracked file changed ──
if ! command -v shasum >/dev/null; then
  echo "error: shasum not on PATH" >&2
  exit 1
fi
SHA_BEFORE="$(shasum -a 256 "$REAL_SCRIPT" | awk '{print $1}')"

echo "== Part 1: bare \`gc\` refuses (real scripts/testdb.sh, unmutated) =="

A1="${TESTPREFIX}refusal_a"
A2="${TESTPREFIX}refusal_b"
createdb_raw "$A1"
createdb_raw "$A2"

OUT="$("$REAL_SCRIPT" gc 2>&1)"
RC=$?

if [ "$RC" -eq 0 ]; then
  fail "REGRESSION #0150: bare 'scripts/testdb.sh gc' exited 0 (expected 1 — refusal) with scratch databases present. Guard is not firing."
else
  pass "bare gc exited non-zero (refused) with scratch databases present"
fi

if db_exists "$A1" && db_exists "$A2"; then
  pass "bare gc did not drop scratch databases ($A1, $A2 still exist)"
else
  fail "REGRESSION #0150: bare 'scripts/testdb.sh gc' (no --all) dropped a scratch database without being asked. This is the exact #0150 defect: the safe default swept another agent's database."
fi

echo "$OUT" | grep -q -- "--all" || fail "bare gc's refusal message no longer points the caller at --all — a caller reading it won't know how to actually sweep"

dropdb_raw "$A1"; dropdb_raw "$A2"

echo "== Part 2: live-connection skip (scoped-prefix copy, --all) =="

SCOPED="$WORKDIR/testdb_scoped.sh"
sed "s/^PREFIX=\"opencircuit_test_\"\$/PREFIX=\"${TESTPREFIX}\"/" "$REAL_SCRIPT" > "$SCOPED"
chmod +x "$SCOPED"

# Verify the prefix substitution actually took — if it silently didn't, the
# scoped copy would still sweep the REAL shared prefix and this test would be
# exactly the hazard it exists to prevent. Abort loudly rather than proceed.
if ! grep -q "^PREFIX=\"${TESTPREFIX}\"\$" "$SCOPED"; then
  echo "FATAL: prefix-scoping of the test copy failed — aborting before running any --all sweep, to avoid touching real scratch databases." >&2
  exit 1
fi
if grep -q '^PREFIX="opencircuit_test_"$' "$SCOPED"; then
  echo "FATAL: scoped copy still contains the real prefix — aborting before running any --all sweep." >&2
  exit 1
fi

CONN_DB="${TESTPREFIX}conn"
IDLE_DB="${TESTPREFIX}idle"
createdb_raw "$CONN_DB"
createdb_raw "$IDLE_DB"

# Hold a live connection open on CONN_DB in the background.
psql "$PGHOST_URL/$CONN_DB" -c "select pg_sleep(20)" >/dev/null 2>&1 &
CONN_PID=$!
BG_PIDS+=("$CONN_PID")

# Poll (don't blind-sleep) until pg_stat_activity actually shows the connection.
WAITED=0
while [ "$(psql_admin -tAc "select count(*) from pg_stat_activity where datname='$CONN_DB'")" -eq 0 ] && [ "$WAITED" -lt 10 ]; do
  sleep 0.5
  WAITED=$((WAITED + 1))
done

"$SCOPED" gc --all >/tmp/testdb_gc_guard_test_part2.$$ 2>&1
cat /tmp/testdb_gc_guard_test_part2.$$
rm -f /tmp/testdb_gc_guard_test_part2.$$

if db_exists "$CONN_DB"; then
  pass "gc --all skipped $CONN_DB, which had a live connection"
else
  fail "REGRESSION #0150: 'gc --all' dropped a database with an active connection ($CONN_DB) instead of skipping it"
fi

if db_exists "$IDLE_DB"; then
  fail "gc --all did not drop $IDLE_DB, which had no connection and should have been swept (feature not working, separate from the #0150 guard itself)"
else
  pass "gc --all dropped $IDLE_DB, which had no connection"
fi

# Terminate the backend server-side rather than trusting the local client's
# death to stop it — see the comment above terminate_backends() (#0223): a
# backend blocked inside pg_sleep(20) keeps running, sleep and all, for as
# long as it has left even after its client is killed.
terminate_backends "$CONN_DB"
kill "$CONN_PID" >/dev/null 2>&1 || true
wait "$CONN_PID" 2>/dev/null || true
if wait_for_disconnect "$CONN_DB" 20; then
  pass "connection to $CONN_DB closed before cleanup dropped it"
else
  fail "REGRESSION #0223: connection to $CONN_DB did not close within the poll timeout before cleanup attempted to drop it"
fi
drop_and_verify "$CONN_DB"
drop_and_verify "$IDLE_DB"

echo "== Part 3: mutation proof — remove the guard, confirm bare gc sweeps again =="

MUTANT="$WORKDIR/testdb_mutant.sh"
sed -e "s/^PREFIX=\"opencircuit_test_\"\$/PREFIX=\"${TESTPREFIX}\"/" \
    -e 's/if \[ "\${1:-}" != "--all" \]; then/if false; then/' \
    "$REAL_SCRIPT" > "$MUTANT"
chmod +x "$MUTANT"

if ! grep -q "^PREFIX=\"${TESTPREFIX}\"\$" "$MUTANT"; then
  echo "FATAL: prefix-scoping of the mutant copy failed — aborting before running it." >&2
  exit 1
fi
if ! grep -q 'if false; then' "$MUTANT"; then
  echo "FATAL: guard-removal mutation did not take effect — aborting rather than run an unmutated bare gc (that would prove nothing)." >&2
  exit 1
fi

MUT_DB="${TESTPREFIX}mutant"
createdb_raw "$MUT_DB"

"$MUTANT" gc >/dev/null 2>&1   # bare — no --all — but the guard is neutered

if db_exists "$MUT_DB"; then
  fail "mutation was ineffective: with the refusal guard removed, bare gc still did NOT sweep $MUT_DB. This means Part 1's assertions would not actually catch a real #0150 regression — the guard test is not sensitive."
else
  pass "with the refusal guard removed, bare gc DID sweep — confirms Part 1's assertions are sensitive to the #0150 regression, not vacuously true"
fi
# Drop-and-verify rather than leaving this to the trap: harmless no-op if the
# mutant already swept it, and keeps every explicitly-created database
# accounted for before the leak census below runs.
drop_and_verify "$MUT_DB"

echo "== Part 4: gc --all does not drop a template under a TEMPLATE_DB override (#0327) =="
#
# #0327: `gc --all` used to exclude ONLY the current $TEMPLATE from its
# sweep. When TEMPLATE_DB is overridden — which #0315/#0320 actively teach
# agents to do to test template behaviour without disturbing concurrent
# agents — the REAL default template (unset TEMPLATE_DB's name) was no
# longer excluded, and got swept. The fix adds two more exclusions:
# DEFAULT_TEMPLATE (derived from PREFIX, so it always names the real
# default regardless of any override) and a broad "looks like a template"
# name pattern. This asserts both the positive behaviour (the real script,
# under an override, still protects the default-looking database and the
# overridden one, while still sweeping an ordinary scratch database) and,
# via mutation, that the assertion is actually sensitive to the #0327
# regression rather than vacuously true.

DEFAULTISH_DB="${TESTPREFIX}template"          # what DEFAULT_TEMPLATE resolves to under $SCOPED's prefix
OVERRIDE_TEMPLATE_DB="${TESTPREFIX}other_template"  # what an agent following #0315's advice sets TEMPLATE_DB to
ORDINARY_TEMPLATE_TEST_DB="${TESTPREFIX}ordinary4"

createdb_raw "$DEFAULTISH_DB"
createdb_raw "$OVERRIDE_TEMPLATE_DB"
createdb_raw "$ORDINARY_TEMPLATE_TEST_DB"

TEMPLATE_DB="$OVERRIDE_TEMPLATE_DB" "$SCOPED" gc --all >/dev/null 2>&1

if db_exists "$DEFAULTISH_DB"; then
  pass "gc --all did not drop the default-looking template ($DEFAULTISH_DB) even though TEMPLATE_DB was overridden to $OVERRIDE_TEMPLATE_DB"
else
  fail "REGRESSION #0327: gc --all dropped the default-looking template ($DEFAULTISH_DB) while TEMPLATE_DB was overridden away from it"
fi

if db_exists "$OVERRIDE_TEMPLATE_DB"; then
  pass "gc --all did not drop the currently-overridden template ($OVERRIDE_TEMPLATE_DB)"
else
  fail "REGRESSION #0327: gc --all dropped the currently-overridden template ($OVERRIDE_TEMPLATE_DB)"
fi

if db_exists "$ORDINARY_TEMPLATE_TEST_DB"; then
  fail "gc --all did not drop $ORDINARY_TEMPLATE_TEST_DB, an ordinary scratch database unrelated to any template (feature not working, separate from the #0327 guard itself)"
else
  pass "gc --all dropped $ORDINARY_TEMPLATE_TEST_DB, the ordinary scratch database"
fi

drop_and_verify "$DEFAULTISH_DB"
drop_and_verify "$OVERRIDE_TEMPLATE_DB"
drop_and_verify "$ORDINARY_TEMPLATE_TEST_DB"

# Mutation proof: neuter #0327's exclusion clause back to its pre-fix shape
# (only the currently-selected $TEMPLATE excluded) and confirm the
# default-looking template WOULD then be swept — proving the assertions
# above are actually sensitive to the #0327 regression, not vacuously true.
# Targets the `exclude_clause=` line specifically (a whole-line replace, via
# a wildcard match on the line's content) rather than reproducing any of
# its literal SQL text as a comparison oracle.
MUTANT2="$WORKDIR/testdb_mutant2.sh"
sed -e "s/^PREFIX=\"opencircuit_test_\"\$/PREFIX=\"${TESTPREFIX}\"/" \
    -e "s/^    exclude_clause=.*/    exclude_clause=\"datname <> '\$TEMPLATE'\"/" \
    "$REAL_SCRIPT" > "$MUTANT2"
chmod +x "$MUTANT2"

if ! grep -q "^PREFIX=\"${TESTPREFIX}\"\$" "$MUTANT2"; then
  echo "FATAL: prefix-scoping of the second mutant copy failed — aborting before running it." >&2
  exit 1
fi
if ! grep -Fq "exclude_clause=\"datname <> '\$TEMPLATE'\"" "$MUTANT2"; then
  echo "FATAL: #0327 guard-removal mutation did not take effect — aborting rather than run an unmutated gc --all (that would prove nothing)." >&2
  exit 1
fi
if grep -q "DEFAULT_TEMPLATE" "$MUTANT2" && grep -q "and datname <> '\$DEFAULT_TEMPLATE'" "$MUTANT2"; then
  echo "FATAL: mutation left the DEFAULT_TEMPLATE exclusion intact — the mutant does not actually reproduce the pre-#0327 script." >&2
  exit 1
fi

MUT2_DEFAULTISH="${TESTPREFIX}template2"
MUT2_OVERRIDE="${TESTPREFIX}other_template2"
createdb_raw "$MUT2_DEFAULTISH"
createdb_raw "$MUT2_OVERRIDE"

TEMPLATE_DB="$MUT2_OVERRIDE" "$MUTANT2" gc --all >/dev/null 2>&1

if db_exists "$MUT2_DEFAULTISH"; then
  fail "mutation was ineffective: with the #0327 exclusions removed, gc --all still did NOT sweep the default-looking template ($MUT2_DEFAULTISH). This means Part 4's assertions would not actually catch a real #0327 regression."
else
  pass "with the #0327 exclusions removed, gc --all under a TEMPLATE_DB override DID sweep the default-looking template — confirms Part 4's assertions are sensitive to the #0327 regression, not vacuously true"
fi
drop_and_verify "$MUT2_DEFAULTISH"
drop_and_verify "$MUT2_OVERRIDE"

echo "== Part 5: leak census — no ${TESTPREFIX}* database survives this run =="
LEFTOVER="$(psql_admin -tAc "select datname from pg_database where datname like '${TESTPREFIX}%'" 2>/dev/null | tr '\n' ' ' | xargs)"
if [ -z "$LEFTOVER" ]; then
  pass "no ${TESTPREFIX}* databases remain after cleanup"
else
  fail "REGRESSION #0223: leaked database(s) after cleanup: $LEFTOVER"
fi

echo "== Byte-identity check =="
SHA_AFTER="$(shasum -a 256 "$REAL_SCRIPT" | awk '{print $1}')"
if [ "$SHA_BEFORE" = "$SHA_AFTER" ]; then
  pass "scripts/testdb.sh unchanged across the run (sha256 $SHA_AFTER) — all mutation happened on private copies in $WORKDIR, never the tracked file"
else
  fail "CRITICAL: scripts/testdb.sh's sha256 changed during this test run ($SHA_BEFORE -> $SHA_AFTER). The tracked script may have been modified. Investigate immediately with: git diff -- scripts/testdb.sh"
fi

echo
if [ "$FAILURES" -eq 0 ]; then
  echo "testdb_gc_guard_test.sh: all guards hold (0 failures)"
  exit 0
else
  echo "testdb_gc_guard_test.sh: $FAILURES failure(s) — see FAIL lines above"
  exit 1
fi
