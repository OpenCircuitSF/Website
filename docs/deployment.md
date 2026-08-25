# Deployment & Operations

**Deployment is deliberately deferred** (user, 2026-08-23; `CLAUDE.md` §10).
No EC2 instance has been provisioned for this codebase, no AWS account exists
for SES, and nothing below has been followed end to end on a real host. This
is a **runbook written and syntax-checked against local tooling**, not a
record of a deploy that happened. Every step says which of the two it rests
on, per `CLAUDE.md` §10's honesty requirement, and the acceptance criterion
"the whole runbook followed once on a clean instance and corrected where it
was wrong" (`#0064`) is explicitly **not met** — it cannot be, until a real
instance exists to run it against. `PRD.md` §10 and `CLAUDE.md` §7/§10 remain
authoritative wherever this document is silent or wrong.

## Current production facts (`CLAUDE.md` §7)

| | |
|---|---|
| Canonical host | `https://www.opencircuitsf.com` — apex and plain HTTP both 301 to it |
| Server | Apache 2.4.68, Amazon Linux, OpenSSL 3.5.7 |
| TLS | Let's Encrypt, valid to 2026-11-16 |
| Already on the box | PostgreSQL and Apache |
| Currently served | the static placeholder (`placeholder/`) |

**A box already exists and already runs Apache and PostgreSQL** — the
production facts above are measured, not aspirational. What is missing is
this project's own configuration on it, and, per `CLAUDE.md` §10 item 6, the
specific operational details of that box: instance ID, size, region, SSH
access, `DocumentRoot`, the vhost file actually installed, the certbot
renewal schedule, and whether the Postgres already running there is the
target for this project's `opencircuit` database or a separate instance is
needed. None of that is documented anywhere available to this agent, and
none of it is guessed at here — every place below that would need one of
those facts is marked `[PLACEHOLDER: CLAUDE.md §10 item 6]` rather than
invented. **Capture the real values into this table the first time someone
with access to the box reads this file**, then delete the placeholder
markers.

| Fact | Value |
|---|---|
| Instance ID | `[PLACEHOLDER: CLAUDE.md §10 item 6]` |
| Instance size / type | `[PLACEHOLDER: CLAUDE.md §10 item 6 — PRD §10.1 assumes t4g.small, Amazon Linux 2023, unconfirmed]` |
| Region | `[PLACEHOLDER: CLAUDE.md §10 item 6 — PRD §10.3 specifies us-west-2 for SES; confirm the EC2 instance is in the same region]` |
| SSH access | `[PLACEHOLDER: CLAUDE.md §10 item 6 — key pair / bastion / SSM, whichever applies]` |
| `DocumentRoot` (current, static placeholder) | `[PLACEHOLDER: CLAUDE.md §10 item 6]` |
| Installed vhost file(s) | `[PLACEHOLDER: CLAUDE.md §10 item 6 — confirm against deploy/apache/opencircuitsf.com.conf before assuming they match]` |
| certbot renewal schedule | `[PLACEHOLDER: CLAUDE.md §10 item 6 — certbot installs its own systemd timer/cron by default; confirm which]` |
| Is the existing Postgres the target for `opencircuit`? | `[PLACEHOLDER: CLAUDE.md §10 item 6 — if it already hosts a `shortlinks` database, `opencircuit` is a second database on the same cluster, not a second cluster]` |

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
process, and (if `[PLACEHOLDER: CLAUDE.md §10 item 6]` confirms it) the
PostgreSQL server process — never a database, a service account, a config
file, or a port:

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

- **EC2 instance**, Amazon Linux 2023, sized per `[PLACEHOLDER: CLAUDE.md §10
  item 6]` (PRD §10.1 assumes `t4g.small` — an ARM/Graviton instance; confirm
  the architecture before downloading arch-specific tarballs below, since
  Graviton needs `arm64` builds and an Intel/AMD instance needs `amd64`).
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

- **PostgreSQL 16.** Already on the box per `CLAUDE.md` §7 — confirm the
  major version with `psql --version` before assuming it matches what this
  project's migrations were verified against (`#0062`/`#0228`'s drills both
  ran on PostgreSQL 16.14, Homebrew). If provisioning fresh:

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
  sudo dnf install -y openssl certbot python3-certbot-apache
  ```

- **DNS already resolving** — confirm before requesting a TLS certificate
  (Certbot's HTTP-01 challenge needs the hostname to already resolve to this
  box):

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
> a development convenience, not a production tool, and per `CLAUDE.md` §1
> this project's greenfield exception for rewriting migrations ends at the
> first production deploy.

**Syntax-checked, not run against a production instance:** `psql --version`
confirms local PostgreSQL 16 syntax-accepts `scripts/db/create.sql` and
`scripts/db/drop.sql` unchanged (they are copied from ShortLinks, `#0001`,
and already exercised repeatedly by `scripts/testdb.sh` and
`scripts/db-reset.sh` against local databases) — this step has not been run
as the `postgres` OS user against a fresh AL2023 PostgreSQL 16 install.

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
  `EMAIL_LIST_DOMAIN`, `SES_INBOUND_BUCKET` — see **SES setup** below; these
  require the SES account and domain verification that `CLAUDE.md` §10 item 2
  records as not started. Fill in the ones that don't depend on SES existing
  (`EMAIL_LIST_DOMAIN`, `AWS_REGION`) now; the rest can be corrected later
  without a rebuild (see **Redeploy procedure**).
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

