#!/usr/bin/env bash
#
# deploy_guard_test.sh — guard test for `scripts/deploy.sh`'s hard gate on
# untracked files under web/public/ (issue #0424).
#
# #0424: deploy.sh's preflight already ran `git status --porcelain` and
# printed `note: working tree has uncommitted changes` — but that notice
# fires on EVERY deploy (web/dist/index.html is always `M` after a build,
# CLAUDE.md §8b), so it was permanently lit and carried no information. It
# fired for two days while two untracked workshop cover images sat in
# web/public/ (#0416) and nobody could tell the real signal from the
# constant noise. The fix adds a SECOND, narrower check: a hard failure on
# any `??` entry under web/public/ specifically — the one directory
# `npm run build` sweeps verbatim into the binary's embedded web/dist/ — left
# scoped tightly enough that it is safe to fail hard rather than merely note,
# because web/dist/index.html (`M`, not `??`, and outside web/public/) can
# never trip it.
#
# What this proves, in order:
#   1. The gate FIRES on a repo with an untracked file under web/public/,
#      naming that file, while an ordinary post-build web/dist/index.html
#      modification is also present.
#   2. The gate stays QUIET when the only change is the ordinary post-build
#      web/dist/index.html modification (direction 1 of #0424 criterion 2).
#   3. The gate stays QUIET when there is dirt elsewhere in the tree — an
#      untracked file OUTSIDE web/public/ — proving it is scoped, not a
#      repeat of the useless blanket notice (direction 2 of criterion 2).
#   4. The pre-existing general dirty-tree notice still fires for that other
#      dirt (criterion 3), and stays silent on a genuinely clean tree.
#   5. Mutation proof: with the gate's own condition inverted on a COPY of
#      deploy.sh, scenario 1's exact untracked-file repository is no longer
#      caught — i.e. assertion 1 is actually sensitive to the #0424 fix, not
#      vacuously true, and this is exactly the blind spot #0416 exploited.
#
# SAFETY DESIGN — read this before changing the test
#
# CLAUDE.md §8a forbids `git stash`/`checkout --`/`restore`/`reset --hard`/
# `clean` against any path this test did not itself create, and forbids
# running `scripts/deploy.sh` at all outside the production host (it sudo's,
# restarts a live service, and talks to the real public URL). So this test:
#
#   * NEVER invokes the real scripts/deploy.sh as a whole. It extracts just
#     the gate's own source lines (between the GATE-0424-BEGIN/END markers)
#     plus the two tiny helper functions the gate calls, and EXECUTES that
#     extracted text — never a hand-retyped copy of it — inside a throwaway
#     git repository this script creates under `mktemp -d`. Executing the
#     real file's own bytes is what makes this an external harness rather
#     than a decorative check: an edit that weakens the real gate changes
#     what gets extracted and run, so it cannot be satisfied by editing only
#     this test.
#   * NEVER touches the tracked working tree of this repository. Every git
#     repo used below is created fresh under WORKDIR, which is `mktemp -d`
#     output, not a path inside this checkout.
#   * The mutation proof (Part 5) mutates a COPY of deploy.sh in WORKDIR —
#     never the tracked scripts/deploy.sh — with a plain, portable `sed`
#     substitution (no `-i`, which is spelled differently on BSD vs GNU sed;
#     this writes to a new file instead, which is portable either way).
#
# Usage: scripts/deploy_guard_test.sh
# Exit 0 = all guards hold. Exit 1 = a regression was detected (message names it).
# Exit 2 = the harness itself is broken (extraction produced nothing, etc.) —
#          distinct from a real regression, per CLAUDE.md §8's "assert the
#          extraction produced something" rule.

set -uo pipefail  # NOT -e: several commands below are EXPECTED to fail (that's the assertion)

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REAL_SCRIPT="$REPO/scripts/deploy.sh"

[ -f "$REAL_SCRIPT" ] || { echo "FATAL: $REAL_SCRIPT not found" >&2; exit 2; }

WORKDIR="$(mktemp -d)"
FAILURES=0

cleanup() { rm -rf "$WORKDIR"; }
trap cleanup EXIT

fail() { FAILURES=$((FAILURES + 1)); printf 'FAIL: %s\n' "$1" >&2; }
pass() { printf 'PASS: %s\n' "$1"; }

