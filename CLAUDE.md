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
| `migrations/` | `golang-migrate` up/down SQL pairs, contiguous `000001`–`000025` |
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
- **Migrations are append-only. The greenfield exception EXPIRED on
  2026-08-25.** Never edit a migration that has been applied to a database
  anyone cares about; add a new one. `migrations/000007` exists because
  ShortLinks broke this rule once.

  The exception was written on 2026-08-21, when nothing was live but the static
  placeholder. It said `migrations/` could be squashed, renumbered, or rewritten
  and that you should prefer editing the migration that owns a table over
  stacking an `ALTER TABLE`. **It ended at the deploy on 2026-08-25**, exactly as
  it said it would: `www.opencircuitsf.com` now serves this project (§7), and
  production's PostgreSQL holds a live `opencircuit` database with
  `schema_migrations.version = 22` and real rows in it — `#0272` is open about
  two specific `outbound_queue` rows there.

  **So: never edit an existing migration. `000001`–`000022` are frozen.** New
  work goes in a new numbered migration, and a column added to an existing table
  is an `ALTER TABLE` in that new file, not an edit to the `CREATE TABLE` that
  owns it.

  This is not hypothetical, and the stale note is why. `#0125` was filed on
  2026-08-21 with an acceptance criterion instructing the `000010` edit, its
  implementer followed that criterion faithfully, and its review bounced it: a
  scratch database built to production's version 22 and then migrated against
  the committed tree fails with `column "import_id" referenced in foreign key
  constraint does not exist` and leaves `schema_migrations` **dirty at 23**. The
  deploy would have broken, and separately every subscriber query on the new
  binary would have errored against a production table lacking the five new
  columns.

  **Check an issue's own greenfield language before following it.** Several
  issues filed before 2026-08-25 still carry the old note in their acceptance
  criteria; the criterion is stale, not authoritative. `#0293` tracks correcting
  the remaining copies in `PRD.md` §6.2 and `docs/deployment.md`.
- Tests sit beside the code as `_test.go`. DB-backed tests gate on
  `TEST_DATABASE_URL` and skip when it is unset — so a green `go test ./...`
  with that variable unset proves less than it looks like it does.
- SPA logic goes in plain TypeScript modules under `web/src/lib/` so it is
  unit-testable without a DOM; Svelte components stay thin. That is still the
  default and the cheap path — the whole suite runs in ~1s.
- **Components can now be mounted and driven, since `#0094`.** Put
  `// @vitest-environment jsdom` at the top of a test file and use
  `@testing-library/svelte`'s `render()`. Only files carrying that pragma pay
  jsdom's ~270ms startup; the rest stay on the fast default. `vite.config.ts`
  has a `VITEST`-gated `resolve.conditions: ['browser']` block — without it Vite
  resolves Svelte's *server* build and `render()` crashes in `mount()`. The gate
  is why `vite build` output is unaffected, verified byte-identical across all
  20 `dist/` files.

  Runes work under it, measured rather than assumed: `$effect` runs on mount,
  re-runs on dependency change, and runs its cleanup on unmount; a `$derived`
  chain settles synchronously, so an assertion needs no `await` unless the
  component itself schedules one (a `tick().then(…)`, typically for focus).

  **jsdom is not a browser.** Layout, visibility, IME, and some focus-order
  edge cases are approximations — a passing jsdom test is weaker evidence than a
  real-browser one, and a verification claim should say which it rests on. The
  AST guards (`#0181`, `#0197`, `#0120`) are not redundant: they catch drift
  between what a test claims and what a component says, which mounting cannot.

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

**Read that trigger as two bounces on the same *design* problem, not two
bounces of any kind** (settled 2026-08-27, over `#0284` and `#0285`). The
distinguishing question is whether the next pass has to **decide** something or
merely **apply** something. A pass handed the replacement expression, the target
number, and a proved-green verification is transcription, and Sonnet is the
right tool for it at a fraction of the cost — measured twice that day, both
third attempts landing complete in about three minutes at roughly half the
tokens of the passes before them.

The corollary matters as much: **a bounce that specifies the remedy precisely
enough is itself what keeps the next pass on Sonnet.** A reviewer that ends with
"figure out the right value" has effectively chosen to escalate. Reviewers
should write the remedy they want when they know it.

Record the decision either way — a decision *not* to escalate after two bounces
is worth the same `## Work log` line as a decision to escalate.

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
scripts/check.sh guards                                 # standalone shell guard tests — §5a

