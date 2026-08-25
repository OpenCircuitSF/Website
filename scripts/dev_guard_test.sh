#!/usr/bin/env bash
#
# dev_guard_test.sh — guard test for `scripts/dev.sh`'s port-safety logic (#0117).
#
# #0117: dev.sh used to `kill -9` whatever held :$PORT/:5173 with no check on
# whose process it was. Three fixes have landed, each pinned below:
#
#   * startup refuses by default and needs RECLAIM_PORTS=1 to reclaim;
#   * the EXIT trap kills only pids dev.sh itself started;
#   * ownership is derived from the FORK ($GO_PID and its descendants,
#     fingerprinted with their start times), never from who happens to hold
#     the port. The second review pass reproduced two live kills that the
#     port-derived answer allowed:
#       repro A — a foreign process merely CONNECTED to :$PORT (a browser tab,
#                 a curl, a poller) answers `lsof -ti tcp:P` and was enrolled
#                 as "own process", then kill -9ed at exit;
#       repro B — the readiness check tested `kill -0 "$GO_PID"` (go run is
#                 alive), not that anything BOUND. On a build slower than the
#                 fixed `sleep 1`, a foreign listener that won the bind race
#                 was recorded as ours and killed at exit.
#
# What it proves, in order:
#   1. Startup refusal: a held :$PORT makes dev.sh exit 1, and the holder
#      survives.
#   2. RECLAIM_PORTS=1 genuinely reclaims a held :$PORT, and it is dev.sh's own
#      server (proved by pid ancestry, not a hopeful curl) that ends up bound.
#   3. The EXIT trap: (a) with no interference, dev.sh's own children all exit
#      and :$PORT/:5173 are both released; (b) if a FOREIGN process takes over
#      :$PORT after dev.sh's Go server dies mid-session, an ordinary Ctrl-C
#      (SIGINT to the whole process group — a plain `kill -INT` on just
#      dev.sh's pid does not reach its foreground `npm run dev` child) does
#      NOT kill it.
#   4. Mutation proof for 3b.
#   5. Repro A: a foreign process holding an ESTABLISHED connection to :$PORT
#      across the ownership capture survives an ordinary Ctrl-C — plus the
#      mutation proof that reverting ownership-by-fork and the listener
#      scoping does kill it.
#   6. Repro B: a foreign LISTENER that wins the bind while `go run` is still
#      compiling is never adopted — dev.sh fails loudly and leaves it alone —
#      plus the mutation proof that ownership-by-port does kill it. The slow
#      compile is produced by a `go` shim on PATH, exactly as the review did;
#      dev.sh is never modified to simulate it.
#
# SAFETY DESIGN
#
# This test kills processes, so it must hold itself to the standard it is
# testing: it never `kill`s a pid it did not start, and never kills "whatever
# is on the port". Every kill either targets a pid this script forked, or a
# listener it has first proved is a descendant of the dev.sh it launched
# (`own_listeners_ready`). It also never asserts on a log line as a proxy for
# a condition — readiness lines are polled through to the condition itself,
# because "Go API server started" appearing is not the same as anything being
# bound (that confusion is repro B).
#
# All mutation runs against untracked private copies of scripts/dev.sh written
# into scripts/ itself — dev.sh self-locates its repo root from
# `dirname "${BASH_SOURCE[0]}"/..`, so a copy outside scripts/ resolves REPO to
# the wrong directory and cannot find go.mod or web/. The tracked file is only
# ever read; byte-identity is checked at the end via sha256, the same bookend
# testdb_gc_guard_test.sh (#0150) uses, and every mutant is removed by the EXIT
# trap.
#
# Every port used is a high, arbitrary number, verified free before use
# (CLAUDE.md §8b) — a part aborts rather than forcing through, since forcing
# would be exactly the hazard #0117 exists to prevent. :5173 (Vite's hardcoded
# dev port) is real and shared: the parts that reach Vite require it free and
# abort (not force) if it is not.
#
# Usage: scripts/dev_guard_test.sh
# Exit 0 = all guards hold. Exit 1 = a regression was detected (see FAIL lines).

