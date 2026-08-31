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

echo "== Part 5: the anchored '_template\$' exclusion is neither too broad nor too narrow, and its two clauses are independently load-bearing (#0332) =="
#
# #0332: #0327's third exclusion clause was `not ilike '%template%'` — an
# UNANCHORED substring match. Any scratch database whose name merely
# CONTAINS "template" anywhere was invisible to BOTH of gc's paths (not
# swept by --all, not even named in the bare refusal listing), which is a
# silent leak on a pool #0315 explicitly teaches agents to create
# "..._template"-suffixed scratch databases in. The fix anchors the pattern
# to a `_template` SUFFIX via a POSIX regex end-anchor (`!~* '_template$'`),
# which protects the naming shape #0315 actually teaches without swallowing
# every name containing the word.
#
# This part proves three things, using the real script (via $SCOPED, no
# TEMPLATE_DB override needed):
#   1. A leak-shaped database (contains "template", does not END in
#      "_template" — #0332's own two motivating examples) IS swept, not
#      left invisible to both gc paths.
#   2. A genuinely template-suffixed database is still protected.
#   3. #0327's two added exclusion clauses (DEFAULT_TEMPLATE, the anchored
#      pattern) are independently load-bearing, not just load-bearing
#      together — the #0327 review's noted weakness, closed by mutating each
#      alone rather than only both at once (as Part 4's MUTANT2 does).

LEAK_CONTAINS="${TESTPREFIX}template_probe"   # contains "template" mid-string — #0332's own example
LEAK_SUFFIXLESS="${TESTPREFIX}0332template"   # ends in "...template" but with NO underscore separator
PROTECTED_SUFFIX="${TESTPREFIX}genuine_template"  # ends in "_template" — must stay protected

createdb_raw "$LEAK_CONTAINS"
createdb_raw "$LEAK_SUFFIXLESS"
createdb_raw "$PROTECTED_SUFFIX"

"$SCOPED" gc --all >/dev/null 2>&1

if db_exists "$LEAK_CONTAINS"; then
  fail "REGRESSION #0332: gc --all left $LEAK_CONTAINS (contains 'template' but does not end in '_template') unswept — exactly the leak #0332 exists to fix"
else
  pass "gc --all swept $LEAK_CONTAINS, a scratch database that merely contains 'template'"
fi

if db_exists "$LEAK_SUFFIXLESS"; then
  fail "REGRESSION #0332: gc --all left $LEAK_SUFFIXLESS (ends in '...template' with no separator) unswept"
else
  pass "gc --all swept $LEAK_SUFFIXLESS"
fi

if db_exists "$PROTECTED_SUFFIX"; then
  pass "gc --all still did not drop $PROTECTED_SUFFIX, a genuine '..._template'-suffixed database — the anchor did not over-correct"
else
  fail "gc --all dropped $PROTECTED_SUFFIX — the anchored exclusion is too narrow, over-correcting #0332's fix"
fi

drop_and_verify "$LEAK_CONTAINS"
drop_and_verify "$LEAK_SUFFIXLESS"
drop_and_verify "$PROTECTED_SUFFIX"

# --- Mutation proof: reverting ONLY the anchor to the old unanchored shape
# reproduces the #0332 leak, confirming the assertions above are sensitive
# to it and not vacuously true.
MUTANT_UNANCHOR="$WORKDIR/testdb_mutant_unanchor.sh"
sed -e "s/^PREFIX=\"opencircuit_test_\"\$/PREFIX=\"${TESTPREFIX}\"/" \
    -e "s/and datname !~\* '_template\\\\\$'/and datname not ilike '%template%'/" \
    "$REAL_SCRIPT" > "$MUTANT_UNANCHOR"
chmod +x "$MUTANT_UNANCHOR"

if ! grep -q "^PREFIX=\"${TESTPREFIX}\"\$" "$MUTANT_UNANCHOR"; then
  echo "FATAL: prefix-scoping of the unanchor mutant failed — aborting before running it." >&2
  exit 1
fi
# Exact full-line match on the exclude_clause= assignment itself, not a bare
# substring — this file's own comments above describe both the old and new
# shapes in prose, so a substring check ("not ilike '%template%'") would
# match those comments even when the CODE line was never touched, making the
# check vacuous. §8: the oracle must not be satisfiable by bytes other than
# the subject it is meant to be checking.
if ! grep -Fxq "    exclude_clause=\"datname <> '\$TEMPLATE' and datname <> '\$DEFAULT_TEMPLATE' and datname not ilike '%template%'\"" "$MUTANT_UNANCHOR"; then
  echo "FATAL: #0332 anchor-removal mutation did not take effect — aborting rather than run an unmutated gc --all (that would prove nothing)." >&2
  exit 1
