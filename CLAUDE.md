# CLAUDE.md — Open Circuit SF

Binding guidance for any agent working in this repository. Read this before
touching code.

**Authoritative sources, in order:**

| For | Read |
|---|---|
| Issue workflow — statuses, the plan → implement → review pipeline, commit conventions, work logs | [`issues/Issues.md`](issues/Issues.md) |
| Product scope, schema, flows, brand, infrastructure | [`PRD.md`](PRD.md) — **extract the one section you need** (§11), never read it whole |
| Current state, environment prerequisites, hard-won findings | [`HANDOFF.md`](HANDOFF.md) |
| Code and repo conventions (this file) | here |

If `CLAUDE.md` and `issues/Issues.md` disagree, prefer this file for code and
repo conventions and `Issues.md` for issue-tracking specifics.

---

## 1. What this is

A Go service with an embedded Svelte 5 SPA serving `opencircuitsf.com`: public
marketing pages, workshop listings, an interest-segmented mailing list (double
opt-in, preference center, unsubscribe handling, campaign sending over AWS SES),
and a passkey-authenticated admin console. PostgreSQL on EC2 behind Apache.

The link shortener at `go.opencircuitsf.com` is a **separate deploy of
ShortLinks**. No shortener code belongs in this repo, and its issues do not
belong in this tracker.

### Repository layout

| Path | Contains |
|---|---|
| `cmd/opencircuit/` | Binary entry point — `serve` and `seed` subcommands, the route table |
| `internal/` | `config`, `db`, `auth`, `audit`, `events`, `devstore`, `handlers`, `middleware`, `testdb` — per-package roles in [`docs/architecture.md`](docs/architecture.md) |
| `migrations/` | `golang-migrate` up/down SQL pairs, contiguous `000001`–`000019` |
| `web/` | Svelte 5 SPA; built to `web/dist/` and embedded via `//go:embed all:dist` |
| `scripts/` | `list-issues-by-phase` (the queue), `check.sh` (canonical verification), `testdb.sh` (per-agent test databases), `db-reset.sh` (rebuild a local DB), `dev.sh`, `sim.sh`, `deploy.sh`, `db-status.sh`, `db/` |
| `deploy/` | Apache vhost and systemd unit assets |
| `docs/` | Per-subsystem documentation, indexed by [`docs/README.md`](docs/README.md) |
| `assets/`, `placeholder/` | Logo and brand assets; the static site currently in production |
| `issues/` | The tracker — `NNNN.md` files plus `Issues.md` |

### Code conventions

- Standard-library `net/http` with Go 1.22+ pattern routing. No web framework.
- Handlers depend on **narrow store interfaces**, never a concrete store — that
  is what lets `internal/devstore` stand in for Postgres under `STORAGE=json`.
- SQL lives in the store package that owns the table, not in handlers.
- **Migrations are append-only — *after* the first production deploy.** Never
  edit a migration that has been applied to a database anyone cares about; add a
  new one. `migrations/000007` exists because ShortLinks broke this rule once.
  **Until then, this project is greenfield** (decided 2026-08-21): nothing is
  live but the static placeholder, there is no PostgreSQL instance on AWS
  holding real data, and no subscriber or campaign has ever existed. So
  `migrations/` may be squashed, renumbered, or rewritten as a deliberate act,
  and the dev database may be dropped and recreated. Prefer editing the
  migration that owns a table over stacking an `ALTER TABLE`, and write no
  backfill for data that does not exist. The exception expires at the first
  deploy that applies a migration to production — see PRD §6.2's greenfield
  note.
- Tests sit beside the code as `_test.go`. DB-backed tests gate on
  `TEST_DATABASE_URL` and skip when it is unset — so a green `go test ./...`
  with that variable unset proves less than it looks like it does.
- SPA logic goes in plain TypeScript modules under `web/src/lib/` so it is
  unit-testable without a DOM; Svelte components stay thin.

## 2. Identity — decided, do not re-litigate

```
module github.com/brennanMKE/OpenCircuitSF
binary  cmd/opencircuit          → /usr/local/bin/opencircuit
service opencircuit.service
```

