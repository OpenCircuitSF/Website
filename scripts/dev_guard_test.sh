#!/usr/bin/env bash
#
# dev_guard_test.sh — guard test for `scripts/dev.sh`'s port-safety logic (#0117).
#
# #0117: free_port() used to kill -9 whatever held :$PORT/:5173 with no check
# on whose process it was. The startup path was fixed to refuse by default
# and require RECLAIM_PORTS=1 to reclaim. Review (2026-08-23) found that fix
# incomplete: the EXIT-trap backstop in cleanup() re-derived "whoever holds
# :$PORT right now" from `lsof` at exit time and killed it unconditionally —
# so if dev.sh's own Go server died mid-session (panic, stray pkill) and a
# foreign process took the freed port, an ordinary Ctrl-C would kill that
# stranger. The re-fix tracks the pids dev.sh actually bound to :$PORT
# (OWNED_PIDS) and the trap kills only those. This script is the guard that
# keeps that fix from regressing.
#
# What it proves, in order:
#   1. Startup refusal: a held :$PORT makes dev.sh exit 1, and the holder
#      survives.
#   2. RECLAIM_PORTS=1 genuinely reclaims a held :$PORT, and it's dev.sh's own
#      server (proved by pid ancestry, not a hopeful curl) that ends up bound.
#   3. The EXIT trap: (a) with no interference, dev.sh's own children all
#      exit and :$PORT/:5173 are both released; (b) if a FOREIGN process
#      takes over :$PORT after dev.sh's Go server dies mid-session, an
#      ordinary Ctrl-C (SIGINT to the whole process group — a plain
#      `kill -INT` on just dev.sh's pid does not reach its foreground
#      `npm run dev` child) does NOT kill it.
#   4. Mutation proof: with cleanup()'s ownership check reverted to the old
#      unconditional "kill whoever holds the port" shape, the SAME
#      foreign-holder scenario from (3b) DOES get the stranger killed — i.e.
#      assertion (3b) is sensitive to the #0117 regression, not vacuously
#      true.
#
# SAFETY DESIGN
#
# All mutation (Part 4) runs against a private copy of scripts/dev.sh written
# into a scratch workdir — the tracked file is only ever read, never edited;
# byte-identity is checked at the end via sha256, the same bookend
# testdb_gc_guard_test.sh (#0150) uses.
#
# Every port used is a high, arbitrary number chosen to avoid collision, and
# is verified free before use (CLAUDE.md §8b) — a part aborts rather than
# proceeds against a port it can't confirm is free, since forcing through
# would be exactly the hazard #0117 exists to prevent. :5173 (Vite's
# hardcoded dev port) is real and shared: Parts 2-4 require it free and abort
# (not force) if it is not.
#
# Usage: scripts/dev_guard_test.sh
# Exit 0 = all guards hold. Exit 1 = a regression was detected (see FAIL lines).

set -uo pipefail   # NOT -e: several steps here are expected to fail — that's the assertion

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEVSH="$REPO/scripts/dev.sh"
WORKDIR="$(mktemp -d)"
FAILURES=0
BG_PIDS=()        # stray background processes (holders, foreign binders) to clean at exit
GROUP_LEADERS=()  # dev.sh (or mutant) subprocess pids, each its own process-group leader

fail() { FAILURES=$((FAILURES + 1)); printf 'FAIL: %s\n' "$1" >&2; }
pass() { printf 'PASS: %s\n' "$1"; }

port_bound() { [ -n "$(lsof -ti tcp:"$1" 2>/dev/null)" ]; }
port_free()  { ! port_bound "$1"; }
port_accepts_connection() { ( exec 3<>"/dev/tcp/127.0.0.1/$1" ) 2>/dev/null; }

wait_for() {  # wait_for <timeout_seconds> <command...>  — 1s poll interval
  local timeout="$1" waited=0; shift
  while ! "$@"; do
    waited=$((waited + 1))
    [ "$waited" -ge "$timeout" ] && return 1
    sleep 1
  done
  return 0
}

wait_pid_gone() {  # wait_pid_gone <pid> <timeout_seconds> — polls, force-kills on timeout
  # Polls rather than `wait $pid`: $pid is a group leader obtained via
  # `lg="$(launch_group ...)"`, and command substitution runs launch_group in
  # a subshell — so the backgrounded process is that subshell's child, not
  # this script's, and `wait` on it errors out instantly instead of blocking
  # (found live: it made this function return before the process had
  # actually exited, racing ahead of dev.sh's own shutdown sequence).
  local pid="$1" timeout="${2:-15}" waited=0
  while kill -0 "$pid" 2>/dev/null; do
    waited=$((waited + 1))
    if [ "$waited" -ge "$timeout" ]; then
      kill -9 "$pid" 2>/dev/null || true
      kill -KILL -- -"$pid" 2>/dev/null || true
      return 1
    fi
    sleep 1
  done
  return 0
}

