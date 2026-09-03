#!/usr/bin/env bash
#
# browser_nav_guard_test.sh — the repeatable browser-level navigation
# regression check `#0287` asks for.
#
# `#0238` shipped a route-change effect (App.svelte) that sets
# document.title and moves focus to the new view's <h1> on every
# client-side navigation. Its first pass silently destroyed back/forward
# scroll restoration, because focus() defaults to `preventScroll: false`:
# the browser scrolled the heading into view AFTER router.ts's restoreScroll()
# had already put the page back where the user was, and the throttled scroll
# listener then stamped that bogus offset into history.state, so the damage
# outlived the moment. `document.title` and `document.activeElement` were
# CORRECT the whole time -- the two properties that were silently wrong
# (window.scrollY and history.state.scrollY after a Back) are exactly the two
# nothing checked. jsdom cannot evidence any of this: on a 5000px-tall jsdom
# document, h1.focus() with the default leaves window.scrollY at 0, identical
# to preventScroll: true -- zero evidence, not weak evidence (measured by
# #0238's implementation and independently by its review).
#
# This script drives a REAL browser (Safari, via safaridriver -- see "TOOL
# CHOICE" below) against a real production build and asserts all four of
# document.title / document.activeElement / window.scrollY /
# history.state.scrollY across all three navigation paths a route change can
# come from: a real <a> click (router.ts's click-interception path), a
# programmatic navigate() call from a <button onclick> (WorkshopDetail.svelte's
# "See all workshops", which is NOT an <a> and so does not exercise
# interception at all), and a popstate (Back).
#
# It is COMPLEMENTARY to, not a replacement for,
# web/src/lib/appNavigationFocus.structuralGuard.test.ts (#0238's AST guard,
# kept as-is per this issue's criterion 7): that guard pins the SOURCE
# invariant (the call site passes `{ preventScroll: true }`); this script
# pins the SYSTEM property ("Back restores window.scrollY and
# history.state.scrollY") against whatever the current source actually does
# at runtime -- so a future regression that breaks the property WITHOUT
# touching that exact call site (a second focus move added elsewhere, a
# scrollIntoView, a change to router.ts's restore timing) still gets caught
# here even though the AST guard would stay green.
#
# TOOL CHOICE (#0287 criterion 5): safaridriver, not Playwright.
# safaridriver ships with macOS (`/System/Cryptexes/App/usr/bin/safaridriver`)
# and needed NO install and NO sudo to drive a real WebDriver session on this
# machine -- confirmed empirically while writing this script: `safaridriver -p
# <port>` came up immediately and `POST /session` succeeded against a plain
# Safari.app launch, with no `safaridriver --enable` step and no manual
# "Allow Remote Automation" toggle needed (the `com.apple.Safari
# AllowRemoteAutomation` default is not even set on this machine). Cost: $0,
# 0 new files, 0 new dependencies. Playwright is NOT a web/package.json
# dependency today (only jsdom + vitest are) -- adding it would mean a new
# devDependency plus whatever `npx playwright install` needs beyond the
# chromium/ffmpeg builds already cached under
# ~/Library/Caches/ms-playwright, for a browser-diversity benefit this issue
# does not ask for. safaridriver is also what all four prior throwaway rigs
# this issue exists to stop re-hand-rolling used (#0063, #0244, #0238 x2), so
# it is the path already proven in this exact codebase.
#
# BUILD STRATEGY: rsync's `web/` (minus node_modules and dist) into a fresh
# temp directory rather than a `git worktree` -- this is deliberately NOT a
# git worktree. Two reasons: (1) it picks up the CURRENT working tree,
# including uncommitted edits -- an implementer running this right after
# editing App.svelte gets THAT edit tested, not last commit's; a git
# worktree checked out at HEAD would silently test stale source. (2) it
# sidesteps the whole git-worktree-cleanup risk class entirely (CLAUDE.md
# §8a/§8b, and #0399's note that a stray `.claude/worktrees/` copy makes
# internal/handlers's path-citation guard fail open) -- there is no worktree
# to forget to remove, because none is ever created. node_modules is
# symlinked from the real web/node_modules rather than reinstalled, the same
# shortcut #0238's own throwaway rigs used.
#
# PORTS (#0287 criteria 1, 3): both the static file server and safaridriver
# bind a per-run port derived from ISSUE (falling back to $$), verified free
# first via lsof, with the same collision-retry shape
# scripts/dev_guard_test.sh's pick_pbase() uses (#0253/#0259) -- so two
# concurrent runs of this script do not collide, and a stray unrelated
# listener already on a candidate port is skipped rather than fought.
# Ownership of the server actually answering is verified with a
# BUILD-SPECIFIC detail (the hashed bundle filename this run's own `npm run
# build` just produced), read back from `document.scripts` inside the
# browser -- not by trusting a bare curl 200 (CLAUDE.md §8b: "a curl that
# succeeds is not evidence the right server answered").
#
# CLEANUP (#0287 criterion 6): the EXIT trap deletes the WebDriver session,
# kills safaridriver and the static file server BY THE PIDS THIS SCRIPT
# recorded when it started them (never a pattern-matched `pkill -f` -- CLAUDE.md
# §5a records that pattern-scoped cleanup has twice reached past its owner
# and killed another agent's process), and removes the temp build directory.
# Safari.app itself is left running if this script did not have to launch it
# -- quitting a real user's browser is not this script's business.
#
# NOT wired into `scripts/check.sh`'s default/web/go/all targets, and
# deliberately not folded into `scripts/check.sh guards` either (#0287
# criterion 4) -- it is markedly heavier than any of that group (a real
# `npm run build`, a real browser session) and the "guards" precedent this
# issue cites is itself the existence of a SEPARATE target for something too
# slow for the default path, not a mandate to join that specific bucket.
# Run directly, or via `scripts/check.sh browser-nav`.
#
# Usage:
#   scripts/browser_nav_guard_test.sh
#   ISSUE=0287 scripts/browser_nav_guard_test.sh   # derives this run's ports
#
# Exit 0 = every assertion held. Exit 1 = at least one FAIL line was printed
# (see the FAIL: lines for which one and why) or setup itself failed.

