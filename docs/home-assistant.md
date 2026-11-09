# Home Assistant: daily engagement check

Every morning, Home Assistant asks the site what changed on Bluesky and
Mastodon in the last 24 hours. If anything did, it sends a phone notification
with a one-sentence summary. It uses a read-only API token and makes one
request a day.

## 1. What the endpoint returns

`GET https://www.opencircuitsf.com/api/v1/engagement`, with the header
`Authorization: Bearer <token>`.

- The token is only read from the header. It is never accepted in the query string.
- GET only. Any other method gets `405`.
- Every response carries `Cache-Control: no-store`.
- By default the window is the 24 hours ending at the request, `(since, until]`.
- Optional `?hours=1..168` or `?since=<RFC 3339 time, within the last 30 days>`
  changes the window. You can use one or the other, not both.
- **Encode the `+` in a `since` offset.** In a query string a bare `+` is read
  as a space. So `?since=2026-11-07T09:00:00+00:00` arrives as
  `2026-11-07T09:00:00 00:00` and returns `400`. Write it as
  `?since=2026-11-07T09:00:00%2B00:00`, or use a `Z` time:
  `?since=2026-11-07T09:00:00Z`. Negative offsets such as `-08:00` work as typed.

A sample response:

```json
{
  "since": "2026-11-07T17:00:00Z",
  "until": "2026-11-08T17:00:00Z",
  "data_as_of": "2026-11-08T16:45:12Z",
  "has_engagement": true,
  "total": 3,
  "bluesky":  {"likes": 1, "reposts": 1, "replies": 0, "quotes": 0, "mentions": 0, "total": 2},
  "mastodon": {"likes": 0, "reposts": 0, "replies": 1, "quotes": 0, "mentions": 0, "total": 1},
  "posts": [
    {"event": "Intro to Soldering", "event_date": "2026-11-20", "wave": "announce", "channel": "bluesky",
     "url": "https://bsky.app/profile/example/post/abc", "posted_at": "2026-11-06T18:00:00Z",
     "likes": 1, "reposts": 1, "replies": 0, "quotes": 0, "total": 2}
  ],
  "summary": "3 new interactions: Bluesky 2 (1 like, 1 repost), Mastodon 1 (1 reply)."
}
```

| Field | Meaning |
|---|---|
| `total` | New interactions across both channels. This is the sensor's state. |
| `has_engagement` | `true` when `total > 0`. |
| `summary` | The notification sentence, ready to send. |
| `bluesky`, `mastodon` | Per-channel `likes`, `reposts`, `replies`, `quotes`, `mentions`, `total`. |
| `posts` | Per-post changes: `event`, `event_date` (a Pacific date, or `null`), `wave`, `channel`, `url`, `posted_at`, the four counters and `total`. |
| `data_as_of` | When the newest engagement snapshot was taken, or `null` if there are none. |

When nothing changed, `summary` reads exactly:
`No new engagement on Bluesky or Mastodon in the last 24 hours.`

## 2. Create the token

1. Sign in to the admin console.
2. Open **Settings**, then the **API tokens** section. The direct link is
   `https://www.opencircuitsf.com/admin#/settings?section=api`.
3. Under **Name**, type `Home Assistant`.
4. The form shows **Access: Read engagement (engagement:read)**. That is the
   only scope today, and it is applied automatically.
5. Click **Create token**.
6. A box appears: **Copy this token now. It will not be shown again.** Click
   **Copy**, then **Done**.
7. The new row shows **Name**, **Access**, **Prefix** (the first 12
   characters, `ocsf_…`), **Created**, **Last used** (`Never` until the token
   is first used, then updated at most about once a minute) and **Status**
   (`Active`).

Tokens start with `ocsf_` and are 48 characters long.

## 3. Store it in `secrets.yaml`

```yaml
# secrets.yaml, next to configuration.yaml
ocsf_engagement_auth: "Bearer ocsf_PASTE_THE_TOKEN_FROM_SETTINGS_HERE"
```

- The value includes the word `Bearer` and a space, as the RESTful Sensor docs
  recommend.
- Never put the token inline in `configuration.yaml`.
- If your configuration is in git, don't commit `secrets.yaml`.

## 4. The sensor (`configuration.yaml`)