set -uo pipefail   # NOT -e: several steps here are expected to fail — that's the assertion

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEVSH="$REPO/scripts/dev.sh"
WORKDIR="$(mktemp -d)"
MUTANT_GLOB="$REPO/scripts/.dev_guard_test_mutant.$$."
FAILURES=0
BG_PIDS=()        # background processes this script forked (holders, connectors)
GROUP_LEADERS=()  # dev.sh (or mutant) subprocess pids, each its own process-group leader

# #0253: every port below used to be a fixed literal (54301, 54311, ...,
# 54381), so two concurrent runs of this script were guaranteed to collide —
# CLAUDE.md §5a makes concurrent agents the norm. Derive a per-run base
# instead, using the project's existing ISSUE (falling back to $$, the same
# convention #0247 used for the restore drill) rather than inventing a second
# scheme. `cksum` gives a numeric hash regardless of whether ISSUE is
# numeric, so this never depends on the project's ISSUE=NNNN convention
# staying purely numeric. Each part still degrades detectably rather than
# corrupting anything if two runs' bases DO collide (every part checks
# port_bound first and fails/skips rather than forcing through) — this just
# makes a collision unlikely instead of certain. :5173 is Vite's own,
# unavoidable, hardcoded port and is left alone; the parts that reach it
# already require it free and abort otherwise.
RUNID="${ISSUE:-$$}"
RUNHASH="$(printf '%s' "$RUNID" | cksum | awk '{print $1}')"
PBASE=$(( 40000 + ( (RUNHASH % 200) * 100 ) ))   # 40000..59900, 200 non-overlapping 100-wide buckets
P1="$((PBASE + 1))"    # part 1
P2="$((PBASE + 11))"   # part 2
P3A="$((PBASE + 21))"  # part 3a
P3B="$((PBASE + 31))"  # part 3b
P4="$((PBASE + 41))"   # part 4
P5="$((PBASE + 51))"   # part 5
P6="$((PBASE + 61))"   # part 6
P7="$((PBASE + 71))"   # part 7
P8="$((PBASE + 81))"   # part 8

fail() { FAILURES=$((FAILURES + 1)); printf 'FAIL: %s\n' "$1" >&2; }
pass() { printf 'PASS: %s\n' "$1"; }
note() { printf '  [note] %s\n' "$1"; }

port_listeners() { lsof -ti tcp:"$1" -sTCP:LISTEN 2>/dev/null || true; }
port_endpoints() { lsof -ti tcp:"$1" 2>/dev/null || true; }   # listeners AND client connections
port_bound()     { [ -n "$(port_listeners "$1")" ]; }
# shellcheck disable=SC2329  # called indirectly as `wait_for N port_free <port>`
port_free()      { [ -z "$(port_listeners "$1")" ]; }
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

