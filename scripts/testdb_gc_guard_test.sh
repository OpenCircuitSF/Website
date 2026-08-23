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
# (opencircuit_test_0150gt_) that no real agent's issue id can ever produce
# (name_for() strips to alnum/underscore, and no real issue is filed under
# "0150gt"). All destructive work in steps 2 and 3 is scoped to that prefix,
# so it can only ever touch databases this script itself created.
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
TESTPREFIX="opencircuit_test_0150gt_"   # scoped prefix; never matches a real agent's database

WORKDIR="$(mktemp -d)"
FAILURES=0
CREATED_DBS=()   # raw databases this script created, for cleanup
BG_PIDS=()

psql_admin() { psql "$PGHOST_URL/postgres" "$@"; }
db_exists() { [ "$(psql_admin -tAc "select 1 from pg_database where datname='$1'" 2>/dev/null)" = "1" ]; }
createdb_raw() { psql_admin -qc "CREATE DATABASE $1;" >/dev/null 2>&1; CREATED_DBS+=("$1"); }
dropdb_raw() { psql_admin -qc "DROP DATABASE IF EXISTS $1;" >/dev/null 2>&1 || true; }

fail() {
  FAILURES=$((FAILURES + 1))
  printf 'FAIL: %s\n' "$1" >&2
}
pass() { printf 'PASS: %s\n' "$1"; }

cleanup() {
  for pid in "${BG_PIDS[@]:-}"; do
    [ -n "$pid" ] && kill "$pid" >/dev/null 2>&1 || true
  done
  for db in "${CREATED_DBS[@]:-}"; do
    [ -n "$db" ] && dropdb_raw "$db"
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
sed 's/^PREFIX="opencircuit_test_"$/PREFIX="opencircuit_test_0150gt_"/' "$REAL_SCRIPT" > "$SCOPED"
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

kill "$CONN_PID" >/dev/null 2>&1 || true
wait "$CONN_PID" 2>/dev/null || true
dropdb_raw "$CONN_DB"
dropdb_raw "$IDLE_DB"

echo "== Part 3: mutation proof — remove the guard, confirm bare gc sweeps again =="

MUTANT="$WORKDIR/testdb_mutant.sh"
sed -e 's/^PREFIX="opencircuit_test_"$/PREFIX="opencircuit_test_0150gt_"/' \
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
# createdb_raw already queued MUT_DB in CREATED_DBS for cleanup; drop is a
# harmless no-op if the mutant already swept it.

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
