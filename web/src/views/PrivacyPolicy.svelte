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
  //  - "What we collect" (email, interests, signup IP, UTM source) and "why"
  //    are taken directly from PRD §11's Compliance section, not invented.
  //  - The GDPR/CCPA posture (double opt-in with IP + timestamp; erasure as
  //    hard delete plus a permanent suppression entry) and the CAN-SPAM
  //    commitments (physical mailing address in every commercial message;
  //    unsubscribe honored immediately, well inside the 10-day requirement)
  //    are PRD §11's stated design, not yet built -- #0026 (subscribe),
  //    #0033-#0039 (unsubscribe/hygiene), and #0060 (erasure) are all still
  //    open at the time this page was written. The policy describes the
  //    documented commitment; if the shipped mechanics ever differ, this page
  //    needs a follow-up pass (see #0070's Notes).
  //  - "No third-party analytics, ad trackers, external CDNs, or email
  //    open-tracking pixels" is CLAUDE.md §9's binding restriction, stated
  //    here as a fact about how the site is built, not a promise.
  //  - Four facts are placeholders because they are not this page's author's
  //    to invent: the exact data-retention window, the erasure turnaround
  //    time, the contact address for privacy requests specifically, and the
  //    legal entity name/type behind "Open Circuit SF". Each is marked
  //    in-line as [PLACEHOLDER: ...] and called out in the issue's
  //    resolution notes for the user to fill in with a real value.
  import TerminalPanel from '../lib/TerminalPanel.svelte';
  import Prompt from '../lib/Prompt.svelte';
  import StatusList from '../lib/StatusList.svelte';
  import Panel from '../lib/Panel.svelte';
  import TraceDivider from '../lib/TraceDivider.svelte';
  import { APP_NAME } from '../lib/branding';

  const CONTACT_EMAIL = 'hello@opencircuitsf.com';
  const LAST_UPDATED = '2026-08-18';
</script>

<main id="main-content" class="app-shell privacy-shell">
  <TerminalPanel title="privacy // open_circuit_sf">
    <Prompt text="cat privacy-policy.md" />
    <h1 class="headline">Privacy Policy</h1>
    <p>
      {APP_NAME} runs a mailing list so people can hear about upcoming workshops. This
      page explains what that list collects, why, how long it's kept, and how to leave
      it. It applies to the workshop mailing list and this website; it does not cover
      third-party services like Discord, which has its own privacy policy.
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
          'the UTM source of the link you signed up from, if any (e.g. which social post or page sent you)',
        ]}
      />
      <p class="text-muted">
        We do not collect anything beyond this at signup, and this site does not run
        third-party analytics, ad trackers, external CDNs, or email open-tracking
        pixels — nothing on this site phones home to anyone but us.
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
          'interests — so we only send you workshops you actually asked to hear about',
          'signup IP + timestamp — documented proof of opt-in consent (GDPR/CCPA)',
          'UTM source — to understand which channels bring people to the list',
        ]}
      />
    </Panel>
  </section>

  <TraceDivider />

  <section aria-labelledby="retention-h">
    <h2 id="retention-h">How long we keep it</h2>
    <Panel>
      <p>
        We keep your subscriber data for as long as you remain subscribed, plus
        <strong>[PLACEHOLDER: exact retention window after unsubscribe/erasure — e.g.
        "30 days" — not yet decided]</strong>, after which it is deleted. If you
        unsubscribe or request erasure, see "How to leave" below for what happens to
        your record.
      </p>
    </Panel>
  </section>

  <TraceDivider />

  <section aria-labelledby="consent-h">
    <h2 id="consent-h">Consent</h2>
    <Panel>
      <p>
        Signup uses double opt-in: you submit the form, then confirm via a link we
        email you. Nothing is added to the list until you confirm, and the confirming
        click's IP address and timestamp are recorded alongside it — that record is
        what "evidenced consent" (the GDPR/CCPA standard) means in practice.
      </p>
    </Panel>
  </section>

  <TraceDivider />

  <section aria-labelledby="leave-h">
    <h2 id="leave-h">How to leave</h2>
    <Panel>
      <p>
        <strong>Unsubscribe.</strong> Every email we send carries a one-click
        unsubscribe link. We honor it immediately — not just within the 10 days
        CAN-SPAM requires.
      </p>
      <p>
        <strong>Erasure.</strong> You can request that we delete your data entirely
        rather than just unsubscribing. An erasure request results in a hard delete of
        your subscriber record plus a permanent suppression entry, so the address
        can't be re-added by a future import or signup. We aim to complete erasure
        requests within
        <strong>[PLACEHOLDER: erasure turnaround time — not yet decided]</strong>.
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
        non-deceptive subject line, and a physical mailing address:
        <strong
          >[PLACEHOLDER: physical mailing address (a PO box is fine) — not yet
          established, see CLAUDE.md open item #3]</strong
        >.
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
        <a href="mailto:{CONTACT_EMAIL}">{CONTACT_EMAIL}</a>
        <span class="text-muted"
          >[PLACEHOLDER: confirm this is the right address for privacy requests
          specifically, vs. a dedicated address]</span
        >.
      </p>
      <p class="text-muted">
        {APP_NAME} is
        <strong>[PLACEHOLDER: legal entity name/type — unincorporated group, LLC,
        nonprofit, etc. — not established in the project's source material]</strong>.
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