wait_pid_gone() {  # wait_pid_gone <pid> <timeout_seconds> — polls, force-kills the group on timeout
  # Polls rather than `wait $pid`: every pid this script tracks comes back
  # through a command substitution (`x="$(launch_group …)"`, `x="$(bind_holder
  # …)"`), which runs the launcher in a SUBSHELL — so the backgrounded process
  # is that subshell's child, not this script's, and `wait` on it returns
  # instantly with rc 127 instead of blocking. That exact mistake produced a
  # false pass here once (see #0117 verification notes); there are no `wait`
  # calls left in this file for that reason.
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

reap() {  # reap <pid> — kill a process this script forked, then wait for it to be gone
  [ -n "${1:-}" ] || return 0
  kill -9 "$1" >/dev/null 2>&1 || true
  wait_pid_gone "$1" 10 >/dev/null 2>&1 || true
}

# shellcheck disable=SC2329  # invoked indirectly via `trap cleanup EXIT` below
cleanup() {
  for pid in "${GROUP_LEADERS[@]:-}"; do
    [ -n "$pid" ] && kill -KILL -- -"$pid" >/dev/null 2>&1 || true
  done
  for pid in "${BG_PIDS[@]:-}"; do
    [ -n "$pid" ] && kill -9 "$pid" >/dev/null 2>&1 || true
  done
  rm -f "$MUTANT_GLOB"*.sh
  rm -rf "$WORKDIR"
}
trap cleanup EXIT

command -v python3 >/dev/null || { echo "error: python3 not on PATH" >&2; exit 1; }
command -v lsof >/dev/null    || { echo "error: lsof not on PATH" >&2; exit 1; }
command -v shasum >/dev/null  || { echo "error: shasum not on PATH" >&2; exit 1; }
command -v go >/dev/null      || { echo "error: go not on PATH" >&2; exit 1; }
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

bind_holder() {  # bind_holder <port> — a foreign LISTENER; echoes pid, registers it for cleanup
  local port="$1" pid
  python3 -m http.server "$port" --bind 127.0.0.1 >/dev/null 2>&1 &
  pid=$!
  BG_PIDS+=("$pid")
  echo "$pid"
}

bind_connector() {  # bind_connector <port> <flagfile> — a foreign CLIENT that holds an ESTABLISHED connection
  local port="$1" flag="$2" pid
  python3 -c '
import socket, sys, time
port, flag = int(sys.argv[1]), sys.argv[2]
deadline = time.time() + 120
s = None
while time.time() < deadline:
    try:
        s = socket.create_connection(("127.0.0.1", port), timeout=2)
        break
    except OSError:
        time.sleep(0.002)
if s is None:
    sys.exit(1)
open(flag, "w").write("connected\n")
time.sleep(600)
' "$port" "$flag" >/dev/null 2>&1 &
  pid=$!
  BG_PIDS+=("$pid")
  echo "$pid"
}

# shellcheck disable=SC2329  # used by own_listeners_ready, itself called indirectly via wait_for
descends_from() {  # descends_from <pid> <ancestor-pid> — bounded ancestry walk
  local anc="$1" target="$2" _hop
  for _hop in 1 2 3 4 5 6 7 8; do
    [ "$anc" = "$target" ] && return 0
    anc="$(ps -o ppid= -p "$anc" 2>/dev/null | tr -d ' ')"
    [ -z "$anc" ] && return 1
    [ "$anc" = "1" ] && return 1
  done
  return 1
}

OWN_LISTENERS=""
# shellcheck disable=SC2329  # called indirectly as `wait_for N own_listeners_ready <port> <pid>`
own_listeners_ready() {  # <port> <ancestor-pid> — true when a listener on <port> descends from <ancestor>; sets OWN_LISTENERS
  local port="$1" anc="$2" h out=""
  for h in $(port_listeners "$port"); do
    if descends_from "$h" "$anc"; then out="$out $h"; fi
  done
  OWN_LISTENERS="$out"
  [ -n "$out" ]
}

MUTANT_N=0
make_mutant() {  # make_mutant <markers> <sed-expr>... ; echoes the mutant path, or empty on failure
  local markers="$1"; shift
  local out args=() e m
  MUTANT_N=$((MUTANT_N + 1))
  out="${MUTANT_GLOB}${MUTANT_N}.sh"
  for e in "$@"; do args+=(-e "$e"); done
  sed "${args[@]}" "$DEVSH" > "$out" || { fail "mutant $MUTANT_N: sed failed"; return 1; }
  chmod +x "$out"
  for m in $markers; do
    if ! grep -q "$m" "$out"; then
      fail "mutant $MUTANT_N: mutation $m did not take effect — the line it targets moved or changed; sensitivity check aborted"
      return 1
    fi
  done
  if ! bash -n "$out"; then fail "mutant $MUTANT_N: syntax error"; return 1; fi
  echo "$out"
}

# The three mutations, each reverting one half of the #0117 fix to the exact
# shape that was proven to kill a stranger. The `$` sigils below belong to the
# mutant script, not to this shell, hence the single quotes.
# shellcheck disable=SC2016
M1='s%^  release_owned_port .*%  free_port "$PORT" 1 2>/dev/null || true   # MUTATED-M1: pre-#0117 unconditional backstop%'
# shellcheck disable=SC2016
M2='s%^  descendant_pids "\$1"$%  lsof -ti tcp:"$PORT" 2>/dev/null || true   # MUTATED-M2: pre-#0117 ownership inferred from the port%'
# shellcheck disable=SC2016
M3='s%^  current="\$(port_listeners "\$p")"$%  current="$(lsof -ti tcp:"$p" 2>/dev/null || true)"   # MUTATED-M3: unscoped, matches client endpoints too%'

echo "== Part 1: startup refusal — held \$PORT makes dev.sh exit 1, holder survives =="
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
  reap "$holder"
fi

echo "== Part 2: RECLAIM_PORTS=1 genuinely reclaims =="
if port_bound "$P2"; then
  fail "part2 setup: port $P2 unexpectedly already bound"
else
  holder="$(bind_holder "$P2")"
  if wait_for 10 port_bound "$P2"; then
    lg="$(launch_group "$WORKDIR/part2.log" "$DEVSH" "PORT=$P2" "RECLAIM_PORTS=1")"
    GROUP_LEADERS+=("$lg")
    # Poll for the CONDITION (a listener of ours on the port), not for the log
    # line that announces it — the log line does not imply anything is bound.
    if wait_for 90 own_listeners_ready "$P2" "$lg"; then
      if kill -0 "$holder" 2>/dev/null; then
        fail "REGRESSION #0117: RECLAIM_PORTS=1 did not reclaim $P2 — the original holder ($holder) is still alive"
      else
        pass "RECLAIM_PORTS=1 killed the original holder ($holder) on $P2"
      fi
      pass "the process now bound to $P2 (pid(s):$OWN_LISTENERS) descends from dev.sh's own group leader (pid $lg) — proof by ancestry, not a hopeful curl"
    else
      fail "part2: dev.sh under RECLAIM_PORTS=1 never got a listener of its own onto $P2 — log tail: $(tail -5 "$WORKDIR/part2.log" | tr '\n' '|')"
    fi
    kill -INT -- -"$lg" >/dev/null 2>&1 || true
    wait_pid_gone "$lg" 20
  else
    fail "part2 setup: holder never bound $P2"
  fi
  reap "$holder"
fi

echo "== Part 3a: ordinary shutdown — no interference, own ports released =="
if port_bound "$P3A" || port_bound 5173; then
  fail "part3a setup: port $P3A or 5173 unexpectedly already bound — cannot safely test against a busy shared port"
else
  lg="$(launch_group "$WORKDIR/part3a.log" "$DEVSH" "PORT=$P3A")"
  GROUP_LEADERS+=("$lg")
  if wait_for 90 own_listeners_ready "$P3A" "$lg" && wait_for 30 port_bound 5173; then
    kill -INT -- -"$lg" >/dev/null 2>&1
    wait_pid_gone "$lg" 20
    if wait_for 15 port_free "$P3A" && wait_for 15 port_free 5173; then
      pass "ordinary Ctrl-C released both $P3A and 5173 with dev.sh's own children all exited"
    else
      fail "REGRESSION #0117: after an ordinary Ctrl-C, $P3A or 5173 is still bound — the trap did not fully release its own ports"
    fi
  else
    fail "part3a setup: dev.sh never reached a ready state — log tail: $(tail -8 "$WORKDIR/part3a.log" | tr '\n' '|')"
    kill -KILL -- -"$lg" >/dev/null 2>&1 || true
  fi
fi

# devsh_fully_up <logfile> <port> <group-leader> — poll through to the state
# where dev.sh is past startup entirely: a listener of OURS on <port>, dev.sh's
# own readiness line printed, and Vite bound to 5173 (i.e. dev.sh is now
# sitting in its foreground `npm run dev`). Anything less and a "mid-session
# crash" would actually land inside dev.sh's startup, where it exits 1 on its
# own — which is a different code path from the one these scenarios exist to
# test, and it silently made the mutant look like a setup failure.
devsh_fully_up() {  # <logfile> <port> <group-leader>
  wait_for 90 own_listeners_ready "$2" "$3" || return 1
  wait_for 30 grep -q "Go API server started" "$1" || return 1
  wait_for 30 port_bound 5173 || return 1
  return 0
}

# ── Scenario: the Go server dies mid-session and a stranger takes the port ───
run_foreign_survives_scenario() {  # <label> <script-path> <go-port> -> sets RESULT
  local label="$1" script="$2" port="$3" lg foreign
  local logfile="$WORKDIR/${label}.log"
  RESULT="inconclusive"

  if port_bound "$port" || port_bound 5173; then
    note "[$label] SKIP setup: $port or 5173 already bound"; return
  fi

  lg="$(launch_group "$logfile" "$script" "PORT=$port")"
  GROUP_LEADERS+=("$lg")

  # Poll for OUR OWN listener — never for the readiness log line alone. The
  # kill below is aimed at exactly the pids proven to descend from the dev.sh
  # this function launched; aiming it at `lsof -ti tcp:$port` (as an earlier
  # draft did) would make this test kill a stranger on a slow compile.
  if ! devsh_fully_up "$logfile" "$port" "$lg"; then
    note "[$label] setup FAILED: dev.sh never got fully up on $port — log tail: $(tail -8 "$logfile" | tr '\n' '|')"
    kill -KILL -- -"$lg" >/dev/null 2>&1 || true
    return
  fi

  # Simulate the Go server dying mid-session (panic, stray pkill).
  # shellcheck disable=SC2086
  kill -9 $OWN_LISTENERS >/dev/null 2>&1 || true
  if ! wait_for 10 port_free "$port"; then
    note "[$label] setup FAILED: $port never freed after killing the simulated crash"
    kill -KILL -- -"$lg" >/dev/null 2>&1 || true
    return
  fi

  # A foreign process — standing in for another agent (CLAUDE.md §5a) — takes
  # over the now-free port.
  foreign="$(bind_holder "$port")"
  if ! wait_for 10 port_bound "$port"; then
    note "[$label] setup FAILED: foreign process never bound $port"
    reap "$foreign"
    kill -KILL -- -"$lg" >/dev/null 2>&1 || true
    return
  fi

  kill -INT -- -"$lg" >/dev/null 2>&1 || true    # ordinary Ctrl-C: SIGINT to the whole group
  wait_pid_gone "$lg" 20

  if kill -0 "$foreign" 2>/dev/null; then RESULT="survived"; else RESULT="killed"; fi
  note "[$label] foreign holder (pid $foreign) after EXIT trap: $RESULT"
  reap "$foreign"
}

echo "== Part 3b: foreign holder survives the EXIT trap (the #0117 review's own repro) =="
run_foreign_survives_scenario "part3b" "$DEVSH" "$P3B"
case "$RESULT" in
  survived) pass "a foreign process that took over :\$PORT after dev.sh's Go server died mid-session survived an ordinary Ctrl-C" ;;
  killed)   fail "REGRESSION #0117: the EXIT trap killed a foreign process it never started — this is the issue's own defect, on the shutdown path" ;;
  *)        fail "part3b: scenario setup was inconclusive — see note above; could not exercise the assertion" ;;