MUTANT="$REPO/scripts/.dev_guard_test_mutant.$$.sh"  # see Part 4 below for why this lives in-repo, not $WORKDIR

# shellcheck disable=SC2329  # invoked indirectly via `trap cleanup EXIT` below
cleanup() {
  for pid in "${GROUP_LEADERS[@]:-}"; do
    [ -n "$pid" ] && kill -KILL -- -"$pid" >/dev/null 2>&1 || true
  done
  for pid in "${BG_PIDS[@]:-}"; do
    [ -n "$pid" ] && kill -9 "$pid" >/dev/null 2>&1 || true
  done
  rm -f "$MUTANT"
  rm -rf "$WORKDIR"
}
trap cleanup EXIT

command -v python3 >/dev/null || { echo "error: python3 not on PATH" >&2; exit 1; }
command -v lsof >/dev/null    || { echo "error: lsof not on PATH" >&2; exit 1; }
command -v shasum >/dev/null  || { echo "error: shasum not on PATH" >&2; exit 1; }
[ -f "$DEVSH" ] || { echo "error: $DEVSH not found" >&2; exit 1; }

SHA_BEFORE="$(shasum -a 256 "$DEVSH" | awk '{print $1}')"

# Launch a target script as its own process-group leader, so a signal to
# `-$pid` reaches every descendant — the way a terminal's Ctrl-C reaches a
# whole foreground pipeline, which a plain `kill -INT $pid` does not (review).
launch_group() {  # launch_group <logfile> <script> [NAME=VAL ...]
  local logfile="$1" script="$2"; shift 2
  (
    for kv in "$@"; do export "${kv?}"; done
    exec python3 -c '
import os, sys
os.setpgrp()
os.execvp(sys.argv[1], sys.argv[1:])
' "$script"
  ) > "$logfile" 2>&1 &
  echo $!
}

bind_holder() {  # bind_holder <port> — echoes pid, registers it for cleanup
  local port="$1" pid
  python3 -m http.server "$port" --bind 127.0.0.1 >/dev/null 2>&1 &
  pid=$!
  BG_PIDS+=("$pid")
  echo "$pid"
}

echo "== Part 1: startup refusal — held \$PORT makes dev.sh exit 1, holder survives =="
P1=52101
if port_bound "$P1"; then
  fail "part1 setup: port $P1 unexpectedly already bound — pick a different port and rerun"
else
  holder="$(bind_holder "$P1")"
  if wait_for 10 port_bound "$P1"; then
    PORT="$P1" "$DEVSH" >"$WORKDIR/part1.log" 2>&1
    rc=$?
    if [ "$rc" -eq 1 ]; then
      pass "dev.sh exited 1 with \$PORT ($P1) held by a foreign process"
    else
      fail "REGRESSION #0117: dev.sh exited $rc (expected 1) with \$PORT held — startup refusal did not fire"
    fi
    if kill -0 "$holder" 2>/dev/null && port_accepts_connection "$P1"; then
      pass "the holder (pid $holder) is still alive and still accepting connections on $P1 after dev.sh's refusal"
    else
      fail "REGRESSION #0117: the holder on $P1 did not survive dev.sh's startup path"
    fi
  else
    fail "part1 setup: holder never bound $P1"
  fi
  kill -9 "$holder" >/dev/null 2>&1 || true
  wait "$holder" 2>/dev/null || true
fi

echo "== Part 2: RECLAIM_PORTS=1 genuinely reclaims =="
P2=52111
if port_bound "$P2"; then
  fail "part2 setup: port $P2 unexpectedly already bound"
