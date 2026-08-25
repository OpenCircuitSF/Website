#!/usr/bin/env bash
#
# db_reset_guard_test.sh — guard test for `scripts/db-reset.sh`'s connection
# and name-scope refusals (issue #0207).
#
# #0207: a bare `db-reset.sh` invocation used to pg_terminate_backend EVERY
# other connection to the target database, unconditionally, then drop and
# recreate it — the same shape #0150 fixed for `testdb.sh gc` (a script that
# assumes it's alone is what caused that incident), except this script's
# blast radius is larger: 'opencircuit'/'opencircuit_test' are the databases
# every agent falls back to sharing when it cannot get its own (CLAUDE.md
# §5a), and 'opencircuit' is also the user's own dev database. The old
# 'opencircuit*' name glob also admitted a per-agent scratch database name
# like opencircuit_test_0055, which belongs to scripts/testdb.sh, not here.
#
# What it proves, in order:
#   1. A bare invocation refuses when the target has another live
#      connection, and that connection survives.
#   2. --force performs the reset and terminates the prior connection.
#   3. Mutation proof: with the connection-refusal guard removed, a bare
#      invocation DOES kill the live connection again — i.e. assertion (1)
#      is actually sensitive to the #0207 regression, not vacuously true.
#   4. The pre-existing host, name-prefix, and (new) identifier-charset
#      refusals still hold.
#   5. A per-agent-shaped database name (opencircuit_test_NNNN) is refused
#      without --force.
#   6. $DB is never interpolated unquoted into a DROP/CREATE DATABASE
#      statement in the tracked source.
#   7. (#0250 item 1) The live-connection check fails CLOSED: when psql
#      cannot run the query at all (a bad port here), db-reset.sh refuses —
#      both without --force and, since #0250, WITH --force too.
#   8. (#0250 item 2) psql absence is checked the way migrate's already is:
#      with psql hidden from PATH (migrate and bash still reachable), the
#      script refuses and names psql, rather than compounding item 7's
#      failure by silently reading "no connections".
#   9. (#0250 item 3) A --force flag placed AFTER the database name is
#      honoured, not silently dropped — matching what the connection-refusal
#      message itself tells the user to do.
#  10. (#0250 item 4) An empty database-name argument is rejected rather
#      than silently defaulting to 'opencircuit' via bash's "${1:-default}".
#
# A pg_database CENSUS is taken before this script does anything and again
# after its own cleanup runs (#0250 item 5 — the prior version of this test
# left opencircuit_test_0207gt_demo behind for a reviewer to drop by hand);
# the two must match exactly, or the run reports a leak by name.
#
# SAFETY DESIGN — read this before changing the test
#
# CLAUDE.md §8b and docs/obstacles.md §4 are explicit: a mutation test that
# disables a guard must run against databases this test creates and owns,
# never against a real agent's database or the shared opencircuit/
# opencircuit_test databases, and never by editing the shared tracked script
# file in place while other agents might invoke it.
#
# So this script never runs the REAL scripts/db-reset.sh's `--force` path (or
# its mutated connection-refusal path) against 'opencircuit' or
# 'opencircuit_test' — those are the two names the real script's own
# allowlist accepts, and this test must never touch either. Instead, Parts
# 1-3 run against a private COPY of db-reset.sh with its name allowlist
# WIDENED (by one extra alternative in the same case-arm the real script
# already has) to also accept a single scoped, throwaway database name that
# no real issue id or per-agent scratch database can ever produce:
# 'opencircuit0207gt' — it starts with 'opencircuit' (so it would otherwise
# need --force under the unmodified script) but has no underscore after
# 'opencircuit', so it can never collide with testdb.sh's
# 'opencircuit_test_<id>' naming scheme, and 'gt' (guard test) is not a
# digit sequence any issue number could produce. All destructive work in
# Parts 1-3 is scoped to that one name.
#
# Mutant copies are written into scripts/ itself (untracked, removed by the
# EXIT trap) rather than /tmp: db-reset.sh resolves its own repo root from
# `dirname "${BASH_SOURCE[0]}"/..`, so a copy outside scripts/ resolves REPO
# to the wrong directory and cannot find migrations/ (the same gotcha
# dev_guard_test.sh's header documents for scripts/dev.sh).
#
# The "restore, verify byte-identity with shasum -a 256" requirement in
# #0150's acceptance criteria (this issue follows the same model) is
# satisfied by hashing the real, tracked scripts/db-reset.sh at the start and
# end of this run and asserting they are identical — this test only ever
# reads that file to build private copies elsewhere, never touches it.
#
# Usage: scripts/db_reset_guard_test.sh
# Exit 0 = all guards hold. Exit 1 = a regression was detected (message names it).