set -uo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO" || exit 1

FAILURES=0
fail() { FAILURES=$((FAILURES + 1)); printf 'FAIL: %s\n' "$1" >&2; }
pass() { printf 'PASS: %s\n' "$1"; }
note() { printf '  [note] %s\n' "$1"; }
step() { printf '\n\033[1m=== %s\033[0m\n' "$1"; }

# --- tool check (CLAUDE.md §5b: surface a missing obstacle, don't paper over it) ---
for tool in rsync python3 curl jq safaridriver npm; do
  command -v "$tool" >/dev/null 2>&1 || {
    echo "error: required tool '$tool' not found on PATH -- cannot run this check." >&2
    exit 1
  }
done

if [ ! -d "$REPO/web/node_modules" ]; then
  echo "error: web/node_modules is missing. Run 'npm install' in web/ first --" \
       "this script symlinks it rather than reinstalling, to keep the common case fast." >&2
  exit 1
fi

# --- port selection: derive a per-run base, verified free, collision-retry ---
# Same shape as scripts/dev_guard_test.sh's pick_pbase() (#0253/#0259): a
# deterministic base from RUNID so a human-diffable, reproducible-by-default
# run is the common case, with bounded retries against a genuinely occupied
# candidate rather than fighting it. Range 20000..39900 is below macOS's
# ephemeral-port floor and clear of the real listeners #0259 found occupying
# the old 40000..59900 range on this machine.
port_listeners() { lsof -ti tcp:"$1" -sTCP:LISTEN 2>/dev/null || true; }
port_bound()     { [ -n "$(port_listeners "$1")" ]; }

pick_pbase() {
  local seed hash base collision attempt=0
  while [ "$attempt" -lt 50 ]; do
    seed="${RUNID}:${attempt}"
    hash="$(printf '%s' "$seed" | cksum | awk '{print $1}')"
    base=$(( 20000 + ( (hash % 200) * 100 ) ))
    collision=0
    for off in 1 11; do
      if port_bound "$((base + off))"; then collision=1; break; fi
    done
    if [ "$collision" -eq 0 ]; then printf '%s\n' "$base"; return 0; fi
    attempt=$((attempt + 1))
  done
  return 1
}

RUNID="${ISSUE:-$$}"
PBASE="$(pick_pbase)" || {
  echo "error: could not find a free port pair after 50 attempts." >&2
  exit 1
}
SERVER_PORT="$((PBASE + 1))"
DRIVER_PORT="$((PBASE + 11))"
NOTFOUND_SLUG="guard-nav-check-missing-workshop-${RUNID}"