```yaml
sensor:
  - platform: rest
    name: "Open Circuit engagement"
    unique_id: ocsf_engagement
    resource: https://www.opencircuitsf.com/api/v1/engagement
    method: GET
    headers:
      Authorization: !secret ocsf_engagement_auth
    value_template: "{{ value_json.total }}"
    json_attributes:
      - has_engagement
      - summary
      - bluesky
      - mastodon
      - data_as_of
      - since
      - until
    scan_interval: 86400
    timeout: 30
```

- If `configuration.yaml` already has a `sensor:` key, add the list item under
  it. A second `sensor:` key is a YAML error.
- `posts` is left out of `json_attributes` on purpose. It can grow large, and
  Home Assistant does not record large attributes.
- The sensor polls once at startup and then once a day. The automation below
  forces the poll that matters.
- The entity id is `sensor.open_circuit_engagement`. Check it under
  Settings → Devices & services → Entities.
- After a restart, look at it in Developer tools → States. The state is a
  number, and the attributes include `summary`.
- The integration-level `rest:` block also works. Use it if you later want
  several sensors from the same request.

## 5. The daily automation (`automations.yaml`)

This is a list item for `automations.yaml`, which the default
`automation: !include automations.yaml` loads. You can also paste it into the
automation editor's **Edit in YAML**.

```yaml
- id: ocsf_daily_engagement
  alias: "Open Circuit daily engagement"
  description: "At 9:00 AM, refresh the engagement sensor and notify if there is anything new."
  mode: single
  triggers:
    - trigger: time
      at: "09:00:00"
  conditions: []
  actions:
    - action: homeassistant.update_entity
      data:
        entity_id:
          - sensor.open_circuit_engagement
    - condition: state
      entity_id: sensor.open_circuit_engagement
      attribute: has_engagement
      state: true
    - action: notify.mobile_app_YOUR_DEVICE
      data:
        title: "Open Circuit SF"
        message: "{{ state_attr('sensor.open_circuit_engagement', 'summary') }}"
```

- Replace `YOUR_DEVICE` with your phone's action name. To find it, type
  `notify.mobile_app` in Developer tools → Actions.
- **Keep the condition as a step under `actions:`, after the refresh.** The
  automation's `conditions:` block runs before any action, so a condition
  there would test yesterday's value.
- `state: true` stays unquoted. An attribute keeps its own type, so a quoted
  `"true"` never matches.
- The time is in Home Assistant's own time zone. Each run covers the 24 hours
  before the request, so a daily 9:00 AM run covers every moment once.
- Installs older than 2024.10 use `trigger:` / `platform: time` / `service:`
  in place of `triggers:` / `trigger: time` / `action:`.
- `notify.send_message` with a `notify.*` entity target also works. Its `title`
  field is optional, and only notify entities that support titles use it.
- **To notify every day,** delete the `condition` step. On quiet days the
  message is the "No new engagement…" sentence.

## 6. Check it by hand with `curl`

This keeps the token out of your shell history:

```sh
read -rs OCSF_TOKEN   # paste the token, press Return
curl -s -w '\nHTTP %{http_code}\n' -H "Authorization: Bearer $OCSF_TOKEN" https://www.opencircuitsf.com/api/v1/token
curl -s -w '\nHTTP %{http_code}\n' -H "Authorization: Bearer $OCSF_TOKEN" https://www.opencircuitsf.com/api/v1/engagement
unset OCSF_TOKEN
```

- The probe at `/api/v1/token` accepts any valid token, whatever its scope. It
  returns `{"name":"Home Assistant","prefix":"ocsf_…","scopes":["engagement:read"]}`
  (the prefix is the first 12 characters) and `HTTP 200`.
- The engagement call returns the JSON shown in section 1.
- `curl -s -o /dev/null -w '%{http_code}\n' https://www.opencircuitsf.com/api/v1/engagement`
  prints `401`, because it sends no header.
- To see a whole week, add `?hours=168` to the engagement URL.
- To pick a start time, use `?since=` with a `Z` time or a `%2B`-encoded
  offset (section 1).
- These commands use `-s`, not `-v`. `-v` prints the request header to the
  terminal, and the token with it.

## 7. Troubleshooting

