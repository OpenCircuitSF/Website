# Agent API (`/api/v1`)

The site has a small REST API for scripts and agents. With it they can read the
guide catalog, create and schedule guide posts on Bluesky and Mastodon, and
manage events. The same operations are also served as MCP tools at `/admin/mcp`. To
connect Claude Code or Claude Desktop, see [`mcp.md`](mcp.md).

Both adapters are built from one operation registry
(`internal/handlers/v1_registry.go`, on top of `internal/api`). So REST, MCP and
the OpenAPI document always list the same operations with the same fields.
**The OpenAPI document is the reference**, and this page is the guide to it:

```
GET https://www.opencircuitsf.com/api/v1/openapi.json
```

It is public (no key needed) and cached for five minutes. Each operation has
its `operationId` (the MCP tool name), its required scope in
`x-required-scope`, and its request and response schemas.

## Base URL and authentication

```
https://www.opencircuitsf.com/api/v1
Authorization: Bearer ocsf_…
```

- The key goes in the `Authorization` header and nowhere else. A query-string
  key is ignored.
- Every key starts with `ocsf_` and is 48 characters long.
- A missing, malformed, revoked or expired key gets the same `401`, so the
  response does not say which one it was.
- Every response other than the OpenAPI document carries
  `Cache-Control: no-store`.

The examples below assume the key is in an environment variable:

```sh
export OCSF_API_KEY='ocsf_…'   # never commit it, never paste it into an issue
B=https://www.opencircuitsf.com/api/v1
```

### Creating a key

1. Sign in to the admin console and open **Settings → API tokens**
   (`https://www.opencircuitsf.com/admin#/settings?section=api`).
2. Give the key a **name** (up to 100 characters) that says which client holds
   it, for example "Claude Code, Brennan's laptop". Audit-log rows written
   with the key show "via API key «name»".
3. Choose **Expires**: 7 days, 30 days, **90 days** (the default), 1 year,
   Never, or a custom date. A custom date expires at 23:59:59 that day in your
   browser's time zone. From then on the key gets `401`.
4. Choose the **scopes**. At least one is required. **Agent (every scope)**
   ticks every scope, which is what an MCP agent needs; **Read-only** ticks
   every `:read` scope, for an agent that should only look.
5. Select **Create token** and copy the key. **It is shown once.** Only its
   hash is stored, and the list afterwards shows only the 12-character prefix.

| Scope | Allows |
|---|---|
| `engagement:read` | `GET /engagement` (the Home Assistant check, [`home-assistant.md`](home-assistant.md)) |
| `guides:read` | the guide catalog |
| `guide_posts:read` | reading guide posts, the overview and the channel list |
| `guide_posts:write` | creating, scheduling, sending and deleting guide posts |
| `events:read` | events, event types, interests, and the coverage report |
| `events:write` | creating, changing, publishing, cancelling and duplicating events |
| `venues:read` | venues |
| `outreach:read` | event outreach, results, the timeline, Luma, flyer locations and flyers |
| `outreach:write` | starting outreach, drafting, scheduling, sending and taking down posts; flyers and their locations |
| `media:read` | the media directory listing |
| `media:write` | upload tickets for images |

`engagement:read` also covers `get_post_performance`. A `:write` scope also
grants the `:read` scope of the same resource. The Agent preset is every scope
above, `engagement:read` included; the Read-only preset is every `:read`
scope.

To revoke a key, select **Revoke** in its row. Anything using it gets `401` on
its next request.

### `whoami`, `GET /api/v1/me`

`/me` checks the connection. Any valid key may call it, whatever its scopes.

```sh
curl -s -H "Authorization: Bearer $OCSF_API_KEY" $B/me
```

```json
{"name":"Claude Code","prefix":"ocsf_AbCdEfG","scopes":["guides:read","guide_posts:read","guide_posts:write","events:read","events:write","venues:read"],"expires_at":"2027-02-13T23:43:41-08:00","server_version":"0.12.0","api_version":"v1"}
```

`token_probe`, `GET /api/v1/token`, is a deprecated alias that Home Assistant
still uses. It is not an MCP tool.

## Conventions

### Times are Pacific

The site's time zone is **America/Los_Angeles**. Every time is RFC 3339 with an
offset.