fi

MUT_LEAK="${TESTPREFIX}template_probe2"
createdb_raw "$MUT_LEAK"
"$MUTANT_UNANCHOR" gc --all >/dev/null 2>&1
if db_exists "$MUT_LEAK"; then
  pass "with the anchor reverted to the old unanchored '%template%' shape, gc --all left $MUT_LEAK unswept — confirms the leak-case assertions above are sensitive to the #0332 regression, not vacuously true"
else
  fail "mutation was ineffective: reverting to the old unanchored pattern still swept $MUT_LEAK. This means the leak-case assertions above would not actually catch a real #0332 regression."
fi
drop_and_verify "$MUT_LEAK"

# --- Separating #0327's two added clauses (the review's noted weakness):
# removing DEFAULT_TEMPLATE and the anchored pattern TOGETHER (Part 4's
# MUTANT2) cannot show which one is load-bearing, since the anchored
# pattern's own definition (PREFIX + "template", and PREFIX always ends in
# "_") means it always ALSO ends in "_template" and so is always ALSO
# caught by the pattern clause. Mutating each alone shows this precisely:
# the pattern clause is independently necessary (protects an ad hoc
# "..._template" scratch database that is neither $TEMPLATE nor
# $DEFAULT_TEMPLATE); DEFAULT_TEMPLATE is independently redundant *given*
# the anchored pattern for the one name it protects, which is why it is
# still kept — a static, cheap, independent safeguard that does not depend
# on the pattern's specific shape or on PREFIX continuing to end in "_".

# Mutant A: remove ONLY the anchored pattern clause, keep DEFAULT_TEMPLATE.
MUTANT_NOPATTERN="$WORKDIR/testdb_mutant_nopattern.sh"
sed -e "s/^PREFIX=\"opencircuit_test_\"\$/PREFIX=\"${TESTPREFIX}\"/" \
    -e "s/ and datname !~\* '_template\\\\\$'//" \
    "$REAL_SCRIPT" > "$MUTANT_NOPATTERN"
chmod +x "$MUTANT_NOPATTERN"

if ! grep -q "^PREFIX=\"${TESTPREFIX}\"\$" "$MUTANT_NOPATTERN"; then
  echo "FATAL: prefix-scoping of the no-pattern mutant failed — aborting." >&2
  exit 1
fi
# Exact full-line match (see the #0332 note above MUTANT_UNANCHOR's check for
# why a bare substring like "!~*" is not safe here — this script's own
# comments contain that text too).
if ! grep -Fxq "    exclude_clause=\"datname <> '\$TEMPLATE' and datname <> '\$DEFAULT_TEMPLATE'\"" "$MUTANT_NOPATTERN"; then
  echo "FATAL: #0332 pattern-only-removal mutation did not take effect as expected (exclude_clause line does not match 'TEMPLATE + DEFAULT_TEMPLATE only') — aborting rather than run it." >&2
  exit 1
fi

NP_DEFAULTISH="${TESTPREFIX}template"            # exactly $DEFAULT_TEMPLATE's value under this prefix
NP_ADHOC="${TESTPREFIX}someones_own_template"    # ad hoc #0315-style scratch template, NOT the literal default
createdb_raw "$NP_DEFAULTISH"
createdb_raw "$NP_ADHOC"

"$MUTANT_NOPATTERN" gc --all >/dev/null 2>&1

if db_exists "$NP_DEFAULTISH"; then
  pass "with ONLY the anchored pattern removed, gc --all still did not drop the literal default template ($NP_DEFAULTISH) — DEFAULT_TEMPLATE alone protects it"
else
  fail "REGRESSION: with only the anchored pattern removed, gc --all dropped the literal default template ($NP_DEFAULTISH) — DEFAULT_TEMPLATE's own exclusion is not doing its job"
fi

if db_exists "$NP_ADHOC"; then
  fail "mutation was ineffective: with only the anchored pattern removed, gc --all still did NOT sweep an ad hoc '..._template' scratch database ($NP_ADHOC) that is neither \$TEMPLATE nor \$DEFAULT_TEMPLATE. This means the anchored pattern is not actually the thing protecting it, so removing it alone should have swept it."
else
  pass "with only the anchored pattern removed, gc --all swept an ad hoc '..._template' scratch database ($NP_ADHOC) that DEFAULT_TEMPLATE's literal exclusion does not cover — confirms the pattern clause is independently load-bearing for that case"
fi

drop_and_verify "$NP_DEFAULTISH"
drop_and_verify "$NP_ADHOC"

