# Outreach and Settings UX walkthrough, 2026-10-17

Evidence for a UX review of the admin Outreach and Settings screens. Every screenshot is real Safari 26.6.2 inside a disposable Tart VM, walking the workflow in order as a first-time admin. No application code was changed. Each `NN-name.png` has a matching `NN-name.md` (screen, steps, goal, candid observations).

## Index

| # | Screenshot | Screen / state |
|---|---|---|
| 01 | [01-admin-overview.png](01-admin-overview.png) | Admin overview (1440 wide, full page) |
| 02 | [02-settings-full.png](02-settings-full.png) | Settings, whole page (4077 px tall) |
| 03 | [03-settings-test-success.png](03-settings-test-success.png) | Settings: Test connection succeeded on all three (full page) |
| 04 | [04-settings-test-failure-unsaved.png](04-settings-test-failure-unsaved.png) | Settings: typed a bad token but did not save, Test connection |
| 05 | [05-settings-mastodon-saved-bad.png](05-settings-mastodon-saved-bad.png) | Settings: bad Mastodon token saved |
| 06 | [06-settings-test-failure.png](06-settings-test-failure.png) | Settings: Test connection failure |
| 07 | [07-settings-mastodon-restored.png](07-settings-mastodon-restored.png) | Settings: working token restored |
| 08 | [08-workshops-list.png](08-workshops-list.png) | Workshops list |
| 09 | [09-workshop-editor.png](09-workshop-editor.png) | Workshop editor with Outreach summary |
| 10 | [10-outreach-list.png](10-outreach-list.png) | Outreach list (Workshops sub-tab) before any outreach started |
| 11 | [11-workshop-outreach-before-start.png](11-workshop-outreach-before-start.png) | Workshop outreach, before Start |
| 12 | [12-workshop-outreach-started.png](12-workshop-outreach-started.png) | Workshop outreach after Start |
| 13 | [13-tracked-links-expanded.png](13-tracked-links-expanded.png) | Tracked links expanded |
| 14 | [14-draft-all-announce-only.png](14-draft-all-announce-only.png) | After Draft all with Reminder and Last call unticked |
| 15 | [15-draft-all-boxes-ticked.png](15-draft-all-boxes-ticked.png) | Reminder and Last call ticked |
| 16 | [16-draft-all-with-reminder-lastcall.png](16-draft-all-with-reminder-lastcall.png) | After Draft all with both boxes ticked (full page, 3,800 px) |
| 17 | [17-workshop-editor-outreach-summary-after-draft.png](17-workshop-editor-outreach-summary-after-draft.png) | Workshop editor outreach summary after drafting |
| 18 | [18-card-editor-open.png](18-card-editor-open.png) | Post card editor open (Bluesky Announce) |
| 19 | [19-card-editor-saved.png](19-card-editor-saved.png) | Card after saving the edit |
| 20 | [20-bluesky-post-now-confirm.png](20-bluesky-post-now-confirm.png) | Post now confirm dialog (native browser confirm) |
| 21 | [21-bluesky-posting-now.png](21-bluesky-posting-now.png) | Bluesky card while posting |
| 22 | [22-bluesky-posted.png](22-bluesky-posted.png) | Bluesky card posted |
| 23 | [23-mastodon-posting-now.png](23-mastodon-posting-now.png) | Mastodon card while posting |
| 24 | [24-mastodon-posted.png](24-mastodon-posted.png) | Mastodon card posted |
| 25 | [25-schedule-all-preview.png](25-schedule-all-preview.png) | Schedule all modal (default state) |
| 26 | [26-schedule-all-preview-ticked.png](26-schedule-all-preview-ticked.png) | Schedule all with boxes ticked |
| 27 | [27-schedule-all-result.png](27-schedule-all-result.png) | After scheduling (full page) |
| 28 | [28-manual-copy-text.png](28-manual-copy-text.png) | Manual channel (Instagram) Copy text |
| 29 | [29-manual-mark-posted.png](29-manual-mark-posted.png) | Manual channel after Mark posted |
| 30 | [30-locations-list.png](30-locations-list.png) | Locations list |
| 31 | [31-locations-add-form.png](31-locations-add-form.png) | Add location form |
| 32 | [32-locations-edit-form.png](32-locations-edit-form.png) | Edit location form |
| 33 | [33-flyers-list.png](33-flyers-list.png) | Flyers list (no flyers yet) |
| 34 | [34-flyer-editor.png](34-flyer-editor.png) | Flyer editor (new flyer) |
| 35 | [35-flyer-editor-links-generated.png](35-flyer-editor-links-generated.png) | Flyer editor after generating links |
| 36 | [36-flyer-print-full.png](36-flyer-print-full.png) | Print view, full page (4 sheets) |
| 37 | [37-flyer-print-half.png](37-flyer-print-half.png) | Print view, half-size double (2 sheets) |
| 38 | [38-results.png](38-results.png) | Results overview |
| 39 | [39-results-workshop-detail.png](39-results-workshop-detail.png) | Results for LED Programming (full page) |
| 40 | [40-error-unknown-workshop.png](40-error-unknown-workshop.png) | Error: unknown workshop |
| 41 | [41-error-unknown-flyer.png](41-error-unknown-flyer.png) | Error: unknown flyer |
| 42 | [42-mobile-workshop-outreach-390.png](42-mobile-workshop-outreach-390.png) | 390 px: workshop outreach (full page, 4,920 px) |
| 43 | [43-mobile-settings-390.png](43-mobile-settings-390.png) | 390 px: settings |
| 44 | [44-mobile-flyer-editor-390.png](44-mobile-flyer-editor-390.png) | 390 px: flyer editor |
| 45 | [45-mobile-results-390.png](45-mobile-results-390.png) | 390 px: results detail |
| 46 | [46-mobile-locations-390.png](46-mobile-locations-390.png) | 390 px: locations |
| 47 | [47-mobile-workshop-editor-390.png](47-mobile-workshop-editor-390.png) | 390 px: workshop editor |
| 48 | [48-video-channels-expanded.png](48-video-channels-expanded.png) | Video channels expanded (TikTok, YouTube) |
## Environment

