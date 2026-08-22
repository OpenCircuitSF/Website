# Obstacles — what stops work on an issue, and how to clear it

Most time lost on this project has gone to **obstacles**, not to the work.
An obstacle is not a hard problem in the code; it is a condition of the
environment that makes progress impossible or, worse, makes failure look like
success. They share three properties:

1. **They are knowable before the work starts.** Every one below could have been
   detected with a command that takes under a second.
2. **They are cheap to clear early and expensive to hit late.** An agent
   halfway through a change has two bad options — stop with the work
   half-done, or work around it. The workaround is usually worse.
3. **They recur.** An obstacle that is routed around rather than cleared is
   rediscovered by the next agent, at full price.

This document is the catalogue. It exists so a planning pass can scan it and
write a `## Obstacles` block, and so an orchestrator can clear what it names
**before** dispatching. The binding rules live in
[`CLAUDE.md`](../CLAUDE.md) §5b and [`issues/Issues.md`](../issues/Issues.md);
this is the evidence behind them.

**Provenance.** Entries marked *recorded* come from the tracker or `CLAUDE.md`'s
incident log. Entries marked *observed* were hit directly on 2026-08-21/22 and
are written from what happened, not from recollection.

---

## 1. A missing privilege, worked around instead of cleared

*Observed.* An implementer on `#0122` needed a private database for a
guard-removal test, found the `opencircuit` role could not `CREATEDB`, and
finished the work over a superuser connection. The issue resolved. The missing
grant stayed missing, so the next agent to need a database hit exactly the same
wall — and that is how a one-time obstacle becomes a permanent tax.

| | |
|---|---|
| **Signal** | `ERROR: permission denied to create database` |
| **Check** | `psql -tAc "select rolcreatedb from pg_roles where rolname=current_user"` |
| **Clear** | `psql -d postgres -c 'ALTER ROLE opencircuit CREATEDB;'` (undo: `NOCREATEDB`) |
| **Now guarded by** | `scripts/testdb.sh` refuses to run and prints the grant command rather than falling back |

**The rule this produced:** never paper over a missing permission. Surface it,
clear it once, record that it is cleared. A workaround that completes the work
while hiding the obstacle is a worse outcome than stopping.

---

## 2. An environment that is silently behind

*Observed.* Asked whether the site was demo-ready, the local `opencircuit`
database turned out to be at **migration 11 of 19** with **no admin user**. The
service booted, `/health` returned `{"status":"ok","db":"ok"}`, and the public
pages rendered — so nothing looked wrong until a request touched a table that
did not exist. A demo would have failed live, on the first campaign screen.

| | |
|---|---|
| **Signal** | none. This is the danger — health checks pass |
| **Check** | `psql "$DSN" -tAc 'select version, dirty from schema_migrations'` against `ls migrations/*.up.sql \| tail -1` |
| **Clear** | `scripts/db-reset.sh` (rebuild + seed) or `migrate -path migrations -database "$DSN" up` |
| **Now guarded by** | `scripts/testdb.sh` refuses to hand out a clone when the template is behind `migrations/` |

---

## 3. Verification that runs green and proves nothing

*Recorded, `CLAUDE.md` §5.* The DB-backed suites skip silently when
`TEST_DATABASE_URL` is unset, so `go test ./...` exits 0 having tested nothing.
This bit `#0002`, which verified clean and left broken `TRUNCATE` statements
that only surfaced two issues later when `#0004` stood up a real database.

A related shape: a package that reports `ok` in 0.00s did not talk to a
database, and `[no test files]` is not a pass.

| | |
|---|---|
| **Signal** | a suspiciously fast green run; `ok ... 0.00s`; no skip notices read |
| **Check** | read the output, not the exit code |
| **Clear** | `scripts/check.sh`, which exports the DSN and audits the run for packages that reported no tests |

**This is the most dangerous class in the catalogue**, because it does not stop
work — it lets work finish and be wrong.