# --- state torn down by the cleanup trap ---
BUILD_DIR=""
SERVER_PID=""
DRIVER_PID=""
SID=""
WD_BASE=""

cleanup() {
  set +e
  if [ -n "$SID" ] && [ -n "$WD_BASE" ]; then
    curl -s -X DELETE "$WD_BASE" >/dev/null 2>&1
  fi
  if [ -n "$DRIVER_PID" ]; then
    kill "$DRIVER_PID" >/dev/null 2>&1
    wait "$DRIVER_PID" 2>/dev/null
  fi
  if [ -n "$SERVER_PID" ]; then
    kill "$SERVER_PID" >/dev/null 2>&1
    wait "$SERVER_PID" 2>/dev/null
  fi
  if [ -n "$BUILD_DIR" ] && [ -d "$BUILD_DIR" ]; then
    rm -rf "$BUILD_DIR"
  fi
}
trap cleanup EXIT INT TERM

# --- build the SPA against the CURRENT working tree (see BUILD STRATEGY above) ---
step "Building the SPA (rsync + npm run build, not the main tree's web/dist)"
BUILD_DIR="$(mktemp -d)"
rsync -a --exclude 'node_modules' --exclude 'dist' "$REPO/web/" "$BUILD_DIR/web/"
ln -s "$REPO/web/node_modules" "$BUILD_DIR/web/node_modules"
if ! ( cd "$BUILD_DIR/web" && npm run build > "$BUILD_DIR/build.log" 2>&1 ); then
  echo "error: npm run build failed:" >&2
  tail -60 "$BUILD_DIR/build.log" >&2
  exit 1
fi
DIST="$BUILD_DIR/web/dist"
BUNDLE_JS="$(grep -o 'assets/index-[A-Za-z0-9_-]*\.js' "$DIST/index.html" | head -1)"
if [ -z "$BUNDLE_JS" ]; then
  echo "error: could not find a hashed assets/index-*.js reference in the built index.html" >&2
  exit 1
fi
note "built bundle: $BUNDLE_JS"

# --- serve the build on the per-run port, with a couple of API stubs ---
step "Starting the static/stub server on 127.0.0.1:$SERVER_PORT"
STUB_SERVER_PY="$BUILD_DIR/stub_server.py"
cat > "$STUB_SERVER_PY" <<'PYEOF'
import http.server
import os
import sys

DIST = sys.argv[1]
PORT = int(sys.argv[2])
NOTFOUND_SLUG = sys.argv[3]


class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        pass

    def do_GET(self):
        path = self.path.split("?", 1)[0]
        if path == "/api/me":
            self.send_response(401)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(b'{"error":"unauthenticated"}')
            return
        if path == "/api/workshops/" + NOTFOUND_SLUG:
            self.send_response(404)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(b'{"error":"not found"}')
            return
        local = os.path.join(DIST, path.lstrip("/"))
        if path != "/" and os.path.isfile(local):
            self.send_response(200)
            if local.endswith(".js"):
                self.send_header("Content-Type", "application/javascript")
            elif local.endswith(".css"):
                self.send_header("Content-Type", "text/css")
            self.end_headers()
            with open(local, "rb") as f:
                self.wfile.write(f.read())
            return
        # SPA fallback -- every route not matched above is a client-side path.
        with open(os.path.join(DIST, "index.html"), "rb") as f:
            body = f.read()
        self.send_response(200)
        self.send_header("Content-Type", "text/html")
        self.end_headers()
        self.wfile.write(body)


if __name__ == "__main__":
    srv = http.server.ThreadingHTTPServer(("127.0.0.1", PORT), Handler)
    srv.serve_forever()
PYEOF

python3 "$STUB_SERVER_PY" "$DIST" "$SERVER_PORT" "$NOTFOUND_SLUG" &
SERVER_PID=$!

for _ in $(seq 1 50); do
  port_bound "$SERVER_PORT" && break
  sleep 0.1
done
if ! port_bound "$SERVER_PORT"; then
  echo "error: stub server never bound :$SERVER_PORT" >&2
  exit 1
fi
note "stub server pid $SERVER_PID bound to :$SERVER_PORT"

# --- start safaridriver ---
step "Starting safaridriver on 127.0.0.1:$DRIVER_PORT"
# Safari must actually be running for safaridriver to pair with it -- an
# already-quit Safari.app made the first session request time out while
# writing this script; launching it first (a no-op if it is already open)
# fixed that.
open -a Safari >/dev/null 2>&1 || true