set -uo pipefail  # NOT -e: several commands here are *expected* to fail (that's the assertion)

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REAL_SCRIPT="$REPO/scripts/db-reset.sh"
PGHOST_URL="${PGHOST_URL:-postgres://opencircuit:opencircuit@localhost:5432}"
TESTDB="opencircuit0207gt"   # scoped name; no real issue id or per-agent scratch db can ever produce this

WORKDIR="$(mktemp -d)"
FAILURES=0
BG_PIDS=()
MUTANTS=()   # private copies written into scripts/, removed by the EXIT trap

psql_admin() { psql "$PGHOST_URL/postgres" "$@"; }
db_exists() { [ "$(psql_admin -tAc "select 1 from pg_database where datname='$1'" 2>/dev/null)" = "1" ]; }
# #0250 item 5: a leaked-database census. Scoped to 'opencircuit%' so it also
# catches a leak under a name this test never explicitly names (the failure
# shape #0250 itself reports: the OLD version of this test left
# opencircuit_test_0207gt_demo behind without ever asserting anything about
# it).
census() { psql_admin -tAc "select datname from pg_database where datname like 'opencircuit%' order by 1" 2>/dev/null; }

fail() { FAILURES=$((FAILURES + 1)); printf 'FAIL: %s\n' "$1" >&2; }
pass() { printf 'PASS: %s\n' "$1"; }

# See testdb_gc_guard_test.sh's identical comment (#0223): killing the local
# psql client only stops that local process, not the backend it was talking
# to — a backend blocked inside pg_sleep(20) keeps running for as long as it
# has left even after its client is killed, so termination and disconnection
# must both be verified server-side rather than assumed from a client kill.
# shellcheck disable=SC2329  # invoked indirectly via the EXIT trap's cleanup()
terminate_backends() {
  local db="$1"
  psql_admin -tAc "select pg_terminate_backend(pid) from pg_stat_activity where datname='$db' and pid <> pg_backend_pid()" >/dev/null 2>&1 || true
}
# shellcheck disable=SC2329  # invoked indirectly via the EXIT trap's cleanup()
wait_for_disconnect() {
  local db="$1" timeout="${2:-20}" waited=0
  while [ "$waited" -lt "$timeout" ]; do
    local n
    n="$(psql_admin -tAc "select count(*) from pg_stat_activity where datname='$db'" 2>/dev/null)"
    [ "$n" = "0" ] && return 0
    sleep 0.5
    waited=$((waited + 1))
  done
  return 1
}
wait_for_connection() {  # <db> <timeout> — poll until at least one connection shows up
  local db="$1" timeout="${2:-10}" waited=0
  while [ "$waited" -lt "$timeout" ]; do
    [ "$(psql_admin -tAc "select count(*) from pg_stat_activity where datname='$db'" 2>/dev/null)" != "0" ] && return 0
    sleep 0.5
    waited=$((waited + 1))
  done
  return 1
}

