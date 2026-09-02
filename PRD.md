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
- **Public campaign archive** — every sent campaign published at a permanent,
  search-indexable URL, with a "view in browser" link in the email itself and
  an archive index linked from the home page.
- **Subscriber import** — admin-only CSV import from Luma and other sources,
  recording where each address came from and when, with a batch-level revoke
  for an import that turns out to lack consent.
- **Delivery health** — per-subscriber bounce history, an admin deliverability
  screen, and a send-time circuit breaker on bounce and complaint rates.
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
| `/archive` | ArchiveIndex | Every sent campaign, reverse chronological. Linked from Home. |
| `/archive/{slug}` | ArchiveEntry | One campaign as a permanent web page. Indexable, public, no token. |
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
| `/admin/subscribers` | Search, filter by status/interest, detail drawer, manual add/suppress, CSV export/import |
| `/admin/subscribers/pending` | Unconfirmed signups — age, resend confirmation, expire |
| `/admin/subscribers/import` | CSV import wizard: map columns, declare source and consent, preview, commit |
| `/admin/deliverability` | Bounce and complaint history, per-address event log, suppression list |
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
    already_subscribed_sent_at TIMESTAMPTZ,       -- 000011: rate-limits the
                                                  -- "you're already subscribed" reply so the
                                                  -- uniform 202 cannot be used to mail-bomb
    synthetic      BOOLEAN NOT NULL DEFAULT FALSE, -- 000019: campaign test-send recipient
                                                  -- fixtures. Excluded from every audience,
                                                  -- count, and export
    unsubscribed_at TIMESTAMPTZ,
    unsubscribe_source TEXT,                      -- one_click | preferences | mailto | admin
    -- Provenance (§6.10). Every address must be able to answer
    -- "where did this come from, and when?" without reading the event log.
    source         TEXT NOT NULL DEFAULT 'signup_form',
                   -- signup_form | import | admin_manual | api
    invited_at     TIMESTAMPTZ,                   -- §6.10.1: set once, ever. Its presence is
                                                  -- what makes "one invitation per address"
                                                  -- enforceable across separate imports
    source_detail  TEXT,                          -- e.g. 'luma:oc-soldering-2026-05'
    consent_basis  TEXT,                          -- double_opt_in | imported_prior_consent | admin_attested
    import_id      BIGINT REFERENCES subscriber_imports(id),
    invite_resent_at TIMESTAMPTZ,                 -- 000026: write-once. The bounded,
                                                  -- user-approved deviation from "one invitation
                                                  -- per address, ever" — at most ONE further
                                                  -- invitation, by an authenticated admin action
                                                  -- (#0312). CHECK (invite_resent_at IS NULL OR
                                                  -- invited_at IS NOT NULL)
    -- Delivery health (§6.9). The streak is the live decision variable;
    -- email_events remains the immutable history behind it.
    soft_bounce_streak INT NOT NULL DEFAULT 0,    -- consecutive Transient bounces; zeroed on Delivery
    last_bounce_at TIMESTAMPTZ,
    last_delivery_at TIMESTAMPTZ,
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
-- Corrected 2026-08-21: the primary key is (email, reason), not email alone.
-- Migration 000013 (#0100) widened it so a second reason for an address adds a
-- coexisting row rather than silently no-opping, and Remove(email, reason)
-- retires exactly one reason.
CREATE TABLE suppressions (
    email      TEXT NOT NULL,
    reason     TEXT NOT NULL,   -- hard_bounce | complaint | manual | repeated_soft_bounce
    note       TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (email, reason)
);

CREATE TABLE email_campaigns (
    id             BIGSERIAL PRIMARY KEY,
    name           TEXT NOT NULL,           -- internal label
    subject        TEXT NOT NULL,
    preheader      TEXT,
    body_md        TEXT NOT NULL,           -- authored source
    slug           TEXT UNIQUE NOT NULL,    -- assigned at DRAFT time (§6.8): the
                                            -- archive URL must exist before the
                                            -- send so short links can be minted
                                            -- against it ahead of announcement
    status         TEXT NOT NULL DEFAULT 'draft',
                   -- draft | scheduled | sending | paused_delivery_health | sent | canceled | failed
    archive_status TEXT NOT NULL DEFAULT 'pending',  -- pending | published | withheld
    archived_at    TIMESTAMPTZ,
    audience_mode  TEXT NOT NULL DEFAULT 'any_of',  -- all | any_of | all_of | none_selected
    workshop_id    BIGINT,                  -- optional: announcement source. The FK to
                                            -- workshops(id) is added by #0050 in Phase 6;
                                            -- 000017 ships the bare column so Phase 5 and
                                            -- Phase 6 stay independently deployable
    scheduled_at   TIMESTAMPTZ,
    materialized_at TIMESTAMPTZ,            -- 000018: the "materialize exactly once" marker.
                                            -- Distinguishes a complete audience from a send
                                            -- that crashed mid-materialization
    test_sent_at   TIMESTAMPTZ,             -- 000018: the no_test_send preflight gate
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
-- can never double-deliver. Note the NULL carve-out: Postgres treats NULLs
-- as distinct for UNIQUE purposes, so once #0060's erasure anonymizes a
-- row's subscriber_id to NULL, that row is no longer constrained by this
-- key against any other NULL row — harmless today because an erased
-- subscriber can never be re-materialized into a new send.
-- subscriber_id is nullable with ON DELETE SET NULL, not NOT NULL/CASCADE:
-- #0060's GDPR erasure anonymizes this table's rows for the erased
-- subscriber rather than deleting them, so historical campaign counts never
-- silently change; a CASCADE delete would destroy exactly the rows erasure
-- must preserve.
CREATE TABLE email_sends (
    id             BIGSERIAL PRIMARY KEY,
    campaign_id    BIGINT NOT NULL REFERENCES email_campaigns(id) ON DELETE CASCADE,
    subscriber_id  BIGINT REFERENCES subscribers(id) ON DELETE SET NULL,
    email          TEXT NOT NULL,          -- snapshot at send time
    status         TEXT NOT NULL DEFAULT 'queued',
                   -- queued | sending | sent | failed | skipped
                   -- 'sending' (000018) is the per-row claim state: the worker's atomic
                   -- UPDATE ... WHERE id=$1 AND status='queued' is what makes two workers
                   -- unable to take the same recipient
                   -- 'bounced'/'complained' were removed by #0131: SES bounce and
                   -- complaint events are recorded exclusively in email_events (#0038)
                   -- and never stamped onto this column, so the two values described
                   -- states nothing could ever produce. #0049's stats view reconciles
                   -- bounce/complaint counts via a JOIN against email_events instead.
    ses_message_id TEXT,
    attempts       INT NOT NULL DEFAULT 0,
    error          TEXT,
    claimed_at     TIMESTAMPTZ,            -- 000018 (#0122): stamped by the worker's
                                           -- atomic claim (status='sending', attempts+1,
                                           -- claimed_at=now()); OrphanSweep resets a row
                                           -- back to 'queued' once claimed_at is older than
                                           -- worker.go's orphanStaleAfter, so a crashed
                                           -- worker's abandoned claim doesn't stall the send
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
    sns_message_id TEXT NOT NULL,   -- SNS's delivery id; half of the dedupe key
    ses_message_id TEXT,            -- SES's id for the outbound email — the reconciliation
                                    -- key back to email_sends, NOT a dedupe key
    event_type     TEXT NOT NULL,   -- Bounce | Complaint | Delivery | Reject | RenderingFailure
                                    -- | DeliveryDelay | Send | ''. Deliberately UNCONSTRAINED:
                                    -- SES adds event types over time and this table records
                                    -- whatever arrives rather than gatekeeping it
    bounce_type    TEXT,            -- Permanent | Transient | Undetermined (Bounce only)
    bounce_subtype TEXT,            -- §6.5's out-of-office handling needs the subtype
    recipient      TEXT NOT NULL DEFAULT '',  -- lower(trim(...)); '' when the event carries none
    event_at       TIMESTAMPTZ,     -- the event's own timestamp, when parseable
    payload        JSONB NOT NULL,  -- raw inner SES JSON, recorded before interpretation
    received_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (sns_message_id, recipient)  -- SNS delivers at-least-once; this is the dedupe
);
CREATE INDEX idx_email_events_message_id ON email_events (ses_message_id);
CREATE INDEX idx_email_events_recipient ON email_events (recipient);
-- Backs the currently-shipped windowed soft-bounce rule (#0039, widened by
-- #0109 to also count Undetermined bounces and exclude the sender-fault
-- Transient subtypes). §6.9 below supersedes this rule with a
-- consecutive-streak count on subscribers.soft_bounce_streak instead of a
-- rolling-window query, at which point this index is retired -- until that
-- ships, it is live and this is what internal/sesnotify's count query uses.
CREATE INDEX idx_email_events_soft_bounce
    ON email_events (recipient, received_at DESC)
    WHERE event_type = 'Bounce'
      AND bounce_type IN ('Transient', 'Undetermined')
      AND (bounce_subtype IS NULL
           OR bounce_subtype NOT IN ('MessageTooLarge', 'ContentRejected', 'AttachmentRejected'));
-- Powers the per-address bounce history on /admin/deliverability (§6.9).
CREATE INDEX idx_email_events_recipient_time ON email_events (recipient, received_at DESC);

-- One row per CSV import run (§6.10). The batch is the unit of revocation:
-- an import that turns out to lack consent is undone wholesale, not row by row.
CREATE TABLE subscriber_imports (
    id             BIGSERIAL PRIMARY KEY,
    source         TEXT NOT NULL,          -- luma | eventbrite | meetup | manual_csv | other
    source_detail  TEXT,                   -- event name, export filename, URL
    consent_mode   TEXT NOT NULL DEFAULT 'invite',  -- prior_consent | invite (§6.10)
    consent_note   TEXT NOT NULL,          -- how consent was obtained, in the admin's words
    collected_at   DATE NOT NULL,          -- when the source collected the addresses;
                                           -- also quoted in the invitation copy
    filename       TEXT,
    row_count      INT NOT NULL DEFAULT 0,
    inserted_count INT NOT NULL DEFAULT 0,
    skipped_count  INT NOT NULL DEFAULT 0,
    invited_count  INT NOT NULL DEFAULT 0,   -- invite mode only
    confirmed_count INT NOT NULL DEFAULT 0,  -- invitations accepted, updated as they land
    status         TEXT NOT NULL DEFAULT 'committed',  -- committed | revoked
    revoked_at     TIMESTAMPTZ,
    revoked_reason TEXT,
    imported_by    BIGINT REFERENCES users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Append-only activity log, one row per meaningful thing that happened to an
-- address (§6.11). Distinct from email_events (raw SES payloads) and from
-- audit_log (privileged staff actions against the console).
CREATE TABLE subscriber_events (
    id            BIGSERIAL PRIMARY KEY,
    subscriber_id BIGINT REFERENCES subscribers(id) ON DELETE SET NULL,
    email         TEXT NOT NULL,          -- snapshot; survives erasure of the row
    action        TEXT NOT NULL,          -- enum, see §6.11
    campaign_id   BIGINT REFERENCES email_campaigns(id) ON DELETE SET NULL,
    import_id     BIGINT REFERENCES subscriber_imports(id) ON DELETE SET NULL,
    actor_user_id BIGINT REFERENCES users(id),   -- NULL when the subscriber or a webhook acted
    detail        JSONB,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_subscriber_events_subscriber ON subscriber_events (subscriber_id, created_at DESC);
CREATE INDEX idx_subscriber_events_email ON subscriber_events (email, created_at DESC);
CREATE INDEX idx_subscriber_events_action ON subscriber_events (action, created_at DESC);

-- Durable queue for transactional mail (§6.11). Campaign mail already has
-- email_sends; this is its counterpart for confirmation, welcome, and
-- notification messages, which were previously sent from an in-process
-- goroutine and lost on restart.
CREATE TABLE outbound_queue (
    id             BIGSERIAL PRIMARY KEY,
    kind           TEXT NOT NULL,          -- confirmation | already_subscribed | welcome |
                                           -- goodbye | admin_alert | registration | recovery
    recipient      TEXT NOT NULL,
    subscriber_id  BIGINT REFERENCES subscribers(id) ON DELETE CASCADE,
    payload        JSONB NOT NULL,         -- template inputs, not rendered MIME
    status         TEXT NOT NULL DEFAULT 'queued',  -- queued | sent | failed | abandoned
    attempts       INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ses_message_id TEXT,
    error          TEXT,
    claimed_at     TIMESTAMPTZ,            -- orphan sweep, same shape as email_sends
    sent_at        TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_outbound_queue_due ON outbound_queue (next_attempt_at, id)
    WHERE status = 'queued';

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
-- Published AND canceled workshops are both publicly visible (#0135: the
-- index and the detail route must apply the same visibility rule, or a
-- canceled workshop silently vanishes from the index while its direct link
-- still 200s). Named idx_workshops_visible (#0142, renamed from
-- idx_workshops_published, matching Store.ListVisible) -- the old name
-- stopped meaning "published" the moment #0135 widened the predicate.
CREATE INDEX idx_workshops_visible ON workshops (starts_at DESC)
    WHERE status <> 'draft';

CREATE TABLE workshop_interests (
    workshop_id BIGINT NOT NULL REFERENCES workshops(id) ON DELETE CASCADE,
    interest_id BIGINT NOT NULL REFERENCES interests(id) ON DELETE CASCADE,
    PRIMARY KEY (workshop_id, interest_id)
);
CREATE INDEX idx_workshop_interests_interest ON workshop_interests (interest_id);
```

Plus the copied auth tables: `users`, `passkey_credentials`,
`webauthn_challenges`, `pending_registrations`, `sessions`, `settings`,
`audit_log`.

> **Migration ordering.** The schema above is grouped by topic for readability,
> not by dependency. `email_campaigns.workshop_id` references `workshops`, so
> the `workshops` migration must be numbered ahead of the campaigns migration —
> or the FK added in a later `ALTER TABLE`. Phase 5 lands before Phase 6, so
> prefer the `ALTER TABLE` approach and keep the phases independently
> deployable. The same applies to `subscribers.import_id`, which references the
> Phase 8 `subscriber_imports` table: `subscribers` shipped in migration
> `000010`, so the column and its FK arrive by `ALTER TABLE` after
> `subscriber_imports` is created, never by editing `000010`.

> ~~**Greenfield until first deploy — decided 2026-08-21.** Nothing is in
> production but the static placeholder. There is no PostgreSQL instance on the
> EC2 box holding real data, no subscriber has ever signed up, and no campaign
> has ever been sent. Until the first real release ships, the migration set is
> **not** append-only in practice: `migrations/` may be squashed, renumbered, or
> rewritten as a deliberate act, and the development database may be dropped and
> recreated from scratch. No backfill logic is owed to data that does not exist.~~
>
> ~~This is a scoped, temporary exception to CLAUDE.md §1's append-only rule, and
> it expires the moment the first production deploy applies a migration to a
> database anyone cares about. After that, append-only is absolute again —
> `migrations/000007` exists because ShortLinks broke it once. An issue that
> adds a column before that line is allowed to say "add it to `000010`" instead
> of "add an `ALTER TABLE` in `000020`"; one filed after it is not.~~
>
> ~~The corollary that actually matters day to day: when a Phase 8 issue wants a
> column on `subscribers`, the cheap and correct answer today is to edit
> `000010`, reset the dev database, and move on.~~
>
> **Correction (`#0293`, 2026-08-27).** The exception above expired on
> **2026-08-25**: that is the day `www.opencircuitsf.com` first served this
> project and production's PostgreSQL applied migrations up to
> `schema_migrations.version = 22`. Migrations `000001`–`000022` are now frozen
> and append-only, without qualification — see CLAUDE.md §1. Any schema change
> from here on, including one that would once have gone straight into `000010`
> or another already-shipped file, belongs in a new migration. The three
> paragraphs above are struck through rather than deleted, as the record of
> what was true from 2026-08-21 to 2026-08-25.

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
   as a hard bounce. A soft bounce is `bounceType == "Transient"` **or**
   `"Undetermined"` — an address that only ever produces an unclassifiable
   bounce is operationally as dead as one producing `Transient` ones, so
   silence on `Undetermined` is not license to ignore it. **Except:** a
   `Transient` bounce whose `bounceSubType` is `MessageTooLarge`,
   `ContentRejected`, or `AttachmentRejected` never counts — those describe a
   fault in our own message, not evidence the recipient's address is bad, and
   must not suppress a live subscriber.
5. Return `200` even for payloads that cannot be interpreted — SNS retries
   aggressively on non-2xx and a parse bug should not become a retry storm.
   Log and move on.
6. Reconcile events to `email_sends` by `ses_message_id`; an event with no
   matching send row is still recorded and still triggers suppression.

Also enable the **SES account-level suppression list** as a second layer. Belt
and suspenders: our list is authoritative for our sending decisions, SES's
protects the account reputation if ours ever has a bug.

---

### 6.8 Public campaign archive

Every campaign that has been sent is also a public web page. Three reasons, and
each one earns its keep independently:

1. **It is the only recurring indexable content the site will have.** There is
   no blog by design (§2), and workshop pages go stale the day after the event.
2. **A prospect can read past issues before subscribing.** That raises signup
   quality and lowers the complaint rate — the metric SES suspends accounts
   over (§6.9).
3. **Email clients mangle HTML.** Every campaign carries a "View this email in
   your browser" link to its archive page.

**The slug is assigned at draft time, not at send time.** This is the load-bearing
detail. Marketing for a campaign — a short link from `go.opencircuitsf.com`, a
Discord post, a Luma description — has to be prepared *before* the send goes
out, which means the URL has to exist before the send goes out. Creating a
campaign draft therefore mints `email_campaigns.slug` immediately and the admin
UI shows the full future URL as a copyable field. The page itself 404s until
the campaign reaches `sent`.

| | |
|---|---|
| Draft URL | `https://www.opencircuitsf.com/archive/{slug}` — reserved, 404 until sent |
| Published | On transition to `sent`, `archive_status` → `published`, `archived_at` stamped |
| Withheld | An admin can set `archive_status = 'withheld'` to keep a campaign off the archive (410 Gone, removed from the index and sitemap) |

**Rendering.** One authored source, `email_campaigns.body_md`, renders three
ways from the same Markdown:

- **Email HTML** — the constrained, table-based, inline-styled template from
  `#0043`. Assume 2003-era CSS support.
- **Email plain text** — the existing text renderer.
- **Web page** — the same Markdown through the site's normal renderer, nested
  in the standard site shell (header, footer, tokens, fonts). This is a *web
  page that happens to contain a newsletter*, not an email screenshotted into a
  page. It is the one of the three that gets to look like the rest of the site.

The email and web renderers share the Markdown parse; they do not share the
output template. `#0042` already separates these concerns.

**Indexing and SEO.**

- `GET /archive` — reverse chronological index, in the sitemap, linked from
  Home and from the site footer.
- `GET /archive/{slug}` — `<title>` from the campaign subject, meta description
  from the preheader, canonical URL, OG card. Server-injected via `#0019`'s
  renderer.
- Both are in `sitemap.xml`. `withheld` campaigns are excluded from the sitemap
  and serve `410`.
- No login, no token, no `noindex`. These pages are public on purpose.

**Attribution.** Archive URLs are the natural landing target for the group's own
marketing. Short links minted at `go.opencircuitsf.com` carry `utm_source` /
`utm_medium` / `utm_campaign` through to the page, where `#0026`'s existing
first-party UTM capture already stores them on any signup that results. This
gives per-channel attribution for campaign promotion **without** adding any
tracking to the email itself — the §2 non-goals (no open-tracking pixels, no
per-recipient click tracking, no third-party analytics) stand unchanged.

**Privacy.** The archive page renders the campaign body only. It must never
render per-recipient substitutions, unsubscribe tokens, `manage_token` values,
or recipient counts. The archive is built from the campaign row, never from an
`email_sends` row.

### 6.9 Delivery health — bounce policy and the circuit breaker

AWS enforces a bounce rate under **5%** and a complaint rate under **0.1%**
across the whole SES account. Crossing either puts the account under review and
then into sandbox — which takes down the confirmation emails too, not just
campaigns. Delivery health is therefore an availability concern, not a metric.

**Classification.** SES reports three bounce types. They are not equivalent and
must not be handled alike:

| SES `bounceType` | Meaning | Policy |
|---|---|---|
| `Permanent` | The mailbox does not exist | Suppress immediately, reason `hard_bounce`. Never retried. |
| `Transient` | Full mailbox, greylisting, temporary server failure | Increment `soft_bounce_streak`. Retry on future campaigns. |
| `Undetermined` | SES could not classify it | Treat as `Transient` (#0109) |

**The streak, and what resets it.** `subscribers.soft_bounce_streak` counts
*consecutive* soft bounces. A SES `Delivery` event for the address sets it back
to `0` and stamps `last_delivery_at`. When the streak reaches
`soft_bounce_threshold_count` (settings, default `5`), the address is suppressed
with reason `repeated_soft_bounce`.

> **This supersedes the shipped windowed rule.** `#0039` and `#0112` implemented
> the threshold as *N Transient bounces within a rolling 30-day window*, computed
> by querying `email_events` at decision time. The window self-heals as it rolls,
> but it has no notion of a successful delivery: an address that bounces four
> times, then delivers cleanly for a month, still carries four bounces against
> it. Consecutive-with-reset-on-delivery is the industry-standard rule (it is
> what Mailchimp, Postmark, and SES's own reputation guidance describe) and it
> is what a recipient would expect. `soft_bounce_threshold_window_days`
> (settings, seeded by migration `000015`) is retired by this change.

**History is never lost.** `email_events` stays append-only and is the record of
every bounce and complaint SES ever reported. Resetting the streak resets a
counter, not the history. `/admin/deliverability` reads that history:

- A list of addresses with bounce activity, sorted by streak then recency.
- Per-address: every event, its type, its SES diagnostic code, the campaign it
  came from, and the timestamp.
- Actions: clear the streak, suppress manually, remove a suppression (`#0100`'s
  existing endpoints).

**The circuit breaker.** The send worker tracks the running bounce and complaint
rate of the campaign in flight. When either crosses its threshold **and** a
minimum sample has been sent, the worker stops:

| Setting | Default | Meaning |
|---|---|---|
| `send_health_min_sample` | `50` | Below this many sends, rates are too noisy to act on |
| `send_health_bounce_pct` | `5.0` | Running bounce rate that pauses the send |
| `send_health_complaint_pct` | `0.1` | Running complaint rate that pauses the send |

On trip: campaign status → `paused_delivery_health`, the reason is written to
the audit log, remaining `email_sends` rows stay `queued`, and an alert email
goes to `ADMIN_EMAIL` through the outbound queue (§6.11). Resuming is a
deliberate admin action with typed confirmation, exactly like starting a send.

The breaker is a safety mechanism and, like the `physical_address` check in
`#0045`, is **not bypassable from the UI**.

### 6.10 Subscriber import and consent provenance

**The website is the primary way addresses enter this list.** Import is the
secondary path, for audiences the group already collected somewhere else — a
Google Form, a sign-in sheet at an event, a spreadsheet. It is not expected to
carry much volume. Importing is legitimate; importing carelessly burns the
sending domain and is unlawful under CAN-SPAM and GDPR. The import path exists
so that the careful version is the easy one.

> **Luma is unlikely to be a real source — noted 2026-08-21.** Earlier drafts
> treated a Luma attendee export as the motivating case. It probably is not one:
> exporting attendee addresses out of Luma to mail them from elsewhere is not
> something this project expects to do. `luma` survives as a `source` value for
> labelling, and nothing in the importer is built around Luma's schema. Google
> Forms and event sign-in sheets are the realistic sources.

**Every import declares its provenance up front.** A `subscriber_imports` row is
created before any address is inserted, and it requires:

- `source` — `luma`, `eventbrite`, `meetup`, `manual_csv`, `other`
- `source_detail` — the specific event or export it came from
- `collected_at` — when the *source* collected the addresses, not when we imported them
- `consent_note` — how consent was obtained, in the admin's own words. Required, non-empty.

Each imported subscriber carries `source = 'import'`, the `import_id`, and a
`consent_basis` of `imported_prior_consent`. `/admin/subscribers/{id}` shows all
of it, so "why is this person on the list?" always has an answer.

**Two consent modes, chosen per batch.** `subscriber_imports.consent_mode`
decides what happens to the addresses an import brings in:

| Mode | What it does | When to use it |
|---|---|---|
| `prior_consent` | Inserts `active`. **Sends nothing** — no confirmation, no welcome. | The source already collected an opt-in and the admin is attesting to it |
| `invite` | Inserts `pending` and sends one invitation naming where the address came from. The address becomes `active` only on confirmation. | Anything short of a clear prior opt-in — and the safer default |

`prior_consent` sending nothing is deliberate and is the decision of record
(2026-08-21). An import asserts that consent already exists; re-running double
opt-in over it produces mail nobody asked for and a low confirmation rate that
reads to SES as a spam signal. A welcome email is likewise not sent — the
welcome (§6.3) is the reward for confirming, and these subscribers did not
confirm here.

That places the entire weight of `prior_consent` on the admin's attestation,
which is why `consent_note` is mandatory and why batch revocation is one action.

`invite` mode is specified in §6.10.1.

**Import rules.**

- CSV, one email column, optional interest-slug column. Column mapping is chosen
  in the UI, not assumed from the header.
- Every address is checked against `suppressions` before insert. A suppressed
  address is skipped and counted, never resurrected. This is absolute — an
  import is exactly the mechanism by which a suppressed address gets a second
  chance to complain.
- An address already present is skipped, not overwritten. An import never
  changes an existing subscriber's status, interests, or consent basis.
- A `prior_consent` import lands subscribers `active` and sends them nothing.
  An `invite` import lands them `pending` and sends one invitation (§6.10.1).
- Dry run first: the wizard previews counts (new / duplicate / suppressed /
  malformed) and the admin commits explicitly.
- Every import writes an `audit_log` entry and one `subscriber_events` row per
  inserted address.

**Batch revocation.** If an import turns out to lack proper consent, the whole
batch is revoked in one action: every subscriber whose `import_id` matches and
whose status is still `active` moves to `unsubscribed` with
`unsubscribe_source = 'admin'`, the import row goes `status = 'revoked'` with a
reason, and the action is audited. Addresses that have since engaged are still
revoked — the question is whether we ever had the right to mail them, and the
answer does not change because they opened something.

#### 6.10.1 Invite mode — asking imported addresses for explicit consent

Deduplication already tells an import which addresses are genuinely new. Those
are exactly the addresses where consent is least certain, and `invite` mode
turns that list into a request rather than an assumption.

**The flow.**

1. The import dedupes. Addresses already in `subscribers`, and addresses on the
   suppression list, are skipped as always — they are never invited.
2. Each genuinely-new address is inserted `pending` with a `confirm_token` and
   `confirm_expires_at`, `source = 'import'`, `consent_basis = NULL`.
3. One invitation goes out per address, through the outbound queue (§6.11).
4. Confirming sets `active`, stamps `confirmed_at`, sets
   `consent_basis = 'double_opt_in'`, and clears the token. From that point the
   subscriber is indistinguishable from a website signup, because they are one.
5. An invitation that is never accepted expires. The row stays `pending` and is
   never mailed again by this mechanism.

**The invitation must say where the address came from.** This is the difference
between an invitation and spam, and it is not optional copy:

> You gave us this address when you signed up for *Intro to Soldering* through
> our Google Form on 12 May 2026. We're starting an email list for workshop
> announcements — confirm below if you'd like to be on it. If not, do nothing
> and you won't hear from us again.

The source sentence is built from `subscriber_imports.source`, `source_detail`,
and `collected_at`, which is the second reason those three fields are mandatory.

**Rules.**

- One invitation per address, ever, from any automated import path. No
  reminder, no re-invite on a later import. An address that has already been
  invited is skipped by every subsequent import, in whatever mode.

  **Approved deviation** (`#0312`, commit `bcf3239`): an authenticated,
  audited admin action may resend that one invitation at most once more,
  ever, per address — recorded in a write-once `subscribers.invite_resent_at`
  (§6.2, migration `000026`; `CHECK (invite_resent_at IS NULL OR invited_at
  IS NOT NULL)`). Two is a constant, not a loop: this is one bounded
  exception for one deliberate admin action, not a general re-invite, and no
  automated path may ever set `invite_resent_at`.
- The invitation carries `List-Unsubscribe` headers and a working opt-out that
  suppresses the address outright, so declining is one click and not a
  non-action.
- No welcome email follows an accepted invitation — the invitation *was* the
  introduction. §6.3's welcome belongs to website signups.
- Invitations are subject to the same send-rate limit as campaign mail, and
  count toward the delivery-health thresholds in §6.9. An invite batch that
  starts bouncing trips the same breaker.
- Revoking the import revokes uninvited and unconfirmed rows. An address that
  confirmed has given consent directly and is left alone — its consent no longer
  derives from the import.

### 6.11 Durable outbound queue and the activity log

**The queue.** Campaign mail is durable — `email_sends` is a real table with an
idempotency key. Transactional mail was not: the confirmation email is dispatched
from an in-process goroutine with roughly a second of SES retry, so an SES
outage or a process restart loses the signup silently. The subscriber sits
`pending` and never hears anything.

`outbound_queue` fixes that with the same shape `email_sends` already uses:
a row per message, claimed by a worker, exponential backoff on retry
(1m, 5m, 15m, 1h, 6h, 24h — capped at `queue.max_retries`, default 8), an
orphan sweep for rows whose worker died, and `abandoned` as the terminal state
with the last error retained. Enqueue happens inside the same transaction as the
state change that caused it, so a confirmation email cannot go missing because
the commit succeeded and the send did not.

**The activity log.** `subscriber_events` is the answer to "what has happened to
this address?" — one row per meaningful action, with a closed set of values:

| `action` | Written when |
|---|---|
| `signup_requested` | `POST /api/subscribe` accepted a new address |
| `confirmation_sent` | A confirmation message left the outbound queue |
| `confirmed` | The double opt-in link was followed |
| `confirmation_expired` | A pending signup aged past `confirm_expires_at` |
| `welcome_sent` | The welcome message was sent |
| `interests_changed` | The preference center replaced the interest set |
| `unsubscribed` | Any of the three unsubscribe paths (§6.5) |
| `resubscribed` | A previously unsubscribed address signed up again |
| `imported` | Inserted by a CSV import |
| `invite_sent` | An import invitation was sent (§6.10.1) |
| `invite_accepted` | An import invitation was confirmed |
| `invite_expired` | An import invitation aged out unaccepted |
| `import_revoked` | Removed by a batch revocation |
| `campaign_sent` | A campaign message was accepted by SES for this address |
| `bounced_soft` / `bounced_hard` | SES reported a Transient / Permanent bounce |
| `complained` | SES reported a complaint |
| `delivered` | SES reported a delivery (this is what resets the streak) |
| `suppressed` / `unsuppressed` | A suppression was added or removed |
| `admin_edited` | A staff member changed the record directly |
| `erased` | GDPR erasure (`#0060`) |

Three logs, three jobs, no overlap: `audit_log` records what *staff* did to the
console, `email_events` stores what *SES* said verbatim, and
`subscriber_events` records what happened to an *address* in our own vocabulary.
The subscriber detail drawer reads the third.

`email` is snapshotted on every row so the log survives erasure of the
`subscribers` row — `#0060`'s erasure nulls `subscriber_id` and redacts the
`email` column on that address's rows rather than deleting the history, which
would otherwise destroy the evidence that the erasure itself was performed.

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
| `POST` | `/api/subscribe/confirm` | Confirm with token → active. Also accepts an import invitation (§6.10.1) — same token shape, same endpoint. |
| `GET` | `/api/preferences?token=` | Read current interests |
| `PATCH` | `/api/preferences` | Replace interest set |
| `POST` | `/api/unsubscribe` | One-click (RFC 8058) and in-page unsubscribe |
| `GET` | `/api/unsubscribe` | **Never mutates.** 302 → `/unsubscribe` |
| `GET` | `/api/archive` | Sent, published campaigns — reverse chronological |
| `GET` | `/api/archive/{slug}` | One published campaign as web content. 404 until sent, 410 if withheld. |
| `GET` | `/api/workshops` | Published workshops |
| `GET` | `/api/workshops/{slug}` | One published workshop |
| `POST` | `/api/ses/notifications` | SNS bounce/complaint webhook (signature-verified) |
| `POST` | `/api/ses/inbound` | SNS inbound-mail webhook (signature-verified) |
| `GET` | `/health` | Liveness. **Shipped as `/health`**, not `/healthz` — corrected here 2026-08-21 to match `cmd/opencircuit`. |
| `GET` | `/api/me` | Current session identity for the SPA shell |

### Authenticated (copied from ShortLinks)

All of `/auth/*`, `/account/credentials*`, `/admin/users*`, `/admin/settings`,
`/admin/audit` carry over unchanged — see ShortLinks' auth route table.

### Admin — session + `is_admin`

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/admin/subscribers` | Paginated, filterable by status and interest |
| `GET` | `/admin/subscribers/{id}` | Detail incl. consent evidence and event history |
| `POST` | `/admin/subscribers/{id}/suppress` | Manual suppression with a note |
| `POST` | `/admin/subscribers/{id}/clear-complaint` | Clear a `complained` state. Admin-only by design — a complained address never auto-resubscribes |
| `DELETE` | `/admin/subscribers/{id}` | Hard delete (GDPR erasure request) |
| `GET` | `/admin/subscribers/export` | CSV export |
| `GET` | `/admin/subscribers/pending` | Unconfirmed signups with age |
| `POST` | `/admin/subscribers/{id}/resend-confirmation` | Re-enqueue the confirmation email |
| `POST` | `/admin/subscribers/import/preview` | Dry run — parse, map, and count without writing |
| `POST` | `/admin/subscribers/import` | Commit an import batch |
| `POST` | `/admin/imports/{id}/revoke` | Revoke a whole import batch |
| `GET` | `/admin/imports` | Import history with per-batch counts and invitation outcomes |
| `GET` | `/admin/deliverability` | Addresses with bounce activity |
| `GET` | `/admin/deliverability/{email}` | Full event history for one address |
| `POST` | `/admin/deliverability/{email}/reset-streak` | Clear the soft-bounce streak |
| `GET` | `/admin/suppressions` | The suppression list (`#0100`) |
| `POST` | `/admin/suppressions/remove` | Retire one suppression reason (`#0100`) |
| `GET`/`POST`/`PATCH`/`DELETE` | `/admin/interests[/{id}]` | Taxonomy CRUD |
| `GET`/`POST`/`PATCH` | `/admin/campaigns[/{id}]` | Campaign CRUD |
| `GET` | `/admin/campaigns/{id}/preflight` | The pre-send gate's current verdict and every blocking reason |
| `POST` | `/admin/campaigns/{id}/preview` | Render HTML + text without sending |
| `POST` | `/admin/campaigns/{id}/test` | Send to one admin address |
| `GET` | `/admin/campaigns/{id}/audience` | Recipient count + sample, before committing |
| `POST` | `/admin/campaigns/{id}/send` | Requires typed confirmation of the count |
| `POST` | `/admin/campaigns/{id}/cancel` | Stop an in-flight send |
| `POST` | `/admin/campaigns/{id}/resume` | Resume a send paused by the delivery-health breaker |
| `PATCH` | `/admin/campaigns/{id}/archive` | Publish or withhold the archive page |
| `GET` | `/admin/campaigns/{id}/stats` | Sent / failed / bounced / complained |
| `GET`/`POST`/`PATCH`/`DELETE` | `/admin/workshops[/{id}]` | Workshop CRUD |
| `POST` | `/admin/workshops/{id}/announce` | Create a draft campaign pre-filled from the workshop |
| `GET` | `/api/events` | SSE — live send progress on the campaign screen |

---

## 9. Configuration

```env
# ── Server ─────────────────────────────────────────────────────────────────
PORT=8080
BASE_URL=https://www.opencircuitsf.com   # canonical host — drives og:url, og:image,
                                         # sitemap <loc> and robots.txt's Sitemap: line.
                                         # Corrected 2026-08-18 (#0072): this block
                                         # previously showed the apex, which production
                                         # 301s to www, so the documented deploy would
                                         # publish canonicals contradicting the redirect.

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

# ── Development and test seams ─────────────────────────────────────────────
# These three ship in the config loader but were missing from this block until
# the 2026-08-21 currency pass.
STORAGE=postgres          # postgres | json. 'json' selects internal/devstore, the
                          # in-memory stand-in that lets scripts/dev.sh run with no
                          # database at all. Never 'json' in production.
MAILER_NOOP=false         # true drops outbound mail instead of calling SES, so a dev
                          # machine with no AWS credentials still exercises the flows.
                          # The admin routes that would send real mail are omitted
                          # from the route table entirely when this is set.
SES_EVENTS_TOPIC_ARN=     # The SNS topic bounce/complaint notifications must arrive on.
                          # A message from any other topic is refused before any
                          # outbound certificate fetch (#0107).
```

Runtime-editable values live in the `settings` table, not the environment:
`registrations_enabled`, `physical_address`, `max_send_rate`,
`signup_enabled`, `default_from_name`, `soft_bounce_threshold_count`,
`send_health_min_sample`, `send_health_bounce_pct`,
`send_health_complaint_pct`, `archive_enabled`.

`soft_bounce_threshold_window_days` is **retired** by §6.9's move from a rolling
window to a consecutive streak; the Phase 8 migration deletes its row.

Seeded by migration today: `physical_address` (`000008`),
`soft_bounce_threshold_count` (`000015`), `max_send_rate` and
`default_from_name` (`000018`).

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

`pg_dump` nightly to a local directory tree on the database host
(`scripts/db/backup.sh`, custom or plain format, pruned after
`BACKUP_RETENTION_DAYS`), then an `rsync`-over-SSH **pull** to a separate
offsite machine — a Mac mini, not the server — run by
`scripts/db/pull-backups.sh` (pull rather than push, so a server compromise
never holds the offsite copy's credentials). `scripts/db/restore.sh` reassigns
ownership of every restored table, sequence, and view to the application
role, since the dump itself carries no owner or grants (`--no-owner
--no-privileges`, stripped so the dump doesn't require the dump-time role to
exist on the target). A systemd timer plus an `OnFailure=` alert unit
(`deploy/systemd/opencircuit-backup.{service,timer}`,
`opencircuit-backup-alert.service`) run the nightly dump and notify on
failure. The subscriber list is the single most valuable and least
reconstructible asset in the system — verify a restore before launch, not
after the first incident; see `docs/deployment.md`'s Backups section for the
full runbook, including what remains unverified until a real server and a
real offsite host exist.

**S3 is a deferred option, not the design.** An earlier draft of this section
specified `pg_dump` to S3 with a 30-day lifecycle rule, bucket encryption, a
public-access block, and IAM scoped to the bucket prefix. None of that is
built, and this section no longer specifies it as the target — it needs an
AWS account, which does not exist yet (`CLAUDE.md` §10 item 2), and building
it now would mean writing against credentials nobody has, same reasoning as
SES. The design above ships without it; S3 upload can be added later on top
of `backup.sh` without redesigning the local-dump/offsite-pull shape.

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

### Phase 8 — Archive, imports, and delivery health
The public campaign archive with draft-time slugs and a "view in browser" link,
CSV import with recorded consent provenance, batch revocation, and an invite
mode that asks genuinely-new imported addresses for explicit consent, the
consecutive soft-bounce streak with an admin deliverability screen, the
send-time bounce/complaint circuit breaker, the durable outbound queue and
subscriber activity log, the welcome email, and the pending-subscriber screen.
**Deployable:** the list becomes something you can grow deliberately and
operate safely rather than only send from.

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

**Phase 8** — 0123 public campaign archive · 0124 delivery-health streak,
history, and circuit breaker · 0125 subscriber CSV import with consent
provenance · 0126 durable outbound queue + subscriber activity log · 0127
welcome email · 0128 pending-subscriber admin screen · 0129 invite imported
addresses to confirm

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
| 9 | ~~What does a Luma attendee export contain, and do its terms permit adding attendees to a newsletter?~~ — **resolved 2026-08-21**: Luma is unlikely to be an import source at all. The importer takes a generic CSV with UI column mapping; Google Forms and event sign-in sheets are the realistic sources. See §6.10. | — | — |
| 10 | Should the archive index be paginated, or is a single page fine for the first few years? | Phase 8 (`#0123`) | Single page; a community newsletter will not outgrow it soon, and one page indexes better |