scripts/testdb.sh template   # rebuild the test template after a migration change
scripts/testdb.sh drop NNNN  # drop YOUR database when done
scripts/testdb.sh gc --all   # drop every agent's — only when you are alone
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
- **Kill what you start — and `pkill -f` cannot tell what you started.** The
  pattern is the shared one, so it matches every agent's processes. Record the
  pids you start and kill those by pid, or resolve each `pgrep` hit with
  `ps -o pid,ppid,command` and confirm it descends from your own shell before
  signalling it. **A cleanup that reaches past its owner is indistinguishable
  from the failure it was meant to clean up.** This happened twice in one
  session: one agent nearly killed another's `go test` from `check.sh`'s
  leftover listing, and one stray `pkill` orphaned another agent's `dev.sh`/vite
  pair, leaving `:5173` bound and failing an unrelated run. The shape is
  cleanup scoped by **pattern** rather than by **ownership** — the same shape as
  the `git stash` and pathspec entries in §8a.
- **Kill what you start.** Before finishing, `pgrep -f 'go test|\.test '` and
  clean up your own processes. Drop any scratch database you created (§8b) —
  `scripts/check.sh` does this for you on exit, and `scripts/testdb.sh drop
  NNNN` drops one by hand. **`gc` is not a cleanup step**: it sweeps *every*
  agent's database, so it now refuses without `--all` and skips databases with
  live connections. One agent swept another's mid-session before that guard
  existed.
