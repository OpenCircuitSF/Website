# Deployment & Operations

**The site is deployed.** Since **2026-08-25**, `www.opencircuitsf.com` is
served by `opencircuit.service` — the Go binary with the Svelte SPA embedded —
behind Apache on the existing EC2 instance, replacing the static placeholder.
The deferral recorded here previously (user, 2026-08-23) is spent.

Read the rest of this document knowing **which parts that deploy actually
exercised**, because it did not exercise all of them:

| Section | Status |
|---|---|
| Production facts, prerequisites, PostgreSQL, migrations, systemd, Apache, TLS, verification | **Followed on the real host** and corrected where it was wrong. The corrections are inline. |
| SES setup, DNS records for DKIM / MAIL FROM / inbound, IAM policy, the account-level suppression list | **Still not followed by this row's original deploy.** SES was deliberately left unconfigured for that deploy so it could be set up afterwards — see "SES is not configured yet" below. No AWS SES identity existed at that point, and ~~**the instance has no IAM role attached at all**~~ **— corrected `#0426`, 2026-09-04: that has since changed.** A later pass (`docs/aws-iam-setup.md`, same day) attached the `opencircuit-instance` role and set up SES; `CLAUDE.md` §10 item 2 and the `## IAM` section below (also corrected, `#0426`) are the current-state record. |
| Backups (`opencircuit-backup.timer` and friends) | **Not installed yet.** The units exist in `deploy/systemd/`; nothing on the box runs them. `#0435` (2026-09-08) re-confirmed this read-only, found a second blocker (the on-box checkout is missing `#0434`'s media-backup scripts), and wrote an approval-ready fix in `deploy/systemd/README.md` — see the "Backups" section below. |

So `#0064`'s acceptance criterion — "the whole runbook followed once on a
clean instance and corrected where it was wrong" — is now **partly** met: the
serving path is proven, the email path is not. `PRD.md` §10 and `CLAUDE.md`
§7/§10 remain authoritative wherever this document is silent or wrong.

### SES is not configured yet (historical — the section title describes the 2026-08-25 deploy, not today)

Deliberate, at the user's direction: bring the site up first, configure SES
after. Two things follow that are easy to get wrong.

**Correction (`#0431`, 2026-09-04): SES has since been configured.** The
deferral this heading describes ended the same day it started — see
`docs/email-setup.md` ("Configured 2026-08-25") and `CLAUDE.md` §10 item 2 for
the current-state record: the `mailing.opencircuitsf.com` identity is
verified, DKIM and custom MAIL FROM are `SUCCESS`, and a real message has been
delivered through it. The account remains **sandboxed** (production access
still pending) — but production's own `/etc/opencircuit/config.env`, read
read-only on 2026-09-03, carries `SES_SANDBOX=false` **and
`SEND_WORKER_ENABLED=true`** (`#0415`, `#0057`'s facts table, `#0423`), which
is the flip §10 item 2 says to make only *after* access is granted. `#0415`
is open to establish `ProductionAccessEnabled` from AWS and owns reconciling
the two — do not take either side as settled from this document. That
variable gates only the **campaign** send worker (`internal/mailing/worker.go`'s
own doc comment: "the only place a *campaign* moves scheduled -> sending ->
sent/failed"), not the `outbound_queue` that transactional mail (a recovery
magic link, a subscription confirmation) rides on — so the bullets below,
written when no SES identity existed at all, no longer describe the current
constraint on those messages the way they did when this section was written.
Whether the first-admin recovery flow has actually been exercised against the
live site is a separate question, addressed in **First admin login** below.

**`MAILER_NOOP=true` is not the way to express this in production.**
`cmd/opencircuit/main.go`'s `checkMailerNoOp` refuses to start unless
`BASE_URL`'s host is `localhost` or `127.0.0.1`, so a production host can
never silently disable outbound email. Setting it here just crash-loops the
service. `/etc/opencircuit/config.env` therefore leaves it unset and
constructs the real SES v2 mailer. (It does **not** additionally disable the
campaign worker: the same 2026-09-03 read shows `SEND_WORKER_ENABLED=true` —
see the correction above and `#0415`.)

**`SES_CONFIGURATION_SET` is required, despite `docs/configuration.md`
listing it as optional.** `mailing.NewSESMailer` returns `cannot construct SES
mailer: missing SES_CONFIGURATION_SET` and the service will not boot without
it. The named set did not have to exist in SES at the time this was written —
**it does now**: `opencircuit-transactional` is live (`docs/aws-iam-setup.md`'s
facts table), corrected `#0431`, 2026-09-04.

What this cost during the pre-SES gap (2026-08-23 – 2026-08-25), historical —
not the current constraint, per the correction above:

- Every outbound message goes through the durable `outbound_queue` (`#0126`),
  so nothing is lost in flight — it is enqueued, the send fails, and the outbox
  worker retries on the six-step backoff up to `queue_max_retries` (8) before
  marking the row `abandoned`. This mechanism is unchanged today; what changed
  is that a queued send can now actually succeed once it reaches SES.
- **The seeded admin could not sign in.** `opencircuit seed` created the
  `ADMIN_EMAIL` user with no passkey, so first sign-in is "Recover account",
  which mails a magic link. With SES now live this is no longer blocked the
  same way, though this pass did not itself exercise the recovery ceremony
  against the live site — see **First admin login** below for what is and
  is not verified there.
- A visitor who subscribes previously got the uniform `202` (`#0026`) but no
  confirmation mail; with SES live, a subscribe request can now actually
  deliver one, subject to the sandbox's verified-recipient restriction
  (`CLAUDE.md` §10 item 2) until production access is granted.

## Current production facts (`CLAUDE.md` §7)

| | |
|---|---|
| Canonical host | `https://www.opencircuitsf.com` — apex and plain HTTP both 301 to it |
| Server | Apache 2.4.68, Amazon Linux, OpenSSL 3.5.7 |
| TLS | Let's Encrypt, valid to 2026-11-16 |
| Already on the box | PostgreSQL and Apache — plus `opencircuit.service` itself, serving the site since 2026-08-25 (this row is a current-state record, corrected `#0431`, 2026-09-04) |
| Currently served | **This project** (`opencircuit.service`), since 2026-08-25 — the static placeholder is gone. **Corrected `#0431`, 2026-09-04**; re-verified read-only against the live host (`curl -sI https://www.opencircuitsf.com/` returns `Server: Apache/2.4.68 (Amazon Linux)` fronting the Go service, matching the detailed facts table below) |

**A box already exists and already runs Apache and PostgreSQL** — the
production facts above are measured, not aspirational. **This project's own
configuration now exists on that box too**: `opencircuit.service` has served
`www.opencircuitsf.com` since **2026-08-25**, replacing the static
placeholder. The operational details `CLAUDE.md` §10 item 6 asked for were
captured during that deploy and are recorded below — measured on the box, not
guessed.

| Fact | Value |
|---|---|
| Instance ID | `i-0e3bd89e87d1c2364`, hostname `bluesky.sstools.co` |
| Instance size / type | `t4g.nano` (ARM/Graviton, Amazon Linux 2023, kernel 6.1 aarch64) — **not** the `t4g.small` PRD §10.1 assumes. 418 MB RAM, backed by a 418 MB zram device plus a 2 GB swapfile; 20 GB root, 53% used. `opencircuit` itself sits at ~14 MB RSS, so the box is tight rather than strained — but it also runs Apache, PostgreSQL, two ShortLinks instances and a prototypes service. `go build` is the memory-hungry step; it succeeds, but it is the thing to suspect if a deploy is ever OOM-killed. |
| Region | **`us-east-1`** (az `us-east-1b`) — **not** the `us-west-2` PRD §10.3 assumed (§10.1 is the topology diagram and never named a region — this cell's own citation dangled on that point until now, #0421, 2026-09-04). ~~PRD §10.3 picks `us-west-2` for *SES*, which is a separate choice from where the instance lives.~~ **Correction (#0418, 2026-09-03):** SES is in `us-east-1` too — the instance and SES share one region, verified against instance metadata `placement/region` and `AWS_REGION` in `/etc/opencircuit/config.env`. PRD §10.3 has been corrected to match. Inbound receiving (PRD §6.5 path 3) is the region-pinned part and pins to that same region. |
| Public IP / DNS | `44.222.209.183`. `www.opencircuitsf.com` and `opencircuitsf.com` are A records to it; `go.opencircuitsf.com` is a CNAME to `ec2.smallsharptools.com`, which resolves to the same address. |
| SSH access | `ssh ec2` from the maintainer's Mac — host `ec2.sstools.co`, user `ec2-user`, key `~/.ssh/sstools-ec2.pem`. `ssh ec2-db` is the same host plus a `LocalForward 15432 → localhost:5432` Postgres tunnel (port 15432, not 5432, because a local PostgreSQL already owns 5432 on the Mac). |
| IAM instance role | ~~**None attached** — the instance metadata service 404s `iam/security-credentials/`. This is the reason SES cannot work yet even after the domain is verified: the AWS SDK's default credential chain has nothing to find, so `docs/configuration.md`'s "the EC2 instance role supplies them" is currently false.~~ **Correction (`#0426`, 2026-09-04):** attached — `opencircuit-instance`. Re-derived for this issue directly from the instance metadata service (`iam/security-credentials/`, read-only via `ssh ec2`), not copied from the filing; `CLAUDE.md` §10 item 2 already records this role as attached and proven by a real delivered send. Attaching a role with `ses:SendEmail`/`ses:SendRawEmail` was the prerequisite `CLAUDE.md` §10 item 2 named, and is now done — the `## IAM` section below (also corrected, `#0426`) describes the policy's actual shape. certbot's renewal never depended on this role — see the certbot row. |
| `DocumentRoot` (former static placeholder) | `/var/www/vhosts/www.opencircuitsf.com`. The placeholder HTML is no longer reachable — the Go service answers `/` — but the directory stays, because `/.well-known/` is still served from disk out of it. See "The `/.well-known/` exception" below. |
| Installed vhost file(s) | `/etc/httpd/conf.d/001-www.opencircuitsf.com-le-ssl.conf` (the proxy vhost) and `001-www.opencircuitsf.com.conf` (port 80 → HTTPS). The apex and every other `*.opencircuitsf.com` name is redirected to `www` by `002-opencircuitsf.com{,-le-ssl}.conf`, which sort *after* the 001 files. **These are not copies of `deploy/apache/opencircuitsf.com.conf`.** That file is one self-contained vhost that does its own apex→www redirect; the box splits the same behaviour across the certbot-managed 001/002 pair it already had, and adding the repo file verbatim would duplicate `ServerName www.opencircuitsf.com`. Edit the installed files; treat the repo file as the reference for the proxy / header / CSP block only. |
| certbot renewal schedule | `certbot-renew.timer` (systemd), firing twice daily at 00:00 and 12:00 UTC. The cert named `opencircuitsf.com` is a single ECDSA **wildcard** covering `opencircuitsf.com` and `*.opencircuitsf.com`, so one cert serves `www`, `go`, and any future subdomain. The authenticator is **`dns-route53`**, not `--apache`: renewal proves control over the domain through a Route 53 TXT record and never reads the vhosts or `/.well-known/acme-challenge`, so no Apache change in this project can break it. Expiry at deploy time: 2026-11-16. |
| Is the existing Postgres the target for `opencircuit`? | **Yes.** One PostgreSQL **15.18** cluster on `127.0.0.1:5432` now holds `shortlinks`, `shortlinks_ocsf`, and `opencircuit` (owner: login role `opencircuit`, password auth over TCP). Note the major version: PRD §10.1 assumes 16, and `#0062`/`#0228`'s drills ran on 16.14, Homebrew — neither matches what is actually installed here. This document's Prerequisites (§1, below) state 15.18 to agree with what is on the box. All 22 migrations applied cleanly on 15.18 and the whole Go suite passes against PostgreSQL 15.14, so 15 is proven for this schema — but nothing in the tree pins it, so do not introduce 16-only syntax without upgrading the box first. |

### The `/.well-known/` exception

`https://www.opencircuitsf.com/.well-known/atproto-did` holds this domain's
Bluesky DID (`did:plc:olbggqhj2rwqv56ik53kvwfx`) and **must keep working
across this and every future deploy.** The Go service has no `/.well-known/`
route and answers `404` there, so the vhost carries an explicit exclusion
ahead of its proxy rules:

```apache
ProxyPass /.well-known/ !
Alias /.well-known/ /var/www/vhosts/www.opencircuitsf.com/.well-known/
<Directory "/var/www/vhosts/www.opencircuitsf.com/.well-known">
    AllowOverride None
    Options -Indexes +FollowSymLinks
    Require all granted
</Directory>
```

Two things about this are load-bearing:

- **Order.** Apache evaluates `ProxyPass` top-to-bottom, so the `!` exclusion
  has to precede `ProxyPass /`. The same rule is why `/api/events` sits above
  it.
- **On disk, not in the app.** Serving the DID from the filesystem means a
  redeploy, a crashed binary, or a deliberately stopped `opencircuit.service`
  cannot take Bluesky handle verification offline with it.

Verify it after any Apache or deploy change — a byte comparison, not just a
`200`:

```bash
curl -s https://www.opencircuitsf.com/.well-known/atproto-did | shasum -a 256
# 4198e742e721856d6832f926be55b916b14ad70ddd47a750adb566774cb5948d
```

## Planned topology (`PRD.md` §10.1)

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

## Coexistence with the ShortLinks install

One EC2 instance hosts **both** this service and the separate ShortLinks
deploy (`go.opencircuitsf.com`), behind **one** Apache instance with **two**
vhosts. They share nothing on the box but the host itself, the Apache
process, and — confirmed on 2026-08-25 — the PostgreSQL server process, a
single 15.18 cluster on `127.0.0.1:5432`. Never a database, a service
account, a config file, or a port. Note that `go.opencircuitsf.com` is served
by a *second* ShortLinks instance (`shortlinks-ocsf.service` on `:8083`,
database `shortlinks_ocsf`); the original `shortlinks.service` on `:8081`
serves `go.sstools.co`. A third service, `prototypes`, holds `:8082`. So the
free port this project took, `:8080`, is the only one it may bind:

| | `opencircuit` (this project) | `shortlinks` |
|---|---|---|
| Local port | `127.0.0.1:8080` | `127.0.0.1:8081` |
| PostgreSQL database | `opencircuit` | `shortlinks` |
| PostgreSQL login role | `opencircuit` | `shortlinks` |
| systemd unit | `opencircuit.service` | `shortlinks.service` |
| systemd service account | `opencircuit` (system user, no home, no shell login) | `shortlinks` (same shape) |
| Config file | `/etc/opencircuit/config.env` | `/etc/shortlinks/config.env` |
| Repo checkout | `/opt/opencircuit` (placeholder — see the Prerequisites step) | `/opt/shortlinks`, per its own `DEPLOYMENT.md` |
| Apache vhost | `deploy/apache/opencircuitsf.com.conf` → `www.opencircuitsf.com` / `opencircuitsf.com` | ShortLinks' own vhost → `go.opencircuitsf.com` |

**A redeploy of one must never touch the other.** `scripts/deploy.sh` (this
project's) only ever builds, installs, and restarts `opencircuit` — it does
not reference `shortlinks` anywhere, and the reverse is true of ShortLinks'
own `scripts/deploy.sh`. Restarting Apache (`systemctl reload httpd`) *does*
affect both vhosts simultaneously, since they share one Apache process — that
is expected and is why the vhost file for each service should be edited and
reloaded independently, with `httpd -t` run before every reload regardless of
which vhost changed (see the Apache step below).

`SEND_WORKER_ENABLED` (`docs/configuration.md`) exists for a *future* second
`opencircuit` instance, not for the ShortLinks split above — it has nothing
to do with ShortLinks and should stay `true` on this single-instance
topology.

## `deploy/`

| File | Purpose |
|---|---|
| `deploy/apache/opencircuitsf.com.conf` | Apache vhost: apex→www redirect, reverse proxy to `127.0.0.1:8080`, `flushpackets=on` on `/api/events`, security headers |
| `deploy/apache/README.md` | Install steps for the vhost |
| `deploy/systemd/opencircuit.service` | The main service unit — hardened per the ShortLinks pattern (see the systemd step below) |
| `deploy/systemd/opencircuit-backup.timer` / `.service` / `-alert.service` | Nightly backup + failure alert (`#0229`) — see **Backups** below |
| `deploy/systemd/README.md` | Install steps for every unit above |

---

## 1. Prerequisites

These are the same regardless of whether the box already exists (per the
production facts above, it likely does) or is provisioned fresh. Confirm what
is already installed before reinstalling anything — Apache and PostgreSQL are
already on the box per `CLAUDE.md` §7.

- **EC2 instance**, Amazon Linux 2023. The real one is a **`t4g.nano`** in
  `us-east-1` — ARM/Graviton, so every arch-specific tarball below needs the
  `arm64` build, not `amd64`. PRD §10.1's `t4g.small` assumption is one size
  too large; the box has 418 MB of RAM plus swap, which is enough but leaves
  little headroom during `go build`.
- **Apache (`httpd`) with `mod_ssl`, `mod_proxy`, `mod_proxy_http`,
  `mod_rewrite`, and `mod_headers`.** Already on the box per `CLAUDE.md` §7;
  if provisioning fresh:

  ```bash
  sudo dnf install -y httpd mod_ssl
  sudo systemctl enable --now httpd
  ```

  All of the above modules ship in AL2023's base `httpd` package and are
  loaded by default — confirm with `httpd -M | grep -E 'ssl|proxy|rewrite|headers'`
  rather than assuming.

- **PostgreSQL 15.18.** Already on the box per `CLAUDE.md` §7 — confirm the
  major version with `psql --version` before assuming anything else. 15 is
  proven for this schema: the migrations applied cleanly on 15.18 and the
  whole Go suite passes against PostgreSQL 15.14 (`#0062`/`#0228`'s drills
  ran on 16.14, Homebrew, but that is not what production runs). Nothing in
  the tree pins a major version, so do not introduce 16-only syntax without
  upgrading the box first. If provisioning a **fresh** instance rather than
  matching this one, 16 is a reasonable choice and this is what that
  installation looks like — but it is not a description of the current box:

  ```bash
  sudo dnf list 'postgresql*server' # confirm the exact package name/version AL2023 offers today
  sudo dnf install -y postgresql16-server postgresql16
  sudo postgresql-setup --initdb
  sudo systemctl enable --now postgresql
  ```

- **Node.js 20+** — build-time only, not needed at runtime (the SPA is
  compiled to `web/dist/` and embedded into the Go binary):

  ```bash
  curl -fsSL https://rpm.nodesource.com/setup_20.x | sudo bash -
  sudo dnf install -y nodejs
  node --version   # v20.x or newer
  ```

- **Go 1.26+** (this repo's `go.mod` pins `go 1.26.3`). AL2023's `dnf` Go
  package is typically older; install from the official tarball:

  ```bash
  GO_VERSION=1.26.3
  GOARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
  curl -OL https://go.dev/dl/go${GO_VERSION}.linux-${GOARCH}.tar.gz
  sudo rm -rf /usr/local/go
  sudo tar -C /usr/local -xzf go${GO_VERSION}.linux-${GOARCH}.tar.gz
  echo 'export PATH=$PATH:/usr/local/go/bin' | sudo tee /etc/profile.d/go.sh
  source /etc/profile.d/go.sh
  go version
  ```

  Log out and back in so every future shell (including deploy scripts) picks
  up the `PATH` change.

- **`golang-migrate` CLI**, built with the `postgres` tag:

  ```bash
  go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
  migrate --version
  ```

- **`openssl` and `certbot`**:

  ```bash
  sudo dnf install -y openssl certbot python3-certbot-dns-route53
  ```

  **Corrected 2026-09-05, `#0436`:** this step previously installed
  `python3-certbot-apache`. That is the plugin §9 now says must never be
  used here — it is HTTP-01 and cannot issue this domain's wildcard — and
  it does not provide `--dns-route53`, so the runbook as it stood
  installed one plugin and then invoked another. Measured on the box,
  both packages are present (`python3-certbot-apache-2.6.0` and
  `python3-certbot-dns-route53-2.6.0`); only the latter is required by
  anything this document tells you to run.

- **DNS already resolving** — not a certificate prerequisite, but confirm
  it before the Apache and proxy steps below. **Corrected 2026-09-05,
  `#0436`:** this bullet previously justified itself as "Certbot's
  HTTP-01 challenge needs the hostname to already resolve to this box",
  which describes a path this box does not use. DNS-01 via `dns-route53`
  (§9) never connects to the box or resolves these names; what it needs
  is permission to write a TXT record under
  `_acme-challenge.opencircuitsf.com` in the hosted zone.

  ```bash
  dig +short www.opencircuitsf.com
  dig +short opencircuitsf.com
  ```

Clone the repository (into `/opt/opencircuit`, matching the ShortLinks
`/opt/shortlinks` convention the systemd units already assume — see
`deploy/systemd/README.md`'s "Backup timer" section, which flags this same
placeholder):

```bash
sudo git clone https://github.com/brennanMKE/OpenCircuitSF.git /opt/opencircuit
cd /opt/opencircuit
```

(The GitHub repo rename `Website` → `OpenCircuitSF` is `CLAUDE.md` §10 item
1, **not done** as of this writing — the clone URL above is the target name,
not necessarily what resolves today. Confirm before running it verbatim.)

---

## 2. Database setup

The one-time bootstrap creates the application **login role** and
**database** — it creates no tables; schema is owned by `golang-migrate` and
applied in the Migrations step. `scripts/db/create.sql` ships with a
placeholder password (`CHANGE_ME_IN_PRODUCTION`) that **must** be replaced
before running this anywhere real:

```bash
# scripts/db/create.sql, in the CREATE ROLE line:
CREATE ROLE opencircuit LOGIN PASSWORD '<a real, strong secret — keep it out of source control>';
```

Then, as the `postgres` superuser:

```bash
sudo -u postgres psql -f scripts/db/create.sql
```

Idempotent for the role; `CREATE DATABASE` cannot be guarded with
`IF NOT EXISTS` in PostgreSQL, so a re-run raises a harmless "database
already exists" error on that one statement.

> To reset a **local/dev** database to a clean slate — **never** in
> production, and never against a database anyone depends on:
>
> ```bash
> sudo -u postgres psql -f scripts/db/drop.sql
> ```
>
> `scripts/db-reset.sh` does the local dev-loop version of this (drop,
> recreate, migrate, seed) and refuses to run against anything but
> localhost/127.0.0.1 and a database name starting with `opencircuit` — it is
> a development convenience, not a production tool, ~~and per `CLAUDE.md` §1
> this project's greenfield exception for rewriting migrations ends at the
> first production deploy.~~
>
> **Correction (`#0293`, 2026-08-27).** That exception ended on **2026-08-25**,
> the day `www.opencircuitsf.com` first served this project and production's
> PostgreSQL applied migrations through `schema_migrations.version = 22`.
> `CLAUDE.md` §1 now treats `migrations/000001`–`000022` as append-only,
> without qualification — no in-place rewrite is legitimate against any of
> them any more.

**Correction (`#0431`, 2026-09-04): this step has since been run for real.**
The paragraph below was written before the 2026-08-25 deploy, when nothing had
run against a real instance. Production's PostgreSQL now holds a database
named `opencircuit` owned by a login role of the same name (production-facts
table above), which is exactly what this step's `CREATE ROLE`/`CREATE
DATABASE` produce — the role and database exist on the box today, so the
create path has been exercised against a real instance, not merely
syntax-checked. (Not necessarily re-confirmed as this literal script
invocation rather than an equivalent manual step — this pass did not read the
deploy transcript — but the outcome the step exists to produce is live and
measured, which is the fact a reader deciding whether to re-run it needs.)

**Original note, left for context — accurate only as a description of the
local, pre-deploy syntax check it performed:** `psql --version` confirms
local PostgreSQL 16 syntax-accepts `scripts/db/create.sql` and
`scripts/db/drop.sql` unchanged (they are copied from ShortLinks, `#0001`,
and already exercised repeatedly by `scripts/testdb.sh` and
`scripts/db-reset.sh` against local databases).

---

## 3. Configuration

All runtime configuration is loaded from environment variables
(`internal/config.Load()`) — see [`configuration.md`](configuration.md) for
the full variable reference, including which are required, which have
defaults, and the `BASE_URL`/`WEBAUTHN_RP_ORIGIN` "must be the www form"
gotcha that has already bitten this project once (`#0072`). **That table,
not `PRD.md` §9, is authoritative for the config template** — `#0072`
corrected both `.env.example` and `docs/configuration.md` to the www form;
`PRD.md` §9's own configuration block was separately corrected in the same
pass and the two now agree, but `.env.example` is what you actually copy.

```bash
sudo mkdir -p /etc/opencircuit
sudo cp .env.example /etc/opencircuit/config.env
sudo chmod 600 /etc/opencircuit/config.env
sudo nano /etc/opencircuit/config.env
```

Fill in every value `.env.example` ships blank or with a placeholder:

- `DATABASE_URL` — must use the same role name (`opencircuit`), database name
  (`opencircuit`), and password set in step 2.
- `SESSION_SECRET` — generate with `openssl rand -hex 32`; a blank value
  fails startup closed (`config: missing required variable SESSION_SECRET`)
  rather than silently signing sessions with a key that was ever published in
  this public repository (`#0067`).
- `ADMIN_EMAIL` — the address pre-authorized as admin on first registration.
- `AWS_REGION`, `SES_CONFIGURATION_SET`, `EMAIL_FROM`, `EMAIL_REPLY_TO`,
  `EMAIL_LIST_DOMAIN`, `SES_INBOUND_BUCKET`, `SES_INBOUND_TOPIC_ARN` — see
  **SES setup** below. As of `#0423` (2026-09-04) `.env.example` ships this
  project's actual production values for five of the seven, not
  placeholders, so a deploy of *this* domain to *this* SES account needs no
  editing for those five — confirm them against `docs/email-setup.md`'s
  current-state table and `docs/aws-iam-setup.md`'s "The facts this rests
  on" table, which is where `AWS_REGION` and `SES_CONFIGURATION_SET`
  actually live, rather than reinventing them. The other two,
  `SES_INBOUND_BUCKET` and `SES_INBOUND_TOPIC_ARN` (added `#0058`), name the
  bucket and SNS topic `#0057`'s runbook (`docs/email-setup.md`, "Inbound
  unsubscribe") creates — leave both as shipped until that runbook's AWS
  steps are actually done; there is nothing to confirm them against before
  then.
  Deploying a fork to a different domain or SES account still means
  replacing every one of these with that identity's own values.
- `MAX_SEND_RATE` — **set to `1` while the SES account is in the sandbox**
  (1 message/second cap). This is a deploy-time fact the code cannot enforce
  on its own — nothing in this codebase can detect sandbox-vs-production SES
  status (`docs/configuration.md`'s "Developing against the SES sandbox"
  section). `CLAUDE.md` §5: there is no performance requirement in this
  project: this value paces for SES's quota, not for throughput.

`BASE_URL` and `WEBAUTHN_RP_ORIGIN` must both be the **www** form
(`https://www.opencircuitsf.com`) — the apex 301s to www, and
`WEBAUTHN_RP_ORIGIN` must match the browser's actual origin exactly or every
passkey ceremony fails with an opaque error (`CLAUDE.md` §7).

**Corrected `#0431`, 2026-09-04 — this has since been verified on a real
box.** The paragraph below described the pre-deploy state. Since 2026-08-25,
`/etc/opencircuit/config.env` is read by `opencircuit.service`'s real
`EnvironmentFile=` on the production host, and the service came up serving
real traffic on it ("What this document is, and is not, verified against"
below records the end-to-end serving path as measured). See the systemd step
for exactly what that run does and does not additionally prove.

**Original note, left for context — accurate only for what it actually
tested:** this step was also exercised locally (`scripts/db-reset.sh` builds
the equivalent environment inline for dev use), which is a different thing
from a real `EnvironmentFile=` on a real systemd unit.

---

## 4. Build

The SPA is embedded into the Go binary at compile time
(`//go:embed all:dist`), so it must be built **first**:

```bash
cd web && npm ci && npm run build
cd ..
go build -o opencircuit ./cmd/opencircuit
```

Install to the path the systemd unit's `ExecStart` references:

```bash
sudo install -m 0755 opencircuit /usr/local/bin/opencircuit
```

**Corrected `#0431`, 2026-09-04 — this has since been run on AL2023 for
real.** The paragraph below described the state before this build step was
first run for real. "What this document is, and is not, verified against"
(near the end of this file) records that the SPA build ran on the box's own
Node 18.20.8 during the 2026-08-25 deploy and produced byte-identical hashed
assets to a local build; the Go binary was likewise built and run on the
instance itself, not cross-compiled. So this step is no longer only
syntax/build-checked off-box.

**Original note, left for context — accurate only as a description of the
pre-deploy state:** `go build ./...` and the web `npm run check`/`npm test`
suite passed locally on macOS/arm64 as of that writing (see `## Verification`
in `#0064`); cross-compilation to `linux/arm64` or `linux/amd64` was not
attempted, and the guidance to run this step **on the target instance itself**
rather than a developer's Mac is still the right instruction going forward.

---

## 5. Migrations

Apply the schema with `golang-migrate`, pointing at the migration files and
the database URL from `/etc/opencircuit/config.env`:

```bash
export DATABASE_URL='postgres://opencircuit:<password>@localhost:5432/opencircuit?sslmode=disable'
migrate -path migrations -database "$DATABASE_URL" up
```

The current on-disk migration range lives in one place, guarded against
drift by `internal/db/docs_parity_test.go`'s `TestDatabaseDocMigrationParity`:
see `docs/database.md`'s migration table — do not restate the range here, it
went three migrations stale the last time it was a second copy (`#0301`).
Apply them in order. Run this again on every deploy that adds new migration
files — check first:

```bash
git diff --name-only <last-deployed-sha>..HEAD -- migrations/   # any output => a migration is needed
migrate -path migrations -database "$DATABASE_URL" version       # or compare against the DB's applied version
```

~~Per `CLAUDE.md` §1, this project is greenfield until the **first** production
deploy — after that, migrations become append-only and must never be edited
in place again. The first real run of this step, against a database that
matters, is the event that ends the greenfield exception.~~

**Correction (`#0293`, 2026-08-27).** That first real run happened on
**2026-08-25**, when this exact `migrate ... up` step was applied to
production's PostgreSQL and left it at `schema_migrations.version = 22`. The
greenfield exception is over: `CLAUDE.md` §1 now treats
`migrations/000001`–`000022` as append-only, without qualification, and any
new schema change is a new migration file above `000022`.

**Corrected `#0431`, 2026-09-04 — this note contradicted the correction just
above it.** This exact `migrate ... up` invocation *has* been run
against a real box: the `#0293` correction just above records that it ran
against production's PostgreSQL on 2026-08-25, leaving
`schema_migrations.version = 22`. Production is PostgreSQL **15.18**, not the
AL2023 PostgreSQL 16 assumed by the pre-deploy note this correction
replaces — consistent with the Prerequisites section's own correction of
that same assumption.

**Original note, left for context — accurate only for the drills it actually
describes:** `#0062`'s and `#0228`'s restore drills ran this exact `migrate
... up` invocation repeatedly against local scratch databases
(`schema_migrations` landing at `version=20, dirty=false` every time) — see
**Backups** below.

---

## 6. Seed

Bootstrap the admin user (idempotent — safe to re-run):

```bash
opencircuit seed
```

This ensures the `ADMIN_EMAIL` user exists with `is_admin = true` and
`active = true`. Unlike ShortLinks' version, it does not seed a test link —
this project has no `links` table (`PRD.md` §3.2). The interest taxonomy is
seeded by migration `#0023`, not by this command.

**The seeded admin has no passkey** — see **First admin login** below;
`seed` only creates the user row.

---

## 7. systemd

The service runs as an unprivileged `opencircuit` system user. Create it
once:

```bash
sudo useradd --system --no-create-home opencircuit
```

Install the unit, reload, enable, and start:

```bash
sudo cp deploy/systemd/opencircuit.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now opencircuit
sudo systemctl status opencircuit
```

Confirm the service is healthy from the host before fronting it with Apache:

```bash
curl -fsS http://127.0.0.1:8080/health
```

### Hardening — mirrors the ShortLinks pattern

`deploy/systemd/opencircuit.service` already carries every directive this
issue's acceptance criterion names, inspected directly in the file:
`NoNewPrivileges=true`, `ProtectSystem=strict`, `ProtectHome=true`,
`PrivateTmp=true`, and `RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX` —
plus `PrivateDevices`, `ProtectKernelTunables`, `ProtectKernelModules`,
`ProtectControlGroups`, `RestrictNamespaces`, and `LockPersonality`, which go
beyond the criterion's named set. It also encodes the process's own graceful
-shutdown budget (`TimeoutStopSec=30`, documented inline against
`cmd/opencircuit/main.go`'s two independent shutdown timeouts) — read that
unit file's comments before changing `TimeoutStopSec` for any reason.

**Corrected `#0431`, 2026-09-04 — this unit has since run under real
systemd.** The paragraph below described the state before the 2026-08-25
deploy, when this development environment's lack of systemd was the only
check available. Since that deploy, `opencircuit.service` has been running
under the box's real systemd: "What this document is, and is not, verified
against" (below) records it coming up `enabled` at boot and serving
`/health` as `{"status":"ok","db":"ok"}`. That run demonstrates
`EnvironmentFile=/etc/opencircuit/config.env` resolves correctly and that the
hardening directives (`RestrictAddressFamilies` included) do not reject
anything the process needs to start and serve traffic. **Still not
demonstrated by anything measured here:** that `Restart=on-failure` /
`KillSignal=SIGTERM` behave as documented under a real crash — no deliberate
crash test has been run against the live service.

**Original note, left for context — accurate only as a description of the
pre-deploy structural check:** there was no systemd on the development
machine (macOS) this runbook was written on, and no `systemd-analyze verify`
available to check the unit file directly (the same gap `#0229` recorded for
the backup units). Confirmed instead: every non-comment line is `key=value`
and every section header is `[Section]` (the same structural check `#0229`
used), and the file was diffed line by line against
`deploy/systemd/README.md`'s and this project's own
`opencircuit-backup.service`'s equivalents for consistency.

### Workshop media upload enablement (`#0465`) — pending approval

`#0433` builds an admin-console upload endpoint for workshop cover images,
provable end to end against a temporary directory, but its planning pass
found **two independent reasons** the Go service cannot actually write
`/var/www/media` (`docs/media.md`) in production. Fixing either one alone
changes nothing — the failure just moves, and looks exactly like a
permissions bug someone already fixed:

1. **Ownership.** The directory is owned by `ec2-user`, not `opencircuit`.
2. **`ProtectSystem=strict`** in `opencircuit.service` (above) makes the
   *entire* filesystem hierarchy read-only to the process, regardless of any
   directory's own mode. `ReadWritePaths=` is systemd's documented escape
   hatch for exactly this — it does not weaken `ProtectSystem=strict` itself,
   it exempts one named path from it.

**Re-derived read-only against the box, 2026-09-08 — do not trust the figures
below without re-checking; re-derive them yourself first if time has passed**
(`CLAUDE.md` §5b). Both blockers hold exactly as `#0433`'s planning pass
measured:

```
$ ssh ec2 stat -c '%U:%G %a %n' /var/www/media
ec2-user:ec2-user 755 /var/www/media          # no setgid bit (mode is exactly 755, not 2755/2775)

$ ssh ec2 id opencircuit
uid=990(opencircuit) gid=990(opencircuit) groups=990(opencircuit)   # no supplementary groups

$ ssh ec2 systemctl show opencircuit.service -p ProtectSystem -p ReadWritePaths
ProtectSystem=strict
ReadWritePaths=                                # empty — nothing exempted yet

$ ssh ec2 df -h /
Filesystem      Size  Used Avail Use% Mounted on
/dev/nvme0n1p1   20G   11G  9.1G  55% /
```

Also confirmed, read-only, and load-bearing for the questions below:

```
$ ssh ec2 id apache
uid=48(apache) gid=48(apache) groups=48(apache)

$ ssh ec2 id postgres
uid=26(postgres) gid=26(postgres) groups=26(postgres)

$ ssh ec2 systemctl cat opencircuit-backup.service
No files found for opencircuit-backup.service.   # #0434/#0435's timer is not installed yet either
```

Neither `apache` nor `postgres` is a member of, or will become a member of,
the `opencircuit` group — both read `/var/www/media` today through the
directory's **`other`** permission bits, which the change below leaves
untouched (`755`'s trailing `5` = `r-x`; `2775`'s trailing `5` is the same
`r-x`). That is the answer to two of the questions below.

**What may be approved.** Two changes: the directory's group and mode, and
`ReadWritePaths=/var/www/media` on the unit — the latter is now committed in
`deploy/systemd/opencircuit.service` (`#0465`), so a rebuilt server picks it
up automatically; only the *box* needs the copy-and-restart below.

**Precondition — the box's checkout of this file is stale (`#0466`).**
`/opt/opencircuit`'s checkout is still at `ef0a58f` (`#0274`, 2026-08-25),
several hundred commits behind `main`, so its copy of
`deploy/systemd/opencircuit.service` predates `#0465`'s
`ReadWritePaths=/var/www/media` line entirely (confirmed read-only,
2026-09-08: `grep ReadWritePaths` on the box's copy finds nothing). Step 2
below (`sudo cp deploy/systemd/opencircuit.service /etc/systemd/system/`)
would therefore install the *old* unit unchanged — no error, no
`ReadWritePaths=`, and the write would keep failing in a way that looks like
step 1 never took effect. **`scp` this one file, not `git pull`:** a
`git pull` would also bring every other commit since `ef0a58f`, including a
`go build`/`npm run build` this box's 418 MB of RAM makes expensive
(`CLAUDE.md` §7), to update a single unit file that needs neither. Run from
the local repo checkout, not on the box:

```bash
scp deploy/systemd/opencircuit.service \
  ec2:/opt/opencircuit/deploy/systemd/opencircuit.service
```

Then confirm the bytes landed correctly (recompute the left-hand value
locally with `shasum -a 256` if `main` has moved since this was written):

```bash
ssh ec2 sha256sum /opt/opencircuit/deploy/systemd/opencircuit.service
```

Expect:

```
5df0b896983a68cd5b7c99f958afe78ee53806b06fee24068e647a3c20f862c8  /opt/opencircuit/deploy/systemd/opencircuit.service
```

```bash
# 1. Give the service's group write access to the directory, and set the
#    setgid bit so newly created files — from either writer — inherit the
#    directory's group rather than the creating process's own primary
#    group. ec2-user stays the owner; the scp workflow (docs/media.md) is
#    unaffected.
sudo chgrp opencircuit /var/www/media
sudo chmod 2775 /var/www/media

# Expect:
stat -c '%U:%G %a %n' /var/www/media
#   ec2-user:opencircuit 2775 /var/www/media

# 2. Install the updated unit (this repo's opencircuit.service now carries
#    ReadWritePaths=/var/www/media; the copy on the box does not yet).
sudo cp deploy/systemd/opencircuit.service /etc/systemd/system/
sudo systemctl daemon-reload

# 3. Restart — daemon-reload alone does not apply a changed ReadWritePaths=
#    to the already-running process; the unit must actually restart.
sudo systemctl restart opencircuit
sudo systemctl status opencircuit

# Expect Active: active (running) and the same PID class as any other
# restart; journalctl should show no new errors:
sudo journalctl -u opencircuit -n 30
```

**A restart briefly interrupts the site.** Unlike the `httpd -k graceful`
reload `#0447`'s certbot hook uses, `systemctl restart opencircuit` stops the
process (draining in-flight work first, per the `KillSignal=SIGTERM` /
`TimeoutStopSec=30` budget documented above — up to ~20s in the worst case,
typically well under a second when the service is idle) and then starts a
new one. There is a real gap, however brief, during which `127.0.0.1:8080`
is not accepting connections and Apache's reverse proxy will return an
error to any request that lands in that window. Nothing else on the box is
touched: Apache itself is not restarted or reloaded, the two ShortLinks
services on `:8081`/`:8083` and the prototypes service on `:8082` are
independent units and are not affected, and the database connection this
service holds is simply closed and reopened.

**Verify the existing images afterward by hash, not by status code** — §7's
standing lesson from the `/.well-known/` carve-out, where a `200` proves
something answered, not that the right bytes did. `#0417` recorded both
files' sizes and dimensions; re-derived here with an independent SHA-256:

```bash
ssh ec2 sha256sum /var/www/media/programming_leds.jpg /var/www/media/soldering.jpg
```

Expect exactly:

```
ad298443d7d4d88ddb05c6825ace4b4c785602bb2002e2bdf313cbe841343d8f  /var/www/media/programming_leds.jpg
55510a2a2ce82e98df077a2cca7451c1bf2911335dcb1968cc4099e8d03c142e  /var/www/media/soldering.jpg
```

(63,253 bytes / 600×450, and 89,179 bytes / 1200×630, per `#0417` — dimensions
re-confirmed here by parsing each file's `SOF0` marker directly, matching.)
Also confirm the files' own ownership and mode are untouched — only the
*directory's* group and mode change, never the files':

```bash
ssh ec2 stat -c '%U:%G %a %n' /var/www/media/programming_leds.jpg /var/www/media/soldering.jpg
#   ec2-user:ec2-user 644 /var/www/media/programming_leds.jpg
#   ec2-user:ec2-user 644 /var/www/media/soldering.jpg
```

Finally, confirm the service and the site over HTTPS, not just the process
state:

```bash
curl -fsS http://127.0.0.1:8080/health
curl -fsS https://www.opencircuitsf.com/health
curl -sI https://www.opencircuitsf.com/media/soldering.jpg | grep -iE 'HTTP|cache-control'
```

**Questions this instruction has to answer, answered:**

- **Why `2775` and not plain `0775`?** The setgid bit (the leading `2`)
  forces every file newly created in the directory to inherit the
  directory's group (`opencircuit`), regardless of the creating process's
  own primary group — this is standard POSIX directory semantics, not
  specific to this project. The `opencircuit` service's primary group is
  already `opencircuit` (confirmed above), so setgid changes nothing for
  files *it* writes. Its actual effect is on the **`scp` path**: `ec2-user`'s
  primary group is `ec2-user`, so without setgid a freshly `scp`'d file would
  land group-owned `ec2-user` while service-written files land group-owned
  `opencircuit` — a split that costs nothing today (both groups already read
  every file fine through the `other` bits, unaffected by this change) but
  would quietly resurface the moment anything ever relies on group
  ownership being consistent across the directory (a future cleanup script
  running as `opencircuit`, for instance). Setgid keeps that never a live
  question, at zero cost: it does not grant any additional access by itself,
  and neither writer's directory-level ability to create, rename, or delete
  files depends on it (that already follows from `ec2-user` owning the
  directory and `opencircuit` now being able to write via the group bit).
- **Does the group change affect `#0434`'s media backup?** No.
  `opencircuit-backup.service` runs as `User=postgres`, and `postgres`
  (`uid=26`, confirmed above) is neither the directory's owner nor a member
  of the `opencircuit` group before or after this change — it reads
  `/var/www/media` through the **`other`** permission bits, which `2775`
  leaves identical to today's `755` (`r-x` either way). The backup's write
  target is `/var/backups/postgres`, a completely different path this
  change never touches. Also confirmed: `opencircuit-backup.service` is not
  installed on the box yet at all (`#0435` is its own separate, still-open
  approval), so there is nothing currently running to disturb.
- **Is `ReadWritePaths=` the narrow escape hatch, and does it weaken anything
  else?** Yes, and no. Per `systemd.exec(5)`, `ReadWritePaths=` is the
  documented mechanism for exempting specific paths from `ProtectSystem=`'s
  effect; it does not change `ProtectSystem=strict`'s own value, and every
  other directory on the host stays exactly as inaccessible to this process
  as before. `systemctl show` after the restart should report
  `ProtectSystem=strict` unchanged and `ReadWritePaths=/var/www/media` newly
  set — both are worth checking, not just the second.
- **What happens to the two existing files?** Nothing. They keep their
  names, their `ec2-user:ec2-user` ownership, their `0644` mode, and their
  `cover_image` values; Apache serves them exactly as before, proven by the
  hash check above rather than assumed from a `200`.

**Confirm the service still starts and the site still serves before
declaring this done** — the `curl` checks above, plus `systemctl status`
showing `active (running)` with no restart loop
(`sudo systemctl show opencircuit -p NRestarts` should read `0` for this
restart). Record exactly what was run and its output in the issue's
`## Work log`.

---

## 8. Apache

Install the vhost and reload — on AL2023, any `.conf` file dropped into
`/etc/httpd/conf.d/` is loaded automatically; there is no `a2ensite`:

```bash
sudo cp deploy/apache/opencircuitsf.com.conf /etc/httpd/conf.d/
sudo httpd -t              # syntax check BEFORE reloading — see below
sudo systemctl reload httpd
```

The vhost (`ServerName www.opencircuitsf.com`, `ServerAlias
opencircuitsf.com`) redirects any request that didn't arrive on `www` before
it reaches the proxy rules, and proxies `/api/events` with
`flushpackets=on` **before** the wildcard `ProxyPass /` — Apache evaluates
`ProxyPass` top-to-bottom, so the SSE route must come first or the wildcard
would swallow it and response buffering would not be disabled.

### Security headers (`#0064`, `PRD.md` §11 / `CLAUDE.md` §11)

The vhost now also sets, via `mod_headers`:

```
Strict-Transport-Security: max-age=31536000; includeSubDomains
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
Referrer-Policy: strict-origin-when-cross-origin
Content-Security-Policy: default-src 'self'; script-src 'self' 'sha256-KLoCdoLOAQC6Tl5qFMi7s/7fwSxANUdbZFjnX7Vhau8='; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'; object-src 'none'
```

**`script-src` has no `unsafe-inline`**, which is the criterion's actual
requirement. `web/index.html` has exactly one inline `<script>` (the
pre-paint theme-flash guard that reads `localStorage`) and everything else
Vite emits is a same-origin, hashed asset — so `script-src 'self'` plus one
hash for that inline script is genuinely achievable, matching this issue's
own Notes ("Vite emits hashable assets, so `unsafe-inline` is genuinely
avoidable here").

**How the hash above was computed, and its real limitation:** it is
`base64(sha256(<the child text content of web/index.html's one bare
<script> element, i.e. everything between the '>' that closes its opening
tag and its '</script>', exactly as the browser sees it>))`, computed
directly (Python's `hashlib`/`base64`, not typed by hand — `CLAUDE.md` §8
warns that hand-typed escapes can land as the literal bytes rather than
describing them). **This was computed from the unminified source, not from
a real `npm run build` output.** `web/dist/` is explicitly out of this
pass's scope (`CLAUDE.md` §8b flags it as a shared mutable resource other
concurrent agents may be building into, and the dispatch instructions for
this pass named `web/` as off-limits), so this hash was never checked
against what Vite's HTML/JS minifier actually emits — minification can
reformat inline-script whitespace and change the hash.

**Corrected 2026-08-24 (`#0064` bounce).** Two prior recompute attempts both
produced the wrong hash, from two different bugs in the same family — a text
search for the literal substring `<script>` is not the same thing as finding
the actual `<script>` *element*:

- The value that shipped in `ccf134d`
  (`sha256-dwSwJdScBQq2rtRDgx+PNrnX/IUc7TDIKGH+8kn188Y=`) came from a regex
  anchored on `r'<script>\n(.*?)</script>'` — it stripped the element's
  leading newline, which CSP does not ignore.
- The phase-3 review's own "browser-verified" replacement
  (`sha256-aV6Z5Fp2xyBUYGmN3Q9e0BQeIOKsDdCa35MPtxT7byg=`) is *also* wrong.
  `web/index.html` line 38 contains the literal four-character-plus-brackets
  substring `<script>` **inside prose, inside an HTML comment**
  ("...embedding it verbatim inside `<script>` is safe..." — describing the
  JSON-LD substitution, not tagging real markup). A plain text search for the
  first occurrence of `<script>` in the file finds *that* substring, 16 lines
  before the real bootstrap tag, and then reads forward to the next
  `</script>` — the real one on line 63 — capturing a ~1.5 KB blob of
  comment prose, three `<link>` tags, and the real script's own opening tag
  as literal text, none of which is what the browser hashes. Re-verified in
  a real browser for this pass (Chromium via Playwright, §10): setting that
  hash in the CSP still blocks the script, and Chromium's own console
  violation message names the *actual* correct hash below whenever a wrong
  one is supplied — that message is not derived from any Python regex and is
  the ground truth used here.
- The correct value, `sha256-KLoCdoLOAQC6Tl5qFMi7s/7fwSxANUdbZFjnX7Vhau8=`
  (shipped in the vhost above as of this pass), was confirmed two ways in a
  real browser: (1) Chromium's CSP violation message, when served the page
  under a deliberately wrong hash, names this exact value as the one that
  would be required; (2) served under a CSP that allows only this hash, the
  script actually executes with zero `script-src` violations — proven by
  seeding `localStorage.theme = 'dark'` before navigation and observing
  `document.documentElement.getAttribute('data-theme') === 'dark'` after
  load, which only the guard script itself can produce.

**Before enabling this CSP against a real deploy, recompute and compare** —
using an HTML parser rather than a text/regex search, specifically *because*
a text search for `<script>` can match prose inside a comment before it
reaches the real tag, as just happened twice:

```bash
python3 - <<'PYEOF'
import hashlib, base64
from html.parser import HTMLParser

class ScriptExtractor(HTMLParser):
    """Finds the one <script> element with no type= and no src= attribute
    (the pre-paint theme guard) — this correctly ignores HTML comments
    entirely (handle_data is never called for comment text), unlike a text
    search for the substring '<script>', and correctly skips the
    type="module" bundle-entry script and any injected
    type="application/ld+json" block."""
    def __init__(self):
        super().__init__(convert_charrefs=False)
        self._in_target = False
        self._buf = []
        self.captured = None

    def handle_starttag(self, tag, attrs):
        if tag == 'script' and self.captured is None:
            attrs_dict = dict(attrs)
            if 'src' not in attrs_dict and 'type' not in attrs_dict:
                self._in_target = True
                self._buf = []

    def handle_data(self, data):
        if self._in_target:
            self._buf.append(data)

    def handle_endtag(self, tag):
        if tag == 'script' and self._in_target:
            self.captured = ''.join(self._buf)
            self._in_target = False

html = open('web/dist/index.html', encoding='utf-8').read()   # the REAL build output
p = ScriptExtractor()
p.feed(html)
if p.captured is None:
    raise SystemExit("no bare <script> element found — did the markup change?")
h = hashlib.sha256(p.captured.encode('utf-8')).digest()
print("sha256-" + base64.b64encode(h).decode())
PYEOF
```

If the printed hash differs from the one in the vhost file, update the vhost
and reload — a stale hash here doesn't fail open, it fails **closed**: the
browser blocks the inline script and the pre-paint theme flash guard simply
stops running (a cosmetic flash-of-wrong-theme on load), not a security
regression, but worth fixing rather than leaving broken. **Do not accept the
new value on the printed hash alone** — the last two rounds show a wrong
extraction can be internally consistent with itself. Confirm it in a real
browser: serve the file with the candidate CSP and either watch for zero
`script-src` console violations, or deliberately supply a wrong hash first
and read the correct one back out of Chromium's own violation message.

**`style-src` keeps `'unsafe-inline'` — a real, load-bearing gap against the
criterion, not an oversight.** Several Svelte components use plain
`style="..."` HTML attributes for per-instance dynamic values (e.g.
`web/src/lib/Logo.svelte`'s `style="--logo-size: {size}px; …"`, and several
static `style="margin-top: …"` attributes in `web/src/views/Admin.svelte`).
CSP's `style-src` governs the `style` attribute the same way it governs
`<style>` elements, and:

- **Hash-allowlisting an attribute needs the CSP3 `'unsafe-hashes'` keyword**
  (hash/nonce sources apply only to elements, not attributes, without it) —
  itself a keyword with "unsafe" in the name for a reason, and it would still
  need one hash per distinct static string, which is fragile against every
  future edit to any of these components.
- **The dynamic ones (`Logo.svelte`) cannot be hash-allowlisted at all** —
  the value changes per render (interpolated from a prop), so there is no
  fixed string to hash.
- Locking `style-src` down for real needs a source-level refactor: moving
  dynamic per-instance styling to `element.style.setProperty(...)` in a
  `$effect` (which CSP's `style-src` does **not** govern — direct CSSOM
  property assignment is exempt) instead of a template `style="..."`
  attribute, and moving the static ones to plain CSS classes. That is a
  `web/src/` change and is out of this pass's scope (`web/` is off-limits
  here); reporting it rather than silently narrowing the criterion.

So the criterion is met for the security-critical half (`script-src`, where
an injected `<script>` is the actual XSS vector) and not for the lower-severity
half (`style-src`, where the realistic worst case is CSS-based UI redressing,
not arbitrary code execution) — a defensible, common compromise, but not what
"a CSP with no `unsafe-inline`" says literally.

**Corrected `#0431`, 2026-09-04 — this vhost has since run on AL2023 against
real requests.** The header below described the state before the 2026-08-25
deploy. "What this document is, and is not, verified against" (below) already
records the vhost syntax-checking and running on the real AL2023 httpd
2.4.68; re-confirmed read-only for this pass with a live request —
`curl -sI https://www.opencircuitsf.com/` returns `Server: Apache/2.4.68
(Amazon Linux) OpenSSL/3.5.7` and the exact `Content-Security-Policy` line
from this section, byte for byte, served over a real HTTPS connection. What
that single GET does **not** confirm: whether `flushpackets=on` behaves as
documented under a real proxied SSE connection to `/api/events` — that needs
a request against that specific route, not the homepage.

**Original note, left for context — accurate only for the local check it
describes:**

```bash
# Minimal wrapper config: load mpm_event, proxy, proxy_http, rewrite, ssl,
# headers; point the vhost's two SSLCertificateFile paths at a throwaway
# self-signed cert (the real /etc/letsencrypt/... paths don't exist on a
# dev machine); Include the real vhost file unmodified; set ErrorLog/
# ServerName so httpd has somewhere to log and doesn't just warn.
openssl req -x509 -newkey rsa:2048 -keyout /tmp/privkey.pem \
  -out /tmp/fullchain.pem -days 1 -nodes -subj "/CN=test"
sed "s#/etc/letsencrypt/live/opencircuitsf.com/fullchain.pem#/tmp/fullchain.pem#; \
     s#/etc/letsencrypt/live/opencircuitsf.com/privkey.pem#/tmp/privkey.pem#" \
  deploy/apache/opencircuitsf.com.conf > /tmp/vhost-scratch.conf
cat > /tmp/httpd-check.conf <<EOF
ServerRoot "/usr"
LoadModule mpm_event_module libexec/apache2/mod_mpm_event.so
LoadModule proxy_module libexec/apache2/mod_proxy.so
LoadModule proxy_http_module libexec/apache2/mod_proxy_http.so
LoadModule rewrite_module libexec/apache2/mod_rewrite.so
LoadModule ssl_module libexec/apache2/mod_ssl.so
LoadModule headers_module libexec/apache2/mod_headers.so
LoadModule unixd_module libexec/apache2/mod_unixd.so
LoadModule log_config_module libexec/apache2/mod_log_config.so
LoadModule authz_core_module libexec/apache2/mod_authz_core.so
Listen 8443
Include /tmp/vhost-scratch.conf
ErrorLog /tmp/httpd-check-error.log
ServerName localhost
EOF
httpd -t -f /tmp/httpd-check.conf
# → Syntax OK
```

run against the real Apache 2.4.67 installed on this development machine
(`httpd -v`) — the closest available stand-in for AL2023's 2.4.68 available
when this was written, not the real thing at the time. This confirms the
file **parses**: every `Header`/`ProxyPass`/`RewriteRule` directive is
spelled correctly and every module they need (`mod_headers`, `mod_proxy`,
`mod_proxy_http`, `mod_rewrite`, `mod_ssl`) is one Apache actually ships. **As
of the correction above, the headers are now also confirmed correct under a
real HTTPS request** — the live `curl` matched this section's CSP exactly.
Still not confirmed: that the CSP doesn't break some interaction not
exercised by the SPA's automated tests, or that `flushpackets=on` behaves as
documented under a real proxied SSE connection to `/api/events`.

---

## 9. TLS

**Corrected `#0431`, 2026-09-04.** ~~The command below (HTTP-01 via the
`--apache` plugin, two named hosts) predates the deploy and was never what
actually ran~~ — production's cert is a **wildcard**, re-verified read-only for
this pass with `openssl s_client -connect www.opencircuitsf.com:443 …
| openssl x509 -noout -text`, which shows `X509v3 Subject Alternative Name:
DNS:*.opencircuitsf.com, DNS:opencircuitsf.com`. A wildcard SAN cannot be
issued over HTTP-01/`--apache`; it requires the DNS-01 challenge, matching
what the production-facts table already says: the authenticator in use is
**`dns-route53`**, not `--apache`.

**Corrected `#0436`, 2026-09-05 — the command itself, not just this
paragraph, was wrong.** `#0431`'s pass identified that the `--apache` command
below did not describe production, but then kept the command in the document
anyway, framed as "a syntactically valid fallback … if that were ever wanted
instead." That framing understated the risk: `--apache` doesn't *decline* the
wildcard, it **replaces** one. Re-verified read-only for this pass —
`sudo certbot certificates` on the box lists exactly one cert for this domain,
`Certificate Name: opencircuitsf.com`, `Domains: opencircuitsf.com
*.opencircuitsf.com`, `Expiry Date: 2026-11-16` — so running the command below
as it previously stood would have obtained a certificate for
`opencircuitsf.com`/`www.opencircuitsf.com` only under that same certificate
name, discarding the wildcard SAN and, with it, coverage for every other
subdomain: **`go.opencircuitsf.com`**, a *different project*'s deploy
(ShortLinks, `CLAUDE.md` §1) hosted on this same box, and any future
subdomain. It would also switch the authenticator from `dns-route53` back to
the Apache plugin, which (re-)couples renewal to `/.well-known/acme-challenge`
and to whatever it rewrites in the installed vhosts — including the
`/.well-known/` `ProxyPass … !` carve-out this box depends on to serve
`atproto-did` from disk (`CLAUDE.md` §7; the Go service itself 404s that
path). There is no scenario in this project where the narrower cert is wanted,
so the command is corrected below rather than kept as an option.

Obtain (or renew ahead of the timer) the certificate the way production
actually does — DNS-01 via `dns-route53`, covering the wildcard. Re-derived
read-only for this pass directly from the installed renewal config,
`/etc/letsencrypt/renewal/opencircuitsf.com.conf`: `authenticator =
dns-route53`, `key_type = ecdsa`, no installer configured — so this obtains a
certificate only and never touches a vhost:

```bash
sudo certbot certonly --dns-route53 -d opencircuitsf.com -d '*.opencircuitsf.com'
```

**Why DNS-01, not `--apache`.** The domain list above includes a wildcard
(`*.opencircuitsf.com`), and the ACME protocol only permits proving control of
a wildcard name via a DNS-01 challenge (a TXT record under
`_acme-challenge.opencircuitsf.com`) — HTTP-01, which is what the `--apache`
plugin drives, cannot issue one at all. That is a protocol constraint, not a
preference: there is no `--apache` invocation that would produce the same
certificate. `dns-route53` is the DNS-01 authenticator this box already has
IAM permissions for, which is why it — not a webroot or standalone HTTP-01
authenticator — is what production uses.

**Do not "simplify" this back to `--apache`.** It reads like the more direct
tool because it needs no Route 53 credentials and matches the
`www`/`opencircuitsf.com` pair already used elsewhere in this document — but
that pair is exactly the two names this cert is *not* scoped to. Running it
would silently drop `go.opencircuitsf.com`'s coverage (a different project),
re-couple renewal to `/.well-known/acme-challenge` and Apache vhost state, and
still fail to reproduce the wildcard even after all that, since `--apache`
cannot issue one under any set of flags.

The renewal timer itself needs no manual step — it is already installed
and running. On this box that is **`certbot-renew.timer`** (systemd), firing
at 00:00 and 12:00 UTC — re-verified read-only for this pass with `systemctl
list-timers certbot-renew.timer`, which shows the next run at 2026-09-05
12:00 UTC and the last run at 2026-09-05 00:00 UTC — using the `dns-route53`
authenticator against the wildcard, so renewal never reads a vhost or an ACME
webroot and no Apache change in this project can break it. Recorded in the
production-facts table above.

**Reload Apache after any renewal, including the timer's.** Re-verified
read-only for this pass: `/etc/letsencrypt/renewal-hooks/deploy/` and
`/etc/letsencrypt/renewal-hooks/post/` are both empty,
`/etc/sysconfig/certbot` sets `PRE_HOOK`, `POST_HOOK` and `DEPLOY_HOOK`
to the empty string, and `certbot-renew.service` is a bare
`certbot renew --quiet`. The renewal config configures no installer. So
nothing reloads Apache when the certificate is replaced, and `mod_ssl`
goes on serving the previous one until it is. After a manual `certonly`,
run the reload yourself:

```bash
sudo systemctl reload httpd
curl -fsS https://www.opencircuitsf.com/health
```

**For the unattended path, `#0447` prepares a certbot deploy hook to close
the gap, pending approval** — `deploy/certbot/reload-apache-deploy-hook.sh`
in this repo. It runs
`httpd -t` and, only if that passes, `systemctl reload httpd`; certbot only
invokes a deploy hook after a certificate actually renews, so the script
needs no "did anything change" check of its own, and a failed `httpd -t`
leaves the previously-loaded certificate serving rather than reloading into
a broken config. It is a deploy-hooks-directory script rather than
`DEPLOY_HOOK` in `/etc/sysconfig/certbot` because `certbot-renew.service`'s
`ExecStart` above does not source that file or splice its hook variables
onto the command line — setting `DEPLOY_HOOK` there would sit inert for the
timer path without also editing the unit. A script placed in
`/etc/letsencrypt/renewal-hooks/deploy/` needs no such edit: certbot scans
that directory unconditionally on every `renew` invocation. See
`deploy/certbot/README.md` for the full reasoning.

**Precondition — the box's checkout does not have this file yet (`#0466`).**
Re-derived read-only, 2026-09-08: `/opt/opencircuit`'s checkout is still at
`ef0a58f` (`#0274`, 2026-08-25), which predates `deploy/certbot/
reload-apache-deploy-hook.sh` by every commit since `8fc216d` — the directory
`/opt/opencircuit/deploy/certbot/` does not exist there at all. Step 1 below
fails at its first command, `sudo install -m 0755 deploy/certbot/
reload-apache-deploy-hook.sh …`, with "No such file or directory" until this
is done. **`scp` this one file, not `git pull`:** the checkout is several
hundred commits behind `main` and a `git pull` would also pull in a
`go build`/`npm run build` this box's 418 MB of RAM makes expensive
(`CLAUDE.md` §7), for a change that needs neither the new binary nor the
SPA — only this one script. Run from the local repo checkout, not on the
box:

```bash
ssh ec2 mkdir -p /opt/opencircuit/deploy/certbot
scp deploy/certbot/reload-apache-deploy-hook.sh \
  ec2:/opt/opencircuit/deploy/certbot/reload-apache-deploy-hook.sh
```

Then confirm the bytes landed correctly (recompute the left-hand value
locally with `shasum -a 256` if `main` has moved since this was written):

```bash
ssh ec2 sha256sum /opt/opencircuit/deploy/certbot/reload-apache-deploy-hook.sh
```

Expect:

```
6fa010d0f74e3d87acc67bbca65fe08295d12270cca6f5961346f685f67f97ee  /opt/opencircuit/deploy/certbot/reload-apache-deploy-hook.sh
```

**This is a change to production and needs the user's explicit approval
before any of it runs on the box** (`CLAUDE.md` §5b, §9). Nothing below has
been run. In order (from `/opt/opencircuit`, per the precondition above):

```bash
# 1. Install the hook (idempotent; safe to re-run).
sudo install -m 0755 deploy/certbot/reload-apache-deploy-hook.sh \
  /etc/letsencrypt/renewal-hooks/deploy/reload-apache.sh

# 2. Confirm it is in place.
sudo ls -l /etc/letsencrypt/renewal-hooks/deploy/

# 3. Exercise the renewal path for THIS certificate only, against Let's
#    Encrypt's *staging* server, with the deploy hook enabled, without
#    touching the real certificate. --cert-name is required: without it
#    certbot simulates renewal of all eight lineages on this box, two of
#    which use the apache authenticator and would temporarily rewrite and
#    reload Apache config for unrelated domains.
sudo certbot renew --cert-name opencircuitsf.com --dry-run --run-deploy-hooks

# 4. Confirm the site is still serving correctly afterward.
curl -fsS https://www.opencircuitsf.com/health
```

**What step 3 proves, and what it does not.** `certbot --help renew`
documents that `--dry-run` alone does *not* run deploy hooks at all
(`--deploy-hook commands do not run, unless enabled by --run-deploy-hooks`),
so `--run-deploy-hooks` is required for this to be a proof of anything.
With it: certbot obtains a **test, invalid** certificate from the staging
server, does **not** save it to disk, and then — because the dry run
succeeded — runs the deploy hook using `RENEWED_LINEAGE` pointing at the
**real, currently active** certificate (per `certbot --help renew`'s own
description of the flag), not the temporary staging one. So this proves the
hook is discovered, that it runs, that `httpd -t` and `systemctl reload
httpd` both succeed, and that Apache goes on serving correctly afterward —
i.e. the exact reload mechanics that would fire on a real renewal. It does
**not** prove a real certificate gets issued or deployed (the dry run
explicitly discards its test cert), and it does not exercise the timer unit
itself — `certbot-renew.service` still runs the unmodified
`certbot renew --quiet` with neither flag, relying on the same
unconditional deploy-hooks-directory scan rather than on anything this dry
run adds. The two are the same scan mechanism, but only the real timer
invocation is proof of the timer path specifically.

**What step 3 does to the live box.** With `--cert-name` it touches one
lineage: certbot writes and then deletes a `_acme-challenge` TXT record in
this domain's Route 53 zone, discards the staging certificate, and runs the
deploy hook once — one `httpd -t` and one `systemctl reload httpd`, which
is `httpd -k graceful` and drains in-flight requests rather than dropping
them. Expect a single reload and no interruption. **Do not drop
`--cert-name`.** Unscoped, `certbot renew --dry-run` simulates renewal of
every lineage on this host regardless of expiry, and two of them
(`www.beerbeerbeer.me`, `www.eurekaplatforms.com`) are configured with
`authenticator = apache`, which — as `certbot --help renew` warns of
`--dry-run` — temporarily modifies and rolls back webserver configuration
on the Apache process this site shares.

`www.opencircuitsf.com` resolves and serves over TLS today: re-verified
read-only for this pass, `curl -sI https://www.opencircuitsf.com/` returns
`HTTP/1.1 200 OK` and the certificate above is valid to 2026-11-16, matching
the production-facts table.

---

## 10. First admin login

`seed` (step 6) creates the admin user row but does **not** enroll a
passkey — the **only** path to the first passkey is **"Recover account"**,
not "Register". Registration rejects an email that already has a user row,
so trying to register `ADMIN_EMAIL` after seeding silently does nothing.
Recovery adds a passkey to an existing account without creating a new user
and does not check the `registrations_enabled` gate — the correct path for
an account that exists but has no passkey yet (mirrors ShortLinks
`DEPLOYMENT.md` step 10).

1. Open the site and click **"Recover account / lost passkey"** on the login
   page.
2. Enter the `ADMIN_EMAIL` address and submit. The page shows a generic
   confirmation regardless of whether the address exists (email enumeration
   is a first-class concern here, `CLAUDE.md` §9).
3. **Email delivery must be working** — SES configured, `MAILER_NOOP=false`
   in `/etc/opencircuit/config.env` — before this step succeeds. See **SES
   setup** below; SES exists but the account is still sandboxed, so a
   recovery mail only reaches an address that is itself a verified SES
   identity until production access lands (`CLAUDE.md` §10 item 2).
   `MAILER_NOOP=true` logs the recovery link to stdout instead
   (`docs/configuration.md`), which is a `dev.sh`/local-only substitute,
   never a production setting — `cmd/opencircuit/main.go`'s
   `checkMailerNoOp` refuses to start it outright on a production
   `BASE_URL` (`CLAUDE.md` §10).
4. Follow the magic link from the recovery email. The browser opens the
   recovery ceremony page and calls `navigator.credentials.create()` —
   WebAuthn requires HTTPS, so TLS (step 9) must already be live.
5. You are redirected in as the admin user; `is_admin` is preserved
   throughout recovery.

**Enabling registration for other users.** `registrations_enabled` defaults
to `false`. Once signed in as admin, toggle it on under **Admin → Settings**
before inviting anyone else to register — non-admin users use the
**Register** form (not Recover account) and complete an email verification
step.

**Corrected `#0431`, 2026-09-04 — both premises below are now false.** SES is
live (`mailing.opencircuitsf.com` verified, DKIM and MAIL FROM `SUCCESS`,
`CLAUDE.md` §10 item 2), and the instance has served `www.opencircuitsf.com`
since 2026-08-25 — re-verified read-only for this pass with a live `curl`
(see the Production-facts correction above). **What is still genuinely
unverified is this specific ceremony, not the infrastructure it needs**: this
pass did not itself click through "Recover account" against the live site,
and nothing in this document's own record of the 2026-08-25 deploy or its
later corrections claims that anyone has. Locally, `#0008`'s manual
verification procedure exercises the equivalent flow with `MAILER_NOOP=true`
logging the link to stdout, which remains the closest thing to a proof this
specific step has — that much of the original note still stands.

**Original note, left for context — accurate only for the pre-deploy
state:** Not run against a real deploy — no SES account exists to send the
recovery email through yet (`CLAUDE.md` §10 item 2), and no live instance
exists to receive the HTTPS request. Locally, `#0008`'s manual verification
procedure exercises the equivalent flow with `MAILER_NOOP=true` logging the
link to stdout — that is the closest thing to a proof this step has.

---

## DNS (Route 53) — real values from `PRD.md` §10.2

| Name | Type | Value | Purpose |
|---|---|---|---|
| `www.opencircuitsf.com` | CNAME | `ec2.smallsharptools.com` (same box) | **Canonical host** — a CNAME in practice, not the A record this table stated until corrected 2026-09-05, `#0448` (same target as `go.opencircuitsf.com` below) |
| `opencircuitsf.com` | A | `44.222.209.183` | 301 → `www` |
| `*.opencircuitsf.com` | CNAME | `ec2.smallsharptools.com` (same box) | Wildcard — why `go.`, `_dmarc.`, and `lists.` resolve without records of their own (added 2026-09-05, `#0448`) |
| `go.opencircuitsf.com` | CNAME | `ec2.smallsharptools.com` (same box) | ShortLinks — a CNAME in practice, not the A record PRD §10.2 planned |
| `<sel1..3>._domainkey.mailing.opencircuitsf.com` | CNAME | `[PLACEHOLDER: issued by SES on domain verification, PRD §10.2/§10.4]` | DKIM (parent corrected 2026-09-05, #0436 — this row named the apex; see below) |
| `bounce.mailing.opencircuitsf.com` | MX | `10 feedback-smtp.us-east-1.amazonses.com` | Custom MAIL FROM (host and region corrected 2026-09-04, #0421 — this row read `mail.opencircuitsf.com`/`us-west-2`; see below) |
| `bounce.mailing.opencircuitsf.com` | TXT | `v=spf1 include:amazonses.com ~all` | SPF alignment |
| `mailing.opencircuitsf.com` | TXT | `v=spf1 include:amazonses.com -all` | SPF for the `From:` domain — added 2026-09-05, `#0452`, present in the live zone and in `docs/email-setup.md` but previously missing from this table entirely. **Hard-fail `-all`, not `~all`** like the `bounce.mailing.` row above: nothing but SES ever sends as this exact name, so a hard fail is safe and stronger — a future editor softening it to `~all` should do so knowingly, not by copying the envelope-domain row's qualifier |
| `lists.opencircuitsf.com` | MX | `10 inbound-smtp.us-east-1.amazonaws.com` *(planned — not created yet)* | **Inbound unsubscribe only** — never the apex MX, `CLAUDE.md` §9 (region corrected 2026-09-04, #0421 — was `us-west-2`; existence checked 2026-09-05, `#0448` — the zone has no record at this name today, matching `docs/email-setup.md`'s "Not created, on purpose"; Phase 4, `#0057`) |
| `_dmarc.mailing.opencircuitsf.com` | TXT | `v=DMARC1; p=none; rua=mailto:contact@opencircuitsf.com; fo=1` | DMARC — **deliberately on the `mailing.` subdomain, not the apex** (name, value, and alignment tags corrected 2026-09-05, `#0427` — this row previously named `_dmarc.opencircuitsf.com`, which has no DMARC record in the live zone, and asserted `adkim=s; aspf=s`, which the real record does not carry; see below) |

Every record name, type, and static value above started as a verbatim copy
from `PRD.md` §10.2; where the live zone disagrees, the row is corrected in
place and dated, not silently overwritten.

**Corrected `#0431`, 2026-09-04 — the paragraph below is stale on both
counts.** The public IP is no longer unknown — it is `44.222.209.183`, the
same value already used two rows above in this very table, and the instance
that owns it has existed since 2026-08-25. The DKIM CNAME targets are also no
longer unknowable in principle: SES reports the `mailing.opencircuitsf.com`
identity's DKIM as `SUCCESS` (`docs/email-setup.md`, `CLAUDE.md` §10 item 2),
which means domain verification completed and the real selector tokens exist
in Route 53 today. This pass did not read them out of the AWS console or DNS
to duplicate them here — `docs/email-setup.md` is the place to look, and
keeping the actual token values in one place avoids the drift this table
already suffered once (`#0421`, below). ~~**Worth a closer look, not fixed by
this pass:** `docs/email-setup.md`'s own DKIM row names
`<3 tokens>._domainkey.mailing.opencircuitsf.com` — under the `mailing.`
subdomain — while this table's row names `<sel1..3>._domainkey.opencircuitsf.com`,
the apex. Those are different hostnames; this document did not resolve which
one is correct or reconcile the two, and it should not be trusted to name
the right hostname until that is checked.~~ **Corrected 2026-09-05, #0436:**
the `mailing.` form is right, matching `docs/email-setup.md`'s own row and
`CLAUDE.md` §9's rule that the apex carries real Google Workspace mail and is
restricted — a DKIM record published at the bare apex would be both wrong and
adjacent to that restriction. The table row above now names the same
`mailing.opencircuitsf.com` parent.

**Original note, left for context — accurate only for the pre-instance
state:** the two things that could not be known before an instance existed
were the Elastic IP and the SES-issued DKIM CNAME targets — both explicitly
placeholdered rather than guessed, per that pass's instructions not to
invent `CLAUDE.md` §10 item 6 facts. **Re-synced 2026-09-04 (#0421)** with the
MAIL FROM host and region `#0418` corrected in `PRD.md` §10.2 on 2026-09-03 —
this table had drifted from its own cited source in the interim, which is the
fact `#0301`'s "correctly scoped" verdict on this file's `us-west-2`
occurrences did not anticipate; see that issue for the correction note.

**DMARC ramp — three steps, not one record.** Start `p=none` for at least
two weeks and read the aggregate (`rua=`) reports, then move to
`p=quarantine`, then `p=reject` once the reports show DKIM/SPF passing
cleanly. Jumping straight to `p=reject` risks silently dropping legitimate
mail with no visibility into why. This applies to
`_dmarc.mailing.opencircuitsf.com`, not the apex — see below.

**DMARC record corrected 2026-09-05, `#0427`.** Both this table and `PRD.md`
§10.2 named `_dmarc.opencircuitsf.com`; re-derived with `dig`, that name
resolves through the zone's `*.opencircuitsf.com` wildcard to
`ec2.smallsharptools.com` — a CNAME, not a DMARC policy. No DMARC record
exists at the apex at all. The real, live record is at
`_dmarc.mailing.opencircuitsf.com` (`v=DMARC1; p=none;
rua=mailto:contact@opencircuitsf.com; fo=1`, matching
[`email-setup.md`](email-setup.md) and confirmed by `aws route53
list-resource-record-sets` and `dig`), and that placement is deliberate: list
mail sends from the `mailing.` subdomain precisely so the apex — which
carries real Google Workspace mail (`CLAUDE.md` §9, §10 item 5) — is
untouched. Publishing a DMARC policy at the apex would govern that Workspace
mail, not this project's. The live record also does **not** carry
`adkim=s; aspf=s` — this pass records that as the current state, not as an
aspiration to reach; adding strict alignment is a real policy decision for
whoever owns the `p=quarantine`/`p=reject` step of the ramp above to make
deliberately, against the record that then exists, not something to restore
here because an older draft of this table asserted it.

`www.opencircuitsf.com`'s row above was corrected the same pass, `#0448`:
the zone holds a CNAME to `ec2.smallsharptools.com`, not an A record to a
literal IP, so a rebuild that followed the old row would have pinned `www` to
an address that has to be maintained by hand instead of following the
indirection the `go.` row already documented. The rest of this table was
swept against `aws route53 list-resource-record-sets` for the same pass;
the apex `A`, DKIM CNAMEs, and `bounce.mailing.` MX/TXT rows all matched
the live zone exactly and were left as they were, per `#0448` criterion 4.
One documented name matches only through the wildcard:
`go.opencircuitsf.com` has **no record of its own** in the zone, and
resolves to `ec2.smallsharptools.com` solely because
`*.opencircuitsf.com` is a CNAME to that name — the same mechanism that
makes `_dmarc.opencircuitsf.com` and `lists.opencircuitsf.com` appear to
resolve. Its row's "a CNAME in practice" wording is accurate as written
and stays, but nothing pins `go.` independently, so narrowing or removing
the wildcard would break it silently. `#0057`, which will add records
here, should treat that wildcard as load-bearing (checked 2026-09-05,
`#0448`).

**Mirror-image sweep, `#0452`, 2026-09-05** — `#0448`'s sweep above checked
this table's existing rows against the live zone for false statements; this
pass is the reverse, checking the live zone for records the table omits
entirely. `aws route53 list-resource-record-sets` for this hosted zone
returns 15 record sets, matching the `ResourceRecordSetCount` that
`aws route53 list-hosted-zones` reports for it. Of those, one was a genuine
omission and is now the `mailing.opencircuitsf.com` SPF row added above.
Six were already documented rows and needed no change: the apex `A`, the
`*.opencircuitsf.com` wildcard `CNAME`, `www.opencircuitsf.com`'s `CNAME`,
`_dmarc.mailing.opencircuitsf.com`'s `TXT`, and
`bounce.mailing.opencircuitsf.com`'s `MX` and `TXT`. The remaining eight
are deliberately left out of this table, not missed:

- The zone's `NS` and `SOA` records at the apex are hosted-zone
  infrastructure that Route 53 creates automatically, not application DNS
  this project manages; every zone has them.
- The apex `MX` (`1 SMTP.GOOGLE.COM.`), the apex `TXT`
  (`google-site-verification=…`), and `google._domainkey.opencircuitsf.com`
  `TXT` all belong to the pre-existing Google Workspace mailboxes at the
  apex (`CLAUDE.md` §7, §9, §10 item 5) — human mail this project must never
  touch, not part of the `mailing.` subsystem this table documents. The apex
  MX in particular is covered by its own restriction elsewhere in this
  document and in `CLAUDE.md` §9 rather than by a row in this DNS table.
- The three DKIM `CNAME` records under `mailing.opencircuitsf.com` are
  already represented by this table's single `<sel1..3>._domainkey…`
  placeholder row (real selector tokens deliberately not reproduced here —
  see `docs/email-setup.md`).

Nothing else in the zone is absent from the table. Re-derived read-only via
`aws route53 list-resource-record-sets --hosted-zone-id Z0825067RV8QY5UIKS96`;
nothing in DNS was changed and the apex MX was not touched.

---

## SES setup

`CLAUDE.md` §10 item 2 recorded this as **not started** when this section was
written (deferred to deployment, user, 2026-08-23) — the only AWS credentials
available in this development environment (`certbot-dns-updater`) cannot even
`ses:ListEmailIdentities`, so nothing below had been run, and nothing in the
pass that wrote it attempted to run it. This section documented the steps
planned to run **on the real box, once the AWS account exists**.

> **This plan was not what actually happened (2026-09-04, #0421).** SES has
> since been set up for real, and item 1 below was decided differently: the
> real deploy used **`us-east-1`**, not `us-west-2` — matching the EC2
> instance's own region, not "closest to San Francisco" (`CLAUDE.md` §7,
> `PRD.md` §10.3, corrected by `#0418`). The custom MAIL FROM host is also
> different from item 3 below: `bounce.mailing.opencircuitsf.com`, not
> `mail.opencircuitsf.com`. The numbered steps are left as originally written
> — an accurate record of the plan as transcribed at the time — but they are
> **not** a description of what is live. For what is actually configured, see
> [`email-setup.md`](email-setup.md) (current) and this document's
> *Production facts* table above, not the list below.

1. **Region: `us-west-2`.** Closest to San Francisco and one of the shorter
   list of regions supporting SES **inbound** receiving (needed for the
   `mailto:` unsubscribe path, `PRD.md` §10.3). Verify the current
   inbound-region list before committing, since AWS revises it.
2. **Verify the domain** (`opencircuitsf.com`) in SES, enable **Easy DKIM**,
   and add the resulting CNAME records to Route 53 (the DNS table above).
3. **Custom MAIL FROM** — `mail.opencircuitsf.com`, with the MX and SPF TXT
   records already listed above.
4. **DMARC** — the ramp described in the DNS section above.
5. **Request production access.** New accounts are sandboxed (200
   messages/day, verified recipients only); approval takes roughly 24 hours
   and everything downstream depends on it, so request this **early**, not
   after everything else is ready. Describe the use case honestly: opt-in
   announcement email for a community electronics workshop group, double
   opt-in, one-click unsubscribe, bounce and complaint handling wired to
   suppression (`PRD.md` §10.4).
6. **A configuration set with open/click tracking disabled.** This is a
   deploy-time fact the code cannot enforce and no test in this repo can
   observe — an enabled configuration set injects a tracking pixel
   **server-side**, at send time, which would violate `CLAUDE.md` §9's
   no-open-tracking rule with zero code change and no visible symptom until
   someone inspects a delivered message's HTML. Create
   `opencircuit-transactional` (matching `.env.example`'s
   `SES_CONFIGURATION_SET` default) with tracking explicitly off.
7. **Enable SES's account-level suppression list** as belt-and-suspenders
   alongside this project's own `suppressions` table (`PRD.md` §6.7,
   `email-setup.md`'s own runbook step):

   ```bash
   aws sesv2 put-account-suppression-attributes \
       --suppressed-reasons BOUNCE COMPLAINT \
       --region us-west-2
   ```

8. **IAM instance role** — see the next section.
9. Fill in the SES-dependent variables in `/etc/opencircuit/config.env`
   (step 3) and restart: `AWS_REGION`, `SES_CONFIGURATION_SET`, `EMAIL_FROM`,
   `EMAIL_REPLY_TO`, `EMAIL_LIST_DOMAIN`, `SES_INBOUND_BUCKET`. Set
   `SES_SANDBOX=true` (or leave it, since `.env.example` defaults it true)
   until step 5's production-access approval lands, and correspondingly cap
   `MAX_SEND_RATE=1` — see step 3 above.

**Corrected `#0431`, 2026-09-04 — "none of the above was executed" is no
longer true and contradicts the blockquote above it.** This numbered list is
a transcription of `PRD.md` §10.2–§10.4 and `email-setup.md` into deploy
order, and most of it *was* substantially executed on 2026-08-25 — with the
region and MAIL FROM host the blockquote above already names as different
from what is written here. What genuinely was not executed by that pass:
step 5's production-access request (`CLAUDE.md` §10 item 2 — the account
remains sandboxed) and step 8's IAM role (also since done — see `## IAM`
below). Treat this numbered list as the plan as originally transcribed, not
as a checklist of what remains; `email-setup.md` and the Production-facts
table above are the current-state record.

---

## IAM

The EC2 instance role must be scoped tightly (`PRD.md` §10.5) — no static
credentials anywhere in this project's configuration; the AWS SDK's default
credential chain picks up the instance role automatically
(`docs/configuration.md`'s `AWS_REGION` row).

**The role is attached** — `opencircuit-instance`. Re-derived for this issue
(`#0426`, 2026-09-04) directly from the instance metadata service
(`iam/security-credentials/`, read-only via `ssh ec2`), not copied from the
filing; `CLAUDE.md` §10 item 2 already records it as attached and proven by
a real delivered send.

The JSON below matches `PRD.md` §10.5's **target** shape, including the
`InboundBucketScoped` statement for the inbound `mailto:` path (`#0057`),
which is not built yet — so this block is still partly a plan, not a
transcription of what is attached today. The `SESSendScoped` statement,
region, and account ID are corrected to match reality:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "SESSendScoped",
      "Effect": "Allow",
      "Action": ["ses:SendEmail", "ses:SendRawEmail"],
      "Resource": [
        "arn:aws:ses:us-east-1:378152330719:identity/*",
        "arn:aws:ses:us-east-1:378152330719:configuration-set/opencircuit-transactional"
      ]
    },
    {
      "Sid": "InboundBucketScoped",
      "Effect": "Allow",
      "Action": ["s3:GetObject", "s3:DeleteObject"],
      "Resource": "arn:aws:s3:::opencircuitsf-inbound/unsubscribe/*"
    }
  ]
}
```

**Correction (`#0057`, 2026-09-11) — `InboundBucketScoped`'s `Resource`
narrowed to the `unsubscribe/` prefix.** `PRD.md` §10.5 itself still reads
`arn:aws:s3:::opencircuitsf-inbound/*` (the whole bucket); `#0057`'s
planning pass corrects this against its own AWS object table (row A11): the
grant should be **the prefix, not the bucket**, since nothing else is
planned to live in this bucket but there is no reason to grant broader
access than the one prefix `#0058`'s handler actually reads. The
ready-to-apply version of this exact statement is
[`deploy/aws/A11-iam-inline-policy.json`](../deploy/aws/A11-iam-inline-policy.json).
`docs/aws-iam-setup.md`'s "What is deliberately *not* in it" section still
shows the bucket-wide form and has the same staleness — reported, not fixed
here, since that file's "not built yet" framing needs its own pass once
`#0057`'s AWS objects actually exist. `PRD.md` §10.5 is unedited by this
pass (out of scope for `#0057`); the disagreement is reported to the
orchestrator for filing.

**Correction (`#0426`, 2026-09-04) — region, account ID, and identity were
all wrong.** This block previously read
`arn:aws:ses:us-west-2:<ACCOUNT_ID>:identity/opencircuitsf.com` with
`<ACCOUNT_ID>` marked `[PLACEHOLDER: unknown until the account from
CLAUDE.md §10 item 2 exists]`, a `Condition` restricting `ses:configuration-set`
to `opencircuit-transactional`, and the note "**this policy has never been
created or attached to a real role**". None of that is still true. The
region is `us-east-1`, not `us-west-2` (`#0418`); the account is
`378152330719`, not unknown; and the identity is `mailing.opencircuitsf.com`
(mail sends from that subdomain, never the apex — `docs/aws-iam-setup.md`),
not `opencircuitsf.com` — except the policy does not name that identity
either, for the reason below.

**Why `identity/*` and not the one identity mail actually sends from.**
`CLAUDE.md` §10 item 2 records the trap: **while the account sits in the
SES sandbox, `ses:SendEmail`/`ses:SendRawEmail` are authorized against the
*recipient's* identity ARN as well as the sender's**, so a policy naming
only `identity/mailing.opencircuitsf.com` fails every send to a recipient
address that is not itself a verified SES identity — which is every real
subscriber. `docs/aws-iam-setup.md`'s "Why `identity/*` and not just the
sending domain" section documents this as something that happened for
real, not a hypothetical: sends to the admin address failed with
`AccessDeniedException` until the policy was corrected, and the durable
outbound queue (`#0126`) is what kept those retries from being lost during
the ~14 minutes that took. **A smoke test against
`success@simulator.amazonses.com` does not exercise this at all** —
simulator addresses are not SES identities, so no recipient-resource check
ever fires against them; only a send to a real verified address does. The
configuration-set restriction moved from a `Condition` key to its own
`Resource` ARN in the corrected policy, matching what
`docs/aws-iam-setup.md` documents as actually attached — SES v2
`SendEmail` authorizes the configuration set as a separate resource, not a
condition key, so the original `ses:configuration-set` condition was never
validated and is now moot rather than corrected.

**Could not independently re-read the live policy for this pass.** The
box's default AWS CLI identity is `certbot-dns-updater`, which is denied
IAM reads: `aws iam get-role`, `list-role-policies`, and
`list-attached-role-policies` against `opencircuit-instance` each returned
`AccessDenied` (checked read-only, 2026-09-04; no credentials were created
or sought). The corrected JSON above is transcribed from
`docs/aws-iam-setup.md`'s own account of the attached policy and the
production incident that shaped it, not from a live `GetRolePolicy` call
this pass could make itself.

The JSON parses (`python3 -m json.tool`, see `## Verification`) — that only
proves it is well-formed, not that IAM has accepted every key in it.

---

## Backups (`PRD.md` §10.6, `#0062`, `#0228`, `#0229`)

The subscriber list is the single most valuable and least reconstructible
asset in the system. `#0062` performed and verified a full restore drill
against a local PostgreSQL 16 cluster, `#0228` closed a real ownership defect
the drill's own review found, and `#0229` reconciled the specification with
what actually shipped and built the schedule. **Everything below is either
already implemented, or explicitly marked as not yet built / not
re-verified on the server.**

### What actually exists today — `PRD.md` §10.6 corrected to match (`#0229`)

`PRD.md` §10.6 used to describe **S3** as the backup target (30-day S3
lifecycle, encryption at rest, a bucket that blocks public access, IAM scoped
to a bucket prefix). That was never what shipped, and `#0229` corrected the
PRD rather than leave it describing infrastructure nobody was building — see
its `## Decision`. The scripts actually in this repo (`scripts/db/backup.sh`,
`pull-backups.sh`, `restore.sh` — copied from ShortLinks in `#0001`, generic
enough to work unmodified for the `opencircuit` database) implement a
**different, already-working design**, now also §10.6's design of record:

1. `backup.sh` runs `pg_dump -Fc` (or `--format=plain | gzip`) into a local
   directory tree, one subfolder per database, on the database host itself
   (`/var/backups/postgres/<db>/` by default). It prunes dumps older than
   `BACKUP_RETENTION_DAYS` (default 14) after each successful run.
2. `pull-backups.sh` runs on a **separate** machine (a Mac mini, per its
   header comment) and `rsync`s the whole backup tree over SSH — a pull,
   not a push, so a compromised server never holds credentials for the
   offsite copy.
3. `deploy/systemd/opencircuit-backup.timer` fires `backup.sh` nightly, and
   its `OnFailure=` chains to `opencircuit-backup-alert.service`
   (`scripts/db/backup-alert.sh`) so a failed run reaches a human rather than
   just leaving a non-zero exit code nobody reads (`#0229`; see "Schedule and
   failure alert" below).

This gets nightly dumps, an offsite copy, and a wired-up failure alert
without AWS, but it is **not** S3, has **no bucket-level
encryption/public-access-block/IAM scoping** (file permissions on the server
and the Mac mini are what protect it — `backup.sh` already `chmod 0700`s the
backup root and `chmod 0600`s each dump), and its retention is a local
`find -mtime` prune, not an S3 lifecycle rule.

**S3 upload is a deferred option, not abandoned.** ~~`#0229`'s `## Decision`:
building it needs an AWS account, which does not exist yet (`CLAUDE.md` §10
item 2), and the user deferred all AWS work to deployment, same as SES.~~
**Correction (`#0430`, 2026-09-05):** both grounds `#0229` gave have lapsed —
the AWS account exists (`378152330719`, re-verified read-only via `aws sts
get-caller-identity` for this issue) with SES live in it, and deployment
itself happened on 2026-08-25, so "deferred until deployment" has also
passed. **No replacement decision to build S3 upload is recorded** — it
remains simply not built, on no current stated grounds. `PRD.md` §10.6
records S3 as an addable upload leg on top of `backup.sh`, not a redesign —
nothing about the local-dump/offsite-pull shape needs to change if it is
added later.

Run locally against a disposable PostgreSQL 16 cluster (Homebrew, macOS),
never against a database anyone depends on. Every database name below is
throwaway and none collides with `scripts/testdb.sh`'s per-agent pool.

**#0239: this drill used to be unfollowable as written.** The old step 4b
asked for the source database to be seeded "one migration behind HEAD"
*before* step 1 — but step 1 was `scripts/testdb.sh create`, whose
`check_template_fresh` guarantees the template (and therefore every clone of
it) is at HEAD. Followed literally, step 4b had nothing pending to apply: it
silently no-op'd and the drill reported success having proved nothing about
a real restore-then-migrate path — exactly the failure `#0235` needed a real
drill to catch. The fix is step 1b below: roll back one migration *after*
creating the source database, which is both reachable (nothing here fights
`check_template_fresh`) and honest about what's happening (a real
`migrate ... down 1`, not a hand-waved "seed it behind" with no mechanism to
do so).

**#0247: every database, directory, and filename below is namespaced per
run, reusing this project's own `ISSUE=NNNN` / `scripts/testdb.sh` convention
(`CLAUDE.md` §5a) rather than a second scheme invented for this drill.** The
drill used to hardcode `0062src`, `opencircuit_test_0062dst`, and
`/tmp/backup-drill` (including a fixed `…-latest.dump` inside it) — fine for
one person running it alone, but this is also the document most likely to be
followed by two people at once (an incident, a rehearsal), and it creates
and **drops** databases: a collision is one run destroying another's target
mid-restore, not a confusing error message. Step 1 below is where the
namespace is actually picked, since that is the first place one is needed —
not here, where it is easy to skip past.

```bash
# 1. Pick a namespace for this run, then create the drill's source
#    database. Reuse the project's own ISSUE=NNNN convention
#    (CLAUDE.md §5a / scripts/testdb.sh) rather than inventing a second
#    naming scheme — set ISSUE to your own issue number if you have one, or
#    any short alphanumeric token two concurrent runs are unlikely to pick
#    in common (your shell's PID, the default below, is a reasonable
#    choice). Every database, directory, and filename from here on is
#    derived from it, which is what lets two people run this drill at the
#    same time without colliding (#0247):
ISSUE="${ISSUE:-$$}"
#    scripts/testdb.sh lowercases the database name it derives from its
#    argument (Postgres identifiers are effectively case-folded); every
#    OTHER ${ISSUE} interpolation below (BACKUP_ROOT, the dump path,
#    DST_DSN) does not. A mixed-case token — an issue id is fine, but
#    something like a branch name is not — makes step 4's
#    opencircuit_test_${ISSUE}src miss the database step 1 actually
#    created. Normalize once, here, rather than requiring every follower to
#    remember to type it in lowercase (#0248 review). Use `tr`, not
#    `${ISSUE,,}` — that expansion is bash 4+, this machine's only bash is
#    3.2.57, and on 3.2 it fails with "bad substitution" and leaves ISSUE
#    unchanged, silently reopening the exact trap this line exists to close
#    (#0248, second review pass). scripts/testdb.sh:120 already lowercases
#    this way; match it. Keep the token alphanumeric — unlike
#    scripts/testdb.sh's name_for(), this does not also strip other
#    characters, so e.g. a hyphen in ISSUE will desync the same way case did:
ISSUE="$(printf '%s' "$ISSUE" | tr '[:upper:]' '[:lower:]')"
echo "Running the drill under ISSUE=${ISSUE}"

# 1a. #0256: everything downstream of "which migration is HEAD" used to be
#    written down as a fixed number (20) and a fixed table name
#    (email_campaigns) — three passes (#0239, #0248, and the review that
#    filed #0256) each corrected those numbers as migrations/ grew, and
#    each correction went stale again the next time a migration landed
#    (#0126's 000021/000022 invalidated #0248's fix in about twenty
#    minutes). Derive them instead, from migrations/ itself, so this
#    section is correct at whatever HEAD happens to be when you run it:
HEAD_FILE="$(ls migrations/*.up.sql | sort | tail -1)"
HEAD_VERSION="$(basename "$HEAD_FILE" | sed -E 's/^0*([0-9]+)_.*/\1/')"
#    NEW_TABLES: every table HEAD's own migration CREATE TABLEs outright —
#    these exist only on the restored side after step 6 and are excluded
#    from every step 7 comparison below, the same way workshops/
#    workshop_interests always were when migration 20 was HEAD. Comments
#    are stripped first (`grep -vE '^[[:space:]]*--'`) — a prose sentence
#    like "attached with an ALTER TABLE" would otherwise read as a second
#    ALTER TABLE statement (caught while building this derivation: 000020's
#    own comments contain exactly that phrase). The identifier class is
#    `[a-z_][a-z0-9_]*`, not `[a-z_]+` — a table name containing a digit
#    (e.g. this section's own proof migration, scratch_probe_0256) was
#    silently truncated at the first digit under the letters-only class,
#    which desyncs every exclusion built from it; caught by proving this
#    section against exactly such a migration, not by inspection:
NEW_TABLES="$(grep -vE '^[[:space:]]*--' "$HEAD_FILE" | grep -oE 'CREATE TABLE [a-z_][a-z0-9_]*' | awk '{print $3}' | sort -u)"
#    ALTERED_TABLES: pre-existing tables HEAD's migration modifies via
#    ALTER TABLE (as opposed to tables it creates outright) — the ONLY
#    tables step 7's constraint-count diff may legitimately show a
#    difference for. Empty is a valid, common answer: a migration that only
#    creates new tables (HEAD's, right now) touches no pre-existing table's
#    constraints at all.
ALTERED_TABLES="$(grep -vE '^[[:space:]]*--' "$HEAD_FILE" | grep -oE 'ALTER TABLE [a-z_][a-z0-9_]*' | awk '{print $3}' | sort -u | grep -vxF "$NEW_TABLES" || true)"
#    EXCLUDE_SQL: NEW_TABLES rendered as a SQL IN-list, quoted and
#    comma-joined without relying on word-splitting a possibly multi-line
#    shell variable (fragile — tested and rejected while building this).
#    '__none__' is a placeholder for "nothing to exclude" — Postgres
#    rejects a bare `not in ()`, and no real table will ever be named it:
EXCLUDE_SQL="$(printf '%s\n' "$NEW_TABLES" | sed "s/.*/'&',/" | tr -d '\n')"
EXCLUDE_SQL="${EXCLUDE_SQL%,}"
[ -n "$EXCLUDE_SQL" ] || EXCLUDE_SQL="'__none__'"
echo "HEAD migration: ${HEAD_VERSION} ($(basename "$HEAD_FILE"))"
echo "Tables it creates outright (excluded from every step 7 comparison): ${NEW_TABLES:-none}"
echo "Pre-existing tables its up.sql alters (the only ones step 7's constraint diff may legitimately show): ${ALTERED_TABLES:-none}"

#    scripts/testdb.sh clones the fully-migrated template —
#    check_template_fresh guarantees this is at HEAD. Confirm it before
#    relying on it:
DSN="$(scripts/testdb.sh create "${ISSUE}src")"
psql "$DSN" -c "select version, dirty from schema_migrations"
#    Expect: version=${HEAD_VERSION}, dirty=false. If this isn't HEAD, stop
#    — step 1b below has nothing real to roll back and the rest of the
#    drill proves nothing about a pending migration.

# 1b. #0239: roll back exactly ONE migration so the source database is
#    genuinely HEAD-1, not merely described as such. This is what the old
#    step 4b could never produce on its own — do it here, after creation,
#    with the real down migration, not a synthetic approximation of one:
migrate -path migrations -database "$DSN" down 1
psql "$DSN" -c "select version, dirty from schema_migrations"
#    Expect: version=$((HEAD_VERSION - 1)), dirty=false. If this still reads
#    ${HEAD_VERSION}, STOP — the rollback didn't take, and everything below
#    would (again) silently prove nothing. $HEAD_FILE's own down.sql is what
#    just ran — read it to see exactly what it removed (for HEAD's current
#    migration: $NEW_TABLES, plus any constraint its up.sql had attached to
#    $ALTERED_TABLES).

# 2. Populate the source database with representative data spanning every
#    table that exists AT THIS SCHEMA VERSION ($((HEAD_VERSION - 1))) —
#    every table in the schema EXCEPT $NEW_TABLES, which doesn't exist yet
#    at this version (that's the entire point of step 1b) — users,
#    subscribers + subscriber_interests (referencing interests' existing
#    rows — migration 000009 already seeded the 12-row taxonomy; don't add
#    more, #0248 second review), suppressions, audit_log, email_campaigns +
#    campaign_interests + email_sends, email_events, and any table a
#    migration after 000009/000017 has since added to that list.
#    Coverage for $NEW_TABLES comes from step 8 below, once HEAD's
#    migration lands on the restored target. Seed throwaway rows — never
#    target a literal or seeded id (CLAUDE.md §8b). The two enums you'll
#    actually need here (checked against the CHECK constraints in
#    migrations/000012 and 000010, not guessed): suppressions.reason is one
#    of hard_bounce | complaint | manual | repeated_soft_bounce (NOT
#    "bounce"); subscribers.status is one of pending | active |
#    unsubscribed | bounced | complained (NOT "confirmed").
psql "$DSN" -f seed.sql                     # your own INSERT ... RETURNING id
                                             # statements

# 3. Snapshot what "correct" looks like BEFORE backing up: row counts per
#    table, schema_migrations (version=$((HEAD_VERSION - 1)), dirty=false),
#    every sequence's last_value (NOT row count — see "Sequences" below),
#    constraint count per table, index count per table.

# 4. Back up with the real script, current-user auth for local dev
#    (BACKUP_RUN_AS="" — see the bug note below; the server run uses the
#    default BACKUP_RUN_AS=postgres via sudo peer auth instead). Namespace
#    BACKUP_ROOT by ISSUE too (#0247) — backup.sh already names the dump
#    after the database (opencircuit_test_${ISSUE}src-latest.dump), so this
#    is a second, independent layer of collision protection rather than
#    relying on the filename alone:
BACKUP_ROOT="/tmp/backup-drill-${ISSUE}" BACKUP_RUN_AS="" \
  bash scripts/db/backup.sh "opencircuit_test_${ISSUE}src"
#    Success looks like: "ok — <size>" per database and a final
#    "Done — all databases backed up." with exit 0. Any pg_dump failure
#    leaves prior dumps untouched and the script exits non-zero.

# 5. Restore into a FRESH, differently-named database — never restore over
#    the source, so the drill can compare instead of destroying:
DST_DSN="postgres://opencircuit:<password>@localhost:5432/opencircuit_test_${ISSUE}dst?sslmode=disable"
BACKUP_RUN_AS="" RESTORE_CREATE=1 \
  bash scripts/db/restore.sh \
    "/tmp/backup-drill-${ISSUE}/opencircuit_test_${ISSUE}src/opencircuit_test_${ISSUE}src-latest.dump" \
    "opencircuit_test_${ISSUE}dst"
#    Success looks like: "Done. Verify with: psql -d <target> -c '\dt'" with
#    no pg_restore errors printed above it. pg_restore's --clean --if-exists
#    makes a re-restore idempotent, but RESTORE_CREATE=1 here creates a
#    database that must not already exist — it errors loudly if it does.
#    restore.sh (#0228) also reassigns ownership of every restored table,
#    sequence, and view to RESTORE_OWNER (default: opencircuit) after the
#    restore completes — see "Roles and ownership" below for why this step
#    exists and how to prove it, not just trust it.
psql "$DST_DSN" -c "select version, dirty from schema_migrations"
#    Expect: version=$((HEAD_VERSION - 1)), dirty=false — the restored copy
#    is genuinely missing HEAD's migration too, carried over faithfully
#    from the source dump. If this reads ${HEAD_VERSION}, the drill has
#    drifted from step 1b again and step 6 below will silently no-op — stop
#    and re-check, don't continue.

# 6. #0235's reproduction, reached by FOLLOWING this drill rather than
#    deviating from it (#0239's whole point): object ownership alone isn't
#    sufficient — restore.sh reassigns every table/sequence/view, but the
#    SCHEMA a still-pending migration will CREATE TABLE into must also be
#    usable by the app role, and this is where that gap showed up. Apply
#    the genuinely-pending migration:
migrate -path migrations -database "$DST_DSN" up
#    Success looks like HEAD's migration actually applying — migrate's own
#    output names it ("${HEAD_VERSION}/u <name> ...") — with no error, and
#    a following `migrate ... version` reporting ${HEAD_VERSION}, not
#    $((HEAD_VERSION - 1)). **"no change" or an unchanged version here is a
#    FAILURE of this drill, not a pass** — it means nothing was pending,
#    the exact silent-no-op #0239 exists to close. Don't declare success;
#    go back and check step 1b.
#    A `permission denied for schema public` here means the
#    schema-ownership step in `restore.sh` did not run or did not reach
#    `public` — this is the exact failure `#0235` found and fixed;
#    `restore.sh` now reassigns the schema itself
#    (`ALTER SCHEMA public OWNER TO …`), not just the objects inside it.
#    `RESTORE_OWNER=""` skips this step along with the object-level one —
#    expect the same permission-denied error in that case, deliberately.

# 7. Compare restored against source for every table that existed at both
#    schema versions — exclude $NEW_TABLES entirely (the table, its
#    sequences, its indexes, and its constraints alike; it exists only on
#    the restored side, post-migration): table-by-table row counts,
#    sequence last_values, and index counts all must match exactly.
#
#    Two things are EXPECTED to differ here, and a MATCH on either one
#    would mean step 6 silently no-op'd, not that the drill passed. (#0239
#    was bounced over exactly this: the old wording asked for these to
#    match too, which they structurally cannot once step 1b and step 6 are
#    both doing their job.)
#
#      - schema_migrations: source stays at version=$((HEAD_VERSION - 1))
#        (nothing ever migrated it past step 1b's rollback); restored reads
#        version=${HEAD_VERSION} (step 6 applied the pending migration to
#        it, and only it). Already proven above at step 5 and step 6 — not
#        a new check, just a reminder not to re-flag it as a mismatch here.
#      - the constraint-count diff below MAY show a difference on whichever
#        table(s) are in $ALTERED_TABLES — this is the one genuinely
#        invariant rule (found while correcting #0248's fix a second time,
#        #0256): the diff is confined to tables the just-reapplied
#        migration's own up.sql modifies via ALTER TABLE, is caused
#        entirely by that migration's own down.sql having removed the same
#        constraint(s), and is EMPTY whenever the migration only creates
#        new tables and touches nothing pre-existing — which is the case
#        for HEAD's migration right now. Do not expect a fixed table name
#        or a fixed line count; expect exactly this rule, checked below:
diff <(psql "$DSN" -tAc "select table_name, count(*) from information_schema.table_constraints where table_schema='public' and table_name not in (${EXCLUDE_SQL}) group by table_name order by 1") \
     <(psql "$DST_DSN" -tAc "select table_name, count(*) from information_schema.table_constraints where table_schema='public' and table_name not in (${EXCLUDE_SQL}) group by table_name order by 1") \
     > /tmp/constraint-diff-${ISSUE}.txt
cat /tmp/constraint-diff-${ISSUE}.txt
if [ -n "$ALTERED_TABLES" ]; then
  echo "Expect one differing pair per name in ALTERED_TABLES (${ALTERED_TABLES}); nothing else."
else
  echo "ALTERED_TABLES is empty for HEAD's migration — expect NO output above. Any output is a real failure."
fi
#    Every table name appearing in /tmp/constraint-diff-${ISSUE}.txt must
#    be a name from $ALTERED_TABLES — a different table, or any output at
#    all when $ALTERED_TABLES is empty, is a real failure. diff(1) itself
#    exits 1 whenever it finds a difference, which is expected and correct
#    whenever $ALTERED_TABLES is non-empty — see the note on exit status
#    right after this block, and don't read a non-zero exit from THIS diff
#    as the drill having failed on that basis alone.
rm -f /tmp/constraint-diff-${ISSUE}.txt
#
#    Row counts, sequence last_values, and index counts get the same
#    table-exclusion treatment but with NO exception — any line of diff
#    output from any of the three below is a real failure, full stop.
#    The table list is every table this schema has (interrogated live, not
#    typed by hand — #0256), excluding $NEW_TABLES:
ROWCOUNT_SQL="$(psql "$DSN" -tAc "select 'select ''' || table_name || ''', count(*) from ' || table_name from information_schema.tables where table_schema='public' and table_type='BASE TABLE' and table_name not in (${EXCLUDE_SQL}) and table_name <> 'schema_migrations' order by 1" | paste -sd' ' - | sed 's/ select/ union all select/g; s/^ union all //')"
diff <(psql "$DSN" -tAc "$ROWCOUNT_SQL order by 1") \
     <(psql "$DST_DSN" -tAc "$ROWCOUNT_SQL order by 1")
#    Expect zero output. $NEW_TABLES is named nowhere in this query, not
#    merely filtered out of it — it doesn't exist on the source at all.

#    $NEW_TABLES' own sequence(s) only exist post-migration, so query them
#    from $DST_DSN (the restored+migrated side) via pg_get_serial_sequence
#    rather than assuming a fixed "<table>_id_seq" spelling — every table
#    in this schema uses a BIGSERIAL id, so this is exact rather than
#    guessed, and survives a table whose PK sequence is ever named
#    something else:
NEW_SEQS="$(for t in $NEW_TABLES; do psql "$DST_DSN" -tAc "select pg_get_serial_sequence('${t}','id')"; done)"
EXCLUDE_SEQ_SQL="$(printf '%s\n' "$NEW_SEQS" | sed -E "s/^public\.//; s/.*/'&',/" | tr -d '\n')"
EXCLUDE_SEQ_SQL="${EXCLUDE_SEQ_SQL%,}"
[ -n "$EXCLUDE_SEQ_SQL" ] || EXCLUDE_SEQ_SQL="'__none__'"
diff <(psql "$DSN" -tAc "select sequencename, last_value from pg_sequences where schemaname='public' and sequencename not in (${EXCLUDE_SEQ_SQL}) order by 1") \
     <(psql "$DST_DSN" -tAc "select sequencename, last_value from pg_sequences where schemaname='public' and sequencename not in (${EXCLUDE_SEQ_SQL}) order by 1")
#    Expect zero output. Never compare with max(id) instead — see
#    "Sequences" below for why that check would pass on a broken restore.

diff <(psql "$DSN" -tAc "select tablename, count(*) from pg_indexes where schemaname='public' and tablename not in (${EXCLUDE_SQL}) group by tablename order by 1") \
     <(psql "$DST_DSN" -tAc "select tablename, count(*) from pg_indexes where schemaname='public' and tablename not in (${EXCLUDE_SQL}) group by tablename order by 1")
#    Expect zero output.

# 8. Prove the restore — including the migration that just landed on it —
#    is actually usable, not just structurally present: insert a new row
#    into a pre-existing table and confirm it does not collide with
#    restored ids, confirm FK/CHECK/UNIQUE constraints still reject bad
#    data, AND insert a row into each table in $NEW_TABLES — the table(s)
#    HEAD's migration just created — to prove the app role can actually use
#    what the pending migration built, not merely that `migrate up` exited
#    0.

# 8b. Prove ownership the way that actually catches the defect (#0228): do
#    NOT just inspect pg_tables.tableowner as the role that ran the restore —
#    that check passes even when broken, because the restoring role can
#    always read what it just restored. Connect AS THE APPLICATION ROLE and
#    issue a real query, once against a pre-existing table and once against
#    each table in $NEW_TABLES:
psql "$DST_DSN" -c "select count(*) from subscribers;"
for t in $NEW_TABLES; do psql "$DST_DSN" -c "select count(*) from ${t};"; done
#    permission denied on any of these means the ownership step did not run
#    or did not reach that object/schema — a passing catalog-only check
#    would not have caught that. The $NEW_TABLES query specifically is what
#    would have caught `#0235`'s gap: that table is owned by whichever
#    role's `ALTER SCHEMA public OWNER TO …` ran (or didn't) during step 6's
#    `migrate up`, not by anything `restore.sh` touched directly.

# 9. Clean up everything THIS RUN created — never a bare DROP DATABASE on a
#    fixed name, since a concurrent run's databases sit right next to
#    yours under the same template-derived naming scheme (#0247):
scripts/testdb.sh drop "${ISSUE}src"
scripts/testdb.sh drop "${ISSUE}dst"
rm -rf "/tmp/backup-drill-${ISSUE}"
#    `testdb.sh drop` (#0228) reports a real failure and exits non-zero
#    instead of silently claiming "does not exist" — see "A real script bug"
#    below. **#0249**: a database restore.sh created with RESTORE_CREATE=1
#    used to stay owned, at the DATABASE level, by whoever ran createdb —
#    RESTORE_OWNER only reassigned the TABLES/SEQUENCES/VIEWS inside it, not
#    the database object itself — so under BACKUP_RUN_AS="" the target ended
#    up owned by the local OS user while `testdb.sh drop` always connects as
#    `opencircuit`, and every drill run leaked one database. `restore.sh` now
#    creates the database with `createdb -O "$RESTORE_OWNER"` when
#    RESTORE_OWNER is set (both documented paths — `BACKUP_RUN_AS=postgres`
#    in production, a local Postgres superuser OS role in dev — can set
#    ownership to another role), so by the time this step runs the target is
#    already owned by `opencircuit` and this drop succeeds like the one
#    above it. `RESTORE_OWNER=""` still opts out of database ownership too,
#    same as it always opted out of the object-level reassignment — that
#    combination is not exercised by this drill (RESTORE_OWNER is left at
#    its default here) and still leaks under it, deliberately: the point of
#    the empty value is "touch nothing," and a database this step cannot
#    drop is one of the things it is knowingly declining to touch. The
#    `rm -rf` above is safe precisely because BACKUP_ROOT was namespaced by
#    ISSUE in step 4 — it can never reach another run's backup directory.
```

**This block deliberately carries no `set -e`, and exit status is not its
pass signal — read the output (#0248).** One step in it can legitimately
return non-zero on a run that is working correctly, and `set -e` would abort
the drill there:

- **Step 7's constraint-count `diff`** is *expected* to print output —
  one differing line per table named in `$ALTERED_TABLES` — whenever HEAD's
  migration modifies a pre-existing table's constraints. It is expected to
  print **nothing** whenever `$ALTERED_TABLES` is empty, which is the case
  for HEAD's migration right now (it only creates a new table). Either way,
  `diff(1)` exits `1` whenever it finds a difference — that is correct
  exactly when `$ALTERED_TABLES` is non-empty, not a drill failure by
  itself; read the table names in the diff's output against `$ALTERED_TABLES`
  to know which.

  This applies to the block **as committed**: step 8 above is comment-only
  (its three deliberate rejections — bad FK, bad `subscribers.status`,
  duplicate `subscribers.email` — are prose, not code, in this file), so
  they cannot trip `set -e` today. A follower who writes those `psql`
  invocations in and then adds `set -e` would be bitten by additional
  non-zero exits — each rejection is a `psql` call failing by design, same
  as the constraint diff can be. Don't add `set -e` even after filling
  step 8 in.

**Step 9's `scripts/testdb.sh drop "${ISSUE}dst"` used to be a second
legitimate non-zero exit here, and no longer is (#0249).** It failed with
`ERROR: must be owner of database` on every local run under
`BACKUP_RUN_AS=""` — confirmed identically across #0248's two runs and
#0239's review before it — because `RESTORE_CREATE=1`'s `createdb` in
`restore.sh` created the target database owned by whichever OS user was
connecting, not by `RESTORE_OWNER`, while `testdb.sh drop` always connects as
`opencircuit`. `restore.sh` now creates the database already owned by
`RESTORE_OWNER` (`createdb -O`) when one is set, so this drop now succeeds
like the `${ISSUE}src` one beside it — proved by running the full drill
twice **concurrently** to completion and taking a census
(`select datname, pg_get_userbyid(datdba) from pg_database`) immediately
after: zero rows matching either run's namespace, both times. That change in
turn shortened the `set -e` decision above from two exceptions to one — see
`restore.sh`'s `#0249` comment on the `createdb` call for why `set -e` still
isn't added here (step 7's diff still isn't unconditionally a candidate for
it).

A completed run's real evidence is: step 1a printing `$HEAD_VERSION`,
`$NEW_TABLES`, and `$ALTERED_TABLES`; step 6 actually applying HEAD's
migration (its own output names it, not "no change"); step 7's four
comparisons behaving as the rule above describes (not against a written-down
table of numbers — see "Where to find a fresh run's actual figures" below);
step 8/8b's inserts and rejections behaving as documented; and step 9
dropping both databases with no manual cleanup — a `testdb.sh drop` failure
there is now a real failure, not an expected one (the `RESTORE_OWNER=""`
opt-out is the one documented exception: it still leaves the database,
deliberately, owned by whoever ran `createdb`, since "leave ownership alone"
is what the opt-out means). The `${ISSUE}src` drop and the final `rm -rf`
remain ordinary — a non-zero exit from either is a real failure.

The custom format (`pg_restore`, default) and the plain format
(`BACKUP_FORMAT=plain`, `gunzip | psql`) have both been run through this full
drill (most recently for `#0256`, at `HEAD_VERSION=22`, and again with a
throwaway extra migration stacked on top to prove the derivation itself, not
just this one migration — see `## Verification` on `issues/0256.md`).

### Where to find a fresh run's actual figures

**This runbook used to carry a "What the drill found" section recording one
past run's row counts, sequence values, and table names.** Three separate
passes (`#0239`, `#0248`, and the review that filed `#0256`) each corrected
it after `migrations/` grew, and each correction was invalidated again
within, at most, a few migrations — `#0126`'s `000021`/`000022` invalidated
`#0248`'s fix in about twenty minutes of ordinary, unrelated work. An
append-only migration set makes a written-down observation about "what this
schema currently looks like" wrong by construction, sooner or later, no
matter how carefully it was measured. Removed rather than corrected a fourth
time (`#0256`); step 1a's derivation and step 7's comparisons above are the
part of this drill that *cannot* go stale, because they ask `migrations/`
and the live database what to expect instead of asserting a number.

To see actual figures for a fresh run — row counts, sequence values,
constraint deltas — run the drill and read its own output; step 1a echoes
`$HEAD_VERSION`/`$NEW_TABLES`/`$ALTERED_TABLES` and every step-7 `diff`
prints whatever it finds (nothing, on a pass). The durable facts below don't
drift the way a specific run's numbers do, so they stay here:

- **Extensions** — none. `grep -rn "CREATE EXTENSION" migrations/` returns
  nothing, so there is no extension dependency to worry about on restore.
- **`schema_migrations`** — the source stays at `version=HEAD_VERSION-1,
  dirty=false` throughout, in both formats — nothing ever migrates it past
  step 1b's rollback. The restored copy reads the same `HEAD_VERSION-1`
  immediately after restore (carried over faithfully from the dump,
  confirmed at step 5) and `HEAD_VERSION, dirty=false` after step 6 applies
  the pending migration. `HEAD_VERSION` on the source, or `HEAD_VERSION-1`
  on the restored copy after step 6 has run, would both be failures, not
  passes. `migrate` refuses to run against a `dirty=true` database, so this
  check is not optional.
- **Sequences** — every sequence's `last_value` must match source-to-restored
  exactly (excluding `$NEW_TABLES`' own sequence(s), which exist only
  post-migration on the restored side), checked with `select sequencename,
  last_value from pg_sequences`, never with `max(id)`. That distinction
  matters because sequence advancement in PostgreSQL is never transactional:
  a `nextval()` call is not undone when the transaction that made it rolls
  back, so a sequence can run ahead of the highest row actually committed (a
  failed bulk insert, a retried job). A restore that reset sequences from
  `max(id)` instead of trusting the dump's own sequence state would collide
  with a row that used to exist; `pg_dump`/`pg_restore` preserve `last_value`
  exactly on their own, which step 7's diff confirms every run, not by
  trusting the tool in the abstract. (Illustrative, from an earlier run at a
  since-superseded HEAD, not a claim about the current one: inserting a new
  `users` row into a freshly restored database landed at the next available
  id with zero collision, against the sequence's own carried-over
  `last_value`.)
- **Roles and ownership (corrected — `#0228`, then `#0235`)** — `backup.sh`
  dumps with `pg_dump --no-owner --no-privileges`, so the dump carries no
  role names or grants at all; without further action, ownership on restore
  is whichever role ran `pg_restore`/`psql`. `#0062`'s own phase-3 review
  reproduced the general case: running the restore as a *different* role
  (as production's documented `sudo -u postgres pg_restore` path does) left
  every table owned by that role instead, and `opencircuit` got `permission
  denied` on its first query — the restore reported success while leaving
  the app unable to read its own data. `restore.sh` closed this (`#0228`):
  after the restore completes, it reassigns ownership of every table,
  sequence, and view to `RESTORE_OWNER` (default `opencircuit`, matching
  `scripts/db/create.sql`'s bootstrap role) — and because ownership implies
  full privileges on an object, this also replaces what `--no-privileges`
  stripped, without a separate `GRANT` step. Proven with a real dump/restore
  round trip on a local scratch database: restoring as a role other than
  `opencircuit` (simulating the `postgres` production path) left tables
  owned by that role and the app role locked out (`permission denied for
  table widgets`); restoring with the fix in place left every table and
  sequence owned by `opencircuit`, and connecting **as `opencircuit`** — not
  as the restoring role — both `SELECT`ed and `INSERT`ed successfully.

  **`#0228` fixed the objects but not the schema they live in, and `#0235`
  closed that gap.** PostgreSQL 15+ defaults the `public` schema's owner to
  `pg_database_owner`, a pseudo-role that resolves to whoever owns the
  *database* — the restoring superuser on the documented production path,
  not `opencircuit` — and `#0228`'s fix never touched it. `SELECT`/`INSERT`
  on the already-restored tables kept working (each table's *own* ownership
  was correct), so this passed every check the drill ran at the time; it
  only surfaces as `permission denied for schema public` on `CREATE TABLE`,
  i.e. at the *next deploy's* `migrate up` — after a restore that looked
  completely successful. `restore.sh` now also reassigns every non-system
  **schema** (`ALTER SCHEMA public OWNER TO …`) to `RESTORE_OWNER`, in the
  same pass and under the same `RESTORE_OWNER=""` opt-out as the objects
  inside it — plus materialized views, standalone types (enums, domains,
  composite types), and functions/procedures, none of which exist in
  `migrations/` today but are covered so this class of gap does not
  reappear the day one is added. (Aggregates and the implicit row types of
  tables/views are the two things still not covered — see the comment above
  `reassign_ownership` in `restore.sh` for exactly why.) Proven end to end
  on a private scratch database (never the shared `opencircuit_test_*` pool,
  §8b), dropped afterward: seeded a source database with migrations 1–19
  applied (migration 20, `create_workshops`, deliberately withheld so there
  was a real pending migration to apply, not a synthetic probe — an
  illustrative migration number from when this was proven, not a claim
  about current HEAD), dumped it, restored it as a superuser standing in for
  `postgres` (reproducing the production path — this machine has no
  `postgres` OS role, so the local superuser filled that role structurally),
  and ran `migrate up` as `opencircuit` against it. **Before the fix**:
  `permission denied for schema public`, migration left `dirty`,
  reproducing `#0235`'s report exactly. **After the fix**: the same real
  `migrate up` applied cleanly and the new table came out owned by
  `opencircuit`. **`RESTORE_OWNER=""`**: confirmed it opts out of the schema
  reassignment too — `public` stayed owned by `pg_database_owner` and the
  first table stayed owned by the restoring role, exactly as documented.
  Also confirmed `RESTORE_OWNER` is now validated *before* any restore work
  begins — a malformed value (`RESTORE_OWNER="bad; owner"`) exits 2 with no
  database created, rather than costing a full restore first.
- **Order and dependencies** — not a manual concern here: `pg_dump`/
  `pg_restore` topologically order the dump themselves (tables, then data
  via `COPY`, then constraints/indexes/sequences afterward), so FK order
  across `interests` → `subscribers` → `subscriber_interests` →
  `email_campaigns` → `email_sends`, etc. is handled correctly with no
  manual intervention in either format.
- **A real script bug, found and fixed by this drill**: `backup.sh` and
  `restore.sh` both defaulted `BACKUP_RUN_AS` with `${BACKUP_RUN_AS:-postgres}`,
  which in bash treats an *explicitly empty* value the same as *unset* — so
  the header comment's own documented local-dev path
  (`BACKUP_RUN_AS=""` to skip `sudo -u postgres`) silently fell back to
  `sudo -u postgres` anyway and failed with `sudo: unknown user postgres`
  on a machine with no `postgres` OS user (any Homebrew Postgres install).
  Fixed in both scripts to `${BACKUP_RUN_AS-postgres}` (unset-only), and
  re-verified: `BACKUP_RUN_AS=""` now actually skips `sudo` end to end,
  confirmed by a full backup → restore → row-check round trip after the
  fix. `shellcheck` and `bash -n` pass on both scripts.
- **A second real script bug, found by `#0062`'s review and fixed by
  `#0228`**: `scripts/testdb.sh drop` chained `db_exists && DROP DATABASE &&
  echo dropped || echo "does not exist"`, so a `DROP DATABASE` that failed
  for *any* reason (not just non-existence — e.g. `ERROR: must be owner of
  database`, exactly what happens when dropping a database `restore.sh`
  created as a different role) fell into the same `||` branch as "never
  existed," printed the misleading message, and **exited 0**, leaving the
  stray database behind. Reproduced against the pre-fix script and confirmed
  fixed: existence and drop-success are now checked separately, and a real
  drop failure prints the psql error, an explanatory message, and exits 1.
- **Two behaviours worth knowing before 3 a.m., not obvious from either
  script's happy path:**
  - A **custom-format** (`.dump`) re-restore over an already-populated target
    is genuinely idempotent: `pg_restore --clean --if-exists` drops and
    recreates each object, so running `restore.sh` twice in a row against the
    same target converges to the dump's contents rather than erroring or
    duplicating rows. Confirmed directly: a row inserted after the first
    restore was gone after the second, and the row count returned to the
    dump's own count.
  - A **plain-format** (`.sql.gz`) restore into a **non-empty** target is
    *not* idempotent — it aborts at the first `CREATE TABLE` under
    `ON_ERROR_STOP=1` (`relation "…" already exists`) and exits `3` via
    `pipefail`, leaving the target's existing contents untouched. It fails
    safe rather than partially applying, but the two formats are not
    interchangeable here — always restore into a fresh target
    (`RESTORE_CREATE=1`), which both the script header and this runbook's
    step 5 already say to do.

### Before restoring over anything live

`restore.sh` **never drops a database itself** — that is deliberate (see its
own header). Restoring into an existing target restores object-by-object
with `pg_restore --clean --if-exists` (or a plain `psql` replay), which is
destructive at the object level the moment it starts. Before pointing it at
anything that is not a fresh scratch database:

1. Confirm the target database name out loud, from the actual command you
   are about to run — not from memory. A typo here overwrites the wrong
   database.
2. Prefer `RESTORE_CREATE=1` into a **new**, never-before-used name so a
   mistake is a stray database, not a destroyed one.
3. If you must restore over a live database, take a fresh `backup.sh` dump
   of *that* database's current state first, so the "before" is itself
   recoverable.
4. `psql -d <target> -c '\dt'` and a row-count spot check are the minimum
   post-restore sanity check — don't declare success on `restore.sh`'s exit
   code alone.

### What this drill does not cover — stated plainly, not left implied

- **No S3.** `#0229` recorded this as a deliberate deferral, not an
  oversight — nothing here talks to AWS. There is no bucket, no lifecycle
  policy, no bucket-level encryption or public-access block, and no IAM
  policy scoped to a bucket prefix~~, because none of those AWS resources
  exist yet (`CLAUDE.md` §10 item 2)~~. **Correction (`#0430`, 2026-09-05):**
  the AWS account now exists (`378152330719`) and SES is live in it, so that
  precondition has lapsed — these resources simply have not been built, and
  no decision to build them is recorded. `PRD.md` §10.6 still records an S3
  upload leg as addable on top of `backup.sh`, not as work that was skipped
  by mistake.
- **No point-in-time recovery.** This is logical (`pg_dump`) backup only —
  a restore recovers to the moment of the last completed dump, not to any
  point in between. No WAL archiving, no continuous archiving, no PITR tool
  (e.g. `pgBackRest`, `wal-g`) is configured or evaluated here.
- **The offsite pull (`pull-backups.sh`) has never been run against a real
  target, in this drill or since.** There is no second machine anywhere in
  this environment to run it against — no Mac mini, no SSH host reachable
  from here. Its logic (`rsync -avz --delete-after --partial`, pull rather
  than push) was read and is straightforward, but "read and looks right" is
  exactly the standard this issue exists to reject for the dump/restore path,
  so the offsite leg is **explicitly unverified**, not merely undocumented.
  **What would verify it:** run `scripts/db/pull-backups.sh` on a real second
  machine (the Mac mini named in its own header comment) against a real
  `backup.sh`-populated `BACKUP_ROOT` reachable over SSH, confirm the local
  mirror matches the source tree byte-for-byte (`rsync -avzn --delete-after`
  a second time should report zero changes), and confirm a file deleted
  server-side (past retention) is pruned locally too on the next pull.
- **Volume and timing at production scale are unmeasured.** This drill's
  database has a handful of rows per table. `CLAUDE.md` §5 is explicit that
  there is no performance requirement on this project, so this is not a
  gap that needs closing — just don't assume the drill's sub-second timings
  say anything about a production-sized dump/restore.

### Schedule and failure alert (`#0229`) — built, not yet exercised on a real box

**Required install step, not optional tuning (`#0236`): set `BACKUP_DATABASES`
before starting the timer.** `deploy/systemd/opencircuit-backup.service` ships
`Environment=BACKUP_DATABASES=opencircuit`, matching this project's own
database — confirm that line is present and uncommented before enabling
`opencircuit-backup.timer`. `scripts/db/backup.sh` no longer has any default
database name to fall back on: with `BACKUP_DATABASES` unset (and no readable
`.env` at `WorkingDirectory`) it now exits 2 and names exactly what's missing,
rather than the pre-`#0236` behavior of silently defaulting to the literal
`shortlinks` — a different project's database, inherited when this script was
ported from ShortLinks by `#0001`. That failure mode was worse than an error:
depending on whether `shortlinks` happened to exist on the box, it either
backed up the wrong project every night or failed against a database that
isn't there, and either way Open Circuit's own data was never backed up until
someone noticed.

**Required install step for the offsite leg, same shape (`#0245`): set
`BACKUP_SSH_HOST` before running `scripts/db/pull-backups.sh` on the Mac
mini.** That script is not wired into systemd at all — it runs on a separate
machine — so it needs its own reminder, not just `backup.sh`'s. It used to
default `BACKUP_SSH_HOST` to `ec2-user@go.sstools.co`, a real ShortLinks
production hostname inherited from the same `#0001` port; run unmodified with
the variable unset, the offsite pull opened an SSH connection to another
project's server rather than this one's — worse than the `BACKUP_DATABASES`
defect above, because the failure mode there was a wrong local database name
while this one reaches out over the network to infrastructure this project
does not own. There is no correct host to substitute yet (this project's own
EC2 host is not provisioned — `CLAUDE.md` §10 item 6), so the script now
exits 2 before any network call, naming exactly what's missing:

```bash
BACKUP_SSH_HOST=ec2-user@<this-project's-host> bash scripts/db/pull-backups.sh
```

`backup.sh` exiting non-zero on failure was verified working by `#0062`, but
until `#0229` nothing consumed that exit code. Two systemd units now do:
`deploy/systemd/opencircuit-backup.timer` fires `opencircuit-backup.service`
(`backup.sh`, run as `postgres`) nightly at 07:30 UTC, and that service's
`OnFailure=opencircuit-backup-alert.service` runs
`scripts/db/backup-alert.sh` on any failure — which always logs a
high-priority journal entry (`journalctl -p err`) and optionally POSTs a
webhook if `BACKUP_ALERT_WEBHOOK_URL` is configured. See
`deploy/systemd/README.md`'s "Backup timer and failure alert" section for
install and test commands.

**What this is, and is not, verified against:** the three unit files pass a
structural well-formedness check (matching `[Section]` headers, every
non-comment line is `key=value`) and `scripts/db/backup-alert.sh` passes
`shellcheck`/`bash -n` and was run directly — both with and without
`BACKUP_ALERT_WEBHOOK_URL` set, confirming it always logs via `logger`, exits
`0` even when a webhook POST fails, and never masks the underlying failure.
**None of that is the same as systemd actually running these units.** There
is no systemd on this development machine (macOS) and no `systemd-analyze
verify` was available to run against the unit files themselves. Nothing here
proves the timer actually fires on schedule, that `OnFailure=` actually
triggers the alert unit the way the unit graph implies, or that `journalctl
-p err` or a real webhook endpoint is where anyone is actually looking.

### Must be re-verified on the server, not assumed from this drill

- **`BACKUP_RUN_AS=postgres` (the default, real `sudo -u postgres` path).**
  This drill ran with `BACKUP_RUN_AS=""` because the local Homebrew cluster
  has no `postgres` OS user and this workstation user already owns the
  cluster. The production path — `sudo -u postgres pg_dump`/`pg_restore` via
  peer authentication — was read, not executed, and needs its own drill on
  the actual EC2 instance once it exists. The same is true of `RESTORE_OWNER`
  reassignment (`#0228`): it was proven locally against a non-superuser role
  standing in for `postgres`, not against a real `sudo -u postgres` restore.
- **Actual disk paths and permissions on the box** — `BACKUP_ROOT` defaults
  to `/var/backups/postgres`; confirm it exists, is owned/writable as
  `backup.sh` expects, and has room for `BACKUP_RETENTION_DAYS` of dumps at
  real data volume. **Corrected (`#0435`, 2026-09-08):** the
  `/opt/opencircuit` `WorkingDirectory=`/`ExecStart=` path is no longer a
  placeholder — `CLAUDE.md` §10 item 6 has recorded it as the real, confirmed
  checkout location since 2026-08-25, and `#0435` re-verified directly that
  the repo lives there. The real blocker on disk paths turned out to be
  ownership, not path correctness — see below.
- **The timer and the alert unit, installed on a real systemd host** —
  confirm `opencircuit-backup.timer` actually fires nightly, that
  `BACKUP_DATABASES` is actually set on that box (`#0236` — it is a required
  unit setting, not a fallback to rely on: `backup.sh` refuses to guess a
  database name), and that a deliberately broken run (e.g. a bad
  `BACKUP_ROOT`) produces a real, seen alert — not just a journal line and a
  non-zero exit code nobody is watching. If `BACKUP_ALERT_WEBHOOK_URL` gets
  configured, confirm the webhook actually delivers to wherever a human
  looks.
- **`pull-backups.sh` end to end**, against the real Mac mini and a real SSH
  key — see "What this drill does not cover" above for exactly what that
  verification looks like.

### Re-derived on the real box (`#0435`, 2026-09-08) — still not installed

**`opencircuit` has never been backed up.** Read-only, on the box: no unit
matching `opencircuit-backup*` exists under `/etc/systemd/system/` (only
`opencircuit.service` is there), `systemctl is-enabled
opencircuit-backup.timer` reports the unit file does not exist, and
`systemctl list-timers` lists none of this project's units among its eight
entries. The recorded blocker — `/var/backups/postgres` is `root:root 0700`,
and the unit's `User=postgres` cannot even traverse it — was re-confirmed
directly (`stat`) rather than trusted from the prior filing, and still holds.

**A second blocker surfaced only by re-deriving rather than trusting the
last-known state:** `/opt/opencircuit`'s checkout is 711 commits behind this
repo's `main` and predates `#0434` entirely, so `scripts/db/backup-media.sh`
and `scripts/db/restore-media.sh` are simply absent there, and the box's copy
of `opencircuit-backup.service` is the older, single-`ExecStart=` version.
Installing today's committed unit file unmodified would run the database leg
successfully and then fail the media leg with a "no such file" error, marking
the whole run failed and paging every night regardless of whether the
database dump worked. `sha256sum` confirmed every *other* file this timer
needs — `backup.sh`, `restore.sh`, `pull-backups.sh`, `backup-alert.sh`, the
`.timer`, and the alert `.service` — is already byte-identical on the box to
this repo's current commit, so only three files need bringing current.

**Both blockers, the exact commands, and a restore drill proving the dump
before trusting it, are now written up as an approval-ready sequence in
`deploy/systemd/README.md`'s "Backup timer and failure alert" section** — see
its "Fix the `/var/backups/postgres` permission blocker", "Bring the on-box
checkout current for the media leg", and "Verify the dump is real, and prove
a restore" subsections. Nothing in this pass was applied to the box —
everything above was read-only, and installation still needs the user's
explicit approval (`CLAUDE.md` §5b, §9) before any of those commands run.

`BACKUP_ALERT_WEBHOOK_URL` remains deliberately unconfigured: no
Slack/Discord/Mattermost/healthchecks.io channel exists anywhere in this
project (`CLAUDE.md` §10 items 2 and 6), so `#0435` recommends leaving it
unset rather than inventing a destination with nothing behind it.
**Corrected (`#0468`, 2026-09-08): `journalctl -p err` and `systemctl
--failed` are not an interim alert — they are pull commands, and nothing on
the box is scheduled to run either one** (its 8 timers are all OS-owned;
there is no root/postgres/ec2-user crontab). So today there is no interim
failure signal beyond a human choosing to check on their own initiative; see
`deploy/systemd/README.md`'s "Backup timer and failure alert" section for the
full honest statement, including why a real destination
(`contact@opencircuitsf.com`, `#0271`) is not currently reachable from this
box (no mail transfer agent installed, and this project's own mail path is
SES, still blocked by `#0415`).
The offsite pull (`pull-backups.sh`) is treated as **out of scope** for this
issue: the user has mentioned a machine named "joe" as the eventual puller,
but its hostname and reachability for an unattended `rsync` are not recorded
anywhere in this repo, and `#0435` does not guess a default for it.

---

## Redeploy procedure

**Before either path below: check for a dirty tracked file first (`#0467`).**
Both the recommended path and the manual steps open with `git pull`, and a
pull refuses outright — loudly, not silently — if the checkout carries an
uncommitted change to a file the incoming commits also touch. `web/dist/index.html`
is the recurring case: `./scripts/dev.sh --built` and any prior `npm run build`
run directly on the box leave it modified (`CLAUDE.md` §8b), and that file is
touched often enough on `main` that a stale checkout's next pull is likely to
collide with it. Check before pulling:

```bash
git status --porcelain
```

Any output there is very likely leftover build artifact, not an intended edit
— restore it to the checkout's own committed version before pulling:

```bash
git show HEAD:web/dist/index.html > web/dist/index.html
git status --porcelain   # confirm clean before the pull below
```

**Never `git checkout -- web/dist/index.html`** to do this — `CLAUDE.md` §8a
forbids that class of command against a path you did not personally edit this
session, and on a shared box you generally cannot tell whether you did. The
`git show HEAD:… >` form above reaches the same end state without it. This
check costs nothing when the file turns out to be clean, so it is worth
running unconditionally rather than only after a pull already fails.

A tracked file the checkout has modified shows as `M`, and the `git show`
form above restores it. A file `scp`'d in that this checkout does not
track yet shows as `??` — the `git show HEAD:… >` form cannot restore it
(`fatal: path '…' exists on disk, but not in 'HEAD'`). Remove it instead
and let the pull deliver the committed copy:

```bash
rm <the ?? paths git status just named>
```

**Which of `git pull` or a targeted `scp` brings a file current is decided by
one question, not by habit (`#0467`): does the change need a rebuilt Go
binary or a rebuilt SPA?** If yes, this section's `git pull` is already the
right and necessary first step — a real redeploy pays for the rebuild anyway,
so paying once to also bring the checkout current at the same time is free.
If no — a single config file, unit file, or standalone script that the
running binary does not embed — a `git pull` here would force that same
rebuild for no reason, on a box with 418 MB of RAM (`CLAUDE.md` §7), while
also landing every other unreleased commit onto the production checkout at
once. `#0447`, `#0465`, and `#0435`'s prepared instructions (`#0466`) each
answered "no" and chose a targeted `scp` of the one file each needed,
verified by hash; nothing here changes that.

Note the two branches interact: a `scp` into `/opt/opencircuit` leaves
that file `M` or `??` in the production checkout, and the next redeploy's
`git pull` refuses until it is cleared — under `--rebase`, on *any*
unstaged change, whether or not the incoming commits touch it. That is
what the whole-tree check above is for; it is not only about
`web/dist/index.html`. This section's `git pull`
remains the mechanism for the case those three are not: an actual redeploy
of the running service.

### Recommended: `scripts/deploy.sh`

From the repo checkout on the host, on the latest commit:

```bash
git pull --rebase origin main
./scripts/deploy.sh
```

`scripts/deploy.sh` runs the redeploy with a verification gate at every step
and **refuses to restart the service unless a genuinely fresh binary is
ready**, in order: rejects any unresolved `[PLACEHOLDER: ...]` marker in
`web/src/` (facts only the user can supply, `#0075`); rebuilds the SPA and
confirms it produced hashed assets, not the committed placeholder; builds the
Go binary and confirms with `grep -a` that it actually **embeds the bundle
it just built** (catching a stale-`web/dist` build); asks for a `[y/N]`
confirmation; installs to the path resolved from the live systemd unit's
`ExecStart` and restarts it; confirms the service is `active` after restart;
then curls the **live public URL** and fails unless it is serving that exact
bundle. If any gate fails, it stops with an error instead of shipping a
broken deploy. Override defaults with `SERVICE=… PUBLIC_URL=… BIN=…
./scripts/deploy.sh`.

**`scripts/deploy.sh` does not run `migrate` at all** — it only builds the
SPA, builds and installs the binary, and restarts the service. If the commit
being deployed added new migration files, running the script alone is not a
complete deploy: run `migrate ... up` (**Migrations**, above) **before**
running `./scripts/deploy.sh`, so the schema is already in the state the
freshly built binary's queries expect by the time the service restarts onto
it.

```bash
git diff --name-only <last-deployed-sha>..HEAD -- migrations/   # any output => migrate first
```

### Manual steps (what the script automates)

```bash
cd /opt/opencircuit
git pull

# 1. Rebuild the SPA and the binary
cd web && npm ci && npm run build
cd ..
go build -o opencircuit ./cmd/opencircuit
sudo install -m 0755 opencircuit /usr/local/bin/opencircuit

# 2. Apply any new migrations
export DATABASE_URL='postgres://opencircuit:<password>@localhost:5432/opencircuit?sslmode=disable'
migrate -path migrations -database "$DATABASE_URL" up

# 3. Restart the service
sudo systemctl restart opencircuit
sudo systemctl status opencircuit
curl -fsS https://www.opencircuitsf.com/health
```

If a deploy only changes `/etc/opencircuit/config.env`, `sudo systemctl
restart opencircuit` alone is enough — no rebuild needed. If the systemd
unit file itself changed, re-copy it and `sudo systemctl daemon-reload`
first.

---

## Troubleshooting: a deploy ran but the site doesn't show the changes

The SPA is **compiled into the Go binary** (`web/embed.go`, `//go:embed
all:dist`) and served by that binary behind Apache. So "my changes don't
appear" almost always means one link in this chain is stale:

> latest commit → `npm run build` writes `web/dist/` → `go build` embeds it
> → binary installed to the `ExecStart` path → service restarted → Apache
> proxies it → browser.

**Fastest single check — compare the served bundle to the built bundle:**

```bash
curl -s https://www.opencircuitsf.com/ | grep -oE '/assets/index-[^"]+'
grep -oE 'index-[A-Za-z0-9_-]+\.(js|css)' web/dist/index.html
```

If those hashes differ, the new build isn't being served. Causes, most
common first:

1. **The SPA wasn't rebuilt before the binary.** `go build` without first
   running `npm run build` embeds the old (or placeholder) bundle. Confirm
   what the binary actually contains — use `grep -a` (whole-file scan), not
   `strings`, which can false-negative on some platforms:

   ```bash
   grep -ao 'index-[A-Za-z0-9_-]*\.js' /usr/local/bin/opencircuit | sort -u
   ```

2. **The service wasn't restarted onto the new binary.** A new file on disk
   does nothing until the process restarts — use `restart`, not `start`:

   ```bash
   systemctl show -p ExecMainStartTimestamp opencircuit   # should read "just now"
   ```

3. **The binary was built to a different path than systemd runs.**
   `go build -o opencircuit` writes to the current directory; systemd runs
   whatever `ExecStart` points at (`/usr/local/bin/opencircuit`). Always
   `sudo install` to the `ExecStart` path.
4. **The build host isn't on the latest commit.** `git rev-parse --short
   HEAD` on the box must match what you intend to ship.
5. **`go build` compiled a different `web/dist` than you rebuilt.** Check for
   a workspace/vendor redirect or a symlinked dist:

   ```bash
   go env GOWORK
   ls -ld vendor 2>/dev/null
   readlink -f web/dist
   ```

6. **Apache is serving a static copy instead of proxying to the binary.**
   Rebuilding the binary changes nothing if the vhost has a
   `DocumentRoot`/`Alias` pointing at a static directory instead of
   `ProxyPass` to `127.0.0.1:8080` — check the deployed config matches
   `deploy/apache/opencircuitsf.com.conf`:

   ```bash
   grep -rE 'DocumentRoot|Alias|ProxyPass' /etc/httpd/conf.d/
   ```

7. **Caching (browser / CDN / proxy).** Hashed `assets/*` filenames bust
   themselves, but `index.html` can be cached. Test with `curl` and a hard
   refresh.

`scripts/deploy.sh` checks #1, #2, #3, and #7 automatically and prints a
diagnosis for #5 — prefer it over the manual steps.

---

## Loopback trust model and the CDN failure mode (`#0077`)

`internal/middleware.ClientIP` (`internal/middleware/clientip.go`) trusts
`X-Forwarded-For` only when the immediate TCP peer (`r.RemoteAddr`) is
loopback, then takes the **rightmost** entry — the single hop
`mod_proxy_http` appends. The Go process itself only ever binds
`127.0.0.1:<port>` (`cmd/opencircuit/main.go`'s `addr :=
fmt.Sprintf("127.0.0.1:%d", cfg.Port)`, currently line 1167 — cite the
symbol, not the line number, since it shifts as the file grows), and there
is no config knob to change either the bind address or the trusted-peer
check — the anchor is enforced in code, not configuration.

**Operational consequence: nothing else on this host may proxy or tunnel
into `127.0.0.1:8080`.** Another local process, a host-network container, or
an SSH tunnel forwarded to that port would be trusted exactly as if it were
Apache — able to forge `X-Forwarded-For` and have it believed for rate
limiting and `signup_ip` attribution. This is a real constraint on anything
else ever run on the box (a monitoring agent, a debugging tunnel, a second
reverse proxy for some other purpose) — audit what else binds or forwards to
that port before adding anything, not after.

**If a CDN or a second proxy is ever put in front of Apache**, the rightmost
`X-Forwarded-For` entry becomes *that* proxy's egress IP, not the real
client's: every user collapses into one rate-limit bucket and `signup_ip`
records the CDN's IP for every signup — **silently, with no error anywhere**.
`middleware.ClientIP` would need to be revisited (trusting the CDN's IP range
and reading one entry further left) before any such change ships; this
project currently has no CDN and no plan to add one, but the failure mode is
worth knowing before someone reaches for one to solve an unrelated problem.

---

## Bootstrap admin (`opencircuit seed`)

See **Seed** (step 6) and **First admin login** (step 10) above for the full
procedure — kept here as a single cross-reference since earlier issues in
this tracker (`#0010`) point at this heading directly.

---

## Open items blocking a real deploy (`CLAUDE.md` §10)

Tracked so a phase doesn't stall silently on one of these — none are code:

Items 2, 4, 5, 6 and 7 below were an unupdated mirror of `CLAUDE.md` §10's
table; their status lines are corrected in place here, 2026-09-03 (`#0414`),
to match it. `CLAUDE.md` §10 remains authoritative if the two ever disagree
again.

1. Rename the GitHub repo `Website` → `OpenCircuitSF`. **Status: not done.**
2. SES: verify domain in `us-east-1` (corrected 2026-09-03, `#0418` — this item
   said `us-west-2`), Easy DKIM, custom MAIL FROM, DMARC at `p=none`, request
   production access — blocks real sends from Phase 3. **Status: all but done
   2026-08-25** — domain verified, DKIM and MAIL FROM `SUCCESS`, DMARC live at
   `p=none` on the subdomain, instance role attached and proven by a real
   delivered send. Production access is the only piece still open (account
   still sandboxed).
3. Physical mailing address (PO box) — `#0045` refuses to start a campaign
   without it. **Status: not started.**
4. ~~Sending identity (`hello@` vs. `workshops@`) and who reads the reply-to
   inbox — undecided, `PRD.md` §14 Q2 defaults to `hello@`.~~ **Correction
   (#0414, 2026-09-03).** Both halves settled 2026-08-25: mail sends as
   `contact@mailing.opencircuitsf.com` with `Reply-To:
   contact@opencircuitsf.com`, a real Google Workspace mailbox that already
   existed and already reads it.
5. Whether the domain needs human mailboxes — determines the apex MX,
   undecided, `PRD.md` §14 Q3. **Status: answered by observation, 2026-08-25**
   — yes, and it already has them (Google Workspace, predating this project).
   The apex MX must never be touched.
6. Server-side details: instance ID/size/region, SSH access,
   `DocumentRoot`, vhost file, certbot renewal schedule, whether the
   existing Postgres is the target — undocumented, capture as encountered.
   This is the item most of this document's `[PLACEHOLDER: ...]` markers
   trace back to. **Status: done, 2026-08-25** — captured in the
   production-facts table above. The instance is `t4g.nano` in `us-east-1`
   on PostgreSQL 15.18, each contradicting an earlier assumption.
7. SES account-level suppression list (`aws sesv2
   put-account-suppression-attributes --suppressed-reasons BOUNCE
   COMPLAINT`) — see **SES setup** above, step 7; gated on item 2. **Status:
   already enabled** — `get-account` returns `SuppressedReasons: ["BOUNCE",
   "COMPLAINT"]`; it appears to predate this project. Nothing to do.

---

## What this document is, and is not, verified against

Originally a documentation deliverable verified only by local inspection. The
2026-08-25 deploy replaced part of that with measurement on the real host.
What the deploy actually established, each item measured rather than assumed:

- **Serving path, end to end.** All 22 migrations applied to a fresh
  `opencircuit` database on the box's PostgreSQL **15.18**; `opencircuit
  seed` created the admin; `opencircuit.service` came up under systemd,
  `enabled` at boot, listening on `127.0.0.1:8080` with `/health` returning
  `{"status":"ok","db":"ok"}`.
- **PostgreSQL 15 is proven for this schema.** The Prerequisites above (§1)
  originally assumed 16, per PRD §10.1; they now state 15.18 to match what is
  actually installed. The migrations applied cleanly on 15.18, and the whole
  Go suite passes against 15.14 locally with `TEST_DATABASE_URL` set and zero
  skips.
- **The `script-src` CSP hash is now verified against a real build** — the
  limitation stated in earlier versions of this section is closed. The hash
  was recomputed from `web/dist/index.html` after an actual `npm run build`,
  using Python's `html.parser` rather than a regex (`CLAUDE.md` §8 records
  that regex getting this wrong twice), and then re-verified against the bytes
  Apache actually serves. The live page contains exactly two `<script>`
  elements — one inline, whose hash matches the CSP, and one external module.
- **The Apache vhost syntax-checks and runs on the real AL2023 httpd 2.4.68**,
  not just a local 2.4.67. `mod_ssl`, `mod_proxy`, `mod_proxy_http`,
  `mod_rewrite`, `mod_headers`, and `mod_alias` are all loaded by default
  there, as this document assumed.
- **The SPA builds on the box's Node 18.20.8** despite the "Node.js 20+"
  prerequisite, and produces byte-identical hashed assets to a local build on
  Node 25 (`index-VSjBgT9l.js`, `index-q9ezqpg5.css` on both).
- **`/.well-known/atproto-did` survived the cutover byte-for-byte** —
  same SHA-256, same `ETag`, same `Last-Modified` before and after.
- **certbot needs no change.** The wildcard cert already in place covers
  `www`, and renewal runs off `dns-route53`, which cannot be affected by any
  vhost edit.

Still **not** verified against anything real, unchanged from the original pass:

- ~~The SES, DKIM/MAIL FROM/inbound DNS, and IAM sections. No SES identity
  exists and the instance has no IAM role, so none of it has been run.~~
  **Correction (`#0426`, 2026-09-04):** this is stale. `docs/aws-iam-setup.md`'s
  facts table records a verified SES identity (`mailing.opencircuitsf.com`,
  DKIM and custom MAIL FROM `SUCCESS`), a live configuration set and SNS
  event pipeline, and the IAM role attached (`opencircuit-instance`) — the
  same facts `CLAUDE.md` §10 item 2 and this document's Production-facts
  table (also corrected, `#0426`) now record. What is genuinely still not
  run: real SES **production access** (the account remains sandboxed —
  `docs/aws-iam-setup.md`, "After the role is attached" step 4) and, for
  this pass specifically, a read of the live attached IAM policy's exact
  contents — the box's default CLI identity is denied IAM reads (see the
  `## IAM` section above).
- The backup timer and its alert unit — the files exist, nothing installs them
  on the box yet.

The original local-inspection claims, which still stand for the parts above
that the deploy did not touch:

- Every artifact path cited (`scripts/db/{create,drop}.sql`,
  `.env.example`, `deploy/apache/opencircuitsf.com.conf`,
  `deploy/systemd/{opencircuit,opencircuit-backup*}.service`,
  `deploy/systemd/opencircuit-backup.timer`, `cmd/opencircuit/{main,seed}.go`,
  `scripts/deploy.sh`, `scripts/db-reset.sh`) was confirmed to exist and was
  read, not assumed.
- The Apache vhost, including the new security headers, syntax-checks clean
  (`httpd -t`) against a real local Apache 2.4.67 with `mod_proxy`,
  `mod_proxy_http`, `mod_rewrite`, `mod_ssl`, and `mod_headers` loaded — see
  the Apache step above for exactly what that does and does not prove. **This
  bullet is listed under "parts the deploy did not touch," which is now
  wrong for this one item (`#0431`, 2026-09-04): the "What the deploy
  actually established" list above already records the same vhost syntax-
  checking and *running* on the real AL2023 httpd 2.4.68, and this pass
  re-confirmed it serving the exact CSP header over a live HTTPS request.
  The local-Apache-2.4.67 check below is real but superseded, not the
  current limit on what is known.**
- The `script-src` CSP hash was computed programmatically (not hand-typed,
  `CLAUDE.md` §8) from `web/index.html`'s source. **That limitation is now
  closed** — see the verified list above; the committed hash turned out to be
  correct against a real build and against the live response.
- ~~`deploy/systemd/opencircuit.service`'s hardening directives were confirmed
  present by reading the file directly, not by running it — there is no
  systemd on this development machine.~~ **Corrected `#0431`, 2026-09-04:**
  superseded by the "Serving path, end to end" bullet above and by the ##
  7. systemd correction — the unit has since run under the box's real
  systemd, confirming the hardening directives don't block a real startup.
  Reading the file directly (still true of *this development machine*, which
  remains macOS with no systemd) is no longer the only evidence.
- ~~The DNS, SES, and IAM sections are transcriptions of `PRD.md` §10.2–§10.5
  and `docs/email-setup.md`, cross-checked against `.env.example` and
  `docs/configuration.md` for internal consistency (variable names, default
  values), not validated against a real AWS account, which does not exist.~~
  **Correction (`#0426`, 2026-09-04):** this no longer stands — the AWS
  account (`378152330719`) exists and SES is live in it, per
  `docs/aws-iam-setup.md`'s facts table. The IAM policy shown in the `## IAM`
  section above is still not validated against a live `aws iam
  simulate-principal-policy` call or a `GetRolePolicy` read from this
  project's own tooling — the box's default CLI identity is denied IAM
  reads, checked read-only for this issue (see that section).
- `CLAUDE.md` §10 item 6's unknowns (instance ID, size, region, SSH access,
  `DocumentRoot`, installed vhost, certbot schedule, target Postgres) were
  **all captured on 2026-08-25** and are in the production-facts table at the
  top. The `[PLACEHOLDER: ...]` markers that remain are the ones that depend
  on SES facts nobody has supplied yet — the DKIM CNAME selectors — not on
  access to the box. (The account ID was one of these too; it is no longer a
  placeholder, corrected `#0426` — see the `## IAM` section.)

## Where to look

| Concern | File |
|---|---|
| systemd unit (the service) | `deploy/systemd/opencircuit.service` |
| Backup timer + failure alert (`#0229`) | `deploy/systemd/opencircuit-backup.{service,timer}`, `opencircuit-backup-alert.service`, `scripts/db/backup-alert.sh` |
| Apache vhost (proxy, apex→www redirect, security headers) | `deploy/apache/opencircuitsf.com.conf` |
| Redeploy automation | `scripts/deploy.sh` |
| Local dev-database reset (never production) | `scripts/db-reset.sh` |
| DB backup/restore scripts | `scripts/db/{backup,restore,pull-backups}.sh` |
| DB create/drop | `scripts/db/{create,drop}.sql` |
| Every configuration variable | `docs/configuration.md`, `.env.example` |
| SES / DNS detail organized by subsystem | `docs/email-setup.md` |