# Mutant B: remove ONLY the DEFAULT_TEMPLATE clause, keep the anchored
# pattern. Expected result: NO regression, because DEFAULT_TEMPLATE's own
# value always ends in "_template" and so is always also caught by the
# pattern — this documents, rather than assumes, that DEFAULT_TEMPLATE is
# presently redundant with the anchored pattern for the literal default
# name, which is exactly why it is kept as an independent safeguard rather
# than removed: nothing here should ever start failing if the pattern's
# shape or PREFIX's trailing underscore ever changes and DEFAULT_TEMPLATE
# stops being redundant.
MUTANT_NODEFAULT="$WORKDIR/testdb_mutant_nodefault.sh"
sed -e "s/^PREFIX=\"opencircuit_test_\"\$/PREFIX=\"${TESTPREFIX}\"/" \
    -e "s/ and datname <> '\\\$DEFAULT_TEMPLATE'//" \
    "$REAL_SCRIPT" > "$MUTANT_NODEFAULT"
chmod +x "$MUTANT_NODEFAULT"

if ! grep -q "^PREFIX=\"${TESTPREFIX}\"\$" "$MUTANT_NODEFAULT"; then
  echo "FATAL: prefix-scoping of the no-default mutant failed — aborting." >&2
  exit 1
fi
# Exact full-line match — same rationale as the two checks above.
if ! grep -Fxq "    exclude_clause=\"datname <> '\$TEMPLATE' and datname !~* '_template\\\$'\"" "$MUTANT_NODEFAULT"; then
  echo "FATAL: #0332 DEFAULT_TEMPLATE-only-removal mutation did not take effect as expected (exclude_clause line does not match 'TEMPLATE + anchored pattern only') — aborting rather than run it." >&2
  exit 1
fi

ND_DEFAULTISH="${TESTPREFIX}template"            # exactly $DEFAULT_TEMPLATE's value under this prefix
createdb_raw "$ND_DEFAULTISH"

"$MUTANT_NODEFAULT" gc --all >/dev/null 2>&1

if db_exists "$ND_DEFAULTISH"; then
  pass "with only DEFAULT_TEMPLATE removed, the anchored pattern alone still protected the literal default template ($ND_DEFAULTISH) — confirms DEFAULT_TEMPLATE is presently redundant with the pattern for this one name, and is kept deliberately as an independent safeguard, not because it is the only thing protecting it today"
else
  fail "with only DEFAULT_TEMPLATE removed, gc --all swept the literal default template ($ND_DEFAULTISH) even though the anchored pattern should also match it (PREFIX always ends in '_', so DEFAULT_TEMPLATE always ends in '_template') — the pattern clause is not doing what this comment assumes"
fi
drop_and_verify "$ND_DEFAULTISH"

echo "== Part 6: 'drop template' refuses without --force, and reports live connections even with it (#0333) =="
#
# #0333: name_for template (and, since #0208, name_for TEMPLATE) resolves to
# the exact same database `create` clones from — $TEMPLATE if TEMPLATE_DB is
# overridden, $DEFAULT_TEMPLATE otherwise. So `drop template` reads exactly
# like `drop 0324` ("drop my own scratch database") but destroys the shared
# resource every concurrent agent's next `create` depends on. #0327 already
# closed this exact blast radius for `gc --all`; this proves the matching
# fix in `drop` — refuse by default, same shape #0150 settled on for gc and
# #0207 for db-reset.sh's --force, and even --force never drops a template
# with a live connection (an agent's `create` may be mid-clone from it).

D_TEMPLATE="${TESTPREFIX}template"   # what $SCOPED's own DEFAULT_TEMPLATE resolves to
createdb_raw "$D_TEMPLATE"

OUT="$("$SCOPED" drop template 2>&1)"
RC=$?
if [ "$RC" -eq 0 ]; then
  fail "REGRESSION #0333: 'testdb.sh drop template' (no --force) exited 0 — expected a refusal"
else
  pass "'drop template' (no --force) refused (exit $RC)"
fi
if db_exists "$D_TEMPLATE"; then
  pass "'drop template' (no --force) did not drop the shared template"
else
  fail "REGRESSION #0333: 'testdb.sh drop template' (no --force) dropped the shared template it should have refused to touch"
fi
echo "$OUT" | grep -q -- "--force" || fail "'drop template's refusal message doesn't mention --force — a caller reading it won't know how to actually drop it"
echo "$OUT" | grep -qi "rebuild" || fail "'drop template's refusal message doesn't say how to rebuild the template"

# Same for the uppercase spelling — #0208 folds it to the same database name.
OUT2="$("$SCOPED" drop TEMPLATE 2>&1)"
RC2=$?
if [ "$RC2" -eq 0 ]; then
  fail "REGRESSION #0333: 'testdb.sh drop TEMPLATE' (uppercase, no --force) exited 0 — expected a refusal"
