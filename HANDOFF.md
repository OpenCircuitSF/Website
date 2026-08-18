# Handoff — Open Circuit SF website

**Written:** 2026-08-18 · **Repo:** `github.com/brennanMKE/Website` · **Live:** <https://www.opencircuitsf.com>

Everything needed to pick this up on another machine. Read this first, then
[`PRD.md`](PRD.md) for the plan and [`issues/Issues.md`](issues/Issues.md) for
the working process.

---

## 1. Where things stand

| | Status |
|---|---|
| Product spec | ✅ [`PRD.md`](PRD.md) — 14 sections, complete |
| Work breakdown | ✅ 64 issues, `issues/0001.md`–`0064.md`, all `open`, dependencies cross-linked |
| Brand / logo assets | ✅ [`assets/logo/`](assets/logo/) — transparent, both tints, reproducible |
| Placeholder site | ✅ **Live** at www.opencircuitsf.com |
| Application code | ❌ **None yet.** No `go.mod`, no `web/`, no migrations. Phase 0 starts from zero. |

The placeholder is a real deliverable, not scaffolding — its token block, theme
toggle, and terminal motifs are the validated source that issues #0011 and #0013
port into Svelte.

---

## 2. Blocking decisions — needed before issue #0001

Neither is answered yet. Both are cheap to decide and expensive to reverse.

### 2.1 Go module path / repo name

`#0001` writes `module <path>` into `go.mod` and every import in the tree.
Changing it afterwards is a repo-wide rewrite plus a GitHub rename.

- Current remote: `github.com/brennanMKE/Website`
- PRD §14 Q5 recommends renaming the repo to `OpenCircuitSF` — `Website` under a
  personal account is ambiguous once there is more than one.

**Decide:** keep `Website`, or rename now and use
`github.com/brennanMKE/OpenCircuitSF`.

### 2.2 Commit granularity for issue files

The `issues` skill's convention is one commit per issue filed (`#NNNN <title>`).
That was written for filing bugs one at a time; bulk-filing 64 issues from a PRD
would produce 64 near-identical commits. This handoff commits them **grouped by
concern** instead. If you want the per-issue history, say so before more work
lands and it can be replayed.

---

## 3. Start these now — they gate later phases on other people's clocks

Neither is code. Both have multi-day lead times and both block a whole phase.

### 3.1 AWS SES — blocks Phase 3 onward (issues #0027+)

New SES accounts are sandboxed: 200 messages/day, verified recipients only.
Production access takes ~24h to approve.

1. Verify `opencircuitsf.com` as an SES identity in **`us-west-2`**
   (PRD §10.3 — chosen because it is close to SF *and* supports SES inbound
   email receiving, which a later phase needs; the inbound region list is
   shorter than the sending list).
2. Enable Easy DKIM → add the three CNAMEs to Route 53.
3. Set the custom MAIL FROM to `mail.opencircuitsf.com` → add its MX and TXT.
4. Add the DMARC TXT record at `p=none` initially; ramp to `p=quarantine` after
   reading two weeks of aggregate reports, then `p=reject`.
5. Request production access. Describe it honestly: opt-in announcement email
   for a community electronics group, double opt-in, one-click unsubscribe,
   bounce and complaint handling wired to a suppression list.

**Do not point the apex MX at SES.** That hijacks all mail to the domain.
Inbound unsubscribe handling uses a dedicated `lists.opencircuitsf.com`
subdomain — see PRD §6.5 path 3.

### 3.2 A physical mailing address — blocks Phase 5's first send (issue #0045)

CAN-SPAM §7704 requires a valid physical postal address in every commercial
message. `#0045`'s send worker is specified to **refuse to start a campaign**
when the `physical_address` setting is empty, and that check is deliberately not
bypassable from the UI.

A PO box is the clean answer and takes days to arrange. This is the quietest
blocker in the whole plan — nothing surfaces it until the day of the first
announcement.

---

## 4. Mac mini setup

### 4.1 Clone the ShortLinks repo first — `#0001` depends on it

**This is the single most important environment prerequisite.** Issue `#0001`
copies the skeleton from a local checkout of ShortLinks; without it, Phase 0
cannot start.

```bash
mkdir -p ~/Developer/brennanMKE
git clone git@github.com:brennanMKE/ShortLinks.git ~/Developer/brennanMKE/ShortLinks
```

The path matters — every Phase 0 issue references
`~/Developer/brennanMKE/ShortLinks`. If you put it elsewhere, update issues
#0001–#0005.

### 4.2 Toolchain

| Tool | Version | For |
|---|---|---|
| Go | 1.26+ | the service |
| Node | 20+ | the Svelte SPA |
| PostgreSQL | 16+ | local dev and tests against a real database (`internal/testdb`) |
| `golang-migrate` | latest | `brew install golang-migrate` |
| Python 3 + Pillow + numpy | — | only to regenerate logo assets |

Phase 0 and most of Phase 1 need only Go and Node — `./scripts/dev.sh` runs the
whole app with `STORAGE=json` and no database at all. PostgreSQL is needed from
`#0004` (migrations) onward.

### 4.3 Regenerating assets

Both scripts are committed and verified to reproduce the current files
byte-for-byte:

