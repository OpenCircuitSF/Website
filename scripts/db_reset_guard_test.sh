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
#  11. (#0309) A non-client backend (a parallel worker, standing in for
#      autovacuum, which cannot be constructed on demand) attached to the
#      target does not fail the run. 11b statically pins that $CLIENT_WHERE
#      (what may be TERMINATED) and $HOLDER_WHERE (what COUNTS as a holder
#      for the refusal decision) are two deliberately different fragments,
#      each defined once and referenced by name at their sites.
#  12. (#0309 review remedy item 3) A foreign-role holder — a live client
#      session authenticated as a role other than this script's own — is
#      still refused without --force, and still reported. This is the
#      regression case the first #0309 attempt broke: sharing one fragment
#      between the terminate step and the refusal check made the refusal
#      check blind to any role but its own.
#  13. (#0309 review remedy item 4) When the second login role discovered
#      for #12 is also a superuser, the exact SUPERUSER-only permission
#      error #0309 diagnosed is reproduced on demand against a mutant that
#      reverts the terminate step to an unscoped query, and shown NOT to
#      occur against the real, narrowed $CLIENT_WHERE.
#
# Both 12 and 13 discover their second login role at runtime and skip
# cleanly, without failing the run, if none is reachable — this must not
# become a machine-specific test.
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
# no real issue id or per-agent scratch database can ever produce: TESTDB
# (defined below as "opencircuit${RUNID}gt", e.g. 'opencircuit0207gt' when
# RUNID happens to be '0207gt') — it starts with 'opencircuit' (so it would
# otherwise need --force under the unmodified script) but has no underscore
# after 'opencircuit', so it can never collide with testdb.sh's
# 'opencircuit_test_<id>' naming scheme, and RUNID (#0253: derived per run
# from ISSUE, falling back to $$) is not a digit sequence any real issue
# number could collide with while a concurrent run is in flight. All
# destructive work in Parts 1-3 is scoped to that one name.
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

