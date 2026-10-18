# Outreach content review (Fable, 2026-10-17)

A standalone whole-feature review of the Outreach feature's **output**: the
posts it drafts and sends to Bluesky and Mastodon, and the captions it hands
the operator for Instagram, TikTok and YouTube. It does not re-review the
plumbing (`#0544`, `#0545`, `#0568`, `#0569`), which is solid; it asks whether
anyone would read, click or share what the plumbing delivers.

Read: `internal/outreach/{templates,channels,length,links,publisher,schedule_all}.go`,
`internal/bluesky/{client,facets}.go`, `internal/mastodon/client.go`,
`internal/workshops/store.go` (the `Workshop` model), `internal/seo/workshop.go`,
`web/src/views/admin/{OutreachPostCards,OutreachComposer,OutreachBulkActions}.svelte`,
`web/src/lib/{outreachCards,outreach}.ts`, and issues `#0544`, `#0545`,
`#0568`, `#0569`. No code was changed, no service was contacted.

The trigger, from the operator on seeing the Announce drafts for workshop 6:

> This will be clearly seen as poorly automated content which will be quickly
> ignored or even blocked. We cannot do this.

The draft in question, byte-identical for Bluesky and Mastodon:

    New workshop: Programming LEDs
    Mon, Oct 19 · 6:30 PM · Sequoia Fabrica

    Details and signup: https://go.opencircuitsf.com/pcNPlc

## 1. Verdict

The operator is right. The drafts are a calendar entry with a shortened URL
under it: no sentence, no reason to care, no picture, no description of what
happens in the room, and the same bytes pushed to two networks whose users
notice exactly that. On Mastodon in particular this is the shape of account
people mute. **The delivery machinery is good and should be kept; the
content layer on top of it needs to be redesigned around "a person writes one
message and the tool adapts and previews it per platform", with defaults that
are already acceptable before the person touches them.** None of the fixes is
large. The highest-value ones are a template rewrite that uses the workshop
summary, dropping the raw URL from Bluesky text because a card is already
attached, attaching the cover image with alt text on Mastodon, a few CamelCase
hashtags, and making the Reminder and Last-call waves opt-in.

## 2. What is wrong

### 2.1 The text reads as generated

- **No sentence, no voice.** `Draft` (`templates.go`) emits a label
  (`New workshop:`), a `·`-separated metadata line, and a CTA. That is the
  shape of an iCal export or a listing-site feed, not a person telling their
  neighbours about something they are excited to run. Social readers
  pattern-match this instantly.
- **Nothing about the workshop itself.** `Facts` carries `Slug`, `Title`,
  `StartsAt`, `LocationName`, `LocationAddress` only. The workshop row has
  `Summary` and `BodyMD` (`workshops.Workshop`), and the flyer subsystem
  already has a per-outreach `tagline` and `description`, but the post
  templates read none of them. The one thing a reader needs to decide whether
  to click, "what will I do and is it for me?", is absent.
- **The lead is the same for every workshop.** "New workshop:" / "One week to
  go:" / "Tomorrow:" prefixes every post for every workshop forever. A
  follower who sees three workshops a season sees the identical skeleton nine
  times.
- **A middle-dot metadata line.** `Mon, Oct 19 · 6:30 PM · Sequoia Fabrica`
  is how a machine prints a record. A person writes "Monday the 19th at 6:30
  at Sequoia Fabrica".
- **"Details and signup:" + a short URL.** A bare `go.<domain>/pcNPlc` is the
  single strongest "automated marketing" signal on the fediverse, where link
  shorteners are distrusted (they hide the destination and imply tracking).
  It is doubly odd on Bluesky, where `publisher.go` *also* attaches an
  external card pointing at the same URL, so the post shows the link twice.
- **No hashtags.** On Mastodon, hashtags are the discovery mechanism (there
  is no algorithmic feed); a post about a San Francisco electronics workshop
  that carries none of `#SanFrancisco`, `#Electronics`, `#Arduino`,
  `#Makerspace` is invisible to everyone who does not already follow the
  account. `bluesky.DetectFacets` already handles tag facets, so the only
  missing piece is the text.