- **Input:** any offset is accepted. Write Pacific times with their own offset:
  `-08:00` from early November to mid-March, `-07:00` the rest of the year. For example,
  `2026-12-12T13:00:00-08:00` is 1 PM on 12 December.
- **Output:** event times (`starts_at`, `ends_at`, `published_at`, and so on)
  and guide-post times (`scheduled_at`, `posted_at`, `created_at`,
  `updated_at`, and a guide's `last_posted_at` and `next_scheduled_at`) come
  back with a Pacific offset. It is the same instant the database holds, only
  written in Pacific time.
- **Two exceptions stay UTC.** `get_engagement` keeps the `Z` times that Home
  Assistant's sensor was built on, byte for byte (see
  [`home-assistant.md`](home-assistant.md)), and the coverage report's Luma
  `read_at` and `last_good_at` are UTC. Convert those before showing them to a
  person.
- **A `+` in a query string has to be encoded.** `?from=2026-12-01T00:00:00+00:00`
  arrives as a space and fails validation. Write `%2B00:00`, or use `Z` or a
  negative offset, which work as typed.

### Requests

- Write requests send `Content-Type: application/json`, with at most 64 KB of
  body. Anything else gets `415` or `413`.
- An **unknown field is a validation error**, not ignored. A field of the wrong
  type is named in the error.
- `PATCH` changes only the fields sent. On events, an empty string clears a
  text field.
- A method the path doesn't take gets `405`.

### Errors

Every error has the same envelope:

```json
{"error":{"code":"validation","message":"the request has invalid fields","fields":{"limit":"must be 1 to 200"}}}
```

`code` is stable, so branch on it. `message` is for people, so don't parse it.
`fields` appears only on validation errors and maps each bad field to what is
wrong with it.