The GitHub repo is being renamed `Website` → `OpenCircuitSF`. Until that rename
lands, `git remote -v` will still show `Website`; the module path above is
correct regardless and is what `#0001` writes into `go.mod`.

## 3. Model policy — cost is a first-class constraint

**Fable is not used in the per-issue pipeline.** Opus replaces it wherever
`Issues.md` previously named the top model.

**Fable is available for occasional, limited review tasks** (user's instruction,
2026-08-19). The clear case is a **whole-implementation review**: reading the
finished system rather than one diff, writing notes, and filing new issues for
concerns and recommended improvements. That is a genuinely different task from
reviewing a single change, and where the extra capability earns its cost.

The boundary: Fable is for occasional standalone reviews, **not** a step in the
plan → implement → review loop. Per-issue planning stays on Opus, implementation
on Sonnet, per-issue review on Opus. Reach for Fable when the task is one review
spanning many issues, not when it is one review of one issue.

| Phase | Model | Runs as |
|---|---|---|
| Orchestration (queue, dispatch, usage accounting) | Opus | the main session |
| 1 — Planning | **Opus** (`claude-opus-5`) | fresh subagent |
| 2 — Implementation | **Sonnet** (`claude-sonnet-5`) | fresh subagent |
| 3 — Review | **Opus** (`claude-opus-5`) | fresh subagent |

Implementation is Sonnet by default and stays Sonnet. Escalate an
implementation pass to Opus only after the reviewer has bounced the same issue
twice, and record the escalation in `## Work log`.

### Planning is selective, not automatic

`Issues.md` describes planning as happening at filing time. These 64 issues were
filed **without** a planning pass and carry detailed acceptance criteria
instead — 483 of them across the tracker. A planning pass that restates existing
acceptance criteria is pure cost.

**Run phase 1 only when an issue meets one of these:**

- it is in Phase 4 or 5 (unsubscribe/hygiene, campaigns) — the subsystems with
  real design latitude, notably `#0037`, `#0038`, `#0044`, `#0045`, `#0047`;
- the reviewer has bounced it once and the failure was a design problem, not a
  typo;
- its acceptance criteria are ambiguous enough that two implementers would build
  different things.

**Skip phase 1** for Phase 0, 1, 2, and 6 issues. They are mechanical (copy,
strip, rename), ports of already-validated work, or conventional CRUD.

### Keeping the orchestrator cheap

The orchestrator holds the queue, not the code. It must not read source files,
run builds, or debug — that is what the subagents are for. Concretely:

- Do not read `PRD.md` end to end. Issues cite the section they need
  (`PRD §6.3`); read that section with `sed -n`.
- Do not read `issues/Issues.md` in the orchestrator once the queue is running;
  each subagent loads it itself.
- **Never enumerate the tracker by reading issue files.** Run
  `scripts/list-issues-by-phase --open --no-color` — the whole queue in ~35
  lines, grouped by phase, in phase order. Then read **only** the handful of
  candidate issues you are choosing between, for their `## Relation` blocks.
  Reading a hundred issue files to find the next one is the most expensive
  mistake available to the orchestrator.
- Never re-dispatch a phase to "double check" a passing result.

## 4. Working the queue

**Start with the script, every time:**

```bash
scripts/list-issues-by-phase --open --no-color    # the queue
scripts/list-issues-by-phase --phase 6 --no-color # one phase
```

Candidates are rows marked `open` **or `in-progress`**. `wontfix` rows appear
under `--open` too — skip them.

**`in-progress` with nobody working it is abandoned work, and it comes first.**
A subagent that bailed, was interrupted, or died after committing code but
before review leaves the issue there, and nothing surfaces it except this
script. It is closer to done than anything `open`, so pick it up and re-enter
the pipeline at the phase it stopped at — usually review, not implementation.

**Order by dependency, not by number.** The issue number is a filing order.
Phases run in order and the script groups by phase; within a phase, read the
candidates' `## Relation` blocks and pick the one whose dependencies are
satisfied. The known exceptions already in the set: **`#0018` (logo assets)
blocks `#0017` (site header)** despite the higher number, and in Phase 8
**`#0126` blocks four of the other six**.

Re-run the script after every resolution — reviewers file new issues, and the
queue you listed ten minutes ago is stale.