- **`scripts/db-reset.sh` no longer assumes it's alone, either (#0207).** It
  targets `opencircuit`/`opencircuit_test` — the databases every agent falls
  back to sharing when it cannot get its own, and `opencircuit` is also the
  user's dev database — so a bare invocation used to `pg_terminate_backend`
  every other connection unconditionally before dropping and recreating it.
  It now refuses and reports who holds the database unless you pass
  `--force`. It also only manages those two names: a per-agent scratch
  database (`opencircuit_test_NNNN`) is refused too, even with a name
  starting `opencircuit*` — that pool belongs to `scripts/testdb.sh`
  (drop/reset/gc), not this script. Four standalone shell guard tests now
  exist for exactly this class of regression — `scripts/testdb_gc_guard_test.sh`
  (#0150), `scripts/dev_guard_test.sh` (#0117), `scripts/db_reset_guard_test.sh`
  (#0207) — run all of them with `scripts/check.sh guards` (not part of
  `go`/`web`/`all`/the default, since `dev_guard_test.sh` alone costs ~48s and
  binds `:5173`).

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
| Server | `i-0e3bd89e87d1c2364`, a **`t4g.nano`** (arm64) in **`us-east-1`**, hostname `bluesky.sstools.co`, public IP `44.222.209.183`. Apache 2.4.68 on Amazon Linux 2023, OpenSSL 3.5.7. `ssh ec2` gets you there. |
| TLS | Let's Encrypt **wildcard** (`opencircuitsf.com` + `*.opencircuitsf.com`), ECDSA, valid to 2026-11-16, renewed by `certbot-renew.timer` via the **`dns-route53`** authenticator — so renewal never touches a vhost or an ACME webroot |
| Already on the box | PostgreSQL **15.18** (one cluster, also holding `shortlinks` and `shortlinks_ocsf`) and Apache. Ports `:8081`/`:8083` are two ShortLinks instances, `:8082` is prototypes; this project owns **`:8080`**. |
| Currently served | **This project, since 2026-08-25.** `opencircuit.service` on `127.0.0.1:8080` behind `/etc/httpd/conf.d/001-www.opencircuitsf.com-le-ssl.conf`. The static placeholder is gone. |
| The one static exception | **`/.well-known/` is still served from disk**, out of `/var/www/vhosts/www.opencircuitsf.com/.well-known/`, via `ProxyPass /.well-known/ !` + `Alias` ahead of the proxy rules. It holds `atproto-did` — this domain's Bluesky DID. **The Go service 404s that path**, so removing the exception silently breaks Bluesky handle verification. Verify by hash, not by status code, after any Apache change: `curl -s https://www.opencircuitsf.com/.well-known/atproto-did \| shasum -a 256` → `4198e742…5948d`. |
| Machine size is real | 418 MB RAM plus swap on the `t4g.nano`, shared with Apache, PostgreSQL, and three other Go services. `go build` is the memory-hungry step of a deploy. |

```env
WEBAUTHN_RP_ID=opencircuitsf.com                   # apex — one passkey covers apex and www
WEBAUTHN_RP_ORIGIN=https://www.opencircuitsf.com   # must match the browser's real origin
```

These two are **not** interchangeable. A mismatch fails passkey ceremonies with
an opaque error — check it first if a Phase 1 ceremony fails.

> **Settled 2026-08-18 — do not "fix" this back.** `#0064`'s vhost criterion now
> correctly reads **apex → `www`**, matching production and
> `deploy/apache/opencircuitsf.com.conf` (`ServerName www.…`, `RewriteCond
> %{HTTP_HOST} !^www\.`). It once read the reverse; that was the defect, and it
> was corrected in the issue itself, which carries the correction note.
> `WEBAUTHN_RP_ORIGIN` depends on www staying canonical, so a passkey ceremony
> fails with an opaque error if this is inverted again.

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
- **A backslash escape you type may land as the real character.** An agent
  writing `\u00A0` into a source file twice got the actual NBSP bytes
  (`\xc2\xa0`) on disk instead of the six ASCII characters it intended — so a
  pattern meant to *describe* a codepoint silently *contained* it. Anything
  whose correctness depends on a file holding literal escape text must be read
  back as bytes: `python3 -c "print(open(P,'rb').read())"`. The workaround that
  worked was generating the backslash at runtime (`chr(92)` /
  `String.fromCharCode(92)`) rather than typing it.
- **A malformed TypeScript fixture fails silently, not loudly.** A raw LF inside
  a single-quoted JS string is invalid, but the parser is error-tolerant: the
  test reported "0 matches" and passed, rather than raising a syntax error. A
  zero-match result from a fixture you just wrote is a reason to check the
  fixture, not to conclude the pattern missed. Build such fixtures from
  `\uXXXX` escapes uniformly.

- **gofmt rewrites doc comments, and backticks do not protect you.** Since Go
  1.19 the doc-comment pass applies smart quotes, so a doubled `''` collapses
  into a single curly `”` — and a doubled backtick collapses into `“`. `#0210` found this had mangled the Postgres escape
  examples in `internal/db/prd_index_parity_test.go` — including one **inside a
  backtick span**, which is not exempt. Only a genuine indented preformatted
  block (`//` + tab) is left alone — and it must be a **tab**: a four-space
  indent leaves the text intact but gofmt rewrites the indent, so `gofmt -l`
  flags the file. Ordinary non-doc comments are not touched at all. Now that `scripts/check.sh` enforces
  `gofmt -l`, any new declaration doc comment containing `''` will be rewritten
  the first time anyone formats. Put such examples in an indented block, and
  prove the result is stable by running `gofmt -w` and confirming the hash does
  not change.

- **A regex that hunts for a tag finds the one inside a comment.** `#0064`'s
  CSP `script-src` hash was wrong twice — the second time computed by a
  reviewer who believed they had verified it in a browser. Both wrong values
  have the same cause: `web/index.html` line 38 contains the literal text
  `<` + `script` + `>` inside prose inside an HTML comment, so a search for the
  first occurrence lands there rather than on the real bootstrap tag, then
  reads forward to the real closing tag and hashes ~1.5 KB of the wrong span.
  Two different regexes over that same wrong span produced two different
  plausible-looking hashes. Parse markup with a parser (`html.parser`, which
  never visits comments) rather than searching it, and take ground truth from
  the browser: a CSP violation names the exact hash it expected, which no
  extraction bug of yours can affect.

- **Backticks inside an unquoted heredoc are command substitution.** An
  implementer wrote `` `migrate up` `` into a SQL comment inside
  `restore.sh`'s `<<SQL` heredoc; bash executed it on every run, emitting a
  stray `error: failed to parse scheme from source URL` into otherwise-clean
  output. Quote the delimiter (`<<'SQL'`) unless you specifically want
  expansion. `shellcheck` catches this as SC2006; `bash -n` does not.

- **`bash` here is 3.2.57, so bash-4 syntax fails — and fails quietly.**
  macOS ships bash 3.2 for licensing reasons, and `/bin/bash` is it. `${var,,}`
  (lowercase expansion) is bash 4+: it raises `bad substitution`, the assignment
  **does not take**, and the script carries on with the variable unchanged.
  `#0248` shipped a `ISSUE="${ISSUE,,}"` normalisation that therefore never ran
  — the mixed-case trap it was written to close stayed fully open, plus a
  spurious error line. **`bash -n` and `shellcheck` both pass on it**, which is
  why the verification could not catch it. Use `tr '[:upper:]' '[:lower:]'`,
  which `scripts/testdb.sh` already does. Note process substitution is *not* a
  counter-example: `<(…)` works fine in 3.2, so "we already require bash" does
  not license bash-4 syntax. The same applies to `${var^^}`, `declare -A`,
  `mapfile`/`readarray`, and `&>>`.

- **Where a guard's proof belongs: ask whether the mutation leaves the assertion
  falsifiable by the thing it measures.** Settled by `#0304`'s review, and it
  generalises. Force a floor constant to `0` and `got < floor` can never fire —
  the in-package test agrees with itself, so it restates the bug instead of
  detecting it, and the proof belongs in an **external harness**
  (`scripts/*_guard_test.sh`, wired into `scripts/check.sh guards`). Mutate the
  *scan roots* instead and `got` itself changes — the in-package test is a
  legitimate, non-circular oracle, and the proof belongs in Go.

  Two refinements from the same review: it turns on the **direction of the
  comparison**, not the kind of thing mutated — a *ceiling* mutated upward needs
  the harness, while a floor mutated *upward* is observable in Go. And the two
  homes are **not exclusive**: an in-file observer can be deleted by the same
  edit it guards, so a constant worth pinning is often worth pinning in both
  places.

- **A guard's oracle must not be the same bytes as its subject.** `#0258` added
  a positive assertion that `run()`'s definition still spells its `FAILED=1`
  accounting, comparing it against a **quoted heredoc holding the expected
  text**. One `sed` replacing the definition line rewrote *both* the live
  definition and the heredoc, so the guard agreed with itself and a failing
  test still reported `VERIFICATION PASSED`. `runpipe()`'s equivalent survived
  the same global replace, because its oracle is a **regex** — a different
  representation of the same fact, which an edit to the subject does not touch.
  The selection rule is not "does the text contain awkward characters"; it is
  **can an edit to the subject also satisfy the oracle**. A copy of the answer
  stored next to the question is not a check.

  The companion rule, from the same review: assert the marker block appears
  **exactly once**. Leaving the real definition intact and *adding* a second
  marked block shadows it at runtime while the scan deletes the decoy before
  counting — `GUARD-0208` closes this with begin/end counts; anything modelled
  on it must too.

- **The agent shell is zsh; the scripts are bash. Verification snippets run in
  the wrong one.** Tool-invoked commands here go through **zsh**, while every
  script in `scripts/` is `#!/usr/bin/env bash` (3.2.57 — see the bash-4 entry
  below). An ad hoc snippet typed to check a script's behaviour is therefore
  *not* running under the same interpreter as the script, and constructs that
  differ silently between the two — array syntax, `[[ ]]` word splitting,
  `${BASH_SOURCE}`, `local` scoping — will give an answer about the wrong
  shell. `#0258`'s implementer had one of its own verification snippets
  silently corrupted this way. Run script checks with an explicit
  `bash -c '…'` or by invoking the script, never by pasting its innards into
  the tool shell.

  Related, from the same pass: `${BASH_SOURCE[0]}` is **not** a fix for a
  relative `$0`. It holds the same relative string and fails identically once
  the script has `cd`'d. Resolve an absolute path *before* changing directory.

- **A self-check can start measuring the process instead of the file.**
  `#0258`'s fourth attempt sourced candidate lines into `( source "$tmp";
  declare -f run )` and hashed the result — a "private subshell". It is a
  subshell of the *running* `scripts/check.sh`, which already has `run` and
  `runpipe` defined. So when the extraction contributes nothing, `declare -f`
  silently falls back to the **live** definition: the check stops measuring the
  file on disk and starts measuring the program currently executing, which
  always agrees with itself.

  That is what turned a separate bug into a **fail-open**. An ordinary helper
  containing a `#` inside a string (`printf '# %s\n'`) broke the comment-strip
  `sed`, which is not quote-aware, and swallowed every later candidate — and
  because the fallback then reported the live definition, the digest matched
  and the guard passed. Measured *outside* a running `check.sh`, the same file
  hashes the empty string and fails closed.

  Two rules follow. Isolate genuinely (`env -i bash --noprofile --norc`, or
  `unset -f` the names first), and **assert the extraction produced something**
  before hashing it — an empty result must be an error, never an input.