| HTTP | `code` | When |
|---|---|---|
| 400 | `validation` | a bad field, query parameter, cursor or `Idempotency-Key` |
| 413, 415 | `validation` | the body is over 64 KB, or not `application/json` |
| 401 | `unauthorized` | no key, or a key that is unknown, revoked or expired |
| 403 | `forbidden` | the key lacks the scope; the message names it (`this API key lacks the engagement:read scope`) |
| 404 | `not_found` | no such object (`event not found`, `guide post not found`) |
| 405 | `method_not_allowed` | wrong method for the path |
| 409 | `conflict` | a state conflict (for example, deleting a guide post that isn't a draft), or an `Idempotency-Key` problem |
| 429 | `rate_limited` | over the per-key budget, this IP made too many failed authentication attempts, or ShortLinks is rate limiting the site; wait `Retry-After` seconds |
| 503 | `unavailable` | a dependency failed, such as ShortLinks not making a short link |
| 500 | `internal` | a server bug; the detail goes to the server log, never the response |

### Pagination

List operations take `limit` (1 to 200, default 50) and `cursor`. They return:

```json
{"items":[ … ],"next_cursor":"eyJpZCI6Mn0"}
```

Pass `next_cursor` back as `cursor` until it is `null`. The cursor is opaque.
Don't build or edit one. `guide_posts_overview` and `list_channels` are not
paginated.

```sh
curl -s -H "Authorization: Bearer $OCSF_API_KEY" "$B/interests?limit=2"
curl -s -H "Authorization: Bearer $OCSF_API_KEY" "$B/interests?limit=2&cursor=eyJpZCI6Mn0"
```

### Idempotency-Key on creates

`POST /guide-posts`, `POST /events` and `POST /events/{id}/duplicate` accept an
optional `Idempotency-Key` header: 1 to 255 printable ASCII characters, unique
per create.

- A retry with the **same key and the same body** within 24 hours replays the
  first success, with the same status and body and an
  `Idempotent-Replayed: true` header. Nothing new is created.
- The same key with a **different body** gets `409 conflict`.
- So does a retry while the first request is **still running**.
- A failed request forgets its key, so it can be retried with the same key.
- Keys are per API key. A key used over REST and then over MCP is a conflict,
  not a replay.

Always send one when a timeout or dropped connection might make you retry:

```sh
curl -s -X POST $B/events \
  -H "Authorization: Bearer $OCSF_API_KEY" -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: event-programming-leds-2026-12-12' \
  -d '{"title":"Programming LEDs","starts_at":"2026-12-12T13:00:00-08:00","ends_at":"2026-12-12T16:00:00-08:00"}'
```

### Rate limits

Each key gets **120 requests a minute**, with a burst of 120. REST and MCP share
the budget. Over it, the response is `429 rate_limited` with a `Retry-After`
header in seconds. Successful responses carry no rate-limit headers.

Failed authentication is limited per client IP as well (`#0683`). Each IP
may present **10 unknown, revoked or expired keys a minute**, with a burst of
10, across REST and MCP together. A missing or malformed `Authorization`
header is refused without a lookup, so it does not count, and neither does a
request that authenticates. Over the limit, every request from that IP gets
`429 rate_limited` with `Retry-After`, even one with a good key, until the
bucket refills at one request every 6 seconds.

### Writes finish

A write that has started runs to completion even if the client disconnects. It
is bounded at two minutes. If a create times out on your side, it may still
have happened: retry with the same `Idempotency-Key`, or list first. A read
stops when the client goes away.

## Resources

Each heading is the MCP tool name, with its REST route. "PUBLIC" means the call
can make something visible to the public, and "DESTRUCTIVE" means it can't be
undone. The OpenAPI descriptions start with the same words.

### Guides (`guides:read`)

The guide catalog is the same file the site renders. Workshop guides come
first, then topic guides. Only **topic** guides can be posted. Sets are
`leds`, `esp32` (the ESP32-C3 guides), `multimeter` and `soldering`.

**`list_guides`**, `GET /guides`, takes `set`, `kind` (`workshop` or `topic`),
`limit` and `cursor`. A topic guide includes `post` (suggested copy) and
`default_body` (the copy plus `{link}`).

```sh
curl -s -H "Authorization: Bearer $OCSF_API_KEY" "$B/guides?set=esp32&kind=topic&limit=200"
```

**`get_guide`**, `GET /guides/{slug}`, returns one guide plus its post
history: when it was last posted, posts per channel, clicks and signups.

```sh
curl -s -H "Authorization: Bearer $OCSF_API_KEY" $B/guides/esp32-c3-wont-upload
```

### Guide posts (`guide_posts:read`, `guide_posts:write`)

A guide post is one post on one channel (`bluesky` or `mastodon`) for one
topic guide. It has its own tracked short link in a ShortLinks campaign. Its
status is `draft`, `scheduled`, `queued`, `posted` or `failed`.

**`list_channels`**, `GET /channels`, shows whether each channel is connected,
its character limit, and how URLs are counted. Bluesky counts 300 graphemes
with a URL at its real length. Mastodon counts 500 code points with every URL
as 23. In a draft, the `{link}` token is measured as
`https://go.opencircuitsf.com/u/XXXXXXXXXXXX`.

```sh
curl -s -H "Authorization: Bearer $OCSF_API_KEY" $B/channels
```

**`guide_posts_overview`**, `GET /guide-posts/overview`, has one row per topic
guide: `last_posted_at`, `last_campaign`, `channels`, `campaigns`,
`posted_count`, `next_scheduled_at`, `clicks` and `signups`. Use it to find
guides that haven't been posted recently.

```sh
curl -s -H "Authorization: Bearer $OCSF_API_KEY" $B/guide-posts/overview
```

**`list_guide_posts`**, `GET /guide-posts`, lists posts newest first. It takes
the filters `status`, `slug`, `channel`, `campaign`, `from` and `to`. `from`
and `to` take any offset. Its times come back with a Pacific offset.

```sh
curl -s -H "Authorization: Bearer $OCSF_API_KEY" "$B/guide-posts?campaign=guides-2026-12&status=scheduled"
```

**`create_guide_posts`**, `POST /guide-posts`, is **PUBLIC when
`scheduled_for` is set** and takes an `Idempotency-Key`. It makes one draft per
channel, each with its own short link. With `scheduled_for`, it schedules them
all.

- `slug` (required): a topic guide.
- `channels` (required): `["bluesky"]`, `["mastodon"]` or both.
- `body`: the text, with `{link}` where the short link goes. Leave it blank to
  use the guide's `default_body`.
- `campaign`: a ShortLinks campaign slug (lowercase, digits, single hyphens).
  Blank means `guide-posts`. House style is `guides-YYYY-MM`.
- `scheduled_for`: at least a minute and at most a year ahead.

Every field is checked first, including each channel's length limit and
whether the channel is connected, and a problem creates nothing. After that,
each channel stands alone. Read every entry in `results`. `post` is the created
post, or `null`. `link_error` means the post was kept as a draft without its
short link. `schedule_error` means it was created but not scheduled. `error`
means that channel's post wasn't created.

Both channels, scheduled:

```sh
curl -s -X POST $B/guide-posts \
  -H "Authorization: Bearer $OCSF_API_KEY" -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: guides-2026-12-esp32-c3-wont-upload' \
  -d '{"slug":"esp32-c3-wont-upload","channels":["bluesky","mastodon"],"campaign":"guides-2026-12","scheduled_for":"2026-12-01T09:00:00-08:00"}'
```

A Mastodon-only draft with custom copy, not scheduled:

```sh
curl -s -X POST $B/guide-posts \
  -H "Authorization: Bearer $OCSF_API_KEY" -H 'Content-Type: application/json' \
  -d '{"slug":"esp32-c3-power","channels":["mastodon"],"campaign":"guides-2026-12","body":"Brownouts on WiFi? Check your power first. {link}"}'
```

A channel that isn't connected fails validation before anything is written:

```json
{"error":{"code":"validation","message":"the request has invalid fields","fields":{"channels.bluesky":"Bluesky is not connected"}}}
```

An unknown or repeated entry is reported under its position in `channels`,
counting from 0, so every bad entry is named. For example,
`"channels":["myspace","bluesky","bluesky"]` gives the fields
`channels.0` (`"myspace" is not a channel; use bluesky or mastodon`) and
`channels.2` (`bluesky is listed twice`).

**`schedule_guide_post`**, `POST /guide-posts/{id}/schedule`, is **PUBLIC**. It
takes `{"scheduled_at": "…"}`. A draft gets its short link first.

```sh
curl -s -X POST $B/guide-posts/42/schedule \
  -H "Authorization: Bearer $OCSF_API_KEY" -H 'Content-Type: application/json' \
  -d '{"scheduled_at":"2026-12-03T09:00:00-08:00"}'
```

**`unschedule_guide_post`**, `POST /guide-posts/{id}/unschedule`, returns a
scheduled post to draft.

**`update_guide_post`**, `PATCH /guide-posts/{id}`, is **PUBLIC when
`scheduled_at` is set**. It changes `body`, `campaign` (only before the post has
its short link) and `scheduled_at`, in that order.

```sh
curl -s -X PATCH $B/guide-posts/42 \
  -H "Authorization: Bearer $OCSF_API_KEY" -H 'Content-Type: application/json' \
  -d '{"body":"Board stuck? Hold BOOT, tap RESET. {link}"}'
```

**`send_guide_post`**, `POST /guide-posts/{id}/send`, is **PUBLIC**. It queues
the post now, or retries a failed one, and answers `202`. The publisher posts
it shortly.

```sh
curl -s -X POST -H "Authorization: Bearer $OCSF_API_KEY" $B/guide-posts/42/send
```

**`post_guide_again`**, `POST /guide-posts/{id}/again`, copies a posted post
into a new draft with a new short link, optionally in a new `campaign`, and
answers `201`.

**`delete_guide_post`**, `DELETE /guide-posts/{id}`, is **DESTRUCTIVE**. It
deletes drafts only, so unschedule a scheduled post first. A post that isn't a
draft gets `409`. The short link stays in ShortLinks.

```sh
curl -s -X DELETE -H "Authorization: Bearer $OCSF_API_KEY" $B/guide-posts/42
```

### Events (`events:read`, `events:write`)

Events are the site's workshops and other listings (the `workshops` table).
Their status is `draft`, `published` or `canceled`. Each event has a
`public_url`, a `public` flag (true when it is shown on the site), and a
`luma_url` when its `signup_url` is a Luma link.

**`list_events`**, `GET /events`, lists events soonest first, with undated
events last. It takes the filters `from`, `to`, `status`, `type` (an event-type
slug) and `venue` (an id). `get_event`, `GET /events/{id}`, adds `body_md`.

```sh
curl -s -H "Authorization: Bearer $OCSF_API_KEY" \
  "$B/events?type=workshop&from=2026-12-01T00:00:00-08:00&to=2027-01-01T00:00:00-08:00"
curl -s -H "Authorization: Bearer $OCSF_API_KEY" $B/events/17
```

**`create_event`**, `POST /events`, takes an `Idempotency-Key`. It creates a
**draft**, and nothing is public until it is published. It is validated the
same way as the admin form, and the slug comes from the title.

- `title` is required.
- `summary`, `body_md` (Markdown), `starts_at` and `ends_at` (`ends_at` must be
  after `starts_at`).
- `signup_url`, `cover_image` (a `/media/…` path) with `cover_alt`, its alt
  text (up to 1500 characters; Mastodon posts attach the cover only when it is
  set), and `capacity`.
- `interest_ids`.
- `event_type_id`. Omit it for the default type.
- `venue_id`. Omit both this and `no_venue` for the default venue, or send
  `"no_venue": true` for an event with no location.

```sh
curl -s -X POST $B/events \
  -H "Authorization: Bearer $OCSF_API_KEY" -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: event-hack-night-2026-12-03' \
  -d '{"title":"Hack Night","event_type_id":2,"starts_at":"2026-12-03T18:00:00-08:00","ends_at":"2026-12-03T21:00:00-08:00","interest_ids":[1]}'
```

**`update_event`**, `PATCH /events/{id}`, is **PUBLIC on a published event**:
its changes show on the site right away. It changes only the fields sent. An
empty string clears a text field. `"clear_capacity": true` removes the limit,
and `"no_venue": true` removes the location. `interest_ids` replaces the list.
`status` is refused, because status changes go through the three calls below.
Changing `cover_image` without sending `cover_alt` clears the old alt text.

```sh
curl -s -X PATCH $B/events/17 \
  -H "Authorization: Bearer $OCSF_API_KEY" -H 'Content-Type: application/json' \
  -d '{"capacity":16}'
```

**`publish_event`**, `POST /events/{id}/publish`, is **PUBLIC**. It makes the
event visible on the site and in the sitemap right away, and sends no email.
**`unpublish_event`**, `POST /events/{id}/unpublish`, returns it to draft.
**`cancel_event`**, `POST /events/{id}/cancel`, is **PUBLIC**: a published
event stays on the site, marked canceled.

```sh
curl -s -X POST -H "Authorization: Bearer $OCSF_API_KEY" $B/events/17/publish
curl -s -X POST -H "Authorization: Bearer $OCSF_API_KEY" $B/events/17/unpublish
curl -s -X POST -H "Authorization: Bearer $OCSF_API_KEY" $B/events/17/cancel
```

**`duplicate_event`**, `POST /events/{id}/duplicate`, takes an
`Idempotency-Key`. It copies an event to a new **draft**, the way the admin's
Clone does: title, summary, body, type, venue, capacity, signup URL, cover
image and interests. The slug gets the next `-2`, `-3` suffix. With
`starts_at`, the copy moves to that start, and its end keeps the source's
length. Without a body, it keeps the source's times. It is the fast way to
repeat a workshop.

```sh
curl -s -X POST $B/events/17/duplicate \
  -H "Authorization: Bearer $OCSF_API_KEY" -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: dup-17-2026-12-12' \
  -d '{"starts_at":"2026-12-12T13:00:00-08:00"}'
```

### Lookups (`events:read`, `venues:read`)

These give the ids that `create_event` and `update_event` take. Each list
includes disabled or inactive rows, marked as such.

| Tool | Route | Scope | Gives |
|---|---|---|---|
| `list_event_types` | `GET /event-types` | `events:read` | `event_type_id`, and the `type` slug for `list_events` |
| `list_venues` | `GET /venues` | `venues:read` | `venue_id`, and `venue` for `list_events` |
| `list_interests` | `GET /interests` | `events:read` | `interest_ids` |

```sh
curl -s -H "Authorization: Bearer $OCSF_API_KEY" $B/event-types
curl -s -H "Authorization: Bearer $OCSF_API_KEY" $B/venues
curl -s -H "Authorization: Bearer $OCSF_API_KEY" $B/interests
```

### Coverage and cadence rules (`events:read`, `events:write`)

A **cadence rule** says what a normal month looks like, for example "Hack
Night every Tuesday" or "2 workshops a month". The **coverage report** checks a
month's events against the rules. Rules are stated in Pacific time.

**`event_coverage`**, `GET /events/coverage?month=YYYY-MM`, defaults to next
month in Pacific time. For each active rule it returns:

- `expected`: `{dates, count}`;
- the matching `events`, `drafts` and `canceled`;
- `missing_dates` and `draft_only_dates` (weekly rules);
- `missing`, a count;
- `state`: `ok`, `drafts` (covered, but partly only by drafts) or `missing`.

It also returns:

- `month`, `label` (for example `December 2026`), `zone`, and the month's
  Pacific bounds `from` and `to`;
- `unmatched_events`, the month's events that match no active rule (canceled
  ones excluded);