# shellcheck disable=SC2329  # invoked indirectly via `trap cleanup EXIT` below
cleanup() {
  for pid in "${BG_PIDS[@]:-}"; do
    [ -n "$pid" ] && kill -9 "$pid" >/dev/null 2>&1 || true
  done
  for pid in "${BG_PIDS[@]:-}"; do
    [ -n "$pid" ] && wait "$pid" 2>/dev/null || true
  done
  terminate_backends "$TESTDB"
  wait_for_disconnect "$TESTDB" 20 || true
  psql_admin -qc "DROP DATABASE IF EXISTS $TESTDB;" >/dev/null 2>&1 || true
  if db_exists "$TESTDB"; then
    echo "warning: cleanup could not confirm $TESTDB was dropped — it may still exist" >&2
  fi
  # #0250 item 5: defensive drop of the per-agent-shaped name Part 5 (and,
  # historically, this test's own leak) targets. Part 5 asserts the real
  # script REFUSES to create it, so under a healthy guard this is a no-op —
  # but if a future regression ever makes that refusal fail, this closes the
  # leak the old version of this test left behind rather than depending on a
  # human to notice and drop it by hand.
  terminate_backends "opencircuit_test_0207gt_demo"
  psql_admin -qc "DROP DATABASE IF EXISTS opencircuit_test_0207gt_demo;" >/dev/null 2>&1 || true
  for m in "${MUTANTS[@]:-}"; do
    [ -n "$m" ] && rm -f "$m"
  done
  rm -rf "$WORKDIR"
}
trap cleanup EXIT

command -v psql >/dev/null    || { echo "error: psql not on PATH" >&2; exit 1; }
command -v migrate >/dev/null || { echo "error: golang-migrate not on PATH" >&2; exit 1; }
command -v shasum >/dev/null  || { echo "error: shasum not on PATH" >&2; exit 1; }
[ -f "$REAL_SCRIPT" ] || { echo "error: $REAL_SCRIPT not found" >&2; exit 1; }

SHA_BEFORE="$(shasum -a 256 "$REAL_SCRIPT" | awk '{print $1}')"
CENSUS_BEFORE="$(census)"

# A private copy with the name allowlist widened to admit TESTDB, via the
# same one-line case-arm the real script uses -- so Parts 1-3 exercise the
# CONNECTION guard specifically, without --force ever being needed for the
# unrelated name-scope guard.
SCOPED="$REPO/scripts/.db_reset_scoped_0207gt.sh"
MUTANTS+=("$SCOPED")
sed "s/opencircuit|opencircuit_test)/opencircuit|opencircuit_test|${TESTDB})/" "$REAL_SCRIPT" > "$SCOPED"
chmod +x "$SCOPED"
if ! grep -q "${TESTDB})" "$SCOPED"; then
  echo "FATAL: name-allowlist widening did not take effect on the scoped copy — aborting before touching any database." >&2
  exit 1
fi

psql_admin -qc "DROP DATABASE IF EXISTS $TESTDB;" >/dev/null 2>&1 || true

echo "== Setup: create $TESTDB via the scoped copy =="
if ! "$SCOPED" --no-seed "$TESTDB" >"$WORKDIR/setup.log" 2>&1; then
  echo "FATAL: setup could not create $TESTDB -- log:" >&2
  cat "$WORKDIR/setup.log" >&2
  exit 1
fi
if db_exists "$TESTDB"; then
  pass "setup created $TESTDB"
else
  fail "FATAL: $TESTDB does not exist after setup"
fi

echo "== Part 1: bare invocation refuses when another connection is live =="
psql "$PGHOST_URL/$TESTDB" -c "select pg_sleep(20)" >/dev/null 2>&1 &
CONN_PID=$!
BG_PIDS+=("$CONN_PID")

if wait_for_connection "$TESTDB" 10; then
  OUT="$("$SCOPED" --no-seed "$TESTDB" 2>&1)"
  RC=$?
  if [ "$RC" -eq 0 ]; then
    fail "REGRESSION #0207: db-reset.sh exited 0 (expected non-zero -- refusal) while $TESTDB had a live connection"
  else
    pass "db-reset.sh refused (exit $RC) while $TESTDB had a live connection"
  fi
  echo "$OUT" | grep -q -- "--force" || fail "the refusal message does not mention --force"
  if kill -0 "$CONN_PID" 2>/dev/null; then
    pass "the live connection survived the refused reset"
  else
    fail "REGRESSION #0207: the live connection did not survive -- it was killed despite the refusal"
  fi
