# Open Circuit SF

This tracker covers the Open Circuit SF website at `opencircuitsf.com` — a Go service with an embedded Svelte 5 SPA, backed by PostgreSQL and AWS SES, hosted on EC2 behind Apache. It serves the group's public marketing pages and workshop listings, runs the interest-segmented mailing list (double opt-in, preference center, unsubscribe handling, campaign sending), and provides a passkey-authenticated admin console for staff. The companion link shortener at `go.opencircuitsf.com` is a separate deploy of [ShortLinks](https://github.com/brennanMKE/ShortLinks) and its issues do **not** belong here. Everything is web — use `All` for Platform. The scope and design decisions behind most issues in this tracker are specified in [`PRD.md`](../PRD.md) at the repo root; issue Notes sections cite the relevant PRD section.

This file is the local guide for managing issues in this project. The companion Mac app (Issues.app) watches the `issues/` folder and renders the current state. Markdown files (and `project.json`) are the source of truth — there is no generated artifact or index to keep in sync.

The `# Open Circuit SF` heading above should match the `name` field in `issues/project.json`. `project.json` is the canonical source for the project's identity (name + repo URL); this guide is the workflow companion.

## Folder layout

```
issues/
├── project.json       # canonical project name + repo URL
├── Issues.md          # this file
├── model-pricing.json # daily-refreshed model price cache (see "Token usage and cost tracking")
├── 0001.md            # one file per issue
├── 0001/              # optional sibling folder for screenshots, crash logs, etc.
│   └── screenshot.png
├── 0002.md
└── …
```

## Project config (`project.json`)

A small JSON file naming the project and its repo. Two required fields:

```json
{
  "name": "Open Circuit SF",
  "url": "https://github.com/user/repo"
}
```

- `name` — the project's human-readable name. Match the heading at the top of this file.
- `url` — the project's canonical web URL (HTTPS form, not SSH). Typically a GitHub URL; GitLab, Bitbucket, etc. work too.

When the repo moves or renames, edit `project.json` directly. Don't infer the project's name from the parent folder path — `project.json` is authoritative.

## Status values

| File value | Display name | Meaning |
|---|---|---|
| `open` | Open | Filed but not yet started |
| `in-progress` | In Progress | Actively being worked on |
| `resolved` | Resolved | Work is done; awaiting user confirmation |
| `closed` | Closed | User has confirmed the fix |
| `wontfix` | Won't Fix | Acknowledged but won't be addressed |

Use the **file value** (lowercase, hyphenated) in the issue's metadata table. The Mac app converts to the display name when rendering.

## Critical rule: never close without explicit confirmation

The most important rule of this workflow: an issue must **never** be marked `resolved`, `closed`, or `wontfix` based on inference. Only when the user has said so in plain language. Specifically, do not infer resolution from:

- a code change you (or a subagent) just made
- a commit message
- the filing of a related issue
- the user saying "thanks, that looks better"

Leave status at `open` (or `in-progress` if work has started) until the user confirms in words like "close this", "this is fixed", "mark resolved", or "won't fix". When in doubt, ask.

The deliberate exception: the **review subagent** (Opus, phase 3 of the standard workflow) may set `resolved` after independently re-verifying the fix (work-is-done-but-not-confirmed). The implementation subagent never sets `resolved` — it leaves the issue `in-progress` for review. No subagent ever sets `closed` — that's the user's call. This separation is the entire reason `resolved` and `closed` are different states.

## Git tracking

This project's choice on whether `issues/` is in git determines whether lifecycle events produce commits. Check on every operation:

```bash
git rev-parse --is-inside-work-tree 2>/dev/null   # is this a git repo?
git check-ignore -q issues/                        # exit 0 = ignored, 1 = tracked
```

- **Not a git repo, or `issues/` is ignored**: edit files only; never commit. The Mac app still tracks changes from the working copy.
- **`issues/` is tracked**: each lifecycle event below produces its own commit.

When tracked:

| Event | What's committed | Commit message |
|---|---|---|
| Initial setup | `project.json` + `Issues.md` together | `Add issue tracker setup` (or bundle with the first `#NNNN` commit) |
| File a new issue | the new `NNNN.md` (and `project.json` / `Issues.md` if newly created) | `#NNNN <issue title>` |
| Planning adds `## Plan` | markdown (the `## Plan` section) | folded into `#NNNN <issue title>` if it lands first, else `#NNNN Plan` |
| Edit project config | `project.json` only | `Update project config` (or e.g. `Update project URL`) |
| Implementation — code commit | code changes only | `#NNNN <verb> <title>` |
| Review — resolution commit | markdown update (status + Closed + Commit + summary), made by the Opus reviewer | `#NNNN Resolve: <title>` |
| Review — bounce to open | markdown (status back to `open` + `## Review notes`) | `#NNNN Review: <reason>` |
| Bail with notes | markdown only | `#NNNN Notes: <brief>` |
| Work-log row appended | markdown only | `#NNNN Work log: <model>, <total tokens>, $<cost>` |
| Daily pricing refresh | `model-pricing.json` only | `Update model pricing` |
| User-confirmed close | markdown only | `#NNNN Close` |
| Won't fix | markdown only | `#NNNN Won't fix` |

**Working-copy-only changes (no commit):**

- Setting status to `in-progress` at the start of work — transient; the resolve commits supersede it. Committing every status flip would create noise.

**Why two commits to resolve, not one:** the **Commit** metadata row records the hash of the code-fix commit, and that hash isn't known until *after* the code commit lands. Splitting resolution into a code commit and a resolution commit keeps each commit single-purpose ("fix the code", "document the fix") and lets the resolution commit reference the hash cleanly.

## Issue file format

Each issue is `NNNN.md` (4-digit zero-padded) with this structure:

```markdown
# NNNN — Title

| | |
|---|---|
| **Status** | open |
| **Module** | <module name(s)> |
| **Platform** | iOS · macOS · iPadOS · All |
| **First seen** | YYYY-MM-DD |

## Description

What is wrong. Lead with the punchline — the first paragraph shows in the Mac app summary.

## Steps to reproduce

1. …
2. …

## Expected behavior

What should happen.

## Actual behavior

What actually happens.

## Attachments

![caption](NNNN/screenshot.png)

## Notes

Any additional context, guesses at root cause, related code locations.
```

### Format details that matter

- **Title separator** is an em-dash (U+2014, `—`), not a hyphen.
- **Metadata field rows** must keep the field name in `**bold**` exactly.
- **Dates** are `YYYY-MM-DD`.
- **Module** can list multiple modules separated by ` / ` (e.g. `BlueskyFeed / BlueskyDataStore`).
- **Platform** is `iOS`, `macOS`, `iPadOS`, `All`, or any other string. `All` is treated as matching every platform filter.
- When status moves to `resolved` or `closed`, add a `**Closed**` row with today's date. When the move to `resolved` is the result of a fix commit, also add a `**Commit**` row with the short hash (`git rev-parse --short HEAD`).
- Steps / Expected / Actual / Attachments / Notes are conventional but not all required — for design-refinement or feature-gap issues, Description alone is fine.

## Filing a new issue

1. Confirm `issues/project.json` exists. If missing, create it (see schema above) before filing the first issue — `name` should match this guide's heading; `url` is the project's canonical web URL (HTTPS, not SSH).
2. Find the highest existing `NNNN.md` and increment. Start at `0001` if the folder is empty. Skip past reserved high numbers (e.g. `8888`, `9999` for test issues).
3. Create `issues/NNNN.md` from the template.
4. Set status to `open`.
5. Use today's date for First seen.
6. Phrase the title as a single declarative sentence describing the bug, not a question or a fix description.
7. **Plan it (phase 1).** Dispatch a fresh subagent on the **planning model (Opus)** to read this guide, `CLAUDE.md`, the new issue, and the relevant code, then write a `## Plan` section into `issues/NNNN.md` (after `## Description`). It writes no code and leaves status at `open`. This gives whoever picks the issue up a running start. Record the planner's usage in `## Work log`. Skip if the user is jotting a quick note and doesn't want planning yet.
8. **If `issues/` is tracked by git**, commit the new file with message `#NNNN <issue title>` so the issue enters git history with its `open` status — the `## Plan` rides along if it's landed, else commit it separately as `#NNNN Plan`. If ignored, skip.

## Updating an issue

Edit the file in place. The Mac app picks up changes automatically — no follow-up command. Touch only the rows or sections that changed; don't reformat the rest.

When status moves to `resolved` or `closed`, add a `**Closed**` row with the date. When the move to `resolved` was driven by a fix commit, also add a `**Commit**` row with the short hash. For any move toward `resolved`, `closed`, or `wontfix`, the "Critical rule" near the top of this file applies — those transitions require explicit user confirmation, not inference.

## The standard workflow (plan → implement → review)