- **A guard inside the file it guards becomes new mutable surface.** `#0258`
  spent six passes hardening `scripts/check.sh`'s self-checks against decoys
  added to `scripts/check.sh`. Each pass closed what it was shown; each next
  review found the adjacent shape — and by pass six the newest mechanism was
  itself the target: the behavioural probe made load-bearing that pass was
  pinned to nothing, so shadowing *it* restored the bypass.

  The pattern is structural, not a failure of care. Every mechanism added to
  defend a file lives in that file, and is therefore one more thing an edit to
  that file can neutralise. Adding a seventh in-file check buys a seventh
  bounce.

  **The project already has the right shape for this**:
  `scripts/db_reset_guard_test.sh` and friends, wired into
  `scripts/check.sh guards`, mutate a *copy* of the thing under test and assert
  its exit code and output **from outside**. An external harness cannot be
  disarmed by editing its subject. Prefer it for anything whose failure mode is
  "the checker was edited", and keep in-file checks only for the early,
  line-naming signal they give.

- **BSD `grep -P` on this machine matches nothing, silently.** It does not
  error and does not warn — it reports zero hits on a file that demonstrably
  contains the bytes, which reads exactly like "the string isn't there." A
  reviewer hit this while byte-verifying a U+2009 thin space. Scan with
  `python3` (or `grep -a` on a hex dump) when you need to prove a byte sequence
  is or is not present; never conclude absence from `grep -P`.

- **`.gitignore` anchors the binary as `/opencircuit`.** An unanchored binary
  name matches at any depth and silently excludes `cmd/opencircuit/` source.
  This already bit ShortLinks.