**Not verified against a real box:** this step was exercised locally
(`scripts/db-reset.sh` builds the equivalent environment inline for dev use)
but never as `/etc/opencircuit/config.env` read by a real `EnvironmentFile=`
on a real systemd unit — see the systemd step for what that confirms and
does not.

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

**Syntax/build-checked, not run on AL2023:** `go build ./...` and the web
`npm run check`/`npm test` suite pass locally on macOS/arm64 as of this
writing (see `## Verification` in `#0064`). Cross-compilation to
`linux/arm64` or `linux/amd64` was **not** attempted here — `go build`
without `GOOS`/`GOARCH` overrides targets the host it runs on, so run this
step **on the target instance itself** (per the Prerequisites step's Go
install), not on a developer's Mac, unless a cross-compile + transfer
pipeline is deliberately set up later.

---

## 5. Migrations

Apply the schema with `golang-migrate`, pointing at the migration files and
the database URL from `/etc/opencircuit/config.env`:

```bash
export DATABASE_URL='postgres://opencircuit:<password>@localhost:5432/opencircuit?sslmode=disable'
migrate -path migrations -database "$DATABASE_URL" up
```

This repo currently ships migrations `000001`–`000020`, applied in order.
Run this again on every deploy that adds new migration files — check first:

```bash
git diff --name-only <last-deployed-sha>..HEAD -- migrations/   # any output => a migration is needed
migrate -path migrations -database "$DATABASE_URL" version       # or compare against the DB's applied version
```

Per `CLAUDE.md` §1, this project is greenfield until the **first** production
deploy — after that, migrations become append-only and must never be edited
in place again. The first real run of this step, against a database that
matters, is the event that ends the greenfield exception.

**Verified locally, not on a real box:** `#0062`'s and `#0228`'s restore
drills both ran this exact `migrate ... up` invocation repeatedly against
local scratch databases (`schema_migrations` landing at `version=20,
dirty=false` every time) — see **Backups** below. It has not been run
against a freshly `initdb`'d AL2023 PostgreSQL 16 cluster.

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

**Structurally verified, not run under real systemd:** there is no systemd on
the development machine (macOS) this runbook was written on, and no
`systemd-analyze verify` available to check the unit file directly (the same
gap `#0229` recorded for the backup units). Confirmed instead: every
non-comment line is `key=value` and every section header is `[Section]`
(the same structural check `#0229` used), and the file was diffed line by
line against `deploy/systemd/README.md`'s and this project's own
`opencircuit-backup.service`'s equivalents for consistency. **What this does
not prove:** that `EnvironmentFile=/etc/opencircuit/config.env` resolves
correctly, that the hardening directives don't reject something the process
legitimately needs (e.g. `RestrictAddressFamilies` blocking a DNS lookup path
that needs `AF_NETLINK`, which is not in the allowed list — watch for this on
first real start), or that `Restart=on-failure` / `KillSignal=SIGTERM`
actually behave as documented under a real crash.

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

**Syntax-checked, not run on AL2023 or against a real request:**

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
(`httpd -v`) — the closest available stand-in for AL2023's 2.4.68, not the
real thing. This confirms the file **parses**: every `Header`/`ProxyPass`/
`RewriteRule` directive is spelled correctly and every module they need
(`mod_headers`, `mod_proxy`, `mod_proxy_http`, `mod_rewrite`, `mod_ssl`) is
one Apache actually ships. It does **not** confirm the headers are correct
under a real HTTPS request, that the CSP doesn't break some interaction not
exercised by the SPA's automated tests, or that `flushpackets=on` behaves as
documented under a real proxied SSE connection.

---

## 9. TLS

Obtain a single certificate covering both names:

```bash
sudo certbot --apache -d opencircuitsf.com -d www.opencircuitsf.com
```

Certbot's Apache plugin typically offers to add its own `:80` vhost with an
HTTP→HTTPS redirect if one doesn't already exist for these names — **accept
that offer** (or add one by hand) so plain-HTTP requests aren't served
insecurely; `deploy/apache/opencircuitsf.com.conf` as committed only defines
a `:443` vhost. Certbot also installs its own renewal timer/cron entry
automatically; confirm which (`systemctl list-timers | grep certbot` on a
systemd-managed renewal) and record it in the production-facts table above —
`[PLACEHOLDER: CLAUDE.md §10 item 6]`.