Issues move from filed to resolved through a **three-phase pipeline**. Each phase runs in a **fresh subagent** dispatched by the orchestrator, on the model that fits the work:

| Phase | When | Model | The subagent does | Status after |
|---|---|---|---|---|
| **1. Planning** | selectively, before an issue is worked (see `CLAUDE.md` §Model policy) | **Opus** | reads conventions + issue, writes `## Plan`; no code | `open` |
| **2. Implementation** | when the issue is worked | **Sonnet** | follows the plan, fixes, builds + verifies, code commit, drafts resolution sections | `in-progress` |
| **3. Review** | after implementation returns | **Opus** | independently re-verifies the diff, then approves or bounces | `resolved` or `open` |

Fresh context per phase is deliberate: each subagent reloads this guide and `CLAUDE.md` cleanly, so edits to those files take effect on the next dispatch. Planning and review both run *only* in a subagent, never in the orchestrator's own context — the orchestrator stays cheap and holds the queue, not the code.

**Fable is not used on this project.** Planning runs on Opus. See `CLAUDE.md` §Model policy for which issues get a planning pass at all — most of this tracker's issues already carry acceptance criteria and skip phase 1.

### Orchestrator: pick and dispatch

1. **Refresh the pricing cache if stale.** If `issues/model-pricing.json` is missing or its `fetched` date isn't today, fetch current model prices and rewrite it (once per day). See "Token usage and cost tracking" below.
2. List `issues/*.md` (skip `Issues.md`). Pick the lowest-numbered file whose status is `open` — it should already carry a `## Plan` from phase 1.
3. **Dispatch a fresh Sonnet subagent** (phase 2) with the issue id and instructions to follow the implementation steps below.
4. **When it returns, record its usage** and dispatch a fresh **Opus reviewer** (phase 3) for the same issue. Record the reviewer's usage when it returns.
5. If the reviewer bounced the issue back to `open`, re-dispatch phase 2. Otherwise move on to the next open issue (or stop if only one was requested).

Usage is recorded per phase — locate the subagent's transcript, sum token counts (deduped by `requestId`), note the model, compute cost, and append a `## Work log` row. Bails and bounces get a row too. If the user names a specific issue ("fix 0046"), dispatch phase 2 to that id directly.

### Phase 2 — Implementation subagent (Sonnet): claim → fix → build → commit

A subagent starts with fresh context, so its first job is loading the project's conventions.

1. **Orient in the project.** Read, every time, in order:
   - **`issues/Issues.md`** (this file) — status vocabulary, module conventions, build/verify command, commit conventions, project rules. **Authoritative for issue-tracking workflow.**
   - **`CLAUDE.md`** at the repo root, if it exists — project-wide guidance, code conventions, restricted areas, build/test commands. **Binding.**
   - **`issues/NNNN.md`** — the issue in full, **including its `## Plan`** and any attachments in `issues/NNNN/`.

   If the two guides disagree, prefer `CLAUDE.md` for code/repo conventions and this file for issue-tracking specifics.

2. **Set status to `in-progress`** in the markdown — working copy only, no commit. The Mac app picks it up immediately.
3. **Make the code changes** required to fix the bug, following the `## Plan`. If you deviate, note why in `## Fix` so the reviewer understands.
4. **Build *and* run the project's verification command, and confirm tests actually executed and passed.** Mandatory; cannot be shortcutted.

   - **Compilation is not verification.** "It builds" / "no type errors" does not count. Tests must actually run — unit tests execute, UI tests run on a simulator, the app launches, whatever the project defines as proof. A green build with zero tests run is a failure of this step.
   - **If you wrote or modified tests, you MUST execute those specific tests and observe them pass.** Confirm the test names appear in the output, the counts increased, and the result was success. A test that compiles but never ran proves nothing.
   - **Read the output, don't just check the exit code.** "0 tests run", "skipped", "no tests found", or "build succeeded" with no test summary are red flags even at exit code 0. iOS in particular reports `xcodebuild` success when no tests ran.
   - **If verification can't run in your environment** (no simulator, missing credentials, hardware, sandbox), you have not verified the fix. Bail per "When the subagent can't finish", naming the step you couldn't run.
   - **If the build was already failing before you started**, note it and bail — don't fix unrelated breakage.