| What you see | Body | Cause | Do this |
|---|---|---|---|
| `401` | `{"error":"unauthenticated"}`, with `WWW-Authenticate: Bearer realm="opencircuitsf"` | Any of these: no `Authorization` header, a missing `Bearer ` prefix, a mistyped or truncated token, an unknown token, a revoked token. The response never says which. | Check that `secrets.yaml` has `Bearer ` and the whole 48-character `ocsf_…` value. Run the probe with curl. In Settings → API tokens, check that the row's Status is `Active`. If the token is lost or revoked, create a new one (section 8). |
| `403` | `{"error":"forbidden"}` | The token is valid but lacks `engagement:read`. | The probe's `scopes` shows what the token has. Create a new one. Every token the Settings form makes has this scope. |
| `400` | `{"error":"<message>"}` | Only the optional `hours` and `since` parameters cause this. The messages are listed below. | Remove the parameter, or fix it as the message says. The sensor above sends neither. |
| `400` with `since must be an RFC 3339 time` for a time that looks right | `{"error":"since must be an RFC 3339 time"}` | The time has an offset like `+00:00` with an unencoded `+`. The `+` is read as a space. | Write the offset as `%2B00:00`, or use a `Z` time (section 1). |
| `404` | — | The URL is mistyped, or the server predates this feature. | Check `resource:`. Make sure the site has been deployed with `#0653`. |
| `405` | — | The sensor uses `method: POST`. | Use `GET`. |
| `500` | `{"error":"internal_error"}` or `{"error":"internal error"}` | A server-side failure in the token lookup or the engagement query. | Retry later. The operator checks `journalctl -u opencircuit`. |
| State `unknown` or unavailable | — | A timeout, or a response that is not JSON or has no `total` (any error). | Check Settings → System → Logs for the `rest` warning, then run the curl checks. |
| Stale `data_as_of` | — | Snapshots are taken hourly for posts under 7 days old, daily from 7 to 30 days, and not at all after 30 days. A `data_as_of` up to about a day old is normal when the newest post is a week or more old. If it is `null` or older than a day, either no post is younger than 30 days (nothing to track, so `total` is 0, which is correct) or the tracker isn't capturing. | Check that the posts are under 30 days old. If they are, an admin checks **Settings → Outreach → Connections** for both channels and looks at the server log. |
| The notification shows yesterday's numbers | — | The refresh hadn't finished when the condition ran. With a once-a-day `scan_interval` this is unlikely. | Add `- delay: "00:00:05"` after the `update_entity` step. |

The `400` messages, exactly as the server sends them:

- `use either hours or since, not both`
- `hours must be a whole number from 1 to 168`
- `since must be an RFC 3339 time`
- `since must not be in the future`
- `since must be within the last 30 days`

## 8. Revoking and rotating

To revoke a token:

1. In Settings → API tokens, click **Revoke** on the row.
2. Confirm the prompt: *Revoke "Home Assistant"? Anything using it will stop
   working on its next request.*
3. The Status changes to `Revoked <date>`. The row stays in the list, and a
   revoked token never works again.

To rotate without a gap:

1. Create a new token, named for example `Home Assistant 2027`, and copy it.
2. Replace the value in `secrets.yaml`, keeping `Bearer `.
3. Restart Home Assistant. A change to `secrets.yaml` is only read when the
   YAML that uses it is reloaded, and a restart is the reliable way to do that.
4. Run the curl probe with the new token. Or run `homeassistant.update_entity`
   from Developer tools → Actions and check that the new row's **Last used**
   no longer reads `Never`. It updates at most about once a minute.
5. Revoke the old row.

Rotate whenever the token may have leaked: pasted somewhere, in a backup, or
in a shared config repo. Tokens don't expire, so otherwise rotate as often as
you like.

## 9. References

Checked against these official pages on 2026-11-08:

- RESTful Sensor: <https://www.home-assistant.io/integrations/sensor.rest/>
- RESTful (integration-level `rest:`): <https://www.home-assistant.io/integrations/rest/>
- Storing secrets: <https://www.home-assistant.io/docs/configuration/secrets/>
- Automation triggers, including the time trigger: <https://www.home-assistant.io/docs/automation/trigger/>
- State condition (attribute values keep their type): <https://www.home-assistant.io/conditions/state/>
- `homeassistant.update_entity`: <https://www.home-assistant.io/actions/homeassistant.update_entity/>
- Stop, and a condition step stopping the steps after it: <https://www.home-assistant.io/building-blocks/stop>
- Companion app notifications (`notify.mobile_app_*` with `title` and `message`): <https://companion.home-assistant.io/docs/notifications/notifications-basic/>
- Notifications (`notify.send_message`): <https://www.home-assistant.io/actions/notify.send_message/>
- Working with states (`state_attr`): <https://www.home-assistant.io/docs/templating/states/>