Reload once more if certbot didn't already:

```bash
sudo systemctl reload httpd
curl -fsS https://www.opencircuitsf.com/health
```

**Not run anywhere** — this needs a real, publicly resolvable hostname
pointed at a real box before certbot's HTTP-01 challenge can succeed; there
is nothing to run it against yet.

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
   setup** below; until SES exists, `MAILER_NOOP=true` logs the recovery
   link to stdout instead (`docs/configuration.md`), which is a
   `dev.sh`/local-only substitute, never a production setting.
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

**Not run against a real deploy** — no SES account exists to send the
recovery email through yet (`CLAUDE.md` §10 item 2), and no live instance
exists to receive the HTTPS request. Locally, `#0008`'s manual verification
procedure exercises the equivalent flow with `MAILER_NOOP=true` logging the
link to stdout — that is the closest thing to a proof this step has.

---

## DNS (Route 53) — real values from `PRD.md` §10.2

| Name | Type | Value | Purpose |
|---|---|---|---|
| `www.opencircuitsf.com` | A | `[PLACEHOLDER: the EC2 instance's Elastic IP — CLAUDE.md §10 item 6]` | **Canonical host** |
| `opencircuitsf.com` | A | `[PLACEHOLDER: same Elastic IP]` | 301 → `www` |
| `go.opencircuitsf.com` | A | `[PLACEHOLDER: same Elastic IP — ShortLinks shares the box]` | ShortLinks |
| `<sel1..3>._domainkey.opencircuitsf.com` | CNAME | `[PLACEHOLDER: issued by SES on domain verification, PRD §10.2/§10.4]` | DKIM |
| `mail.opencircuitsf.com` | MX | `10 feedback-smtp.us-west-2.amazonses.com` | Custom MAIL FROM |
| `mail.opencircuitsf.com` | TXT | `v=spf1 include:amazonses.com ~all` | SPF alignment |
| `lists.opencircuitsf.com` | MX | `10 inbound-smtp.us-west-2.amazonaws.com` | **Inbound unsubscribe only** — never the apex MX, `CLAUDE.md` §9 |
| `_dmarc.opencircuitsf.com` | TXT | `v=DMARC1; p=none; adkim=s; aspf=s; rua=mailto:…; fo=1` | DMARC — **start at `p=none`** |

Every record name, type, and static value above is real, copied verbatim
from `PRD.md` §10.2 (not invented for this document); the two things that
cannot be known before an instance exists are the Elastic IP and the
SES-issued DKIM CNAME targets — both explicitly placeholdered rather than
guessed, per this pass's instructions not to invent `CLAUDE.md` §10 item 6
facts.

**DMARC ramp — three steps, not one record.** Start `p=none` for at least
two weeks and read the aggregate (`rua=`) reports, then move to
`p=quarantine`, then `p=reject` once the reports show DKIM/SPF passing
cleanly. Jumping straight to `p=reject` risks silently dropping legitimate
mail with no visibility into why.

---

## SES setup

`CLAUDE.md` §10 item 2 records this as **not started**, deferred to
deployment (user, 2026-08-23) — the only AWS credentials available in this
development environment (`certbot-dns-updater`) cannot even
`ses:ListEmailIdentities`, so nothing below has been run, and nothing in this
pass attempted to run it. This section documents the steps to run **on the
real box, once the AWS account exists** — see
[`email-setup.md`](email-setup.md) for the same information organized by
subsystem rather than by deploy sequence.

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

**None of the above was executed.** This is a transcription of `PRD.md`
§10.2–§10.4 and `email-setup.md` into deploy order, re-checked against those
two documents for consistency, not a record of a real SES setup.

---

## IAM