- `summary`: `{rules, missing, drafts, unmatched}`;
- a `luma` section: `not_on_luma` (published events with no Luma copy),
  `differs` (each with `luma_url`, `luma_title` and `diffs`) and
  `only_on_luma`. Its `status` is `ok`, `off`, `unreachable` or `malformed`.
  Unless it is `ok` the lists are empty, and the report still answers `200`.
  Its `read_at` and `last_good_at` are UTC. Once the month has started,
  `only_on_luma` covers only Luma events from now on, and `note` says so.

Canceled events never count toward a rule. Undated events are ignored. Each
listed event carries its `admin_url` and `public_url`.

```sh
curl -s -H "Authorization: Bearer $OCSF_API_KEY" "$B/events/coverage?month=2026-12"
```

**`list_cadence_rules`**, `GET /cadence-rules`, takes `active`, `limit` and
`cursor`. **`set_cadence_rule`**, `POST /cadence-rules`, creates a rule, or
replaces one when `id` is given. It is an upsert and takes no
`Idempotency-Key`. The fields are:

- `name`;
- `event_type_id` and/or `title_prefix` (case-insensitive);
- `frequency`: `weekly` with a `weekday` (`sunday` … `saturday`), or `monthly`
  with `per_month` (1 to 31);
- an optional `venue_id`;
- `active` (default true).

