#!/usr/bin/env bash
#
# check.sh — the canonical verification run. Use this instead of hand-rolling
# `go test` invocations, so every agent verifies the same way.
#
#     scripts/check.sh                       # build + vet + scoped Go tests + web checks
#     scripts/check.sh go ./internal/mailing/...   # just those Go packages
#     scripts/check.sh web                   # just npm run check + npm test
#     scripts/check.sh all                   # the whole Go suite (a batch's review pass only)
#     scripts/check.sh guards                # the standalone shell guard tests (see below)
#
# `guards` runs scripts/testdb_gc_guard_test.sh, scripts/dev_guard_test.sh,
# and scripts/db_reset_guard_test.sh — not part of any other mode, since
# #0117's third review measured dev_guard_test.sh alone at ~48s and binding
# :5173 in several parts, which does not belong in every ordinary run. #0207
# named the actual defect this solves: two prior guard tests
# (testdb_gc_guard_test.sh from #0150, dev_guard_test.sh from #0117) existed
# but nothing ran them automatically, so a safety property only held when
# someone remembered the guard existed. This is that "something someone
# actually runs" — it is not wired into `go`/`web`/`all`/the default, so it
# costs nothing on the common path.
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

# #0208: every build/vet/test/npm step below that pipes into `tail` (so the
# piped command's real exit code isn't discarded — see #0140: without
# -o pipefail, `go test ... | tail -40` reports tail's exit status, which is
# always 0, so a failing run prints FAILED output and this script still says
# VERIFICATION PASSED) funnels through this ONE function, the single place
# that spells the flag. #0140's original fix was a source grep asserting
# every "run bash -c" call site individually said "-o pipefail" — reformatted
# per CLAUDE.md §5's example set, that guard was shown (issues/0208.md) to
# miss `/bin/bash -c`, `env bash -c`, `$SH -c`, a bare `bash -c` outside
# `run`, and a `bash \`-newline-`-c` split across two lines, plus a false
# "clean" reading from a trailing `# pipefail` comment. Structure removes the
# whole evasion surface: there is now nowhere else in this file to spell (or
# lose) the flag, so none of those forms is expressible as a way to lose it —
# a call site either goes through runpipe() or it doesn't run through a shell
# pipe at all.
# BEGIN RUNPIPE-0208
runpipe() { run bash -o pipefail -c "$1"; }
# END RUNPIPE-0208

# BEGIN GUARD-0208
# Self-check: confirms the funnel above hasn't been bypassed by a later edit.
# #0140's original guard was a same-line textual match: a call site excluded
# itself from its own "missing -o pipefail" search only because that same
# source line ALSO happened to contain the word "pipefail" elsewhere on it
# (in a `grep -v 'pipefail'` clause). Reformatting either of the two lines
# that coincidence depended on made the guard match its OWN source and exit 2
# on every invocation, permanently (issues/0208.md). This guard cannot repeat
# that failure by construction: it deletes its own block (between the
# BEGIN/END GUARD-0208 markers, right here, which is also why the `step` call
# announcing this check lives inside them) and runpipe()'s own definition
# block (BEGIN/END RUNPIPE-0208, above) from a scratch copy of this file
# before scanning anything — so no wording inside either block, however it's
# laid out or reformatted, can ever be mistaken for a violation of itself.
# What must NOT appear anywhere else in the file: the word "pipefail" (a
# second spelling means either a call site is spelling the flag directly
# instead of going through runpipe(), or runpipe() itself was duplicated —
# the script's OWN top-of-file `set -uo pipefail` is excluded by exact line
# match below, since that is this script's own option, not a call site), or
# any other bash/sh invocation carrying a "-c" flag (a bypass that lost the
# flag entirely wouldn't otherwise show up as a second "pipefail").
#
# Known, accepted gap (#0208's review, recorded by #0251): `run "$SHELL" -c
# "… | tail"` would evade every check here — it spells neither a literal
# "pipefail" nor a literal "bash"/"sh" token, and adding it as a new step
# leaves the runpipe() call count unchanged. Contrived; no realistic edit to
# this file produces it. Not closed.
step "self-check: pipefail funnel guard (#0208)"
# The four sed patterns below are each built from two concatenated pieces
# (no separator between the closing and opening quotes) rather than spelled
# as one literal token — so THIS line's own source text never contains a
# contiguous "# BEGIN GUARD-0208" (etc.) substring. That matters because sed
# range-deletion reopens a range if a LATER line matches the start pattern
# again: had this line spelled the markers whole, it would itself re-trigger
# the very ranges it exists to close, deleting everything from here to EOF —
# exactly the self-referential trap this design otherwise avoids.
BG="# BEGIN"" GUARD-0208"; EG="# END"" GUARD-0208"
BR="# BEGIN"" RUNPIPE-0208"; ER="# END"" RUNPIPE-0208"
# Scans "$0" (however THIS invocation was actually run), not a hardcoded
# "$REPO/scripts/check.sh" — the latter would let a mutated copy of this
# script (used to mutation-test this very guard, per CLAUDE.md §8a: never
# edit the shared tracked file to prove a guard catches a regression) scan
# straight past its own mutation and check the untouched tracked file
# instead, silently proving nothing.
SCAN="$(sed -e "/${BG}/,/${EG}/d" -e "/${BR}/,/${ER}/d" "$0" \
    | grep -vE '^[[:space:]]*#' \
    | grep -vxF 'set -uo pipefail')"

