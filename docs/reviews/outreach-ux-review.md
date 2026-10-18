# Outreach UX review (Fable, 2026-10-18)

A standalone whole-workflow review of the admin **Outreach** feature and the
**Settings** panels it depends on, written for Brennan and for the agents who
will fix it. The earlier content review
([`outreach-content-review.md`](outreach-content-review.md), 2026-10-17)
asked whether the *posts* were worth reading; this one asks whether the
*screens* let a person get a workshop promoted without a manual.

**Evidence.** The 48 Safari screenshots and driver notes in
[`outreach-ux-2026-10-17/`](outreach-ux-2026-10-17/README.md), every PNG
viewed and every note read; the code behind each screen
(`web/src/views/admin/Outreach*.svelte`, `Flyer*.svelte`,
`ServerSettings*.svelte`, `WorkshopEditor.svelte`, `web/src/lib/outreach*.ts`,
`adminRouter.ts`, `internal/handlers/admin_outreach_*.go`) read only to
confirm or explain what the screenshots show; issues `#0543`–`#0549` and
`#0559`–`#0579` for intent. No code was changed, no browser was launched on
the host, no live service was contacted.

**Demo limits that are not product bugs** (from the walkthrough README):
`/media/` 404s locally so cover photos are missing; mock post links are
dead; the mock ShortLinks pre-fills click history before anything is posted.
Where a finding touches one of these it says so.

**Severity scale.** *Blocker*: stops the job or makes the operator unwilling
to finish it. *Major*: causes a wrong action, a lost minute, or a loss of
trust in what the screen says. *Minor*: friction. *Polish*: cosmetic.

**Attribution.** Each finding is tagged **[driver]** (from the walkthrough
notes, confirmed by me), **[own]** (my observation from the screenshots or
code), or **[pushback]** where I think the driver's note was wrong or
misframed and say why.

---

## 1. Verdict

**Yes, Brennan can get a workshop announced on Bluesky and Mastodon with
this today.** The `#0570`–`#0579` batch fixed the text (the Announce drafts
in screenshot 14 are sentences with the summary and hashtags, not an iCal
line), and Post now works end to end (22, 24). The delivery and tracking
machinery is sound and should be kept.

What he cannot do is do it **confidently** or **quickly**. The biggest single
obstacle is that the workshop outreach page is organised around the system's
inventory rather than around the job: five channels × three waves is fifteen
slots, plus nineteen tracked links, plus a "campaign", and the two posts that
actually matter, the Announce on the two networks that post themselves, are
visually indistinguishable from the thirteen that do not (12, 16). Nothing on
the page shows what a reader will see (the Bluesky post appears to have no
link at all, because the link card is invisible in the admin), and the moment
of commitment is a native OS alert reading "Post this to Bluesky now?" with
no preview, no account name, and no "this is public" (20). Everything else in
this review, the buried Settings panel, the test-connection trap, the time
zone mismatch, the Schedule-all dialog that lists four posts and offers to
schedule zero, is real and worth fixing, but it is this framing that makes
the feature feel like "a giant links table" even after the table was
collapsed: the page still presents the machine's model, and the person has
to translate.

---

## 2. The job to be done

From Brennan's side, for the Oct 31 LED Programming workshop:

> Tell people about Saturday's workshop on Bluesky and Mastodon, in our own
> voice, with the picture. A week out, nudge again if seats are left. Hang
> flyers at the four usual spots. Afterwards, see whether any of it brought
> people in.

That is four tasks, in order of frequency: **announce** (every workshop),
**remind** (some workshops), **flyer** (some workshops), **see results**
(occasionally). The current UI gives all four, plus links, plus video
channels, equal weight on one 4,000 px page, and puts the prerequisites
(connections, locations, Start outreach) on three other screens.

### "Post the Announce to both networks"

Assuming the three connections are already saved and tested.

| | Current flow (12 → 24) | Ideal |
|---|---|---|
| 1 | Admin → **Workshops** tab | Admin → **Workshops** (or Outreach) → the workshop row shows "Not promoted · Promote" |
| 2 | Click the workshop (editor opens, 2,500 px form) | Click **Promote** |
| 3 | Find the Outreach panel, click **Open outreach** | Page opens with the Announce already written and previewed as Bluesky and Mastodon will show it; edit if wanted |
| 4 | Read "Start outreach to create the ShortLinks campaign and every tracked link" and decide whether that is safe; click **Start outreach** | Click **Post to Bluesky and Mastodon** |
| 5 | Land on nine "Not drafted" cards; locate **Draft all** at the top; decide whether to tick Reminder / Last call | One in-app confirmation shows both previews and both account names; click **Post** |
| 6 | Click **Draft all**; read "Drafted 5 posts." (which 5? three are visible) | Page shows "Posted to Bluesky (view) and Mastodon (view) at 10:02 AM PT" |
| 7 | Scroll to **Bluesky · Announce**; read the text; wonder where the link is | |
| 8 | (Optionally Edit → Save) | |
| 9 | Click **Post now**; read an OS alert; click OK | |
| 10 | Watch "Posting now…" for 5–10 s with no spinner | |
| 11 | Scroll to **Mastodon · Announce**; read; (edit) | |
| 12 | Click **Post now**; OS alert; OK | |
| 13 | Wait again | |
| 14 | Scroll to the bottom to find the "Queued…" status line, or notice the card changed | |
| **Steps** | **~14 clicks, 6 decisions** (Start? which waves? per-card post vs schedule? ×2 confirm; what to do with Instagram/video cards) | **4 clicks, 1 decision** (edit or not) |

### "Print flyers for four locations"

Assuming the four locations already exist and outreach has been started.

| | Current flow (33 → 37) | Ideal |
|---|---|---|
| 1 | Outreach → **Flyers** sub-tab | On the workshop's outreach page, open **Flyers** |
| 2 | Pick the workshop in a dropdown; click **New flyer** | The flyer is already there: cover photo, default template, all four spots ticked, one copy each |
| 3 | Photo box is empty; click **Use cover image** | (Optionally change tagline / template / spots) |
| 4 | Type a tagline (otherwise the title block has no second line) | Click **Print 4 flyers** |
| 5 | Choose a template, a title position, a description, a short description (five decisions, two of them only for the half-size layout) | Browser print dialog; the page reminds you of the three settings |
| 6 | Tick four location checkboxes | |
| 7 | Click **Save** (nothing says Generate needs it) | |
| 8 | Click **Generate links and QR codes** (was disabled until now, with no reason shown) | |
| 9 | Notice the "Copies" spinners that appeared in the Locations list | |
| 10 | Click **Print all · full page** | |
| 11 | Browser print dialog, set Letter / 100 % / no margins / no headers | |
| **Steps** | **~11 clicks, 7 decisions**, with one hidden dependency (Save before Generate) and one prerequisite on another tab (Start outreach) | **3–4 clicks, 0–2 decisions** |