Create and replace both answer `200` with the rule, which names its type as
`event_type {id, slug, name}` and its venue as `venue {id, name}`. An unknown
`id` is `404`. `weekday` is read case-insensitively.

There is no delete call. `"active": false` retires a rule, and deleting is in
the admin UI only.

```sh
curl -s -X POST $B/cadence-rules \
  -H "Authorization: Bearer $OCSF_API_KEY" -H 'Content-Type: application/json' \
  -d '{"name":"Hack Night","event_type_id":2,"frequency":"weekly","weekday":"tuesday"}'
```

### Outreach (`outreach:read`, `outreach:write`)

- **`list_outreach`**, `GET /outreach`: every event with outreach, with post counts by status (paged).
- **`get_event_outreach`**, `GET /events/{id}/outreach`: the event's outreach panel, plus `started`.
- **`outreach_connections_status`**, `GET /outreach/connections`: connection status, booleans and sources only.
- **`start_event_outreach`**, `POST /events/{id}/outreach`: start outreach for a published event (its campaign and tracked links); repeating it returns the existing outreach.
- **`create_outreach_post`**, `POST /outreach/{id}/posts`: a draft post for one `channel` and `wave`; `body` omitted is drafted from the template. Takes `Idempotency-Key`; `201`.
- **`update_outreach_post`**, `PATCH /outreach-posts/{id}`: exactly one of `body` or `regenerate: true`. `409` once posted or sending.
- **`delete_outreach_post`**, `DELETE /outreach-posts/{id}`: destructive; `409` once posted or sending.
- **`reset_event_outreach`**, `POST /events/{id}/outreach/reset`: destructive; removes every post and flyer, refused (`409`) once anything is public.
- **`draft_event_outreach`**, `POST /events/{id}/outreach/draft-all`: drafts every empty slot in `announce` plus any `waves` named; existing posts are skipped. No ShortLinks call; `409` before outreach starts.
- **`prepare_outreach_post`**, `POST /outreach-posts/{id}/prepare`: makes the post's tracked link and puts it in the body; repeating it makes no ShortLinks call. `503` when ShortLinks is not configured.
- **`ensure_outreach_hand_links`**, `POST /events/{id}/outreach/hand-links`: makes every missing link for the hand-posted channels; per-slot failures are listed in `failed`. `503` when ShortLinks is not configured.
- **`schedule_outreach_wave`**, `POST /events/{id}/outreach/schedule`: public; gives every automatic post of `wave` (`announce`, `reminder`, `last-call`) the same `scheduled_at`, all or nothing; skipped posts are in `left_alone`.
- **`unschedule_outreach_wave`**, `POST /events/{id}/outreach/unschedule`: puts the wave's scheduled posts back to draft; `409` when none is scheduled.
- **`send_outreach_post`**, `POST /outreach-posts/{id}/send`: public; queues a prepared post to go out now, or retries a failed one; `202`. `503` when the channel is not configured.
- **`mark_outreach_post_posted`**, `POST /outreach-posts/{id}/posted`: records a hand post, with an optional `remote_url`.
- **`unmark_outreach_post_posted`**, `POST /outreach-posts/{id}/unmark`: takes back a mark-posted; `409` for a post the site sent.
- **`takedown_outreach_post`**, `POST /outreach-posts/{id}/takedown`: destructive and public; deletes the post from its network and returns it to draft. Safe to retry; `503` when the network refuses, with a message naming the reason: `rate_limited`, `rejected`, `timeout`, `network_error` or `failed`.
- **`get_outreach_results`**, `GET /events/{id}/outreach/results`: clicks per post and channel, each post's latest likes/reposts/replies/quotes, flyers and mentions. Always `200` for a known event; `status` says whether the clicks are real (`ok`, `not_configured`, `not_started`, `campaign_missing`, `rejected`, `unreachable`).
- **`outreach_results_summary`**, `GET /outreach/results`: one row per event with outreach (posts, clicks, reactions) plus recent mentions; `status` as above.
- **`outreach_timeline`**, `GET /outreach/timeline`: published events starting in the next 30 days with their outreach and posts; no network call.
- **`outreach_luma`**, `GET /outreach/luma`: each upcoming event against the Luma calendar (`on_luma`, `differs` with the fields, `not_on_luma`) and unmatched Luma events; `status` is `off`, `ok` or a failure.
- **`list_outreach_locations`**, `GET /outreach/locations`: the places flyers are put up, active and inactive.
- **`create_outreach_location`**, `POST /outreach/locations`: `name`, `code` (2–16 lowercase letters, digits or dashes), optional `address`, `notes`, `active`. Takes `Idempotency-Key`; `201`; `409` when the code is taken.
- **`update_outreach_location`**, `PATCH /outreach/locations/{id}`: only the fields sent change; `409` when changing a code a flyer link already uses.
- **`delete_outreach_location`**, `DELETE /outreach/locations/{id}`: destructive; `409` when a flyer link uses it (set `active` to false instead).
- **`list_flyers`**, `GET /flyers`: saved flyers with their event.
- **`get_flyer`**, `GET /flyers/{id}`: one flyer's design, the event facts it prints, its locations with their short links and QR state, and `print_url` (the admin page a person prints it from).
- **`create_flyer`**, `POST /flyers`: `outreach_id` plus optional design fields; `photo` must be a `/media/` path. Takes `Idempotency-Key`; `201`.
- **`update_flyer`**, `PATCH /flyers/{id}`: only the fields sent change; an empty `photo` clears it.
- **`set_flyer_locations`**, `PUT /flyers/{id}/locations`: `{locations: [{location_id, copies}]}` replaces the selection; each active, once, 1–50 copies.
- **`generate_flyer`**, `POST /flyers/{id}/generate`: makes each selected location's short link and QR code for a published event; safe to repeat, and `remaining` above 0 means call again.
- **`set_flyer_link_put_up`**, `POST /flyer-links/{id}/put-up`: `{put_up: true|false}` marks or clears it; marking keeps the first time.
- **`delete_flyer`**, `DELETE /flyers/{id}`: destructive; the flyer's short links stay with the outreach.