**Batching.** Dispatch issues individually by default. Batch only when a run of
issues is one operation split several ways *and* they are not chained by
`Depends on` — a batch cannot resolve its own internal dependencies any faster
than sequential dispatch, it just hides the ordering.

- `#0001`–`#0006` were batched: one mechanical copy-and-strip, six code commits,
  one review pass.
- Phase 1 (`#0007`–`#0010`) is a strict chain — `#0007` → `#0008` → `#0009` —
  and must go one at a time.
- Phase 2 splits naturally into three coherent batches that share files:
  design system (`#0011`, `#0012`, `#0013`, `#0018`), shell and views (`#0014`,
  `#0017`, `#0015`, `#0016`, `#0022`), and SEO (`#0019`, `#0020`, `#0021`).
  Respect `#0018` blocking `#0017` by ordering the batches, not by splitting.

Each batch still gets exactly one review pass covering every issue in it.

Phases are gates, not suggestions — each ends at a deployable state. Do not
start Phase 3 with Phase 2 unreviewed.

## 5. Build and verify

**Use `scripts/check.sh`.** It encodes every rule below so they cannot be
forgotten: exports `TEST_DATABASE_URL`, forces `-p 2`, refuses `-count=N` for
N>1, bounds output, prints `uptime` first, and audits the run for packages that
reported no tests.

```bash
ISSUE=0123 scripts/check.sh go ./internal/handlers/...  # scoped, own database
ISSUE=0123 scripts/check.sh                             # Go + web, own database
scripts/check.sh web                                    # npm run check + npm test
scripts/check.sh all                                    # whole suite — batch review pass only

scripts/testdb.sh template   # rebuild the test template after a migration change
scripts/testdb.sh gc         # drop leftover per-agent databases
scripts/db-reset.sh          # rebuild the local dev DB from migrations + seed admin

./scripts/dev.sh             # Vite :5173 + Go API :8080, hot reload, STORAGE=json
./scripts/dev.sh --built     # production embedding at :8080
```

**Always set `ISSUE=NNNN`** — it gives the run its own database and is what
lets two agents test concurrently (§5a).

> **`STORAGE=json` has no mailing list.** `internal/devstore` implements none
> of interests, subscribers, campaigns, or suppressions, so `dev.sh`'s default
> mode 404s `/api/interests`, returns 405 from `POST /api/subscribe`, and omits
> the mailing admin routes from the route table. Fine for auth, settings,
> audit, and marketing pages. Anything touching the mailing subsystem must run
> against Postgres.

The underlying commands, when you need one directly:

```bash
go build ./... 2>&1 | tail -40
go vet ./...   2>&1 | tail -40
go test ./internal/handlers/... -p 2 2>&1 | tail -40
(cd web && npm run check 2>&1 | tail -40)
(cd web && npm test 2>&1 | tail -40)
(cd web && npm run build 2>&1 | tail -20)   # must precede a go build that embeds dist/
```

**Check the machine before you blame the code.** Run `uptime` first. On this
laptop an idle load average is under ~4. If it is above ~8, something else is
running — another agent, another suite — and *any* timing failure you see is
that, not a defect. Wait for it to drain or stop the other work. Do not
"reproduce under load," do not record a load average as an ambient baseline,
and above all do not widen a deadline to accommodate it.

This is not hypothetical. `#0084`, `#0087`, `#0091`, `#0096`, and `#0099` were
all filed against timing failures produced by ~200 concurrent agent test runs
peaking at load average 320. A day went into widening test deadlines from 5s to
20s so that `TRUNCATE` on a *local* database would stop timing out. The
statements were never slow. The machine was saturated. All five are closed;
`#0096` and `#0099` as `wontfix`.

**There is no performance requirement in this project and no load test.** It is
a marketing site with a mailing list. If you catch yourself sizing a constant
against measured machine load, stop — you are solving the wrong problem.

**`go test ./...` without `TEST_DATABASE_URL` is not verification.** The
database-backed suites skip silently when it is unset, so the run exits green
having proved nothing about them. This actually happened in `#0002`: it verified
clean, and the broken `TRUNCATE` statements it left behind only surfaced two
issues later when `#0004` stood up a real database. Export it before every
backend verification run:

```bash
export TEST_DATABASE_URL='postgres://opencircuit:opencircuit@localhost:5432/opencircuit_test?sslmode=disable'
```

Then confirm the output shows **zero skips** and that the DB-backed packages
(`internal/auth`, `internal/audit`, `internal/db`, `internal/handlers`,
`internal/middleware`) actually reported `ok` rather than `[no test files]` or a
skip notice. A package that reports `ok` in 0.00s did not talk to a database.

**Never scan the filesystem to locate a dependency.** A subagent once ran
`find / -type d -name 'go-webauthn'` and it was still going 28 minutes later.
Ask the toolchain instead — it answers in milliseconds:

```bash
go list -m -f '{{.Dir}}' github.com/go-webauthn/webauthn   # a module's source
go env GOMODCACHE                                          # the module cache root
go doc github.com/go-webauthn/webauthn.Config              # an API without reading files
npm ls --parseable <pkg>                                   # a node package
```

Scope every `find` to the repo (`find . -name …`). A bare `find /` is never the
right tool here.

**Always bound verification output.** A full `go test ./...` on a tree this size
emits more tokens than the entire PRD, and the summary is the last few lines.
Pipe through `tail -40` by default; re-run a single failing package unbounded
(`go test ./internal/mailing/... -run TestX -v`) only when you need the detail.
The same applies to `git log`, `git diff`, and `find` — bound them or scope them.

**`go build ./...` passing is not verification.** Tests must actually execute
and their output must be read. Issues touching the SPA require `npm run check`
and `npm test` in addition to the Go suite. Issues touching email rendering or
the send worker must name the specific tests that ran in `## Verification`.

## 5a. Concurrency — bounded, and now genuinely parallel

One orchestrator session spawned **197 subagents over 25 hours**, several in
separate git worktrees, each recompiling the tree and running DB-backed suites
against the same Postgres. It spent its own time killing orphaned `go test`
processes and polling `pgrep` to keep its children from starving each other. The
user killed the session by hand. That must not recur — but the cap it produced
was blunter than the actual constraint, and the actual constraint has been
fixed.

**What was really serialising the work.** `internal/testdb.Lock()` takes a
session-level advisory lock on one fixed key (`0x53484F52544C4B`), and every
DB-backed package's `TestMain` calls it. That lock is scoped to whatever
database `TEST_DATABASE_URL` names. Two agents pointed at the *same* database
queue behind each other; two agents pointed at *different* databases never
contend at all. `scripts/testdb.sh` clones a fully-migrated template database in
**~0.2s**, so every agent can cheaply have its own.

- **At most three subagents at once.** Each must have its own test database
  (`ISSUE=NNNN scripts/check.sh`, or `scripts/testdb.sh create NNNN`). An agent
  that cannot get one shares the default and must then be the only one testing.
- **Two of the three should be a pipelined pair**: while issue N is in review
  (Opus), issue N+1's implementation (Sonnet) may start — *provided the two
  touch disjoint files*. Check that before dispatching, not after.
- **Do not exceed three**, and do not dispatch a fourth "while you wait". The
  ceiling is about the machine and about your own ability to follow what is
  happening, not just about the database.
- **Worktrees share the Go build cache.** `go env GOCACHE` is one user-level
  directory (`~/Library/Caches/go-build`), keyed by content, so a worktree does
  *not* recompile from scratch — the earlier 4.5 GB figure was the cache growing
  across many distinct builds, not evidence of non-sharing. Use a worktree when
  two agents would otherwise edit the same files; it is not the expensive thing
  the old rule assumed. Still prefer the main checkout when the work is
  disjoint, because a worktree is one more place for a stray build artifact to
  hide.
- **Scope the run to what you changed.** `ISSUE=NNNN scripts/check.sh go
  ./internal/handlers/...` is the default. A full suite run is for a batch's
  single review pass, not for every implementer, and never with `-race -count=N`
  layered on top.
- **`-count=2` and higher are banned for flake-hunting.** A test that fails only
  under concurrent agent load is not flaky; the machine is busy. Re-running it
  N times just multiplies the load that caused it.