The EC2 instance role must be scoped tightly (`PRD.md` §10.5) — no static
credentials anywhere in this project's configuration; the AWS SDK's default
credential chain picks up the instance role automatically
(`docs/configuration.md`'s `AWS_REGION` row). A policy document matching
§10.5 exactly:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "SESSendScoped",
      "Effect": "Allow",
      "Action": ["ses:SendEmail", "ses:SendRawEmail"],
      "Resource": [
        "arn:aws:ses:us-west-2:<ACCOUNT_ID>:identity/opencircuitsf.com"
      ],
      "Condition": {
        "StringEquals": {
          "ses:configuration-set": "opencircuit-transactional"
        }
      }
    },
    {
      "Sid": "InboundBucketScoped",
      "Effect": "Allow",
      "Action": ["s3:GetObject", "s3:DeleteObject"],
      "Resource": "arn:aws:s3:::opencircuitsf-inbound/*"
    }
  ]
}
```

`<ACCOUNT_ID>` is `[PLACEHOLDER: the AWS account ID, unknown until the
account from CLAUDE.md §10 item 2 exists]`. No `ses:*`, no wildcard
`Resource`, and no action beyond the two named pairs — matching `PRD.md`
§10.5 and `email-setup.md`'s IAM section verbatim. **This policy has never
been created or attached to a real role**; it is transcribed from the spec
and re-checked against `docs/configuration.md`'s `SES_INBOUND_BUCKET`
(`opencircuitsf-inbound`) and `SES_CONFIGURATION_SET`
(`opencircuit-transactional`) defaults for consistency, not validated by
`aws iam simulate-principal-policy` or any live AWS call — no credentials

**The `ses:configuration-set` condition key above is a best-effort encoding
of §10.5's "restricted to ... the configuration set" requirement, not a
verified one.** Its exact name and availability for `ses:SendEmail`/
`ses:SendRawEmail` was **not** checked against AWS's current IAM
condition-key reference for SES — no live AWS access exists in this
environment to confirm it (`CLAUDE.md` §10 item 2). Verify this key against
AWS's service-authorization reference for Amazon SES before attaching this
policy to a real role; the `Resource`-scoped identity-ARN restriction above
it does not depend on this key and is solid either way. The JSON itself
parses (`python3 -m json.tool`, see `## Verification`) — that only proves it
is well-formed, not that IAM will accept every key in it.
capable of that exist in this environment (`CLAUDE.md` §10 item 2).

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

**S3 upload is a deferred option, not abandoned.** `#0229`'s `## Decision`:
building it needs an AWS account, which does not exist yet (`CLAUDE.md` §10
item 2), and the user deferred all AWS work to deployment, same as SES.
`PRD.md` §10.6 now records S3 as an addable upload leg on top of `backup.sh`
once that account exists, rather than a redesign — nothing about the
local-dump/offsite-pull shape needs to change to add it later.

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
#    remember to type it in lowercase (#0248 review):
ISSUE="${ISSUE,,}"
echo "Running the drill under ISSUE=${ISSUE}"
#    scripts/testdb.sh clones the fully-migrated template —
#    check_template_fresh guarantees this is at HEAD (migration 20
#    applied). Confirm it before relying on it:
DSN="$(scripts/testdb.sh create "${ISSUE}src")"
psql "$DSN" -c "select version, dirty from schema_migrations"
#    Expect: version=20, dirty=false. If this isn't HEAD, stop — step 1b
#    below has nothing real to roll back and the rest of the drill proves
#    nothing about a pending migration.

# 1b. #0239: roll back exactly ONE migration so the source database is
#    genuinely N-1, not merely described as such. This is what the old
#    step 4b could never produce on its own — do it here, after creation,
#    with the real down migration, not a synthetic approximation of one:
migrate -path migrations -database "$DSN" down 1
psql "$DSN" -c "select version, dirty from schema_migrations"
#    Expect: version=19, dirty=false. If this still reads 20, STOP — the
#    rollback didn't take, and everything below would (again) silently
#    prove nothing. Migration 20 (create_workshops) is what's now missing:
#    its own down.sql dropped workshop_interests, workshops, and the FK it
#    had added to email_campaigns.

# 2. Populate the source database with representative data spanning every
#    table that exists AT THIS SCHEMA VERSION (19) — users, interests,
#    subscribers + subscriber_interests, suppressions, audit_log,
#    email_campaigns + campaign_interests + email_sends, email_events.
#    workshops / workshop_interests are deliberately NOT seeded here — that
#    table doesn't exist yet at version 19, which is the entire point.
#    Coverage for it comes from step 8 below, once migration 20 lands on
#    the restored target. Seed throwaway rows — never target a literal or
#    seeded id (CLAUDE.md §8b). The two enums you'll actually need here
#    (checked against the CHECK constraints in migrations/000012 and
#    000010, not guessed): suppressions.reason is one of hard_bounce |
#    complaint | manual | repeated_soft_bounce (NOT "bounce");
#    subscribers.status is one of pending | active | unsubscribed |
#    bounced | complained (NOT "confirmed").
psql "$DSN" -f seed.sql                     # your own INSERT ... RETURNING id
                                             # statements

# 3. Snapshot what "correct" looks like BEFORE backing up: row counts per
#    table, schema_migrations (version=19, dirty=false), every sequence's
#    last_value (NOT row count — see "Sequences" below), constraint count
#    per table, index count per table.

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
#    Expect: version=19, dirty=false — the restored copy is genuinely
#    missing migration 20 too, carried over faithfully from the source dump.
#    If this reads 20, the drill has drifted from step 1b again and step 6
#    below will silently no-op — stop and re-check, don't continue.