- **Shortening an identifier can silently widen an interface's satisfier set,
  and the identifier that moves need not be on the type that gets recruited.**
  `#0325` (`f7771ed`) renamed `(*seo.Site).InvalidateWorkshops` and
  `(*seo.Renderer).InvalidateWorkshops` → `Invalidate`, and in the same commit
  renamed the method their two consuming seams require —
  `handlers.workshopCacheInvalidator` (now `seoCacheInvalidator`) and
  `mailing.ArchiveCacheInvalidator` — from `InvalidateWorkshops()` to
  `Invalidate()`. Go interface satisfaction is structural, so that rename
  moved both interfaces onto `*seo.Sitemap` — a type whose own code the
  commit never changed, in a file it did touch: `f7771ed` rewrote
  the doc comment directly above `func (s *Sitemap) Invalidate()`, so the
  recruited declaration sat in the rename commit's own diff as context, and
  still nobody noticed it. `(*seo.Sitemap).Invalidate` already carried that
  exact zero-parameter, zero-result signature, so passing a bare `*Sitemap` into
  either seam went from a compile error to a silent half-invalidation.
  Nothing failed to compile and no toolchain diagnostic fired. Measured with
  `go/types.Implements` over the package's full named-type set, the
  satisfiers of a bare `Invalidate()` were `{*Sitemap}` before the rename,
  `{*Site, *Renderer, *Sitemap}` after it, and `{*Site, *Renderer}` once
  `#0337` closed the instance by unexporting `Sitemap.invalidate`. That
  full named-type set is the check to run before and after a rename like this,
  not a hand-picked list of the ones you already suspect — `#0337`'s first
  guard checked only the three types its author already had in mind, and its
  review found the gap precisely by widening the check to every named type in
  the package.

  **Value receivers count, and this is the sharper half.** `*T`'s method set
  includes every value-receiver method declared on `T`, so an AST guard that
  scans only pointer receivers has a hole. This is not hypothetical:
  `#0337`'s first guard justified a pointer-receiver-only scan in its own doc
  comment, and a `func (s Sitemap) Invalidate() {}` mutation — a value
  receiver, not a pointer one — fully restored the original regression while
  the guard still reported `ok`. Any guard modeled on this one must cover both
  receiver forms; `go/types` is the sound oracle, since method-set membership
  is a type-checker rule, not something an AST walk can fully reconstruct on
  its own (an AST walk also can't see a method acquired by embedding a satisfying
  struct or interface field, or hiding behind a generic receiver — see
  `internal/seo/invalidator_satisfier_guard_test.go`'s doc comments for how
  those are handled).

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

**"Scoped" does not make `git stash` safe.** `#0120`'s implementer stashed to
measure a pre-fix baseline and to prove its new guard failed against the old
tree — a genuinely good reason, and it described the stash as scoped. But `git
stash` takes the *whole* working tree; two other agents were mid-issue at the
time, and it was luck rather than design that neither had uncommitted work when
it ran. Verified afterwards: stash list empty, both their commits intact.

To prove a guard fails against the old code, check the old file out **to a
different path** and point the test at it, or run in a throwaway worktree at the
parent commit. Never move the shared tree out from under other agents.

**The usual motive is a before/after measurement.** An agent wanting a "before"
test count ran a broad `git stash` / `git stash pop`, briefly sweeping another
agent's in-flight `internal/mailing/worker_test.go` in with its own. The pop
reapplied cleanly and nothing was lost, and it disclosed the whole thing — which
is the only reason it is written down here. `git stash` is not a read-only
operation on a shared tree, and "I'll put it right back" is exactly the shape of
the reasoning that precedes a loss.

To measure a before/after, copy your own file aside and compare hashes:

```bash
cp <your-path> /tmp/before-NNNN            # your file only
# ...edit, measure...
shasum -a 256 <your-path> /tmp/before-NNNN # prove you restored it
```

Or measure the baseline in a throwaway worktree pinned to a commit, where the
working tree is yours alone.

**The orchestrator can cause this too, and did.** While `#0113`'s subagent was
editing `CLAUDE.md` §11, the orchestrator edited `CLAUDE.md` §7 and §10 and
committed with `git commit -- CLAUDE.md`. A pathspec names a *file*, not a
*hunk*, so it swept the subagent's uncommitted §11 corrections into a commit
about something else. Nothing was lost and the content was verified correct, but
the attribution is wrong and it could as easily have been a conflict.

**So: do not edit a file you have dispatched an agent against.** Wait for it, or
hand your change to that agent. If you must, `git diff -- <file>` first and
confirm the only changes present are yours — a clean `git status` when you
started is not evidence, because the agent may write at any moment.

**Running the check is not the same as acting on it.** `#0161`'s implementer
ran `git status --porcelain` before its `gofmt -w` sweep — and did not branch on
the output. It formatted `cmd/opencircuit/seo_wiring_test.go` while `#0165` had
that file open, caught it in `git diff`, reversed only its own hunk by hand, and
waited for the other agent to commit before reformatting. Nothing was lost, and
only because it looked afterwards.