- **Kill what you start.** Before finishing, `pgrep -f 'go test|\.test '` and
  clean up your own processes. Drop any scratch database you created (§8b) —
  `scripts/check.sh` does this for you on exit; `scripts/testdb.sh gc` sweeps
  whatever leaked.

If a verification genuinely needs the whole suite under `-race`, say so and ask
first — on this tree that is a ~5 minute fully-loaded run, and it is not free.

## 5b. Obstacles and permissions — clear them before dispatching

Most time lost on this project has gone to obstacles rather than to the work: a
role without `CREATEDB`, a stale template database, a port already bound, a
credential that does not exist yet. They share a shape — **knowable in advance,
cheap to clear then, expensive once an agent is halfway through a change.**

The catalogue of what has actually stopped work here — each entry with its
signal, its cost, and the command that clears it — is
[`docs/obstacles.md`](docs/obstacles.md). Read it when writing a plan.

**The orchestrator clears obstacles before dispatch. The planning subagent
finds them.** A phase-1 pass writes `## Obstacles` next to `## Plan`: what each
one is, whether it can be avoided, and the exact command or request that clears
it. When no planning pass runs (§3 skips most issues), the orchestrator does the
scan itself — it is a couple of `command -v` checks, not an investigation.

What to check:

| Class | Check | Not this |
|---|---|---|
| Database privileges | `psql -tAc "select rolcreatedb from pg_roles where rolname=current_user"` | falling back to a superuser connection |
| Tool availability | `command -v migrate psql npm` | `find / -name …` (§5) |
| Missing credentials | is SES configured? (§10 item 2) | discovering it at the verification step |
| Shared resources | which ports, which database, `web/dist` (§8b) | assuming you are alone |
| Approval-gated steps | anything destructive or outward-facing | stalling mid-change waiting for a human |

**Never paper over a missing permission.** This already happened: an implementer
needed a scratch database, found `opencircuit` lacked `CREATEDB`, and used a
superuser connection to finish. The work completed and the missing grant stayed
missing, so the next agent hit it too. Surface it, clear it once, record it.

Grants this project needs locally, already applied:

| Grant | Why | Undo |
|---|---|---|
| `ALTER ROLE opencircuit CREATEDB` | per-agent test databases (§5a) and scratch databases for guard-removal tests (§8b) | `ALTER ROLE opencircuit NOCREATEDB` |

If an obstacle turns up mid-work and clearing it is outside the issue's scope,
**file it**. One that cost you an hour will cost the next agent an hour.

## 6. Source material

| Thing | Where |
|---|---|
| ShortLinks checkout — the Phase 0 skeleton source | `~/Developer/brennanMKE/ShortLinks` |
| Its deploy model | `~/Developer/brennanMKE/ShortLinks/DEPLOYMENT.md` (601 lines), `deploy/systemd/`, `deploy/apache/`, `scripts/db/` |
| Validated design tokens and terminal motifs | `placeholder/index.html` — the porting source for `#0011` and `#0013` |
| Logo, favicon, app icon, OG card | `assets/logo/`, `placeholder/og-default.png` — already built and reproducible |

Copy from ShortLinks; do not reinvent. Do **not** port
`internal/links`, `clicks`, `campaigns`, `filters`, `cache`, or `qr`, and drop
the `gozxing`, `go-qrcode`, and `ristretto` dependencies. ShortLinks'
`campaigns` (grouping short links) and this project's `email_campaigns` (a
message sent to a segment) share a word and nothing else.

## 7. Production facts

| | |
|---|---|
| Canonical host | **`https://www.opencircuitsf.com`** — the apex and plain HTTP both 301 to it |
| Server | Apache 2.4.68, Amazon Linux, OpenSSL 3.5.7 |
| TLS | Let's Encrypt, valid to 2026-11-16 |
| Already on the box | PostgreSQL and Apache |
| Currently served | the static placeholder |

```env
WEBAUTHN_RP_ID=opencircuitsf.com                   # apex — one passkey covers apex and www
WEBAUTHN_RP_ORIGIN=https://www.opencircuitsf.com   # must match the browser's real origin
```

These two are **not** interchangeable. A mismatch fails passkey ceremonies with
an opaque error — check it first if a Phase 1 ceremony fails.