# ---- extraction -------------------------------------------------------------
# Pull the gate's actual source, and the two one-line helpers it calls
# (die, info), out of a given copy of deploy.sh. Never a retyped copy of the
# logic — always the file's own bytes, so a weakened gate is what gets tested.
extract_gate() { awk '/# GATE-0424-BEGIN/{f=1; next} /# GATE-0424-END/{f=0} f' "$1"; }
extract_die()  { grep -m1 '^die(){'  "$1"; }
extract_info() { grep -m1 '^info(){' "$1"; }
# The pre-existing general dirty-tree notice line (criterion 3) — a distinct
# representation (a substring grep for its own user-facing text) from the
# GATE-0424 marker pair above, per CLAUDE.md §8's "a guard's oracle must not
# be the same bytes as its subject" — a single edit cannot satisfy both by
# accident.
extract_notice() { grep -m1 'note: working tree has uncommitted changes' "$1"; }

GATE_SRC="$(extract_gate "$REAL_SCRIPT")"
DIE_LINE="$(extract_die "$REAL_SCRIPT")"
INFO_LINE="$(extract_info "$REAL_SCRIPT")"
NOTICE_LINE="$(extract_notice "$REAL_SCRIPT")"

[ -n "$GATE_SRC" ]    || { echo "FATAL: extraction of the GATE-0424 block from $REAL_SCRIPT produced nothing — markers moved or removed?" >&2; exit 2; }
[ -n "$DIE_LINE" ]    || { echo "FATAL: extraction of die() from $REAL_SCRIPT produced nothing" >&2; exit 2; }
[ -n "$INFO_LINE" ]   || { echo "FATAL: extraction of info() from $REAL_SCRIPT produced nothing" >&2; exit 2; }
[ -n "$NOTICE_LINE" ] || { echo "FATAL: extraction of the general dirty-tree notice from $REAL_SCRIPT produced nothing" >&2; exit 2; }

# ---- scratch repo builder ----------------------------------------------------
# A minimal repo shaped like the real one in the one way that matters here:
# a tracked web/public/ (some design assets) and a tracked web/dist/index.html
# placeholder that a "build" modifies in place, exactly as npm run build does
# in the real deploy checkout (CLAUDE.md §8b).
make_repo() {
  local dir="$1"
  mkdir -p "$dir/web/public" "$dir/web/dist"
  ( cd "$dir" \
      && git init -q \
      && git config user.email "deploy-guard-test@example.com" \
      && git config user.name "deploy-guard-test" \
      && echo "<svg/>" > web/public/logo.svg \
      && echo "<!doctype html><!-- placeholder -->" > web/dist/index.html \
      && git add -A \
      && git commit -q -m "init" )
}

# Simulate the one thing every real deploy does to the tree: `npm run build`
# rewrites web/dist/index.html with hashed asset references.
simulate_build() {
  local dir="$1"
  echo "<!doctype html><script src=/assets/index-DEADBEEF.js></script>" > "$dir/web/dist/index.html"
}

# ---- harness runner -----------------------------------------------------------
# Builds a standalone script from extracted source and runs it with cwd=$1,
# capturing combined stdout+stderr and exit code into globals.
RUN_OUT=""
RUN_RC=0
run_harness() {
  local dir="$1" gate_src="$2" harness
  harness="$WORKDIR/harness_$$_$RANDOM.sh"
  {
    printf '#!/usr/bin/env bash\n'
    printf 'set -euo pipefail\n'
    printf '%s\n' "$DIE_LINE"
    printf '%s\n' "$INFO_LINE"
    printf '%s\n' "$gate_src"
  } > "$harness"
  RUN_OUT="$(cd "$dir" && bash "$harness" 2>&1)"
  RUN_RC=$?
  rm -f "$harness"
}

# Runs just the pre-existing general notice line (criterion 3), independent
# of the GATE-0424 block, so a pass/fail here says something about that
# separate mechanism and not about the new gate.
run_notice() {
  local dir="$1" harness
  harness="$WORKDIR/notice_$$_$RANDOM.sh"
  {
    printf '#!/usr/bin/env bash\n'
    printf 'set -uo pipefail\n'   # no -e: an AND-list whose left side is false is expected here
    printf '%s\n' "$INFO_LINE"
    printf '%s\n' "$NOTICE_LINE"
  } > "$harness"
  RUN_OUT="$(cd "$dir" && bash "$harness" 2>&1)"
  RUN_RC=$?
  rm -f "$harness"
}

# ==============================================================================
# Part 1 + 2 direction 1: fires on an untracked file under web/public/, and
# names it, alongside the ordinary post-build web/dist/index.html change.
# ==============================================================================
REPO_A="$WORKDIR/repo_fires"
make_repo "$REPO_A"
simulate_build "$REPO_A"
echo "not part of the repo" > "$REPO_A/web/public/oops.jpg"

run_harness "$REPO_A" "$GATE_SRC"
if [ "$RUN_RC" -eq 1 ] && printf '%s' "$RUN_OUT" | grep -q 'oops\.jpg' \
   && printf '%s' "$RUN_OUT" | grep -q 'web/public/'; then
  pass "gate fires on untracked web/public/oops.jpg, naming it (exit $RUN_RC)"
