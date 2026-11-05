# Outreach posts screen review

A read-only review by a Fable subagent, 2026-11-04, of a 1453×6414 CSS px capture of the Hack Night Outreach screen and its code. The user's response and the resulting work are in `#0622`–`#0628`.

## Findings

- **Instagram** is excluded from each wave section by `.filter(sendsItself)` in `OutreachAnnouncement.svelte`, and its cards sit in the collapsed "Post by hand" row at the bottom of the page. This is a layout problem.
- **TikTok and YouTube** are absent because `outreach_channels` defaults to `bluesky,mastodon,instagram`. Production has no saved value (checked 2026-11-04). When enabled, they land in a second collapsed section.
- **Scrolling:** the page is about 8 laptop screens. Four full-width copies of the cover image take about 1,980 px (31%). Two open Customize editors take about 560 px each.
- **Context:** several whole screens are only cover image. Preview titles have no wave, nothing is pinned, the event title appears once, and the same status is written three different ways.
- **Waves** differ by one bold word.
- No posting or scheduling defect was found. Every complaint is about layout and labelling.

## Recommended layout

A compact status board, with one wave open under it. A full wave-by-channel editing matrix cannot hold a message field and previews in each cell.

```
Hack Night · Mon Nov 9, 7:00 PM PT · published
              Bluesky         Mastodon        Instagram
1 Announce    [ OK ] Nov 4    [ OK ] Nov 4    [ -- ] copy
2 Reminder    [ ⏱ ] Sun 10a   [ ⏱ ] Sun 10a   [ -- ] copy
3 Final       [ ⏱ ] Mon 10a   [ ⏱ ] Mon 10a   [ -- ] copy

> 2 · REMINDER · posts Sun Nov 8 10:00 AM PT                    (pinned)
Message box, then one card per channel, titled "Bluesky · Reminder"
[ Post to Bluesky and Mastodon ]   Schedule for later…          (pinned)
```

The third wave is relabelled here from the review's "Last call" to the user's "Final". The review's fixed timing phrases are omitted because they did not match the configured schedule.
