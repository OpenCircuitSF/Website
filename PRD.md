# Open Circuit SF — Product Requirements Document

**Domain:** `opencircuitsf.com`
**Repository:** `github.com/brennanMKE/OpenCircuitSF`
**Go module:** `github.com/brennanMKE/OpenCircuitSF`
**Binary / service name:** `opencircuit`
**Status:** Draft — v1 scope agreed, ready to break into issues
**Last updated:** 2026-08-18

---

## 1. Overview

Open Circuit SF is a San Francisco group that runs hands-on electronics workshops.
Workshops happen at any location — a makerspace, a co-working room, or a
neighborhood garage. Open Circuit SF is location-independent by design and has
its own identity; it is not affiliated with or hosted by any single venue.

This document specifies the public website at `opencircuitsf.com`, its
interest-segmented mailing list, and the admin console used to run both.

### What this site is for

| Goal | How the site serves it |
|---|---|
| Give social media a destination | Fast, well-branded landing page with correct link-preview cards |
| Grow an audience | Low-friction email signup with interest selection |
| Announce workshops | Public workshop listings + targeted email campaigns |
| Keep the list healthy | Double opt-in, granular preferences, one-click unsubscribe, bounce/complaint suppression |
| Measure what works | Inbound UTM capture, tied to `go.opencircuitsf.com` campaign tracking |

### Companion property — `go.opencircuitsf.com`