---

## 4. Shared mutable resources

*Recorded, `CLAUDE.md` §8b.* Three distinct incidents, one cause.

- **A port.** A subagent started a server on `:8099`, its own process died with
  `bind: address already in use`, and `curl` returned 200 — from a *different
  agent's server*. It came within one step of proving its fix against someone
  else's build.
- **The shared test database.** `#0024`'s guard-removal proof aimed a `DELETE`
  at a hardcoded `/admin/interests/1`, the guard was gone by design, and it
  permanently destroyed a seeded taxonomy row. Caught only because the agent
  checked a row count afterwards.
- **Ambient state between tests.** `#0121`: three tests asserted exact
  whole-table counts and passed only because Go's sorted file order happened to
  run them first. Its review then found a *second* instance one table over.

| | |
|---|---|
| **Signal** | a passing check you cannot attribute to your own build; a test that passes only in one order |
| **Check** | verify the server answering is yours by confirming a build-specific detail (a hashed asset name, a string you just changed) |
| **Clear** | pick an unlikely port; `ISSUE=NNNN scripts/check.sh` for a private database; never target a literal or seeded row id |

---

## 5. A test suite too slow to run

*Recorded, `#0091`, `#0096` (wontfix).* `internal/auth` reached ~103s alone and
the full suite ~20 minutes. Reviewers are told to run it before a change, after,
and once per mutation — so a three-mutation review paid eight minutes of pure
waiting. The predictable result, in the issue's own words: *"Every reviewer and
implementer now either waits ~20 minutes or skips the full run."*

An obstacle that makes the correct process expensive does not get followed; it
gets skipped, quietly.

| | |
|---|---|
| **Clear** | scope the run (`scripts/check.sh go ./internal/handlers/...`); full suite is for a batch's single review pass |

---

## 6. Machine load misdiagnosed as flaky tests

*Recorded, `CLAUDE.md` §5.* `#0084`, `#0087`, `#0091`, `#0096`, `#0099` were all
filed against timing failures produced by ~200 concurrent agent test runs
peaking at **load average 320**. A day went into widening deadlines from 5s to
20s so that `TRUNCATE` on a *local* database would stop timing out. The
statements were never slow. Two of those issues closed `wontfix`.

**And the inverse, observed this session:** `#0121`'s first pass attributed a
failure to load at ~7.6. The reviewer reproduced it deterministically at load
2.55 with nothing else running. The distinction that resolves both:

> §5's load guidance covers **timing** failures. A **state** assertion — a
> missing error code, a wrong count — has no deadline in it, so load was never a
> candidate explanation.

| | |
|---|---|
| **Check** | `uptime` first, every time. Idle here is under ~4; above ~8 something else is running |
| **Clear** | wait for it to drain. Do not reproduce under load, do not record a load average as a baseline, and never widen a deadline to accommodate it |

---

## 7. Unbounded search and unbounded output

*Recorded, `CLAUDE.md` §5.* A subagent ran `find / -type d -name 'go-webauthn'`
and it was **still running 28 minutes later**. Ask the toolchain instead —
`go list -m -f '{{.Dir}}'` answers in milliseconds.

The same class: a full `go test ./...` emits more tokens than the entire PRD,
and the answer is the last few lines. Bound it or scope it.

---

## 8. Destructive git against another agent's work

*Recorded, `CLAUDE.md` §8a.* A reviewer ran `git checkout -- issues/0068.md` to
undo its own edit, destroyed the implementer's uncommitted resolution notes, and
reconstructed them by hand. It disclosed this, which is the only reason it was
caught. At that moment a second subagent had uncommitted edits in `web/` and
`issues/0011.md`; a slightly broader path would have taken those too.

Uncommitted changes have no reflog and no recovery path.

| | |
|---|---|
| **Clear** | to undo your own edit, rewrite the file, or `cp` it aside first. Stage narrowly — never `git add -A` |