# 6. #0235's reproduction, reached by FOLLOWING this drill rather than
#    deviating from it (#0239's whole point): object ownership alone isn't
#    sufficient — restore.sh reassigns every table/sequence/view, but the
#    SCHEMA a still-pending migration will CREATE TABLE into must also be
#    usable by the app role, and this is where that gap showed up. Apply
#    the genuinely-pending migration:
migrate -path migrations -database "$DST_DSN" up
#    Success looks like migration 20 actually applying — migrate's own
#    output names it ("20/u create_workshops ...") — with no error, and a
#    following `migrate ... version` reporting 20, not 19. **"no change" or
#    an unchanged version here is a FAILURE of this drill, not a pass** — it
#    means nothing was pending, the exact silent-no-op #0239 exists to
#    close. Don't declare success; go back and check step 1b.
#    A `permission denied for schema public` here means the
#    schema-ownership step in `restore.sh` did not run or did not reach
#    `public` — this is the exact failure `#0235` found and fixed;
#    `restore.sh` now reassigns the schema itself
#    (`ALTER SCHEMA public OWNER TO …`), not just the objects inside it.
#    `RESTORE_OWNER=""` skips this step along with the object-level one —
#    expect the same permission-denied error in that case, deliberately.

# 7. Compare restored against source for every table that existed at both
#    schema versions — exclude workshops / workshop_interests entirely
#    (tables, their sequences, their indexes, and their constraints alike;
#    they exist only on the restored side, post-migration): table-by-table
#    row counts, sequence last_values, and index counts all must match
#    exactly.
#
#    Two things are EXPECTED to differ here, and a MATCH on either one
#    would mean step 6 silently no-op'd, not that the drill passed. (#0239
#    was bounced over exactly this: the old wording asked for these to
#    match too, which they structurally cannot once step 1b and step 6 are
#    both doing their job.)
#
#      - schema_migrations: source stays at version=19 (nothing ever
#        migrated it past step 1b's rollback); restored reads version=20
#        (step 6 applied the pending migration to it, and only it). Already
#        proven above at step 5 and step 6 — not a new check, just a
#        reminder not to re-flag it as a mismatch here.
#      - email_campaigns' constraint count: restored must be EXACTLY ONE
#        MORE than source's — the re-added `email_campaigns_workshop_id_fkey`
#        FK that migration 20's up.sql attaches (step 1b's comment names
#        what its down.sql drops). This is the positive check the old
#        wording was missing; confirm the actual delta, not "looks close":
diff <(psql "$DSN" -tAc "select table_name, count(*) from information_schema.table_constraints where table_schema='public' and table_name not in ('workshops','workshop_interests') group by table_name order by 1") \
     <(psql "$DST_DSN" -tAc "select table_name, count(*) from information_schema.table_constraints where table_schema='public' and table_name not in ('workshops','workshop_interests') group by table_name order by 1")
#    Expect EXACTLY one pair of lines, both naming email_campaigns,
#    differing by 1 (e.g. "< email_campaigns|12" / "> email_campaigns|13" —
#    your own counts depend on what you seeded; the delta of exactly 1 does
#    not). Any other line in this diff's output — a different table, or a
#    delta other than 1 on email_campaigns — is a real failure. diff(1)
#    itself exits 1 when it finds this expected line, which is correct —
#    see the note on exit status right after this block, and don't read a
#    non-zero exit from THIS diff as the drill having failed.
#
#    Row counts, sequence last_values, and index counts get the same
#    table-exclusion treatment but with NO exception — any line of diff
#    output from any of the three below is a real failure, full stop.
#    Committed here (#0248) rather than left as prose to translate: two
#    passes had already written this SQL independently before this one.
diff <(psql "$DSN" -tAc "select 'users', count(*) from users union all select 'interests', count(*) from interests union all select 'subscribers', count(*) from subscribers union all select 'subscriber_interests', count(*) from subscriber_interests union all select 'suppressions', count(*) from suppressions union all select 'audit_log', count(*) from audit_log union all select 'email_campaigns', count(*) from email_campaigns union all select 'campaign_interests', count(*) from campaign_interests union all select 'email_sends', count(*) from email_sends union all select 'email_events', count(*) from email_events union all select 'settings', count(*) from settings order by 1") \
     <(psql "$DST_DSN" -tAc "select 'users', count(*) from users union all select 'interests', count(*) from interests union all select 'subscribers', count(*) from subscribers union all select 'subscriber_interests', count(*) from subscriber_interests union all select 'suppressions', count(*) from suppressions union all select 'audit_log', count(*) from audit_log union all select 'email_campaigns', count(*) from email_campaigns union all select 'campaign_interests', count(*) from campaign_interests union all select 'email_sends', count(*) from email_sends union all select 'email_events', count(*) from email_events union all select 'settings', count(*) from settings order by 1")
#    Expect zero output. workshops / workshop_interests are named nowhere
#    in this query, not merely filtered out of it — they don't exist on
#    the source at all.