```bash
python3 assets/logo/build-logo.py   # all logo/mark/favicon variants
python3 placeholder/build-og.py     # the 1200x630 share card
```

`build-og.py` depends on macOS system fonts (Arial Black, Menlo) and will fail
loudly elsewhere. The source master is committed at
`assets/logo/source/Logo-2026.png`.

---

## 5. Live deployment — what is known

Observed from outside on 2026-08-18. **Nobody has written down the server-side
details yet** — see the gaps below.

| | |
|---|---|
| Canonical host | `https://www.opencircuitsf.com` (apex and HTTP both 301 to it) |
| Server | Apache 2.4.68 on Amazon Linux, OpenSSL 3.5.7 |
| TLS | Let's Encrypt, valid to **2026-11-16** |
| Assets | all six serving correctly, correct content types |
| Social preview | correct — OG tags and canonical all point at the www host |

### 5.1 www is canonical — this changed the WebAuthn config

The PRD originally assumed the apex would serve. It doesn't. Corrected in
PRD §9 and issue #0008:

```env
WEBAUTHN_RP_ID=opencircuitsf.com              # apex — a passkey stays valid across apex and www
WEBAUTHN_RP_ORIGIN=https://www.opencircuitsf.com   # must match the browser's actual origin
```

These are **not** interchangeable. A mismatch between `RP_ORIGIN` and the real
origin fails passkey ceremonies with an opaque error. Check this first if a
ceremony fails in Phase 1.

### 5.2 Gaps to fill in on the Mac mini

Write these into `docs/deployment.md` (issue `#0064`) while they are still fresh:

- What is the EC2 instance ID / size / region, and how do you SSH in?
- Where does Apache serve the placeholder from (`DocumentRoot`)?
- Which vhost config file, and is certbot auto-renewal actually scheduled?
- Is PostgreSQL installed on this box, and is it the target for the Go service?
- Is the ShortLinks install for `go.opencircuitsf.com` on this same instance?

### 5.3 Deferred hardening

The live placeholder currently sends **no** security headers — no HSTS,
`X-Content-Type-Options`, `Referrer-Policy`, or CSP — and no `Cache-Control`, so
the 148 KB share card revalidates on every load. Low risk for a static page with
no auth or forms, and `#0064` covers all of it for the real deploy. Two cheap
wins whenever you are next in the Apache config: add HSTS (it wants a ramp-up
period before you would ever consider preload), and set `ServerTokens Prod` to
stop advertising the exact Apache and OpenSSL versions.

---

## 6. Findings worth not rediscovering

Each of these cost real time to work out.

**CSS masks fail silently under `file://`.** The logo is drawn with
`mask-image` tinted by `currentColor`, which is what makes one asset work in
both themes. Under `file://` every file is its own opaque origin, the mask fails
to load, and the element renders **fully transparent** — while the `<img>`-based
favicon still works, which makes it look like a CSS bug. Always preview with
`python3 -m http.server`. Noted in `placeholder/README.md` and folded into
`#0018` so it reaches `docs/frontend.md`.

**Transparency alone does not make a logo theme-adaptive.** The brand green
`#68FF23` measures 14.8:1 on the dark ground but **1.32:1 on white** — nearly
invisible. Hence two tints (`#30800C` for light) and the mask approach. Full
measurements in `assets/logo/README.md`.

**The full logo is illegible below ~96 px.** At 32 px it is mush. The chip with
the `>_` prompt is extracted as a separate `mark-*` asset for favicons and
avatars.

**Focus rings must use `--accent`, not `--border-strong`.** The latter measures
under 3:1 against the page in both themes — acceptable for a decorative edge,
insufficient as a focus indicator. Recorded in PRD §4.2.

**Headless Chromium enforces a minimum window width** (~485 px CSS), so
`--window-size=390` does not test a phone layout — it silently clips a wider
render, which looks exactly like a horizontal-overflow bug. Test narrow layouts
by loading the page in a sized `<iframe>` instead; media queries apply to the
iframe's own viewport. The placeholder was verified clean at 305 px and 390 px
this way.

---

## 7. Suggested first moves

1. Answer §2.1 (module path) — one word, unblocks everything.
2. Kick off §3.1 (SES) and §3.2 (PO box) — they run on other people's clocks.
3. Clone ShortLinks to `~/Developer/brennanMKE/ShortLinks` (§4.1).
4. Start `#0001`. Phase 0 (`#0001`–`#0006`) is mechanical — copy, strip, rename,
   get `go build ./...` and `go test ./...` green on the reduced tree.

Issues are worked in numeric order unless an issue's `## Relation` says
otherwise. The one exception already in the set: **`#0018` (logo assets) blocks
`#0017` (site header)** despite the higher number.

### A note on the issue workflow

`issues/Issues.md` describes a three-phase pipeline — planning (Fable) at filing
time, implementation (Sonnet), review (Opus) — each in a fresh subagent. **The
64 issues were filed without the planning phase**, at the user's standing
instruction not to spawn agents unasked. They carry detailed acceptance criteria
instead of `## Plan` sections. If you want planning passes, run them per issue
before implementation; the Phase 4–5 issues (unsubscribe, campaigns, send
worker) would benefit most, the Phase 0–1 issues least.