esac

echo "== Part 4: mutation proof for 3b — revert the EXIT-trap ownership check =="
if MUT="$(make_mutant 'MUTATED-M1' "$M1")"; then
  run_foreign_survives_scenario "part4" "$MUT" "$P4"
  case "$RESULT" in
    killed)   pass "with cleanup()'s ownership check reverted to the old unconditional backstop, the SAME scenario DOES kill the foreign holder — Part 3b is sensitive to the regression, not vacuously true" ;;
    survived) fail "mutation M1 was ineffective: the old unconditional free_port backstop still did not kill the foreign holder — Part 3b would not catch a real regression" ;;
    *)        fail "part4: mutant scenario setup was inconclusive — see note above" ;;
  esac
fi

# ── Repro A: a foreign CLIENT connection held across the ownership capture ───
run_connector_survives_scenario() {  # <label> <script-path> <go-port> -> sets RESULT
  local label="$1" script="$2" port="$3" lg conn flag
  local logfile="$WORKDIR/${label}.log"
  flag="$WORKDIR/${label}.connected"
  RESULT="inconclusive"

  if port_bound "$port" || port_bound 5173; then
    note "[$label] SKIP setup: $port or 5173 already bound"; return
  fi

  # The connector retries connect() every 2ms, so it is ESTABLISHED within
  # microseconds of dev.sh's server binding — i.e. before dev.sh records what
  # it owns, which is the whole point of repro A.
  conn="$(bind_connector "$port" "$flag")"
  lg="$(launch_group "$logfile" "$script" "PORT=$port")"
  GROUP_LEADERS+=("$lg")

  if ! devsh_fully_up "$logfile" "$port" "$lg"; then
    note "[$label] setup FAILED: dev.sh never got fully up on $port — log tail: $(tail -8 "$logfile" | tr '\n' '|')"
    reap "$conn"; kill -KILL -- -"$lg" >/dev/null 2>&1 || true
    return
  fi
  if ! wait_for 15 test -f "$flag"; then
    note "[$label] setup FAILED: the foreign connector never established a connection to $port"
    reap "$conn"; kill -KILL -- -"$lg" >/dev/null 2>&1 || true
    return
  fi
  if ! printf '%s\n' "$(port_endpoints "$port")" | grep -qx "$conn"; then
    note "[$label] setup FAILED: connector $conn is not among $port's endpoints"
    reap "$conn"; kill -KILL -- -"$lg" >/dev/null 2>&1 || true
    return
  fi

  kill -INT -- -"$lg" >/dev/null 2>&1 || true
  wait_pid_gone "$lg" 20

  if kill -0 "$conn" 2>/dev/null; then RESULT="survived"; else RESULT="killed"; fi
  note "[$label] foreign connector (pid $conn) after EXIT trap: $RESULT"
  reap "$conn"
}

