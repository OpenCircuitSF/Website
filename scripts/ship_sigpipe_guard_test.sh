#!/usr/bin/env bash
#
# ship_sigpipe_guard_test.sh — guard test for scripts/ship.sh's --yes pipe
# (issue #0714).
#
# #0714: `yes y | scripts/deploy.sh` under `set -euo pipefail` exited 141
# after a successful deploy — yes dies of SIGPIPE once deploy.sh exits, and
# pipefail reports it. The fix wraps the producer: `{ yes y || true; } | …`.
#
# Static: the fixed line appears exactly once and no bare `yes y |` pipe into
# deploy.sh remains (regex oracle, not a copy of the subject — AGENTS.md §8).
# Behavioural: under bash -c 'set -euo pipefail', a stub reader that exits
# without reading proves old shape -> 141, new shape -> stub's own status.
#
# SHIP_SH overrides the file under test (mutation proof against a copy).
# Never runs ship.sh or deploy.sh. Exit: 0 pass, 1 regression, 2 harness broken.
set -uo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
SUBJECT="${SHIP_SH:-$REPO/scripts/ship.sh}"
[ -f "$SUBJECT" ] || { echo "FATAL: $SUBJECT not found" >&2; exit 2; }

FAILURES=0
fail() { FAILURES=$((FAILURES + 1)); printf 'FAIL: %s\n' "$1" >&2; }
pass() { printf 'PASS: %s\n' "$1"; }

# ---- static -----------------------------------------------------------------
# shellcheck disable=SC2016  # literal $VERSION in the regex
n="$(grep -cE '^[[:space:]]*\{ yes y \|\| true; \} \| scripts/deploy\.sh "\$VERSION"[[:space:]]*$' "$SUBJECT")"
if [ "$n" = "1" ]; then pass "fixed --yes pipe appears exactly once"
else fail "fixed --yes pipe appears $n times in $SUBJECT (want exactly 1)"; fi

n="$(grep -vE '^[[:space:]]*#' "$SUBJECT" | grep -cE '(^|[^{[:alnum:]_])[[:space:]]*yes[[:space:]]+y[[:space:]]*\|[^|].*deploy\.sh')"
if [ "$n" = "0" ]; then pass "no bare 'yes y |' pipe into deploy.sh"
else fail "$n bare 'yes y |' pipe(s) into deploy.sh in $SUBJECT (#0714 SIGPIPE regression)"; fi

# ---- behavioural ------------------------------------------------------------
# The stub reader exits immediately without reading stdin, as deploy.sh does
# once it has its answer.
shape() { # $1 = producer, $2 = stub exit code
  bash -c "set -euo pipefail; $1 | { exit $2; }" >/dev/null 2>&1
  echo $?
}
got="$(shape 'yes y' 0)"
if [ "$got" = "141" ]; then pass "old shape exits 141 (harness non-vacuous)"
else echo "FATAL: old shape exited $got, expected 141 — harness cannot observe SIGPIPE" >&2; exit 2; fi

got="$(shape '{ yes y || true; }' 0)"
if [ "$got" = "0" ]; then pass "new shape exits 0 when deploy succeeds"
else fail "new shape exited $got when stub exited 0 (want 0)"; fi

got="$(shape '{ yes y || true; }' 3)"
if [ "$got" = "3" ]; then pass "new shape propagates deploy failure (3)"
else fail "new shape exited $got when stub exited 3 (want 3)"; fi

if [ "$FAILURES" -gt 0 ]; then echo "ship_sigpipe_guard_test: $FAILURES failure(s)" >&2; exit 1; fi
echo "ship_sigpipe_guard_test: all checks passed"