5. **Make the code commit.** Stage *only the code changes* (not the issue markdown yet). The message starts with `#NNNN` and a short, declarative title — the verb that fits (`Fix`, `Add`, `Refactor`, `Update`, `Remove`, …); not every issue is a bug fix. Blank line, then a paragraph of detail. Example:

   ```
   #0046 Add navigation from avatar tap to profile

   The avatar tap on PostCardView was not wired to any NavigationLink.
   Threaded the author DID through the cell and connected onTapGesture
   to push ProfileView.
   ```

6. **Capture the commit hash** with `git rev-parse --short HEAD`.
7. **Draft the resolution sections in the markdown — but do NOT set `resolved`.** That's the reviewer's transition.

   - Leave **Status** at `in-progress`; add a `**Commit**` row with the short hash from step 6.
   - Add, all *after* `## Description`:
     - **`## Root cause`** — what was actually wrong.
     - **`## Fix`** — the approach taken; call out any divergence from the plan.
     - **`## Verification`** — the exact command(s) you ran and what you observed (e.g. "`xcodebuild test -scheme MyAppUITests` — 14 tests passed including the 3 new in `ReplyButtonUITests`"). Name new tests and confirm they ran.
     - **`## Files changed`** — one bullet per file, with a short note on what changed.
     - **`## Gotchas`** *(optional)* — surprises, dead ends, non-obvious behavior. Skip if nothing's notable; be specific when present.

8. **Do not commit the markdown draft.** Return to the orchestrator with a one-line summary. The reviewer makes the single resolution commit in phase 3.

### Phase 3 — Review subagent (Opus): verify → resolve or bounce

An independent reviewer is the gate between "code landed" and "issue resolved". It owns the `resolved` transition; the implementation subagent never sets it.

1. **Orient** the same way (this file, `CLAUDE.md`, `issues/NNNN.md` including the `## Plan` and the drafted resolution sections).
2. **Inspect the code commit** — read its diff (`git show <hash>`). Does it actually address the reported bug? Right scope? Any correctness, security, or regression risk?
3. **Re-run the verification command yourself** and read the output — don't trust the drafted `## Verification`. Confirm tests actually executed and passed (same standard as phase 2, step 4). This independent run is the core of the review.
4. **Decide:**
   - **Approve** (fix is correct, verification passed): change **Status** to `resolved`; add a `**Closed**` row with today's date; ensure the `**Commit**` row is present; add a top-of-resolution `## Resolution notes` blockquote (`> 🟢 Resolved YYYY-MM-DD — <one sentence>.`). **If `issues/` is tracked**, make the resolution commit — stage `issues/NNNN.md`, message `#NNNN Resolve: <title>`, body noting the code commit hash. If ignored, skip; the markdown is the record.
   - **Bounce** (verification failed, fix wrong, scope off): revert **Status** to `open`; add a `## Review notes` section stating exactly what failed and what the next implementation pass must fix. Leave the code commit in place unless you say otherwise in the notes. **If tracked**, commit the markdown with `#NNNN Review: <reason>`. Return to the orchestrator, which re-dispatches phase 2.

Status flow: `open` (with `## Plan`) → `in-progress` → review → `resolved`, or bounced back to `open`. **Never set `closed`** — the user does that after verifying the fix.

### Build / verify command for this project

Backend:

```bash
go build ./... 2>&1 | tail -40
go vet ./...   2>&1 | tail -40
go test ./...  2>&1 | tail -40
```

Frontend (from `web/`):

```bash
npm run check 2>&1 | tail -40   # svelte-check — type errors
npm test      2>&1 | tail -40   # vitest run
npm run build 2>&1 | tail -20   # vite build; must precede `go build` embedding dist/
```

Full local run:

```bash
./scripts/dev.sh            # Vite :5173 + Go API :8080, hot reload
./scripts/dev.sh --built    # production embedding at :8080
```

Bound the output — `tail -40` keeps the summary and drops the noise; re-run a single failing package unbounded when you need the detail. See `CLAUDE.md` §5.

**`go build ./...` passing is not verification.** Tests must actually execute and their output must be read. Issues touching the SPA require `npm run check` and `npm test` in addition to the Go suite. Issues touching email rendering or the send worker require the relevant unit tests to run and be named in `## Verification`.

### When the subagent can't finish

If the bug is unreproducible, out of scope, or the build won't pass after reasonable effort:

1. **Discard or stash any partial code changes** so the bail doesn't accidentally include half-done work.
2. **Revert status to `open`** in the issue markdown so the issue goes back into the queue.
3. **Add a `## Notes` section** describing what was tried, why work stopped, and what you'd try next. Be specific.
4. **If `issues/` is tracked by git**, commit the markdown change with message `#NNNN Notes: <one-line bail summary>`. If ignored, skip.
5. Return with a one-line summary of why work stalled.

