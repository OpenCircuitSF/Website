# Plan: outreach UX batches 1–3 (#0581, #0582, #0583)

Opus phase-1 plan, 2026-10-18. Three sequential passes, one per issue. Each pass
builds on the tree the previous one left (and on `#0592`'s Reset button, which
must survive). Verify each pass with `ISSUE=NNNN scripts/check.sh web` and, where
Go changes, `ISSUE=NNNN scripts/check.sh go ./internal/outreach/... ./internal/outreachconn/... ./internal/handlers/... ./cmd/opencircuit/...`.

## Facts that shape the design (verified in code)

- Bluesky Announce draft (`internal/outreach/templates.go`): lead + summary +
  hashtag line, **no link** (the card carries it). Mastodon Announce: "Join us
  for X…" + summary + "Spots are limited, so you can sign up here: {link}" +
  hashtags, paragraphs joined by `\n\n`. `DraftAll` auto-prepares from
  `outreach_links`, so a fresh Mastodon draft already holds the real `short_url`.
- Bluesky card (`publisher.go postBluesky`): URL = `short_url`, or
  `DestinationURL` when `bluesky_card_url == "canonical"` and the site base is
  set; title = workshop title; description = full summary; thumb = only a
  `/media/` `cover_image` on local disk, else no image.
- Mastodon link preview = the workshop page's OpenGraph (`internal/seo/seo.go
  workshopRouteMeta`): og:title, og:description = summary, og:image =
  `cover_image` or `/workshops/{slug}/og.png`, host = the site's.
- `outreach_posts` has no customized column. No outreach devstore exists.
- `web/src/lib/liveRegionGuard.structuralGuard.test.ts` fails any
  `role="status"` inside `{#if}`/`{#each}` — per-card live regions must sit
  unconditionally at the top level of a per-card component file.
- Modal guards glob `views/**/*.svelte`; new dialogs copy
  `OutreachScheduleAllDialog.svelte` exactly (`$effect(() => void tick().then(() =>
  dialogEl?.focus()))`, `tabindex="-1"`, `onkeydown` with `isModalEscape` on the
  same node, the backdrop pattern).
- `confirm()` calls remaining: `OutreachPostCards.svelte` (Post now/Retry, Mark
  posted, "Discard your unsaved changes?") and `OutreachComposer.svelte`
  (discard). `OutreachBulkActions.svelte` has a local function named `confirm`,
  not a call.
- The composer's own Post now/Retry/Mark posted send with **no** confirmation —
  a hole the card dialog must not leave open.

---

## Pass 1 — #0581: dialog, per-card feedback, progress, Copied

### Go (the dialog needs "the account that will post it")
- `internal/outreachconn/manager.go`: `func (m *Manager) Account(channel string) string`
  from current values — `bluesky` → `"@" + strings.TrimPrefix(handle, "@")` (or
  `""`), `mastodon` → `url.Parse(mastodon_base_url).Host`, else `""`. No network.
- `internal/handlers/admin_outreach.go`: `Account string \`json:"account"\`` on
  `outreachChannelView`; field `accountFn func(string) string` + `SetAccounts(fn)`
  (like `SetHashtags`); `channelViews()` fills it for automatic channels only.
- `cmd/opencircuit/main.go`: `adminOutreachH.SetAccounts(conns.Account)` next to
  `SetHashtags`.
- Tests: manager_test (handle with/without `@`, mastodon host, empty when unset);
  admin_outreach_test (GET view carries account for bluesky, `""` for instagram);
  wiring test only if it asserts the setter list.

### TS
- `OutreachChannel.account: string` in `web/src/lib/types.ts`; update fixtures.

### Pure lib (`web/src/lib/outreachCards.ts`, tested in `outreachCards.test.ts`)
- `pollDelay(attempt)` → `[1000, 2000, 4000][attempt] ?? 10_000`.
- `postableAutomatic(view, wave)` → `{post, channel}[]`: automatic+configured
  channels with a post where `canPostNow || canRetry`; server channel order.
- `interface CardNotice { text; kind: 'status'|'error'; status: string|null }`;
  `visibleNotice(n, post)` returns `n.text` only while `post?.status ?? null ===
  n.status` (so "Queued…" vanishes when polling brings back posted/failed).
- `postDialogCopy(mode: 'post'|'retry'|'markPosted', channels: {label, account}[])`
  → `{title, warning, accountLine[], confirmLabel}`. Post/Retry warning: "This
  goes out publicly, right now, and cannot be unsent." Account line: "Posts
  publicly as @x on Bluesky", or "…from the Bluesky account set in Settings"
  when empty. Mark posted: "Records that you posted this on Instagram by hand.
  Edit and Copy go away."
- `combinedPostResult(r: {label, ok, error?}[])` → "Bluesky: queued · Mastodon:
  could not post (reason)".
- `copyLabel(base, copied)` → `"Copied"` or `base`.

### Components
- New `OutreachConfirmDialog.svelte` (generic shell): `{title, inFlight,
  errorMessage, confirmLabel, danger?, onConfirm, onClose, children}`. Used for
  discard-changes and wrapped by the post dialog.
- New `OutreachPostDialog.svelte`: `{mode, items: {channel, post}[], inFlight,
  errorMessage, onConfirm(remoteUrl?), onClose}`; renders `postDialogCopy` and
  per item label, account, `<pre>` body (Pass 3 swaps in previews). Mark posted
  adds an optional "Link to the post" URL input validated with
  `validateRemoteUrl` (D4).
- New `OutreachPostCard.svelte` (extracted from the `card` snippet in
  `OutreachPostCards.svelte`): owns `notice`, `copied: 'text'|'link'|null` (2 s
  timeout, cleared on unmount), dialog state. Always rendered at the bottom of
  the `<article>`, outside the edit `{#if}`: `<p role="status"
  class="card-notice">{visibleNotice(notice, p)}</p>` and `<p
  role="alert">{error}</p>`. Queued: CSS spinner (`aria-hidden`) + "Posting
  now… usually under ten seconds" as static text in the status line. C12: after
  Save, focus the Edit button with `focus({preventScroll: true})`.
- `OutreachPostCards.svelte`: the list (grouping, `editingId`, the discard
  dialog replacing `openEditor`'s confirm). Remove the page-level `notice`
  paragraph; keep one list-level `role="status"` for both-networks results. Add
  **Post to Bluesky and Mastodon** above Announce cards when
  `postableAutomatic(view,'announce').length >= 2` (label from channel labels):
  opens `OutreachPostDialog` with both; on confirm calls `sendOutreachPost`
  **sequentially**, catching each error separately; `onChange` per success; writes
  `combinedPostResult` to the list status and each card notice. Mark posted calls
  `markOutreachPosted(id, remoteUrl)`.
- `OutreachComposer.svelte`: dirty Cancel uses `OutreachConfirmDialog`. In card
  mode (`onClose` set) hide Post now/Retry/Schedule/Mark posted — the card owns
  them (also C9). Grid mode keeps them.
- `OutreachPanel.svelte`: replace `setInterval` with a backoff chain:
  ```
  $effect(() => {
    void pollTick;
    if (!view || !shouldPoll(view)) { pollAttempt = 0; return; }
    const t = setTimeout(async () => { pollAttempt++; await refreshQuietly(); pollTick++; }, pollDelay(pollAttempt));
    return () => clearTimeout(t);
  })
  ```
  `pollAttempt` non-reactive, `pollTick` `$state`. In `onChange`, if a post newly
  became `queued`, reset `pollAttempt = 0`. Delete `POLL_MS`.

### Tests (jsdom)
- New `OutreachPostDialog.behavior.test.ts`: body, "@acct on Bluesky", "cannot
  be unsent"; focus on open; Escape closes; Mark posted URL rejects `ftp:`.
- `OutreachPostCards.behavior.test.ts`: rewrite the Post-now-confirm test (opens
  `role="dialog"`, Cancel sends nothing, Post sends once); rewrite #0579's
  discard test for the dialog; both-networks calls `sendOutreachPost` twice and
  shows one combined line, including one rejection; the notice lands in the right
  `<article>`; Copy text → "Copied" → reverts after 2000 ms; queued shows
  progress text.
- `OutreachPanel.behavior.test.ts`: fake timers; `getWorkshopOutreach` after
  1000, +2000, +4000, +10000 ms; stops on unmount; no polling when nothing queued.
- New `web/src/lib/outreachNoNativeConfirm.guard.test.ts`:
  `import.meta.glob('../views/admin/Outreach*.svelte', {query:'?raw'})`; no
  match for ``/(?<![\w.])(window\.)?confirm\(\s*[`'"]/`` and no `window.confirm`.

---

## Pass 2 — #0582: Announcement-first page

### A. Implicit Start
- In `OutreachPanel.svelte` behind a new prop `autoStart?: boolean`; only
  `OutreachWorkshop` passes it. GET stays read-only.
- After `load()`, if `autoStart && view.available && view.outreach === null &&
  !autoStartTried`, call `start()` once (`autoStartTried` non-reactive, reset
  only when `workshopId` changes). Start is idempotent server-side.
- While starting: "Setting up links…". Failure: warning line + "Try again", no
  loop. Link-report failures: `linkReportMessage` as a warning in the Links row.
- Delete the "Start outreach to create…" paragraph and button.
- After `#0592`'s Reset the outreach row still exists (links kept), so the
  Announcement shows its empty state "No posts yet. Start with the
  announcement." with **Write the announcement** → `startOutreach` if needed →
  `draftAllOutreach(id, [])` → reload.

### B. "Customized" needs migration 000041
- Deriving it fails: Bluesky and Mastodon leads differ, so Mastodon would look
  customized on first load; comparing to a re-rendered template breaks when the
  summary changes; localStorage is per-browser.
- `migrations/000041_outreach_posts_customized.{up,down}.sql`: `ALTER TABLE
  outreach_posts ADD COLUMN customized BOOLEAN NOT NULL DEFAULT false;` / drop.
  (Check `ls migrations` for the next free number.)
- `internal/outreach/store.go`: `Post.Customized`, in every column list and scan;
  `UpdatePostBody(ctx, id, body, fingerprint *string, customized *bool)`;
  regenerate (`fingerprint != nil`) forces `customized=false`. Update the handler
  interface and fakes.
- `PatchPost`: `Customized *bool`, only with `body` (400 with regenerate); audit
  metadata records it. `postView.Customized`.
- TS: `OutreachPost.customized`; `updateOutreachPost(id, {body, customized?} |
  {regenerate: true})`.
- Go tests: store round trip, regenerate clears; PATCH `{body, customized:true}`
  sets, `{body}` leaves alone, `{regenerate, customized}` → 400.

### C. Pure lib — `web/src/lib/outreachMessage.ts` (+ tests)
- `isHashtagParagraph(p)` — `/^(#[\p{L}\p{N}_]+\s*)+$/u`.
- `splitBody(body, shortUrl)` → `{message, tail[]}`: split on `\n\n`, peel
  trailing paragraphs that are hashtag lines or contain `shortUrl`/`LINK_TOKEN`.
- `composeBody(message, tail)`.
- `messageTargets(view, wave)` — automatic, `!customized`, not posted/queued.
- `sharedMessage(view, wave)` — `splitBody` message of the first target.
- `planSaveMessage(view, wave, message)` → `{post, channel, body, over}[]`.
- `waveRowStatus(view, wave, results)` → `{text, action: 'write'|'open'}` ("Not
  written · Write it", "Drafted · not scheduled", "Posts automatically …",
  "Posted 2 of 2"); `manualRowStatus(view)`.
- `summaryLine` empty state → "No posts yet. Start with the announcement." (L3);
  never the slug.

### D. Components
- `OutreachWorkshop.svelte` top to bottom: header + `#0592`'s Reset button/dialog
  (gated on `can_reset`); `<OutreachPanel autoStart>`; body snippet: summary;
  `<OutreachAnnouncement wave="announce">`; `<OutreachWaveRow>` ×2 (reminder,
  last-call); Schedule all (from `OutreachBulkActions`); a "Post by hand"
  `<details>` with `<OutreachPostCards channelFilter>` for non-automatic
  channels; a "Flyers" row linking `adminOutreachFlyersHash()`; a "Links and
  clicks" row with `resultsSummaryLine` + Results link + link-report warning.
  Remove `OutreachLinks` from this page.
- New `OutreachAnnouncement.svelte` `{view, results, wave, onChange, reload,
  workshopId}`: Message textarea seeded from `sharedMessage` (re-seeded when not
  dirty); **Save message** runs `updateOutreachPost` sequentially over
  `planSaveMessage`, disabled if any `over` or unchanged; per-section
  `role="status"` ("Saved to Bluesky and Mastodon. Mastodon is customized and was
  left alone."). "How it will look": one card per automatic post (Pass 2 `<pre>`,
  Pass 3 previews) with count/limit, **Customize…** (opens composer with
  `markCustomized` → save sends `customized:true`) and a "Customized · Use shared
  message" badge (sends composed body, `customized:false`). **Post to Bluesky and
  Mastodon** via Pass 1's dialog — move the logic into a shared `postAll(items,
  onChange)` helper used by both callers. **Schedule for later…**: one
  `datetime-local`, `scheduleOutreachPost` per postable target. No posts → Write
  the announcement.
- New `OutreachWaveRow.svelte`: `<details>` whose summary shows label,
  `weekdayTime(suggested_at)` and `waveRowStatus`; **Write it** →
  `draftAllOutreach(workshopId, [wave.name])`, reload, open; body is
  `<OutreachAnnouncement wave={wave.name}>`.
- `OutreachBulkActions.svelte`: remove Draft all, "Also draft" fieldset and the
  procedure paragraph (C10, E2, L2); keep Schedule all and its dialog (§6).
- `OutreachComposer.svelte` (G2): short URL, "Goes to" and UTM chips under
  `<details><summary>Link details</summary>`; drop the duplicate `<h3>` in card
  mode (C8).
- `OutreachEditorSummary.svelte`: `view.outreach === null ? existing text :
  summaryLine(view, null)`; slug gone.

### Tests
- New `OutreachAnnouncement.behavior.test.ts`: Save message composes Bluesky
  `message+"\n\n"+tags` and Mastodon `message+"\n\n"+cta+"\n\n"+tags`; skips
  customized; Customize → Save sends `customized:true`; Save disabled when over.
- New `OutreachWaveRow.behavior.test.ts`: Write it → `draftAllOutreach(id,
  ['reminder'])`, row opens.
- `OutreachWorkshop.behavior.test.ts`: new order; no "Tracked links", no "Start
  outreach"; auto-start calls `startOutreach` exactly once for available
  `outreach:null`, never for `available:false`; Reset still present.
- `OutreachPanel`, `OutreachBulkActions`, `OutreachPostCards`, `outreachCards`
  and `WorkshopEditor*` tests updated to match (delete Draft-all / Also-draft
  tests; move tracked-links/summary tests).

---

## Pass 3 — #0583: platform-faithful previews

### Go
- `outreachWorkshopView`: `Summary *string`, `CoverImage *string` from the
  workshop. `outreachPanelView.BlueskyCard string` (`"short"`|`"canonical"`) via
  `SetCardURL(fn)` wired to `serverSettings.BlueskyCardURL`; report `"short"`
  whenever the publisher would fall back (`baseURL == ""`).
- Go test: GET carries summary, cover_image, bluesky_card (default short;
  canonical via setter).

### Pure lib — `web/src/lib/outreachPreview.ts` (+ tests)
- `segments(body)` → `{kind: 'text'|'hashtag'|'link', text}[]`.
- `hostOf(url, fallbackHost)`.
- `blueskyCard(view, post, fallbackHost)` → `{title, description, image, host}`;
  image only for a `/media/` cover.
- `mastodonLinkCard(view, fallbackHost)`; image = cover or
  `/workshops/{slug}/og.png`; null when the body has no URL.
- `identicalAutomaticBodies(view, wave)`; `copyablePostLink(post)` (safe
  `remote_url`).

### Components
- New `OutreachBlueskyPreview.svelte`: "Bluesky · {account}", text with styled
  hashtags, external card mock (image, title, clamped description, host); "No
  image: the card goes out without a picture" without a `/media/` cover;
  count/limit.
- New `OutreachMastodonPreview.svelte`: styled links/hashtags, link-preview mock;
  image-attachment block only when the workshop exposes `cover_alt` (#0572).
- Wire previews into `OutreachAnnouncement`, `OutreachPostDialog` and
  `OutreachPostCard` (automatic channels). Posted cards keep the preview and add
  **Copy link to post** (with the Copied flip). Identical text → static
  `.text-warn` "Bluesky and Mastodon will get exactly the same text."

### Tests
- Preview behaviour tests (hashtags styled, card host short vs canonical, no
  `<img>` for non-`/media/` cover, Mastodon og.png fallback, ALT block rules);
  identical-text warning present/absent; Copy link to post writes `remote_url`;
  `outreachPreview.test.ts`.

---

## Obstacles
1. `#0592` lands first; keep its button, dialog and `can_reset`.
2. The migration contradicts "prefer no migration"; justified in Pass 2 §B — say
   so in `## Resolution`. It must deploy before the binary that reads it
   (`scripts/deploy.sh` migrates first).
3. The Mastodon handle is not stored (only base URL + token), so the account
   reads "on mastodon.social". A real `@user@host` needs VerifyCredentials —
   follow-up.
4. Save message rewrites Mastodon's lead to the shared one (accepted by the
   review); tail detection relies on the CTA holding the link and a pure hashtag
   line. `#0575`'s server-side Adapt is the real fix.
5. jsdom cannot verify `preventScroll`, spinner animation or line-clamp.
6. Grid mode in `OutreachPanel` becomes dead code after Pass 2 — leave it whole
   or remove it whole later.
7. Full flyer section is batch 9; "Post by hand" still lists TikTok/YouTube until
   batch 8.
