# Open Circuit SF

The website for Open Circuit SF at [opencircuitsf.com](https://www.opencircuitsf.com):
public marketing pages and workshop listings, an interest-segmented mailing
list with double opt-in and full unsubscribe compliance, and a
passkey-authenticated admin console for staff. A single Go binary with an
embedded Svelte 5 SPA, backed by PostgreSQL and AWS SES, hosted on EC2 behind
Apache.

The link shortener at [go.opencircuitsf.com](https://go.opencircuitsf.com) is
a **separate deploy** of [ShortLinks](https://github.com/brennanMKE/ShortLinks).
No shortener code lives in this repository.

## Features

- **Public site** — home, workshop listings and detail pages, about, inline
  and standalone subscribe forms (`PRD.md` §5.1).
- **Mailing list** — interest-segmented double opt-in signup, a
  token-authenticated preference center, and three independent unsubscribe
  paths (one-click header, in-body link, and inbound `mailto:`), all required
  by CAN-SPAM (`PRD.md` §6).
- **Campaigns** — compose, preview, test-send, target, schedule, and send to
  a segment over AWS SES, with per-campaign delivery/bounce/complaint stats.
- **Workshops** — admin-managed listings with an "announce to list" shortcut.
- **Admin console** — passkey (WebAuthn) authentication, staff user
  management, subscriber search and manual add/suppress, audit log, and
  runtime settings — all carried over from ShortLinks' proven auth stack.
- **No third-party analytics, ad trackers, external CDNs, or email
  open-tracking pixels.** The site is self-contained by design
  (`CLAUDE.md` §9).

Not every feature above is built yet — this repository is worked phase by
phase against `PRD.md` §12's build plan and the issue tracker in `issues/`.
Phase 0 (this foundation) carries over the auth/admin skeleton from
ShortLinks; the mailing list, campaigns, and workshops land in later phases.

## Stack

| Layer | Technology |
|---|---|
| Backend | Go 1.26, standard library `net/http` (Go 1.22+ pattern routing) |
| Database | PostgreSQL, schema managed by `golang-migrate` |
| Frontend | Svelte 5 + Vite 6 + TypeScript, built to `web/dist/` and embedded into the Go binary via `//go:embed` |
| Auth | WebAuthn passkeys ([go-webauthn](https://github.com/go-webauthn/webauthn)), server-side sessions |
| Email | AWS SES (transactional + campaign sending), SNS webhook for bounce/complaint ingestion |
| Hosting | Single EC2 instance, Apache 2 reverse proxy, Let's Encrypt TLS |
| Testing | Go's standard `testing` package + a live PostgreSQL for integration tests; Vitest for the SPA's pure-TypeScript helpers |

See `CLAUDE.md` for the full project identity, build/verify commands, and
code conventions, and `PRD.md` for product scope, schema, and design
decisions.

## Quick start

No PostgreSQL, no migrations, no `.env` file needed for local UI iteration:

```bash
./scripts/dev.sh
```

Open **http://localhost:5173** — you land on the account view already signed
in as a mock admin. See `docs/dev.md` for the full walkthrough, environment
variable overrides, and the iOS Simulator mobile-rendering workflow.

## Production build

```bash
cd web && npm run build && cd ..
go build -o opencircuit ./cmd/opencircuit
```

`go build ./...` alone is **not** sufficient verification — see `CLAUDE.md`
§5 for the required `go vet`/`go test`/`npm run check`/`npm test` sequence.
Full production topology (Apache vhost, systemd unit, DNS, SES setup) is
documented in `docs/deployment.md`.

## Documentation

Start with [`docs/README.md`](docs/README.md) for the full documentation
index, or [`CLAUDE.md`](CLAUDE.md) for the binding project conventions any
agent (human or AI) working in this repository should read first.

## Issue tracking

Work is tracked in `issues/` — see [`issues/Issues.md`](issues/Issues.md) for
the workflow (plan → implement → review), status vocabulary, and commit
conventions.

## License

See [`LICENSE`](LICENSE).
