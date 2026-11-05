# Outreach scheduling review

A read-only review by a Fable subagent, 2026-11-05, of the live v0.3.0 Outreach screen (`~/Gibson/OCSF/OCSF Outreach Scheduling.png`, a 2906×6782 full-page capture of the Programming LEDs event with the Reminder section open and its Mastodon post in Customize…) and of the code behind it. The user's words and the resulting plan are in `#0640`. The previous review is `outreach-posts-screen-review.md`; this one judges what `#0622`–`#0628` and `#0635` built.

## What causes the confusion

Nothing posts or schedules wrongly. Every problem is that the one fact "the Reminder goes out Thu, Nov 12 at 6:00 PM PT" is written eight times and can be changed in three places, and that the two sections not being worked on are still on the page.

### One time, eight places

Reading down the capture, the Reminder's single scheduled time appears at:

1. the status board, Bluesky cell: `Scheduled · Thu, Nov 12 at 6:00 PM PT`;
2. the status board, Mastodon cell: the same words again;
3. the section strip: `2 REMINDER · Posts automatically Thu, Nov 12 at 6:00 PM PT · 3 left to post by hand`;
4. under the Bluesky preview: `Posts automatically Thu, Nov 12 at 6:00 PM PT`;
5. inside the open Mastodon Customize… editor: `Scheduled for Nov 12, 2026 at 6:00 PM PT.`, in a third date format;
6. the Instagram Reels, TikTok and YouTube Shorts rows: `Aim to post on Thu, Nov 12, with the automatic posts.` three times.

Beside them sit two more dates that are not the Reminder's: the Final strip's `suggested Sun, Nov 15 at 10:00 AM PT` and the Announce row's `Tue, Nov 10 at 11:50 AM PT`, plus the event's own `Nov 16, 2026 at 6:30 PM PT` in the header. Ten dates on one screen for an event that has one date and three posts.

### Three ways to set it

The time can be set from three different controls, and the capture shows all three at once:

- **Per post**, inside Customize…: `Change time…` and `Unschedule` on the Mastodon editor's button row. This is the only place a Bluesky and a Mastodon post can be given different times, and nothing on the page says whether that is wanted.
- **Per section**: `Post to Bluesky and Mastodon` · `Schedule for later…` · `Unschedule`, directly under the two previews. `Schedule for later…` is shown while the section is already scheduled, because `cardButtons().schedule` is true for a scheduled post (a reschedule is the same server call), so the button contradicts the line above it, `Scheduled on Bluesky and Mastodon.`
- **Per page**: `Schedule all` at the bottom, whose dialog lists every post with its own `Change time` link and a `Pick a time` rescue for a passed slot. It is the one control that invites a different time per post as a matter of course, and it schedules sections that are not the one on screen.

These are three code paths too: `OutreachComposer.schedule()`, `OutreachAnnouncement.schedule()` looping `scheduleOutreachPost` once per post, and `OutreachBulkActions` → `POST …/schedule-all`. None of them is atomic across Bluesky and Mastodon; the section loop stops at the first failure and leaves one post scheduled and the other not, which is exactly the state the user never wants.

### The other two sections never leave

"One open section" (`#0625`) was built as an accordion: three `OutreachWaveRow`s, each with a pinned strip, of which one panel is shown. The two closed strips stay on the page, each with its own ordinal, name, status line and (for Final) a `Write it` button. So every section is named at least twice (board row and strip) and the open one five times (board row, strip, `Bluesky · Reminder`, `Mastodon · Reminder`, `Instagram Reels · Reminder`, …).

The capture also shows the specific way this misleads. The Reminder strip is `position: sticky`, and a full-page capture draws a sticky element where it was pinned at the moment of capture, so the `2 REMINDER` strip sits in the middle of the Reminder's own content, under the Bluesky preview. Meanwhile the closed `1 ANNOUNCE` strip sits directly above the Reminder's Message box and `Go to Video` link, so the first screen of the open section reads as Announce's. It is an artifact of the capture, but the same adjacency is on screen while scrolling: whatever is pinned at the top belongs to the open section, and whatever closed strip is just above the content does not.

### The status words are not the user's words

The user asked for three states: drafted, scheduled, posted. The board writes `Not written`, `Draft / Not scheduled`, `Post by hand`, `Scheduled`, `Posted`, `Posting now`, `Failed`, `On hold`, `Off`; the strip writes `Not written · not scheduled · suggested …`, `Drafted · not scheduled`, `Posts automatically …`, `Posted … · 3 left to post by hand`; the preview line writes `Posts automatically …`; the composer writes `Scheduled for …`. A hand-posted row's `Post by hand` is a status that never changes until it is marked posted, so it tells the user nothing about whether the text is ready.