PF_HITS="$(printf '%s\n' "$SCAN" | grep -c 'pipefail' || true)"
if [ "${PF_HITS:-0}" -ne 0 ]; then
  printf '\033[31mPIPEFAIL REGRESSION (#0208): "pipefail" appears in scripts/check.sh outside runpipe()'"'"'s definition. A call site may be spelling the flag directly instead of calling runpipe(), or runpipe() was duplicated. Offending line(s):\033[0m\n' >&2
  printf '%s\n' "$SCAN" | grep -n 'pipefail' >&2
  exit 2
fi

SHELLC_HITS="$(printf '%s\n' "$SCAN" | grep -nE '\b(bash|sh)\b' | grep -E -- '-c\b' || true)"
if [ -n "$SHELLC_HITS" ]; then
  printf '\033[31mPIPEFAIL REGRESSION (#0208): scripts/check.sh has a shell "-c" invocation outside runpipe() — it could silently lose "-o pipefail" the same way #0140 did. Offending line(s):\033[0m\n' >&2
  printf '%s\n' "$SHELLC_HITS" >&2
  exit 2
fi

RUNPIPE_CALLS="$(printf '%s\n' "$SCAN" | grep -c 'runpipe "' || true)"
if [ "${RUNPIPE_CALLS:-0}" -lt 9 ]; then
  printf '\033[31mPIPEFAIL REGRESSION (#0208): expected at least 9 runpipe() call sites (one per go build/vet/test and npm check/test step); found %s. A step may have reverted to spelling a shell invocation directly instead of calling runpipe().\033[0m\n' "$RUNPIPE_CALLS" >&2
  exit 2
fi

# #0251: everything above is a NEGATIVE assertion over $SCAN — a copy of
# this file with runpipe()'s own definition (the RUNPIPE-0208 block) already
# deleted, precisely so the checks above can't flag their own necessary
# mention of "pipefail" and "-c". That makes the guard structurally BLIND to
# the one line that actually carries the property: whether runpipe() itself
# still spells "-o pipefail". Strip that one flag from runpipe()'s
# definition and every check above still passes — no stray "pipefail", no
# stray shell "-c", still >= 9 "runpipe \"" call sites — while the property
# this whole self-check exists to guarantee is gone. Demonstrated in
# issues/0251.md: with the flag stripped, a failing test produced
# VERIFICATION PASSED and exit 0.
#
# This is the missing POSITIVE half: extract runpipe()'s definition block
# directly from "$0" (between the RUNPIPE-0208 markers, comments stripped)
# — NOT from $SCAN, which has that block deleted — and assert it still
# spells the funnel it exists to own. Scoping the extraction to exactly
# that marker-delimited block (rather than grepping the whole file) is what
# makes this un-satisfiable by "-o pipefail" merely appearing somewhere
# else: only the actual definition can produce a match here.
RUNPIPE_DEF="$(sed -n "/${BR}/,/${ER}/p" "$0" | grep -vE '^[[:space:]]*#')"
if ! printf '%s\n' "$RUNPIPE_DEF" | grep -qE 'run[[:space:]]+bash[[:space:]]+-o[[:space:]]+pipefail[[:space:]]+-c'; then
  printf '\033[31mPIPEFAIL REGRESSION (#0208/#0251): runpipe()'"'"'s own definition no longer spells "run bash -o pipefail -c" — the funnel this self-check exists to guarantee has lost the flag it is supposed to hold. Definition block as found in the running script:\033[0m\n' >&2
  printf '%s\n' "$RUNPIPE_DEF" >&2
  exit 2