A bulk rewrite (`gofmt -w`, a codemod, `sed -i` over a glob) is the dangerous
shape: it touches files you never named. Filter the list first, and skip what is
dirty:

```bash
gofmt -l . | while read -r f; do
  [ -n "$(git status --porcelain -- "$f")" ] && { echo "skip (dirty): $f"; continue; }
  gofmt -w "$f"
done
```

**`git commit -- <paths> -m "<msg>"` silently swallows the message as a
pathspec.** After `--`, everything is a path — including `-m` and the message.
The commit does not happen, and git prints `did not match any file(s) known to
git`. `#0268`'s implementer hit this, and the reason it is dangerous here is
what came next: **the error printed alongside an unrelated short hash from
another agent's commit landing concurrently**, so the output read like success
to anyone checking the hash rather than the words. It was caught with `git show
--stat HEAD`.

Correct form: `git commit -F <file> -- <paths>`, or put `-m` before the `--`.
And confirm every commit with `git show --stat HEAD` rather than trusting a
hash that appeared nearby — with several agents committing, a hash on your
screen is not necessarily yours.

**`git commit -- <paths>` commits the WORKING TREE state of those paths, not
the index.** This is `--only` semantics and it is documented git behaviour, but
it is the opposite of what "I staged narrowly, so only my work is committed"
implies — and this whole file has been recommending that form. Naming a pathspec
makes git commit the *files*, including **unstaged** changes another agent has
in them. This is the sibling of the `#0268` hazard just above — same `--`
construct, opposite failure: `#0268` is a commit that does not happen at all;
this is a commit that happens and contains more than intended.

`#0305`'s implementer hit it exactly that way: it isolated its own hunks with
`git apply --cached`, verified the index, then `git commit -- <paths>` swept in
`#0129`'s unstaged edits to the same three files. It caught this from
`git show --stat HEAD` — the diff was far larger than what it had staged — and
recovered with a second commit removing precisely the swept-in hunks, verifying
the working tree byte-identical to its pre-mistake state. Nothing was lost, and
only because it checked the size rather than the exit code.

`#0328`'s implementer hit the same construct with no other agent involved: it
isolated one issue's hunks with `git apply --cached`, ran
`git commit -F - -- <pathspec>`, and got `34147d4` — a commit titled for
`#0327` alone but carrying `#0328`'s working-tree changes too, because the
pathspec bypassed the index it had just staged rather than confirming it. The
"only my own work is at risk" case fails exactly the same way as the
concurrent-agent case above. The commit was left in place rather than split
apart, since a concurrent agent had already built on top of it by the time the
mistake was found.

**Both forms have a failure mode, and the index tells you which to use.**

| Form | Commits | Fails when |
|---|---|---|
| `git commit` (no pathspec) | exactly the index | another agent **staged** their files |
| `git commit -- <paths>` | the working tree at those paths | another agent has **unstaged** edits in *your* files |

So there is no blanket rule. Look at the index first, then pick:

```bash
git add <your-specific-paths>          # stage narrowly
git diff --cached --name-only          # LOOK. Whose files are in here?
#   only yours      -> git commit -m "..."            (no pathspec)
#   someone else's  -> git commit -m "..." -- <paths> (pathspec, and check the
#                       named files aren't dirty from them first)
git show --stat HEAD                   # confirm the file list AND the line counts
```

`#0129`'s reviewer got this right against a blanket instruction from the
orchestrator: it found `#0309`'s files staged in the shared index, recognised
that a bare commit would sweep them in, and used a pathspec deliberately. Read
the index; do not follow either form by habit.

**And the check itself is racy — the window between looking and committing is
real.** `#0315`'s reviewer ran `git diff --cached --name-only`, saw only its own
file, and **seconds later** its pathspec-less commit swept in six of `#0123`'s
files staged in between. It caught this from `git show --stat`, confirmed `HEAD`
was still its own, recovered with `git reset --soft HEAD~1`, and re-committed
with a pathspec; the six files returned to the index untouched, because a soft
reset does not touch content. So: **commit immediately after checking, and always
verify afterwards with `git show --stat HEAD`.** The check tells you which form
to use; only the after-the-fact stat tells you what actually happened. `git show --stat`
after every commit is what catches both this and the shared-index case below:
check the **line counts**, not just the filenames, because a swept-in hunk lands
in a file you did legitimately touch.

**Staging narrowly is not enough — the index is shared too.** `git commit` with
no pathspec commits *whatever is staged*, including files another agent staged
seconds earlier. This happened twice in one session: two implementers working
disjoint halves of Phase 6 each swept the other's staged work into a commit that
did not describe it. Both caught it from `git show --stat` and recovered with
`git reset --soft HEAD~1` plus `git restore --staged <their-paths>` — which is
safe only because neither touched file *contents*. Two habits prevent it:

```bash
git diff --cached --name-only          # before every commit — is anything here not yours?
git commit -- <your-paths>             # pathspec on the commit, not just the add
```

