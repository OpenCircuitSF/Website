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
7. **Do not plan it yet** unless it will be worked today. Phase 1 runs the day the issue is worked, against the code as it stands then (see "Why the plan is written the same day"). A filed issue carries its description and acceptance criteria; the plan comes later.
8. **If `issues/` is tracked by git**, commit the new file with message `#NNNN <issue title>` so the issue enters git history with its `open` status — the `## Plan` rides along if it's landed, else commit it separately as `#NNNN Plan`. If ignored, skip.

**This path has the same timing gap, in miniature (`#0478`).** A freshly
written `## Description` and its acceptance criteria are exactly the kind of
issue-markdown prose `internal/db`'s guards scan, and step 8 commits them with
no run in between — there is no phase-2-style build-and-test step here to have
already passed. The gap is narrower than phase 2's or phase 3's, though: those
guards scan all of `issues/`, not just the file most recently touched, and
`internal/db` sits in the default package list every bare `scripts/check.sh`
run reads. So the very next phase 2 step 8 or phase 3 step 3/5 dispatched for
*any* issue incidentally re-scans a newly filed file — the same mechanism that
caught `#0476` late, i.e. by luck, not by design. Two things follow, short of
a new check: the planning pass (phase 1) already has a shell open before its
own commit, so it runs `ISSUE=NNNN scripts/check.sh go ./internal/db/...`
there, folding the re-run into a pass that already exists rather than adding
one (see "What a plan contains"). Between filing and that plan — usually a gap
of days, since plans are written the day the issue is worked — there is no
subagent, and this note does not create one; the filed text is caught, or
not, by the next dispatch's ordinary run. `issues/0478.md` itself
was filed exactly this way.

## Updating an issue

Edit the file in place. The Mac app picks up changes automatically — no follow-up command. Touch only the rows or sections that changed; don't reformat the rest.

When status moves to `resolved` or `closed`, add a `**Closed**` row with the date. When the move to `resolved` was driven by a fix commit, also add a `**Commit**` row with the short hash. For any move toward `resolved`, `closed`, or `wontfix`, the "Critical rule" near the top of this file applies — those transitions require explicit user confirmation, not inference.

### Correcting text that later events made wrong

**A paragraph superseded by later events keeps its exact original wording, struck through inline, immediately followed by a dated `**Correction (#NNNN, date).**` note — never a paraphrase, never a silent deletion.**

This applies to acceptance criteria, `## Notes`, and any prose an issue asserts as fact. The record of what was believed, and when, is what lets a later reader tell a mistake from a change of circumstances.

It is not a style preference. `#0125` was bounced because its acceptance criterion instructed editing an already-shipped migration — a criterion written on 2026-08-21, correct then, and invalidated by the 2026-08-25 deploy. Reconstructing that took reading the issue against the deployment history. Then the correction itself was written as a *paraphrase* claiming to be "left in place, uncorrected", which was untrue; `#0293` recovered the verbatim original from commit `e762488` and restored it. The same text was mishandled twice, which is why the rule is written down rather than assumed.

Two practical points:

- Strike the **original bytes** and add `~~` around them, nothing else. A "restored verbatim" claim that is itself a paraphrase is the same defect one layer down, and it is checkable: the struck span with its `~~` removed must be byte-identical to the pre-correction text.
- Inline sentence continuations may use `**corrected <date> (#NNNN)**` instead of the block form, which reads badly mid-sentence. Both are the convention; a paraphrase is not.

Do **not** correct settled history in `resolved` issues. Their criteria describe what was done and why, and rewriting them destroys that. The rule is for text that still reads as a live instruction.

## The standard workflow (plan → implement → review)

Issues move from filed to resolved through a **three-phase pipeline**. Each phase runs in a **fresh subagent** dispatched by the orchestrator. The orchestrator never does phase work itself: it picks issues, dispatches, records usage, and keeps the queue current.

| Phase | When | Model · effort | The subagent does | Status after |
|---|---|---|---|---|
| **1. Plan** | the day the issue is worked, before phase 2 | **Opus · medium** | reads this guide, `AGENTS.md`, the issue and the current code; writes `## Plan` (see "What a plan contains"); no code | `open` |
| **2. Implement** | right after the plan lands | **Opus · low** (medium after a bounce or bail) | follows the plan, checks it compiles (build-only), runs the fast tier, commits the code, drafts the resolution sections | `in-progress` |
| **3. Review** | after implementation returns | **Opus · medium** | reads the diff, re-runs verification itself, then resolves or bounces; only the reviewer sets `resolved` | `resolved` or `open` |