### What does not need fixing

- The status board's shape (one row per section, one column per channel) is right and is what the user calls the summary. It needs the vocabulary and the row-level time, not a new component.
- The Message field, `Save message`, Customize…, the previews, `Post to Bluesky and Mastodon` with its confirmation, Delete and redo, and the Video group's copy buttons all work and are not part of the complaint.
- The server's single-post `schedule` and `unschedule` routes are correct; they are the wrong unit for the screen, not wrong in themselves.

## Proposed layout

Three parts, top to bottom, and nothing else about scheduling anywhere on the page:

1. **The summary.** The status board, reworded. Each row's heading carries the section's one line (`Scheduled Thu, Nov 12 at 6:00 PM PT`), so the time is written once per row; the cells say only `Drafted`, `Scheduled`, `Posted`, `Not written` (and the exceptional `Posting now`, `Failed`, `On hold`, `Off`). A hand-posted cell reads `Drafted · by hand` until it is marked, then `Posted <when>`. A line under the board says that Instagram Reels, TikTok and YouTube Shorts are posted by hand and the site never posts them.
2. **The section picker, pinned.** A tab strip `1 Announce · 2 Reminder · 3 Final`; under it, the selected section's one line. This is the only sticky element. Board rows still select, as they do today.
3. **The one selected section.** Nothing of the other two is rendered. Inside it: the `Go to Video` link, the Message box, one block headed **When it goes out** that holds the one date and the only scheduling buttons on the page, the two previews with no time under them, then Video with its day said once in the group's intro line.

The Reminder's time is then on the page three times (board row, pinned line, When it goes out), each a different kind of thing: overview, context, control.

### Desktop (1280 px)

```
Programming LEDs                                        Edit event   All outreach
Nov 16, 2026 at 6:30 PM PT · published

Posts and when they go out
                                Bluesky     Mastodon    Instagram Reels  TikTok    YouTube Shorts
▸ 1 ANNOUNCE                    Posted      Posted      Drafted · by hand   Drafted · by hand   Drafted · by hand
  Posted Tue, Nov 10 at 11:50 AM PT
  Light it up with LEDs. You can do it with an ESP32…
▾ 2 REMINDER                    Scheduled   Scheduled   Drafted · by hand   Drafted · by hand   Drafted · by hand
  Scheduled Thu, Nov 12 at 6:00 PM PT
  Colorful lights are better. Join us to make your own…
▸ 3 FINAL                       Not written Not written Not written  Not written  Not written
  Not written · suggested Sun, Nov 15 at 10:00 AM PT
Instagram Reels, TikTok and YouTube Shorts are posted by hand; the site never posts them.

┌──────────────────────────────────────────────────────────────────────────────┐  (pinned)
│ [ 1 Announce ] [ 2 REMINDER ] [ 3 Final ]                                      │
│ Scheduled Thu, Nov 12 at 6:00 PM PT · 3 left to post by hand                   │
└──────────────────────────────────────────────────────────────────────────────┘
Go to Video: 3 left to post

Message
┌──────────────────────────────────────────────────────────────────────────────┐
│ Colorful lights are better. Join us to make your own. A few seats may still… │
└──────────────────────────────────────────────────────────────────────────────┘
[ Save message ]

When it goes out
Scheduled for Thu, Nov 12 at 6:00 PM PT on Bluesky and Mastodon.
[ Change time… ] [ Unschedule ] [ Post to Bluesky and Mastodon ]

How it will look
┌ Bluesky · Reminder  @www.opencircuitsf.com ──────────────────────────────────┐
│ (preview card)                                                                │
│ [ Customize… ]                                                                │
└──────────────────────────────────────────────────────────────────────────────┘
┌ Mastodon · Reminder  @opencircuitsf@sfba.social ─────────────────────────────┐
│ (preview card)                                                                │
│ [ Customize… ]                                                                │
└──────────────────────────────────────────────────────────────────────────────┘

Video
One video, posted in each place by hand. Aim for Thu, Nov 12, with the automatic
posts. Each has its own link.
┌ Instagram Reels · Reminder ──────────────────────────────────────────────────┐
│ (text)   Put this in your bio https://go…  [Copy link] [Copy with link] …   │
└──────────────────────────────────────────────────────────────────────────────┘
┌ TikTok · Reminder ───────────────────────────────────────────────────────────┐
┌ YouTube Shorts · Reminder ───────────────────────────────────────────────────┐

Flyers · print and post at saved locations                            Open flyers
Links and clicks · 0 clicks since outreach started                    View results
[ Reset outreach ]
```