# #0253: TESTDB, the demo-db name, and the private mutant/scoped script paths
# below all used to be fixed, so two concurrent runs of this script collided
# on all of them — this project's implementer hit exactly that collision
# while working #0250/#0251. Namespace per run using the project's existing
# ISSUE convention (falling back to $$, the same convention #0247 used for
# the restore drill) rather than inventing a second scheme. Lower-case and
# strip to alnum-only: #0208 fixed testdb.sh's mixed-case identifier fold,
# and TESTDB is interpolated into raw SQL identifiers the same way, so a new
# call site here must not reintroduce it — and stripping to alnum-only (no
# underscore) preserves the invariant the comment below relies on: TESTDB
# starts with 'opencircuit' but has no underscore right after it, so it can
# never collide with testdb.sh's 'opencircuit_test_<id>' naming scheme.
RUNID_RAW="${ISSUE:-$$}"
RUNID="$(printf '%s' "$RUNID_RAW" | tr -cd '[:alnum:]' | tr '[:upper:]' '[:lower:]')"
[ -n "$RUNID" ] || { echo "error: empty run id (derived from ISSUE=$RUNID_RAW)" >&2; exit 1; }
TESTDB="opencircuit${RUNID}gt"   # scoped name; no real issue id or per-agent scratch db can ever produce this
DEMODB="opencircuit_test_${RUNID}gt_demo"   # Part 5's per-agent-shaped name, now namespaced too

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
# it) — #0253 additionally scopes it to names containing this run's own
# RUNID, so a concurrent run's own scratch databases (each with a different
# RUNID) coming and going during this run's before/after window cannot make
# this census spuriously disagree. Every database this test creates embeds
# RUNID by construction (TESTDB, DEMODB), so nothing this run could leak
# falls outside this pattern.
census() { psql_admin -tAc "select datname from pg_database where datname like 'opencircuit%' and datname like '%${RUNID}%' order by 1" 2>/dev/null; }

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
# Parts 12/13 hold a foreign-role (possibly superuser) session open, and
# psql_admin (this script's own role, opencircuit) cannot pg_terminate_backend
# a superuser-owned one -- that permission failure is the exact #0309 bug
# under test, so terminate_backends() cannot be used to clean these up. A
# role can always terminate its OWN other backends regardless of superuser
# status, so open a one-off connection AS that same role and have it signal
# itself. Note this is a genuinely separate connection attempt, not the held
# one -- it fails harmlessly (|| true) if that role turns out unreachable.
terminate_as_role() {
  local role="$1" db="$2"
  psql "postgres://${role}@localhost:5432/postgres" -tAc "select pg_terminate_backend(pid) from pg_stat_activity where datname='$db' and usename='$role' and pid <> pg_backend_pid()" >/dev/null 2>&1 || true
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
  terminate_backends "$DEMODB"
  psql_admin -qc "DROP DATABASE IF EXISTS $DEMODB;" >/dev/null 2>&1 || true
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
SCOPED="$REPO/scripts/.db_reset_scoped_${RUNID}.sh"
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
MUTANT="$REPO/scripts/.db_reset_mutant_${RUNID}.sh"
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
if "$REAL_SCRIPT" "$DEMODB" >/dev/null 2>&1; then
  fail "REGRESSION #0207: db-reset.sh reset a per-agent-shaped database name ($DEMODB) without --force"
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

echo "== Part 11: a non-client backend attached to the target does not fail the run (#0309) =="
# #0309: the found bug was an unscoped terminate query catching a
# non-'client backend' row (most plausibly autovacuum) attached to the
# target database -- pg_terminate_backend() on such a row raises a
# SUPERUSER-only permission error that, inside a single SELECT under
# `set -e`, aborted the whole reset. A REAL autovacuum worker cannot be
# constructed on demand (it attaches on its own schedule, not this test's).
# A Postgres PARALLEL WORKER can: it is a genuine, deterministic
# non-'client backend' row in pg_stat_activity, attached to a specific
# database, spawned the moment a query needs it -- no special privileges,
# no fixture, just parallel_setup_cost/parallel_tuple_cost driven to 0 and a
# join fanout over generate_series() to give a small table enough rows to
# scan that a handful of workers stay busy for a few seconds.
#
# What this DOES prove: with a real, live, non-'client backend' row
# attached to $TESTDB throughout the run, db-reset.sh --force still
# completes successfully -- i.e. the `backend_type = 'client backend'`
# narrowing in $CLIENT_WHERE is exercised against an actual row in
# pg_stat_activity, not just read as source text.
#
# What it does NOT prove: independently verified (outside this test, see
# the #0309 report) that pg_terminate_backend() on a parallel worker owned
# by the SAME role the query ran as succeeds without error -- parallel
# workers are not SUPERUSER-restricted the way autovacuum and other
# background workers are. So this part cannot reproduce the exact
# SUPERUSER-permission error #0309 found, and does NOT by itself show that
# $CLIENT_WHERE's narrowing is what stands between a live row and that
# error -- a review of this issue ran this same construction against the
# UNFIXED parent commit and got exit 0 with no "superuser" in the output,
# so neither of Part 11's two core assertions is sensitive to the terminate
# narrowing (criterion 1); only the report assertion below is, and it is
# sensitive to criterion 2's reporting, not criterion 1. Part 11b pins the
# narrowed query's source text as a second, independent static check that
# does not depend on constructing a live row at all. Part 13 below supplies
# the genuinely criterion-1-sensitive case this part cannot: a foreign
# SUPERUSER-owned CLIENT backend, which the unscoped terminate query really
# did choke on and $CLIENT_WHERE really does exclude.
psql "$PGHOST_URL/$TESTDB" -qc "CREATE TABLE IF NOT EXISTS pw_seed AS SELECT g, md5(g::text) h FROM generate_series(1,500000) g;" >/dev/null 2>&1
psql "$PGHOST_URL/$TESTDB" -qc "ANALYZE pw_seed;" >/dev/null 2>&1
(
  psql "$PGHOST_URL/$TESTDB" >/dev/null 2>&1 <<'SQL'
SET min_parallel_table_scan_size = 0;
SET parallel_setup_cost = 0;
SET parallel_tuple_cost = 0;
SET max_parallel_workers_per_gather = 4;
SELECT count(*) FROM pw_seed, generate_series(1,150) i WHERE md5(pw_seed.h || i::text) LIKE 'zzzz%';
SQL
) &
PW_LEADER_PID=$!
BG_PIDS+=("$PW_LEADER_PID")

WORKER_PID=""
for i in $(seq 1 40); do
  WORKER_PID="$(psql_admin -tAc "select pid from pg_stat_activity where datname='$TESTDB' and backend_type <> 'client backend' limit 1" 2>/dev/null)"
  [ -n "$WORKER_PID" ] && break
  sleep 0.2
done

if [ -z "$WORKER_PID" ]; then
  fail "part11 setup: no non-client backend (parallel worker) ever appeared for $TESTDB -- this machine/Postgres build may not spawn parallel workers under these settings; Part 11b's static pin still ran"
else
  pass "a non-client backend (pid $WORKER_PID, backend_type <> 'client backend') is attached to $TESTDB"
  OUT11="$("$SCOPED" --no-seed --force "$TESTDB" 2>&1)"
  RC11=$?
  if [ "$RC11" -eq 0 ]; then
    pass "db-reset.sh --force succeeded while a non-client backend was attached to $TESTDB"
  else
    fail "REGRESSION #0309: db-reset.sh failed (exit $RC11) while a non-client backend was attached -- output: $(echo "$OUT11" | tr '\n' '|')"
  fi
  if echo "$OUT11" | grep -qi 'superuser'; then
    fail "REGRESSION #0309: db-reset.sh's output mentions a superuser permission error -- the narrowed query is targeting a backend it should not"
  else
    pass "no superuser-permission error appeared in db-reset.sh's output"
  fi
  if echo "$OUT11" | grep -q 'background backend'; then
    pass "db-reset.sh reported the non-client backend (criterion 2) rather than silently ignoring or choking on it"
  else
    fail "db-reset.sh did not report the non-client backend that was attached during the reset (best-effort: the worker may have already finished by the time the report query ran -- see the timing note in the #0309 report)"
  fi
fi
kill -9 "$PW_LEADER_PID" >/dev/null 2>&1 || true
wait "$PW_LEADER_PID" 2>/dev/null || true

echo "== Part 11b: \$CLIENT_WHERE and \$HOLDER_WHERE are two distinct fragments, each defined once and used only at their own site (#0309) =="
# The static counterpart to Part 11 and 12, and the fallback the issue asked
# for if a live non-client backend could not be constructed at all: reads
# the TRACKED source directly (not a copy of expected text stored in this
# test -- see CLAUDE.md's GUARD-0208 note on why an oracle must not be the
# same bytes as its subject).
#
# This is where the FIRST #0309 attempt actually broke, per the review on
# this issue: it shared one fragment ($CLIENT_WHERE, narrowed by
# usename = current_user) between the terminate step AND the
# connection-refusal check. Narrowing what may be TERMINATED is safe;
# narrowing what COUNTS AS A HOLDER is not -- it makes the refusal check
# blind to any live session authenticated as a role other than this
# script's own, including the user's own interactive psql. So this part
# pins that the two fragments are genuinely different, not just that one
# of them exists.
CLIENT_WHERE_DEFS="$(grep -c '^CLIENT_WHERE=' "$REAL_SCRIPT")"
if [ "$CLIENT_WHERE_DEFS" = "1" ]; then
  pass "\$CLIENT_WHERE is defined exactly once in scripts/db-reset.sh"
else
  fail "REGRESSION #0309: expected exactly one \$CLIENT_WHERE definition, found $CLIENT_WHERE_DEFS -- a second, independently-spelled copy could drift unnarrowed"
fi
CLIENT_WHERE_LINE="$(grep '^CLIENT_WHERE=' "$REAL_SCRIPT")"
if echo "$CLIENT_WHERE_LINE" | grep -q "backend_type = 'client backend'" && echo "$CLIENT_WHERE_LINE" | grep -q "usename = current_user"; then
  pass "\$CLIENT_WHERE's definition narrows by both backend_type='client backend' and usename=current_user"
else
  fail "REGRESSION #0309: \$CLIENT_WHERE's definition no longer narrows by both backend_type='client backend' and usename=current_user -- found: $CLIENT_WHERE_LINE"
fi
if grep -qE 'pg_terminate_backend\(pid\) FROM pg_stat_activity WHERE \$CLIENT_WHERE' "$REAL_SCRIPT"; then
  pass "the terminate step references \$CLIENT_WHERE, not an independent unscoped query"
else
  fail "REGRESSION #0309: the terminate step no longer references \$CLIENT_WHERE -- it may have reverted to an unscoped query"
fi

HOLDER_WHERE_DEFS="$(grep -c '^HOLDER_WHERE=' "$REAL_SCRIPT")"
if [ "$HOLDER_WHERE_DEFS" = "1" ]; then
  pass "\$HOLDER_WHERE is defined exactly once in scripts/db-reset.sh"
else
  fail "REGRESSION #0309: expected exactly one \$HOLDER_WHERE definition, found $HOLDER_WHERE_DEFS"
fi
HOLDER_WHERE_LINE="$(grep '^HOLDER_WHERE=' "$REAL_SCRIPT")"
if echo "$HOLDER_WHERE_LINE" | grep -q "usename is not null"; then
  pass "\$HOLDER_WHERE's definition narrows by usename is not null"
else
  fail "REGRESSION #0309: \$HOLDER_WHERE's definition no longer narrows by usename is not null -- found: $HOLDER_WHERE_LINE"
fi
if echo "$HOLDER_WHERE_LINE" | grep -q "usename = current_user"; then
  fail "REGRESSION #0309: \$HOLDER_WHERE's definition carries usename = current_user -- this is the exact defect the review bounced: it would make the refusal check blind to any live session authenticated as a foreign role -- found: $HOLDER_WHERE_LINE"
else
  pass "\$HOLDER_WHERE's definition does NOT narrow by usename = current_user (that predicate belongs only to \$CLIENT_WHERE, the terminate-step fragment)"
fi
if grep -qE 'from pg_stat_activity where \$HOLDER_WHERE' "$REAL_SCRIPT"; then
  pass "the connection-refusal check references \$HOLDER_WHERE"
else
  fail "REGRESSION #0309: the connection-refusal check no longer references \$HOLDER_WHERE -- it may have reverted to \$CLIENT_WHERE (the exact regression this issue bounced on) or an independent query"
fi
if grep -qE 'from pg_stat_activity where \$CLIENT_WHERE' "$REAL_SCRIPT"; then
  fail "REGRESSION #0309: the connection-refusal check references \$CLIENT_WHERE -- narrowing the refusal check by the terminate step's OWN-ROLE predicate is exactly what this issue bounced on"
else
  pass "the connection-refusal check does not reference \$CLIENT_WHERE"
fi

echo
echo "== Part 12: a foreign-role holder is refused without --force, and reported (#0309 review remedy item 3) =="
# Parts 1/2/3/5/9 all connect as this script's OWN role, so none of them can
# see the class of failure this issue bounced on: a live client session
# authenticated as a DIFFERENT role. Discover a second login role at
# runtime -- order by rolsuper desc so that, when one exists, the SAME role
# also lets Part 13 below reproduce the exact SUPERUSER error #0309
# diagnosed from one fixture, per the review's remedy. Skip cleanly,
# without failing this run, when no second role is reachable over trust
# auth with no password -- this must not become a machine-specific test.
SECOND_ROLE="$(psql_admin -tAc "select rolname from pg_roles where rolcanlogin and rolname <> current_user order by rolsuper desc, rolname limit 1" 2>/dev/null)"
SECOND_ROLE_OK=0
SECOND_ROLE_SUPER=0
if [ -n "$SECOND_ROLE" ]; then
  if PGCONNECT_TIMEOUT=5 psql "postgres://${SECOND_ROLE}@localhost:5432/postgres" -tAc "select 1" >/dev/null 2>&1; then
    SECOND_ROLE_OK=1
    [ "$(psql_admin -tAc "select rolsuper from pg_roles where rolname = '$SECOND_ROLE'" 2>/dev/null)" = "t" ] && SECOND_ROLE_SUPER=1
  fi
fi

if [ "$SECOND_ROLE_OK" != "1" ]; then
  echo "SKIP: no second login role reachable without a password over trust auth (discovered: ${SECOND_ROLE:-<none>}) -- Part 12 and Part 13 need one and cannot run on this machine. This is not a failure: it means this machine cannot exercise the foreign-role class of regression, not that the fix is unverified."
else
  psql "postgres://${SECOND_ROLE}@localhost:5432/$TESTDB" -c "select pg_sleep(20)" >/dev/null 2>&1 &
  CONN4_PID=$!
  BG_PIDS+=("$CONN4_PID")
  if wait_for_connection "$TESTDB" 10; then
    OUT12="$("$SCOPED" --no-seed "$TESTDB" 2>&1)"; RC12=$?
    if [ "$RC12" -eq 0 ]; then
      fail "REGRESSION #0309: db-reset.sh exited 0 (expected non-zero -- refusal) while $TESTDB had a live connection from foreign role '$SECOND_ROLE'"
    else
      pass "db-reset.sh refused (exit $RC12) while $TESTDB had a live connection from foreign role '$SECOND_ROLE'"
    fi
    if echo "$OUT12" | grep -q -- "refusing:" && echo "$OUT12" | grep -q "$SECOND_ROLE"; then
      pass "the refusal names the foreign-role holder ('$SECOND_ROLE')"
    else
      fail "REGRESSION #0309: the refusal did not name the foreign-role holder -- output: $(echo "$OUT12" | tr '\n' '|')"
    fi
    if kill -0 "$CONN4_PID" 2>/dev/null; then
      pass "the foreign-role connection survived the refused reset"
    else
      fail "REGRESSION #0309: the foreign-role connection did not survive -- it was killed despite the refusal"
    fi
  else
    fail "part12 setup: foreign-role connection never registered in pg_stat_activity"
  fi
  kill -9 "$CONN4_PID" >/dev/null 2>&1 || true
  wait "$CONN4_PID" 2>/dev/null || true
  # The held session is inside pg_sleep(20) and never polls its socket, so
  # killing the local client above does not end it server-side (see
  # terminate_backends' note). Part 13 immediately resets $TESTDB, so
  # terminate it for real -- as the SAME role, since this script's own role
  # cannot pg_terminate_backend a superuser-owned one -- and wait for the
  # server side to actually clear rather than racing it.
  terminate_as_role "$SECOND_ROLE" "$TESTDB"
  wait_for_disconnect "$TESTDB" 20 || true
fi

echo
echo "== Part 13: the exact SUPERUSER error is reproduced against an unscoped mutant, and absent against the real \$CLIENT_WHERE (#0309 review remedy item 4) =="
if [ "$SECOND_ROLE_OK" != "1" ] || [ "$SECOND_ROLE_SUPER" != "1" ]; then
  echo "SKIP: no second SUPERUSER login role reachable (discovered: ${SECOND_ROLE:-<none>}, reachable: $SECOND_ROLE_OK, superuser: $SECOND_ROLE_SUPER) -- the exact SUPERUSER-only error #0309 diagnosed can only be constructed against a superuser-owned backend. Part 12's foreign-role holder coverage above still stands regardless."
else
  # Build a mutant: widen the name allowlist for TESTDB (as with $SCOPED),
  # and revert ONLY the terminate step's $CLIENT_WHERE reference back to the
  # UNSCOPED pre-#0309 query -- the connection-refusal check and
  # $HOLDER_WHERE are left untouched, isolating criterion 1 specifically.
  CLIENT_TERM_OLD="FROM pg_stat_activity WHERE \$CLIENT_WHERE;\" >/dev/null"
  CLIENT_TERM_NEW="FROM pg_stat_activity WHERE datname = '\$DB' AND pid <> pg_backend_pid();\" >/dev/null"
  MUTANT2="$REPO/scripts/.db_reset_mutant2_${RUNID}.sh"
  MUTANTS+=("$MUTANT2")
  CONTENT="$(cat "$REAL_SCRIPT")"
  CONTENT="${CONTENT//opencircuit|opencircuit_test)/opencircuit|opencircuit_test|${TESTDB})}"
  if [[ "$CONTENT" != *"$CLIENT_TERM_OLD"* ]]; then
    echo "FATAL: could not locate the terminate step's \$CLIENT_WHERE reference to mutate -- aborting before running Part 13." >&2
    exit 1
  fi
  CONTENT="${CONTENT//$CLIENT_TERM_OLD/$CLIENT_TERM_NEW}"
  printf '%s\n' "$CONTENT" > "$MUTANT2"
  chmod +x "$MUTANT2"
  if ! grep -q "${TESTDB})" "$MUTANT2"; then
    echo "FATAL: name-allowlist widening did not take effect on the criterion-1 mutant -- aborting before running it." >&2
    exit 1
  fi
  if grep -qE 'WHERE \$CLIENT_WHERE;" >/dev/null' "$MUTANT2"; then
    echo "FATAL: the terminate-step mutation did not take effect -- aborting rather than running an unmutated invocation (that would prove nothing)." >&2
    exit 1
  fi

  # Reset TESTDB cleanly via the real (fixed) scoped copy first, so this
  # part starts from a known-good state whatever Part 12 left behind.
  psql_admin -qc "DROP DATABASE IF EXISTS $TESTDB;" >/dev/null 2>&1 || true
  if ! "$SCOPED" --no-seed "$TESTDB" >"$WORKDIR/part13setup.log" 2>&1; then
    echo "FATAL: could not recreate $TESTDB before Part 13 -- log:" >&2
    cat "$WORKDIR/part13setup.log" >&2
    exit 1
  fi

  psql "postgres://${SECOND_ROLE}@localhost:5432/$TESTDB" -c "select pg_sleep(20)" >/dev/null 2>&1 &
  CONN5_PID=$!
  BG_PIDS+=("$CONN5_PID")
  if wait_for_connection "$TESTDB" 10; then
    OUT13A="$("$MUTANT2" --no-seed --force "$TESTDB" 2>&1)"; RC13A=$?
    if [ "$RC13A" -eq 0 ]; then
      fail "part13: the unscoped-terminate mutant exited 0 against a foreign superuser-owned client backend -- expected it to reproduce #0309's SUPERUSER error"
    elif echo "$OUT13A" | grep -qi 'superuser'; then
      pass "criterion-1-sensitive: reverting \$CLIENT_WHERE's terminate-step reference to an unscoped query reproduces the #0309 SUPERUSER-only permission error against a foreign superuser-owned client backend (role '$SECOND_ROLE')"
    else
      fail "part13: the unscoped-terminate mutant failed (exit $RC13A) but did not mention 'superuser' -- output: $(echo "$OUT13A" | tr '\n' '|')"
    fi
  else
    fail "part13 setup: foreign superuser-role connection never registered in pg_stat_activity before the mutant run"
  fi
  kill -9 "$CONN5_PID" >/dev/null 2>&1 || true
  wait "$CONN5_PID" 2>/dev/null || true
  # The mutant's unscoped terminate step errored on this very row (that is
  # the assertion above), so it did NOT actually end the session -- clean up
  # for real before the second sub-test below, same reasoning as Part 12.
  terminate_as_role "$SECOND_ROLE" "$TESTDB"
  wait_for_disconnect "$TESTDB" 20 || true

  # Same scenario against the REAL, fixed $CLIENT_WHERE: this must NOT
  # reproduce the superuser error. It is still expected to fail overall --
  # Postgres itself blocks the DROP because the foreign superuser session
  # is never terminated ($CLIENT_WHERE only ever targets this script's OWN
  # role, by design, and correctly leaves a superuser-owned session alone
  # rather than trying and failing to sign it). What this asserts is the
  # ABSENCE of the superuser error specifically, not the exit code.
  psql "postgres://${SECOND_ROLE}@localhost:5432/$TESTDB" -c "select pg_sleep(20)" >/dev/null 2>&1 &
  CONN6_PID=$!
  BG_PIDS+=("$CONN6_PID")
  if wait_for_connection "$TESTDB" 10; then
    OUT13B="$("$SCOPED" --no-seed --force "$TESTDB" 2>&1)"
    if echo "$OUT13B" | grep -qi 'superuser'; then
      fail "REGRESSION #0309: db-reset.sh's real, narrowed \$CLIENT_WHERE terminate step produced a superuser-permission error against a foreign superuser-owned client backend -- the narrowing regressed"
    else
      pass "the real, narrowed \$CLIENT_WHERE terminate step does NOT reproduce the superuser error against the same foreign superuser-owned client backend it was run against above -- \$CLIENT_WHERE's narrowing is what stands between this scenario and #0309's bug"
    fi
  else
    fail "part13 setup: foreign superuser-role connection never registered in pg_stat_activity before the fixed-script run"
  fi
  kill -9 "$CONN6_PID" >/dev/null 2>&1 || true
  wait "$CONN6_PID" 2>/dev/null || true
  # $CLIENT_WHERE correctly never targeted this row either (that is the
  # point of the assertion above), so it is still connected -- clean up for
  # real before Final cleanup below tries to drop $TESTDB.
  terminate_as_role "$SECOND_ROLE" "$TESTDB"
  wait_for_disconnect "$TESTDB" 20 || true
fi

echo
echo "== Final cleanup and leaked-database census (#0250 item 5) =="
terminate_backends "$TESTDB"
wait_for_disconnect "$TESTDB" 20 || true
psql_admin -qc "DROP DATABASE IF EXISTS $TESTDB;" >/dev/null 2>&1 || true
terminate_backends "$DEMODB"
psql_admin -qc "DROP DATABASE IF EXISTS $DEMODB;" >/dev/null 2>&1 || true
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