> **Known defect in the tracker:** `#0064`'s acceptance criteria say the vhost
> should redirect `www` → apex. Production does the opposite, and
> `WEBAUTHN_RP_ORIGIN` depends on www staying canonical. Correct that criterion
> before implementing `#0064`.

## 8. Gotchas that already cost someone a day

- **CSS masks fail silently under `file://`.** The logo is drawn with
  `mask-image` tinted by `currentColor`. Under `file://` the mask never loads
  and the element renders fully transparent — while the `<img>` favicon still
  works, which makes it look like a CSS bug. Always preview with
  `python3 -m http.server`.
- **Transparency does not make a logo theme-adaptive.** Brand green `#68FF23`
  measures 14.8:1 on the dark ground but **1.32:1 on white**. Two tints exist
  (`#30800C` for light); use the mask assets.
- **The full logo is illegible below ~96 px.** Use the `mark-*` assets for
  favicons and avatars.
- **Focus rings use `--accent`, never `--border-strong`.** The latter measures
  under 3:1 against the page in both themes.
- **Headless Chromium enforces a ~485 px minimum window width.**
  `--window-size=390` silently clips a wider render, which looks exactly like a
  horizontal-overflow bug. Test narrow layouts in a sized `<iframe>` instead.
- **`.gitignore` anchors the binary as `/opencircuit`.** An unanchored binary
  name matches at any depth and silently excludes `cmd/opencircuit/` source.
  This already bit ShortLinks.

## 8a. Destructive git operations

**Never run `git checkout --`, `git restore`, `git stash`, `git reset --hard`,
or `git clean` against a path you did not personally edit this session.**
Uncommitted working-copy changes have no reflog and no recovery path — discarding
them is irreversible in a way that discarding a commit is not.

This nearly cost real work. A reviewer ran `git checkout -- issues/0068.md` to
undo its own edit, destroyed the implementer's uncommitted resolution notes, and
had to reconstruct them by hand from what it had read earlier. It disclosed this,
which is the only reason it was caught. At that moment a second subagent had
uncommitted edits in `web/` and `issues/0011.md`; a slightly broader path would
have destroyed those too.

Subagents frequently run concurrently on disjoint files. Assume any dirty file
you did not create is someone else's in-flight work.

To undo **your own** edit, prefer rewriting the file to the intended content, or
`git diff -- <path>` first and reverse only your hunk. If you genuinely must
discard, copy the file aside first (`cp <path> /tmp/…`) so the change is
recoverable. Stage narrowly — `git add <specific-path>`, never `git add -A` or
`git commit -a`, which sweep up other agents' work into a commit that does not
describe it.

## 8b. Ports, when agents run concurrently

**Never bind a fixed, shared port for a verification server.** A subagent
started the binary on `:8099`, its own process died with `bind: address already
in use`, and `curl` cheerfully returned 200 — **from a different agent's server
on the same port**. It came within one step of proving its fix against someone
else's build.

- Pick a port unlikely to collide, and **verify the server you are talking to is
  yours**: check the bind succeeded, and confirm a build-specific detail in the
  response (the hashed asset filename, a string you just changed) before
  trusting a single byte of it.
- A `curl` that succeeds is not evidence the *right* server answered.
- Kill your server when done; never leave one listening for the next agent to
  hit by accident.

The same caution applies to any shared mutable resource — a database, a
scratch directory, `web/dist/`. Namespace it or verify ownership.

**Mutation testing that removes a guard must run against a private database.**
Removing an authorization check is exactly what lets a destructive request reach
real data. This happened: `#0024`'s guard-removal proof aimed a `DELETE` at a
hardcoded `/admin/interests/1`, the guard was gone by design, and it permanently
destroyed a seeded taxonomy row in the shared `opencircuit_test`. It was caught
by a row-count check and restored by hand — but only because the agent looked.

Two rules follow:

- **Create your own database for any mutation that disables a guard**
  (`CREATE DATABASE opencircuit_<issue>`, drop it after). The shared test
  database is for reading and for ordinary suite runs.
- **Never target a literal or seeded id in a test.** Seed a throwaway row and
  target that. A hardcoded `/1` is a live object in every database it reaches.

The general principle: a mutation test deliberately breaks a safety property, so
assume during that window that *nothing* is protecting the data underneath it.