safaridriver -p "$DRIVER_PORT" >"$BUILD_DIR/safaridriver.log" 2>&1 &
DRIVER_PID=$!

DRIVER_READY=0
for _ in $(seq 1 50); do
  if curl -s "http://127.0.0.1:$DRIVER_PORT/status" 2>/dev/null | grep -q '"ready":true'; then
    DRIVER_READY=1
    break
  fi
  sleep 0.2
done
if [ "$DRIVER_READY" -ne 1 ]; then
  echo "error: safaridriver never reported ready on :$DRIVER_PORT" >&2
  cat "$BUILD_DIR/safaridriver.log" >&2
  exit 1
fi
note "safaridriver pid $DRIVER_PID ready on :$DRIVER_PORT"

# --- WebDriver session ---
SESSION_RESP="$(curl -s -X POST "http://127.0.0.1:$DRIVER_PORT/session" \
  -H 'Content-Type: application/json' -d '{"capabilities":{}}' --max-time 30)"
SID="$(echo "$SESSION_RESP" | jq -r '.value.sessionId // empty')"
if [ -z "$SID" ]; then
  echo "error: could not create a WebDriver session: $SESSION_RESP" >&2
  exit 1
fi
WD_BASE="http://127.0.0.1:$DRIVER_PORT/session/$SID"
note "WebDriver session $SID"

wd_exec() {
  # $1 = JS source (a function body; use `return ...`). Optional $2 = a JSON
  # array literal for `args` (defaults to "[]"); reference arguments[N] in
  # the JS to use them, rather than string-interpolating values into the
  # script text.
  local js="$1" argsjson="${2:-[]}"
  local body
  body="$(jq -n --arg s "$js" --argjson a "$argsjson" '{script:$s, args:$a}')"
  curl -s -X POST "$WD_BASE/execute/sync" -H 'Content-Type: application/json' \
    -d "$body" --max-time 15
}

wd_nav() {
  curl -s -X POST "$WD_BASE/url" -H 'Content-Type: application/json' \
    -d "$(jq -n --arg u "$1" '{url:$u}')" --max-time 20 >/dev/null
}

wd_back() {
  curl -s -X POST "$WD_BASE/back" -H 'Content-Type: application/json' \
    -d '{}' --max-time 15 >/dev/null
}

assert_eq() {
  local label="$1" actual="$2" expected="$3"
  if [ "$actual" = "$expected" ]; then
    pass "$label (got: $actual)"
  else
    fail "$label -- expected [$expected], got [$actual]"
  fi
}

assert_contains() {
  local label="$1" haystack="$2" needle="$3"
  case "$haystack" in
    *"$needle"*) pass "$label (got: $haystack)" ;;
    *) fail "$label -- expected to contain [$needle], got [$haystack]" ;;
  esac
}

# One capture, all four properties #0287 criterion 2 asks for in one round
# trip. Deliberately returns a plain object (not JSON.stringify'd) so
# WebDriver's own JSON serialization does the encoding -- no manual escaping.
CAPTURE_JS="$(cat <<'JS'
var el = document.activeElement;
return {
  title: document.title,
  activeTag: el ? el.tagName : null,
  activeText: el ? (el.textContent || '').trim().slice(0, 80) : null,
  scrollY: window.scrollY,
  stateScrollY: (window.history.state && typeof window.history.state.scrollY === 'number')
    ? window.history.state.scrollY
    : null
};
JS
)"

assert_nav_snapshot() {
  local label="$1" want_title_substr="$2" want_tag="$3" want_text_substr="$4" want_scrolly="$5" want_state="$6"
  local resp value title tag text scrolly state
  resp="$(wd_exec "$CAPTURE_JS")"
  value="$(echo "$resp" | jq -c '.value // empty')"
  if [ -z "$value" ] || [ "$value" = "null" ]; then
    fail "$label: execute/sync returned no usable value ($resp)"
    return
  fi
  title="$(echo "$value" | jq -r '.title')"
  tag="$(echo "$value" | jq -r '.activeTag')"
  text="$(echo "$value" | jq -r '.activeText')"
  scrolly="$(echo "$value" | jq -r '.scrollY')"
  state="$(echo "$value" | jq -r '.stateScrollY')"

  assert_contains "$label: document.title"         "$title" "$want_title_substr"
  assert_eq       "$label: document.activeElement" "$tag"   "$want_tag"
  assert_contains "$label: activeElement text"      "$text" "$want_text_substr"
  assert_eq       "$label: window.scrollY"          "$scrolly" "$want_scrolly"
  assert_eq       "$label: history.state.scrollY"   "$state"   "$want_state"
}

