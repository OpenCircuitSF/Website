# Frontend / SPA

Svelte 5 + Vite 6 + TypeScript, built to `web/dist/` and embedded into the
Go binary at compile time via `//go:embed all:dist` (`web/embed.go`). No
component framework, no CSS framework, no runtime dependencies beyond
Svelte. Vitest covers the pure-TypeScript helper modules; `svelte-check`
covers types. This stack is unchanged from ShortLinks (`PRD.md` §7.1).

## Build and embed

```bash
cd web && npm run build   # emits web/dist/{index.html,assets/*}
go build -o opencircuit ./cmd/opencircuit   # embeds whatever is currently in web/dist/
```

`web/dist/index.html` is committed as a minimal placeholder so
`go build ./...` compiles from a clean checkout *before* any `npm run
build` has run — `web/dist/*` is otherwise gitignored (`!web/dist/index.html`
is the one exception). Never commit a real built `index.html` over the
placeholder.

## Structure

```
web/src/
├── App.svelte       — root component; view switch (no router yet, see below)
├── main.ts          — Svelte 5 imperative mount
├── app.css          — global styles / design tokens (see design.md)
├── views/           — one .svelte file per top-level screen
└── lib/             — pure TS helpers + shared UI primitives (Panel, Button, Footer)
```

## Current views (Phase 0)

| View | Purpose |
|---|---|
| `Login.svelte` | Passkey autofill + explicit sign-in, plus a register sub-form |
| `Account.svelte` | The signed-in user's own passkey management |
| `Admin.svelte` | Settings toggle, user management, audit log (admin-only) |
| `RegisterVerify.svelte`, `RecoverVerify.svelte` | Magic-link landing pages |

Every shortener-specific view (`Dashboard`, `LinkDetail`, `CampaignsList`,
`CampaignDetail`) and their supporting `lib/` modules were deleted in
`#0003`. The marketing/mailing-list public views (`Home`, `WorkshopsIndex`,
`WorkshopDetail`, `About`, `Subscribe`, `PreferenceCenter`, `Unsubscribe`
— `PRD.md` §5.1) land in Phase 2+ and are this project's own, not a port.

## Navigation (temporary — replaced by `#0014`)

There is **no client-side router yet**. `App.svelte` reads
`stores.ts`'s `currentView` writable and renders the matching view directly
— navigation between the currently-small view set is a store write, not a
URL change. `#0014` ("Add a History API path router") replaces this
wholesale once the public marketing routes in `PRD.md` §5.1 need real,
bookmarkable, shareable URLs.

## State

`web/src/lib/stores.ts` — `currentView` (the active view), `currentUser`
(from `GET /api/me`), `pendingVerifyToken` (the magic-link token parsed
from the landing URL before the SPA takes over routing).

## API client

`web/src/lib/api.ts` — a typed `fetch` wrapper (`apiGet`/`apiPost`/
`apiPatch`/`apiDelete`, all same-origin with `credentials: 'include'`) plus
one function per backend endpoint the SPA actually calls, each documented
with its request/response shape. Throws a typed `ApiError` (carries the
HTTP status and parsed error body) on any non-2xx response.

## Real-time updates (SSE)

`web/src/lib/events.ts` — a generic `subscribeEvent<T>(eventName, onEvent,
factory)` helper over the browser's `EventSource`, parameterized by event
name and payload type. Originally hardwired to ShortLinks'
`link.created` frames; generalized in `#0003` since that specific event no
longer exists here. `#0048` (live campaign send progress) is the first
consumer in this project.

## Testing

```bash
npm run check   # svelte-check — type errors across .svelte and .ts
npm test        # vitest run — pure-TS helper modules only, no DOM
```

Both must pass, in addition to the Go suite, for any issue touching `web/`
(`CLAUDE.md` §5). There is no component-level (DOM) testing in this stack —
`lib/*.test.ts` files test pure functions extracted out of the `.svelte`
files specifically so they're unit-testable without a browser.

## Where to look

| Concern | File |
|---|---|
| Root component / view switch | `web/src/App.svelte` |
| Global state | `web/src/lib/stores.ts` |
| API client | `web/src/lib/api.ts` |
| Shared types (mirrors Go JSON shapes) | `web/src/lib/types.ts` |
| Design tokens | `web/src/app.css`, [`design.md`](design.md) |
| Vite dev-server proxy | `web/vite.config.ts` |
