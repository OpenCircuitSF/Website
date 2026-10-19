# Admin navigation — options

*Standalone design review (Fable, per AGENTS.md §3), 2026-10-18. Read-only:
no code was changed. The visual mockups are in
[`admin-nav-options.html`](admin-nav-options.html) — open it with
`python3 -m http.server` from `docs/reviews/` (or just double-click it; it
has no external resources, so `file://` is fine here). It follows
`prefers-color-scheme` and has a theme toggle in the corner.*

Brennan, 2026-10-18, looking at the nav that `#0586` shipped:

> I do not like the navigation. … We could instead organize all of the
> screens by sections so they don't all have to be visible at the same
> time. I would like to see options.

This note diagnoses the current bar, fixes the information architecture
first (the part every option shares), then lays out four genuinely different
navigation models with mockups, and ends with a recommendation and a plan
small enough to file as two issues, with an optional third.

---

## 1. What is there today

`Admin.svelte` renders, above every admin screen, in this order:

1. The app header: title, an **Account | Admin** tab pair, Sign out.
2. One `<nav aria-label="Admin sections">` holding **14 links at one visual
   weight**: Overview, then three `role="group"`s labelled MAILING LIST
   (Subscribers, Pending, Suppressions, Deliverability, Campaigns,
   Interests), WORKSHOPS (Workshops, Outreach), SYSTEM (Settings, Users,
   Audit log, CRT session). Each link is a bordered pill (`--bg-panel`,
   1px `--border`, 2px bottom edge), text `--text-muted`, **underlined** —
   `.subtab` never clears the global `a { text-decoration }`. The active one
   turns its bottom edge and text `--accent`, weight 600.
3. At ≤480px the links are `display:none` and a full-width native
   `<select>` with three `<optgroup>`s replaces them.
4. Then, depending on the screen, a third row: Outreach's own sub-nav
   (`Posts · Flyer spots · Flyers · Results`, plain links, bold+underline
   when current), or Settings' jump links (`General · Server settings ·
   Outreach`, anchor-scroll), or a master/detail "back" link (Workshops,
   Campaigns).

"CRT session", for the record, is `CrtCommands.svelte` (`#0393`): the rows
behind the home page hero's animated terminal screen — command text, output,
order, on/off. It is **site content**, edited a few times a year, and the
label is the only thing in the admin that names an implementation detail
rather than a job.

### 1.1 Diagnosis — the specific problems

**P1. The grouping is typographic, not structural.** All 14 links are still
on screen at once; the group labels are small uppercase text with a 1px
left rule. In the 1040px shell the set does not fit on one line, so it
wraps — and flex-wrap breaks a group wherever the line happens to end, so
SYSTEM's label can sit at the end of row one with its items on row two. The
grouping reads as decoration on a flat list, which is why it still *feels*
like the thirteen-tab bar the review's A2 complained about.

**P2. Frequency is inverted.** The screens Brennan opens around every
workshop (Workshops, Outreach, Campaigns) sit at exactly the same weight —
and in the case of Workshops/Outreach, *after* six mailing-list links — as
screens opened a few times a year (Users, Audit log, Suppressions, CRT
session). Overview, the landing, is mailing-list statistics
(`01-admin-overview.png`: a red Needs-attention box about CAN-SPAM and SES,
then zeros).

**P3. The active state is weak.** A 2px green underline on one of fourteen
bordered boxes, plus green text at 15px. Among the pills' own borders, the
uppercase group labels, and the *text underlines on every link*, the current
location is the least prominent thing in the bar.

**P4. Three idioms for "level two".** Outreach's sub-nav (bold+underline
text links), Settings' jump links (anchor scroll within one long page), and
Workshops/Campaigns' master/detail (list → editor with a back link) are three
different answers to the same question. A reader cannot predict what
clicking a second-row item will do.