else
  pass "'drop TEMPLATE' (uppercase, no --force) refused too"
fi
echo "$OUT2" | grep -q -- "--force" || fail "'drop TEMPLATE's (uppercase) refusal message doesn't mention --force either"

# Criterion 4: --force with a live connection still refuses. See the #0223
# comment above terminate_backends() for why the connection must be waited
# for and terminated server-side, not just trusted to close when the local
# client is killed.
psql "$PGHOST_URL/$D_TEMPLATE" -c "select pg_sleep(15)" >/dev/null 2>&1 &
CONN_PID=$!
BG_PIDS+=("$CONN_PID")
WAITED=0
while [ "$(psql_admin -tAc "select count(*) from pg_stat_activity where datname='$D_TEMPLATE'")" -eq 0 ] && [ "$WAITED" -lt 10 ]; do
  sleep 0.5
  WAITED=$((WAITED + 1))
done

OUT3="$("$SCOPED" drop template --force 2>&1)"
RC3=$?
if [ "$RC3" -eq 0 ]; then
  fail "REGRESSION #0333: 'drop template --force' dropped a template with a live connection instead of refusing"
else
  pass "'drop template --force' refused while the template had a live connection"
fi
if db_exists "$D_TEMPLATE"; then
  pass "'drop template --force' did not drop the template while it had a live connection"
else
  fail "REGRESSION #0333: 'drop template --force' dropped the template while it had a live connection ($D_TEMPLATE gone)"
fi
echo "$OUT3" | grep -qi "connection" || fail "'drop template --force's refusal (live-connection case) doesn't mention the connection — not reporting it the way #0150's gc guard does"

terminate_backends "$D_TEMPLATE"
kill "$CONN_PID" >/dev/null 2>&1 || true
wait "$CONN_PID" 2>/dev/null || true
if wait_for_disconnect "$D_TEMPLATE" 20; then
  pass "connection to $D_TEMPLATE closed before the next step"
else
  fail "REGRESSION #0223: connection to $D_TEMPLATE did not close within the poll timeout"
fi

# Criterion 2: --force with NO connection actually performs the drop.
"$SCOPED" drop template --force >/dev/null 2>&1
if db_exists "$D_TEMPLATE"; then
  fail "'drop template --force' (no connections) did not drop the template — the explicit override doesn't work"
else
  pass "'drop template --force' (no connections) dropped the template"
fi

# Criterion 3: an ordinary `drop <id>` is unaffected and still needs no flag.
D_ORDINARY="${TESTPREFIX}0333ord"
createdb_raw "$D_ORDINARY"
"$SCOPED" drop 0333ord >/dev/null 2>&1
if db_exists "$D_ORDINARY"; then
  fail "'drop <ordinary id>' (no flag) failed to drop an ordinary scratch database — #0333's template guard is over-triggering"
else
  pass "'drop <ordinary id>' (no flag) still drops an ordinary scratch database, unaffected by the template guard"
fi

# Mutation proof: neuter the refusal condition (targeting its own line by a
# wildcard match, not a copy of its literal condition — §8) and confirm
# 'drop template' (no --force) WOULD then drop it, proving the assertions
# above are sensitive to the #0333 regression, not vacuously true.
MUTANT3="$WORKDIR/testdb_mutant3.sh"
sed -e "s/^PREFIX=\"opencircuit_test_\"\$/PREFIX=\"${TESTPREFIX}\"/" \
    -e 's/if \[ "\$force" != "1" \]; then/if false; then/' \
    "$REAL_SCRIPT" > "$MUTANT3"
chmod +x "$MUTANT3"

if ! grep -q "^PREFIX=\"${TESTPREFIX}\"\$" "$MUTANT3"; then
  echo "FATAL: prefix-scoping of the #0333 mutant failed — aborting before running it." >&2
  exit 1
fi
if ! grep -q 'if false; then' "$MUTANT3"; then
  echo "FATAL: #0333 guard-removal mutation did not take effect — aborting rather than run an unmutated drop (that would prove nothing)." >&2
  exit 1
fi

D_MUT="${TESTPREFIX}template"
createdb_raw "$D_MUT"
"$MUTANT3" drop template >/dev/null 2>&1   # no --force, but the refusal is neutered
if db_exists "$D_MUT"; then
  fail "mutation was ineffective: with the #0333 refusal removed, 'drop template' (no --force) still did NOT drop it. This means the assertions above would not actually catch a real #0333 regression."
else
  pass "with the #0333 refusal removed, 'drop template' (no --force) DID drop it — confirms the assertions above are sensitive to the #0333 regression, not vacuously true"
fi
drop_and_verify "$D_MUT"

echo "== Part 7: leak census — no ${TESTPREFIX}* database survives this run =="
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