else
  holder="$(bind_holder "$P2")"
  if wait_for 10 port_bound "$P2"; then
    lg="$(launch_group "$WORKDIR/part2.log" "$DEVSH" "PORT=$P2" "RECLAIM_PORTS=1")"
    GROUP_LEADERS+=("$lg")
    if wait_for 60 grep -q "Go API server started" "$WORKDIR/part2.log"; then
      if kill -0 "$holder" 2>/dev/null; then
        fail "REGRESSION #0117: RECLAIM_PORTS=1 did not reclaim $P2 — the original holder ($holder) is still alive"
      else
        pass "RECLAIM_PORTS=1 killed the original holder ($holder) on $P2"
      fi
      # Confirm the pid now bound to $P2 descends from dev.sh's own group
      # leader — proof by ancestry, not a hopeful curl (CLAUDE.md §8b).
      owned=0
      for h in $(lsof -ti tcp:"$P2" 2>/dev/null || true); do
        anc="$h"
        for _ in 1 2 3 4 5 6; do
          [ "$anc" = "$lg" ] && { owned=1; break 2; }
          anc="$(ps -o ppid= -p "$anc" 2>/dev/null | tr -d ' ')"
          [ -z "$anc" ] && break
        done
      done
      if [ "$owned" = "1" ]; then
        pass "the process now bound to $P2 descends from dev.sh's own group leader (pid $lg)"
      else
        fail "after RECLAIM_PORTS=1, $P2's holder(s) are not descendants of dev.sh's group leader ($lg)"
      fi
    else
      fail "part2: dev.sh under RECLAIM_PORTS=1 never logged \"Go API server started\" — log tail: $(tail -5 "$WORKDIR/part2.log" | tr '\n' '|')"
    fi
    kill -INT -- -"$lg" >/dev/null 2>&1 || true
    wait_pid_gone "$lg" 20
  else
    fail "part2 setup: holder never bound $P2"
    kill -9 "$holder" >/dev/null 2>&1 || true
  fi
fi

echo "== Part 3a: ordinary shutdown — no interference, own ports released =="
P3A=52121
if port_bound "$P3A" || port_bound 5173; then
  fail "part3a setup: port $P3A or 5173 unexpectedly already bound — cannot safely test against a busy shared port"
else
  lg="$(launch_group "$WORKDIR/part3a.log" "$DEVSH" "PORT=$P3A")"
  GROUP_LEADERS+=("$lg")
  if wait_for 60 grep -q "Go API server started" "$WORKDIR/part3a.log" && wait_for 30 port_bound 5173; then
    kill -INT -- -"$lg" >/dev/null 2>&1
    wait_pid_gone "$lg" 20
    if port_free "$P3A" && port_free 5173; then
      pass "ordinary Ctrl-C released both $P3A and 5173 with dev.sh's own children all exited"
    else
      fail "REGRESSION #0117: after an ordinary Ctrl-C, $P3A or 5173 is still bound — the trap did not fully release its own ports"
    fi
  else
    fail "part3a setup: dev.sh never reached a ready state — log tail: $(tail -8 "$WORKDIR/part3a.log" | tr '\n' '|')"
    kill -KILL -- -"$lg" >/dev/null 2>&1 || true
  fi
fi

# The core #0117 regression scenario, reusable against both the real (fixed)
# dev.sh and a mutated copy, so Part 4 can prove Part 3b is not vacuous.
run_foreign_survives_scenario() {  # <label> <script-path> <go-port> -> sets RESULT
  local label="$1" script="$2" port="$3" lg gopid foreign
  local logfile="$WORKDIR/${label}.log"

  if port_bound "$port" || port_bound 5173; then
    echo "  [$label] SKIP setup: $port or 5173 already bound"
    RESULT="inconclusive"; return
  fi

  lg="$(launch_group "$logfile" "$script" "PORT=$port")"
  GROUP_LEADERS+=("$lg")

  if ! wait_for 60 grep -q "Go API server started" "$logfile"; then
    echo "  [$label] setup FAILED: dev.sh never logged readiness — log tail: $(tail -8 "$logfile" | tr '\n' '|')"
    kill -KILL -- -"$lg" >/dev/null 2>&1 || true
    RESULT="inconclusive"; return
  fi

  # Simulate the Go server dying mid-session (panic, stray pkill): kill
  # whatever actually holds $port right now, directly — not through dev.sh.
  gopid="$(lsof -ti tcp:"$port" 2>/dev/null || true)"
  if [ -z "$gopid" ]; then
    echo "  [$label] setup FAILED: nothing bound to $port right after readiness"
    kill -KILL -- -"$lg" >/dev/null 2>&1 || true
    RESULT="inconclusive"; return
  fi
  # shellcheck disable=SC2086
  kill -9 $gopid >/dev/null 2>&1 || true
  if ! wait_for 10 port_free "$port"; then
    echo "  [$label] setup FAILED: $port never freed after killing the simulated crash"
    kill -KILL -- -"$lg" >/dev/null 2>&1 || true
    RESULT="inconclusive"; return
  fi

  # A foreign process — standing in for another agent (CLAUDE.md §5a) —
  # takes over the now-free port.
  foreign="$(bind_holder "$port")"
  if ! wait_for 10 port_bound "$port"; then
    echo "  [$label] setup FAILED: foreign process never bound $port"
    kill -9 "$foreign" >/dev/null 2>&1 || true
    kill -KILL -- -"$lg" >/dev/null 2>&1 || true
    RESULT="inconclusive"; return
  fi

  # Ordinary Ctrl-C: SIGINT to the whole process group.
  kill -INT -- -"$lg" >/dev/null 2>&1 || true
  wait_pid_gone "$lg" 20

  if kill -0 "$foreign" 2>/dev/null; then
    RESULT="survived"
  else
    RESULT="killed"
  fi
  echo "  [$label] foreign holder (pid $foreign) after EXIT trap: $RESULT"
  kill -9 "$foreign" >/dev/null 2>&1 || true
  wait "$foreign" 2>/dev/null || true
}