echo "== Part 5: repro A — a foreign process merely CONNECTED to \$PORT is not 'ours' =="
run_connector_survives_scenario "part5" "$DEVSH" "$P5"
case "$RESULT" in
  survived)
    pass "a foreign process holding an ESTABLISHED connection to :\$PORT across the ownership capture survived an ordinary Ctrl-C" ;;
  killed)
    fail "REGRESSION #0117 (repro A): dev.sh kill -9ed a process that was only CONNECTED to its port — a browser tab or a curl is not 'own process'" ;;
  *)
    fail "part5: scenario setup was inconclusive — see note above" ;;
esac

echo "== Part 6: mutation proof for repro A — ownership by port + unscoped exit lookup =="
if MUT="$(make_mutant 'MUTATED-M2 MUTATED-M3' "$M2" "$M3")"; then
  attempt=0
  while [ "$attempt" -lt 3 ]; do
    attempt=$((attempt + 1))
    run_connector_survives_scenario "part6.$attempt" "$MUT" "$P6"
    [ "$RESULT" = "killed" ] && break
    [ "$attempt" -lt 3 ] && note "[part6] attempt $attempt did not enrol the connector (sub-millisecond capture race) — retrying"
  done
  case "$RESULT" in
    killed)   pass "with ownership taken from the port and the exit lookup unscoped, the SAME connector IS killed — Part 5 is sensitive to repro A, not vacuously true" ;;
    survived) fail "mutations M2+M3 were ineffective after 3 attempts: the connector still survived — Part 5 would not catch repro A" ;;
    *)        fail "part6: mutant scenario setup was inconclusive — see note above" ;;
  esac
