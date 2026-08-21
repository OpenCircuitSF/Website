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
  //  - The erasure paragraph under "How to leave" must keep naming all three
  //    things #0060 retains after a hard delete (suppression entry,
  //    anonymized `email_sends` rows, raw `email_events` payloads) -- #0060's
  //    own acceptance criteria require this page to document that, and a
  //    bare "we delete your data entirely" is false against that design.
  //  - "No third-party analytics, ad trackers, external CDNs, or email
  //    open-tracking pixels" is CLAUDE.md §9's binding restriction, stated
  //    here as a fact about how the site is built, not a promise.
  //  - #0075 answered the five facts that were bracketed "PLACEHOLDER" markers here:
  //    retention after unsubscribe (kept indefinitely, marked `unsubscribed`, no
  //    purge job -- matches the schema as designed), erasure turnaround (30 days),
  //    the privacy-request contact address (hello@opencircuitsf.com, not a
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

  const CONTACT_EMAIL = 'hello@opencircuitsf.com';
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
        subscriber record — your email, interests, and signup details — but three
        things are deliberately retained rather than deleted:
      </p>
      <StatusList
        items={[
          'a permanent suppression entry, so the address cannot be silently re-added by a future import or signup',
          'anonymized rows in our send history, so historical campaign counts (how many people a given email actually reached) do not silently change',
          'the raw deliverability events (bounces, complaints, deliveries) already logged against your address, kept without the link back to your identity, for spam/abuse forensics',
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