diff <(psql "$DSN" -tAc "select sequencename, last_value from pg_sequences where schemaname='public' and sequencename <> 'workshops_id_seq' order by 1") \
     <(psql "$DST_DSN" -tAc "select sequencename, last_value from pg_sequences where schemaname='public' and sequencename <> 'workshops_id_seq' order by 1")
#    Expect zero output. Never compare with max(id) instead — see
#    "Sequences" below for why that check would pass on a broken restore.

diff <(psql "$DSN" -tAc "select tablename, count(*) from pg_indexes where schemaname='public' and tablename not in ('workshops','workshop_interests') group by tablename order by 1") \
     <(psql "$DST_DSN" -tAc "select tablename, count(*) from pg_indexes where schemaname='public' and tablename not in ('workshops','workshop_interests') group by tablename order by 1")
#    Expect zero output.

# 8. Prove the restore — including the migration that just landed on it —
#    is actually usable, not just structurally present: insert a new row
#    into a pre-existing table and confirm it does not collide with
#    restored ids, confirm FK/CHECK/UNIQUE constraints still reject bad
#    data, AND insert a row into `workshops` — the table migration 20 just
#    created — to prove the app role can actually use what the pending
#    migration built, not merely that `migrate up` exited 0.

# 8b. Prove ownership the way that actually catches the defect (#0228): do
#    NOT just inspect pg_tables.tableowner as the role that ran the restore —
#    that check passes even when broken, because the restoring role can
#    always read what it just restored. Connect AS THE APPLICATION ROLE and
#    issue a real query:
psql "$DST_DSN" -c "select count(*) from subscribers;"
psql "$DST_DSN" -c "select count(*) from workshops;"
#    permission denied on either query means the ownership step did not run
#    or did not reach that object/schema — a passing catalog-only check
#    would not have caught that. The `workshops` query specifically is what
#    would have caught `#0235`'s gap: it is owned by whichever role's
#    `ALTER SCHEMA public OWNER TO …` ran (or didn't) during step 6's
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
pass signal — read the output (#0248).** One step in it legitimately returns
non-zero on a run that is working correctly, and `set -e` would abort the
drill there:

- **Step 7's constraint-count `diff`** is *expected* to print exactly one
  differing line (`email_campaigns`) — `diff(1)` exits `1` whenever it finds
  a difference, which here means the drill passed, not that it failed.

  This applies to the block **as committed**: step 8 above is comment-only
  (its three deliberate rejections — bad FK, bad `subscribers.status`,
  duplicate `subscribers.email` — are prose, not code, in this file), so
  they cannot trip `set -e` today. A follower who writes those `psql`
  invocations in and then adds `set -e` would be bitten by four non-zero
  exits, not one — each rejection is a `psql` call failing by design, same
  as the constraint diff. Don't add `set -e` even after filling step 8 in.

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
isn't added here (step 7's diff still isn't a candidate for it).

A completed run's real evidence is: step 6 actually applying migration 20
(its own output names it, not "no change"), step 7's four comparisons
printing exactly what "What the drill found" below documents, step 8/8b's
inserts and rejections behaving as documented, and step 9 dropping both
databases with no manual cleanup — a `testdb.sh drop` failure there is now a
real failure, not an expected one (the `RESTORE_OWNER=""` opt-out is the one
documented exception: it still leaves the database, deliberately, owned by
whoever ran `createdb`, since "leave ownership alone" is what the opt-out
means). The `${ISSUE}src` drop and the final `rm -rf` remain ordinary — a
non-zero exit from either is a real failure.

The custom format (`pg_restore`, default) and the plain format
(`BACKUP_FORMAT=plain`, `gunzip | psql`) were **both** run through this full
drill for #0248 (`ISSUE=0248` and `ISSUE=0248p`, one full pass each) —
identical results on every check in both runs, reported below with the
actual output.

### What the drill found

- **Row counts** — every table step 7's row-count `diff` names matched
  source-to-restored exactly, in both formats — zero output in both the
  `ISSUE=0248` and `ISSUE=0248p` runs. That list is the nine tables step 2
  actually seeds (`users`, `subscribers`, `subscriber_interests`,
  `suppressions`, `audit_log`, `email_campaigns`, `campaign_interests`,
  `email_sends`, `email_events`) plus `interests` and `settings`, which are
  not seeded at step 2 at all — both are populated by migrations before
  step 1 ever runs (`interests` by `000009`'s own `INSERT`, `settings` by
  `000004`/`000008`/`000015`/`000018`), so their row counts are fixed by
  the template rather than by whatever a follower seeds. Actual counts from
  the `0248` run, identical on both sides: `audit_log|2`,
  `campaign_interests|1`, `email_campaigns|1`, `email_events|2`,
  `email_sends|2`, `interests|12`, `settings|6`, `subscriber_interests|3`,
  `subscribers|5`, `suppressions|2`, `users|2`. `settings|6` is not a figure
  specific to this run — it is constant for every follower who runs
  `scripts/testdb.sh create` and seeds nothing into `settings`, and was
  re-measured directly against the live template, a fresh `migrate … up`
  database, and the `0248` source itself, all three agreeing (`#0248`,
  review pass). **`workshops` and `workshop_interests` are excluded, not
  compared** — the step 7 query names neither table. They don't exist on
  the source at all: step 1b rolls back migration 20, which drops them, so
  there is nothing on the source side to compare a restored count against.
  (This section previously claimed these two tables matched
  source-to-restored — that claim predated `#0239`'s rewrite and was never
  true of the drill as it exists now; corrected by `#0248`. A later pass
  found `settings|7` here, one higher than every direct measurement of the
  table; corrected to `settings|6` and the "every table populated at
  step 2" wording tightened to say which tables step 2 actually seeds,
  same review.)
- **Sequences** — every sequence's `last_value` matched source-to-restored
  exactly, in both formats — step 7's sequence `diff` printed zero output
  in both runs, checked with `select sequencename, last_value from
  pg_sequences where schemaname='public'` (excluding `workshops_id_seq`,
  which exists only post-migration on the restored side), never with
  `max(id)`. That distinction matters because sequence advancement in
  PostgreSQL is never transactional: a `nextval()` call is not undone when
  the transaction that made it rolls back, so a sequence can run ahead of
  the highest row actually committed (a failed bulk insert, a retried job).
  A restore that reset sequences from `max(id)` instead of trusting the
  dump's own sequence state would collide with a row that used to exist;
  `pg_dump`/`pg_restore` preserve `last_value` exactly on their own, which
  the diff above confirms every run, not by trusting the tool in the
  abstract. Confirmed directly at
  step 8: inserting a new `users` row into the restored `0248` database
  landed at `id=3` against a `users_id_seq` last_value of `2` — the next
  value, zero collision.
- **`schema_migrations`** — the source stays at `version=19, dirty=false`
  throughout, in both formats — nothing ever migrates it past step 1b's
  rollback. The restored copy reads `version=19, dirty=false` immediately
  after restore (carried over faithfully from the dump, confirmed at step
  5) and `version=20, dirty=false` after step 6 applies the pending
  migration (confirmed at step 6: `20/u create_workshops` printed, then a
  `select version, dirty` read back `20|f`). `version=20` on the source, or
  `version=19` on the restored copy after step 6 has run, would both be
  failures, not passes. `migrate` refuses to run against a `dirty=true`
  database, so this check is not optional. (This section previously
  claimed `version=20, dirty=false` before and after — that predates
  `#0239`'s step 1b/step 6 split, under which the drill can no longer
  produce that result; corrected by `#0248`.)
- **Extensions** — none. `grep -rn "CREATE EXTENSION" migrations/` returns
  nothing, so there is no extension dependency to worry about on restore.
- **Roles and ownership (corrected — `#0228`, then `#0235`)** — `backup.sh`
  dumps with `pg_dump --no-owner --no-privileges`, so the dump carries no
  role names or grants at all; without further action, ownership on restore
  is whichever role ran `pg_restore`/`psql`. **This section previously
  claimed that matched `opencircuit` before and after — that was true only
  because the local drill happened to run as the `opencircuit` role, and it
  is false in general.** `#0062`'s own phase-3 review reproduced the general
  case: running the restore as a *different* role (as production's
  documented `sudo -u postgres pg_restore` path does) left every table owned
  by that role instead, and `opencircuit` got `permission denied` on its
  first query — the restore reported success while leaving the app unable to
  read its own data. `restore.sh` closed this (`#0228`): after the restore
  completes, it reassigns ownership of every table, sequence, and view to
  `RESTORE_OWNER` (default `opencircuit`, matching `scripts/db/create.sql`'s
  bootstrap role) — and because ownership implies full privileges on an
  object, this also replaces what `--no-privileges` stripped, without a
  separate `GRANT` step. Proven with a real dump/restore round trip on a
  local scratch database: restoring as a role other than `opencircuit`
  (simulating the `postgres` production path) left tables owned by that role
  and the app role locked out (`permission denied for table widgets`);
  restoring with the fix in place left every table and sequence owned by
  `opencircuit`, and connecting **as `opencircuit`** — not as the restoring
  role — both `SELECT`ed and `INSERT`ed successfully.

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
  was a real pending migration to apply, not a synthetic probe), dumped it,
  restored it as a superuser standing in for `postgres` (reproducing the
  production path — this machine has no `postgres` OS role, so the local
  superuser filled that role structurally), and ran `migrate up` as
  `opencircuit` against it. **Before the fix**: `permission denied for
  schema public`, migration left `dirty`, reproducing `#0235`'s report
  exactly. **After the fix**: the same real `migrate up` applied
  `create_workshops` cleanly and `workshops` came out owned by
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
  `email_campaigns` → `email_sends`, etc. was handled correctly with no
  manual intervention in either format.