fi

# ── Repro B: a foreign LISTENER wins the bind while `go run` is still building ─
SHIMDIR="$WORKDIR/shim"
mkdir -p "$SHIMDIR"
REAL_GO="$(command -v go)"
cat > "$SHIMDIR/go" <<SHIM
#!/bin/sh
# Stands in for a cold build cache: delay before the real \`go\` ever runs, so
# the Go server binds late. dev.sh itself is NOT modified to simulate this.
sleep 6
exec "$REAL_GO" "\$@"
SHIM
chmod +x "$SHIMDIR/go"

run_slow_build_scenario() {  # <label> <script-path> <go-port> -> sets RESULT
  local label="$1" script="$2" port="$3" lg foreign
  local logfile="$WORKDIR/${label}.log"
  RESULT="inconclusive"

  if port_bound "$port" || port_bound 5173; then
    note "[$label] SKIP setup: $port or 5173 already bound"; return
  fi

  lg="$(launch_group "$logfile" "$script" "PORT=$port" "PATH=$SHIMDIR:$PATH")"
  GROUP_LEADERS+=("$lg")

  # Bind the stranger only AFTER dev.sh has cleared its startup free_port
  # check — otherwise dev.sh would (correctly) refuse to start at all, and we
  # would be re-testing Part 1 instead of the readiness window.
  if ! wait_for 30 grep -q "Starting Go API server" "$logfile"; then
    note "[$label] setup FAILED: dev.sh never got past preflight — log tail: $(tail -8 "$logfile" | tr '\n' '|')"
    kill -KILL -- -"$lg" >/dev/null 2>&1 || true
    return
  fi
  foreign="$(bind_holder "$port")"
  if ! wait_for 10 port_bound "$port"; then
    note "[$label] setup FAILED: foreign process never bound $port"
    reap "$foreign"; kill -KILL -- -"$lg" >/dev/null 2>&1 || true
    return
  fi
  note "[$label] foreign listener $foreign won the bind while \`go run\` was still building"

  # Let the delayed build reach its failed bind, then Ctrl-C whatever is left.
  wait_for 120 grep -q "address already in use" "$logfile" || \
    note "[$label] (no 'address already in use' line yet — continuing)"
  kill -INT -- -"$lg" >/dev/null 2>&1 || true
  wait_pid_gone "$lg" 30

  if kill -0 "$foreign" 2>/dev/null; then RESULT="survived"; else RESULT="killed"; fi
  note "[$label] foreign listener (pid $foreign) after EXIT trap: $RESULT"
  reap "$foreign"
}