else
  fail "part1 setup: connection never registered in pg_stat_activity"
fi

echo "== Part 2: --force performs the reset and terminates the prior connection =="
if "$SCOPED" --no-seed --force "$TESTDB" >"$WORKDIR/part2.log" 2>&1; then
  pass "--force reset $TESTDB successfully"
else
  fail "--force did not succeed -- log: $(tail -20 "$WORKDIR/part2.log" | tr '\n' '|')"
fi
if kill -0 "$CONN_PID" 2>/dev/null; then
  fail "REGRESSION #0207: --force did not terminate the prior connection"
else
  pass "--force terminated the prior connection"
fi
kill -9 "$CONN_PID" >/dev/null 2>&1 || true
wait "$CONN_PID" 2>/dev/null || true

echo "== Part 3: mutation proof -- remove the refusal, confirm bare invocation kills connections again =="
MUTANT="$REPO/scripts/.db_reset_mutant_0207gt.sh"
MUTANTS+=("$MUTANT")
sed -e "s/opencircuit|opencircuit_test)/opencircuit|opencircuit_test|${TESTDB})/" \
    -e 's/if \[ -n "\$CONNS" \] && \[ "\$FORCE" != "1" \]; then/if false; then/' \
    "$REAL_SCRIPT" > "$MUTANT"
chmod +x "$MUTANT"
if ! grep -q "${TESTDB})" "$MUTANT"; then
  echo "FATAL: name-allowlist widening did not take effect on the mutant -- aborting before running it." >&2
  exit 1
fi
if ! grep -q 'if false; then' "$MUTANT"; then
  echo "FATAL: refusal-removal mutation did not take effect -- aborting rather than running an unmutated bare invocation (that would prove nothing)." >&2
  exit 1
fi

psql "$PGHOST_URL/$TESTDB" -c "select pg_sleep(20)" >/dev/null 2>&1 &
CONN2_PID=$!
BG_PIDS+=("$CONN2_PID")

if wait_for_connection "$TESTDB" 10; then
  "$MUTANT" --no-seed "$TESTDB" >/dev/null 2>&1   # bare -- no --force -- but the refusal guard is neutered
  if kill -0 "$CONN2_PID" 2>/dev/null; then
    fail "mutation was ineffective: with the refusal guard removed, bare invocation still did NOT kill the live connection. This means Part 1's assertions would not actually catch a real #0207 regression -- the guard test is not sensitive."
  else
    pass "with the refusal guard removed, bare invocation DID kill the live connection -- confirms Part 1's assertions are sensitive to the #0207 regression, not vacuously true"
  fi
else
  fail "part3 setup: connection never registered in pg_stat_activity"
fi
kill -9 "$CONN2_PID" >/dev/null 2>&1 || true
wait "$CONN2_PID" 2>/dev/null || true

echo "== Part 4: the pre-existing host, name-prefix, and charset refusals still hold =="
if PGHOST_URL="postgres://opencircuit:opencircuit@example.com:5432" "$REAL_SCRIPT" --force opencircuit >/dev/null 2>&1; then
  fail "REGRESSION: db-reset.sh accepted a non-localhost PGHOST_URL"
else
  pass "a non-localhost PGHOST_URL is still refused"
fi
if "$REAL_SCRIPT" --force notopencircuit >/dev/null 2>&1; then
  fail "REGRESSION: db-reset.sh accepted a database name not starting with 'opencircuit'"
else
  pass "a database name not starting with 'opencircuit' is still refused, even with --force"
fi
if "$REAL_SCRIPT" --force 'opencircuit; DROP DATABASE opencircuit' >/dev/null 2>&1; then
  fail "REGRESSION #0207: db-reset.sh accepted a database name containing characters outside the safe identifier charset"
else
  pass "a database name with unsafe characters is refused, even with --force"
fi