Nothing was lost either time, and only because both agents checked and
disclosed. Assume the index has someone else's work in it.

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

**Redact secrets from verification transcripts.** `#0277`'s implementer proved
its fix end to end and pasted the log line into the issue's `## Verification` —
including a live recovery magic link (`token=4g5mVqo…`). Harmless in that
instance: single-use, a 15-minute TTL, a localhost dev database, and long
expired before anyone read it. But issue files are committed and this repo is
going to GitHub, so a token, session cookie, or API key pasted into a
transcript is published. Show the shape, not the value —
`http://localhost:8080/recover/verify?token=<redacted>` proves the same thing.

**`./scripts/dev.sh --built` runs a real `npm run build`, which overwrites the
tracked `web/dist/index.html` placeholder.** Three agents have now hit this, and
the instinct each time is `git checkout -- web/dist/index.html`, which §8a
forbids outright. The safe restore, which does not touch anyone else's work:

```bash
git show HEAD:web/dist/index.html > web/dist/index.html   # restore, no checkout
git status --porcelain -- web/dist/                        # confirm clean
```

Better still, run a production build in a throwaway worktree. `scripts/check.sh`
deliberately does not build, so an ordinary verification never needs this.

**The scratchpad directory is not session-private in practice.** Two agents
have now had a script pick up a *previous* session's leftover file from it —
one nearly pasted another issue's review notes into the file it was writing,
and caught it only from `git diff`. Use a unique filename (include the issue
number), and read back what you wrote before believing it.

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

- **Subagents do not file new issues; they report them.** Picking "the next
  number" is not safe when several agents run at once — two of them read the
  same highest number and both write `issues/NNNN.md`, and the second commit
  silently overwrites the first. **This happened twice in one session**, both
  times a reviewer's follow-up being clobbered by the orchestrator; both were
  recovered from the losing commit (`#0154`, `#0159`) only because the content
  was still in git. A reviewer that wants an issue filed should say so in its
  report and in its `## Review notes` — the orchestrator, which is single and
  therefore cannot race itself, allocates the number and writes the file.

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

**Deployment happened on 2026-08-25** — the 2026-08-23 deferral is spent, and
`www.opencircuitsf.com` now serves this project (§7). **SES was deliberately
left out of it**, so the "develop against mocks" rule still holds for anything
that sends email, and only for that: an issue that can only be proved against
real SES is still work to be built against a mock now and validated on the box
later. Say which of the two a verification claim rests on.

Two things about the live box that a mock will not tell you, both learned the
hard way on deploy day:

- **`MAILER_NOOP=true` cannot be used in production at all.**
  `cmd/opencircuit/main.go`'s `checkMailerNoOp` refuses to start unless
  `BASE_URL`'s host is `localhost`/`127.0.0.1`. That guard is correct and must
  not be weakened — it is what stops a production host silently swallowing
  every outbound email. "Turn SES off in production" is expressed as
  `SEND_WORKER_ENABLED=false` plus an unconfigured SES, not as `MAILER_NOOP`.
- **`SES_CONFIGURATION_SET` is required, not optional.**
  `docs/configuration.md` lists it as optional; `mailing.NewSESMailer` refuses
  to construct without it and the service will not boot. The doc is wrong.

Anything now queued for send accumulates in `outbound_queue` and retries on
the six-step backoff up to `queue_max_retries` (8) before going `abandoned`,
so a magic link requested before SES exists is simply burned — request it
after.

Started on other people's clocks, not code. Track them; do not let a phase
stall silently on one.