The ideal numbers are achievable with the endpoints that exist today; §4
says how.

---

## 3. Findings

### A. Information architecture and navigation

**A1. Outreach is three hops from the workshop, and the Outreach tab's empty
state sends you back where you came from.** (10, 09, 11) **Major. [driver +
own]** The Outreach tab's Workshops sub-view says "No workshop has outreach
yet. Open a published workshop under Workshops and choose Open outreach."
That is the one place a first-time user looks, and it answers with
directions to another tab. The editor's panel then offers **Open outreach**
for something that has not been opened yet (09), and the page it opens asks
you to **Start outreach** (11). Three screens, three verbs, zero posts.
*Fix:* the Outreach list shows every published, upcoming workshop whether or
not outreach exists, with a status chip ("Not started", "Announced", "2
posted · 4 scheduled") and one button per row. Starting is implicit: the
page creates the campaign and links the first time it needs them (see C2).

**A2. Thirteen top-level tabs in two rows (five rows on a phone), with
Outreach between Workshops and CRT session.** (01, 42) **Major. [driver]**
Nothing in the label says "social posts and flyers", and the mailing-list
tabs outnumber everything else. *Fix:* group the admin nav: *Mailing list*
(Subscribers, Pending, Suppressions, Deliverability, Campaigns, Interests),
*Workshops* (Workshops, Outreach), *System* (Settings, Users, Audit log, CRT
session). On narrow screens collapse to a select or an overflow menu.
`ADMIN_NAV_SECTIONS` / `ADMIN_SECTION_LABELS` in `adminRouter.ts` are the
one place to change; the subtab markup in `Admin.svelte` renders them.

**A3. The Outreach sub-nav's first item is "Workshops", directly under the
top tab "Workshops".** (10) **Minor. [driver]** *Fix:* label it "Posts" (or
drop the sub-nav item and make the list the tab's landing page).

**A4. "Open outreach" on a workshop with no outreach.** (09) **Minor.
[driver]** *Fix:* "Promote this workshop" when nothing exists; "Open
outreach · 2 posted" once it does. `OutreachEditorSummary.svelte` already
knows which case it is in.

**A5. The Workshops list has no outreach column.** (08) **Minor. [driver]**
You cannot see which upcoming workshops still need announcing. *Fix:* a
fourth column, "Outreach", with the same chip as A1; `listOutreach()`
already returns per-workshop counts.

**A6. The Overview's "Needs attention" box knows nothing about outreach.**
(01) **Minor. [driver]** It lists the mailing address, SES sandbox and
inbound mail, none of which an outreach user can act on. *Fix:* add
"Bluesky / Mastodon / ShortLinks: connected" (or "not connected: Settings →
Outreach") and "N upcoming workshops not yet announced".

### B. Settings and connections

**B1. Test connection tests the *saved* credential and reports success while
a different, unsaved value sits in the field.** (04) **Major. [driver,
confirmed in code]** `OutreachConnectionGroup.svelte`'s `runTest` posts to
`/admin/outreach-connections/{group}/test`, and
`AdminOutreachConnectionsHandler.test` calls `h.conns.Test(ctx, group)`
against stored configuration; the request carries nothing from the form. So
typing a new token and clicking Test says "Signed in to Mastodon as
@opencircuitsf" about the old token. This is the one place in the workflow
that can make an operator believe a broken credential works. *Fix:* when
the group has unsaved changes (`buildPatch(group, draft)` is non-empty),
disable Test and label it "Save, then test", or make the primary action
**Save and test** which runs both. The result line should say what it
tested: "Tested the saved token: signed in as …".

**B2. A failed test renders in the same green as success.** (06) **Major.
[driver, confirmed in code]** One `message` state feeds a single
`<p class="text-notice">`, and `.text-notice` is `color: var(--success)`
(`app.css:542`). "Mastodon rejected the access token." in green is colour
saying the opposite of the words, and colour-only status is also an
accessibility failure (M1). *Fix:* separate `ok`/`error` state; failures use
`text-error` with `role="alert"` and a leading "Could not connect:"; successes
keep `role="status"`. The group's "Configured" line should reflect the last
test ("Configured · last test failed").

**B3. Outreach connections is the sixth and last panel, about 3,000 px down
a 4,077 px page; nothing at the top or in the nav points to it.** (02, 43)
**Major. [driver]** The outreach page's unavailable message links to
`#/settings`, which lands at the top of that scroll. *Fix:* a jump list at
the top of Settings (one link per section), section anchors
(`#/settings?section=outreach`) that the outreach page links to, and one
**Outreach** section holding both the connections and the three outreach
server settings (card link, hashtags, posting hour), which today sit in a
different panel under the same word (B7).

**B4. Secret fields look empty.** (02) **Minor. [driver]** A blank password
box with "Set — saved in Settings" in small grey text under it reads as
"nothing here". *Fix:* placeholder `••••••••` plus a "Saved" badge next to
the label; the input's placeholder text "Enter a new token to replace it".

**B5. "Configured" stays on while the token is rejected.** (06) **Minor.
[driver]** See B2's fix.

**B6. Saved confirmation.** (05) **[pushback]** The driver wrote "No
confirmation that the save succeeded." Screenshot 05 shows "Mastodon saved."
in green under the buttons; the code sets it (`submit(...,
\`${label} saved.\`)`). The real problem is that saved, tested-ok and
tested-failed all share one small green line in the same spot, so none of
them registers. B2's fix covers it.

**B7. Outreach settings are split across two panels under the same word.**
(02) **Minor. [driver]** "Outreach" under Server settings (Bluesky card link,
hashtags, posting hour) and "Outreach connections" below it. Merge per B3.

**B8. Six sections with different save semantics, explained one field at a
time.** (02) **Minor. [driver]** "Takes effect immediately" versus "after the
service restarts" is repeated per field in grey. *Fix:* one badge per section
("Applies immediately" / "Applies at next deploy").

**B9. Disabled Save buttons are pale green and look like a secondary
primary.** (02) **Polish. [driver]** *Fix:* disabled state uses the neutral
grey the other disabled buttons use, not a tint of the accent.

### C. The workshop outreach page and post cards

**C1. The page is a 15-slot inventory; the two posts that matter are 2 of
15.** (12, 14, 16, 42) **Blocker for confidence. [own + driver]** After Draft
all, the operator sees Bluesky, Mastodon and Instagram cards for Announce,
then the same three for Reminder, then for Last call, with TikTok and
YouTube in a disclosure per wave, every card the same size with the same
button row. The mental model the page teaches is "fill the grid", which is
exactly the failure mode the content review named. The summary line frames
success as "Drafted: Announce 5, Reminder 5, Last call 5" (16). *Fix:* the
default view is the **Announcement**: one message, previewed per network,
one post button (§4). Reminder and Last call are collapsed sections ("not
written yet · Write it"); manual channels are a collapsed "Post by hand"
section; links are a one-line "N clicks so far · Results".

**C2. "Start outreach to create the ShortLinks campaign and every tracked
link for this workshop."** (11) **Major. [driver]** Two pieces of jargon, no
statement of outcome, and a gate before anything useful. *Fix:* remove the
gate. `startOutreach()` runs when the page first needs it (the first draft or
the first print), and the page copy says what the person gets: "We'll write
the announcement for Bluesky and Mastodon and set up links so you can see
who clicked." If a visible Start must stay (it does one network call to
ShortLinks), label it "Write the announcement".

**C3. Nothing shows what a reader will see.** (14, 18, 22) **Major. [driver +
own]** The Bluesky Announce card contains no URL (correct since `#0571`,
because the external card carries it) but the admin shows no card, so the
operator reads a post with no link and no picture. Mastodon shows a raw
`127.0.0.1:18081/shortlinks/s/u3co7n`. The cover image, the alt text, the
card title and domain, none of it is visible before Post now. This is
`#0575`'s preview half and it is the single biggest contributor to "I can't
tell what this will do". *Fix:* per-network preview components: a Bluesky
card mock (image, title, summary, host) under the text; a Mastodon post with
the link preview and the image with an ALT badge (once `#0572` lands);
hashtags styled. `WorkshopOutreach.workshop` needs to carry `summary` and
`cover_image` (one handler change); the Bluesky card host is already a
setting (`bluesky_card_url`).

**C4. Click counts on posts that have not been posted.** (13, 14, 16)
**Minor. [own; the driver dismissed this as demo data]** The *numbers* are
mock, but the *line* is real: `cellClicksLabel` renders "29 clicks" under a
"Not scheduled" draft whenever the link has a count. On production the count
will be 0 or a few bot hits, and a "0 clicks" line on every draft is noise
that teaches the operator to ignore the clicks line. *Fix:* show clicks only
on posted cards.

**C5. Feedback appears far from the action and stale messages persist.**
(14, 15, 27, 28, 29) **Major. [driver, with a correction]** "Drafted 5
posts." sits under the bulk-action paragraph at the top; "Queued…",
"Copied.", "Marked as posted." render in one `role="status"` paragraph
*after the last card*, at the bottom of a 2,000–4,000 px page (the driver
said "top"; `OutreachPostCards.svelte` renders `{notice}` after the
`{#each groups}` loop). Screenshot 27 shows the earlier "Queued. It posts
within a few seconds…" still on screen after four posts were scheduled. *Fix:*
status goes on the card it concerns (an inline line under the button row,
still a live region), plus a transient toast for bulk actions; every action
clears the previous notice (it does) *and* the card line times out or is
replaced by the card's new state.

**C6. The editor's summary and the outreach page disagree: "6 drafted"
versus fifteen drafted posts.** (17 vs 16) **Minor. [driver, cause found]**
`OutreachEditorSummary.svelte` counts `status === 'draft'`; after Draft all,
nine of the fifteen posts are manual-channel posts whose status is `manual`,
so they are not counted. The outreach page uses `summaryLine()` which counts
per wave. *Fix:* use `summaryLine` in both, and drop the campaign slug (C7).

**C7. "Campaign led-programming" exposes an internal identifier.** (17)
**Minor. [driver]** Nobody needs the slug here.

**C8. The card title repeats inside the editor.** (18) **Polish. [driver]**
"Bluesky · Announce" as the card heading and again as the composer heading.
*Fix:* the composer replaces the card body only; `#0579` made it replace the
display, so this is a leftover `<h3>`.

**C9. Seven buttons in the editor's action row, with Post now as the primary
beside Save.** (18) **Major. [driver]** Post now, Schedule, Save, Copy
content, Mark posted, Delete, Cancel. The most consequential action is the
brightest button in an *editing* context, two inches from Save, and Delete
is in the same row. *Fix:* the editor has **Save** and **Cancel**, with
Delete under a "More" disclosure or at the far end in the danger style.
Posting and scheduling happen from the card, after Save. (The card already
has Post now / Schedule…; the composer's copies are redundant since `#0579`.)

**C10. "Also draft: Reminder, Last call" checkboxes under the buttons, with
their meaning in the paragraph below.** (14, 15) **Minor. [driver]** The
checkboxes have no visible effect until Draft all is pressed, and after
drafting with them unticked, the stale "Drafted 5 posts." stays while you
tick them. *Fix:* fold them into the collapsed Reminder / Last call sections
("Write it" drafts that wave for every channel), so the choice is made where
its result appears.

**C11. Draft all's result line says "5 existing posts were left alone" but
nothing marks which five.** (16) **Polish. [driver]** Fine once C5 puts
status on the cards.

**C12. Saving an edit scrolls the card to the top of the viewport.** (19)
**Polish. [driver]** Focus returns to the Edit button (good); the jump is the
browser scrolling the focused element into view. *Fix:* `focus({
preventScroll: true })` or scroll the card's top into view with a margin.

### D. Posting, confirmations and feedback

**D1. Post now is a native `confirm()`: "Post this to Bluesky now?".** (20)
**Major. [driver]** No text, no account, no "public and cannot be unsent".
It looks like an OS error, it blocks screen readers inconsistently, and it is
inconsistent with the in-app Schedule all dialog thirty pixels away.
`OutreachPostCards.svelte`: `if (!confirm(\`Post this to ${c.channel.label}
now?\`)) return;`. *Fix:* an in-app dialog modelled on
`OutreachScheduleAllDialog.svelte` / `CampaignSendDialog.svelte`
(`role="dialog"`, focus, Escape): the rendered preview (C3), "Posts publicly
as @opencircuitsf.demo on Bluesky", **Post** / **Cancel**.

**D2. "Posting now…" for 5–10 seconds with no progress indicator and the
buttons gone.** (21, 23) **Major. [driver]** The panel polls every 10 s
(`POLL_MS = 10_000`), so a post that lands in 2 s can show "Posting now…" for
10. *Fix:* a spinner or progress text ("Usually under ten seconds"), and
poll faster immediately after a send (1 s, 2 s, 4 s, then 10 s).

**D3. The posted card loses Edit and shows only "view post".** (22, 24)
**Minor. [driver, partly]** The text sent is still shown (the card body), so
"no way to see the text actually sent" overstates it; what is missing is the
link the card carried (Bluesky) and a copy affordance. *Fix:* the posted
state keeps the preview (C3) and adds "Copy link to post".

**D4. Mark posted has no undo.** (29) **Major for the undo, [pushback] on
the confirm.** The driver wrote "One click, no confirmation". The code asks
`confirm(\`Mark this ${label} post as posted?\`)`; the driver had overridden
`window.confirm` to auto-accept earlier in the run, so the dialog never
appeared. What is true: after it, Copy text, Open flyer and Edit vanish
immediately and nothing can take it back (`cardButtons`: `edit: !posted`).
*Fix:* the same in-app dialog as D1 with an optional "Link to the post" field
(the composer already has one; the card path does not), and an "Unmark"
action on a manually posted card, or a ten-second undo in the card line.

**D5. Copy text / Copy link give no feedback at the button.** (28) **Minor.
[pushback, partly]** "Copied." does render, in the status paragraph at the
page bottom (C5), so "no visible feedback at all" is wrong but the
experience is the same. *Fix:* the button reads "Copied" for two seconds.

**D6. Two networks means two confirms and two waits.** (20–24) **Minor.
[own]** *Fix:* **Post to Bluesky and Mastodon** on the Announcement section:
one dialog, two `sendOutreachPost` calls, one combined status line.

### E. Scheduling

**E1. The Schedule all dialog lists four posts under "Will be scheduled" and
offers to "Schedule 0 posts", disabled.** (25) **Major. [driver]** Reminder
and Last call are opt-in (`#0574`), so the dialog's checkboxes start
unticked and `chosen` is empty, but the list still shows all four in full
weight. The heading says "Schedule all drafted posts?" and the button says
zero. *Fix:* if the operator has already drafted Reminder and Last call (they
exist, so they opted in), tick them by default; otherwise list only what is
chosen and grey the rest with "Include". Heading: "Schedule the Reminder and
Last call?".

**E2. The wave toggles exist twice: "Also draft" on the page and "Include"
in the dialog.** (25) **Minor. [driver]** Collapses with C10.

**E3. Instagram, TikTok and YouTube are silently absent from the plan.**
(25) **Minor. [driver]** *Fix:* one line: "Posts you make by hand (Instagram,
TikTok, YouTube) are not scheduled."

**E4. Times are fixed at 5:00 PM and cannot be changed in the dialog.** (25,
27) **Minor. [driver]** The hour is a server setting
(`outreach_posting_hour`). *Fix:* show "at 5:00 PM PT (change in Settings)"
in the dialog, and let a per-row time be edited inline for the one-off case.

**E5. The composer's Schedule field prefills today 12:30 PM with "No
suggested time for this wave, or it has passed. Pick one."** (18) **Minor.
[driver]** Announce has no suggestion by design (`defaultScheduleAt` returns
null for `announce`). *Fix:* for Announce: "The announcement usually goes out
now. Pick a time only if you want to wait." For a passed suggestion: "The
suggested time (Sat Oct 24, 5:00 PM) has passed."

**E6. Scheduled times carry no zone.** (27) See K1.

### F. Manual channels

**F1. Instagram, TikTok and YouTube are equal-weight siblings of the
automatic channels, drafted by Draft all and counted everywhere.** (12, 14,
38, 48) **Major. [driver + own]** Draft all creates TikTok and YouTube
drafts inside a closed disclosure, so "Drafted 5 posts." shows three cards,
and Results says "3 of 15 posted" (I1). Brennan may never post a video. *Fix:*
a per-channel **enabled** toggle in Settings → Outreach (default: Bluesky,
Mastodon, Instagram on; TikTok, YouTube off). Disabled channels are not
drafted, not counted, and not shown. Enabled manual channels live in one
collapsed "Post by hand" section on the workshop page.

**F2. "Open flyer" on the Instagram card goes to the Flyers list, which is
empty.** (29, 33) **Minor. [driver]** `adminOutreachFlyersHash()` is the
generic list. *Fix:* link to this workshop's flyer when one exists
(`listOutreachFlyers()` filtered by workshop), otherwise "Make a flyer for
this workshop" which opens the editor with the workshop preselected; better
still, "Download image" of the flyer face as a PNG, which is what Instagram
actually needs.

**F3. Posting by hand takes two copies (text, then link).** (28) **Minor.
[driver]** *Fix:* "Copy caption" copies the caption; the link row is labelled
"Put this in your bio" so its separate existence makes sense.

**F4. Video-channel captions print the sign-up link twice.** (48) **Minor.
[driver, confirmed]** The body contains "Sign up: <url>" and the card also
renders the "Link to paste" row. *Fix:* hide the link row when the body
already contains `short_url`.

### G. Links and tracking

**G1. The tracked-links table is collapsed (good, `#0569`) but still a
19-row table of full UTM destinations with no stated purpose.** (13)
**Minor. [own]** Nineteen rows of `utm_campaign=led-programming&utm_content=…`
wrapping mid-word tell the operator nothing they can act on. *Fix:* move the
table to the Results page; on the workshop page show one line, "12 clicks so
far · Results", and an "Advanced: links" disclosure only if someone asks
for it.

**G2. Every Edit shows the short URL, "Goes to <long URL>", and four UTM
chips.** (18) **Minor. [own]** It is correct information in the wrong
moment; the operator opened the editor to change a sentence. *Fix:* collapse
under "Link details".

**G3. Clicks before posting.** (13) Demo data; the design point is C4.

### H. Flyers and locations

**H1. The flyer flow has a hidden dependency chain: Start outreach (other
tab) → New flyer → tick locations → Save → Generate (disabled until then,
no reason shown) → Print.** (33, 34, 35) **Major. [driver]** The Flyers list
says "Start outreach for a workshop first"; the editor's Generate button is
`disabled={busy || !!textError || selected.length === 0}` and nothing says
why; "Choose locations and save." under Generated links is the only hint.
*Fix:* one primary action, **Print N flyers**, that saves, generates any
missing links, and opens the print view; Generate becomes an internal step
with a progress line. The flyer lives on the workshop's outreach page (§4),
so "which workshop" is never asked.

**H2. The preview scrolls out of view on desktop and sits below the whole
form on a phone.** (34, 44) **Major. [driver]** A flyer editor where you
cannot see the flyer while editing. *Fix:* `position: sticky` preview column
on desktop; on narrow screens put the preview first (collapsed to a
thumbnail that expands).

**H3. The photo defaults to nothing even when the workshop has a cover
image.** (34) **Minor. [own]** The driver had to click **Use cover image**.
*Fix:* new flyers start with the cover image.

**H4. Locations all unticked by default.** (34) **Minor. [own]** The point of
saved locations (`#0548`) is that they are "the same boards every time".
*Fix:* all active locations ticked, copies 1.

**H5. The generated-links list is ragged: QR images alternate left and
right, "QR (SVG)" and "Mark put up" are bare links.** (35) **Minor.
[driver]** *Fix:* a table: Location · Code · Short link · QR (download) ·
Put up (toggle).

**H6. Print view: "Could not load: photo, photo, photo, photo."** (36, 37)
**Minor. [driver]** A demo artifact (no `/media/`), but the copy is bad
regardless and a missing photo on a printed flyer is a real failure mode.
*Fix:* "The photo could not be loaded for 4 flyers. They will print with a
plain tint." and a visible placeholder on each sheet.

**H7. The Add location form shows "Name is required." and "Code is
required." before anything is typed.** (31) **Minor. [driver, confirmed]**
`errors` is derived from `validateLocationForm` on the empty draft and
rendered unconditionally. *Fix:* show a field's error after it has been
touched, or on submit.

**H8. Code has no format hint and no suggestion.** (31) **Minor. [driver]**
*Fix:* auto-fill from the name (`civic-center-makerspace` → `civic-mkr` is a
human edit, but `civic-center-makerspace` is a fine default), editable until
a link uses it; help text "letters, digits and hyphens".

**H9. Deactivate is a plain link beside Edit with no confirmation or undo.**
(30) **Polish. [driver]** *Fix:* confirm, or an "Undo" in the status line.

**H10. An empty gap above Add location.** (30) **Polish. [driver]**

**H11. The print view is good.** (36, 37) **[own]** Chrome-less, two layouts,
clear print settings, cut marks on the half-size sheet. Keep it as the
model for the rest.

### I. Results

**I1. "3 of 15 posted" counts every drafted post including the video
channels nobody intends to post.** (38) **Major. [driver, reframed]** The
driver wrote that 15 "counts every channel slot including video channels
never drafted"; they *were* drafted (Draft all drafts them, hidden under the
disclosure), and `posts_total` sums every post row. Either way the number
reads as failure. *Fix:* count enabled channels only (F1), and word it as
"Posted: Bluesky, Mastodon, Instagram · 4 scheduled".

**I2. Two different click totals for the same channel on one page.** (39)
**Major. [driver]** Legend "Bluesky: 40 clicks" (the chart window) versus
"55 clicks" in the channel table and the link rows. Nothing says which is
which. *Fix:* one definition; if the chart window must differ, label the
legend "in the last 7 days" and the table "all time".

**I3. The x-axis clips the last label.** (39, 45) **Minor. [driver]**

**I4. "Scans this workshop" and "Scans all workshops" are identical
columns.** (39) **Minor. [driver]** *Fix:* hide the second until more than
one workshop has flyer links at that location.

**I5. The page is summary, chart, channel table, 19-row link table, flyers,
mentions, with no hierarchy.** (39) **Minor. [driver]** *Fix:* summary and
chart first; the per-link table collapsed; mentions as their own panel.

### J. Mobile

**J1. Results and Locations overflow the page horizontally at 390 px.**
(45, 46) **Major. [driver]** The Locations table has its own scroll
container (the panel-level scrollbar is visible in 46) but the page still
scrolls to ~608 px, so something outside the container (the table's
`min-width`, or the panel) is wider than the viewport; Results is ~660 px.
*Fix:* `overflow-x: auto` on the scroll wrapper *and* `max-width: 100%` on
the panel; on phones, render Locations as cards (name, address, code,
Edit/Deactivate) rather than a six-column table.

**J2. Five rows of nav tabs.** (42) See A2.

**J3. Card action rows wrap into cramped two-line clusters with the "Open
flyer" link squeezed between buttons.** (42) **Minor. [driver]** *Fix:* under
480 px, stack the buttons full-width with the primary first.

**J4. Flyer editor preview below the form.** (44) See H2.

### K. Time zones and consistency

**K1. The admin shows workshop and post times in the browser's zone while
posts and flyers say Pacific.** (08, 09, 11, 14, 22, 27) **Major. [driver +
own; the driver half-dismissed it as a VM artifact]** `formatDateTime` in
`lib/admin.ts` calls `toLocaleString` with no `timeZone`; no file in
`web/src` mentions `America/Los_Angeles`. In the VM (UTC) the 6:30 PM Oct 31
workshop reads "Nov 1, 2026, 1:30 AM" in the list, the editor, the outreach
header and Results, while the post text, the flyer and the Go side say
"Saturday, Oct 31". This is not only a VM problem: Brennan on a trip, or a
second admin anywhere else, sees a workshop on the wrong day, and the
`datetime-local` inputs in the editor interpret typed values in that same
zone. *Fix:* every admin date for a workshop or a post is rendered with
`timeZone: 'America/Los_Angeles'` and a "PT" suffix; the editor's Starts /
Ends and the composer's Schedule input are labelled "Pacific time" and
converted with that zone, not `getTimezoneOffset()`.

**K2. Results days are UTC.** (39) **Minor. [own]** "Days are UTC" is honest
but wrong for a San Francisco event; a 6:30 PM post lands on the next UTC
day. *Fix:* bucket by Pacific day on the server.

**K3. Two different "Announce" concepts in the workshop editor.** (09)
**Minor. [driver]** The social *Announce* wave and the **Announce to list**
email button at the bottom of the form. *Fix:* rename the email action
"Email the list" / "Draft an email campaign".

### L. Copy and terminology

**L1. Jargon throughout: "ShortLinks campaign", "tracked link", "Prepare",
"campaign led-programming", "UTM", "PDS URL", "wave", "Held".** (11, 17, 18,
02) **Major. [driver + own]** Each is correct and each is a word the operator
has to translate. *Fix:* a terminology sweep: "sign-up link", "short link",
"Bluesky server (leave as bsky.social unless you self-host)",
"announcement / reminder / last call" as section names rather than "wave".

**L2. The two-sentence procedure paragraph under Draft all.** (12) **Minor.
[own]** "Draft all writes the Announce post for every channel, and the
Reminder and Last call posts you tick, and never changes an existing post.
Schedule all shows a preview…" is documentation inline because the controls
are not self-explanatory. Goes away with C1/C10.

**L3. "Nothing drafted yet · 0 scheduled · 0 posted" in bold as the empty
state.** (12) **Minor. [driver]** *Fix:* "No posts yet. Start with the
announcement." and a button.

**L4. The Reminder and Last call templates still read templated ("One week
out:", "Tomorrow:").** (16) **Minor. [driver]** Acceptable as a starting
point; `#0575`'s shared message is what makes them feel written.

**L5. Not-found states.** (40, 41) **Minor. [driver]** `#/outreach?view=workshop&workshop=999`
shows a "Loading…" heading that never resolves over "workshop not found" with
a Retry that cannot succeed, and the header still offers "Edit workshop".
The flyer case is a bare lowercase line. *Fix:* a proper not-found state:
"This workshop no longer exists." with "All outreach".

### M. Accessibility

**M1. Colour-only distinction between success and failure.** (06) **Major.
[own]** WCAG 1.4.1; fix with B2.

**M2. Native `confirm()` dialogs.** (20, 29) **Major. [driver]** Inconsistent
with assistive technology and untestable; fix with D1 / D4.

**M3. Live-region status lines far from the control.** (14, 27, 28) **Minor.
[own]** They are announced (the always-rendered `role="status"` convention is
right) but sighted keyboard users lose them. C5's inline status keeps the
live region and puts it where the eye is.

**M4. The Add location form sets `aria-invalid` and error text on load.**
(31) **Minor. [own]** A screen reader hears two errors before the user has
done anything. Fix with H7.

**M5. The links table reads a 120-character URL per row.** (13) **Minor.
[own]** Fix with G1.

**M6. Things that are right.** The focal-point control is a real
`role="slider"` with keyboard steps (34); the Schedule all dialog has
`role="dialog"`, focus and Escape; the chart has a text description; status
lines use persistent live regions; the print toolbar states the print
settings. Keep these patterns.

---

## 4. A proposed redesign of the core flow

Everything below uses endpoints that exist today unless marked **new**.

### 4.1 Where it lives

- **Workshops** list gains an Outreach chip per row (A5). The **Outreach**
  tab's landing page lists upcoming published workshops with the same chip
  and a **Promote** / **Open** button (A1); sub-nav: *Posts* · *Flyer spots*
  · *Results*. Flyers are no longer a global list: a flyer belongs to a
  workshop and is a section of that workshop's outreach page (the global
  list, if kept at all, lives under Results).
- The workshop **editor** keeps its small Outreach panel with the one-line
  status and the button (A4).

### 4.2 The workshop outreach page, default view

Starting outreach is implicit: the page calls `startOutreach()` the first
time it is opened for an available, published workshop (or on the first
Write/Print), and shows a one-line "Setting up links…" while it does.

```
LED Programming                                       Edit workshop · Results
Sat Oct 31, 6:30 PM PT · Mission Makerspace Annex · Published

┌─ Announcement ──────────────────────────────────────────── Not posted ─┐
│                                                                          │
│  Message                                                                 │
│  ┌────────────────────────────────────────────────────────────────────┐  │
│  │ We're running LED Programming at Mission Makerspace Annex on       │  │
│  │ Saturday, Oct 31.                                                  │  │
│  │                                                                    │  │
│  │ Wire up an LED strip and make it dance: a hands-on evening of      │  │
│  │ microcontroller programming for complete beginners.                │  │
│  └────────────────────────────────────────────────────────────────────┘  │
│  Hashtags: #SanFrancisco #Electronics                 [ Save message ]    │
│                                                                          │
│  How it will look                                                        │
│  ┌─ Bluesky · @opencircuitsf.demo ───┐  ┌─ Mastodon · @opencircuitsf ──┐ │
│  │ We're running LED Programming …   │  │ We're running LED Programm… │ │
│  │ #SanFrancisco #Electronics        │  │                              │ │
│  │ ┌───────────────────────────────┐ │  │ Spots are limited, sign up:  │ │
│  │ │ [cover image]                 │ │  │ go.opencircuitsf.com/u3co7n  │ │
│  │ │ LED Programming               │ │  │ #SanFrancisco #Electronics   │ │
│  │ │ Wire up an LED strip and …    │ │  │ ┌──────────────────────────┐ │ │
│  │ │ opencircuitsf.com             │ │  │ │ [cover image]       ALT  │ │ │
│  │ └───────────────────────────────┘ │  │ └──────────────────────────┘ │ │
│  │ 221 / 300            Customize…   │  │ 288 / 500        Customize…  │ │
│  └───────────────────────────────────┘  └──────────────────────────────┘ │
│                                                                          │
│  [ Post to Bluesky and Mastodon ]      Schedule for later…               │
└──────────────────────────────────────────────────────────────────────────┘

▸ Reminder · a week before (Sat Oct 24, 5:00 PM PT)     Not written · Write it
▸ Last call · the day before (Fri Oct 30, 5:00 PM PT)   Not written · Write it
▸ Post by hand · Instagram                              Caption ready · Copy
▸ Flyers · 4 spots                                      Not printed · Make flyer
▸ Links and clicks                                      12 clicks · Results
```

- **Message** is one textarea. In the first implementation it is the Bluesky
  Announce body without its hashtag line; **Save message** writes it to both
  automatic Announce cards through the existing `updateOutreachPost(id,
  {body})`, appending the Mastodon link line and the hashtags client-side
  (the Go templates already know the per-channel shape, so the clean version
  is `#0575`'s server-side `Adapt` behind a **new** `PATCH
  …/outreach/{id}/message`; the client-side version is a day's work and gets
  the shape right). **Customize…** opens the existing composer for one card
  and marks it "customized" so Save message leaves it alone.
- **How it will look** is two **new** SPA components fed by data the view
  already has or nearly has: `post.body`, `post.short_url`, the channel's
  handle (from `getOutreachConnections()`, or a **new** `account` field on
  `OutreachChannel`), and the workshop's `title`, `summary`, `cover_image`
  (add the last two to `WorkshopOutreach.workshop` in
  `admin_outreach.go`). No network calls to the platforms.
- **Post to Bluesky and Mastodon** opens the dialog in 4.3 and then calls
  `sendOutreachPost` for each. **Schedule for later…** opens the existing
  per-post schedule picker for both at once (two `scheduleOutreachPost`
  calls).
- The collapsed rows reuse what exists: **Write it** calls
  `draftAllOutreach(workshopId, ['reminder'])` and expands the section to
  the same message-plus-previews layout; **Post by hand** holds the manual
  cards as they are today; **Flyers** holds the flyer editor (4.4); **Links
  and clicks** is `resultsSummaryLine` plus a link.

### 4.3 Confirming a post

```
┌─ Post the announcement? ────────────────────────────────────────┐
│ This goes out publicly, right now, and cannot be unsent.        │
│                                                                 │
│  Bluesky · @opencircuitsf.demo        Mastodon · @opencircuitsf │
│  ┌──────────────────────────┐         ┌───────────────────────┐ │
│  │ (preview, as above)      │         │ (preview, as above)   │ │
│  └──────────────────────────┘         └───────────────────────┘ │
│                                                                 │
│                                        [ Cancel ]  [ Post ]     │
└─────────────────────────────────────────────────────────────────┘
```

Same `role="dialog"` pattern as `OutreachScheduleAllDialog.svelte`. After
**Post**, the Announcement section header becomes "Posting…" with a
spinner, polling at 1 s / 2 s / 4 s / 10 s, then "Posted to Bluesky (view)
and Mastodon (view) · 10:02 AM PT", and the section collapses to that one
line with the previews under a disclosure. A failure on one network shows on
that network's preview with **Retry** (existing `canRetry` path) and does
not hide the other's success.

### 4.4 Flyers, inside the workshop page

```
▾ Flyers · 4 spots                                              Not printed
┌──────────────────────────────┬─────────────────────────────────────────┐
│ Photo        [cover image ✓] │  (sticky)                               │
│ Tagline      Build something │  ┌─────────────┐  ┌─────────────┐       │
│              that glows      │  │ full page   │  │ half, two-up │       │
│ Layout       ● Full photo    │  │  preview    │  │  preview     │       │
│              ○ Photo top     │  └─────────────┘  └─────────────┘       │
│              ○ Split         │  4 sheets          2 sheets              │
│ Spots        ☑ Civic Center  │                                          │
│              ☑ Haight Co-op  │  [ Print 4 flyers ▾ ]  full page · half  │
│              ☑ Mission Lib.  │                                          │
│              ☑ SoMa Hardware │  Letter · 100 % · no margins · no        │
│ ▸ More (description, title   │  headers (the print page reminds you)   │
│   position, copies)          │                                          │
└──────────────────────────────┴─────────────────────────────────────────┘
After printing: a Spots table with QR download and a "Put up" toggle each.
```

**Print N flyers** does `updateOutreachFlyer` (if dirty) →
`setOutreachFlyerLocations` → `generateOutreachFlyer` (only if any link is
missing) → opens the existing print route. Defaults: cover image, full-photo
template, every active location, one copy. Description, short description,
title position and copies go under **More**.

### 4.5 What recedes

- The tracked-links table moves to Results. The workshop page shows one
  number.
- UTM chips and "Goes to" go under a "Link details" disclosure in the
  composer.
- Video channels are off by default (F1) and, when on, sit inside "Post by
  hand".
- "Campaign", "Prepare", "wave", "Held" never appear in the UI.

---

## 5. Prioritized fix plan

Ordered batches, each sized for one implementer pass. Effort: **S** under
half a day, **M** one to two days, **L** more. The first three unblock
"post the Announce confidently"; the rest can be taken in any order after
them except where noted. Do not file these as issues from this document
(AGENTS.md §9); the orchestrator allocates numbers.

### Batch 1 — Post with an in-app confirmation, inline feedback, and progress

*Closes:* D1, D2, D5, D6, C5, C12, M2, M3. *Effort:* **M**.

1. Post now, Retry and Mark posted open an in-app `role="dialog"` (modelled
   on `OutreachScheduleAllDialog.svelte`) showing the post text, the
   account that will post it, and "public, cannot be unsent"; no
   `window.confirm` remains in `web/src/views/admin/Outreach*.svelte`
   (a source-level test pins it).
2. A **Post to Bluesky and Mastodon** button on the Announce wave posts both
   automatic Announce cards from one dialog and reports both results on one
   line.
3. Each card renders its own status / notice line under its button row (a
   live region per card); the page-level notice paragraph is removed; a
   notice is replaced when the card's state changes.
4. While a post is queued the card shows a visible progress indicator and
   the panel polls at 1 s, 2 s, 4 s then 10 s until the post resolves.
5. Copy text / Copy link flip their label to "Copied" for two seconds.
6. jsdom tests: dialog opens with the account and text; both-networks path
   calls `sendOutreachPost` twice; the per-card notice appears on the right
   card.

### Batch 2 — Announcement-first page: one message, previews, everything else collapsed

*Closes:* C1, C2, C10, C11, F1 (page half), G1, G2, L2, L3, C6, C7, E2.
*Effort:* **L**. *Depends on:* Batch 1 for the dialog; pairs with Batch 3.

1. The workshop outreach page's default view is the Announcement section
   from §4.2: one **Message** field, per-network previews (Batch 3), one
   post button and one schedule link. Reminder, Last call, Post by hand,
   Flyers, and Links and clicks are collapsed rows with a status and one
   action each.
2. Start outreach is implicit: opening the page for an available published
   workshop (or the first Write / Print) creates the campaign and links;
   the "Start outreach to create the ShortLinks campaign…" screen and
   paragraph are gone; the link report, if anything failed, shows as a
   warning line.
3. **Save message** writes the shared text to every non-customized
   automatic card for that wave via `updateOutreachPost`; **Customize…**
   opens the existing composer for one card and marks it customized.
4. **Write it** on a collapsed wave calls `draftAllOutreach(id, [wave])`
   and expands it; the "Also draft" checkboxes are removed.
5. The editor's Outreach summary and the page's summary use one function
   (`summaryLine`) and neither shows the campaign slug.
6. The tracked-links table is removed from this page (it stays on Results);
   UTM chips and "Goes to" sit under a "Link details" disclosure in the
   composer.

### Batch 3 — Platform-faithful previews

*Closes:* C3, D3, L4 (partly). *Effort:* **M**. *Depends on:* `#0572` for the
Mastodon image; useful before it.

1. `WorkshopOutreach.workshop` carries `summary` and `cover_image`;
   `OutreachChannel` carries the configured `account` (handle) for Bluesky
   and Mastodon.
2. A Bluesky preview renders the text with hashtags styled and an external
   card mock (cover image, title, summary, host chosen by `bluesky_card_url`).
3. A Mastodon preview renders the text with the link and hashtags styled, a
   link-preview mock, and, once `#0572` lands, the image with an ALT badge
   or a "no image: alt text missing" note.
4. A posted card keeps its preview and adds "Copy link to post".
5. A warning appears on both previews when the two automatic bodies are
   byte-identical.
6. jsdom tests for each preview's content and the identical-text warning.

### Batch 4 — Settings: a trustworthy connection test and one Outreach section

*Closes:* B1–B9, M1. *Effort:* **M**.

1. Test connection is disabled and labelled "Save, then test" while a group
   has unsaved changes; its result line names what it tested ("Tested the
   saved token…"). A **Save and test** primary action is acceptable instead.
2. A failed test renders with `text-error` and `role="alert"`, prefixed
   "Could not connect:"; success stays `role="status"`; the group's
   "Configured" line reflects the last test result.
3. Secret fields show a masked placeholder and a "Saved" badge when set.
4. Settings has a jump list of sections at the top, each section is
   addressable (`#/settings?section=outreach`), and the outreach page's
   "Open Settings" links land on the Outreach section.
5. One **Outreach** section holds the connections and the three outreach
   server settings; each Settings section carries one "Applies immediately"
   / "Applies at next deploy" badge instead of per-field sentences.
6. Disabled primary buttons use the neutral disabled style.

### Batch 5 — Pacific time everywhere in the admin

*Closes:* K1, K2, E6, I6. *Effort:* **S–M** (web S, results bucketing M).

1. `formatDateTime` (and `weekdayTime`) for workshop and post times render
   in `America/Los_Angeles` with a "PT" suffix; a test asserts the Oct 31
   6:30 PM fixture renders as Oct 31 under a UTC `TZ`.
2. The workshop editor's Starts / Ends inputs and the composer's Schedule
   input are labelled "Pacific time" and convert through that zone, not
   `getTimezoneOffset()`; a test covers the 2026-11-01 DST boundary.
3. Results buckets days by Pacific date and the note says so.
4. No admin screen shows the same instant in two zones (the driver's
   Nov 1 1:30 AM / Oct 31 6:30 PM mismatch is a named regression test).

### Batch 6 — Navigation: grouped tabs, an Outreach list that lists workshops

*Closes:* A1–A6, J2. *Effort:* **M**.

1. The admin nav is grouped (Mailing list / Workshops / System) and
   collapses to a select or overflow on narrow screens; no more than two
   rows at 390 px.
2. The Outreach landing page lists every published upcoming workshop with a
   status chip and one button; the "No workshop has outreach yet. Open a
   published workshop under Workshops…" empty state is gone.
3. The Workshops list has an Outreach column with the same chip.
4. The Outreach sub-nav reads Posts · Flyer spots · Results.
5. The editor's panel button reads "Promote this workshop" when nothing
   exists and "Open outreach · N posted" otherwise.
6. The Overview's Needs-attention box reports disconnected outreach services
   and unannounced upcoming workshops.

### Batch 7 — Scheduling that agrees with itself

*Closes:* E1, E3, E4, E5. *Effort:* **S**.

1. The Schedule all dialog ticks a wave by default when its drafts exist,
   lists only chosen posts in full weight, and its heading names the waves
   ("Schedule the Reminder and Last call?"); the button is never "Schedule
   0 posts" while rows are listed in full weight.
2. The dialog states that by-hand channels are not scheduled.
3. Each row shows its time with the zone and a per-row inline edit; the
   dialog links to the posting-hour setting.
4. The composer's schedule hint for Announce reads "usually goes out now";
   a passed suggestion is named with its date.

### Batch 8 — Manual channels and Mark posted

*Closes:* F1 (settings half), F2, F3, F4, D4. *Effort:* **M**.

1. Settings → Outreach has a per-channel enabled toggle (default Bluesky,
   Mastodon, Instagram on; TikTok, YouTube off); disabled channels are not
   drafted, counted or shown anywhere.
2. Mark posted uses the Batch 1 dialog with an optional post-URL field, and
   a manually posted card offers **Unmark**.
3. The Instagram card's flyer link goes to this workshop's flyer (or
   "Make a flyer" preselecting the workshop) and offers the flyer face as a
   downloadable PNG.
4. "Copy caption" copies the caption; the link row is labelled for the bio;
   the link row is hidden when the body already contains the URL.

### Batch 9 — Flyers and spots: one Print action, sensible defaults, live preview

*Closes:* H1–H10, J4. *Effort:* **M**.

1. New flyers default to the cover image, the full-photo template, every
   active location and one copy; the preview renders without any click.
2. **Print N flyers** saves, generates missing links, and opens the print
   view in one action; Generate is no longer a separate operator step; a
   progress line names what it is doing.
3. The preview column is sticky on desktop and precedes the form on
   narrow screens.
4. Generated links render as a table (Location · Code · Short link · QR
   download · Put up toggle).
5. The print view's missing-photo message says how many flyers are affected
   and that they print with a plain tint; each affected sheet shows a
   placeholder.
6. The location form shows a field's error only after it is touched or on
   submit; Code is suggested from the name; Deactivate confirms or offers
   undo.

### Batch 10 — Results that add up, and fit on a phone

*Closes:* I1–I5, J1, J3. *Effort:* **M**.

1. The overview's posts cell reads as named channels and counts
   ("Posted: Bluesky, Mastodon · 4 scheduled"), counting enabled channels
   only.
2. The chart legend and the channel table use one definition of clicks, or
   each is labelled with its window; a test asserts legend = table for a
   fixture.
3. The x-axis's last label is not clipped; the "all workshops" flyer column
   appears only when more than one workshop has links.
4. The per-link table is collapsed by default; summary and chart come first.
5. No Outreach page scrolls horizontally at 390 px (Results, Locations);
   Locations renders as cards under 480 px; card button rows stack.

### Batch 11 — Words and dead ends

*Closes:* L1, L5, K3, C8, C9. *Effort:* **S**.

1. A terminology sweep removes "campaign", "Prepare", "tracked link",
   "wave", "Held", "PDS" from visible copy (a source-level test lists the
   banned strings against `web/src/views/admin/Outreach*.svelte`).
2. The editor's action row is Save and Cancel, with Delete separated;
   posting actions live on the card only; the duplicated title is gone.
3. The workshop editor's email button reads "Email the list".
4. Unknown workshop / flyer routes show a proper not-found state with a
   way back and no Retry or stale header links.

---

## 6. What is already right

So the fix passes do not regress it: the print view (H11); Schedule all's
dry-run preview with exact ids (`#0568`); opt-in Reminder / Last call
(`#0574`); the links table collapsed (`#0569`); Edit replacing the card in
place with focus return (`#0579`); the Bluesky link carried by the card, not
the text (`#0571`); the persistent live-region convention; the focal-point
slider; the write-only secret handling in connections (`#0564`); and the
content of the Announce drafts themselves since `#0570`, which is the part
Brennan first objected to and which no longer reads as a feed.