echo "== Part 5: a per-agent-shaped database name needs --force =="
if "$REAL_SCRIPT" opencircuit_test_0207gt_demo >/dev/null 2>&1; then
  fail "REGRESSION #0207: db-reset.sh reset a per-agent-shaped database name (opencircuit_test_0207gt_demo) without --force"
else
  pass "a per-agent-shaped database name is refused without --force"
fi

echo "== Part 6: \$DB is never interpolated unquoted into a DROP/CREATE DATABASE statement =="
if grep -qE 'DATABASE (IF EXISTS )?\$DB\b' "$REAL_SCRIPT"; then
  fail "REGRESSION #0207: scripts/db-reset.sh still interpolates \$DB unquoted into a DROP/CREATE DATABASE statement"
else
  pass "scripts/db-reset.sh does not interpolate \$DB unquoted into DROP/CREATE DATABASE"
fi

echo "== Part 7: the live-connection check fails CLOSED when psql cannot run the query (#0250 item 1) =="
# Bad port on localhost still passes the host check but makes the
# live-connection query itself fail -- exactly the "psql errored" case
# #0250 item 1 describes. Target 'opencircuit_test' (a name the real script
# already accepts without --force): the check under test runs and refuses
# well before anything destructive, so this never touches that database --
# confirmed below by re-checking it still exists and by the overall census.
BAD_PGHOST_URL="postgres://opencircuit:opencircuit@localhost:1"
if PGHOST_URL="$BAD_PGHOST_URL" "$REAL_SCRIPT" opencircuit_test >/dev/null 2>&1; then
  fail "REGRESSION #0250: db-reset.sh proceeded even though its live-connection check's psql query failed (bad port), without --force"
else
  pass "db-reset.sh refuses when the live-connection check's psql query fails (bad port), without --force"
fi
if PGHOST_URL="$BAD_PGHOST_URL" "$REAL_SCRIPT" --force opencircuit_test >/dev/null 2>&1; then
  fail "REGRESSION #0250: db-reset.sh proceeded under --force even though its live-connection check's psql query failed (bad port) -- this must fail closed regardless of --force"
else
  pass "db-reset.sh refuses when the live-connection check's psql query fails (bad port), even WITH --force"
fi
if db_exists "opencircuit_test"; then
  pass "opencircuit_test still exists after Part 7 (never reached, as expected)"
else
  fail "FATAL: opencircuit_test does not exist after Part 7 -- something touched it unexpectedly"
fi

echo "== Part 8: psql presence is checked, the way migrate already is (#0250 item 2) =="
REAL_MIGRATE="$(command -v migrate)"
FAKEPATH="$WORKDIR/fakepath"
mkdir -p "$FAKEPATH"
ln -s "$REAL_MIGRATE" "$FAKEPATH/migrate"
NOPSQL_PATH="$FAKEPATH:/bin:/usr/bin"
# hash -r: this script's own process has already hashed psql's real location
# (every psql_admin call above did that), and a prefix-assigned "PATH=... cmd"
# does not by itself invalidate bash's hash table -- so "command -v psql"
# below would report the stale hashed path and silently pass regardless of
# PATH, proving nothing. The real $REAL_SCRIPT invocation a few lines down is
# unaffected by this (it execs a brand-new bash process per its shebang, which
# starts with an empty hash table), but these two diagnostic checks, run
# in-process, need the table cleared explicitly.
hash -r
if PATH="$NOPSQL_PATH" command -v psql >/dev/null 2>&1; then
  echo "FATAL: psql is reachable under /bin or /usr/bin on this machine -- Part 8's stub PATH does not actually hide it, aborting rather than proving nothing." >&2
  exit 1
fi
if ! PATH="$NOPSQL_PATH" command -v bash >/dev/null 2>&1; then
  echo "FATAL: bash is not reachable under Part 8's stub PATH ($NOPSQL_PATH) -- the script wouldn't even start, aborting rather than proving nothing." >&2
  exit 1
fi
hash -r
OUT8="$(PATH="$NOPSQL_PATH" "$REAL_SCRIPT" opencircuit_test 2>&1)"; RC8=$?
if [ "$RC8" -eq 0 ]; then
  fail "REGRESSION #0250: db-reset.sh proceeded without psql on PATH"