else
  fail "gate did not fire (or did not name the file) for an untracked file under web/public/: rc=$RUN_RC output=[$RUN_OUT]"
fi

# ==============================================================================
# Part 2 direction 2 (criterion 2, part 1): stays quiet on the ordinary
# post-build web/dist/index.html modification alone.
# ==============================================================================
REPO_B="$WORKDIR/repo_quiet_build"
make_repo "$REPO_B"
simulate_build "$REPO_B"

run_harness "$REPO_B" "$GATE_SRC"
if [ "$RUN_RC" -eq 0 ] && [ -z "$RUN_OUT" ]; then
  pass "gate stays quiet on an ordinary web/dist/index.html modification (exit 0, no output)"
else
  fail "gate misfired on an ordinary post-build web/dist/index.html change: rc=$RUN_RC output=[$RUN_OUT]"
fi

# ==============================================================================
# Part 3 (criterion 2, part 2): stays quiet on dirt OUTSIDE web/public/ —
# proves the gate is scoped, not a repeat of the always-lit blanket notice.
# ==============================================================================
REPO_C="$WORKDIR/repo_quiet_elsewhere"
make_repo "$REPO_C"
simulate_build "$REPO_C"
echo "scratch notes, not part of the repo" > "$REPO_C/NOTES.txt"

run_harness "$REPO_C" "$GATE_SRC"
if [ "$RUN_RC" -eq 0 ] && [ -z "$RUN_OUT" ]; then
  pass "gate stays quiet on an untracked file OUTSIDE web/public/ (exit 0, no output) — it is scoped"
else
  fail "gate fired on dirt outside web/public/, which it must never do: rc=$RUN_RC output=[$RUN_OUT]"
fi

# ==============================================================================
# Part 4 (criterion 3): the pre-existing general notice still works —
# fires for the same outside-web/public/ dirt, and is silent on a clean tree.
# ==============================================================================
run_notice "$REPO_C"
if printf '%s' "$RUN_OUT" | grep -q 'note: working tree has uncommitted changes'; then
  pass "general dirty-tree notice still fires for non-web/public/ dirt"
else
  fail "general dirty-tree notice did not fire on a dirty tree: rc=$RUN_RC output=[$RUN_OUT]"
fi

REPO_D="$WORKDIR/repo_clean"
make_repo "$REPO_D"   # no build simulated, no extra files — genuinely clean

run_notice "$REPO_D"
if [ -z "$RUN_OUT" ]; then
  pass "general dirty-tree notice is silent on a genuinely clean tree"
else
  fail "general dirty-tree notice fired on a clean tree (false positive): output=[$RUN_OUT]"
fi

# ==============================================================================
# Part 5 — mutation proof: invert the gate's own condition on a COPY of
# deploy.sh (never the tracked file) and confirm Part 1's exact scenario is
# no longer caught. This is the #0416 blind spot, reconstructed on purpose,
# to prove Part 1 is actually sensitive to the fix rather than vacuous.
# ==============================================================================
MUTATED="$WORKDIR/deploy_mutated.sh"
sed 's/\[ -n "\$UNTRACKED_PUBLIC" \]/[ -z "$UNTRACKED_PUBLIC" ]/' "$REAL_SCRIPT" > "$MUTATED"

if ! diff -q "$REAL_SCRIPT" "$MUTATED" >/dev/null 2>&1; then
  MUT_GATE_SRC="$(extract_gate "$MUTATED")"
  if [ -n "$MUT_GATE_SRC" ]; then
    run_harness "$REPO_A" "$MUT_GATE_SRC"   # same repo Part 1 caught
    if [ "$RUN_RC" -eq 0 ] && [ -z "$RUN_OUT" ]; then
      pass "mutation proof: inverting the gate's condition silently loses the #0416 case (exit 0, no output) — Part 1 is a real assertion, not a vacuous one"
    else
      fail "mutation proof failed to demonstrate a regression: expected the mutated (inverted) gate to silently miss the untracked file, but got rc=$RUN_RC output=[$RUN_OUT]"
    fi
  else
    fail "mutation proof: extraction from the mutated copy produced nothing (sed pattern no longer matches deploy.sh's condition — update this test's sed pattern to match the current source)"
  fi
else
  fail "mutation proof: the sed substitution made no change to the mutated copy — its pattern no longer matches scripts/deploy.sh's current condition text"
fi

# ---- verdict ------------------------------------------------------------------
printf '\n'
if [ "$FAILURES" -eq 0 ]; then
  echo "VERIFICATION PASSED — all deploy.sh web/public/ gate checks hold (#0424)"
  exit 0
else
  echo "VERIFICATION FAILED — $FAILURES check(s) above"
  exit 1
fi