When the section is not yet scheduled the block reads:

```
When it goes out
Not scheduled. Bluesky and Mastodon go out together.
[ Schedule… ] [ Post to Bluesky and Mastodon ]
```

and `Schedule…` opens, in place:

```
Post on (Pacific time)  [ 2026-11-12 18:00 ]   [ Schedule ] [ Cancel ]
The suggested time is Thu, Nov 12 at 6:00 PM PT. Pick a time.     (only while the box is empty)
```

An unwritten section's panel is its existing empty state (`No posts yet. Start with the final post.` and `[ Write the final post ]`); there is no `Write it` beside a strip, because there is no strip.

### Phone (390 px)

```
Programming LEDs
Nov 16, 2026 at 6:30 PM PT · published
Edit event · All outreach

▸ 1 ANNOUNCE
  Posted Tue, Nov 10 at 11:50 AM PT
  Bluesky Posted · Mastodon Posted ·
  Instagram Reels Drafted · TikTok Drafted ·
  YouTube Shorts Drafted
▾ 2 REMINDER
  Scheduled Thu, Nov 12 at 6:00 PM PT
  Bluesky Scheduled · Mastodon Scheduled ·
  Instagram Reels Drafted · TikTok Drafted ·
  YouTube Shorts Drafted
▸ 3 FINAL
  Not written · suggested Sun, Nov 15
  at 10:00 AM PT
Instagram Reels, TikTok and YouTube Shorts
are posted by hand; the site never posts them.

┌──────────────────────────────────────┐ (pinned)
│ [1 Announce] [2 REMINDER] [3 Final]  │
│ Scheduled Thu, Nov 12 at 6:00 PM PT  │
└──────────────────────────────────────┘
Go to Video: 3 left to post
Message
┌──────────────────────────────────────┐
│ Colorful lights are better. …        │
└──────────────────────────────────────┘
[ Save message ]
When it goes out
Scheduled for Thu, Nov 12 at 6:00 PM PT
on Bluesky and Mastodon.
[ Change time… ] [ Unschedule ]
[ Post to Bluesky and Mastodon ]
How it will look
┌ Bluesky · Reminder ──────────────────┐
┌ Mastodon · Reminder ─────────────────┐
Video
One video, posted in each place by
hand. Aim for Thu, Nov 12, with the
automatic posts. Each has its own link.
┌ Instagram Reels · Reminder ──────────┐
┌ TikTok · Reminder ───────────────────┐
┌ YouTube Shorts · Reminder ───────────┐
```

The board's cells run inline on a phone (one or two wrapped lines per row) instead of one grid line per channel, so the board is about nine lines, not fifteen, and the pinned tabs plus `Go to Video` are on the first screen.

## Scheduling both channels at once

One server call per section: `POST /admin/workshops/{id}/outreach/schedule` with `{"wave": "reminder", "scheduled_at": "…"}` schedules every automatic, connected post in that section that can still be scheduled (draft, scheduled or failed) at that one instant, in one transaction, all or nothing; a post that is posted or posting now is left alone and named in the response. `POST …/outreach/unschedule` with `{"wave": "reminder"}` is its inverse. The per-post routes stay for tests and as a fallback, but no button on the page calls them.

Per-channel times are **not** kept in the interface. No real need for them was found: the user says the two go out together, the templates differ only by wording, and the one case where times differ in production today (none known; `hack-night`'s Reminders share Nov 8 10:00 PT) would be shown honestly as `Scheduled at different times` with `Change time…` setting one. `Schedule all` goes with it: with a section scheduled in one click from its own block, a page-level dialog that lists every post with its own time is the thing the user is describing.

## What the plan absorbs from the open follow-ups

Absorbed, because the same components are being rewritten: `#0631` items 1–3; `#0636` items 2, 4 and 5; `#0637` items 2, 4, 5, 7 and 8; `#0634` item 2. Left separate: `#0636` items 1 and 3 (switched-off channels and the user's decision about posting to one), `#0637` items 1, 3 and 6 (Reminder wording in the Go templates, a video excerpt on the board, caption length — each needs the user), `#0634` item 1 and `#0632`, `#0633` (unsaved-edit handling), which should be re-planned after this lands. The reasoning is in `#0640`'s plan.