elif echo "$OUT8" | grep -qi 'psql'; then
  pass "db-reset.sh refuses (exit $RC8) and names psql when it is not on PATH"
else
  fail "db-reset.sh refused (exit $RC8) without psql on PATH, but the message doesn't mention psql -- output: $OUT8"
fi

echo "== Part 9: a --force flag placed AFTER the database name is honoured (#0250 item 3) =="
psql "$PGHOST_URL/$TESTDB" -c "select pg_sleep(20)" >/dev/null 2>&1 &
CONN3_PID=$!
BG_PIDS+=("$CONN3_PID")
if wait_for_connection "$TESTDB" 10; then
  if "$SCOPED" --no-seed "$TESTDB" --force >"$WORKDIR/part9.log" 2>&1; then
    pass "a --force flag placed AFTER the database name is honoured (reset succeeded despite a live connection)"
  else
    fail "REGRESSION #0250: --force placed after the database name was not honoured -- log: $(tail -20 "$WORKDIR/part9.log" | tr '\n' '|')"
  fi
  if kill -0 "$CONN3_PID" 2>/dev/null; then
    fail "REGRESSION #0250: --force placed after the database name did not terminate the prior connection"
  else
    pass "--force placed after the database name terminated the prior connection"
  fi
else
  fail "part9 setup: connection never registered in pg_stat_activity"
fi
kill -9 "$CONN3_PID" >/dev/null 2>&1 || true
wait "$CONN3_PID" 2>/dev/null || true

echo "== Part 10: an empty database-name argument is rejected, not defaulted to 'opencircuit' (#0250 item 4) =="
if "$REAL_SCRIPT" --force '' >/dev/null 2>&1; then
  fail "REGRESSION #0250: db-reset.sh accepted an empty database-name argument (it must be rejected, not fall through to the 'opencircuit' default)"
else
  pass "an empty database-name argument is refused rather than silently defaulting to 'opencircuit'"
fi

echo
echo "== Final cleanup and leaked-database census (#0250 item 5) =="
terminate_backends "$TESTDB"
wait_for_disconnect "$TESTDB" 20 || true
psql_admin -qc "DROP DATABASE IF EXISTS $TESTDB;" >/dev/null 2>&1 || true
terminate_backends "opencircuit_test_0207gt_demo"
psql_admin -qc "DROP DATABASE IF EXISTS opencircuit_test_0207gt_demo;" >/dev/null 2>&1 || true
CENSUS_AFTER="$(census)"
if [ "$CENSUS_BEFORE" = "$CENSUS_AFTER" ]; then
  pass "pg_database census (datname like 'opencircuit%') unchanged before/after this run: $(printf '%s' "$CENSUS_BEFORE" | tr '\n' ' ')"
else
  fail "REGRESSION #0250: pg_database census changed -- a database was leaked or removed. before=[$(printf '%s' "$CENSUS_BEFORE" | tr '\n' ' ')] after=[$(printf '%s' "$CENSUS_AFTER" | tr '\n' ' ')]"
fi

echo
echo "== Byte-identity check =="
SHA_AFTER="$(shasum -a 256 "$REAL_SCRIPT" | awk '{print $1}')"
if [ "$SHA_BEFORE" = "$SHA_AFTER" ]; then
  pass "scripts/db-reset.sh unchanged across the run (sha256 $SHA_AFTER) -- all mutation happened on private copies in scripts/, never the tracked file"
else
  fail "CRITICAL: scripts/db-reset.sh's sha256 changed during this test run ($SHA_BEFORE -> $SHA_AFTER). Investigate immediately with: git diff -- scripts/db-reset.sh"
fi

echo
if [ "$FAILURES" -eq 0 ]; then
  echo "db_reset_guard_test.sh: all guards hold (0 failures)"
  exit 0
else
  echo "db_reset_guard_test.sh: $FAILURES failure(s) -- see FAIL lines above"
  exit 1
fi