- App and data: `./scripts/dev-outreach-demo.sh` (app on 127.0.0.1:18080 signed in via `DEV_ADMIN_LOGIN`, mocks for Bluesky / Mastodon / ShortLinks on 127.0.0.1:18081, database `opencircuit_demo`, tracker interval 20 s). Waited for `DEMO READY`.
- Browser: Safari 26.6.2 (macOS 26.6.2 guest) in a clone of `uitest-golden`, driven by `safaridriver` on the guest's port 4444 via small WebDriver scripts run with `tart exec`. Guest clone deleted and lease released afterwards.
- Window sizes: 1440 wide, window height 900 (about 848 px viewport after Safari chrome). Screenshots marked "full page" were taken by growing the window to the document height; modals and the native dialog were shot at normal viewport size. Mobile pass: 390 wide (items 42-47).
- The guest cannot reach the host over a network. The app is reached through a stdio HTTP bridge: a guest-side `guest_proxy.py` listens on the guest's 127.0.0.1:18080 and frames each request as a JSON line on stdout of `tart exec -i`; a host-side `host_bridge.py` replays it against 127.0.0.1:18080 and writes the response back on stdin. Nothing listens on a non-loopback interface on either side. Scripts live in `.scratch/uxwalk/` (not committed).

## Known demo limits (not product bugs)

- The Go app does not serve `/media/` locally, so cover images and the flyer photo 404 (empty photo box, 'Could not load: photo' on print views, no thumbnails).
- Mock Bluesky and Mastodon post links ('view post', 'Open') are dead.
- The mock ShortLinks service pre-fills click history (340 clicks over Oct 12-17) before anything is posted, and mock engagement and mentions appear after posting.
- Timezone: the guest VM is UTC, so admin screens render workshop times as 1:30 AM Nov 1 while post copy and flyers say Saturday Oct 31, 6:30 PM (Pacific). On a real admin laptop the mismatch exists whenever its zone is not Pacific.
- The native `confirm()` for Post now blocks Safari's WebDriver, so only the first one was photographed (screencapture inside the guest, 1024x768 guest screen); later runs overrode `window.confirm` in the page. The Mastodon confirm and a Mastodon-failure Post now were not captured.
- Clipboard result for Copy text/Copy link could not be verified under automation.

## Not reached

Anything needing real services or a failing post (Post now failure, ShortLinks test failure, scheduled posts firing at their time), unpublished workshop outreach, and the real print dialog.
