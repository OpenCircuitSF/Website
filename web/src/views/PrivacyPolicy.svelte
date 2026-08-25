<script lang="ts">
  // Privacy policy view (#0070, PRD §11): what the mailing list collects,
  // why, how long it's kept, and how to leave. Filed because the footer
  // (#0017) already links /privacy and, until this issue, the route didn't
  // exist -- a 404 behind a link PRD §11 requires.
  //
  // Composed from the same motif components as About.svelte (TerminalPanel,
  // Prompt, Panel, StatusList, TraceDivider) -- no bespoke styling, per the
  // acceptance criteria.
  //
  // Copy notes:
  //  - "What we collect" and "why" are checked against PRD §6.2's actual
  //    `subscribers` schema, not just §11's four-item prose summary -- the
  //    schema also stores `signup_user_agent` and the full UTM triple
  //    (source/medium/campaign), so both lists name all of them. §11's
  //    four items are a fair *summary*; this page enumerates the schema.
  //  - The GDPR/CCPA posture (double opt-in with IP + timestamp; erasure as
  //    hard delete plus a permanent suppression entry) and the CAN-SPAM
  //    commitments (physical mailing address in every commercial message;
  //    unsubscribe honored immediately, well inside the 10-day requirement)
  //    are PRD §11's stated design, not yet built -- #0026 (subscribe),
  //    #0033-#0039 (unsubscribe/hygiene), and #0060 (erasure) are all still
  //    open at the time this page was written. The policy describes the
  //    documented commitment; if the shipped mechanics ever differ, this page
  //    needs a follow-up pass (see #0070's Notes). Because none of this is
  //    live yet, the intro panel carries one explicit line that signup is not
  //    open -- present tense elsewhere is forward-looking policy, not a claim
  //    about today (see #0070's Gotchas for the full reasoning).
  //  - Do not close either collection list with an absolute ("that is the
  //    complete list", "we collect nothing else"). Two review passes bounced
  //    exactly that sentence: signup also stores `confirmed_at` and mints
  //    `manage_token`/`confirm_token`, and §6.2's table is not in code yet
  //    (#0025 open, migrations stop at 000008), so any completeness claim
  //    drifts the moment the schema lands. Enumerate; don't claim closure.
  //  - The Consent section names only what §6.2 actually has two of:
  //    `signup_ip` + `confirmed_at`. There is no confirm-time IP column --
  //    don't reintroduce "the confirming click's IP" here.
  //  - The erasure paragraph under "How to leave" must keep naming all five
  //    things erasure retains after a hard delete (suppression entry,
  //    anonymized `email_sends` rows, raw `email_events` payloads, the
  //    internal admin audit log, and -- since #0126 -- the redacted
  //    `subscriber_events` activity log) -- #0060's own acceptance criteria
  //    require this page to document that, and a bare "we delete your data
  //    entirely" is false against that design. Do not shrink this back to
  //    "four things": a Phase 3 review of #0060 walked the real subscribe ->
  //    confirm -> erase journey and found the audit log still holds the
  //    address and the signup IP after erasure -- the page previously
  //    omitted it. Per the "don't close either list with an absolute" note
  //    above, if a future change adds or removes a retained category,
  //    update the count rather than re-closing it at a fixed number.
  //  - #0126 added the fifth category: `subscriber_events`
  //    (`internal/subscribers/events.go`) is a new append-only activity log
  //    -- one row per meaningful thing that happened to an address (signup,
  //    confirmation, interest changes, unsubscribe, bounces, suppressions,
  //    the erasure itself). `subscribers.Store.Erase` redacts its `email`
  //    column (replacing it with a placeholder keyed by the now-deleted
  //    subscriber's id, grouping the address's history without
  //    re-identifying it) and lets `subscriber_id` go `NULL` via the
  //    table's own `ON DELETE SET NULL`, rather than deleting the rows --
  //    the same "anonymize, don't delete" treatment `email_sends` already
  //    gets, for the same reason: the erasure's own evidence (an `erased`
  //    row, written with the same placeholder) must survive being
  //    performed. See `internal/subscribers/erase.go`'s redaction and
  //    `PrivacyPolicy.guard.test.ts`'s fifth `ERASURE_RETAINED_CATEGORIES`
  //    entry, which pins this item's required phrases against that code.
  //    `PrivacyPolicy.guard.test.ts` (#0226) asserts this list structurally
  //    against `internal/subscribers/erase.go` and
  //    `internal/handlers/admin_subscribers.go`'s real behavior -- see that
  //    file for the exact source lines each item is checked against. Three
  //    accuracy fixes from #0226's two review passes, all folded into the
  //    item text above rather than left as a second "the page previously
  //    said" footnote:
  //      - the audit-log item now says "each action" / "each entry records
  //        the IP address of the request that made it", not just "your
  //        signup IP address" -- audit.Entry{IP: clientIP(r)} is written on
  //        every subscriber-driven request (signup, the confirming click,
  //        both unsubscribe paths, a preference update), not only signup
  //        (internal/handlers/subscribe.go, confirm.go, unsubscribe.go,
  //        preferences.go all set it), and the erasure entry itself carries
  //        the ADMIN's IP (admin_subscribers.go's clientIP(r) is the admin's
  //        own request), not the erased subscriber's -- worth not implying
  //        otherwise. The item also now says this is "a separate mechanism"
  //        from the single consent-evidence IP the Consent section
  //        describes, so the two sections read as complementary rather than
  //        contradictory (the Consent section's "not a second IP address"
  //        claim is about the `subscribers` table's own columns -- it has
  //        exactly one, `signup_ip` -- and stays true; audit_log is a
  //        different table serving a different, operational purpose).
  //      - the deliverability-events item now names "bounce/complaint
  //        handling" as a purpose alongside "spam/abuse forensics" -- the
  //        events themselves (bounces, complaints, deliveries) were already
  //        named, but the parenthetical purpose list omitted the reason
  //        PRD §6.9's delivery-health circuit breaker exists at all.
  //      - #0226's FIRST review pass (2026-08-24) widened the IP disclosure
  //        above but, in doing so, deleted the email-address disclosure the
  //        item previously had ("this includes your email address") without
  //        replacing it -- so the page said less than the code retains,
  //        which is the more serious direction of drift (CLAUDE.md §9). The
  //        item now says explicitly that the confirmation entry and the
  //        erasure entry itself each record the email address:
  //        confirm.go's audit.Entry sets `Metadata: map[string]any{"email":
  //        sub.Email}` (internal/handlers/confirm.go), and
  //        admin_subscribers.go's erasure entry sets `Metadata:
  //        map[string]any{"email": result.Email, ...}`
  //        (internal/handlers/admin_subscribers.go) -- verified directly
  //        against both call sites, not assumed. subscribe.go's,
  //        unsubscribe.go's, and preferences.go's entries carry
  //        `kind`/`source`/`interest_count` metadata, not the email, so the
  //        item does NOT claim every entry carries it -- only that some do,
  //        naming which.
  //      - #0237 (#0226's own phase-3 review): the audit-log item above was
  //        still scoped to what #0226 examined -- the five SUBSCRIBER-DRIVEN
  //        audit.Entry call sites (signup, confirmation, unsubscribe,
  //        preference update, erasure). Reading every `audit.Entry{`
  //        construction in internal/ and cmd/ (not just those five) found
  //        five MORE that write the subscriber's own address into
  //        `audit_log.metadata` and, like the others, survive erasure:
  //        `internal/handlers/admin_subscribers.go`'s manual-add entry,
  //        `internal/handlers/admin_suppressions.go`'s suppression-removal
  //        entry, and `internal/handlers/ses_notifications.go`'s two bounce
  //        entries (permanent, and repeated-soft) plus its complaint entry.
  //        The item now names all three CATEGORIES a row can come from
  //        (subscriber-initiated, admin-initiated, automated from the
  //        delivery provider) rather than enumerating only the subscriber
  //        ones, and lists all six actions that record the email explicitly
  //        (confirmation, manual add, suppression removal, bounce,
  //        complaint, erasure) rather than just two. It also corrects the
  //        IP claim: the three SES-driven entries set NO `IP` field at all
  //        (verified directly -- `ses_notifications.go`'s three
  //        `audit.Entry{}` literals for these actions carry no `IP:` key),
  //        because there is no user request behind an SES notification; the
  //        previous "each entry records the IP address of the request that
  //        made it" was true only for the request-driven entries.
  //        `internal/handlers/audit_email_metadata_guard_test.go` (#0237) is
  //        the durable version of this check #0226's Notes asked for: it
  //        walks every production `audit.Entry{` construction found inside a
  //        named function's body in internal/ and cmd/ (see that file's
  //        header and #0252 below for the shapes that sit outside one and
  //        are therefore invisible to it), classifies whether its Metadata
  //        could carry the literal key "email" (resolving inline literals, a
  //        traced local variable built across the function including any
  //        conditional index assignment, and one known-safe helper call),
  //        and fails -- naming the exact file and action -- if the SET of
  //        call sites that write an email differs from what it has pinned.
  //      - #0237's own phase-3 review BOUNCED the first pass: its guard
  //        matched only the literal key "email", so a real production shape
  //        it had just excluded as a near-miss --
  //        `admin_campaign_preview.go`'s `"recipient_email": sub.Email` --
  //        proved the exact hole (a synthetic
  //        `{"recipient_email": addr, "subscriber_address": addr}` site
  //        passed the guard silently). The guard now matches any
  //        underscore-token of "email", "address", "recipient", or "query"
  //        (`metadataKeyIsSuspectedEmailCarrier`), not just the exact string
  //        "email" -- token-based, not substring, so it does not also flag
  //        `internal/mailing/worker.go`'s unrelated "recipients" (a COUNT,
  //        not an address). That widening surfaced two more real sites,
  //        pinned in `auditEmailMetadataKnownSites` but NEITHER added to the
  //        "six actions" list above, because neither is a subscriber's own
  //        address: `admin_campaign_preview.go`'s test-send entry (the
  //        acting admin's own address, and a synthetic
  //        `campaign-test+admin-<id>@` test recipient -- never a real
  //        subscriber), and `admin_subscribers_export.go`'s export entry,
  //        whose "filter_query" key holds the raw text of an admin's search
  //        box -- unbounded free text this guard cannot read, so it MAY
  //        contain a subscriber's own address if that is what was searched
  //        for. That second one genuinely can carry your address, so this
  //        item's last clause now discloses it explicitly ("if a staff
  //        member ever searches ... that search text is recorded too"),
  //        contingently rather than as a certainty like the six structural
  //        actions. The "admin-initiated actions" parenthetical also gained
  //        "for example" -- the review separately noted it read as an
  //        exhaustive list of admin actions on a subscriber's record when it
  //        is not (two more exist, `ActionSubscriberSuppressed` and
  //        `ActionSubscriberComplaintCleared`, neither carrying an email);
  //        softening it to an example list is honest without enumerating
  //        two more actions that touch nothing this bullet is about.
  //      - #0252 corrected this comment block's own echo of a false claim
  //        the guard file's header made about itself: "walks every
  //        production `audit.Entry{` construction" overstated the guard's
  //        reach -- it only walks composite literals found inside a named
  //        function's body, and three demonstrated shapes sit outside one
  //        entirely (a package-level var literal, one inside a func literal
  //        on a package-level var, and an elided-type slice element), all
  //        invisible to it (SITES=0, run directly, not reasoned about). A
  //        fourth shape -- `e := audit.Entry{}` then a later
  //        `e.Metadata = ...` struct-field assignment -- was SEEN by the
  //        walk but resolved as email-free by default, a silent pass; #0252
  //        fixed the guard itself to report that shape as unresolved
  //        instead, the same conservative "cannot classify means fail"
  //        default every other unresolvable shape already gets. None of the
  //        three structurally invisible shapes is exercised by any real
  //        site in this tree, so nothing about the "six actions" disclosure
  //        above changed -- only the guard's own description of what it
  //        can see, and the guard's actual behavior for the one shape that
  //        was a live risk rather than a structural blind spot.
  //  - "No third-party analytics, ad trackers, external CDNs, or email
  //    open-tracking pixels" is CLAUDE.md §9's binding restriction, stated
  //    here as a fact about how the site is built, not a promise.
  //  - #0075 answered the five facts that were bracketed "PLACEHOLDER" markers here:
  //    retention after unsubscribe (kept indefinitely, marked `unsubscribed`, no
  //    purge job -- matches the schema as designed), erasure turnaround (30 days),
  //    the privacy-request contact address (contact@opencircuitsf.com, not a
  //    dedicated alias), and the legal entity ("Open Circuit SF" is an
  //    unincorporated community group, San Francisco -- named as the data
  //    controller; do not imply incorporation anywhere on this page).
  //  - The fifth placeholder -- a physical mailing address -- was removed rather
  //    than answered. PRD §11's privacy-policy bullet requires only what's
  //    collected, why, retention, and how to leave; it does not list a postal
  //    address. #0060's acceptance criteria likewise only require this page to
  //    document post-erasure retention, not an address. CAN-SPAM §7704's postal-
  //    address obligation is scoped to commercial *email* (PRD §11's Compliance
  //    bullet: "physical mailing address in every message") and is enforced by
  //    #0045's send-worker gate (`physical_address` setting), not by this page.
  //    CLAUDE.md §10 open item 3 (PO box, "not started") still tracks getting
  //    that address for #0045 -- unrelated to this page now. `scripts/deploy.sh`
  //    gate 1 greps web/src/ for that bracketed marker and, with none left, no
  //    longer blocks this page.
  import TerminalPanel from '../lib/TerminalPanel.svelte';
  import Prompt from '../lib/Prompt.svelte';
  import StatusList from '../lib/StatusList.svelte';
  import Panel from '../lib/Panel.svelte';
  import TraceDivider from '../lib/TraceDivider.svelte';
  import { APP_NAME } from '../lib/branding';

  const CONTACT_EMAIL = 'contact@opencircuitsf.com';
  const LAST_UPDATED = '2026-08-19';