### Media (`media:read`, `media:write`)

- **`create_media_upload`**, `POST /media/uploads`: a single-use upload ticket valid 10 minutes: `{upload_url, upload_token, expires_at, max_bytes, curl}`; `201`. `503` when no media directory is set.
- **`list_media`**, `GET /media`: files in the media directory, `{name, path, size_bytes, modified_at}`, newest first (paged).
- *(REST only)* `POST /media/uploads/{id}`, the `upload_url`: send the ticket as `Authorization: Bearer <upload_token>` and the image as multipart field `file` (JPEG or PNG, 5 MiB). Answers `{path}`, a `/media/…` path for `update_event` `cover_image` or `update_flyer` `photo`. An unknown, used or expired ticket is `401`, and one another upload is using at that moment is `409`. A refused upload (wrong type, too large, unreadable) leaves the ticket usable; it is spent only by a successful upload. Failed tickets count toward the same per-IP failed-authentication limit as API keys (`429` once exhausted). A `photo` or `cover_image` with a `.` or `..` segment (including `%2e` and backslash forms) is refused with `400`. The URL carries only the ticket's id; the secret `upload_token` travels only in the `Authorization` header, so it stays out of access logs.

Adding a cover image to an event, end to end:

```sh
# 1. Ask for a ticket (needs media:write).
curl -s -X POST -H "Authorization: Bearer $OCSF_API_KEY" \
  https://www.opencircuitsf.com/api/v1/media/uploads
# → {"upload_url": "https://www.opencircuitsf.com/api/v1/media/uploads/<ticket-id>",
#    "upload_token": "<redacted>", "expires_at": "…", "max_bytes": 5242880, "curl": "…"}

# 2. Upload the file within 10 minutes. The secret goes in the header only.
curl -H "Authorization: Bearer <redacted>" -F file=@cover.jpg \
  https://www.opencircuitsf.com/api/v1/media/uploads/<ticket-id>
# → {"path": "/media/…"}

# 3. Set it on the event, with alt text (needs events:write).
curl -X PATCH -H "Authorization: Bearer $OCSF_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"cover_image": "/media/…", "cover_alt": "Two people soldering at a bench"}' \
  https://www.opencircuitsf.com/api/v1/events/<id>
```

