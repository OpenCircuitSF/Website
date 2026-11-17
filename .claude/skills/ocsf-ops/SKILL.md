---
name: ocsf-ops
description: Run Open Circuit SF site operations through the `ocsf` MCP server, with the house rules. Use it to schedule guide posts on Bluesky and Mastodon, set up a repeat workshop or other event, check what is missing from next month's events and draft it, or anything else that calls the ocsf tools (create_guide_posts, duplicate_event, event_coverage, publish_event, …). It covers confirming before public actions, Pacific time, campaign names, idempotency keys and safe retries.
---

# Open Circuit SF operations

The tools come from the `ocsf` MCP server (`https://www.opencircuitsf.com/mcp`).
Setup, scopes, every tool, and full step-by-step versions of the workflows
below are in `docs/mcp.md`. The REST reference is `docs/api.md`. If the tools
aren't connected, point the user to `docs/mcp.md` §2 rather than guessing at
`curl`.

Start a session with `whoami`, to confirm the connection and see the key's
scopes. The tools you see are exactly the ones those scopes allow.

## House rules

1. **Confirm before anything public or destructive.** Show what will happen
   and get a clear yes first. The full list:
   - PUBLIC: `send_guide_post`, `schedule_guide_post`, `create_guide_posts`
     or `update_guide_post` with a schedule time, `publish_event`,
     `cancel_event`, `update_event` on a published event,
     `schedule_outreach_wave`, `send_outreach_post`.
   - DESTRUCTIVE: `delete_guide_post`, `delete_outreach_post`,
     `reset_event_outreach`, `delete_outreach_location`, `delete_flyer`,
     and `takedown_outreach_post` (also PUBLIC).

   One confirmation can cover a batch the user has seen in full, such as a
   table of 14 scheduled posts or one outreach wave. It never carries over to
   different work. Under `bypass-permissions` nothing else stops these calls.
2. **Pacific time.** The site's zone is America/Los_Angeles.
   - Write times with their real offset: `-08:00` from early November to
     mid-March, `-07:00` otherwise. Check the date against the DST change.
   - Read "next month", "Saturday" and "9 AM" in Pacific.
   - Guide-post times come back in UTC. Convert them before showing them.
   - Default posting time is **9:00 AM Pacific** unless the user says
     otherwise.
3. **Campaign names are `guides-YYYY-MM`**, for the month the posts belong to
   (for example `guides-2026-12`). Use the user's name if they give one.
   Never leave the campaign blank, which would put the posts in the catch-all
   `guide-posts`.
4. **Give every create an `idempotency_key`.** That means `create_guide_posts`,
   `create_event`, `duplicate_event`, `create_outreach_post`,
   `create_outreach_location` and `create_flyer`. Build the key from the request, so a
   retry rebuilds the same one:
   - `guides-<campaign-month>-<slug>-<date>`;
   - `dup-<source id>-<date>`;
   - `coverage-<month>-rule-<id>-<date>`.
5. **Check `list_*` before retrying.** A write keeps running after a timeout or
   cancel (bounded at two minutes), so it may already have happened. Either
   retry with the same `idempotency_key`, or check first with
   `list_guide_posts` or `list_events`. Never retry a create with a new key
   until a list shows it isn't there.
6. **Read per-channel results.** `create_guide_posts` can succeed on one
   channel and not the other. Report each `link_error`, `schedule_error` and
   `error`. Don't redo the whole call.
7. **Drafts first.** Events are created and duplicated as drafts. Publishing is
   a separate, confirmed step.
8. **Never echo the API key.** If the user pastes one, don't repeat it back or
   write it to a file in the repo.

## Workflows

### Schedule guide posts

For example: "one a day for two weeks across the ESP32-C3 guides, both
channels, campaign guides-2026-12, skip guides posted in the last 30 days."

1. `list_channels`: both channels `configured`?
2. `list_guides {set, kind: "topic", limit: 200}`. The sets are `leds`,
   `esp32` (ESP32-C3), `multimeter` and `soldering`.
3. `guide_posts_overview`: drop guides whose `last_posted_at` is inside the
   window, or that have a `next_scheduled_at`.
4. `list_guide_posts {campaign}`: skip what an earlier run already made.
5. Show a table of date (Pacific), guide and body. If there are fewer guides
   than days, say so rather than repeating. **Confirm.**
6. `create_guide_posts {slug, channels: ["bluesky","mastodon"], campaign, scheduled_for, idempotency_key}`,
   once per day. Leaving out `body` uses the guide's default copy.
7. `list_guide_posts {campaign, status: "scheduled"}` to verify, then summarise
   in Pacific.

### Repeat a workshop

For example: "next month's Programming LEDs workshop, same as last time, on
the second Saturday."

1. `list_event_types`, then `list_events {type, status: "published", from, to: now}`.
   Take the latest event whose title matches, then `get_event`.
2. Work out the date in Pacific: the second Saturday is the 8th to the 14th.
   Keep the source's start time.
3. `list_events` over the target month, to check it isn't already set up.
4. `duplicate_event {id, starts_at, idempotency_key: "dup-<id>-<date>"}`. The
   result is a draft, with the end keeping the source's length.
5. Flag what probably needs editing: a Luma `signup_url` for the old date, and
   any dated text in `body_md`. Offer `update_event`. **Confirm** before
   `publish_event`.

### What's missing next month

1. `event_coverage {}` defaults to next month in Pacific, or takes
   `{month: "YYYY-MM"}`.
2. Report each rule's `state`, its `missing_dates` or `missing` count, the
   drafts awaiting publish, `unmatched_events`, and the Luma lines when
   `luma.status` is `ok`.
3. On a yes, for each gap, find the latest matching event and use
   `duplicate_event` with the missing date. If no event matches, use
   `create_event` with the rule's type, title and venue. A monthly rule gives
   only a count, so propose dates and confirm them first.
4. Run `event_coverage` again. The rules should read `drafts`. List the drafts
   with their admin links. **Confirm** before publishing any.

Cadence rules themselves are managed with `list_cadence_rules` and
`set_cadence_rule`. `active: false` retires a rule, and there is no delete
tool.

### Announce an event (outreach)

1. The event must be published. `outreach_connections_status`, then
   `start_event_outreach {id}` (safe to repeat).
2. `draft_event_outreach {id}` drafts the `announce` wave (`waves` adds
   `reminder`, `last-call`). `get_event_outreach` and show every draft; edit
   with `update_outreach_post`.
3. **Waves are scheduled as a whole.** **Confirm**, then
   `schedule_outreach_wave {id, wave, scheduled_at}`: all automatic posts of
   the wave, one time, all or nothing. Report `left_alone`. Undo with
   `unschedule_outreach_wave`.
4. Hand-posted channels: `ensure_outreach_hand_links`, the user posts, then
   `mark_outreach_post_posted` with the `remote_url`.

### Add a cover image

1. `create_media_upload` → run its `curl` with the file → `{path}`. Never echo
   the `upload_token`.
2. **Confirm** if the event is published, then
   `update_event {id, cover_image: path, cover_alt}`. Always write alt text:
   Mastodon posts attach the cover only with it.

### How did the posts do?

`get_post_performance {}` (last 7 days; `since`/`until`, `kind`). Lead with its
summary, then the best posts by clicks and reactions. `clicks: null` with
`clicks_error` means ShortLinks was down, not zero clicks. One event in depth:
`get_outreach_results`.