- **Index presence** — per-table index counts (`pg_indexes`), excluding
  `workshops`/`workshop_interests`, matched source-to-restored exactly, in
  both formats — step 7's index-count `diff` printed zero output in both
  runs.
- **Constraint presence** — per-table constraint counts
  (`information_schema.table_constraints`), same exclusion, do **not**
  match exactly, by design: `email_campaigns` differs by exactly one in
  both formats — step 7's constraint `diff` printed `< email_campaigns|12`
  / `> email_campaigns|13` in the `0248` run — the re-added
  `email_campaigns_workshop_id_fkey` FK migration 20 attaches. No other
  table differed, in either format. Live enforcement was confirmed by
  attempting (and getting rejected) a bad FK, a bad CHECK value, and a
  duplicate UNIQUE email against the restored database. (This section
  previously described constraint counts as part of a blanket "matched
  exactly" alongside index counts — they don't, by design, once step 1b
  and step 6 are both doing their job; corrected by `#0248`.)
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
  policy scoped to a bucket prefix, because none of those AWS resources exist
  yet (`CLAUDE.md` §10 item 2). `PRD.md` §10.6 records it as an addable
  upload leg once the AWS account exists, not as work that was skipped by
  mistake.
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
  real data volume. `deploy/systemd/opencircuit-backup.service`'s
  `WorkingDirectory=`/`ExecStart=` assume the repo is checked out at
  `/opt/opencircuit` — a placeholder (`CLAUDE.md` §10 item 6, still
  undocumented) that must be corrected to the real path before installing.
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

