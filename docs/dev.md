# Local Development

Run the full Open Circuit SF app on your Mac for fast UI iteration — **no
PostgreSQL, no systemd, no migrations required.**

## Quick start (hot-reload)

```bash
./scripts/dev.sh
```

Then open **http://localhost:5173** in your browser.

You land on the account view already signed in as the mock admin
(`admin@localhost`). No passkey ceremony, no setup.

## What happens

- The Go API server starts on `:8080` using `STORAGE=json` — an in-memory dev
  store (`internal/devstore`) pre-seeded with a mock admin user.
- The dev auto-login middleware (`internal/middleware.DevAutoLogin`) injects a
  session cookie on every unauthenticated request, so the account view is
  immediately accessible.
- The Vite dev server starts on `:5173` and proxies `/api`, `/auth`,
  `/account`, and `/admin` to the Go server on `:8080`. There is no `/u`
  redirect namespace in this project — that belongs to the separate
  ShortLinks deploy at `go.opencircuitsf.com`.
- Svelte HMR keeps the browser in sync with source changes in `web/src/`.
- Ctrl-C stops both processes cleanly.

## Built-SPA mode

To test the production embedding (the same `//go:embed` path used in production):

```bash
./scripts/dev.sh --built
```

This runs `npm run build` first, then `go run ./cmd/opencircuit serve`,
serving the embedded SPA at **http://localhost:8080**. Useful for verifying
asset embedding, favicon, and SPA deep-link handling before deploying.

## Environment variables

All variables have sensible defaults — no `.env` file is needed. Override any
of them before calling the script:

| Variable | Default | Description |
|----------|---------|-------------|
| `STORAGE` | `json` | Must stay `json` for dev mode |
| `PORT` | `8080` | Go server port |
| `BASE_URL` | `http://localhost:${PORT}` | Public base URL — derives from `$PORT` (#0213), so it moves with an override rather than staying fixed at `:8080` |
| `WEBAUTHN_RP_ID` | `localhost` | WebAuthn relying-party ID |
| `WEBAUTHN_RP_ORIGIN` | `http://localhost:${PORT}` | WebAuthn origin — also derives from `$PORT` (#0213) |
| `SESSION_SECRET` | *(dev value)* | HMAC signing key — insecure, dev only |
| `ADMIN_EMAIL` | `admin@localhost` | Mock admin email |

`$PORT` is listed first because `BASE_URL` and `WEBAUTHN_RP_ORIGIN` are
defined in terms of it — `scripts/dev.sh` sets `PORT` before either, for the
same reason.

Example override:

```bash
ADMIN_EMAIL=me@example.com PORT=9090 ./scripts/dev.sh
```

With this override, `BASE_URL` and `WEBAUTHN_RP_ORIGIN` both default to
`http://localhost:9090`, not `:8080` — they track whatever `$PORT` is set to.

## Production path is unchanged

`scripts/dev.sh` does not touch `scripts/deploy.sh`, the systemd unit, or any
Postgres migration files. The dev store is engaged solely by `STORAGE=json`
and is refused if that variable is not set — `internal/config.Config.DevMode`
gates it, and `cmd/opencircuit`'s `serve` command only ever constructs
`internal/devstore.Store` on the `DevMode() == true` path (`serveDevMode`);
the production path (`servePostgres`) never imports or references it.

---

## Verifying mobile rendering (iOS Simulator)

The iOS Simulator runs **genuine mobile Safari**, so it can expose rendering
differences that desktop browsers hide — narrow-viewport layout, form control
sizing, and dark-mode colors in particular. This site has a real mobile
audience arriving from social apps, so this check is worth keeping in the
loop as marketing and mailing-list views land in later phases.

### Prerequisites

- **Xcode** installed (App Store or `xcode-select --install`)
- At least one **iOS Simulator runtime** installed
  (Xcode → Settings → Platforms → add an iOS runtime if none is listed)
- The Open Circuit SF app must be **running locally** before opening the
  Simulator

### Quick workflow

```bash
# 1. Start the app (built-SPA mode so the simulator hits the embedded assets)
./scripts/dev.sh --built

# 2. In a second terminal, boot the simulator and open the app
./scripts/sim.sh

# 3. The script prints the screenshot path (it names the actual file written,
#    e.g. /tmp/opencircuit-sim-12345.png — see OUTPUT_PATH below); open that
open /tmp/opencircuit-sim-<pid>.png
```

### `scripts/sim.sh` options

```
./scripts/sim.sh [URL] [DEVICE_NAME] [OUTPUT_PATH]
```

| Argument | Default | Description |
|----------|---------|-------------|
| `URL` | `http://localhost:8080` | URL to open in Safari |
| `DEVICE_NAME` | `iPhone 17` | Simulator device name |
| `OUTPUT_PATH` | `/tmp/opencircuit-sim-$$.png` | Where to save the screenshot — namespaced by the run's own pid (#0207) so two concurrent agents running `sim.sh` with default arguments don't overwrite each other's screenshot |

Examples:

```bash
# Default — iPhone 17 at localhost:8080
./scripts/sim.sh

# Different device
./scripts/sim.sh http://localhost:8080 "iPhone 17 Pro"

# Custom output path
./scripts/sim.sh http://localhost:8080 "iPhone 17" ~/Desktop/account.png
```

If the simulator is already booted, `sim.sh` reuses it (no error).

### What to check

- **Responsive layout**: nav collapses, tables scroll horizontally, no
  clipped content at narrow viewport widths (CLAUDE.md §8 notes headless
  Chromium's ~485px minimum window width, which the Simulator does not
  share — the Simulator is the more trustworthy check for anything narrower).
- **Dark mode**: colors match the design tokens on both Light and Dark
  system settings (toggle in the Simulator's Settings app or via the Feature
  menu). See `docs/design.md` once #0011/#0013 land the terminal design
  tokens.
- **Favicon**: the mark favicon should appear in the Safari address bar and
  in the browser's tab strip (CLAUDE.md §8: use the `mark-*` assets below
  ~96px, never the full logo).

### Limitations

- This is **manual visual inspection**, not automated E2E testing. There are
  no assertions; you review the screenshots yourself.
- First boot of a simulator can take 30–60 seconds.
- `sim.sh` always uses the first matching device name across all installed
  runtimes; if you have both iOS 17 and iOS 18 runtimes you may get either
  one. Pass the UDID directly via `xcrun simctl boot <UDID>` for precise
  control.
- The simulator must be able to reach `localhost` on the Mac (it always can —
  the simulator shares the Mac's network stack).
