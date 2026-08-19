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
| `migrations/` | `golang-migrate` up/down SQL pairs, contiguous `000001`–`000007` |
| `web/` | Svelte 5 SPA; built to `web/dist/` and embedded via `//go:embed all:dist` |
| `scripts/` | `dev.sh`, `sim.sh`, `deploy.sh`, `db-status.sh`, `db/`, `list-issues-by-phase` |
| `deploy/` | Apache vhost and systemd unit assets |
| `docs/` | Per-subsystem documentation, indexed by [`docs/README.md`](docs/README.md) |
| `assets/`, `placeholder/` | Logo and brand assets; the static site currently in production |
| `issues/` | The tracker — `NNNN.md` files plus `Issues.md` |

### Code conventions

- Standard-library `net/http` with Go 1.22+ pattern routing. No web framework.
- Handlers depend on **narrow store interfaces**, never a concrete store — that
  is what lets `internal/devstore` stand in for Postgres under `STORAGE=json`.
- SQL lives in the store package that owns the table, not in handlers.
- **Migrations are append-only.** Never edit an applied migration; add a new
  one. `migrations/000007` exists because ShortLinks broke this rule once.
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

**Fable is not used on this project.** Opus replaces it wherever `Issues.md`
previously named the top model.

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
- Dispatch one issue at a time unless the batching rule in §4 applies.
- Never re-dispatch a phase to "double check" a passing result.

## 4. Working the queue

Work issues in numeric order unless an issue's `## Relation` says otherwise.
The one known exception already in the set: **`#0018` (logo assets) blocks
`#0017` (site header)** despite the higher number.

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

```bash
# Backend
go build ./... 2>&1 | tail -40
go vet ./...   2>&1 | tail -40
go test ./...  2>&1 | tail -40

# Frontend (from web/)
npm run check 2>&1 | tail -40   # svelte-check
npm test      2>&1 | tail -40   # vitest run
npm run build 2>&1 | tail -20   # vite build; must precede go build embedding dist/

# Full local run
./scripts/dev.sh            # Vite :5173 + Go API :8080, hot reload, STORAGE=json
./scripts/dev.sh --built    # production embedding at :8080
```

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
| 4 | Sending identity (`hello@` vs `workshops@`) and who reads the reply-to inbox | Phase 3 | undecided; PRD §14 Q2 defaults to `hello@` |
| 5 | Whether the domain needs human mailboxes — determines apex MX | Phase 0 DNS | undecided; PRD §14 Q3 |
| 6 | Server-side details: instance ID/size/region, SSH access, `DocumentRoot`, vhost file, certbot renewal schedule, whether the existing Postgres is the target | `#0064` | undocumented — capture as encountered |

`issues/model-pricing.json` does not exist yet. Create it on the first dispatch
that needs cost accounting; refresh once per day.

## 11. PRD section index

`PRD.md` is 1,239 lines (~15k tokens). **Never read it whole.** Every issue's
`## Relation` block names the section it needs and carries the exact extraction
command; run that. This table is the fallback when you need a section no issue
cites.

Ranges are heading-anchored rather than line-numbered, so they survive edits to
the PRD.

| Section | Lines | Extract |
|---|---|---|
| **§1 Overview** | 41 | `sed -n '/^## 1\. /,/^## [0-9]/p' PRD.md` |
| **§2 v1 Scope** | 40 | `sed -n '/^## 2\. /,/^## [0-9]/p' PRD.md` |
| **§3 Codebase Strategy — Copy and Strip** | 71 | `sed -n '/^## 3\. /,/^## [0-9]/p' PRD.md` |
| §3.1 Copy unchanged (rename module path + branding strings only) | 19 | `sed -n '/^### 3\.1 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §3.2 Delete outright | 15 | `sed -n '/^### 3\.2 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §3.3 Build new | 9 | `sed -n '/^### 3\.3 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §3.4 Deviations from ShortLinks' architecture | 20 | `sed -n '/^### 3\.4 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| **§4 Brand and Visual Design** | 173 | `sed -n '/^## 4\. /,/^## [0-9]/p' PRD.md` |
| §4.1 Direction | 24 | `sed -n '/^### 4\.1 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §4.2 Color tokens | 79 | `sed -n '/^### 4\.2 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §4.3 Typography | 12 | `sed -n '/^### 4\.3 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §4.4 Reusable motifs | 13 | `sed -n '/^### 4\.4 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §4.5 Logo | 43 | `sed -n '/^### 4\.5 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| **§5 Information Architecture** | 34 | `sed -n '/^## 5\. /,/^## [0-9]/p' PRD.md` |
| §5.1 Public routes | 16 | `sed -n '/^### 5\.1 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §5.2 Admin routes (session + `is_admin` required) | 16 | `sed -n '/^### 5\.2 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| **§6 Mailing List — The Core Subsystem** | 459 | `sed -n '/^## 6\. /,/^## [0-9]/p' PRD.md` |
| §6.1 Interest taxonomy | 23 | `sed -n '/^### 6\.1 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §6.2 Database schema | 158 | `sed -n '/^### 6\.2 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §6.3 Subscription flow (double opt-in) | 56 | `sed -n '/^### 6\.3 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §6.4 Preference center | 16 | `sed -n '/^### 6\.4 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §6.5 Unsubscribe — three paths, all required | 93 | `sed -n '/^### 6\.5 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §6.6 Sending engine | 78 | `sed -n '/^### 6\.6 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §6.7 SES event ingestion | 33 | `sed -n '/^### 6\.7 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| **§7 Frontend** | 84 | `sed -n '/^## 7\. /,/^## [0-9]/p' PRD.md` |
| §7.1 Stack | 7 | `sed -n '/^### 7\.1 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §7.2 Routing | 14 | `sed -n '/^### 7\.2 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §7.3 Public views | 13 | `sed -n '/^### 7\.3 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §7.4 SEO and social preview cards | 23 | `sed -n '/^### 7\.4 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §7.5 Accessibility | 13 | `sed -n '/^### 7\.5 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §7.6 Performance budget | 12 | `sed -n '/^### 7\.6 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| **§8 HTTP API** | 47 | `sed -n '/^## 8\. /,/^## [0-9]/p' PRD.md` |
| **§9 Configuration** | 47 | `sed -n '/^## 9\. /,/^## [0-9]/p' PRD.md` |
| **§10 Infrastructure and Deployment** | 89 | `sed -n '/^## 10\. /,/^## [0-9]/p' PRD.md` |
| §10.1 Topology | 38 | `sed -n '/^### 10\.1 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §10.2 DNS records (Route 53) | 16 | `sed -n '/^### 10\.2 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §10.3 SES region choice | 7 | `sed -n '/^### 10\.3 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §10.4 SES production access | 8 | `sed -n '/^### 10\.4 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §10.5 IAM | 9 | `sed -n '/^### 10\.5 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| §10.6 Backups | 9 | `sed -n '/^### 10\.6 /,/^#\{2,3\} [0-9]/p' PRD.md` |
| **§11 Security and Compliance** | 35 | `sed -n '/^## 11\. /,/^## [0-9]/p' PRD.md` |
| **§12 Phased Build Plan** | 53 | `sed -n '/^## 12\. /,/^## [0-9]/p' PRD.md` |
| **§13 Issue Breakdown** | 43 | `sed -n '/^## 13\. /,/^## [0-9]/p' PRD.md` |
| **§14 Open Questions** | 12 | `sed -n '/^## 14\. /,/^## [0-9]/p' PRD.md` |

Bold rows are whole top-level sections and include their subsections. §6 (the
mailing list) is 459 lines — take the subsection, not the parent.