**P5. Two nav rows above the nav.** The admin header's **Account | Admin**
pair is a navigation row whose only purpose is to reach the passkey screen.
On desktop there are three rows of chrome before content; on a phone,
four (title, Account/Admin, the select, Outreach's sub-nav).

**P6. The phone fallback hides the map.** The `<select>` is one row, which is
what `#0586` set out to achieve, but a select shows only the current value:
no siblings, no sense of which group you are in, and `<optgroup>` labels are
not announced by every screen reader. It also looks like a form control, not
navigation.

**P7. Labels that need the group to be understood.** "Pending" (pending
what? — the panel it opens is titled *Pending confirmations*), "Deliverability"
next to "Suppressions" (the PRD's §5.2 has one `/admin/deliverability` for
both), and "CRT session" (P1 above). Fine once you know; not fine for the
future second admin, and not fine for a `<select>` where the group label may
not be read.

What is right and must survive: every screen has a real, copyable
`/admin#/…` URL (`#0527`); the hash is the single source of truth; the links
are real `<a>` elements with `aria-current="page"`; the groups
(`ADMIN_NAV_GROUPS`) are the right three groups; and the Outreach sub-views
already live in the hash query, so no option below needs a new route.

---

## 2. Information architecture

### 2.1 Task frequency

Taken from the outreach review's §2 ("announce every workshop, remind some,
flyer some, see results occasionally") and from what each screen does. These
are estimates, not measurements — see §6.

| Screen | Job | How often | Section |
|---|---|---|---|
| Overview | What needs attention, what is sending, what is next | every visit | — (landing) |
| Workshops | Create/edit/publish a workshop, announce it | every workshop (several times) | Workshops |
| Outreach → Posts | Post the Announce, remind, mark posted | every workshop (several times) | Workshops |
| Outreach → Flyers / Flyer spots | Print flyers, maintain the four spots | some workshops; spots rarely | Workshops |
| Outreach → Results | Did it bring people in | occasionally, after | Workshops |
| Campaigns | Compose, test-send, send a mailing | every announcement | Mailing list |
| Subscribers | Look someone up, export, import | occasionally | Mailing list |
| Pending (confirmations) | Resend / expire unconfirmed signups | rarely | Mailing list |
| Deliverability | Bounces and complaints after a send | after a send | Mailing list |
| Suppressions | Inspect or lift a suppression | rarely | Mailing list |
| Interests | Edit the taxonomy | rarely | Mailing list |
| Settings (General · Server · Outreach) | Address, SES flags, connections | setup, then rarely | System |
| CRT session | Edit the home page terminal | a few times a year | System |
| Users | Second admin, deactivate | almost never | System |
| Audit log | Investigate | when something is odd | System |

Two screens dominate (Workshops, Outreach), two more are regular (Campaigns,
Overview), and ten are occasional-to-rare. **A good nav for this console is
one that keeps four things one click away and ten things two clicks away
without making the ten feel lost.** That is the test each option is held to.

### 2.2 Sections

Keep `#0586`'s three groups; they are right. Two label changes and one
ordering change, applied in every option:

- Order the sections by frequency: **Overview · Workshops · Mailing list ·
  System**. Workshops before Mailing list, because that is where the daily
  work is and because the nav should not open with the six least-used
  screens.
- Within Mailing list: **Campaigns, Subscribers, Pending confirmations,
  Deliverability, Suppressions, Interests** — Campaigns first (it is the one
  used per announcement), the hygiene screens after.
- Relabel **Pending → Pending confirmations** (the panel's own title) and
  **CRT session → Home screen** (with "CRT" kept in the panel title and the
  page's `<h2>` so the word is still searchable). Both are label-only edits
  in `ADMIN_SECTION_LABELS`; the hashes `#/pending` and `#/crt` do not change.

| Section | Screens, in order | Landing (what a section click opens) |
|---|---|---|
| Overview | — | The dashboard: Needs attention, sending now, upcoming workshops not announced, counts |
| Workshops | Workshops · Outreach | Workshops list (Outreach column already shows each workshop's status) |
| Mailing list | Campaigns · Subscribers · Pending confirmations · Deliverability · Suppressions · Interests | Campaigns list |
| System | Settings · Home screen · Users · Audit log | Settings |

"Landing" matters in every option: a section is a thing you can be *in*, so
clicking it must go somewhere. The landing is the section's most-used
screen, never a blank page. (Option C makes the landing a hub page instead;
see its trade-offs.)

### 2.3 Level two and three

- **Section → screen** is the only level the top nav owns.
- **Screen → view** (Outreach's Posts · Flyer spots · Flyers · Results;
  Settings' General · Server · Outreach) belongs to the screen: rendered
  inside or directly on top of its panel, in **one shared style**, so P4 goes
  away. A small `SubNav.svelte` (links with `aria-current`, same chip style as
  the screen row) used by both Outreach and Settings is the cheap way to get
  there; the Settings jump links keep their scroll behaviour.
- **List → record** (a workshop, a campaign, a flyer) stays master/detail
  with the back link; the nav above does not change when a record opens,
  which is the right behaviour and what the hash router already does.

---

## 3. Options

All four share §2. In every mockup the state shown is **Workshops → Outreach
→ Posts**, the most common place to be.

Common to all: the hash routes are untouched. The nav derives *which section
is active* from `adminRoute.section` through `ADMIN_NAV_GROUPS` (a
`sectionGroup(section)` helper in `adminRouter.ts`, ~5 lines, unit-tested
with the existing parser tests); the section's link `href` is the landing
screen's `adminSectionHash(...)`. A `not-found` route lights nothing.
Cross-screen jumps (`#/campaigns/42`, `#/audit?campaign=7`,
`#/settings?section=outreach`, `preflight.ts`'s "Go to Settings") keep
working because they already set the hash and nothing else.

Also common, and worth doing with whichever option wins: drop the
**Account | Admin** pair from the admin header. "Account" becomes a plain
link beside Sign out (it is the passkey screen; it is not a peer of the whole
admin). That removes one row of chrome everywhere (P5). `Account.svelte` keeps
its own header as is.

### Option A — Section tabs with a contextual screen row

**Concept.** Two rows, each short. Row one is the four sections. Row two
shows *only the active section's screens*. On Overview there is no row two.
This is the "organize by sections so they don't all have to be visible"
reading taken literally, with the fewest moving parts.

**Desktop.**

```
 Open Circuit SF                                 Account · Sign out
 ┌──────────┐
 │ Overview │  Workshops ▍ Mailing list   System          ← row 1: 4 tabs, active = filled
 └──────────┘
   Workshops   Outreach                                   ← row 2: this section only
               ────────                                     active = 2px accent underline
 ┌ Outreach ─────────────────────────────────────────────┐
 │  Posts · Flyer spots · Flyers · Results               │ ← level 3, inside the panel
 │  …                                                    │
```

Row one uses the look the header's `.nav-tab.active` already has
(`--accent-subtle` fill, `--accent` text, 600) — a *filled* tab, not an
underlined pill, so the active section is the strongest mark on the page.
Row two is quieter: plain labels in `--text-muted`, the active one in
`--text` with the 2px `--accent` bottom edge the current `.subtab.active`
uses, no boxes, no underlines. A 1px `--border` rule under row two separates
chrome from content.

**Phone (390px).** Row one fits in one line at `--fs-base` ("Overview
Workshops Mailing list System" is ~36 characters plus padding); set
`overflow-x:auto; white-space:nowrap` so a longer future label scrolls
rather than wraps. Row two is the same: chips in a horizontally scrolling
strip with a fade at the right edge. Two rows, both one line, no select.
The `#0586` select goes away — row one is already a complete, visible map.

**Sub-navigation.** Outreach's four views and Settings' three sections sit
in the panel (§2.3). When Outreach is open the page shows three levels, but
rows one and two are each one short line, and level three is visually part
of the content, not the chrome.

**Active location.** Row one: filled tab, `aria-current="true"` (it is the
current section, not necessarily the current page). Row two: underlined
label, `aria-current="page"`. Level three: `aria-current="page"` on the view
link, as today.

**Keyboard and screen reader.** Links in two labelled `<nav>`s: "Admin
sections" and "{Section} screens". Tab order is row one left to right, then
row two, then content. No roving tabindex, no `role="tablist"` — these are
links to URLs, exactly as `#0527` chose, and the
`:where(a, .nav-tab, .subtab):focus-visible` underline focus rule in
`app.css` still applies. A screen reader lands on
"navigation, Admin sections, 4 items" and then "navigation, Workshops
screens, 2 items", which is also a plain-language description of where you
are.

**Deep links.** Unchanged; `#/outreach?view=results` lights Workshops in
row one, Outreach in row two, Results in the panel. `#/` shows row one only.

**Cost: S.** `adminRouter.ts` (add a `landing` to each group, a
`sectionGroup()` helper, the two label changes, reorder the groups);
`Admin.svelte` (the `<nav class="subtabs">` block and its ~70 lines of CSS;
remove the select; the header pair); `Admin.nav.behavior.test.ts` (rewrite
its two tests: groups → rows, select → gone); `focusOutline.guard.test.ts`
only if `.subtab` is renamed (keep the class name and it does not move).
Plus a Tart render at 390px as the real-browser evidence `#0586` still owes.

**Trade-offs.** The full map is never visible at once — that is the point,
but a first-time admin has to click a section to see what is in it. Switching
between sections that are not adjacent (Outreach → Audit log) is two clicks.
Three levels of tabs when Outreach is open is the deepest the console gets;
it is still shallower than today's two rows plus sub-nav.

### Option B — Left sidebar with collapsible sections

**Concept.** A persistent 200px sidebar on the left of the content: Overview,
then the three sections as collapsible groups. The active section is
expanded, the others are collapsed to their label and a count ("Mailing list
· 6"). Outreach's views are nested under Outreach when it is active, so the
whole path Workshops → Outreach → Posts is visible as an indented tree. The
current item carries a `--accent-dim` `>` chevron — the PRD's prompt motif
doing wayfinding, which is the one place the terminal idiom genuinely helps
here.

**Desktop.**

```
 Open Circuit SF                                 Account · Sign out
 ┌────────────────┬──────────────────────────────────────────┐
 │ Overview       │ ┌ Outreach ─────────────────────────────┐ │
 │                │ │ …                                     │ │
 │ WORKSHOPS      │ │                                       │ │
 │   Workshops    │ │                                       │ │
 │ > Outreach     │ │                                       │ │
 │     > Posts    │ │                                       │ │
 │       Flyer sp.│ │                                       │ │
 │       Flyers   │ │                                       │ │
 │       Results  │ │                                       │ │
 │ MAILING LIST ▸ │ │                                       │ │
 │ SYSTEM       ▸ │ │                                       │ │
 └────────────────┴──────────────────────────────────────────┘
```

The content column gets narrower: the `.app-shell` is 1040px, so content
drops from ~1008px to ~790px unless the admin shell is widened (a
`.app-shell--wide` modifier at 1240px is a one-line change in `app.css`; at
viewports under 1240 the content still loses the sidebar's width). The
Subscribers and Audit log tables, the campaign editor's two-column preview,
and the Results table all currently use the full width.

**Phone (390px).** The sidebar cannot stay. The header gains a **Menu**
button (`aria-expanded`, `aria-controls`) showing the current path as its
label ("Workshops / Outreach"); it opens a full-height drawer from the left
with the same tree, all groups expanded. Choosing an item navigates and
closes the drawer. The drawer is a `<dialog>` so Escape, focus return, and
backdrop dismissal come from the platform, matching the `#0120` modal
conventions already in the console.

**Sub-navigation.** Absorbed into the tree: Outreach's views and Settings'
three sections are nested items. Outreach.svelte's own sub-nav and the
Settings jump links go away. (`Outreach.svelte` needs to stop rendering its
nav; the Settings scroll-to-section effect in `Admin.svelte` stays, driven by
the same `?section=` hash.)

**Active location.** The whole path is lit: section label in `--text`, the
current screen with the `>` chevron and `--accent` text, the current view
indented beneath it. `aria-current="page"` on the one link that is the page.

**Keyboard and screen reader.** The tree is a `<nav aria-label="Admin">` of
nested lists with disclosure buttons for the collapsed groups (the WAI-ARIA
*disclosure navigation* pattern: `<button aria-expanded>` controlling a
`<ul>`; links inside are plain links). Tab moves through visible items only;
Enter/Space on a group label expands it. On the phone, the drawer is a modal
dialog; focus moves into it on open and back to the Menu button on close.

**Deep links.** Unchanged. Loading `#/suppressions` opens the Mailing list
group and collapses the others; the expanded state is derived from the
route, never stored, so there is nothing to get out of sync.

**Cost: M.** Everything in A, plus: a two-column layout in `Admin.svelte`
(grid, sidebar, collapsed-group state derived from the route); a drawer with
focus handling (`<dialog>`; the open/close and "close on hashchange" logic in
a small `lib/adminDrawer.ts` so it is unit-testable); `Outreach.svelte` and
the Settings jump nav lose their own sub-navs; `app.css` gets the wide-shell
modifier; and the wide tables need a look at 1040px with 200px less room.
Three or four files more than A, and the only option that changes the
content column.

**Trade-offs.** The best wayfinding of the four — you always see where you
are and what is near — at the cost of content width, a drawer to build and
test, and a persistent column that is mostly collapsed labels for a console
whose daily use is two screens. It is the conventional admin-console answer,
and it is more console than this one needs.

### Option C — Section hubs with a breadcrumb

**Concept.** The top bar has only the four sections. Clicking a section
opens a **hub page**: one card per screen with a one-line description and,
where cheap, a live number ("Pending confirmations · 3", "Suppressions · 12",
"Workshops · 2 upcoming, 1 not announced"). Inside a screen, a **breadcrumb**
replaces the second row: `Workshops / Outreach / Posts`. The hub is where the
rarely used screens get explained; the breadcrumb is how you get back.

**Desktop.**

```
 Overview   Workshops ▍  Mailing list   System
 Workshops / Outreach / Posts                                ← breadcrumb, last segment plain
 ┌ Outreach ─────────────────────────────────────────────┐
 │ …

 (section landing, e.g. Mailing list)
 ┌ Campaigns ──────────┐ ┌ Subscribers ────────┐ ┌ Pending confirmations ┐
 │ Compose, test, send │ │ 212 active          │ │ 3 waiting             │
 └─────────────────────┘ └─────────────────────┘ └───────────────────────┘
 ┌ Deliverability ─────┐ ┌ Suppressions ───────┐ ┌ Interests ────────────┐
```

**Phone (390px).** The four section tabs in one scrolling row; the breadcrumb
wraps naturally; hub cards stack one per row. One row of chrome plus a
breadcrumb line.

**Sub-navigation.** The breadcrumb's last segment is the current view, so
Outreach's view switcher still has to exist somewhere — it stays in the
panel as in A (or the middle crumb becomes a small dropdown of siblings,
which is D's mechanism and D's cost).

**Active location.** Section tab filled; breadcrumb spells the path;
`aria-current="page"` on the last crumb (`<nav aria-label="Breadcrumb">`,
the standard pattern).

**Keyboard and screen reader.** Simplest of the four: two `<nav>`s of plain
links and a list of card links on the hub. Nothing to expand or trap.

**Deep links.** Unchanged for every existing route. The hubs need **new
routes** — `#/mailing-list`, `#/system`, and something for the Workshops hub
that does not collide with the `workshops` screen (`#/workshops-hub`, or
`#/section/workshops`). New `AdminSection` values, new `parseAdminHash`
cases, new `not-found` tests.

**Cost: M**, L with live counts. `adminRouter.ts` (three hub routes),
`Admin.svelte` (section bar, breadcrumb, mount the hub), a new
`views/admin/SectionHub.svelte` with a per-section card list (static copy is
S; the counts need `GET /admin/overview` for subscriber/pending counts — it
has them — and new or extra calls for suppressions and workshops, which is
the L).

**Trade-offs.** Hubs are the right home for the ten rarely used screens: a
card with a sentence is the only place in the console that explains what
"Deliverability" is for. But the hub is in the way of the daily path:
Workshops → Outreach becomes section → hub → card → screen, and switching
Outreach ↔ Workshops means going back up through the breadcrumb, two clicks
each way, with no sibling visible. For a console whose use is 80% two
screens, the hub taxes the common case to help the rare one — the wrong
way round. A hub also wants content to be worth the page, and three of the
four sections have two to four screens.

### Option D — Compact bar with disclosure menus, plus a "Go to" palette

**Concept.** One row: `Overview`, then `Workshops ▾`, `Mailing list ▾`,
`System ▾`. Each section is a button that opens a menu of its screens, with
Outreach's views indented under Outreach. The bar's active button reads the
*current screen* as its label — `Workshops · Outreach ▾` — so the location is
in the bar without a second row. On the right, a `Go to…` field (⌘K / Ctrl-K)
filters every screen and view by name and jumps to it.

**Desktop.**

```
 Overview   Workshops · Outreach ▾   Mailing list ▾   System ▾        [ ⌘K Go to… ]
            ┌──────────────────┐
            │ Workshops        │
            │ Outreach   ✓     │
            │   Posts    ✓     │
            │   Flyer spots    │
            │   Flyers         │
            │   Results        │
            └──────────────────┘
```

**Phone (390px).** The four buttons become one: `Workshops · Outreach ▾`,
full width, opening an accordion sheet with all three groups; `Overview` is
its first row. One row of chrome. The palette becomes a search field at the
top of the sheet.

**Sub-navigation.** Inside the menus (indented). Outreach's own sub-nav can
stay as well for in-screen switching; if it goes, every view switch is
button → menu → item.

**Active location.** The label of the active section button names the
screen; `aria-current="page"` on the menu item that is the page. Weakest of
the four when the menus are closed: one bold label in a row of four.

**Keyboard and screen reader.** The WAI-ARIA *disclosure navigation menu*
pattern, not `role="menu"`: `<button aria-expanded aria-controls>` opening a
`<ul>` of plain links; Escape closes and returns focus; optional arrow-key
movement between items; clicking outside closes. The project has no such
component; a `<details><summary>` gets the open/close for free but needs JS
for Escape and outside-click, and Safari's `summary` focus handling has
needed workarounds before. The palette is a `<dialog>` with a text input and
a filtered list (`aria-activedescendant` or a plain list of links). Pure
filtering logic goes in `lib/commandPalette.ts` and is trivially testable.

**Deep links.** Unchanged. The palette is *built from* the same tables
(`ADMIN_NAV_GROUPS`, `ADMIN_SECTION_LABELS`, the Outreach and Settings view
lists), so it cannot name a screen that does not exist.

**Cost: M** for the menus (`views/admin/NavMenu.svelte` + `lib/navMenu.ts`
for the key handling + tests; `Admin.svelte` bar; phone sheet), **+S** for
the palette (`lib/commandPalette.ts` + a dialog in `Admin.svelte`). M–L
together.

**Trade-offs.** Densest chrome, best for a keyboard-first single operator,
and the palette is a genuine pleasure once learned. But every navigation is
two actions (open, choose) and nothing is visible until opened, which is
P1's opposite failure: instead of everything at once, nothing at all. A
menu that opens on click (never hover — hover menus fail on touch and on
keyboards) is one more interaction model to get right, and the console has
none today.

### Side by side

| | A · tabs + row | B · sidebar | C · hubs | D · menus + ⌘K |
|---|---|---|---|---|
| Clicks, Workshops ↔ Outreach | 1 | 1 | 2 | 2 |
| Clicks, Outreach → Audit log | 2 | 1–2 | 2 | 2 |
| Where am I, menus closed | strong | strongest | good | weak |
| What else is here, without clicking | this section | this section (+ others collapsed) | nothing (breadcrumb) | nothing |
| Rows of chrome at 390px | 2 | 1 + drawer | 1 + crumb | 1 |
| Content width | unchanged | −200px | unchanged | unchanged |
| New routes | none | none | 3 hubs | none |
| New interaction model | none | drawer | none | disclosure menu, palette |
| Cost | **S** | M | M (L with counts) | M–L |
| Files | 3 | 7–8 | 5–6 | 6–8 |

---

## 4. Recommendation

**Option A**, with §2's IA, the header cleanup, and one shared sub-nav style
for level three. Then, only if wanted, D's palette as an add-on — it is
orthogonal to A and would be the one piece of D worth having.

Why A:

1. **It answers the complaint directly and cheaply.** "Organize by sections
   so they don't all have to be visible" is, literally, row one showing
   sections and row two showing one section's screens. Fourteen visible
   links become four plus at most six, usually four plus two.
2. **It fits the frequency profile.** Workshops and Outreach are siblings on
   row two the moment you are in Workshops; Campaigns is one click from
   anywhere via Mailing list; the ten rare screens are two clicks away and
   each is explained by the section it appears under. B and D give no
   better than this on the daily path; C is worse on it.
3. **It changes nothing structural.** No new routes, no layout change, no
   drawer, no menu component, no new interaction model to make accessible.
   The content column stays the width every table and editor was built for.
   The `#0527` hash contract, the `aria-current` links, and the focus-outline
   guard all carry over untouched if `.subtab` keeps its name.
4. **It already looks like the site.** Row one is the existing
   `.nav-tab.active` style (the filled Account/Admin tab), row two is the
   existing `.subtab.active` underline without the box. Nothing new to
   design, just less of it.
5. **It leaves room.** If a second admin someday needs the whole map at
   once, B's sidebar is a layout change on top of the same two tables; if
   Brennan wants ⌘K, D's palette is a dialog on top of the same two tables.
   A does not foreclose either.

Why not the others, in one line each: B spends content width and a drawer
on wayfinding a one-person console does not need; C taxes the daily path to
explain the rare screens, which a label change does more cheaply; D hides
everything behind a click and brings a menu component the console does not
have.

Two details inside A worth deciding now:

- **Overview stays a tab, not the Workshops landing.** The Needs-attention
  box is the reason the screen exists (`#0061`), and `#0586` already made it
  report unannounced workshops and disconnected outreach services. Leave it
  first; make sure it keeps pointing at the right screen.
- **No underlines on nav links.** Set `text-decoration: none` on both rows
  (the `:where(a, .nav-tab, .subtab):focus-visible` rule in `app.css` keeps its
  `!important` underline for keyboard focus, so nothing is lost). The pills'
  underlines are the single biggest contributor to the "wall of links" feel.

---

## 5. Implementation plan

Small enough for two issues; the third is optional and independent.

**Issue 1 — Admin nav: section tabs with a contextual screen row (S).**

1. `web/src/lib/adminRouter.ts`: reorder `ADMIN_NAV_GROUPS` to Workshops ·
   Mailing list · System and Mailing list's screens to Campaigns first; add
   `landing` to each group; add `sectionGroup(section)` returning the group
   (or `null` for `overview`/`not-found`); relabel `pending` → "Pending
   confirmations" and `crt` → "Home screen" in `ADMIN_SECTION_LABELS`.
   `ADMIN_NAV_SECTIONS` stays derived, so every hash still parses. Tests in
   `adminRouter.test.ts`.
2. `web/src/views/Admin.svelte`: replace the `<nav class="subtabs">` block
   with two `<nav>`s (`aria-label="Admin sections"`, `aria-label="{group}
   screens"`), row two rendered only when `sectionGroup()` is non-null;
   delete the `<select>`; row-one style from `.nav-tab`, row-two from
   `.subtab` minus the box; `text-decoration: none` on both; ≤480px: both
   rows `overflow-x:auto; white-space:nowrap`. Keep the `.subtab` class on
   row-two links so `focusOutline.guard.test.ts` and `app.css`'s `:where`
   rules need no change.
3. Header: remove the Account/Admin `.nav-tabs`; render "Account" as a plain
   link next to Sign out. (`Account.svelte`'s header is out of scope.)
4. `web/src/views/Admin.nav.behavior.test.ts`: rewrite — row one has four
   links in order with the right `href`s; row two shows only the active
   group's screens, in order, with `aria-current="page"` on the active one;
   no row two on `#/`; `#/outreach?view=results` lights Workshops/Outreach;
   `#/nope` lights nothing; the select is gone. `Admin.hashRouting.behavior.test.ts`
   criterion 4 ("the active subtab is a real link with aria-current") should
   pass unchanged.
5. Evidence: `ISSUE=NNNN scripts/check.sh web`, plus a Tart VM render at
   390px and 1040px of `#/`, `#/outreach`, `#/settings?section=outreach` —
   the real-browser proof `#0586`'s review said was still owed.

**Issue 2 — One sub-nav style for Outreach and Settings (S).**

1. `web/src/lib/SubNav.svelte` (or `views/admin/`): a `<nav aria-label>` of
   links with `aria-current`, styled as small chips (`--fs-base`,
   `--text-muted`, active `--text` + 2px `--accent` edge), horizontally
   scrolling under 480px.
2. `Outreach.svelte` uses it for Posts · Flyer spots · Flyers · Results;
   `Admin.svelte`'s Settings jump nav uses it for General · Server settings ·
   Outreach, keeping the `onclick` scroll.
3. Tests: the existing `OutreachList.behavior.test.ts` sub-nav assertions and
   `Admin.hashRouting.behavior.test.ts`'s Settings-sections assertions keep
   passing against the component's markup.

**Issue 3 (optional) — "Go to…" palette (S–M).** `lib/commandPalette.ts`
(build the entry list from `ADMIN_NAV_GROUPS`, `ADMIN_SECTION_LABELS`, the
Outreach views and Settings sections; filter by substring, case-folded;
pure, tested) and a `<dialog>` in `Admin.svelte` opened by a header button
and ⌘K/Ctrl-K, listing matches as links. Only if Brennan wants it; A is
complete without it.

---

## 6. What this review could not determine

- **Usage frequencies are inferred**, from the outreach review's §2 and from
  what each screen does, not measured. The audit log would give real
  numbers for the mutating screens; nothing records reads. If Campaigns are
  in practice as frequent as Outreach, the IA still holds (both are first in
  their section).
- **No browser was run**, per AGENTS.md §8. The HTML mockup was written
  against the real tokens and checked by reading, not by rendering; the
  phone frames assume `--fs-base` 13px text and the 8px grid, which is how
  the claim "row one fits at 390px" was estimated. The mockup also cannot
  load the self-hosted Archivo and JetBrains Mono fonts, so it renders in the
  system fallbacks named in `app.css`.
- **Whether Brennan wants Account folded into the header** (§3, common to
  all options) — it is recommended, not required; Issue 1 step 3 is
  separable.
- **Whether "Home screen" is the right new name for CRT session**, or whether
  Brennan would rather keep the label and move the screen to a fourth "Site"
  section with Settings. Three sections is the recommendation; the label is
  the cheaper fix.
