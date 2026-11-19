# Social posts: content guide

How to write the Bluesky and Mastodon posts the site sends: event outreach
(announce, reminder, last call) and guide posts. Read it before drafting or
editing post copy, whether in the admin Outreach screen, the guide-post
screen, or through the `ocsf` MCP tools ([`mcp.md`](mcp.md)).

The mechanics (scheduling, campaigns, idempotency keys, confirming before
anything public) live in the `ocsf-ops` skill and [`mcp.md`](mcp.md). This
file covers only what the posts say.

## Hashtags

Hashtags go in the **last paragraph** of the post, after the link, on a line
of their own. Write them in lowercase (`#sanfrancisco`, `#esp32`), and keep
to two.

On Mastodon, hashtags are how people who don't follow the account find a
post: there is no algorithmic feed, so following a tag is the main way new
readers arrive. On Bluesky they matter less but are still clickable and
searchable.

### Event posts

| Tag | When |
|---|---|
| `#sanfrancisco` | The event is **in San Francisco**. Leave it off for an event anywhere else. |
| `#electronics` | Every event. |

The default comes from the **Outreach hashtags** server setting
(`outreach_hashtags`, under Settings → Server settings), which is added to
every event's drafts whatever the venue. Every venue used so far is in San
Francisco, so the default is right today. For an event outside the city,
remove `#sanfrancisco` from its drafts by hand before they are scheduled.

### Guide posts

Pick one or two tags from what the guide is about:

| Tag | When |
|---|---|
| `#esp32` | The guide is about the ESP32: it is in the `esp32` set, or its tags include `ESP32` (for example `soldering-header-pins`). A guide that only mentions an ESP32 in passing does not get it. |
| `#raspberrypi` | The guide is about the Raspberry Pi: it is in the `pi` set, or its tags include `Raspberry Pi`. |
| `#electronics` | Every guide. |

So an ESP32 guide carries `#esp32 #electronics`, a Raspberry Pi guide carries
`#raspberrypi #electronics`, and every other guide carries `#electronics`.

**Don't use narrow tool or technique tags** such as `#soldering` or
`#multimeter`. Few people follow them, and `#electronics` already covers those
guides. Add a new tag to this table only when it names a topic people
actually follow, and update this file when you do.

Guide posts don't get hashtags automatically: a guide's default copy is its
`post` text and the link. Add the tag line when creating the post (pass a
`body` to `create_guide_posts`), or edit it afterwards with
`update_guide_post`.

## Shape of a post

```
<one or two sentences: the hook>

<link>

#esp32 #electronics
```

- Bluesky allows 300 characters and Mastodon 500, counting the link and the
  hashtags. The server rejects a body that is too long, so check the copy fits
  with the tag line included.
- Keep the guide's own copy as the hook. It is written to stand alone.
- One link per post: the post's own short link (`{link}` in a draft), so the
  click is attributed to that post and channel.