echo "== Part 7: repro B — a foreign listener that wins the bind during a slow build is not adopted =="
run_slow_build_scenario "part7" "$DEVSH" "$P7"
case "$RESULT" in
  survived)
    pass "a foreign listener that won the bind while \`go run\` was still compiling survived — dev.sh never adopted it"
    if grep -qE 'ERROR: Go server (exited unexpectedly|did not bind)' "$WORKDIR/part7.log"; then
      pass "dev.sh failed loudly instead of silently continuing with nothing of its own bound"
    else
      fail "part7: dev.sh did not report a startup failure even though it never bound $P7 — log tail: $(tail -8 "$WORKDIR/part7.log" | tr '\n' '|')"
    fi ;;
  killed)
    fail "REGRESSION #0117 (repro B): dev.sh kill -9ed the foreign listener that won the bind race — ownership is still being inferred from the port" ;;
  *)
    fail "part7: scenario setup was inconclusive — see note above" ;;
esac

echo "== Part 8: mutation proof for repro B — ownership by port =="
if MUT="$(make_mutant 'MUTATED-M2' "$M2")"; then
  run_slow_build_scenario "part8" "$MUT" "$P8"
  case "$RESULT" in
    killed)   pass "with ownership taken from the port, the SAME foreign listener IS killed — Part 7 is sensitive to repro B, not vacuously true" ;;
    survived) fail "mutation M2 was ineffective: the foreign listener survived even with ownership-by-port — Part 7 would not catch repro B" ;;
    *)        fail "part8: mutant scenario setup was inconclusive — see note above" ;;
  esac
fi

echo "== Byte-identity check =="
SHA_AFTER="$(shasum -a 256 "$DEVSH" | awk '{print $1}')"
if [ "$SHA_BEFORE" = "$SHA_AFTER" ]; then
  pass "scripts/dev.sh unchanged across the run (sha256 $SHA_AFTER) — all mutation happened on untracked private copies (${MUTANT_GLOB}*.sh, removed on exit), never the tracked file"
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
