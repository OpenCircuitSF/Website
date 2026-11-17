# MCP server (`/mcp`): Claude Code and Claude Desktop

`https://www.opencircuitsf.com/mcp` serves the [`/api/v1`](api.md) operations
as MCP tools. It uses Streamable HTTP, is stateless and answers in JSON. Each
tool is one registry operation (`internal/mcpserver`), so it has the same name
as the operation's `operationId` in `/api/v1/openapi.json`, the same fields,
the same validation and the same error envelope. Keys, scopes and the rate
limit are the same too: one key has one budget across REST and MCP.

## 1. Make a key for the agent

Follow [`api.md` → Creating a key](api.md#creating-a-key). For an agent:

- **Scopes.** Choose **Agent (all guide and event scopes)**: `guides:read`,
  `guide_posts:read`, `guide_posts:write`, `events:read`, `events:write` and
  `venues:read`. For an agent that should only look, tick only `guides:read`,
  `guide_posts:read`, `events:read` and `venues:read`. Leave `engagement:read` to Home
  Assistant's own key.
- **One key per client.** Use separate keys for your laptop's Claude Code,
  Claude Desktop and any CI job, named so the audit log says which one acted.
- **Expiry.** Keep the 90-day default, and rotate before it runs out.

The tool list follows the key. A client sees only the tools its scopes allow,
so a read-only key never sees `send_guide_post`.

## 2. Connect Claude Code

```sh
export OCSF_API_KEY='ocsf_…'
claude mcp add --transport http ocsf https://www.opencircuitsf.com/mcp \
  --header "Authorization: Bearer $OCSF_API_KEY"
```

Your shell expands `$OCSF_API_KEY` when you run this, so the key itself is
saved in your Claude Code config (`~/.claude.json`, local scope by default).
**Never add it with `--scope project`**, which writes it into `.mcp.json` in the
repository. To share the server definition without the key, check in a
`.mcp.json` that uses Claude Code's environment-variable expansion instead:

```json
{
  "mcpServers": {
    "ocsf": {
      "type": "http",
      "url": "https://www.opencircuitsf.com/mcp",
      "headers": { "Authorization": "Bearer ${OCSF_API_KEY}" }
    }
  }
}
```

Check the connection with `claude mcp list`, or `/mcp` inside a session. Then
ask "call whoami". It answers with the key's name, scopes and expiry.

For a local server, use `http://localhost:8080/mcp` with a key made in your
local admin console. The mailing list and these tools need Postgres; they are
not served under `STORAGE=json`.

## 3. Connect Claude Desktop

Claude Desktop's **Settings → Connectors → Add custom connector** form has no
field for a bearer header. Instead, bridge it with the `mcp-remote` npm package
(a third-party stdio-to-HTTP proxy) in `claude_desktop_config.json`
(**Settings → Developer → Edit Config**):

```json
{
  "mcpServers": {
    "ocsf": {
      "command": "npx",
      "args": ["-y", "mcp-remote", "https://www.opencircuitsf.com/mcp",
               "--header", "Authorization:${OCSF_AUTH}"],
      "env": { "OCSF_AUTH": "Bearer ocsf_…" }
    }
  }
}
```

The header is written without a space after the colon, with the space inside
the variable. That gets around argument-splitting problems some platforms have
with spaces in `args`. Restart Claude Desktop after saving. This file holds the
key in plain text, so keep it out of anything synced or shared.

## 4. Rotating and revoking

- **Rotate.** Create the new key first. Then update the client: run
  `claude mcp remove ocsf` and `claude mcp add …` with the new key, or edit
  Claude Desktop's `env`. Call `whoami` and check the prefix is the new key's.
  Then **Revoke** the old key under Settings → API tokens.
- **Revoke** right away if a key may have leaked. Revocation applies to the
  next request. A revoked or expired key gets the same `401` as an unknown one,
  and an MCP client shows that as a failed connection.
- The admin list shows each key's **Last used** time (updated at most once a
  minute), which helps you find a key nothing is using any more.

## 5. Tools

Each description begins with **PUBLIC** when the call makes something visible
to the public, and with **DESTRUCTIVE** when it can't be undone. The server's
instructions tell the model to confirm with you before calling either. The MCP
annotations carry the same flags: `openWorldHint` for PUBLIC, `destructiveHint`
for DESTRUCTIVE, and `readOnlyHint` for every read.