**Pass `model` and `effort` explicitly on every dispatch**, in all three phases, and record both in the work log. Never rely on a default: a default that changes silently changes the cost and quality of every phase at once.

Fresh context per phase is deliberate: each subagent reloads this guide and `AGENTS.md` cleanly, so edits to those files take effect on the next dispatch. Planning and review both run *only* in a subagent, never in the orchestrator's own context — the orchestrator stays cheap and holds the queue, not the code.

**Fable is used only when the user asks for it** (`AGENTS.md` §3). Nobody but the user sets `closed`.

### Why the plan is written the same day, and why implementation runs at low effort

A low-effort implementer does well with a concrete plan and poorly when it has to explore. So every issue gets a plan, and the plan is written against the code **as it stands the day the issue is worked** — a plan written at filing time goes stale as other issues land on the same files, and a stale plan is worse than none, because a low-effort implementer follows it.

A bounce does not need a new plan when the reviewer's `## Review notes` say exactly what to change (`AGENTS.md` §3: a bounce that names the remedy keeps the next pass cheap). Re-plan only when the review found a design problem.

The rationale is measured, not assumed: in a comparable project, ten Opus · low implementations working from same-day plans took about half the model time and turns of Sonnet · medium (2.2 vs 4.3 min, 14 vs 23 turns) at the same cost per run, and all ten passed review first time against a historical bounce rate of about 15%. This project's own baseline, from `scripts/worklog.py --scan` over its transcripts on 2026-10-19:

| Median per run | Sonnet · medium implement (n=50) | Opus · medium review (n=48) | Opus · medium plan (n=11) |
|---|---|---|---|
| Wall | 4.2 min | 3.8 min | 8.7 min |
| Time in `scripts/check.sh` | 1.0 min (3 runs) | 1.2 min (3 runs) | 0.1 min |
| Model time (wall − tests) | 3.2 min | 2.5 min | 8.6 min |
| Turns | 26 | 22 | 38 |
| Cost (at 2026-10-19 prices) | $0.67 | $1.08 | $2.20 |

Compare new work-log rows against this table to judge the change.

### What a plan contains

`## Plan`, after `## Description`, written by phase 1:

- **Files** to change, by path.
- **Approach** — what changes in each, in enough detail that implementing it is applying, not deciding.
- **Tests by name** — the test functions or `it(...)` titles to add or change, and the file each lives in.
- **Verification** — the tier (see "Build / verify command") and the exact `scripts/check.sh` commands, including every repo-wide guard package the change reaches (`AGENTS.md` §5's table). Say **full tier** if the change touches what a slow check guards.
- **Check after deploy** — a short checklist for the user: what to open on `www.opencircuitsf.com` (or in the admin console) after deploying, and what they should see. This is what the user uses to decide to close the issue. Write it as the user will see the page, by label and location, not by component name.
- **`## Obstacles`** alongside it (next section).

The planner also runs `ISSUE=NNNN scripts/check.sh go ./internal/db/...` before its commit, so the new prose passes the issue-text guards (see "Filing a new issue"). Commit the plan as `#NNNN Plan`.

### Obstacles come first, in every phase

Most of the time lost on this project has gone to obstacles, not to the work:
a role without `CREATEDB`, a missing tool, a shared port already bound, a stale
template database, a permission prompt nobody was there to answer. They share a
shape — **they are knowable before the work starts and cheap to clear then,
and expensive once an agent is halfway through a change.**

[`docs/obstacles.md`](../docs/obstacles.md) catalogues the ones that have
actually stopped work on this project, with the signal and the fix for each.
Scan it; most of what a plan needs to say is already there.

**The planning subagent's first job is an obstacle scan**, written into the
issue as `## Obstacles` alongside `## Plan`. For each one: what it is, whether
it can be avoided, and if not, the exact command or request that clears it.
Look for:

- **Permissions and privileges** — a database role that cannot `CREATEDB`, a
  file the agent cannot write, a keychain or credential prompt, an `sudo` step.
- **Tool availability** — `migrate`, `psql`, `npm`, a linter. Ask the toolchain
  (`command -v`), never scan the filesystem (`AGENTS.md` §5).
- **Credentials that do not exist yet** — SES is the standing example; an issue
  that needs a real send cannot be verified today and must say so up front
  rather than discovering it at the verification step.
- **Shared mutable resources** — ports, the test database, `web/dist`. Name
  which ones the work will touch and how it will avoid colliding (`AGENTS.md`
  §8b).
- **Approval-gated actions** — anything destructive or outward-facing that will
  stop and wait for a human. If a step needs approval, say so in the plan so
  the orchestrator can request it **before** dispatching, not mid-change.

**Request every needed permission up front.** An agent that discovers a missing
grant halfway through has two bad options: stop with the work half-done, or
work around it — and the workaround is usually worse than the obstacle. This
actually happened: an implementer needed a scratch database, found the
`opencircuit` role lacked `CREATEDB`, and fell back to a superuser connection
to finish. That worked, and it hid the missing grant from the next agent, who
hit it again. **Do not paper over a missing permission. Surface it, clear it
once, and record that it is cleared.**

Known grants this project needs locally, already applied:

| Grant | Why | Undo |
|---|---|---|
| `ALTER ROLE opencircuit CREATEDB` | per-agent test databases (`scripts/testdb.sh`) and scratch databases for guard-removal tests (`AGENTS.md` §8b) | `ALTER ROLE opencircuit NOCREATEDB` |

**When an obstacle is hit anyway**, the rule is: clear it properly, or bail and
say so. Never route around it in a way that leaves the next agent to rediscover
it. If clearing it is outside the issue's scope, file it — an obstacle that
cost you an hour will cost the next agent an hour too.


### Orchestrator: pick and dispatch

**Never enumerate the tracker by reading issue files.** There are over a
hundred and reading them to find the next one is the single most expensive
mistake the orchestrator can make. Use the script:

```bash
scripts/list-issues-by-phase --open --no-color   # the queue, ~35 lines
scripts/list-issues-by-phase --phase 6 --no-color
```

Piped or redirected it degrades to plain aligned columns — `number  status
phase  title` — under `#` phase headers carrying the per-phase `d/t` and
percentage, so it stays greppable (`grep -v '^#'` leaves the rows alone). It
groups by phase and phases run in order, which is the order that matters.

1. **Refresh the pricing cache if stale.** If `issues/model-pricing.json` is missing or its `fetched` date isn't today, fetch current model prices and rewrite it (once per day). See "Token usage and cost tracking" below.
2. **Run the script** to get the candidate set. Candidates are issues marked
   `open` **or `in-progress`**. Note that `--open` also lists `wontfix` rows —
   skip those; they are never going to be worked.
3. **Pick up `in-progress` strays first.** An `in-progress` issue with no
   subagent currently working it is abandoned work — a subagent bailed, was
   interrupted, or died after committing code but before review. It is closer
   to done than anything `open` and it is invisible to everyone until someone
   resumes it. Read its `## Review notes` / `## Verification` to find out where
   it stopped, then re-enter the pipeline at the right phase — usually phase 3,
   not phase 2.
4. **Then choose among the `open` candidates by the order of operations, not by
   number.** The issue number is a filing order, not a dependency order. Read
   **only the candidate issues' files** — their `## Relation` blocks name what
   they depend on and block — and pick the one whose dependencies are already
   satisfied. Reading the whole tracker to make this decision is exactly the
   bloat this step exists to avoid.
5. **Dispatch the planner** (phase 1, Opus · medium) unless the issue already carries a `## Plan` written today, or a bounce whose `## Review notes` name the remedy.
6. **Dispatch the implementer** (phase 2, **Opus · low**; **medium** if the issue was bounced or bailed before) with the issue id, dispatching **by reference, not by paraphrase**: point at phase 2 below and add only what is issue-specific (file scope, database needs, anything unusual). Do not restate part of phase 2's sequence in the brief — a brief that names the issue file's end state without also naming step 2 reads as the whole instruction and quietly overrides the rest (`#0496`).
7. **When it returns, record its usage** and dispatch a fresh **reviewer** (phase 3, Opus · medium) for the same issue. Record the reviewer's usage when it returns.
8. If the reviewer bounced the issue, re-dispatch phase 2 at medium effort. Otherwise, **if the issue just resolved was its phase's last open issue, run the phase-end full tier** (see "Build / verify command"). Then return to step 2 — **re-run the script**, since the queue has changed and a review may have filed new issues.

Usage is recorded per phase with `scripts/worklog.py` (see "Token usage and cost tracking"), as a `## Work log` row. Bails and bounces get a row too. If the user names a specific issue ("fix 0046"), start at phase 1 for that id directly.

> **Recording usage is not optional, and it is the step most often skipped.**
> One session dispatched twenty subagents across seven issues (~2.5M tokens)
> and recorded a `## Work log` on exactly one of them, while every issue before
> `#0013` carried full usage and cost rows. A work log that exists for the
> early, cheap issues and stops at the expensive ones is worse than none — it
> makes the project look cheaper than it was. Write the row when the subagent
> returns, before dispatching the next one.

### Reporting progress to the user

- **`resolved` does not mean done.** It means work is finished and awaiting the
  user's confirmation. Only the user sets `closed`.
- **Never announce a phase as complete.** Say what is true: "N issues are
  `resolved` and awaiting your confirmation; M are still open." A phase whose
  issues are all `resolved` is not finished, and a review that files a new
  issue into that phase has just made it less finished, not more.
- Re-run `scripts/list-issues-by-phase` before reporting. It carries the
  per-phase completion percentage, so there is no reason to compute one by
  hand or from memory.

### Phase 2 — Implementation subagent (Opus · low): claim → fix → build → commit

A subagent starts with fresh context, so its first job is loading the project's conventions.

1. **Orient in the project.** Read, every time, in order:
   - **`issues/Issues.md`** (this file) — status vocabulary, module conventions, build/verify command, commit conventions, project rules. **Authoritative for issue-tracking workflow.**
   - **`AGENTS.md`** at the repo root, if it exists — project-wide guidance, code conventions, restricted areas, build/test commands. **Binding.**
   - **`issues/NNNN.md`** — the issue in full, **including its `## Plan`** and any attachments in `issues/NNNN/`.

   If the two guides disagree, prefer `AGENTS.md` for code/repo conventions and this file for issue-tracking specifics.

2. **Set status to `in-progress` now, before step 3's first code change.** Do
   this even if your dispatch brief only describes the issue file's *end*
   state — that is not a waiver of this step. Working copy only, no commit:
   `scripts/list-issues-by-phase` reads the working tree directly, so the flip
   is visible the instant you save, and this pipeline dispatches phase 2 from
   one checkout, so nothing needs a second checkout to see it. This is the
   queue's only signal that the issue is taken — `AGENTS.md` §4 treats a stray
   `in-progress` row as abandoned work worth resuming, while a stray `open`
   row mid-fix is indistinguishable from untouched and invites a second agent
   to start the same fix. A commit here would only buy a second, status-only
   commit per issue for visibility the working-tree read already gives.
3. **Make the code changes** required to fix the bug, following the `## Plan`. If you deviate, note why in `## Fix` so the reviewer understands. If the plan turns out to be wrong in a way that needs a design decision, bail (see "When the subagent can't finish") rather than improvising one.
4. **Check it compiles, then run the fast tier, and confirm tests actually executed and passed.** Mandatory; cannot be shortcutted.

   - **Build only first** (`scripts/check.sh build`; see "Build / verify command"). It stops in seconds on a compile or type error, instead of after a full run. It never counts as verification.
   - **Then the fast tier**, exactly as the plan names it, and the full tier too if the plan says so.
   - **Compilation is not verification.** "It builds" / "no type errors" does not count. Tests must actually run. A green build with zero tests run is a failure of this step.
   - **If you wrote or modified tests, you MUST execute those specific tests and observe them pass.** Confirm the test names appear in the output, the counts increased, and the result was success. A test that compiles but never ran proves nothing.
   - **Read the output, don't just check the exit code.** "0 tests run", "skipped", "no tests found", or "build succeeded" with no test summary are red flags even at exit code 0. Quote the `ok` lines, the vitest file and test counts, and the `VERIFICATION PASSED` line in `## Verification`.
   - **If verification can't run in your environment** (missing credentials, a port you cannot bind, a step that needs the user), you have not verified the fix. Bail per "When the subagent can't finish", naming the step you couldn't run.
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
     - **`## Verification`** — the exact command(s) you ran, which tier, and what you observed: the quoted `ok` lines, test counts and verdict line. Name new tests and confirm they ran. Say what the evidence rests on (Go tests, jsdom, a VM browser run).
     - **`## Files changed`** — one bullet per file, with a short note on what changed.
     - **`## Check after deploy`** — the plan's checklist, updated to match what was built.
     - **`## Gotchas`** *(optional)* — surprises, dead ends, non-obvious behavior. Skip if nothing's notable; be specific when present.

8. **Re-run the doc-scanning guards over the sections you just drafted.** Step
   4's run predates this text — `## Root cause`, `## Fix`, `## Verification`,
   `## Files changed`, `## Check after deploy`, and `## Gotchas` did not
   exist yet when it passed. Of those six, only `## Verification` sits inside the citation guards' reach:
   `internal/db`'s undefined-test-name guard and its line-number-evidence
   sibling both resolve citations under one shared allowlist of nine section
   titles (`internal/db`'s `issueCitationSectionHeaders`), which admits
   `verification` and `work log` but not `root cause`, `fix`, `files
   changed`, `check after deploy`, or `gotchas` (`#0480`). "Scanned" is three separate things here,
   not one: a *status* filter limits these guards to `open`/`in-progress`
   files; a *section* filter, the allowlist above, further limits them to a
   handful of headings within such a file; and each guard's own reach is a
   third, independent matter — the `**Closed**`-row check in phase 3 step 5
   keys off status alone rather than prose, and the dangerous-fence family
   scans on its own terms. Run this step anyway, because `## Verification` is
   where citations concentrate and is the section that broke in `#0476`,
   which is what began this convention. Run
   `ISSUE=NNNN scripts/check.sh go ./internal/db/...` by default — it is the
   cheap package. Add `./internal/handlers/...` only when this pass touched a
   Go comment or renamed/renumbered a `AGENTS.md` `##` heading (`AGENTS.md`
   §5 explains why that package alone reads `AGENTS.md`'s own section
   headings); it costs roughly 8× `internal/db` (`AGENTS.md` §5a), so don't run
   it by default. If the re-run fails, rephrase the offending line — never
   relocate it under an unscanned heading (`AGENTS.md` §8) — and run again
   before moving on.
9. **Do not commit the markdown draft.** Return to the orchestrator with a one-line summary. The reviewer makes the single resolution commit in phase 3.

### Phase 3 — Review subagent (Opus · medium): verify → resolve or bounce

An independent reviewer is the gate between "code landed" and "issue resolved". It owns the `resolved` transition; the implementation subagent never sets it.

1. **Orient** the same way (this file, `AGENTS.md`, `issues/NNNN.md` including the `## Plan` and the drafted resolution sections).
2. **Inspect the code commit** — read its diff (`git show <hash>`). Does it actually address the reported bug? Right scope? Any correctness, security, or regression risk? **Would the `## Check after deploy` list actually pass on the live site?** Trace where the changed UI renders for the production data the issue describes, not only where a test mounts it.
3. **Re-run the verification command yourself** and read the output — don't trust the drafted `## Verification`. Run the same tier the plan names (fast, or full when the plan says so or the diff touches what a slow check guards). Confirm tests actually executed and passed (same standard as phase 2, step 4). This independent run is the core of the review.
4. **Decide:**
   - **Approve** (fix is correct, verification passed): add a top-of-resolution `## Resolution notes` blockquote (`> 🟢 Resolved YYYY-MM-DD — <one sentence>.`) and ensure the `**Commit**` row and `## Check after deploy` list are present. **Leave Status at `in-progress` for now** — step 5 covers when it flips to `resolved`, and doing that here would move this file out of scope before its own guard run.
   - **Bounce** (verification failed, fix wrong, scope off): revert **Status** to `open`; add a `## Review notes` section stating exactly what failed and **the remedy you want** — the replacement, the file, the test — so the next pass applies it rather than deciding it. Leave the code commit in place unless you say otherwise in the notes.
5. **Re-run the doc-scanning guards over the notes you just wrote, before
   either branch's commit in step 6.** Only `## Review notes` sits inside the
   citation guards' section allowlist. `## Resolution notes`, the approve
   branch's top-of-file blockquote, is outside that allowlist at *any*
   status, not only while `resolved` — phase 2 step 8 names the full
   allowlist and spells out the status/section/per-guard distinction this
   relies on.
   - **Bounce**: Status already reads `open` from step 4, so `## Review
     notes` is inside both the status filter and the section filter. Run
     `ISSUE=NNNN scripts/check.sh go ./internal/db/...` by default; add
     `./internal/handlers/...` only if you touched a Go comment or a
     `AGENTS.md` `##` heading. Once it is clean, step 6 commits directly.
   - **Approve**, as two ordered sub-steps — do not run the second without
     the first:
     1. **Run `ISSUE=NNNN scripts/check.sh go ./internal/db/...` now, while
        Status still reads `in-progress` from step 4.** This is the only
        point at which the citation guards can see `## Review notes`, the
        commentary a reviewer records regardless of verdict.
        `TestIssueVerificationCitationGuardExcludesResolvedFilesByDefault`
        pins that a `resolved` file is excluded by status alone for the
        undefined-test-name guard; `TestIssueLineCitationGuardExcludesResolvedFilesByDefault`
        pins the matching exclusion for the line-number-citation guard. If it
        fails, rephrase the offending line and run again before continuing.
     2. **Only once that run is clean**, flip **Status** to `resolved`, add
        the `**Closed**` row with today's date, and run the identical
        command a second time — same `ISSUE=NNNN` suffix; `scripts/check.sh`
        drops its database on exit, so this run re-provisions rather than
        reusing the first's. This second run checks a different thing, not
        the same thing twice: by now the file is outside the guards' status
        scope, so it is `TestResolvedOrClosedIssueCarriesClosedRow` this run
        exercises — firing on a `resolved` status left without a
        `**Closed**` row — not a re-check of any prose. The two runs
        together cost under a second (`#0479` measured 0.533s and 0.484s);
        that is not worth optimising away.
6. **Make the commit.** Check `git diff --cached --name-only` first
   (`AGENTS.md` §8a) — a bare `git commit` takes whatever is staged, so if
   anything besides `issues/NNNN.md` is sitting in the index, commit with a
   pathspec instead of bare. **If `issues/` is tracked**: on approve, stage
   `issues/NNNN.md`, message `#NNNN Resolve: <title>`, body noting the code
   commit hash; on bounce, message `#NNNN Review: <reason>` and return to the
   orchestrator, which re-dispatches phase 2. If ignored, skip either way; the
   markdown is the record.

Status flow: `open` (with `## Plan`) → `in-progress` → review → `resolved`, or bounced back to `open`. **Never set `closed`** — the user does that after verifying the fix.

### Build / verify command for this project

**Use `scripts/check.sh`.** It exists so every agent verifies the same way and
so the things that are easy to forget cannot be forgotten — it exports
`TEST_DATABASE_URL` (without it the DB-backed suites skip silently and a green
run proves nothing), forces `-p 2`, refuses `-count=N` for N>1, bounds output,
prints `uptime` first, and audits the run for packages that reported no tests.

**Always set `ISSUE=NNNN`.** It gives the run its own PostgreSQL database,
cloned from a template in about 0.2s and dropped afterwards. That is what lets
two agents test at the same time — see `AGENTS.md` §5a.

#### Tiers

| Stage | When | Command | What runs |
|---|---|---|---|
| **Build only** | implementer, after editing and before any test run | `scripts/check.sh build` (`#0607`; until it lands: `go build ./... && go vet ./...` and `(cd web && npm run check)`) | Go build and vet, `svelte-check`. Stops in seconds with the compiler or type errors. **Never counts as verification.** |
| **Fast tier** (default) | every implementation and every review | `ISSUE=NNNN scripts/check.sh go <packages>` and/or `ISSUE=NNNN scripts/check.sh web` | build, vet, gofmt, the floor guard, the named Go packages (plus every repo-wide guard package the change reaches, per `AGENTS.md` §5's table), and/or `svelte-check` + vitest |
| **Full tier** | when the plan or the diff touches what a slow check guards (below); and once at the end of each phase | `ISSUE=NNNN scripts/check.sh all` then `scripts/check.sh guards` | the whole Go suite and web suite, then the standalone shell guard tests |

**What only the full tier checks**, so a change touching it runs the full tier:

- `scripts/check.sh`, `scripts/dev.sh`, `scripts/db-reset.sh`, `scripts/testdb.sh`, `scripts/deploy.sh` — each has an external harness that only `guards` runs (`dev_guard_test.sh` alone is ~48s and binds `:5173`).
- `internal/testdb` and anything that changes how test databases are created or locked.
- A change spanning many packages, where picking the scoped set is itself a guess. When unsure, run the full tier.

**Not in any tier:** `scripts/check.sh browser-nav` starts Safari and `safaridriver` on the host, which `AGENTS.md` §8 now forbids. Do not run it until it moves into a Tart VM guest (`#0609`). Real-browser evidence comes from a VM run or not at all; say which in `## Verification`.

**There are no individually slow tests to split out.** Measured on 2026-10-19 with `go test -json` over the whole Go suite (1,940 top-level tests, 50s wall including compile): the slowest test takes 2.7s, three take over 1s, and eighteen over 0.5s, together 16s. The web suite is 86 files in 8s. So the tiers split by *suite*, not by test. The time worth recovering is elsewhere:

- `check.sh`'s `go_test` runs every Go package **twice** — once for the result and again for the skip audit. On a typical `./internal/outreach/... ./internal/handlers/...` run that is 13.8s of tests plus a 12.2s repeat, 37% of a 33s run. `#0608` makes it one pass.
- A compile error found by a full run costs the whole run; `build` (`#0607`) finds it in seconds.

**Rules for every tier:**

- Quote the `ok` lines, the vitest file and test counts, and the `VERIFICATION PASSED`/`FAILED` line in `## Verification`; the exit code alone is not evidence.
- New tests must appear by name in the output as passed (`go test -run TestX -v` for the one package when the bounded output does not show them).
- Zero skips. A DB-backed package that reports `ok` in 0.00s did not talk to a database.
- A timing failure at load average above ~8 is the machine (`AGENTS.md` §5); at normal load it is a defect. Never widen a deadline.

#### Phase end

When the review of a phase's last open issue resolves it (every issue in that phase is `resolved`, `closed` or `wontfix`), the orchestrator runs the full tier once at `HEAD` and records the result — the `ok`/`FAIL` lines and verdicts of both commands — in its report to the user. Each distinct failure becomes a **new issue in that phase**, through the normal filing and planning steps. Never reopen a resolved issue for it. The phase is not finished until a full-tier run passes, and even then its issues stay `resolved`, not done, until the user closes them (see "Reporting progress to the user").

#### Commands

```bash
ISSUE=0123 scripts/check.sh go ./internal/handlers/...   # fast tier, scoped, own database
ISSUE=0123 scripts/check.sh                              # Go (default packages) + web, own database
scripts/check.sh web                                     # npm run check + npm test
ISSUE=0123 scripts/check.sh all                          # full tier, part 1: whole Go and web suites
scripts/check.sh guards                                  # full tier, part 2: standalone shell guard tests
```

Database management:

```bash
scripts/testdb.sh template      # (re)build the template after a migration change
scripts/testdb.sh list | gc     # see / drop leftover per-agent databases
scripts/db-reset.sh             # drops and rebuilds the local dev DB from migrations + seed admin
```

Full local run:

```bash
./scripts/dev.sh            # Vite :5173 + Go API :8080, hot reload — STORAGE=json
./scripts/dev.sh --built    # production embedding at :8080
```

> **`STORAGE=json` has no mailing list.** The devstore has no implementation for
> interests, subscribers, campaigns, or suppressions, so in that mode
> `/api/interests` 404s, `POST /api/subscribe` returns 405, and the mailing
> admin routes are absent from the route table entirely. It is fine for auth,
> settings, audit, and the marketing pages. **Any issue touching the mailing
> subsystem must be exercised against Postgres**, not `dev.sh`'s default.

The underlying commands, when you need one directly — bound the output, and
re-run a single failing package unbounded only when you need the detail:

```bash
go build ./... 2>&1 | tail -40
go vet ./...   2>&1 | tail -40
go test ./internal/handlers/... -p 2 2>&1 | tail -40
(cd web && npm run check 2>&1 | tail -40)
(cd web && npm test 2>&1 | tail -40)
```

See `AGENTS.md` §5.

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

Every subagent dispatch gets a usage record on the issue it worked: which model did the work, exactly how many tokens it consumed, and an estimated cost. The **orchestrator** records this after the subagent returns — a subagent can't measure its own totals. Under the three-phase workflow each issue accumulates a row per phase — **plan (Opus · medium); implement (Opus · low, or medium after a bounce or bail); review (Opus · medium)** — plus a row for every bounce or bail. Each row also records the effort, wall-clock time, and the time spent in `scripts/check.sh` runs, so model time (wall minus tests) can be compared against the baseline in "Why the plan is written the same day".

### Pricing cache (`issues/model-pricing.json`)

Anthropic publishes prices on the docs site (no API endpoint); `docs.claude.com` now redirects to `platform.claude.com`. Fetch once per day, cache to:

```json
{
  "fetched": "YYYY-MM-DD",
  "source": "https://platform.claude.com/docs/en/about-claude/pricing",
  "currency": "USD per MTok",
  "models": {
    "claude-opus-5-5": { "input": 4.00, "output": 20.00, "cache_write_5m": 5.00, "cache_write_1h": 8.00, "cache_read": 0.20 },
    "claude-sonnet-5-5": { "input": 2.00, "output": 10.00, "cache_write_5m": 2.50, "cache_write_1h": 4.00, "cache_read": 0.10 }
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

**Output tokens are not in the transcript.** `message.usage` is the stream-start snapshot: `input_tokens` and `output_tokens` are near zero on every line, while the cache fields are exact. Estimate output as the character count of all assistant content (text, thinking, tool_use input) divided by 4, and mark it `~`. **Effort** is the `effort` field on assistant lines. **Wall** is the span between the first and last `timestamp`. **Tests** is the count and total duration of Bash calls that run `scripts/check.sh`, from the call to its result (or, for a background run, to its task notification).

**`scripts/worklog.py` computes all of these** and prints a ready work-log row:

```bash
scripts/worklog.py <agent.jsonl> --phase implement --outcome reviewed   # one row
scripts/worklog.py --scan                                               # JSON for every subagent transcript; how baselines are built
```

It reads `issues/model-pricing.json`, matching a model id such as `claude-opus-5-5[1m]` to the longest priced prefix.

### The `## Work log` section

One row per work session, conventionally the last section of the issue file (always after `## Description`). The optional **Phase** column (`plan` / `implement` / `review`) makes the three-phase breakdown legible:

```markdown
## Work log

| Date | Phase | Model | Effort | Wall | Tests | Turns | Output | Cache read | Cache write | Cost | Outcome |
|---|---|---|---|---|---|---|---|---|---|---|---|
| 2026-10-20 | plan | claude-opus-5-5 | medium | 9m | 1 (0.1m) | 38 | ~29,400 | 2,088,496 | 157,531 | $2.38 | planned |
| 2026-10-20 | implement | claude-opus-5-5 | low | 4m | 3 (1.0m) | 14 | ~12,000 | 1,400,000 | 90,000 | $0.90 | reviewed |
| 2026-10-20 | review | claude-opus-5-5 | medium | 4m | 2 (1.1m) | 22 | ~4,800 | 2,431,182 | 117,810 | $1.19 | resolved |

**Total: $4.47**
```

Update the `**Total**` line whenever a row is appended. Outcome is one of `planned`, `reviewed` (implementation handed to review), `resolved`, `bounced`, `bailed`, `stopped`. Older tables keep their columns; when an older issue gets a new row, extend its header to these columns and pad earlier rows with `—`, the one exception to "don't reformat". Each row lands as its phase returns. Bails and bounces get a row too. Don't reformat existing rows.

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
| `docs` | `docs/`, `AGENTS.md`, `README.md` |

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
| 8 | Archive, imports, and delivery health | 0123–0129 |

When a phase's last open issue resolves, the orchestrator runs the full tier once (see "Phase end" under "Build / verify command").

## Issue format note

These issues are **feature work driven by the PRD**, not bug reports. The `## Steps to reproduce` / `## Expected behavior` / `## Actual behavior` sections in the generic template do not apply and are omitted. Instead each issue carries:

- `## Description` — the punchline, 1–3 sentences.
- `## Acceptance criteria` — a checklist that defines done. This is what the reviewer verifies against.
- `## Notes` — PRD reference, prior art in the ShortLinks repo, and known gotchas.
- `## Relation` — dependencies on other issues, cross-linked both directions.

Genuine bugs found later should use the full template with reproduction steps.
