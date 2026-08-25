# Open Circuit SF — Documentation

Developer and operator documentation for the Open Circuit SF website. Start
with the architecture overview, then dive into the subsystem you need.

Most of these documents describe a system still being built phase by phase —
see `PRD.md` §12 for the build plan and `issues/` for the tracker. A doc
whose subsystem hasn't landed yet is a stub with real headings rather than
full content; it gets filled in by the issue that builds that subsystem, so
there's always a place to write instead of a documentation-debt cleanup
later.

## Overview & setup

- [Architecture overview](architecture.md) — components, request lifecycle, tech stack
- [Configuration & environment variables](configuration.md) — every config value the service reads
- [Local development](dev.md) — `./scripts/dev.sh`, the in-memory dev store, iOS Simulator checks
- [Deployment & operations](deployment.md) — EC2 / Apache / systemd setup and redeploys

## Data & backend

- [Database schema & migrations](database.md) — tables, `golang-migrate`, the `seed` command
- [Authentication & sessions](auth.md) — sessions, the guard middleware, the registration gate, admin authorization
- [Passkeys / WebAuthn](passkeys.md) — the registration, login, and recovery ceremonies

## Mailing list

- [Mailing list](mailing-list.md) — interest taxonomy, subscription flow, campaigns, sending engine
- [Email setup (AWS SES)](email-setup.md) — the `mailing.` sending subdomain, DKIM, custom MAIL FROM, DMARC, event ingestion, production access
- [AWS IAM setup for SES](aws-iam-setup.md) — the instance role the service needs to reach SES, and the console steps to create it
- [Unsubscribe & list hygiene](unsubscribe.md) — the three required unsubscribe paths, suppression, bounce/complaint handling

## Frontend & brand

- [Frontend / SPA](frontend.md) — the Svelte 5 app, routing, and the build/embed pipeline
- [Brand & design system](design.md) — terminal-inspired design tokens, motifs, logo assets
- [SEO & social preview cards](seo.md) — server-injected meta tags, sitemap, structured data

## Working the tracker

- [Obstacles](obstacles.md) — what stops work on an issue, the signal for each, and how to clear it before dispatch

## Project conventions

- [`CLAUDE.md`](../CLAUDE.md) — binding project-wide guidance: identity, model policy, build/verify commands, restricted areas
- [`issues/Issues.md`](../issues/Issues.md) — the issue workflow: statuses, the plan → implement → review pipeline, commit conventions
- [`PRD.md`](../PRD.md) — product scope, schema, flows, brand, infrastructure (extract the section you need — never read it whole)