Never use `wontfix` or `closed` to escape a stuck issue.

## Token usage and cost tracking

Every subagent dispatch gets a usage record on the issue it worked: which model did the work, exactly how many tokens it consumed, and an estimated cost. The **orchestrator** records this after the subagent returns — a subagent can't measure its own totals. Under the three-phase workflow each issue accumulates a row per phase — **planning (Opus), when run; implementation (Sonnet); review (Opus)** — plus a row for every bounce or bail.

### Pricing cache (`issues/model-pricing.json`)

Anthropic publishes prices on the docs site (no API endpoint). Fetch once per day, cache to:

```json
{
  "fetched": "YYYY-MM-DD",
  "source": "https://docs.claude.com/en/docs/about-claude/pricing",
  "currency": "USD per MTok",
  "models": {
    "claude-opus-5": { "input": 5.00, "output": 25.00, "cache_write_5m": 6.25, "cache_read": 0.50 },
    "claude-sonnet-5": { "input": 3.00, "output": 15.00, "cache_write_5m": 3.75, "cache_read": 0.30 }
  }
}
```

If `fetched` is today, use as-is. If the fetch fails, use the stale cache and note the staleness next to the cost; with no cache at all, record tokens and model with `—` for cost. Never trust example numbers over a fresh fetch.

### Getting exact token counts

Claude Code writes each subagent's transcript to `~/.claude/projects/<project-slug>/<session-id>/subagents/agent-<id>.jsonl`, where `<project-slug>` is the working directory with `/`, `.`, and `_` replaced by `-`. Assistant lines carry `message.usage` (exact `input_tokens`, `output_tokens`, `cache_read_input_tokens`, `cache_creation_input_tokens`) and `message.model`.

**Dedupe by `requestId`** — one API response can span several JSONL lines repeating the same usage object; summing every line over-counts. Find the newest agent file mentioning the issue id, keep one usage entry per `requestId`, and sum.

```
cost = (input × input_rate + output × output_rate
      + cache_read × cache_read_rate + cache_write × cache_write_5m_rate) / 1,000,000
```

If no transcript is available (different harness), record whatever total the harness reported, or `—`. Never fabricate counts.

### The `## Work log` section

One row per work session, conventionally the last section of the issue file (always after `## Description`). The optional **Phase** column (`plan` / `implement` / `review`) makes the three-phase breakdown legible:

```markdown
## Work log

| Date | Phase | Model | Input | Output | Cache read | Cache write | Cost |
|---|---|---|---|---|---|---|---|
| 2026-06-03 | plan | claude-opus-5 | 84 | 6,102 | 512,400 | 41,200 | $0.58 |
| 2026-06-04 | implement | claude-sonnet-5 | 120 | 18,530 | 2,904,110 | 98,400 | $1.12 |
| 2026-06-04 | review | claude-opus-5 | 96 | 9,240 | 1,331,200 | 44,800 | $1.05 |

**Total: $2.75**
```

Update the `**Total**` line whenever a row is appended. The `plan` row lands at filing time; the `implement` and `review` rows land as each phase returns. Bails and bounces get a row too. Don't reformat existing rows.

## Attachments

Screenshots, crash logs, console output, sample data, etc. live in a sibling folder `issues/NNNN/`. Reference them with paths *relative to the issue's `.md` file* — that means the folder prefix `NNNN/` is part of the link target. The bytes that ship are `1335/screenshot.png`, not `screenshot.png` and not `issues/1335/screenshot.png`.

```
issues/1335.md           ← the markdown that contains the link
issues/1335/screenshot.png   ← the file being linked

# inside 1335.md the link reads:
![caption](1335/screenshot.png)
```

Concrete example with both image and video attachments:

```markdown
## Attachments

![Reply button does nothing when tapped](1335/screenshot.png)
![Crash log](1335/crash.log)
[![Sidebar resize jitter](1335/sidebar-resize-jitter.poster.png)](1335/sidebar-resize-jitter.mov)
```

### Videos (`.mov`, `.mp4`, etc.)

Videos can't be embedded as `![…](…)` — markdown renderers treat that as an `<img>` and a `.mov` won't load. Instead, generate a poster frame with `qlmanage` and emit an image-inside-a-link (shown in the example above). Quick recipe — copy the video into `issues/NNNN/` first, then:

```bash
qlmanage -t -s 1280 -o issues/NNNN issues/NNNN/<basename>.<ext>
mv issues/NNNN/<basename>.<ext>.png issues/NNNN/<basename>.poster.png
```

`qlmanage` ships with macOS — no install. It reliably produces posters for AVFoundation-supported formats: `.mov`, `.mp4`, `.m4v`, `.qt`. For `.avi` it usually works; for `.mkv` and `.webm` it generally fails on stock macOS unless a third-party Quick Look generator is installed. If the rename step doesn't produce the `.poster.png`, fall back to the plain `![alt](NNNN/file.mov)` form with a `<!-- poster generation failed -->` HTML comment in the Attachments section. Don't apply the link wrapper to plain images, and don't generate posters for animated GIFs.

### macOS screenshot / screen recording filename gotcha

macOS Screenshot and Screen Recording filenames both use a **narrow no-break space** (U+202F) before AM/PM, visually identical to a regular space. A literal `cp` of the quoted filename will fail with "No such file or directory". Use a glob to skip past it:

```bash
mkdir -p issues/NNNN
cp ~/Desktop/Screenshot\ YYYY-MM-DD\ at\ H.MM.SS*PM.png issues/NNNN/screenshot.png
cp ~/Desktop/Screen\ Recording\ YYYY-MM-DD\ at\ H.MM.SS*PM.mov issues/NNNN/recording.mov
```

The `*` matches the U+202F. Substitute the actual timestamp; if you don't know which file the user means, list `~/Desktop/Screenshot*` or `~/Desktop/Screen\ Recording*` by mtime and pick the most recent.

## Module conventions for this project

Use these canonical module names in the `**Module**` row. Multiple modules are separated by ` / `; the first is the primary and determines the swimlane.

| Module | Covers |
|---|---|
| `infra` | Repo scaffolding, build, Makefile, dev scripts, dependency management |
| `config` | `internal/config` — environment variable loading and validation |
| `db` | `migrations/`, `internal/db` — schema and connection pool |
| `auth` | `internal/auth`, `internal/middleware` — passkeys, sessions, authorization |
| `admin` | Admin console API and views (users, settings, audit, overview) |
| `web` | `web/src` — SPA shell, router, public views, components |
| `design` | Design tokens, fonts, motif components, logo and image assets |
| `seo` | Server-injected meta tags, sitemap, robots, structured data |
| `subscribers` | `internal/subscribers` — signup, confirmation, preferences, unsubscribe, suppression |
| `interests` | `internal/interests` — the interest taxonomy |
| `mailing` | `internal/mailing` — campaigns, rendering, audience, send worker, SES mailer |
| `sesnotify` | `internal/sesnotify` — SNS webhook, bounce and complaint ingestion |
| `inbound` | `internal/inbound` — inbound `mailto:` unsubscribe processing |
| `workshops` | `internal/workshops` — workshop records and public pages |
| `deploy` | Apache, systemd, Route 53, SES/IAM setup, backups, runbooks |
| `docs` | `docs/`, `CLAUDE.md`, `README.md` |

## Phases

Issues carry a `**Phase**` metadata row matching the phased build plan in [`PRD.md` §12](../PRD.md). Phases are ordered and each ends at a deployable state — work them in order unless a specific issue says otherwise.

| Phase | Theme | Issues |
|---|---|---|
| 0 | Foundation — copy and strip the ShortLinks skeleton | 0001–0006 |
| 1 | Auth carried over | 0007–0010 |
| 2 | Brand and marketing pages | 0011–0022 |
| 3 | Mailing list capture | 0023–0032 |
| 4 | Unsubscribe and list hygiene | 0033–0039 |
| 5 | Campaigns | 0040–0049 |
| 6 | Workshops | 0050–0056 |
| 7 | Inbound and polish | 0057–0064 |

## Issue format note

These issues are **feature work driven by the PRD**, not bug reports. The `## Steps to reproduce` / `## Expected behavior` / `## Actual behavior` sections in the generic template do not apply and are omitted. Instead each issue carries:

- `## Description` — the punchline, 1–3 sentences.
- `## Acceptance criteria` — a checklist that defines done. This is what the reviewer verifies against.
- `## Notes` — PRD reference, prior art in the ShortLinks repo, and known gotchas.
- `## Relation` — dependencies on other issues, cross-linked both directions.

Genuine bugs found later should use the full template with reproduction steps.
