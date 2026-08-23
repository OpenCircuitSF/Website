#!/usr/bin/env bash
#
# check.sh — the canonical verification run. Use this instead of hand-rolling
# `go test` invocations, so every agent verifies the same way.
#
#     scripts/check.sh                       # build + vet + scoped Go tests + web checks
#     scripts/check.sh go ./internal/mailing/...   # just those Go packages
#     scripts/check.sh web                   # just npm run check + npm test
#     scripts/check.sh all                   # the whole Go suite (a batch's review pass only)
#
# WHAT IT ENFORCES, so you cannot forget:
#
#   * TEST_DATABASE_URL is exported. Without it the DB-backed suites SKIP
#     SILENTLY and a green run proves nothing (CLAUDE.md §5). If ISSUE is set,
#     this script gives you your own database via scripts/testdb.sh instead of
#     sharing one — see that script for why that lifts the concurrency cap.
#   * -p 2 on every Go run (CLAUDE.md §5a).
#   * -count=N for N>1 is refused (CLAUDE.md §5a — banned for flake-hunting).
#   * Output is bounded with tail, because a full run emits more tokens than
#     the PRD.
#   * `uptime` is printed first. Above ~8 the machine is busy and any TIMING
#     failure is that, not your code. A state assertion is never explained by
#     load.
#
# Set ISSUE=0123 to get an isolated test database, created before the run and
# dropped after:
#
#     ISSUE=0123 scripts/check.sh go ./internal/handlers/...

set -uo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO"
TAIL="${TAIL:-40}"
FAILED=0

step() { printf '\n\033[1m=== %s\033[0m\n' "$*"; }
run()  { "$@"; local rc=$?; [ $rc -eq 0 ] || { FAILED=1; printf '\033[31mFAILED (%d): %s\033[0m\n' "$rc" "$*"; }; return 0; }

for a in "$@"; do
  case "$a" in
    -count=1|-count) ;;
    -count=*) echo "error: $a is banned (CLAUDE.md §5a). A test that fails only under load is not flaky." >&2; exit 2 ;;
  esac
done

step "machine"
uptime
LOAD="$(uptime | sed 's/.*load averages*: *//' | awk '{print $1}' | tr -d ,)"
if awk "BEGIN{exit !($LOAD > 8)}"; then
  echo "WARNING: load average $LOAD is above ~8. Something else is running."
  echo "Any TIMING failure you see is that, not a defect. Do not widen a deadline to accommodate it."
fi

OWN_DB=""
if [ -n "${ISSUE:-}" ]; then
  step "isolated test database for issue $ISSUE"
  if TEST_DATABASE_URL="$(scripts/testdb.sh create "$ISSUE")"; then
    export TEST_DATABASE_URL; OWN_DB="$ISSUE"
    echo "$TEST_DATABASE_URL"
  else
    echo "could not create an isolated database; aborting rather than silently sharing one" >&2
    exit 1
  fi
else
  export TEST_DATABASE_URL="${TEST_DATABASE_URL:-postgres://opencircuit:opencircuit@localhost:5432/opencircuit_test?sslmode=disable}"
  echo "TEST_DATABASE_URL=$TEST_DATABASE_URL  (shared — set ISSUE=NNNN for your own)"
fi
cleanup() { [ -n "$OWN_DB" ] && scripts/testdb.sh drop "$OWN_DB" >/dev/null 2>&1; }
trap cleanup EXIT

MODE="${1:-default}"; shift || true

go_test() {
  local pkgs="$*"; [ -n "$pkgs" ] || pkgs="./internal/... ./cmd/..."
  step "go test $pkgs -p 2"
  run bash -o pipefail -c "go test $pkgs -p 2 -count=1 2>&1 | tail -$TAIL"
  step "skip audit — a package reporting [no test files] or 'no test files' proves nothing"
  go test $pkgs -p 2 -count=1 2>&1 | grep -E 'no test files|SKIP|--- SKIP' | head -20 || echo "(no skips)"
}

web_check() {
  step "npm run check"; run bash -o pipefail -c "cd web && npm run check 2>&1 | tail -$TAIL"
  step "npm test";      run bash -o pipefail -c "cd web && npm test 2>&1 | tail -$TAIL"
}

case "$MODE" in
  go)  step "go build"; run bash -o pipefail -c "go build ./... 2>&1 | tail -$TAIL"
       step "go vet";   run bash -o pipefail -c "go vet ./... 2>&1 | tail -$TAIL"
       go_test "$@" ;;
  web) web_check ;;
  all) step "go build"; run bash -o pipefail -c "go build ./... 2>&1 | tail -$TAIL"
       step "go vet";   run bash -o pipefail -c "go vet ./... 2>&1 | tail -$TAIL"
       go_test "./..."; web_check ;;
  *)   step "go build"; run bash -o pipefail -c "go build ./... 2>&1 | tail -$TAIL"
       step "go vet";   run bash -o pipefail -c "go vet ./... 2>&1 | tail -$TAIL"
       go_test "./internal/... ./cmd/..."; web_check ;;
esac

step "leftover processes you may have started"
pgrep -fl 'go test|\.test ' || echo "(none)"

if [ "$FAILED" -ne 0 ]; then printf '\n\033[31mVERIFICATION FAILED\033[0m\n'; exit 1; fi
printf '\n\033[32mVERIFICATION PASSED\033[0m\n'