| # | Item | Blocks | Status |
|---|---|---|---|
| 1 | Rename the GitHub repo to `OpenCircuitSF` | `#0001` housekeeping | not done |
| 2 | SES — **production access is the only piece left** | real sends to non-verified addresses | **all but done 2026-08-25.** Identity `mailing.opencircuitsf.com` verified, DKIM and custom MAIL FROM `SUCCESS`, DMARC `p=none` on the subdomain, config set + SNS + auto-confirmed HTTPS subscription live, account suppression list on, and instance role `opencircuit-instance` attached and **proven by a real delivered send**. Remaining: the account is still sandboxed (`ProductionAccessEnabled: false`) — 200/day, 1/sec, verified recipients only. When granted, flip `SES_SANDBOX=false` and `SEND_WORKER_ENABLED=true`. **Trap worth knowing:** in the sandbox SES authorizes `SendEmail` against the *recipient's* identity ARN too, so the role policy needs `identity/*`, not just the sending domain — and a test send to `success@simulator.amazonses.com` will NOT catch a policy that gets this wrong, because simulator addresses are not identities. See [`docs/aws-iam-setup.md`](docs/aws-iam-setup.md) |
| 3 | Physical mailing address (PO box) | `#0045` refuses to start a campaign without it — Phase 5 | not started |
| 4 | Sending identity and **who reads that inbox** | Phase 3 | **both halves settled 2026-08-25.** The public contact address is `contact@opencircuitsf.com` (`#0271`), and it is a real **Google Workspace** mailbox that already exists — which is what `#0075`'s privacy policy needed, since it routes GDPR erasure and data-export requests there. Mail *sends* as `contact@mailing.opencircuitsf.com` with `Reply-To: contact@opencircuitsf.com`: the `From:` has to sit on the verified SES subdomain identity, while replies land in the Workspace inbox. Same local part on purpose, so the two read as one address |
| 5 | Whether the domain needs human mailboxes — determines apex MX | Phase 0 DNS | **answered by observation 2026-08-25: yes, and it already has them.** The apex MX is `1 smtp.google.com` with a `google._domainkey` record — Google Workspace, predating this project. This is why list mail sends from the `mailing.` subdomain and why **the apex MX must never be touched**: doing so would hijack real human mail, not just hypothetical mail |
| 6 | Server-side details: instance ID/size/region, SSH access, `DocumentRoot`, vhost file, certbot renewal schedule, whether the existing Postgres is the target | `#0064` | **done, 2026-08-25.** All captured and recorded in §7 above and in `docs/deployment.md`'s production-facts table, measured on the box rather than guessed. Three of them contradicted what the PRD assumed: the instance is a `t4g.nano`, not `t4g.small`; it is in `us-east-1`, not `us-west-2`; and its PostgreSQL is **15**, not 16 |
| 7 | SES account-level suppression list — the second layer alongside `suppressions`, PRD §6.7 | `#0038` criterion 8 | **already enabled.** `get-account` returns `SuppressedReasons: ["BOUNCE", "COMPLAINT"]`; it appears to predate this project. Nothing to do |

`issues/model-pricing.json` does not exist yet. Create it on the first dispatch
that needs cost accounting; refresh once per day.

## 11. PRD section index

`PRD.md` is 1,839 lines (~21k tokens). **Never read it whole.** Every issue's
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
| **§6 Mailing List — The Core Subsystem** | 952 | `sed -n '/^## 6\. /,/^## [0-9]/p' PRD.md` |
| §6.1 Interest taxonomy | 23 | `sed -n '/^### 6\.1 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §6.2 Database schema | 345 | `sed -n '/^### 6\.2 /,/^#\{2,3\} [0-9]/p' PRD.md` |
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
| **§10 Infrastructure and Deployment** | 111 | `sed -n '/^## 10\. /,/^## [0-9]/p' PRD.md` |
| §10.1 Topology | 38 | `sed -n '/^### 10\.1 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §10.2 DNS records (Route 53) | 16 | `sed -n '/^### 10\.2 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §10.3 SES region choice | 7 | `sed -n '/^### 10\.3 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §10.4 SES production access | 8 | `sed -n '/^### 10\.4 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §10.5 IAM | 9 | `sed -n '/^### 10\.5 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §10.6 Backups | 31 | `sed -n '/^### 10\.6 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| **§11 Security and Compliance** | 35 | `sed -n '/^## 11\. /,/^## [0-9]/p' PRD.md` |
| **§12 Phased Build Plan** | 63 | `sed -n '/^## 12\. /,/^## [0-9]/p' PRD.md` |
| **§13 Issue Breakdown** | 49 | `sed -n '/^## 13\. /,/^## [0-9]/p' PRD.md` |
| **§14 Open Questions** | 14 | `sed -n '/^## 14\. /,/^## [0-9]/p' PRD.md` |

Bold rows are whole top-level sections and include their subsections. §6 (the
mailing list) is 933 lines — take the subsection, not the parent.

§6.10 includes §6.10.1 (import invite mode).

**Regenerated 2026-08-23** (`#0113`), and now **guarded**:
`internal/db/prd_section_index_test.go` runs every row's own `sed` command
against `PRD.md` and fails naming any row that disagrees. It is in
`scripts/check.sh`'s default Go scope, so an edit that widens a section without
updating this table fails an ordinary run.

Two things the guard was written because of, both measured by `#0113`'s review:

- **§6.2 drifted across two commits, not one** — `#0131` added 5 lines and
  `#0148` added 25, table stuck at 296 against an actual 326. Neither pass
  noticed, which is what a "every row recomputed" note cannot prevent.
- **§14 never drifted; it was wrong from birth.** `45f659e` wrote `15` into the
  table in the same commit that made the section 14 lines. §14 is the one row
  with no following heading, so `sed` runs to EOF and its count is taken raw —
  the regeneration applied `raw + 1` anyway, against the exception it had just
  documented.

**The guard checks arithmetic, not `sed` syntax.** Its BRE→RE2 translation
handles `\{n,m\}` but a *bare* `{n,m}`, `+`, `?`, `(`, `)` are literal in BRE
and operators in RE2 — a row using one would pass the guard while the real
command extracts something else. `#0218` is open to lint for that.