</script>

<main id="main-content" class="app-shell privacy-shell">
  <TerminalPanel title="privacy // open_circuit_sf">
    <Prompt text="cat privacy-policy.md" />
    <h1 class="headline">Privacy Policy</h1>
    <p>
      {APP_NAME} runs a mailing list so people can hear about upcoming workshops. This
      page explains what that list collects, why, how long it's kept, and how to leave
      it. It applies to the workshop mailing list and this website; it does not cover
      third-party services like Discord or Luma, which have their own privacy policies.
    </p>
    <p class="text-muted">
      Mailing-list signup is not open yet. This policy documents the commitment we are
      making about it in advance, so it is accurate from the moment signup does open —
      nothing below describes something already happening to you today.
    </p>
    <p class="text-muted">Last updated: {LAST_UPDATED}</p>
  </TerminalPanel>

  <TraceDivider />

  <section aria-labelledby="collect-h">
    <h2 id="collect-h">What we collect</h2>
    <Panel>
      <p>Signing up for the mailing list collects:</p>
      <StatusList
        items={[
          'your email address',
          'the workshop interests/topics you select at signup',
          'the IP address and timestamp of your signup — evidence of consent, not a tracking measure',
          'the browser/device user agent string your browser sends at signup',
          'the UTM parameters (source, medium, campaign) of the link you signed up from, if any (e.g. which social post or page sent you)',
        ]}
      />
      <p>
        Signing up also records the time you confirm your subscription (see "Consent"
        below), and generates the tokens that make your unsubscribe and
        preference-center links work.
      </p>
      <p class="text-muted">
        This site does not run third-party analytics, ad trackers, external CDNs, or
        email open-tracking pixels — nothing on this site phones home to anyone but us.
      </p>
    </Panel>
  </section>

  <TraceDivider />

  <section aria-labelledby="why-h">
    <h2 id="why-h">Why we collect it</h2>
    <Panel>
      <StatusList
        items={[
          'email — to send you workshop announcements and the occasional list update',
          'interests — so a workshop announcement can go to the people who asked about that topic instead of the whole list',
          'signup IP, user agent, and timestamp — documented proof of opt-in consent (GDPR/CCPA) and abuse prevention',
          'UTM parameters — to understand which channels bring people to the list',
        ]}
      />
    </Panel>
  </section>

  <TraceDivider />

  <section aria-labelledby="retention-h">
    <h2 id="retention-h">How long we keep it</h2>
    <Panel>
      <p>
        We keep your subscriber data for as long as you remain subscribed. If you
        unsubscribe, we keep your record marked unsubscribed so we do not
        accidentally add you back — it is not automatically deleted. You can ask us
        to erase it entirely at any time; see "How to leave" below for how, and for
        the handful of records that outlive even an erasure request by design, and
        why.
      </p>
    </Panel>
  </section>

  <TraceDivider />

  <section aria-labelledby="consent-h">
    <h2 id="consent-h">Consent</h2>
    <Panel>
      <p>
        Signup uses double opt-in: you submit the form, then confirm via a link we
        email you. Submitting the form stores your details with a pending status; the
        only email an unconfirmed signup receives is that confirmation, because every
        campaign's audience is drawn from confirmed subscribers. The IP address and
        timestamp of your original signup (see "What we collect" above) are recorded as
        evidence of consent, and the confirming click separately records its own
        timestamp — not a second IP address. Together, those records are what
        "evidenced consent" (the GDPR/CCPA standard) means in practice.
      </p>
    </Panel>
  </section>

  <TraceDivider />

  <section aria-labelledby="leave-h">
    <h2 id="leave-h">How to leave</h2>
    <Panel>
      <p>
        <strong>Unsubscribe.</strong> Every campaign email we send carries a
        one-click unsubscribe link. We honor it immediately — not just within the
        10 days CAN-SPAM requires.
      </p>
      <p>
        <strong>Erasure.</strong> You can request that we delete your personal data
        rather than just unsubscribing. An erasure request hard-deletes your
        subscriber record — your email, interests, and signup details — but five
        things are deliberately retained rather than deleted:
      </p>
      <StatusList
        items={[
          'a permanent suppression entry, so the address cannot be silently re-added by a future import or signup',
          'anonymized rows in our send history, so historical campaign counts (how many people a given email actually reached) do not silently change',
          'the raw deliverability events (bounces, complaints, deliveries) already logged against your address, kept without the link back to your identity, for spam/abuse forensics and to keep our own bounce/complaint handling accurate',
          'an internal admin audit log entry for actions on your account — not only the ones you take yourself (signup, confirmation, unsubscribe, and preference updates) but also the erasure itself, admin-initiated actions (for example, being added to the list manually or having a suppression removed), and automated entries from our delivery provider (a bounce or a spam complaint registered against your address); each entry records the IP address behind the request that created it: yours for actions you take, the IP of the acting admin for admin-initiated ones including the erasure, and no IP at all for the automated delivery-provider entries, since there is no request behind them — a separate mechanism from the single consent-evidence IP described above; the confirmation entry, being added manually, a suppression removal, resetting an address\'s bounce streak, a bounce, a complaint, and the erasure entry itself also record your email address explicitly, and if a staff member ever searches the subscriber list by your address while exporting it, that search text is recorded too; kept so we can prove what happened and to investigate abuse; it is not exposed publicly and is not used to re-add or re-contact you',
          'an entry in our address activity log for every meaningful thing that happened to your subscription (signup, confirmation, interest changes, unsubscribe, bounces, suppressions, and the erasure itself) — kept after erasure with your email address replaced by a placeholder, so the history stays grouped as evidence the erasure was performed, but the placeholder no longer identifies you',
        ]}
      />
      <p>
        We will erase your data within 30 days of your request, and usually much
        sooner.
      </p>
      <p>
        <strong>Export.</strong> You can also request a copy of the data we hold about
        you.
      </p>
      <p>
        To unsubscribe, use the link in any email from us. For erasure or export
        requests, contact us at the address below.
      </p>
    </Panel>
  </section>

  <TraceDivider />

  <section aria-labelledby="canspam-h">
    <h2 id="canspam-h">CAN-SPAM</h2>
    <Panel>
      <p>
        Every commercial email we send carries an accurate "From" address, a
        non-deceptive subject line, and a physical mailing address, printed in the
        email itself as CAN-SPAM requires. That address does not need to appear on
        this page — CAN-SPAM's postal-address requirement applies to the emails, not
        to this policy — so if you want to reach us in the meantime, use the contact
        address below.
      </p>
    </Panel>
  </section>

  <TraceDivider />

  <section aria-labelledby="contact-h">
    <h2 id="contact-h">Contact</h2>
    <Panel>
      <p>
        For questions about this policy, or to request export or erasure of your data,
        reach us at
        <a href="mailto:{CONTACT_EMAIL}">{CONTACT_EMAIL}</a>.
      </p>
      <p class="text-muted">
        {APP_NAME} is an unincorporated community group based in San Francisco. We
        are the data controller for the information described here.
      </p>
    </Panel>
  </section>

  <TraceDivider />

  <section aria-labelledby="changes-h">
    <h2 id="changes-h">Changes to this policy</h2>
    <Panel>
      <p>
        If this policy changes in a way that affects what we collect or how we use it,
        we'll update this page and note the date at the top.
      </p>
    </Panel>
  </section>
</main>

<style>
  .privacy-shell {
    display: flex;
    flex-direction: column;
    gap: var(--space-6);
  }

  .headline {
    margin: var(--space-3) 0 var(--space-4);
    font-size: var(--fs-xl);
    font-weight: 700;
  }

  .privacy-shell section h2 {
    font-size: var(--fs-lg);
    margin: 0 0 var(--space-3);
  }

  .privacy-shell p {
    max-width: var(--measure);
    margin: 0 0 var(--space-3);
  }
  .privacy-shell p:last-child {
    margin-bottom: 0;
  }
</style>