| Resource | Tool | Flag | Scope | Takes `idempotency_key` |
|---|---|---|---|---|
| Connection | `whoami` | | any | |
| Guides | `list_guides` | | `guides:read` | |
| | `get_guide` | | `guides:read` | |
| Guide posts | `list_channels` | | `guide_posts:read` | |
| | `guide_posts_overview` | | `guide_posts:read` | |
| | `list_guide_posts` | | `guide_posts:read` | |
| | `create_guide_posts` | PUBLIC (when `scheduled_for` is set) | `guide_posts:write` | yes |
| | `update_guide_post` | PUBLIC (when `scheduled_at` is set) | `guide_posts:write` | |
| | `schedule_guide_post` | PUBLIC | `guide_posts:write` | |
| | `unschedule_guide_post` | | `guide_posts:write` | |
| | `send_guide_post` | PUBLIC | `guide_posts:write` | |
| | `post_guide_again` | | `guide_posts:write` | |
| | `delete_guide_post` | DESTRUCTIVE | `guide_posts:write` | |
| Events | `list_events` | | `events:read` | |
| | `get_event` | | `events:read` | |
| | `create_event` | | `events:write` | yes |
| | `update_event` | PUBLIC (on a published event) | `events:write` | |
| | `publish_event` | PUBLIC | `events:write` | |
| | `unpublish_event` | | `events:write` | |
| | `cancel_event` | PUBLIC | `events:write` | |
| | `duplicate_event` | | `events:write` | yes |
| Lookups | `list_event_types` | | `events:read` | |
| | `list_venues` | | `venues:read` | |
| | `list_interests` | | `events:read` | |
| Coverage | `event_coverage` | | `events:read` | |
| | `list_cadence_rules` | | `events:read` | |
| | `set_cadence_rule` | | `events:write` | |
| Outreach | `list_outreach` | | `outreach:read` | |
| | `get_event_outreach` | | `outreach:read` | |
| | `outreach_connections_status` | | `outreach:read` | |
| | `start_event_outreach` | | `outreach:write` | |
| | `create_outreach_post` | | `outreach:write` | yes |
| | `update_outreach_post` | | `outreach:write` | |
| | `delete_outreach_post` | DESTRUCTIVE | `outreach:write` | |
| | `reset_event_outreach` | DESTRUCTIVE | `outreach:write` | |
| | `draft_event_outreach` | | `outreach:write` | |
| | `prepare_outreach_post` | | `outreach:write` | |
| | `ensure_outreach_hand_links` | | `outreach:write` | |
| | `schedule_outreach_wave` | PUBLIC | `outreach:write` | |
| | `unschedule_outreach_wave` |  | `outreach:write` | |
| | `send_outreach_post` | PUBLIC | `outreach:write` | |
| | `mark_outreach_post_posted` |  | `outreach:write` | |
| | `unmark_outreach_post_posted` |  | `outreach:write` | |
| | `takedown_outreach_post` | DESTRUCTIVE, PUBLIC | `outreach:write` | |
| | `get_outreach_results` | | `outreach:read` | |
| | `outreach_results_summary` | | `outreach:read` | |
| | `outreach_timeline` | | `outreach:read` | |
| | `outreach_luma` | | `outreach:read` | |
| Media | `create_media_upload` | | `media:write` | |
| | `list_media` | | `media:read` | |
| Engagement | `get_engagement` | | `engagement:read` | |

A failed tool call is a tool error (`isError`) whose text is the v1 error
envelope, `{"error":{"code":…,"message":…,"fields":…}}`, so the model can read
the field and fix the call.

### `idempotency_key` on create tools

`create_guide_posts`, `create_event` and `duplicate_event` take an optional
`idempotency_key` argument, 1 to 255 printable ASCII characters. It is MCP's
version of REST's `Idempotency-Key`.

- A retry with the same key and the same arguments returns the first call's
  result and creates nothing.
- The same key with different arguments, or while the first call is still
  running, is a `conflict`.
- Keys are kept 24 hours per API key.
- Any other tool refuses the argument as an unknown field.

Use a key you can rebuild from the request itself, so a retry after a crash
produces the same one, for example `guides-2026-12-esp32-c3-power-2026-12-03`
or `dup-17-2026-12-12`.

### Cancellation

- **Reads cancel.** A read-only tool stops when the client cancels or
  disconnects.
- **Writes finish.** Once a tool that changes data has started, it runs to
  completion even if you press Esc, the client times out or the connection
  drops. Its audit rows and its idempotency result are recorded.
- **The 2-minute write timeout.** Every write is bounded at two minutes on the
  server.
- **After an interrupted write**, check whether it happened before calling it
  again: use the same `idempotency_key`, or the matching `list_*` tool.

Cancelling only reaches the server from clients on the 2026-07-28 MCP protocol
or later. With older clients a read also runs to the end, which is harmless.

## 6. Worked workflows

Times below are for a session on 15 November 2026. "Next month" is December
2026, and every date in it is Pacific Standard Time (`-08:00`). The
[`ocsf-ops` skill](../.claude/skills/ocsf-ops/SKILL.md) wraps these with the
house rules.

### A. Two weeks of ESP32-C3 guide posts