## 9. Restricted areas

- **Never mark an issue `resolved`, `closed`, or `wontfix` by inference.** The
  reviewer may set `resolved` after independently re-verifying. Only the user
  sets `closed`. See the critical rule at the top of `issues/Issues.md`.
- **Never weaken the uniform `202` in `POST /api/subscribe` (`#0026`).** Varying
  the response by whether an address is already on the list turns the endpoint
  into an email-enumeration oracle. The handler test asserts the bodies are
  byte-identical.
- **Never make the `physical_address` check in the send worker (`#0045`)
  bypassable from the UI.** CAN-SPAM §7704 requires a physical postal address in
  every commercial message; refusing to send is the correct behavior.
- **`complained` subscribers never auto-resubscribe.** Only an admin clears that
  state.
- **Do not point the apex MX at SES.** That hijacks all mail to the domain.
  Inbound unsubscribe handling uses the dedicated `lists.opencircuitsf.com`
  subdomain.
- **No third-party analytics, ad trackers, external CDNs, or email
  open-tracking pixels.** The site is self-contained by design.

## 10. Open items blocking later phases

Started on other people's clocks, not code. Track them; do not let a phase
stall silently on one.

| # | Item | Blocks | Status |
|---|---|---|---|
| 1 | Rename the GitHub repo to `OpenCircuitSF` | `#0001` housekeeping | not done |
| 2 | SES: verify domain in `us-west-2`, Easy DKIM, custom MAIL FROM, DMARC at `p=none`, request production access | real sends from Phase 3; sandbox is enough to develop against | not started |
| 3 | Physical mailing address (PO box) | `#0045` refuses to start a campaign without it — Phase 5 | not started |
| 4 | Sending identity (`hello@` vs `workshops@`) and **who reads that inbox** | Phase 3 — **but load-bearing now** | undecided. `#0075` published a privacy policy routing erasure and data-export requests to `hello@opencircuitsf.com`. The page is honest either way, but the commitment only works if someone reads that mailbox from the day it ships |
| 5 | Whether the domain needs human mailboxes — determines apex MX | Phase 0 DNS | undecided; PRD §14 Q3 |
| 6 | Server-side details: instance ID/size/region, SSH access, `DocumentRoot`, vhost file, certbot renewal schedule, whether the existing Postgres is the target | `#0064` | undocumented — capture as encountered |
| 7 | SES account-level suppression list (`aws sesv2 put-account-suppression-attributes --suppressed-reasons BOUNCE COMPLAINT`) — the second layer alongside `suppressions`, PRD §6.7 | `#0038` criterion 8; belt-and-suspenders once the SES account exists | not started — runbook step in `docs/email-setup.md`, gated on item 2 (the SES account doesn't exist yet) |

`issues/model-pricing.json` does not exist yet. Create it on the first dispatch
that needs cost accounting; refresh once per day.

## 11. PRD section index

`PRD.md` is 1,769 lines (~21k tokens). **Never read it whole.** Every issue's
`## Relation` block names the section it needs and carries the exact extraction
command; run that. This table is the fallback when you need a section no issue
cites.

Ranges are heading-anchored rather than line-numbered, so they survive edits to
the PRD.

| Section | Lines | Extract |
|---|---|---|
| **§1 Overview** | 41 | `sed -n '/^## 1\. /,/^## [0-9]/p' PRD.md` |
| **§2 v1 Scope** | 48 | `sed -n '/^## 2\. /,/^## [0-9]/p' PRD.md` |
| **§3 Codebase Strategy — Copy and Strip** | 71 | `sed -n '/^## 3\. /,/^## [0-9]/p' PRD.md` |
| §3.1 Copy unchanged (rename module path + branding strings only) | 19 | `sed -n '/^### 3\.1 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §3.2 Delete outright | 15 | `sed -n '/^### 3\.2 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §3.3 Build new | 9 | `sed -n '/^### 3\.3 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §3.4 Deviations from ShortLinks' architecture | 20 | `sed -n '/^### 3\.4 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| **§4 Brand and Visual Design** | 181 | `sed -n '/^## 4\. /,/^## [0-9]/p' PRD.md` |
| §4.1 Direction | 24 | `sed -n '/^### 4\.1 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §4.2 Color tokens | 79 | `sed -n '/^### 4\.2 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §4.3 Typography | 12 | `sed -n '/^### 4\.3 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §4.4 Reusable motifs | 21 | `sed -n '/^### 4\.4 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §4.5 Logo | 43 | `sed -n '/^### 4\.5 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| **§5 Information Architecture** | 39 | `sed -n '/^## 5\. /,/^## [0-9]/p' PRD.md` |
| §5.1 Public routes | 18 | `sed -n '/^### 5\.1 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §5.2 Admin routes (session + `is_admin` required) | 19 | `sed -n '/^### 5\.2 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| **§6 Mailing List — The Core Subsystem** | 903 | `sed -n '/^## 6\. /,/^## [0-9]/p' PRD.md` |
| §6.1 Interest taxonomy | 23 | `sed -n '/^### 6\.1 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §6.2 Database schema | 296 | `sed -n '/^### 6\.2 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §6.3 Subscription flow (double opt-in) | 56 | `sed -n '/^### 6\.3 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §6.4 Preference center | 16 | `sed -n '/^### 6\.4 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §6.5 Unsubscribe — three paths, all required | 93 | `sed -n '/^### 6\.5 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §6.6 Sending engine | 78 | `sed -n '/^### 6\.6 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §6.7 SES event ingestion | 40 | `sed -n '/^### 6\.7 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §6.8 Public campaign archive | 65 | `sed -n '/^### 6\.8 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §6.9 Delivery health — bounce policy and the circuit breaker | 60 | `sed -n '/^### 6\.9 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §6.10 Subscriber import and consent provenance | 120 | `sed -n '/^### 6\.10 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §6.11 Durable outbound queue and the activity log | 54 | `sed -n '/^### 6\.11 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| **§7 Frontend** | 84 | `sed -n '/^## 7\. /,/^## [0-9]/p' PRD.md` |
| §7.1 Stack | 7 | `sed -n '/^### 7\.1 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §7.2 Routing | 14 | `sed -n '/^### 7\.2 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §7.3 Public views | 13 | `sed -n '/^### 7\.3 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §7.4 SEO and social preview cards | 23 | `sed -n '/^### 7\.4 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §7.5 Accessibility | 13 | `sed -n '/^### 7\.5 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §7.6 Performance budget | 12 | `sed -n '/^### 7\.6 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| **§8 HTTP API** | 65 | `sed -n '/^## 8\. /,/^## [0-9]/p' PRD.md` |
| **§9 Configuration** | 75 | `sed -n '/^## 9\. /,/^## [0-9]/p' PRD.md` |
| **§10 Infrastructure and Deployment** | 89 | `sed -n '/^## 10\. /,/^## [0-9]/p' PRD.md` |
| §10.1 Topology | 38 | `sed -n '/^### 10\.1 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §10.2 DNS records (Route 53) | 16 | `sed -n '/^### 10\.2 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §10.3 SES region choice | 7 | `sed -n '/^### 10\.3 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §10.4 SES production access | 8 | `sed -n '/^### 10\.4 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §10.5 IAM | 9 | `sed -n '/^### 10\.5 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §10.6 Backups | 9 | `sed -n '/^### 10\.6 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| **§11 Security and Compliance** | 35 | `sed -n '/^## 11\. /,/^## [0-9]/p' PRD.md` |
| **§12 Phased Build Plan** | 63 | `sed -n '/^## 12\. /,/^## [0-9]/p' PRD.md` |
| **§13 Issue Breakdown** | 49 | `sed -n '/^## 13\. /,/^## [0-9]/p' PRD.md` |
| **§14 Open Questions** | 15 | `sed -n '/^## 14\. /,/^## [0-9]/p' PRD.md` |

Bold rows are whole top-level sections and include their subsections. §6 (the
mailing list) is 903 lines — take the subsection, not the parent.

§6.10 includes §6.10.1 (import invite mode).

**Regenerated 2026-08-21**, twice: when Phase 8 (§6.8–§6.11) was added, and
again after the currency pass that synced §6.2, §8, and §9 to what actually
shipped. Three rows had already drifted before either (`#0113`); every row above
is recomputed from the current file.