# --- Test sequence ------------------------------------------------------
step "Navigating to /privacy and confirming server identity"
wd_nav "http://127.0.0.1:$SERVER_PORT/privacy"
sleep 0.3

OWN_CHECK_JS="$(cat <<'JS'
var bundle = arguments[0];
return Array.from(document.scripts).some(function (s) { return s.src.indexOf(bundle) !== -1; });
JS
)"
OWN_RESP="$(wd_exec "$OWN_CHECK_JS" "[\"$BUNDLE_JS\"]")"
OWN_VALUE="$(echo "$OWN_RESP" | jq -r '.value')"
# CLAUDE.md §8b: a curl 200 is not evidence the right server answered. This
# is: the exact hashed bundle filename THIS run's own npm run build just
# produced, read back from document.scripts inside the actual browser page.
if [ "$OWN_VALUE" = "true" ]; then
  pass "server identity: page's document.scripts references this run's own bundle ($BUNDLE_JS)"
else
  fail "server identity: document.scripts does NOT reference $BUNDLE_JS -- talking to the wrong server ($OWN_RESP)"
fi

step "Setting up scroll state on /privacy (tall body, scroll to 1200, let the throttled listener stamp it)"
SETUP_JS="$(cat <<'JS'
document.body.style.minHeight = '5000px';
window.scrollTo(0, 1200);
return window.scrollY;
JS
)"
wd_exec "$SETUP_JS" >/dev/null
sleep 1   # SCROLL_SAVE_INTERVAL_MS is 400ms (router.ts); give it comfortable room

STATE_CHECK_JS='return (window.history.state && typeof window.history.state.scrollY === "number") ? window.history.state.scrollY : null;'
PRE_STATE="$(wd_exec "$STATE_CHECK_JS" | jq -r '.value')"
assert_eq "setup: history.state.scrollY stamped by the throttled scroll listener" "$PRE_STATE" "1200"

step "Path 1/3 -- link click (a real <a>, exercises router.ts's click-interception path)"
CLICK_HOME_JS="$(cat <<'JS'
var a = document.querySelector('header a[href="/"]');
if (!a) return false;
a.click();
return true;
JS
)"
CLICKED="$(wd_exec "$CLICK_HOME_JS" | jq -r '.value')"
if [ "$CLICKED" != "true" ]; then
  fail "link click: could not find header a[href=\"/\"] to click"
else
  sleep 0.3
  assert_nav_snapshot "link click -> /" \
    "Hands-on electronics workshops" "H1" "Hands-on electronics workshops" "0" "0"
fi

step "Path 2/3 -- popstate (Back to /privacy) -- THE #0238 regression lives here"
wd_back
sleep 1.5   # RESTORE_DEADLINE_MS is 1000ms (router.ts); give the rAF retry loop room to finish
assert_nav_snapshot "popstate (Back) -> /privacy" \
  "Privacy Policy" "H1" "Privacy Policy" "1200" "1200"

step "Path 3/3 -- programmatic navigate() (a <button onclick>, NOT an <a> -- does not touch click-interception)"
wd_nav "http://127.0.0.1:$SERVER_PORT/workshops/$NOTFOUND_SLUG"
sleep 0.5
CLICK_BUTTON_JS="$(cat <<'JS'
var btns = document.querySelectorAll('button');
for (var i = 0; i < btns.length; i++) {
  if (btns[i].textContent.indexOf('See all workshops') !== -1) {
    btns[i].click();
    return true;
  }
}
return false;
JS
)"
CLICKED2="$(wd_exec "$CLICK_BUTTON_JS" | jq -r '.value')"
if [ "$CLICKED2" != "true" ]; then
  fail "programmatic navigate(): could not find the 'See all workshops' button on the not-found workshop view"
else
  sleep 0.3
  assert_nav_snapshot "navigate() -> /workshops" \
    "Workshops" "H1" "Workshops" "0" "0"
fi

# --- verdict ---
step "Verdict"
if [ "$FAILURES" -eq 0 ]; then
  echo "ALL PASSED (0 failures)"
  exit 0
else
  echo "$FAILURES FAILURE(S) -- see FAIL: lines above"
  exit 1
fi