Through MCP, `create_media_upload`'s `curl` field is step 2 ready to run. Set
`cover_alt` whenever you set a cover: Mastodon outreach posts attach the cover
only when it has alt text, and post text-only without it.

### Engagement (`engagement:read`)

`get_engagement`, `GET /engagement`, returns new Bluesky and Mastodon
engagement in a window. Its times are UTC, unlike the rest of v1: Home
Assistant's sensor reads this response as it shipped, so it stays byte for byte
unchanged. It is documented in
[`home-assistant.md`](home-assistant.md).

- **`get_post_performance`**, `GET /posts/performance`: the cumulative results of the event and guide posts posted in a window (`since`/`until`, default the last 7 days, at most 90; `kind` `event`, `guide` or `all`): each post's latest likes/reposts/replies/quotes and clicks, totals per channel and per kind, and a one-sentence summary. Times are Pacific. A ShortLinks failure still answers `200`, with clicks `null` and `clicks_error` set.

## Public API (/api/public/v1/), no key

`https://www.opencircuitsf.com/api/public/v1/` is a public, read-only REST API
(`internal/publicapi`, `#0734`). It is separate from the admin `/api/v1/`
above:

- **No key.** An `Authorization` header is ignored; nothing answers 401 or 403.
- **GET only.** Any other method is a JSON `405` with `Allow: GET, HEAD, OPTIONS`; `OPTIONS` is a `204` CORS preflight.
- **CORS `*`.** Every response carries `Access-Control-Allow-Origin: *`, so a browser app on any site can read it. The admin `/api/v1/` sends no CORS headers.
- **Cached.** A success is `Cache-Control: public, max-age=60`; an error is `no-store`.

It serves the same operations as the public MCP server, built from one
registry, so a route and its tool return the same JSON:

| Route | MCP tool |
|---|---|
| `GET events` (`limit`, `after`, `before`) | `list_upcoming_events` |
| `GET events/search` (`query`, `from`, `to`, `include_past`, `limit`) | `search_events` |
| `GET events/{slug}` | `get_event` |
| `GET newsletter` (`limit`) | `list_newsletter_issues` |
| `GET newsletter/{slug}` | `get_newsletter_issue` |
| `GET about` | `get_about` |

The OpenAPI document is generated at startup:
`https://www.opencircuitsf.com/api/public/v1/openapi.json`.

    curl -s 'https://www.opencircuitsf.com/api/public/v1/events?limit=3'

An event whose slug is `search` cannot be read at `events/{slug}`, because
`events/search` takes that path; use the MCP `get_event` tool for it.
Unknown, draft and unpublished items all return the same `404` body.