---

## Redeploy procedure

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

1. Rename the GitHub repo `Website` → `OpenCircuitSF`.
2. SES: verify domain in `us-west-2`, Easy DKIM, custom MAIL FROM, DMARC at
   `p=none`, request production access — blocks real sends from Phase 3.
3. Physical mailing address (PO box) — `#0045` refuses to start a campaign
   without it.
4. Sending identity (`hello@` vs. `workshops@`) and who reads the reply-to
   inbox — undecided, `PRD.md` §14 Q2 defaults to `hello@`.
5. Whether the domain needs human mailboxes — determines the apex MX,
   undecided, `PRD.md` §14 Q3.
6. Server-side details: instance ID/size/region, SSH access,
   `DocumentRoot`, vhost file, certbot renewal schedule, whether the
   existing Postgres is the target — undocumented, capture as encountered.
   This is the item most of this document's `[PLACEHOLDER: ...]` markers
   trace back to.
7. SES account-level suppression list (`aws sesv2
   put-account-suppression-attributes --suppressed-reasons BOUNCE
   COMPLAINT`) — see **SES setup** above, step 7; gated on item 2.

---

## What this document is, and is not, verified against

This is a **documentation deliverable**, verified by local inspection,
syntax-checking, and cross-referencing against the repo's actual files and
issue history — not by following it end to end on a real instance, which
does not exist. Concretely, for this pass:

- Every artifact path cited (`scripts/db/{create,drop}.sql`,
  `.env.example`, `deploy/apache/opencircuitsf.com.conf`,
  `deploy/systemd/{opencircuit,opencircuit-backup*}.service`,
  `deploy/systemd/opencircuit-backup.timer`, `cmd/opencircuit/{main,seed}.go`,
  `scripts/deploy.sh`, `scripts/db-reset.sh`) was confirmed to exist and was
  read, not assumed.
- The Apache vhost, including the new security headers, syntax-checks clean
  (`httpd -t`) against a real local Apache 2.4.67 with `mod_proxy`,
  `mod_proxy_http`, `mod_rewrite`, `mod_ssl`, and `mod_headers` loaded — see
  the Apache step above for exactly what that does and does not prove.
- The `script-src` CSP hash was computed programmatically (not hand-typed,
  `CLAUDE.md` §8) from `web/index.html`'s source, with the limitation that
  it was never checked against a real `npm run build` output — stated
  inline where the hash appears, with the exact recompute command.
- `deploy/systemd/opencircuit.service`'s hardening directives were confirmed
  present by reading the file directly, not by running it — there is no
  systemd on this development machine.
- The DNS, SES, and IAM sections are transcriptions of `PRD.md` §10.2–§10.5
  and `docs/email-setup.md`, cross-checked against `.env.example` and
  `docs/configuration.md` for internal consistency (variable names, default
  values), not validated against a real AWS account, which does not exist.
- `CLAUDE.md` §10 item 6's unknowns (instance ID, size, region, SSH access,
  `DocumentRoot`, installed vhost, certbot schedule, target Postgres) are
  left as explicit `[PLACEHOLDER: ...]` markers throughout, not invented.
- The acceptance criterion "the whole runbook followed once on a clean
  instance and corrected where it was wrong" is **not met** and cannot be
  until a real instance exists — see the top of this document.

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