echo "== Part 3b: foreign holder survives the EXIT trap (the #0117 review's own repro) =="
run_foreign_survives_scenario "part3b" "$DEVSH" 52131
case "$RESULT" in
  survived) pass "a foreign process that took over :\$PORT after dev.sh's Go server died mid-session survived an ordinary Ctrl-C" ;;
  killed)   fail "REGRESSION #0117: the EXIT trap killed a foreign process it never started — this is the issue's own defect, on the shutdown path" ;;
  *)        fail "part3b: scenario setup was inconclusive — see log line above; could not exercise the assertion" ;;
esac

echo "== Part 4: mutation proof — revert the EXIT-trap fix, confirm the SAME scenario now kills the foreign holder =="
# The mutant is written into scripts/ itself (untracked, uniquely named with
# $$, removed by the cleanup trap) rather than $WORKDIR: dev.sh self-locates
# its repo root from `dirname "${BASH_SOURCE[0]}"/..`, so a copy living
# outside scripts/ resolves REPO to the wrong directory and can't find go.mod
# or web/. This never edits the tracked scripts/dev.sh — only reads it via
# sed into a new, separate file that git does not track and this script
# deletes on exit; the byte-identity check below is the proof.
# shellcheck disable=SC2016  # the single-quoted $PORT is meant for the mutant script, not this shell
sed 's/^  release_owned_port .*/  free_port "$PORT" 1 2>\/dev\/null || true   # MUTATED: pre-#0117-fix unconditional backstop/' "$DEVSH" > "$MUTANT"
chmod +x "$MUTANT"

if grep -q '^  release_owned_port ' "$MUTANT"; then
  fail "part4: mutation did not take effect — mutant still calls release_owned_port; aborting the sensitivity check"
elif ! grep -q 'MUTATED: pre-#0117-fix unconditional backstop' "$MUTANT"; then
  fail "part4: mutation marker missing from mutant — cannot confirm what it claims to be"
elif ! bash -n "$MUTANT"; then
  fail "part4: mutant script has a syntax error"
else
  run_foreign_survives_scenario "part4" "$MUTANT" 52141
  case "$RESULT" in
    killed)
      pass "with the ownership check reverted, the SAME scenario DOES kill the foreign holder — confirms Part 3b's survival assertion is sensitive to the #0117 regression, not vacuously true" ;;
    survived)
      fail "mutation was ineffective: reverting cleanup() to the old unconditional free_port still did not kill the foreign holder — Part 3b would not actually catch a real regression" ;;
    *)
      fail "part4: mutant scenario setup was inconclusive — see log line above" ;;
  esac
fi

echo "== Byte-identity check =="
SHA_AFTER="$(shasum -a 256 "$DEVSH" | awk '{print $1}')"
if [ "$SHA_BEFORE" = "$SHA_AFTER" ]; then
  pass "scripts/dev.sh unchanged across the run (sha256 $SHA_AFTER) — all mutation happened on an untracked private copy ($MUTANT, removed on exit), never the tracked file"
else
  fail "CRITICAL: scripts/dev.sh's sha256 changed during this test run ($SHA_BEFORE -> $SHA_AFTER). Investigate immediately with: git diff -- scripts/dev.sh"
fi

echo
if [ "$FAILURES" -eq 0 ]; then
  echo "dev_guard_test.sh: all guards hold (0 failures)"
  exit 0
else
  echo "dev_guard_test.sh: $FAILURES failure(s) — see FAIL lines above"
  exit 1
fi