> Schedule one post a day for the next two weeks across the ESP32-C3 guides,
> on both channels, with campaign `guides-2026-12`, skipping guides posted in
> the last 30 days.

1. `list_channels {}` checks that both `bluesky` and `mastodon` are
   `configured`. If either isn't, stop and say so; `create_guide_posts` would
   refuse anyway.
2. `list_guides {"set": "esp32", "kind": "topic", "limit": 200}` lists the
   ESP32-C3 topic guides (17 today).
3. `guide_posts_overview {}` gives each guide's `last_posted_at` and
   `next_scheduled_at`. Drop any guide with `last_posted_at` after 16 October
   2026, or with a post already scheduled.
4. `list_guide_posts {"campaign": "guides-2026-12"}` shows what an earlier run
   already made, so a re-run doesn't double up.
5. Make a plan: one guide per day, 16 to 29 November, at 9:00 AM Pacific. If
   fewer than 14 guides remain, show the shorter plan rather than repeating
   guides. Show the plan as a table (date, guide, the two bodies) and **ask
   before going ahead**, because scheduling is PUBLIC.
6. Once you approve, make 14 calls like this one:

   ```json
   create_guide_posts {
     "slug": "esp32-c3-wont-upload",
     "channels": ["bluesky", "mastodon"],
     "campaign": "guides-2026-12",
     "scheduled_for": "2026-11-16T09:00:00-08:00",
     "idempotency_key": "guides-2026-12-esp32-c3-wont-upload-2026-11-16"
   }
   ```

   Leaving out `body` uses the guide's `default_body`, which fits both limits.
   Read every `results[]` entry. A `link_error` or `schedule_error` leaves that
   channel as a draft, and the agent reports it rather than retrying blindly.
7. `list_guide_posts {"campaign": "guides-2026-12", "status": "scheduled"}`
   confirms the result. There should be 28 posts, each at 9:00 AM with a
   `-08:00` offset.

### B. Next month's Programming LEDs workshop, like last time

> Set up next month's Programming LEDs workshop, the same as last time, on the
> second Saturday.

1. `list_event_types {}` finds the `workshop` slug.
2. `list_events {"type": "workshop", "status": "published", "from": "2026-05-01T00:00:00-07:00", "to": "2026-11-16T00:00:00-08:00", "limit": 200}`
   lists past workshops, soonest first. Take the **last** one whose title starts
   "Programming LEDs", page through `next_cursor` if needed, then call
   `get_event {"id": 17}` to see it in full.
3. The second Saturday of December 2026 is **12 December**, since 1 December is
   a Tuesday. Keep the source's start time of day.
4. `list_events {"from": "2026-12-01T00:00:00-08:00", "to": "2027-01-01T00:00:00-08:00", "type": "workshop"}`
   checks that it isn't already set up.
5. Make the copy:

   ```json
   duplicate_event {"id": 17, "starts_at": "2026-12-12T13:00:00-08:00",
                    "idempotency_key": "dup-17-2026-12-12"}
   ```

   The result is a **draft** (`programming-leds-2` or the next suffix). It has
   the same body, venue, capacity, interests and signup URL, and the end time
   keeps the source's length.
6. Report the draft's admin link
   (`https://www.opencircuitsf.com/admin#/workshops/<id>`) and anything that probably needs
   changing: a Luma `signup_url` still points at the old Luma event, and the
   new date needs its own Luma event. Then **ask** before `publish_event`.

### C. "What's missing for next month?"

> What's missing for next month?

1. `event_coverage {}` defaults to the next Pacific month, here `2026-12`. Or
   name the month: `{"month": "2026-12"}`.
2. Summarise from `summary` and each rule's `state`:
   - for weekly rules, `missing_dates`;
   - for monthly rules, `missing`;
   - drafts that still need publishing (`draft_only_dates`, `drafts`);
   - `unmatched_events`;
   - the `luma` section (`not_on_luma`, `differs`), when its `status` is `ok`.
3. Ask "create drafts for the missing ones?"

> Yes, create them.

4. For each missing date or count, find the most recent event matching the
   rule: `list_events` with `type` set to the rule's `event_type.slug` and
   `venue` to its `venue.id`, then pick by the rule's `title_prefix` (there is
   no title filter). Then:

   ```json
   duplicate_event {"id": 31, "starts_at": "2026-12-01T18:00:00-08:00",
                    "idempotency_key": "coverage-2026-12-rule-3-2026-12-01"}
   ```

   If nothing matches, use `create_event` with `event_type_id` and `venue_id`
   from the rule's `event_type.id` and `venue.id`, a title from its
   `title_prefix` or `name`, and an `idempotency_key` of the same shape. A
   monthly rule only gives a count, so propose dates first and confirm them.
5. `event_coverage {"month": "2026-12"}` again. Those rules should now show
   `state: "drafts"`. List the drafts with their `admin_url`s, and **ask**
   before publishing any.