A separate installation of [ShortLinks](https://github.com/brennanMKE/ShortLinks)
serves all social-media links, using its campaign feature to measure which
channels drive traffic. It is **out of scope for this repository** — it is a
deploy of an existing product, not new code. Two integration points matter here:

1. Short links land on `opencircuitsf.com` with `utm_*` parameters attached.
   This site captures those on first visit and attributes new subscribers to a
   source (§6.3).
2. Both properties run on the same EC2 instance under separate Apache vhosts,
   separate systemd units, and separate PostgreSQL databases (§10.2).

### Relationship to ShortLinks (this codebase's starting point)

This project starts by **copying the ShortLinks skeleton and stripping the
link-shortening features**. What comes over unchanged, what gets deleted, and
what gets built new is enumerated in §3.

---

## 2. v1 Scope

### In scope

- **Marketing pages** — home, about, workshops index, workshop detail.
- **Workshops** — admin-managed records rendered on the public site. No RSVP,
  no capacity tracking, no ticketing in v1.
- **Mailing list** — email-only signup with interest selection, double opt-in,
  a token-authenticated preference center, and full unsubscribe handling.
- **Email campaigns** — compose in Markdown, target by interest, send via AWS
  SES with per-recipient unsubscribe tokens.
- **List hygiene** — SES bounce/complaint ingestion, suppression list, inbound
  `mailto:` unsubscribe processing.
- **Passkey accounts for staff** — organizers and admins only. WebAuthn/FIDO2,
  no passwords. Copied from ShortLinks.
- **Admin console** — subscribers, interests, campaigns, workshops, users,
  settings, audit log.

### Explicitly out of scope for v1

| Not building | Why / when |
|---|---|
| Subscriber accounts (passkeys for the public) | Signup friction kills a social funnel. Preference center uses signed token links instead. Revisit if members ask for logins. |
| RSVP / capacity / waitlist | Needs an identity story for RSVPers. Phase 7 candidate. |
| Payments or ticketing | Not needed for free community workshops. |
| Link shortening | Lives at `go.opencircuitsf.com`. All shortener code is deleted from this repo. |
| Blog / CMS | Workshop records cover the content need in v1. |
| Photo galleries | Static assets only until there is a reason for more. |
| Discord integration | Manual cross-posting for now. |

### Non-goals

- No third-party analytics scripts, no ad trackers, no external CDN
  dependencies. The site is self-contained and privacy-respecting; measurement
  comes from `go.opencircuitsf.com` and first-party UTM capture.
- No email open-tracking pixels. Click tracking, if ever added, goes through
  the shortener, not through per-recipient pixels.

---

## 3. Codebase Strategy — Copy and Strip

The fastest correct path is to copy the ShortLinks repository skeleton, delete
the shortener, and build the mailing list into the space that opens up. This
carries over a tested config loader, DB pool, passkey auth stack, session
middleware, audit log, rate limiter, SSE broker, embedded-SPA build, deploy
scripts, and the `docs/` + `issues/` conventions.

### 3.1 Copy unchanged (rename module path + branding strings only)

| Path | What it gives us |
|---|---|
| `internal/config/` | Env-var loader with all-errors-at-once validation |
| `internal/db/` | pgx v5 pool |
| `internal/auth/` | WebAuthn registration / login / recovery ceremonies, sessions, credential store, `Mailer` interface |
| `internal/middleware/` | `RequireSession`, `RequireAdmin`, per-IP token-bucket rate limiter, dev auto-login |
| `internal/audit/` | Append-only audit log with `WriteTx` + `Record` paths |
| `internal/events/` | In-memory SSE pub/sub broker |
| `internal/handlers/` — `auth.go`, `credentials.go`, `me.go`, `settings.go`, `users.go`, `audit.go`, `health.go`, `static.go`, `events.go` | Auth + admin HTTP surface |
| `internal/devstore/` | `STORAGE=json` in-memory dev store (adapt to new entities) |
| `web/src/lib/` — `webauthn.ts`, `api.ts`, `stores.ts`, `types.ts`, `Panel.svelte`, `Button.svelte`, `branding.ts` | Passkey browser plumbing + UI primitives |
| `web/src/views/` — `Login.svelte`, `Account.svelte`, `RegisterVerify.svelte`, `RecoverVerify.svelte` | Passkey UI, unchanged flows |
| `web/embed.go`, `vite.config.ts`, `svelte.config.js`, `tsconfig.json` | Embedded-SPA build |
| `deploy/systemd/`, `deploy/apache/`, `scripts/dev.sh`, `scripts/deploy.sh`, `scripts/db/` | Deployment |
| `migrations/000001` (users), `000004` (passkeys/challenges/pending), `000005` (sessions), `000006` (settings), `000007` (audit_log), `000009` (backup flags), `000013` (session NOT NULL) | Auth schema, renumbered `000001`–`000007` |
| `issues/` conventions, `docs/` structure, `Makefile`, `.gitattributes`, `.gitignore` | Working process |

### 3.2 Delete outright

`internal/links/`, `internal/clicks/`, `internal/campaigns/`, `internal/filters/`,
`internal/cache/` (redirect + rule caches — no hot redirect path here),
`internal/qr/`, `internal/handlers/redirect.go`, `links.go`, `campaigns*.go`,
`url_filters.go`, `qr.go`; all link/click/campaign/filter migrations; the entire
`web/src/views/Dashboard.svelte`, `LinkDetail.svelte`, `CampaignsList.svelte`,
`CampaignDetail.svelte`; `web/src/lib/links.ts`, `utm.ts`, `charts.ts`,
`campaigns.ts`, `linkDetail.ts`, `BatchCreateLinks.svelte`, all chart components,
all UTM components. Drop the `gozxing`, `go-qrcode`, and `ristretto` dependencies.

> ShortLinks' `campaigns` concept (grouping short links) and this project's
> `email_campaigns` concept (a message sent to a segment) share a word and
> nothing else. Do not port any of it.

### 3.3 Build new

`internal/subscribers/`, `internal/interests/`, `internal/mailing/` (campaign
composition, audience materialization, send worker), `internal/sesnotify/` (SNS
signature verification + bounce/complaint ingestion), `internal/inbound/`
(mailto: unsubscribe processing), `internal/workshops/`, `internal/seo/`
(server-injected meta tags, sitemap, robots), plus the public marketing SPA and
the admin console views.

### 3.4 Deviations from ShortLinks' architecture

Three deliberate departures, each driven by this being a **public marketing
site** rather than an authenticated tool:

1. **Real URL routing.** ShortLinks writes to a `currentView` store with no URL
   router — acceptable for a signed-in dashboard, wrong for pages that get
   shared and indexed. This project adds a small History API path router
   (§7.2). No routing library.
2. **Server-injected meta tags.** The Go SPA handler rewrites `<title>`,
   `<meta name="description">`, and Open Graph / Twitter Card tags into
   `index.html` per known public route before serving it (§7.4). Social
   crawlers do not run JavaScript; without this, every shared link previews
   identically and badly. This is the single most important technical detail
   for a site whose whole job is receiving social traffic.
3. **A background worker.** ShortLinks is request/response only. This service
   runs a send worker and a token-sweep ticker inside the same process (§6.6).

---

## 4. Brand and Visual Design

### 4.1 Direction

A **terminal/console aesthetic**: near-black background, bright electronics
green, monospace type, command-prompt and status-line motifs. It reads as
"electronics and code" without being a novelty theme, and it makes a small set
of assets go a long way — most of the visual interest comes from type and
color, not from illustration or photography.

The reference layout that establishes the language:

```
┌─────────────────────────────────────────────┐  ● ● ●   (terminal dots)
│  > open_circuit // san_francisco            │  ← green mono prompt line
│                                             │
│  LEARN TO SOLDER                            │  ← heavy uppercase display
│                                             │
│  [ OK ]  monthly · beginners                │  ← green mono status rows
│  [ OK ]  tools + kit provided               │
│  [ OK ]  zero experience needed             │
│                                             │
│  $ opencircuitsf.com▮                       │  ← command line + block cursor
└─────────────────────────────────────────────┘
```

### 4.2 Color tokens

**The site adapts to light and dark.** Light is the bare `:root` definition;
dark is restated twice — once under `prefers-color-scheme` guarded against an
explicit light override, and once under `[data-theme="dark"]` so an in-page
toggle wins in both directions:

```css
:root                                    { /* light tokens */ }
@media (prefers-color-scheme: dark) {
  :root:not([data-theme="light"])        { /* dark tokens */ }
}
:root[data-theme="dark"]                 { /* dark tokens, repeated */ }
```

`color-scheme: light dark` on `:root` and a matching `<meta name="color-scheme">`
let native controls and scrollbars follow. The chosen theme is read from
`localStorage` in a blocking inline script in `<head>` so the correct palette is
never repainted. The toggle cycles **auto → light → dark**; auto removes the
attribute entirely rather than pinning the current OS value.

**Designing light mode.** A terminal aesthetic has no automatic light
counterpart — inverting it produces a washed-out dark theme. Light mode is
instead designed as a *printed listing*: paper-white panels, near-black text,
and a deep green accent, with the same prompt, `[ OK ]` and cursor motifs intact.
The motifs, not the darkness, are what carry the identity.

| Token | Light | Dark | Use |
|---|---|---|---|
| `--bg-page` | `#F4F6F3` | `#0A0D0B` | Page background |
| `--bg-panel` | `#FFFFFF` | `#121712` | Cards, panels, terminal blocks |
| `--bg-subtle` | `#E9EEE6` | `#182018` | Input fills, table stripes |
| `--bg-header` | `#E1E8DE` | `#1B231C` | Panel header bars |
| `--border` | `#CFD8CA` | `#243028` | Default 1px borders |
| `--border-strong` | `#87977F` | `#4E6553` | Emphasized borders |
| `--text` | `#10140F` | `#E8F0E8` | Body copy |
| `--text-muted` | `#4C574A` | `#9AA79E` | Secondary text |
| `--text-faint` | `#5F6A5C` | `#7E8D82` | Timestamps, meta |
| `--accent` | `#2F7D0C` | `#68FF23` | Prompts, `[ OK ]`, links, primary fill |
| `--accent-hover` | `#245F09` | `#8AFF55` | Hover state |
| `--accent-dim` | `#79C34A` | `#3C9E0F` | Decorative rules in accent color |
| `--accent-text` | `#FFFFFF` | `#0A0D0B` | Text on an accent-filled surface |
| `--accent-subtle` | `#E4F1DB` | `#142B0C` | Accent-tinted backgrounds |
| `--warning` | `#8A5A00` | `#E5B22C` | Amber status |
| `--danger` | `#B4231B` | `#FF5F56` | Errors, destructive actions |

**Measured contrast** (WCAG 2.1 relative luminance, range across
`--bg-page` / `--bg-panel` / `--bg-subtle`):

| Foreground | Light | Dark |
|---|---|---|
| `--text` | 15.8–18.6 ✅ | 14.4–16.8 ✅ |
| `--text-muted` | 6.4–7.6 ✅ | 6.7–7.8 ✅ |
| `--text-faint` | 4.8–5.2 ✅ | 4.8–5.6 ✅ |
| `--accent` | 4.4–5.2 ✅ | 12.7–14.8 ✅ |
| `--accent-text` on `--accent` | 5.2 ✅ | 14.8 ✅ |

**Rules that are not negotiable:**

- **Body paragraphs use `--text`, never `--accent`.** Green is for prompts,
  `[ OK ]` rows, links, labels, and short status lines. Long runs of saturated
  green are tiring in dark mode and lose contrast in light mode.
- **Focus rings use `--accent`, not `--border-strong`.** `--border-strong`
  measures under 3:1 against the page in both themes — fine for a decorative
  edge, insufficient for a focus indicator. `--accent` clears 3:1 in both.
- `--accent-dim` is **decorative only** (rules, dividers, the `>` chevron). It
  is deliberately low-contrast and must never carry text or state.
- Never signal state by color alone.
- The blinking cursor and any scanline effect are disabled under
  `@media (prefers-reduced-motion: reduce)`.

The dark `--accent` is the logo green (§4.5), so the mark and the UI share one
colour. The light `--accent` is the same hue and saturation with the value
lowered until it clears 4.5:1 on white — the identical relationship the two logo
tints use.

A working reference implementation of this entire token set, the toggle, and the
motif components is the placeholder site in [`placeholder/`](placeholder/).

### 4.3 Typography

| Role | Family | Notes |
|---|---|---|
| Display / headlines | **Archivo** (variable, weight 800–900), uppercase, tight tracking | Falls back to `ui-sans-serif, system-ui, sans-serif` |
| Body | **Archivo** 400/500 | Same family keeps the stack small |
| Mono / UI chrome | **JetBrains Mono** 400/700 | Prompts, `[ OK ]` rows, code, tables, timestamps |

Fonts are **self-hosted woff2** in `web/public/fonts/`, not loaded from Google
Fonts. Rationale: no third-party request on a privacy-respecting site, no
external point of failure, and faster first paint. Subset to Latin.

### 4.4 Reusable motifs

Build these as Svelte components in `web/src/lib/` so the language stays
consistent and is not re-implemented per page:

| Component | Renders |
|---|---|
| `Prompt.svelte` | `> text` with `--accent-dim` chevron in mono |
| `StatusList.svelte` | Rows of `[ OK ] label` — takes a string array |
| `CommandLine.svelte` | `$ command▮` with a CSS-animated block cursor |
| `TerminalPanel.svelte` | Bordered panel with the three-dot title bar |
| `TraceDivider.svelte` | Inline SVG PCB-trace horizontal rule |

> **Correction (2026-08-18, from `#0013`'s review).** This table previously said
> the `Prompt` chevron uses `--accent`, contradicting §4.2, which states verbatim
> that `--accent-dim` is "decorative only (rules, dividers, the `>` chevron)" —
> naming this exact glyph. §4.2 is authoritative; this table was the defect.
> `#0013`'s acceptance criterion was written from the wrong row and has been
> amended. Note `--accent-dim` measures 1.84–2.16:1 in light mode, which is fine
> for decorative punctuation beside `--accent` prose but must never carry text.

### 4.5 Logo

The mark is the circular green PCB badge (`Logo 2026.png`): a command prompt at
the centre of a board populated with a microcontroller, an SBC, LEDs, a tactile
switch and a buzzer, ringed by `> open_circuit_sf` and wordmarked
`OPEN CIRCUIT_SF` over `$ hack · build · learn`. It shares the terminal language
of the site rather than sitting beside it, so the earlier blue hexagonal badge is
**retired** — there is no longer a green-UI / blue-mark split to reconcile.

Processed assets live in [`assets/logo/`](assets/logo/) with full derivation
notes in its `README.md`. The black ground has been removed by luminance keying,
so every asset is transparent and composites onto any surface without a fringe.

**Two greens, not one.** The brand green `#68FF23` measures 14.8:1 on the dark
page ground and **1.32:1 on white** — transparency alone does not make the logo
theme-adaptive, because on a light surface the bright green all but disappears.
Light contexts use `#30800C`: same hue, same saturation, value reduced until it
clears 4.5:1 on white.

| Context | Asset | Colour |
|---|---|---|
| Site header, footer, dark surfaces | `logo-green.png` / `logo-mask.png` | `#68FF23` |
| Light surfaces, print on white | `logo-light.png` / `logo-mask.png` | `#30800C` |
| Favicon, avatars, inline icons | `mark-*` (chip only) | either |
| iOS home screen | `apple-touch-icon.png` | opaque dark ground |

**The full logo is illegible below ~96 px** — traces, board outlines and the
`hack · build · learn` line collapse into noise. The central chip with the `>_`
prompt is extracted as a separate mark and reads cleanly from 32 px. Favicons and
avatars use the mark; the full logo is used at 128 px and above.

Preferred integration is the **alpha mask** (`logo-mask.png` / `mark-mask.png`)
driven by `background-color: currentColor`, so one asset follows both the OS
setting and any in-page theme toggle. A `<picture>` element with
`prefers-color-scheme` is the fallback where a real `alt` attribute matters more
than toggle support.

Remaining deliverable: a **vector trace**, tracked in issue #0018. These are
raster derivatives of a raster source; a traced SVG would give crisp rendering at
every size, a real `favicon.svg`, and `currentColor` tinting without the mask.

---

## 5. Information Architecture

### 5.1 Public routes

| Path | View | Purpose |
|---|---|---|
| `/` | Home | Hero terminal block, what Open Circuit SF is, next 3 workshops, inline subscribe form |
| `/workshops` | WorkshopsIndex | Upcoming (chronological) + past (reverse chronological) |
| `/workshops/{slug}` | WorkshopDetail | Full description, date/time, location, what to bring, subscribe CTA |
| `/about` | About | Who we are, philosophy, venues, Discord link, contact |
| `/subscribe` | Subscribe | Standalone signup with the full interest picker |
| `/subscribe/thanks` | ConfirmSent | "Check your email" — no PII in the URL |
| `/confirm` | ConfirmSubscription | Landing for the double opt-in link (`?token=`) |
| `/preferences` | PreferenceCenter | Token-authenticated interest management (`?token=`) |
| `/unsubscribe` | Unsubscribe | GET renders confirm; the actual action is a POST |
| `/login` | Login | Staff passkey sign-in |
| `/register/verify`, `/recover/verify` | RegisterVerify, RecoverVerify | Passkey magic-link landings |

### 5.2 Admin routes (session + `is_admin` required)

| Path | Purpose |
|---|---|
| `/admin` | Overview — list size, growth, recent campaigns, pending sends |
| `/admin/subscribers` | Search, filter by status/interest, detail drawer, manual add/suppress, CSV export |
| `/admin/interests` | CRUD the interest taxonomy, reorder, activate/deactivate |
| `/admin/campaigns` | List, compose, preview, test-send, target, schedule, send, per-campaign stats |
| `/admin/workshops` | CRUD workshops, publish/unpublish, "announce to list" shortcut |
| `/admin/users` | Staff user management (copied from ShortLinks) |
| `/admin/settings` | Registration toggle, send rate, from-addresses, physical mailing address |
| `/admin/audit` | Audit log (copied from ShortLinks) |
| `/account` | Own passkey management (copied from ShortLinks) |

---

## 6. Mailing List — The Core Subsystem

### 6.1 Interest taxonomy

Interests are **rows in a table, not a Go enum**. New workshop themes appear
constantly; adding one must not require a deploy. Seed list:

| Slug | Name |
|---|---|
| `microcontrollers` | Microcontrollers (ESP32, Arduino, RP2040) |
| `soldering` | Soldering & Assembly |
| `homelab` | Homelab & Self-Hosting |
| `home-automation` | Home Automation |
| `pcb-design` | PCB Design & Fabrication |
| `sensors-iot` | Sensors & IoT |
| `robotics` | Robotics & Motion |
| `radio-rf` | Radio & RF |
| `retro-computing` | Retro Computing & Repair |
| `3d-printing` | 3D Printing & Enclosures |
| `test-equipment` | Test Equipment & Measurement |
| `beginner` | Absolute Beginner Sessions |

Subscribers with **zero** interests selected receive only general
announcements — this is a valid and expected state, not an error.

### 6.2 Database schema

```sql
-- Subscribers: one row per email address, independent of any user account.
CREATE TABLE subscribers (
    id             BIGSERIAL PRIMARY KEY,
    email          TEXT UNIQUE NOT NULL,          -- stored lowercased, normalized
    status         TEXT NOT NULL DEFAULT 'pending',
                   -- pending | active | unsubscribed | bounced | complained
    confirm_token  TEXT UNIQUE,                   -- NULL once confirmed
    confirm_sent_at   TIMESTAMPTZ,
    confirm_expires_at TIMESTAMPTZ,
    confirmed_at   TIMESTAMPTZ,
    manage_token   TEXT UNIQUE NOT NULL,          -- long-lived preference/unsub token
    signup_ip      INET,                          -- consent evidence
    signup_user_agent TEXT,
    utm_source     TEXT,
    utm_medium     TEXT,
    utm_campaign   TEXT,
    unsubscribed_at TIMESTAMPTZ,
    unsubscribe_source TEXT,                      -- one_click | preferences | mailto | admin
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_subscribers_status ON subscribers (status);
CREATE INDEX idx_subscribers_created_at ON subscribers (created_at DESC);
CREATE INDEX idx_subscribers_confirm_expires ON subscribers (confirm_expires_at)
    WHERE confirm_token IS NOT NULL;

CREATE TABLE interests (
    id          BIGSERIAL PRIMARY KEY,
    slug        TEXT UNIQUE NOT NULL,
    name        TEXT NOT NULL,
    description TEXT,
    sort_order  INT NOT NULL DEFAULT 0,
    active      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE subscriber_interests (
    subscriber_id BIGINT NOT NULL REFERENCES subscribers(id) ON DELETE CASCADE,
    interest_id   BIGINT NOT NULL REFERENCES interests(id) ON DELETE CASCADE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (subscriber_id, interest_id)
);
CREATE INDEX idx_subscriber_interests_interest ON subscriber_interests (interest_id);

-- Global suppression list. Checked before EVERY send, regardless of
-- subscriber status. Survives resubscribe attempts.
CREATE TABLE suppressions (
    email      TEXT PRIMARY KEY,
    reason     TEXT NOT NULL,   -- hard_bounce | complaint | manual | repeated_soft_bounce
    note       TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE email_campaigns (
    id             BIGSERIAL PRIMARY KEY,
    name           TEXT NOT NULL,           -- internal label
    subject        TEXT NOT NULL,
    preheader      TEXT,
    body_md        TEXT NOT NULL,           -- authored source
    status         TEXT NOT NULL DEFAULT 'draft',
                   -- draft | scheduled | sending | sent | canceled | failed
    audience_mode  TEXT NOT NULL DEFAULT 'any_of',  -- all | any_of | all_of | none_selected
    workshop_id    BIGINT REFERENCES workshops(id), -- optional: announcement source
    scheduled_at   TIMESTAMPTZ,
    started_at     TIMESTAMPTZ,
    completed_at   TIMESTAMPTZ,
    created_by     BIGINT REFERENCES users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE campaign_interests (
    campaign_id BIGINT NOT NULL REFERENCES email_campaigns(id) ON DELETE CASCADE,
    interest_id BIGINT NOT NULL REFERENCES interests(id) ON DELETE CASCADE,
    PRIMARY KEY (campaign_id, interest_id)
);

-- One row per (campaign, subscriber). Materialized when a send starts.
-- The UNIQUE key is the idempotency guarantee: a crashed and restarted send
-- can never double-deliver.
CREATE TABLE email_sends (
    id             BIGSERIAL PRIMARY KEY,
    campaign_id    BIGINT NOT NULL REFERENCES email_campaigns(id) ON DELETE CASCADE,
    subscriber_id  BIGINT NOT NULL REFERENCES subscribers(id) ON DELETE CASCADE,
    email          TEXT NOT NULL,          -- snapshot at send time
    status         TEXT NOT NULL DEFAULT 'queued',
                   -- queued | sent | failed | bounced | complained | skipped
    ses_message_id TEXT,
    attempts       INT NOT NULL DEFAULT 0,
    error          TEXT,
    sent_at        TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (campaign_id, subscriber_id)
);
CREATE INDEX idx_email_sends_queued ON email_sends (campaign_id, id)
    WHERE status = 'queued';
CREATE INDEX idx_email_sends_message_id ON email_sends (ses_message_id);

-- Raw SES/SNS notifications, kept for forensics. Append-only.
CREATE TABLE email_events (
    id             BIGSERIAL PRIMARY KEY,
    ses_message_id TEXT,
    event_type     TEXT NOT NULL,   -- Bounce | Complaint | Delivery | Reject | ...
    bounce_type    TEXT,            -- Permanent | Transient | Undetermined
    recipient      TEXT,
    payload        JSONB NOT NULL,
    received_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_email_events_message_id ON email_events (ses_message_id);
CREATE INDEX idx_email_events_recipient ON email_events (recipient);

CREATE TABLE workshops (
    id               BIGSERIAL PRIMARY KEY,
    slug             TEXT UNIQUE NOT NULL,
    title            TEXT NOT NULL,
    summary          TEXT,                  -- used in meta description + list cards
    body_md          TEXT,
    starts_at        TIMESTAMPTZ,
    ends_at          TIMESTAMPTZ,
    location_name    TEXT,
    location_address TEXT,
    location_note    TEXT,                  -- "exact address emailed to attendees"
    capacity         INT,                   -- display only in v1
    signup_url       TEXT,                  -- external RSVP link if used
    cover_image      TEXT,                  -- path under /assets
    status           TEXT NOT NULL DEFAULT 'draft',  -- draft | published | canceled
    published_at     TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_workshops_published ON workshops (starts_at DESC)
    WHERE status = 'published';

CREATE TABLE workshop_interests (
    workshop_id BIGINT NOT NULL REFERENCES workshops(id) ON DELETE CASCADE,
    interest_id BIGINT NOT NULL REFERENCES interests(id) ON DELETE CASCADE,
    PRIMARY KEY (workshop_id, interest_id)
);
```

Plus the copied auth tables: `users`, `passkey_credentials`,
`webauthn_challenges`, `pending_registrations`, `sessions`, `settings`,
`audit_log`.

> **Migration ordering.** The schema above is grouped by topic for readability,
> not by dependency. `email_campaigns.workshop_id` references `workshops`, so
> the `workshops` migration must be numbered ahead of the campaigns migration —
> or the FK added in a later `ALTER TABLE`. Phase 5 lands before Phase 6, so
> prefer the `ALTER TABLE` approach and keep the phases independently
> deployable.

**Email normalization.** Store `lower(trim(email))`. Do **not** strip Gmail dots
or `+tag` suffixes — they are distinct addresses per RFC and users legitimately
use them to segment their own mail.

### 6.3 Subscription flow (double opt-in)

```
Visitor lands on opencircuitsf.com/?utm_source=instagram&utm_campaign=solder-oct
    │
    ├─ SPA stores utm_* in sessionStorage on first paint
    │
    ▼ POST /api/subscribe  { email, interests: [slug,...], website: "" }
    │
    ├─ Rate limit: per-IP token bucket (5/min, burst 3)
    ├─ Honeypot: `website` field must be empty; a filled value returns 202 and
    │  silently discards. Bots fill every input.
    ├─ Timing gate: form must have been on screen ≥ 2s (client-sent nonce
    │  timestamp, server-validated). Optional; cheap and effective.
    ├─ Validate email syntax + reject disposable-domain list
    │
    ├─ Suppressed? → return 202, send nothing. Never tell the caller.
    │
    ├─ Existing subscriber?
    │   ├─ status=active      → send "you're already subscribed" email
    │   │                        containing the preference-center link
    │   ├─ status=pending     → resend confirmation (rate-limited to 1/hour)
    │   └─ status=unsubscribed→ treat as new signup; fresh confirm token
    │
    ├─ New: INSERT subscribers (status='pending', confirm_token=<32 random bytes>,
    │        confirm_expires_at=now()+7d, manage_token=<32 random bytes>,
    │        signup_ip, signup_user_agent, utm_*)
    │        + subscriber_interests rows
    │
    ├─ Send confirmation email via SES
    │
    └─ 202 Accepted  { message: "Check your email to confirm." }
       ← ALWAYS the same response shape, for every branch above.
```

**Why the uniform 202:** varying the response by whether an address is already
on the list turns the endpoint into an email-enumeration oracle. Every branch
returns the same body; the differentiation happens in the inbox, where only the
address owner sees it.

**Confirmation:**

```
GET /confirm?token=…            → SPA view, no side effect (prefetch-safe)
POST /api/subscribe/confirm     → { token }
    ├─ Token unknown/expired    → 400, offer to re-subscribe
    ├─ Valid → status='active', confirmed_at=now(), confirm_token=NULL
    ├─ audit.Record('subscriber.confirmed')
    └─ 200 → SPA shows the preference center inline, already authenticated
             by the returned manage_token
```

Consent evidence retained for every active subscriber: signup IP, signup user
agent, signup timestamp, confirmation timestamp. That is the record you produce
if a deliverability complaint ever needs answering.

### 6.4 Preference center

`GET /preferences?token=<manage_token>` → `GET /api/preferences?token=…` returns
the subscriber's email (masked as `b•••••n@gmail.com` unless the token is fresh
from a confirm), current interests, and the full active interest list.

`PATCH /api/preferences` `{ token, interests: [slug,...] }` replaces the
interest set. Setting an empty array keeps the subscriber **active** on general
announcements only — the UI states this explicitly, because "uncheck everything"
is a common way people try to unsubscribe without meaning to fully leave. The
page offers an explicit "Unsubscribe from everything" action alongside.

`manage_token` is a 32-byte random value, not an HMAC of the email. Random
tokens are revocable (rotate the column) and leak nothing if a URL ends up in a
referrer header or a screenshot.

### 6.5 Unsubscribe — three paths, all required

This is the part that most implementations get wrong, and getting it wrong is
what lands a domain in spam folders. Gmail and Yahoo bulk-sender requirements
(effective 2024) make paths 1 and 2 mandatory in practice.

#### Path 1 — One-click unsubscribe (RFC 8058)

Every campaign email carries:

```
List-Unsubscribe: <https://opencircuitsf.com/api/unsubscribe?token=MANAGE_TOKEN>,
                  <mailto:unsubscribe@lists.opencircuitsf.com?subject=unsubscribe:MANAGE_TOKEN>
List-Unsubscribe-Post: List-Unsubscribe=One-Click
List-Id: Open Circuit SF <list.opencircuitsf.com>
```

Handler requirements:

- `POST /api/unsubscribe` accepts the request with **no session and no CSRF
  token** — the mail provider posts it, not the user's browser. It accepts a
  body of `List-Unsubscribe=One-Click` and ignores it.
- Must respond `200` quickly and unsubscribe **synchronously** (the spec allows
  2 days; do it immediately — there is no reason not to).
- `GET /api/unsubscribe` must **never** unsubscribe. It redirects to the
  `/unsubscribe` confirmation page. Mail clients and security scanners prefetch
  every GET URL in a message; a GET that mutates state will unsubscribe people
  who never clicked anything.
- Unknown or already-used token → still return `200` with a neutral "you are
  unsubscribed" page. Never 404; a provider seeing errors on the unsubscribe
  endpoint downgrades sender reputation.

#### Path 2 — In-body link → preference center

The footer of every email contains both:

> Manage your interests · Unsubscribe from everything

Granular management measurably reduces full unsubscribes: someone tired of
homelab posts drops one interest instead of the whole list.

#### Path 3 — Inbound `mailto:` unsubscribe

Some clients (and some people) reply or send to the `mailto:` address. Handling
it automatically is the piece the ShortLinks codebase has no precedent for.

```
Sender → unsubscribe@lists.opencircuitsf.com
    │
    ▼ MX for lists.opencircuitsf.com → inbound-smtp.us-west-2.amazonaws.com
    │
    ▼ SES Receipt Rule Set  "opencircuit-inbound"
    │   ├─ Action 1: S3  → s3://opencircuitsf-inbound/unsubscribe/{messageId}
    │   └─ Action 2: SNS → topic  opencircuit-inbound-mail  (notification only)
    │
    ▼ SNS HTTPS subscription → POST /api/ses/inbound   (signature-verified)
    │
    ▼ internal/inbound: fetch the object from S3, parse with net/mail
    │   ├─ Extract token from Subject: "unsubscribe:<token>"      (preferred)
    │   ├─ else match the From: address against subscribers.email  (fallback)
    │   └─ neither → log, leave the object for manual review, 200 OK
    │
    ▼ Unsubscribe, audit.Record('subscriber.unsubscribed', source='mailto')
    │
    └─ S3 lifecycle rule deletes objects after 30 days
```

**Critical DNS detail.** SES inbound email requires the receiving domain's MX to
point at SES. Do **not** point the MX of `opencircuitsf.com` itself at SES —
that would hijack all mail to the apex domain, including any human mailboxes.
Use a dedicated subdomain, `lists.opencircuitsf.com`, with its own MX record.
The apex domain's MX stays with whatever provider serves human mail.

**SES inbound is region-limited** — available in `us-east-1`, `us-west-2`,
`eu-west-1` and a few others. Pick the SES region for the whole project with
inbound availability in mind (§10.3).

**Simpler fallback if inbound proves fiddly:** point the `mailto:` at a real
monitored mailbox and process unsubscribes manually. Volume at launch is low
enough that this is honest and workable. Path 3 is the only one of the three
that is optional in v1 — ship paths 1 and 2 first.

#### Unsubscribe state machine

| From | Event | To | Side effect |
|---|---|---|---|
| `active` | one-click / preferences / mailto | `unsubscribed` | `unsubscribed_at`, `unsubscribe_source`, rotate `manage_token` |
| `active` | SES hard bounce | `bounced` | Insert into `suppressions` |
| `active` | SES complaint | `complained` | Insert into `suppressions`, **never auto-resubscribe** |
| `active` | 5 soft bounces in 30 days | `bounced` | Insert into `suppressions` |
| `unsubscribed` | new signup | `pending` | Fresh confirm token; requires re-confirmation |
| `complained` | new signup | *(no change)* | Return 202, send nothing. Only an admin can clear a complaint. |

### 6.6 Sending engine

**Transport: AWS SES v2 API** (`github.com/aws/aws-sdk-go-v2/service/sesv2`),
not SMTP. ShortLinks uses SES SMTP with static IAM credentials in the config
file; for bulk sending, prefer the API because it:

- authenticates via the **EC2 instance IAM role** — no long-lived SMTP password
  sitting in `/etc/opencircuit/config.env`;
- returns the `MessageId` on every send, which is the join key for bounce and
  complaint events;
- accepts a **configuration set** per message, which is how event publishing to
  SNS is enabled.

Keep ShortLinks' `Mailer` interface seam so tests inject a recorder:

```go
type Mailer interface {
    Send(ctx context.Context, msg Message) (messageID string, err error)
}
```

**Send worker** (`internal/mailing/worker.go`) — one goroutine, started by
`serve`, shut down on `SIGTERM`:

```
loop every 2s:
  ├─ claim next campaign WHERE status='sending'
  │  (or status='scheduled' AND scheduled_at <= now() → transition to 'sending')
  │
  ├─ on first claim: materialize the audience
  │     INSERT INTO email_sends (campaign_id, subscriber_id, email)
  │     SELECT $1, s.id, s.email FROM subscribers s
  │      WHERE s.status = 'active'
  │        AND NOT EXISTS (SELECT 1 FROM suppressions x WHERE x.email = s.email)
  │        AND <audience predicate>
  │     ON CONFLICT (campaign_id, subscriber_id) DO NOTHING
  │
  ├─ fetch a batch of queued rows (LIMIT 50, FOR UPDATE SKIP LOCKED)
  ├─ for each: render per-recipient body + headers, send via SES,
  │            UPDATE status='sent', ses_message_id, sent_at
  │            on error: attempts++, status='failed' after 3 attempts
  ├─ throttle to SETTINGS.max_send_rate (default 10/s, below the SES quota)
  │  with exponential backoff on ThrottlingException
  └─ no queued rows remain → campaign status='sent', completed_at=now()
```

**Audience predicates** by `audience_mode`:

| Mode | SQL predicate |
|---|---|
| `all` | *(none — every active subscriber)* |
| `any_of` | `EXISTS (SELECT 1 FROM subscriber_interests si WHERE si.subscriber_id=s.id AND si.interest_id = ANY($2))` |
| `all_of` | `(SELECT count(*) FROM subscriber_interests si WHERE si.subscriber_id=s.id AND si.interest_id = ANY($2)) = cardinality($2)` |
| `none_selected` | `NOT EXISTS (SELECT 1 FROM subscriber_interests si WHERE si.subscriber_id=s.id)` |

**Message composition:**

- Author in Markdown; render to HTML **and** a plain-text alternative. Send
  `multipart/alternative`. A text part is not optional — text-only parts
  measurably improve inbox placement and some recipients read mail that way.
- HTML uses inline styles and a single-column table layout. Email clients are
  not browsers; no CSS custom properties, no flexbox, no `<style>` reliance.
  The terminal aesthetic translates as: dark background, mono headers, green
  accents — with a `background-color` on the outer table because Outlook
  ignores `body` backgrounds.
- Every send substitutes the recipient's `manage_token` into the footer links
  and the `List-Unsubscribe` headers.
- **Every email includes the physical mailing address** — CAN-SPAM §7704
  requires it and it is a hard legal requirement, not a nicety. Store it in
  `settings` under `physical_address`; the send worker **refuses to start a
  campaign** if it is empty.
- `Reply-To` is a monitored human address, not `noreply@`.

**Pre-send safety checks** — a campaign cannot leave `draft` unless:
subject is non-empty, body renders, at least one recipient matches the
audience, `physical_address` is set, a test send has been delivered to an
admin, and the sender confirms the recipient count in a typed confirmation.

### 6.7 SES event ingestion

```
SES Configuration Set "opencircuit-transactional"
  → Event destination (Bounce, Complaint, Delivery, Reject, RenderingFailure)
  → SNS topic "opencircuit-ses-events"
  → HTTPS subscription → POST /api/ses/notifications
```

Handler (`internal/sesnotify`) requirements:

1. **Verify the SNS signature on every message.** Validate `SigningCertURL`
   points at an `amazonaws.com` host over HTTPS before fetching it, build the
   canonical string per SNS's documented field order, verify with the cert's
   public key. An unverified endpoint is an open door for anyone to forge
   bounce events and mass-suppress the list.
2. Handle `SubscriptionConfirmation` by fetching `SubscribeURL` once, then log
   it. Handle `UnsubscribeConfirmation` by alerting, never by acting.
3. Insert the raw payload into `email_events` before interpreting it.
4. Apply the state machine from §6.5. Match `Bounce.bounceType == "Permanent"`
   as a hard bounce; `Transient` counts toward the soft-bounce threshold.
5. Return `200` even for payloads that cannot be interpreted — SNS retries
   aggressively on non-2xx and a parse bug should not become a retry storm.
   Log and move on.
6. Reconcile events to `email_sends` by `ses_message_id`; an event with no
   matching send row is still recorded and still triggers suppression.

Also enable the **SES account-level suppression list** as a second layer. Belt
and suspenders: our list is authoritative for our sending decisions, SES's
protects the account reputation if ours ever has a bug.

---

## 7. Frontend

### 7.1 Stack

Unchanged from ShortLinks: Svelte 5 + Vite 6 + TypeScript, built to `web/dist/`,
embedded into the Go binary with `//go:embed all:dist`. Vitest for unit tests on
the pure-TypeScript helper modules. No component framework, no CSS framework, no
runtime dependencies beyond Svelte.

### 7.2 Routing

Add `web/src/lib/router.ts` — roughly 60 lines, no library:

- Parse `window.location.pathname` into a `Route` union on load.
- Intercept clicks on same-origin `<a>` elements, `pushState`, update a
  `currentRoute` store.
- Handle `popstate`.
- Support one path parameter (`/workshops/:slug`).
- Fall through to a `NotFound` view.

This replaces ShortLinks' `currentView` store as the navigation mechanism. The
store stays for transient UI state.

### 7.3 Public views

| View | Notes |
|---|---|
| `Home.svelte` | Hero `TerminalPanel` with `Prompt` + display headline + `StatusList` + `CommandLine`; "next up" workshop cards; inline `SubscribeForm` |
| `WorkshopsIndex.svelte` | Two sections, upcoming and past |
| `WorkshopDetail.svelte` | Rendered Markdown body, date/location block, subscribe CTA scoped to that workshop's interests (pre-checks them) |
| `About.svelte` | Static content, Discord link |
| `SubscribeForm.svelte` | Email field + interest checkbox grid + honeypot; used inline and standalone |
| `ConfirmSubscription.svelte` | Reads `?token=`, POSTs it, rolls straight into the preference center on success |
| `PreferenceCenter.svelte` | Interest toggles + explicit unsubscribe action |
| `Unsubscribe.svelte` | Confirmation page; POSTs the action |

### 7.4 SEO and social preview cards

The site's entire purpose is to receive social traffic, so link previews have to
be right. Crawlers for Discord, Slack, iMessage, X, Facebook, and LinkedIn do
not execute JavaScript — a plain SPA gives every URL the same generic card.

`internal/seo` handles this in the SPA handler:

1. Hold the built `index.html` in memory with placeholder markers for
   `<title>`, `<meta name="description">`, `og:title`, `og:description`,
   `og:image`, `og:url`, `og:type`, and `twitter:card`.
2. On each SPA request, match the path against a small route table. For
   `/workshops/{slug}`, look up the workshop and use its title, summary, and
   cover image. For static routes, use a compiled-in table.
3. Substitute and serve. HTML-escape every substituted value.
4. Cache the rendered `index.html` per path with a short TTL; invalidate on
   workshop mutation.

Also serve: `GET /sitemap.xml` (generated from published workshops + static
routes), `GET /robots.txt`, `GET /favicon.svg`, and JSON-LD `Event` structured
data inside each workshop detail page's server-injected `<head>` — that is what
makes workshops eligible for rich results in search.

### 7.5 Accessibility

- Semantic landmarks (`<header>`, `<nav>`, `<main>`, `<footer>`), one `<h1>`
  per page.
- The `[ OK ]` decorations are `aria-hidden`; the label text carries the meaning.
- Interest checkboxes are real `<input type="checkbox">` in a `<fieldset>` with
  a `<legend>`, never div-based fakes.
- Form errors are announced via `aria-live="polite"` and associated with their
  field via `aria-describedby`.
- All motion behind `prefers-reduced-motion`.
- Target: keyboard-navigable end to end; verified with VoiceOver on the signup
  and preference flows.

### 7.6 Performance budget

| Metric | Budget |
|---|---|
| JS (gzipped, initial route) | ≤ 60 KB |
| CSS (gzipped) | ≤ 15 KB |
| Fonts | ≤ 120 KB total, 2 families, `font-display: swap` |
| LCP on 4G | < 2.0 s |
| External requests | **0** |

---

## 8. HTTP API

### Public — unauthenticated

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/interests` | Active interest list for the signup form |
| `POST` | `/api/subscribe` | Start double opt-in. Rate-limited. Always 202. |
| `POST` | `/api/subscribe/confirm` | Confirm with token → active |
| `GET` | `/api/preferences?token=` | Read current interests |
| `PATCH` | `/api/preferences` | Replace interest set |
| `POST` | `/api/unsubscribe` | One-click (RFC 8058) and in-page unsubscribe |
| `GET` | `/api/unsubscribe` | **Never mutates.** 302 → `/unsubscribe` |
| `GET` | `/api/workshops` | Published workshops |
| `GET` | `/api/workshops/{slug}` | One published workshop |
| `POST` | `/api/ses/notifications` | SNS bounce/complaint webhook (signature-verified) |
| `POST` | `/api/ses/inbound` | SNS inbound-mail webhook (signature-verified) |
| `GET` | `/healthz` | Liveness |

### Authenticated (copied from ShortLinks)

All of `/auth/*`, `/account/credentials*`, `/admin/users*`, `/admin/settings`,
`/admin/audit` carry over unchanged — see ShortLinks' auth route table.

### Admin — session + `is_admin`

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/admin/subscribers` | Paginated, filterable by status and interest |
| `GET` | `/admin/subscribers/{id}` | Detail incl. consent evidence and event history |
| `POST` | `/admin/subscribers/{id}/suppress` | Manual suppression with a note |
| `DELETE` | `/admin/subscribers/{id}` | Hard delete (GDPR erasure request) |
| `GET` | `/admin/subscribers/export` | CSV export |
| `GET`/`POST`/`PATCH`/`DELETE` | `/admin/interests[/{id}]` | Taxonomy CRUD |
| `GET`/`POST`/`PATCH` | `/admin/campaigns[/{id}]` | Campaign CRUD |
| `POST` | `/admin/campaigns/{id}/preview` | Render HTML + text without sending |
| `POST` | `/admin/campaigns/{id}/test` | Send to one admin address |
| `GET` | `/admin/campaigns/{id}/audience` | Recipient count + sample, before committing |
| `POST` | `/admin/campaigns/{id}/send` | Requires typed confirmation of the count |
| `POST` | `/admin/campaigns/{id}/cancel` | Stop an in-flight send |
| `GET` | `/admin/campaigns/{id}/stats` | Sent / failed / bounced / complained |
| `GET`/`POST`/`PATCH`/`DELETE` | `/admin/workshops[/{id}]` | Workshop CRUD |
| `POST` | `/admin/workshops/{id}/announce` | Create a draft campaign pre-filled from the workshop |
| `GET` | `/api/events` | SSE — live send progress on the campaign screen |

---

## 9. Configuration

```env
# ── Server ─────────────────────────────────────────────────────────────────
PORT=8080
BASE_URL=https://opencircuitsf.com

# ── Database ───────────────────────────────────────────────────────────────
DATABASE_URL=postgres://opencircuit:CHANGE_ME@localhost:5432/opencircuit?sslmode=disable

# ── WebAuthn ───────────────────────────────────────────────────────────────
# RP ID is the apex so a passkey stays valid across apex and www.
# ORIGIN must be the host the browser is actually on. As of the 2026-08-18
# placeholder deploy the canonical host is www (apex 301s to www), so the
# origin is the www form. These two are NOT interchangeable — a mismatch
# between RP_ORIGIN and the browser's real origin fails the ceremony with an
# opaque error.
WEBAUTHN_RP_ID=opencircuitsf.com
WEBAUTHN_RP_ORIGIN=https://www.opencircuitsf.com

# ── Sessions ───────────────────────────────────────────────────────────────
SESSION_SECRET=  # openssl rand -hex 32

# ── AWS / SES ──────────────────────────────────────────────────────────────
AWS_REGION=us-west-2
SES_CONFIGURATION_SET=opencircuit-transactional
EMAIL_FROM=Open Circuit SF <hello@opencircuitsf.com>
EMAIL_REPLY_TO=hello@opencircuitsf.com
EMAIL_LIST_DOMAIN=lists.opencircuitsf.com
SES_INBOUND_BUCKET=opencircuitsf-inbound
# No static credentials — the EC2 instance role provides them.

# ── Sending ────────────────────────────────────────────────────────────────
MAX_SEND_RATE=10          # messages/second, keep below the SES quota
SEND_BATCH_SIZE=50
SEND_WORKER_ENABLED=true  # false on a second instance to avoid double-sending

# ── Bootstrap ──────────────────────────────────────────────────────────────
ADMIN_EMAIL=offwhite@gmail.com
```

Runtime-editable values live in the `settings` table, not the environment:
`registrations_enabled`, `physical_address`, `max_send_rate`,
`signup_enabled`, `default_from_name`.

---

## 10. Infrastructure and Deployment

### 10.1 Topology

```
                       Route 53  (opencircuitsf.com)
                            │
                  ┌─────────┴──────────┐
                  │                    │
              A: apex/www         A: go.
                  │                    │
                  ▼                    ▼
        ┌──────────────────────────────────────┐
        │  EC2 (t4g.small, Amazon Linux 2023)  │
        │  ┌────────────────────────────────┐  │
        │  │ Apache 2 — TLS (Let's Encrypt) │  │
        │  │  vhost opencircuitsf.com →8080 │  │
        │  │  vhost go.opencircuitsf.com    │  │
        │  │                          →8081 │  │
        │  └───────┬──────────────┬─────────┘  │
        │          ▼              ▼            │
        │   opencircuit     shortlinks         │
        │   (systemd)       (systemd)          │
        │          │              │            │
        │          ▼              ▼            │
        │   PostgreSQL: opencircuit, shortlinks│
        └──────────────────────────────────────┘
                  │                    ▲
                  ▼ SES v2 API         │ SNS HTTPS
             AWS SES ─────── events ───┘
                  │
                  ▼ inbound (lists.opencircuitsf.com MX)
             S3 + SNS
```

Two services on one instance keeps cost at one box. They share nothing but the
host: separate databases, separate config files, separate systemd units,
separate service accounts. `SEND_WORKER_ENABLED` exists so that if the site is
ever scaled to two instances, exactly one runs the send worker.

### 10.2 DNS records (Route 53)

| Name | Type | Value | Purpose |
|---|---|---|---|
| `www.opencircuitsf.com` | A | EC2 Elastic IP | **Canonical host** — serves the site |
| `opencircuitsf.com` | A | EC2 Elastic IP | 301 redirects to `www` |
| `go.opencircuitsf.com` | A | EC2 Elastic IP | ShortLinks |
| `<sel1..3>._domainkey` | CNAME | *(from SES)* | DKIM |
| `mail.opencircuitsf.com` | MX | `10 feedback-smtp.us-west-2.amazonses.com` | Custom MAIL FROM |
| `mail.opencircuitsf.com` | TXT | `v=spf1 include:amazonses.com ~all` | SPF alignment |
| `lists.opencircuitsf.com` | MX | `10 inbound-smtp.us-west-2.amazonaws.com` | **Inbound unsubscribe only** |
| `_dmarc.opencircuitsf.com` | TXT | `v=DMARC1; p=quarantine; adkim=s; aspf=s; rua=mailto:…; fo=1` | DMARC |

Start DMARC at `p=none` for two weeks, read the aggregate reports, then move to
`p=quarantine`, then `p=reject` once clean.

### 10.3 SES region choice

Pick **`us-west-2`**: closest to San Francisco, and one of the regions that
supports SES **inbound** email receiving (needed for §6.5 path 3). Verify the
inbound-region list before committing — it is shorter than the sending-region
list, and the whole project should sit in one region.

### 10.4 SES production access

New accounts are sandboxed: 200 messages/day, verified recipients only. Request
production access early — approval takes ~24 hours and everything downstream is
blocked on it. Describe the use case honestly: opt-in announcement email for a
community electronics workshop group, double opt-in, one-click unsubscribe,
bounce and complaint handling wired to suppression.

### 10.5 IAM

Instance role policy, scoped tightly:

- `ses:SendEmail`, `ses:SendRawEmail` restricted to the verified identity ARN
  and the configuration set.
- `s3:GetObject`, `s3:DeleteObject` on `arn:aws:s3:::opencircuitsf-inbound/*`.
- Nothing else. No `ses:*`, no wildcard resources.

### 10.6 Backups

`pg_dump` nightly to S3 with a 30-day lifecycle, via the same script pattern
ShortLinks uses. The subscriber list is the single most valuable and least
reconstructible asset in the system — verify a restore before launch, not after
the first incident.

---

## 11. Security and Compliance

### Security

| Concern | Control |
|---|---|
| Email enumeration | Uniform 202 from `/api/subscribe` regardless of branch |
| Signup abuse | Per-IP token bucket, honeypot field, form-timing gate |
| Token guessing | 32 bytes from `crypto/rand`, base64url; constant-time compare |
| Token leakage | `manage_token` rotates on unsubscribe; `Referrer-Policy: strict-origin-when-cross-origin` |
| Forged bounce events | Full SNS signature verification; reject unverified payloads |
| SSRF via `SigningCertURL` | Host allowlist (`*.amazonaws.com`) + HTTPS-only before fetch |
| Session theft | `HttpOnly; Secure; SameSite=Strict`, 30-day sliding expiry |
| Passwords | None exist — passkeys only |
| Admin blast radius | Send requires typed recipient-count confirmation + prior test send |
| Headers | HSTS, `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, and a CSP with no `unsafe-inline` (Vite emits hashable assets) |

### Compliance

- **CAN-SPAM** — accurate `From`, non-deceptive subjects, physical mailing
  address in every message, working unsubscribe honored within 10 days (we do
  it immediately). A PO box is acceptable and preferable to a home address;
  **this needs to exist before the first campaign sends.**
- **RFC 8058 / Gmail–Yahoo bulk sender rules** — one-click unsubscribe headers,
  authenticated domain (SPF + DKIM + DMARC), complaint rate under 0.3%.
- **GDPR / CCPA posture** — consent is explicit and evidenced (double opt-in
  with IP + timestamps). Support export and erasure by request; erasure is a
  hard delete plus a permanent suppression entry so the address is not
  re-added.
- **Privacy policy** page covering what is collected (email, interests, signup
  IP, UTM source), why, retention, and how to leave. Link it in the footer and
  next to the signup button.

---

## 12. Phased Build Plan

Each phase ends at a state worth deploying.

### Phase 0 — Foundation
Copy the ShortLinks skeleton, rewrite the module path, strip every shortener
package and migration, get `go build ./...` and `go test ./...` green on the
reduced tree, stand up `scripts/dev.sh`, write `CLAUDE.md` and `docs/README.md`.

### Phase 1 — Auth carried over
Auth migrations renumbered and applied; passkey registration, login, recovery,
and credential management working end to end on `localhost`; admin users,
settings, and audit log screens live. **Deployable:** an empty site with a
working staff login.

### Phase 2 — Brand and marketing pages
Design tokens, fonts, terminal motif components, the History API router, Home /
About / 404, server-injected meta tags, sitemap, robots, favicons, OG image.
**Deployable:** the real public site, minus signup. This is the point at which
social links can start pointing at it.

### Phase 3 — Mailing list capture
Interests table and admin CRUD, subscribe endpoint with the full anti-abuse
stack, confirmation email over SES, confirm and preference-center flows,
subscriber admin screens. **Deployable:** signups start accumulating — the
sooner this ships, the sooner the list grows.

### Phase 4 — Unsubscribe and list hygiene
One-click unsubscribe with RFC 8058 headers, preference-center unsubscribe,
SNS bounce/complaint ingestion with signature verification, suppression list,
soft-bounce thresholds. **Required before any bulk send.**

### Phase 5 — Campaigns
Campaign CRUD, Markdown → HTML + text rendering, audience targeting and
preview, test send, send worker with idempotency and throttling, live progress
over SSE, per-campaign stats. **Deployable:** the first real announcement.

### Phase 6 — Workshops
Workshop CRUD, public index and detail pages, JSON-LD `Event` markup,
workshop-derived meta tags, "announce to list" shortcut.

### Phase 7 — Inbound and polish
SES inbound `mailto:` unsubscribe processing, CSV export, GDPR erasure,
admin overview dashboard, backup verification, accessibility audit,
`p=reject` DMARC.

### Later / candidates
RSVP with capacity and waitlist; per-campaign click tracking through
`go.opencircuitsf.com`; a photos/recap section; ICS calendar feed for
workshops; Discord webhook cross-posting.

---

## 13. Issue Breakdown

Filed in `issues/NNNN.md` following the ShortLinks convention (status table,
description, acceptance criteria, implementation, verification, files changed,
gotchas). Proposed first pass:

**Phase 0** — 0001 repo scaffold & module rename · 0002 strip shortener packages
· 0003 strip shortener frontend · 0004 renumber auth migrations · 0005 dev
script & devstore adaptation · 0006 CLAUDE.md + docs skeleton

**Phase 1** — 0007 config loader for new vars · 0008 auth stack verification
pass · 0009 admin users/settings/audit screens · 0010 seed command

**Phase 2** — 0011 design tokens & app.css · 0012 self-hosted fonts · 0013
terminal motif components · 0014 History API router · 0015 Home view · 0016
About view · 0017 site header/footer/nav · 0018 logo assets & favicons · 0019
server-injected meta tags · 0020 sitemap + robots · 0021 OG share image · 0022
404 view

**Phase 3** — 0023 interests migration + seed · 0024 interests admin CRUD ·
0025 subscribers migration · 0026 subscribe endpoint + anti-abuse · 0027 SES v2
mailer · 0028 confirmation email templates · 0029 SubscribeForm component ·
0030 confirm flow · 0031 preference center · 0032 subscribers admin screen

**Phase 4** — 0033 suppressions migration · 0034 one-click unsubscribe endpoint
· 0035 List-Unsubscribe headers · 0036 unsubscribe views · 0037 SNS signature
verification · 0038 bounce/complaint ingestion · 0039 soft-bounce threshold job

**Phase 5** — 0040 campaigns migration · 0041 campaign CRUD API · 0042 Markdown
→ HTML+text renderer · 0043 email HTML template · 0044 audience materialization
· 0045 send worker · 0046 test send & preview · 0047 campaign compose UI · 0048
send progress over SSE · 0049 campaign stats

**Phase 6** — 0050 workshops migration · 0051 workshops CRUD API · 0052
workshops admin UI · 0053 public workshops index · 0054 workshop detail · 0055
JSON-LD Event markup · 0056 announce-to-list shortcut

**Phase 7** — 0057 SES inbound receipt rules · 0058 inbound mail parsing · 0059
CSV export · 0060 GDPR erasure · 0061 admin overview · 0062 backup verification
· 0063 accessibility audit · 0064 deployment runbook

---

## 14. Open Questions

| # | Question | Blocks | Default if unanswered |
|---|---|---|---|
| 1 | Physical mailing address for CAN-SPAM — PO box, or a venue that agrees to receive mail? | Phase 5 (first send) | Rent a PO box; it is the only clean answer |
| 2 | Sending identity — `hello@opencircuitsf.com` or `workshops@`? Who monitors the reply-to inbox? | Phase 3 | `hello@`, forwarded to a monitored inbox |
| 3 | Does `opencircuitsf.com` need human mailboxes (Google Workspace / Fastmail)? Determines apex MX. | Phase 0 DNS | Set up a mail provider first; SES inbound stays on `lists.` regardless |
| 4 | ~~Green identity vs. blue logo~~ — **resolved 2026-08-18**: the green PCB badge (`Logo 2026.png`) is the mark; the blue hexagonal badge is retired. See §4.5. | — | — |
| 5 | Rename the GitHub repo from `Website` to `OpenCircuitSF`? | Phase 0 | Rename now, before any deploy references it |
| 6 | Is the interest taxonomy in §6.1 right, and should any be merged or dropped? | Phase 3 | Ship as listed; it is admin-editable |
| 7 | Expected list size in year one — changes nothing architecturally, but sets the SES quota request | Phase 4 | Request 50k/day; ask for what you might need |
| 8 | Do workshop pages need photos at launch, or is type-only acceptable? | Phase 6 | Type-only; add a cover image field now, populate later |