fi
# END GUARD-0208

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
  # #0212: the default package list must include every package that can hold
  # a Go test, not just the ones that happen to today. ./web/... (web/embed.go,
  # currently no _test.go) used to be absent here, so a test placed in that
  # package would compile, pass locally under a bare `go test ./...`, and then
  # never run again under the command every issue is told to use — exactly
  # what happened to #0141's first guard placement before it was relocated to
  # internal/seo. Add a new top-level Go package here when it is created;
  # don't rely on `scripts/check.sh all`, which CLAUDE.md reserves for a
  # batch's single review pass.
  local pkgs="$*"; [ -n "$pkgs" ] || pkgs="./internal/... ./cmd/... ./web/..."
  step "go test $pkgs -p 2"
  runpipe "go test $pkgs -p 2 -count=1 2>&1 | tail -$TAIL"
  step "skip audit — a package reporting [no test files] or 'no test files' proves nothing"
  go test $pkgs -p 2 -count=1 2>&1 | grep -E 'no test files|SKIP|--- SKIP' | head -20 || echo "(no skips)"
}

web_check() {
  step "npm run check"; runpipe "cd web && npm run check 2>&1 | tail -$TAIL"
  step "npm test";      runpipe "cd web && npm test 2>&1 | tail -$TAIL"
}

# #0161: gofmt drift is otherwise invisible — `go vet` does not check
# formatting, so an unformatted file passes build+vet+test silently and
# stays unformatted indefinitely. Scoped to the whole repo (`.`), not a list
# of package roots: gofmt only ever looks at *.go files, so scanning the
# whole tree can't false-positive on non-Go content (web/, migrations/,
# etc.) and needs no maintenance as packages are added or moved. Must FAIL,
# not just print — `gofmt -l` exits 0 even when it lists files (the #0140
# shape of bug: a real signal sitting in output nobody checks), so the
# non-empty output itself is what flips FAILED here.
gofmt_check() {
  step "gofmt -l (#0161 — formatting drift)"
  local out
  out="$(gofmt -l . 2>&1)"
  if [ -n "$out" ]; then
    FAILED=1
    printf '\033[31mFAILED: gofmt -l found unformatted file(s):\033[0m\n%s\n' "$out"
    printf '\033[31mFix: gofmt -w %s\033[0m\n' "$(echo "$out" | tr '\n' ' ' | sed 's/ *$//')"
  else
    echo "(clean)"
  fi
}

case "$MODE" in
  go)  step "go build"; runpipe "go build ./... 2>&1 | tail -$TAIL"
       step "go vet";   runpipe "go vet ./... 2>&1 | tail -$TAIL"
       gofmt_check
       go_test "$@" ;;
  web) web_check ;;
  guards)
       # #0207: the "something someone actually runs" for the standalone
       # shell guard tests — see the header comment for why this is its own
       # mode rather than folded into go/all/default. Each script manages
       # its own scratch database(s) and prints its own PASS/FAIL lines;
       # `run` here only needs to observe the exit code.
       step "scripts/testdb_gc_guard_test.sh (#0150)"; run scripts/testdb_gc_guard_test.sh
       step "scripts/dev_guard_test.sh (#0117)";        run scripts/dev_guard_test.sh
       step "scripts/db_reset_guard_test.sh (#0207)";   run scripts/db_reset_guard_test.sh
       ;;
  all) step "go build"; runpipe "go build ./... 2>&1 | tail -$TAIL"
       step "go vet";   runpipe "go vet ./... 2>&1 | tail -$TAIL"
       gofmt_check
       go_test "./..."; web_check ;;
  *)   step "go build"; runpipe "go build ./... 2>&1 | tail -$TAIL"
       step "go vet";   runpipe "go vet ./... 2>&1 | tail -$TAIL"
       gofmt_check
       # #0212 review (bounced bfdfe04): this arm used to spell its own
       # explicit package list ("./internal/... ./cmd/..."), a second source
       # of truth that go_test()'s default-list edit never reached — so
       # ./web/... joined the `go` mode's scope but not the BARE default's,
       # which is the command CLAUDE.md §5 and every issue actually tells
       # agents to run. Calling go_test with no arguments makes this arm read
       # the exact same default list `go)` reads when given none, so there is
       # only one place that list is spelled.
       go_test; web_check ;;
esac

step "leftover processes you may have started"
pgrep -fl 'go test|\.test ' || echo "(none)"

if [ "$FAILED" -ne 0 ]; then printf '\n\033[31mVERIFICATION FAILED\033[0m\n'; exit 1; fi
printf '\n\033[32mVERIFICATION PASSED\033[0m\n'