- **No image on Mastodon.** `mastodon.Client` has no media upload ("No media
  and no card" in its package comment). Posts with an image get several
  times the engagement of text-only ones on both networks, and on Mastodon
  an image *with alt text* is also a community-norm signal that a human is
  behind the account. The cover image exists and is already read from disk
  for the Bluesky thumbnail (`Publisher.thumbnail`).
- **Identical cross-posting.** `Draft` varies only for `instagram`. Bluesky
  and Mastodon receive the same bytes. Many people follow the same account
  on both networks; seeing the identical post twice is the most visible
  signature of a cross-poster bot. The networks' own norms differ too
  (Bluesky: short, card does the work; Mastodon: longer, hashtags, alt text,
  CW etiquette), so one text cannot be right for both.
- **The timing is mechanical.** `SuggestedAt` is `startsAt - N days`, so the
  Reminder posts at exactly 6:30 PM a week before and the Last call at
  exactly 6:30 PM the night before. Posts landing at the same minute as the
  event's start time, seven and one days out, are another machine tell, and
  6:30 PM on a weekday is not a particularly good posting hour.
- **Three waves per channel is a lot for a small account.** Fifteen posts
  (5 channels × 3 waves) for one workshop; for the two automatic channels,
  three near-identical posts a week apart about the same event. For an
  account with a few hundred followers this reads as spam long before it
  reads as a campaign. The Reminder and Last call are useful *sometimes*
  (seats genuinely left, a real update to share), which argues for opt-in,
  not default-on with a **Schedule all** button that queues all of them.

### 2.2 Platform specifics

**Bluesky.** The card is already right in shape: `app.bsky.embed.external`
with title, description (`Summary`), and a JPEG/PNG thumbnail; facets for
links and tags; `langs: ["en"]`. Two problems:

1. The text repeats the URL that the card already carries. Bluesky's own
   composer removes the URL from text when it becomes a card; posts that
   keep both look pasted.
2. The card's `uri` is the short link, so the card footer reads
   `go.opencircuitsf.com`. That is a brand subdomain, so it is tolerable, but
   it is weaker than `opencircuitsf.com`. There is a real trade-off here
   (tracking lives in ShortLinks; the site deliberately has no analytics,
   AGENTS.md §9), and it should be an explicit per-channel choice, not an
   accident of `*job.ShortURL` being the only URL at hand. Do **not** solve
   it by making the facet's display text differ from its `uri`: Bluesky
   clients flag mismatched link text as misleading.

**Mastodon.** `PostStatus` sends `status`, `visibility: public`,
`language: en`, and an `Idempotency-Key`. Missing, in order of how much they
matter to how the post is received:

1. Media with alt text (`POST /api/v2/media` then `media_ids`). Alt text is
   expected on the fediverse; images without it get called out.
2. CamelCase hashtags (screen readers pronounce `#SanFrancisco` correctly and
   `#sanfrancisco` as one word).
3. The card. The instance builds it from the first URL in the text by
   following redirects, so the short link should work (ShortLinks answers
   302), and the workshop page already has OpenGraph tags (`internal/seo`).
   Worth confirming once on the live instance which host the card footer
   shows (the original or the final URL), because that decides whether the
   short link or the canonical URL belongs in Mastodon text.
4. Account hygiene, not code: the Mastodon account's **bot flag** should be
   *off* if a human writes and approves every post (it is a flag for
   unattended accounts and some clients de-emphasise flagged posts), the
   profile should say who runs it and link back to the site, and the site
   should carry a `rel="me"` link to the profile so the link verifies. The
   same applies to Bluesky's domain-handle verification.

**Instagram / TikTok / YouTube** (manual). The Instagram caption is
`Link in bio — https://go.opencircuitsf.com/...`: it says the link is in the
bio and then prints a URL that Instagram will not make clickable. Captions
also cannot be "copied and posted" without a picture or a video, which the
feature does not produce or point at; the flyer PNG from `#0561` is the
obvious asset to hand over alongside the caption. TikTok and YouTube captions
need a video; a text template for them is of marginal use, and `youtube` with
`utm_medium=video` suggests these were included for UTM completeness rather
than because the operator posts there.

### 2.3 The admin UI reinforces the problem

- `OutreachPostCards.svelte` renders the body (which contains the short URL)
  and then `<p class="mono short">{p.short_url}</p>` directly under it, so
  every card shows the link twice. The links table under "Tracked links"
  shows it a third time, with the UTM destination. The operator's eye lands
  on URLs and UTM chips, not on words.
- The composer edits one slot's text. There is no place to write *the
  message* once; fifteen textareas each start from the same template, so
  the natural path is to accept the template, which is the failure mode.
- Nothing shows what a reader will actually see: no card preview, no image,
  no hashtag rendering, no "this is identical to the Mastodon post" warning.
  The operator discovered the problem by reading raw drafts, which is the
  only view the UI offers.
- `summaryLine`'s "15 posts drafted · 0 scheduled · 0 posted" frames
  success as fifteen posts.

## 3. Example posts

All copy below is **example copy**. The real workshop summary is not in the
repository (it lives in the production database, which this review did not
touch); the sentences describing the workshop are plausible stand-ins for
what `workshops.summary` or the flyer `tagline`/`description` would supply.
Facts used: *Programming LEDs*, Monday 19 October 2026, 6:30 PM, Sequoia
Fabrica (San Francisco). Short links shown are placeholders.

### Bluesky

Bluesky text should carry **no URL**: the external card holds the link,
title, summary and cover image, and the facets make the hashtags clickable.
Counts are graphemes of 300.

**Announce** (282):

    We're running Programming LEDs at Sequoia Fabrica on Monday, Oct 19 at 6:30 PM.

    Wire up a strip of addressable LEDs, write a few lines of code to make them do something, and take the board home. No experience needed, and we have loaner laptops.

    #SanFrancisco #Electronics #Arduino

    [card: cover image · "Programming LEDs" · summary · opencircuitsf.com or go.opencircuitsf.com]

**Reminder, one week out** (269):

    One week out: Programming LEDs is Monday the 19th, 6:30 PM at Sequoia Fabrica.

    A few seats left. If you've been meaning to try making hardware do things, this is a gentle first step: blink an LED, then a whole strip, then whatever you want.

    #SanFrancisco #Electronics

**Last call, the day before** (201):

    Tomorrow night: Programming LEDs, 6:30 PM at Sequoia Fabrica. Last couple of seats, and we'd love to fill them.

    Bring curiosity; we bring the LEDs, the boards and the help.

    #SanFrancisco #Electronics

### Mastodon

Mastodon text **needs the URL** (the instance builds the preview from it),
has room for a fuller description, and should attach the cover image with
alt text. Counts use Mastodon's rule (URL = 23) of 500.

**Announce** (379):

    Next Monday (Oct 19, 6:30 PM) we're at Sequoia Fabrica for Programming LEDs. You'll wire up a strip of addressable LEDs, write your first few lines of code to make them do something, and take the board home with you.

    No experience needed, and there are loaner laptops. Spots are limited, so grab one here:
    https://go.opencircuitsf.com/pcNPlc

    #SanFrancisco #Electronics #Arduino #Makerspace

    [image: workshop cover · alt: "A breadboard with a strip of lit RGB LEDs wired to a small microcontroller on a workbench at Sequoia Fabrica"]

**Reminder** (341):

    One week to go: Programming LEDs is next Monday the 19th, 6:30 PM at Sequoia Fabrica, and there are still a few seats.

    It's a gentle first step into making hardware do things: blink one LED, then a strip, then whatever you can think of. Everything you need is provided.

    Sign up: https://go.opencircuitsf.com/example

    #SanFrancisco #Electronics #Arduino

**Last call** (227):

    Tomorrow night, 6:30 PM at Sequoia Fabrica: Programming LEDs. A couple of seats left and we'd love to fill them.

    Bring curiosity; we bring the LEDs, the boards and the help.

    https://go.opencircuitsf.com/example

    #SanFrancisco #Electronics

### Instagram (manual, needs the flyer image)

Caption for the flyer PNG. Instagram does not link captions, so the CTA
points at the bio and the one link the bio can hold is the workshop page,
not a per-wave short link. Hashtags go last and there can be more of them.

    Programming LEDs: Monday Oct 19, 6:30 PM at Sequoia Fabrica.

    Wire up a strip of addressable LEDs, write a few lines of code to make them do something, and take the board home. No experience needed, loaner laptops available.

    Sign up at the link in our bio.

    #SanFrancisco #Electronics #Arduino #Makerspace #LearnToSolder #SFMakers #OpenCircuitSF

Reminder and Last call on Instagram are better as **Stories** (a photo of
the kits being prepped, "a few seats left for Monday", link sticker to the
short URL) than as a second and third grid post about the same event.

### TikTok / YouTube (manual)

Only worth a slot when there is a video. The useful artifact is a
description block to paste under it, not a post:

    Programming LEDs — a beginner workshop at Sequoia Fabrica, San Francisco, Monday Oct 19 at 6:30 PM. Sign up: https://go.opencircuitsf.com/example

    Open Circuit SF runs hands-on electronics workshops in San Francisco. https://www.opencircuitsf.com

## 4. Recommended changes, ranked by impact

Effort: **S** under half a day, **M** one to two days, **L** more.

| # | Change | Impact | Effort |
|---|---|---|---|
| 1 | **Rewrite the templates around a sentence, the workshop summary and a hook; vary by channel.** `Facts` gains `Summary` (and the flyer `tagline` when set). The Announce lead is a sentence containing the date and venue in prose, then the summary, then (Mastodon only) the link, then hashtags. Bluesky and Mastodon templates differ in structure, not just length. Drop `·`. | Removes the "generated" shape from every default post. | S |
| 2 | **Bluesky: no URL in the text; the card carries it.** `Draft` for `bluesky` omits `LinkToken`; `ValidateForSend` stops requiring the token to be absent-after-substitution for a channel whose template never had it; the publisher keeps the card. Decide the card `uri` per channel (short link for tracking, or canonical URL for `opencircuitsf.com` in the footer) as a documented setting rather than a side effect. | Fixes the doubled link and the strongest bot tell on Bluesky. | S |
| 3 | **Mastodon: attach the cover image with alt text.** Add `UploadMedia` (`POST /api/v2/media`, poll 206 → 200) to `mastodon.Client`; `postMastodon` uploads the same bytes `thumbnail` already loads, with alt text from a new per-workshop `cover_alt` field (editor UI) and a refusal to attach when the alt text is empty. | Several-fold engagement lift; meets the fediverse's clearest norm. | M |
| 4 | **Hashtags, CamelCase, few, configurable.** A site-level default set (`#SanFrancisco #Electronics`) in Settings plus an optional per-workshop set (from its interests, or typed). Appended by the template; the Bluesky facet code already makes them clickable. | Discovery on Mastodon; zero cost elsewhere. | S |
| 5 | **Reminder and Last call become opt-in.** Draft all drafts Announce only by default, with a checkbox per extra wave; Schedule all schedules only waves that have drafts. Suggested times move to a posting hour (e.g. 10:00 Pacific), not the workshop's clock time, computed in `America/Los_Angeles`. | Cuts the per-workshop volume from 15 to 2–5 posts; removes the 6:30-PM-exactly tell. | S |
| 6 | **"Write it once" composer.** One **Message** textarea per outreach (seeded from template #1) above the cards; each channel card shows its adapted rendering (Bluesky without URL + card mock; Mastodon with URL, image, alt; Instagram with link-in-bio) and an **Edit this one** override. Saving the message re-renders every card that has not been overridden. | Makes the human-authored path the default path, not a fifteen-textarea chore. | M–L |
| 7 | **Render-accurate previews.** In each card, show what the platform shows: the card (image, title, summary, domain), the image with its alt badge, hashtags styled, grapheme/character count. Warn when two automatic channels have byte-identical text. | The operator sees the problem before posting, not after. | M |
| 8 | **Card and table clean-up.** Remove the second `short_url` line under the body; show the link once, labelled, only for manual channels (where it must be copied). Fix the Instagram caption to "link in bio" without the URL; offer the flyer PNG next to Copy text. | Small, visible, cheap. | S |
| 9 | **Stale detection includes the summary** once templates use it (`Fingerprint`). | Correctness follow-on to #1. | S |
| 10 | **Account hygiene checklist** in `docs/`: bot flag off, profile bio and site link, `rel="me"` on the site, Bluesky domain handle, post cadence guidance (how many posts per workshop, reply to replies). | Not code, but the difference between a tolerated account and a muted one. | S |

Items 1, 2, 4, 5 and 8 together are about a day's work and would make the
default output acceptable to post. Item 3 is the single biggest engagement
lever after that. Items 6 and 7 are the UX that keeps it good over time.

## 5. Correctness notes found on the way

None of these is a blocker; they are recorded so they are not lost.

1. **Link displayed twice on every card.** `OutreachPostCards.svelte` prints
   the body (containing the short URL) then `<p class="mono short">{p.short_url}</p>`.
2. **Instagram caption contradicts itself.** `Draft` emits
   `Link in bio — {link}` and Prepare substitutes the URL, so the caption
   says the link is in the bio and then contains the (unclickable) URL. Also,
   a bio holds one link, so three per-wave tracked links cannot all be "in
   bio".
3. **Suggested times are computed in the DB's zone, not Pacific.**
   `SuggestedAt` is `startsAt.AddDate(0, 0, -N)` on whatever `time.Location`
   the row came back in (UTC on the server). Across a DST change (next:
   2026-11-01) a "7 days before" lands one hour off the workshop's Pacific
   clock time. Harmless today because the time itself is the wrong choice
   (see change #5), but once a posting hour is chosen it must be built with
   `time.Date(... pacific)`, not by subtraction.
4. **Scheduled status line has no date.** `weekdayTime` renders "Posts
   automatically Monday 6:30 PM"; a Reminder scheduled two weeks out is
   ambiguous.
5. **`Fingerprint` excludes `Summary`**, by design today; it must include it
   when templates start reading it, or a corrected summary never marks drafts
   stale.
6. **Bluesky card footer shows the short-link host** because
   `ExternalCard.URI = *job.ShortURL`. See change #2; decide, do not inherit.
7. **Weekday/date formatting is correct.** `Mon, Oct 19` for a workshop
   stored as 2026-10-20T01:30Z is right in `America/Los_Angeles`; `#0568`'s
   "2026-10-20" is the UTC date. `time/tzdata` is imported so the zone
   resolves on the box. No bug here, checked because it looked like one.
8. **Length counting is right** for both channels (graphemes; Mastodon's
   URL=23 with trailing-punctuation trimming), and `ValidateForSend` runs
   before every send. Nothing in this review needs the counters changed,
   only the text they count.

## 6. Proposed issues

For the orchestrator to file (AGENTS.md §9). Suggested order is the table's.

### A. Outreach drafts read as generated: rewrite templates around the workshop summary, a hook, and per-channel structure

- `outreach.Facts` carries `Summary` (trimmed `workshops.summary`) and, when
  present, the outreach flyer `tagline`; `outreachFacts` fills them and
  `Fingerprint` includes `Summary`.
- `Draft` produces a prose lead that contains the weekday, date, time and
  venue in a sentence (no `·` separators, no `New workshop:` label), followed
  by the summary (truncated at a sentence boundary to fit the channel), the
  channel's CTA, then the hashtag line. Three waves have distinct leads that
  read as written by a person.
- Bluesky and Mastodon drafts are never byte-identical for the same facts;
  a Go test asserts it.
- A Go test pins the Programming-LEDs-shaped fixture's output for each
  channel × wave (golden strings), within each channel's limit after the
  short link is substituted.
- The `Regenerate` path produces the new text; existing edited drafts are
  untouched.

### B. Bluesky posts carry the link twice: drop the URL from Bluesky text and make the card URI an explicit choice

- The Bluesky template contains no `LinkToken`; `ValidateForSend` and the
  Prepare/Draft-all paths accept a Bluesky body without a link because the
  card carries it (`job.ShortURL` is still required at send time).
- A new setting `bluesky_card_url` ∈ {`short`, `canonical`} selects whether
  the external card's `uri` is the tracked short link or the workshop's
  canonical URL with UTM; default documented with the trade-off (tracking vs
  `opencircuitsf.com` in the card footer).
- Facet display text is never different from its `uri`.
- Publisher tests cover both settings; no network.

### C. Mastodon posts have no image: upload the cover with alt text

- `mastodon.Client.UploadMedia(ctx, data, mime, altText)` posts to
  `/api/v2/media`, handles 202/206 processing by polling `GET /api/v1/media/:id`
  within the send budget, and returns the media id; token never logged.
- `workshops` gains `cover_alt` (new migration, `ALTER TABLE`); the workshop
  editor has an "Image description (alt text)" field under the cover.
- `postMastodon` attaches the media when the cover bytes load (same rules as
  `thumbnail`) **and** `cover_alt` is non-empty; otherwise it posts text only
  and records a visible notice on the card ("Posted without image: no alt
  text").
- The Bluesky card continues to use the thumbnail; a later issue may switch
  Bluesky to an `app.bsky.embed.images` post when the operator prefers an
  image over a card.
- `httptest` tests for upload, processing poll, and the no-alt-text refusal.

### D. Hashtags: a configurable CamelCase set appended to drafts

- Settings gains `outreach_hashtags` (default `#SanFrancisco #Electronics`),
  validated as `#` + letters/digits, at most six, CamelCase recommended in
  the help text.
- Optional per-workshop hashtags on the outreach row, editable on the
  outreach page.
- Templates append the merged set on its own final line; Bluesky facets and
  Mastodon counting already handle them; the character count includes them.
- Tests: facets land on the appended tags; the limit check includes them.

### E. Reminder and Last call are opt-in; suggested times use a posting hour in Pacific

- Draft all drafts **Announce** for every channel by default; Reminder and
  Last call are checkboxes (unchecked) in the Draft all control and in the
  Schedule all preview.
- `SuggestedAt` returns a time at a configurable posting hour (default
  10:00 `America/Los_Angeles`) on the day N days before the workshop,
  constructed with `time.Date` in the Pacific location, with a test across
  the 2026-11-01 DST boundary.
- The scheduled status line shows weekday **and date** ("Mon Oct 12, 10:00 AM").
- `summaryLine` no longer reports "15 posts drafted" as the headline; it
  reports posts per wave that exist.

### F. Compose once, adapt per channel, preview what each platform renders

- The outreach page gets a **Message** editor above the cards, seeded from
  the Announce template; saving it re-renders every non-overridden card via
  the server (`PATCH .../outreach/{id}/message`), per-channel adaptation
  done in Go (`outreach.Adapt(ch, wave, message, facts)`).
- Each card renders a platform-faithful preview: Bluesky card mock (image,
  title, summary, host), Mastodon image with an "ALT" badge and the link
  preview, Instagram caption with the flyer thumbnail; hashtags styled;
  counts shown.
- Cards for automatic channels show a warning when their text is identical
  to another automatic channel's text; Post now and Schedule all are not
  blocked but the confirm dialog repeats the warning.
- "Edit this one" overrides a single card; an overridden card is marked and
  no longer follows the Message.
- jsdom tests for seeding, re-render, override, and the identical-text
  warning.

### G. Outreach cards: show the link once, fix the Instagram caption, hand over the flyer

- `OutreachPostCards.svelte` drops the `short_url` line under the body; for
  manual channels only, a labelled "Link to paste" row remains with Copy.
- The Instagram template ends with "Sign up at the link in our bio." and
  contains no URL; the card's Copy for Instagram also offers "Open flyer" to
  the workshop's flyer PNG when one exists.
- TikTok and YouTube cards are collapsed by default under "Video channels"
  and their template is a description block (title, one sentence, sign-up
  link, site link), not a post.
- jsdom tests for the single link, the Instagram text, and the collapse.

### H. Docs: a social account hygiene checklist for Outreach

- `docs/outreach-accounts.md`: Mastodon bot flag off, profile bio names the
  people behind it and links the site, `rel="me"` link on the site footer
  to the Mastodon profile, Bluesky domain handle, image alt text policy,
  cadence guidance (Announce always; Reminder/Last call only with news),
  reply-to-replies expectation.
- `docs/README.md` indexes it; `Footer.svelte` gains the `rel="me"` link if
  the profile URL is configured (Settings).