---

## 9. A surface nothing can test

*Recorded, `#0094`.* No test in this repository imports a `.svelte` file — there
is no jsdom and no component harness. So an acceptance criterion reading
"component tests pass" **cannot be met as written**, and an agent that ticks it
has recorded coverage that does not exist.

This surfaced twice: `#0049` carried that criterion and its reviewer had already
written the substitution into the issue; `#0119`'s fix was for a defect
(a dangling `aria-describedby`) that renders fine, screenshots fine, and
announces nothing — invisible to every test that exists.

| | |
|---|---|
| **Clear** | put the decision in a pure `web/src/lib/` module with vitest coverage; keep the component to markup and wiring |
| **Say so** | when a criterion cannot be met as written, state that and what you did instead. Do not tick it |

---

## 10. Credentials and services that do not exist yet

*Recorded, `CLAUDE.md` §10.* SES has never been set up: no verified domain, no
DKIM, no production access. So **no issue requiring a real send can be verified
today**. `MAILER_NOOP=true` logs mail instead of sending and the send worker
does not start at all, which means live send progress and the SSE stream have
nothing to drive them.

Two more of the same shape: no physical mailing address (`#0045` refuses to
start a campaign without one, by design), and no decision on who reads
`hello@opencircuitsf.com` — which `#0075`'s published privacy policy already
commits to.

**The obstacle is not the missing credential. It is discovering it at the
verification step**, after the work is done, instead of naming it in the plan.

---

## 11. A run mode that silently lacks the subsystem

*Observed.* `./scripts/dev.sh` defaults to `STORAGE=json`, and
`internal/devstore` implements none of interests, subscribers, campaigns, or
suppressions. In that mode `/api/interests` 404s, `POST /api/subscribe` returns
405, and the mailing admin routes are absent from the route table entirely.

The service starts, logs no error, and serves the marketing pages perfectly. An
agent verifying mailing work this way would prove nothing and not notice.

| | |
|---|---|
| **Clear** | run against Postgres for anything touching the mailing subsystem |

---

## 12. Context spent on discovery instead of work

*Observed.* The orchestrator was enumerating the tracker by reading issue
files — over a hundred of them — to decide what to work next.
`scripts/list-issues-by-phase --open --no-color` answers the same question in
~35 lines, grouped by phase, in phase order. It already existed.

The same session dropped two standing conventions without noticing: **six of
seven issues got no `## Work log`** (~2.5M tokens across 20 subagents went
unrecorded, while every issue before `#0013` carried full cost rows), and
`issues/model-pricing.json` was never created despite `CLAUDE.md` requiring it
from the first dispatch.

A process step that is skipped silently is indistinguishable from one that does
not exist.

---

## 13. A requirement that is wrong in the issue

*Recorded.* `#0064`'s acceptance criteria say the vhost should redirect
`www` → apex. Production does the opposite, and `WEBAUTHN_RP_ORIGIN` depends on
`www` staying canonical — so implementing the criterion as written would break
passkey login. `CLAUDE.md` §7 flags it for correction before implementation.

An issue is not automatically right. When a criterion contradicts a verified
fact, say so, correct it, and record why — do not implement it and do not
silently drop it.

---

## Quick pre-dispatch scan

Under a second, and it catches most of the above:

```bash
uptime                                    # §6 — idle is under ~4 here
command -v migrate psql npm go            # §7 — ask the toolchain, never find /
psql "$PGURL/postgres" -tAc "select rolcreatedb from pg_roles where rolname=current_user"   # §1
scripts/testdb.sh template                # §2 — refuses if migrations moved
git status --short                        # §8 — whose uncommitted work is here?
scripts/list-issues-by-phase --open --no-color   # §12
```

Then ask the two questions the catalogue keeps answering:

- **Can this issue actually be verified today?** (§3, §9, §10, §11)
- **What am I sharing with another agent?** (§4)
