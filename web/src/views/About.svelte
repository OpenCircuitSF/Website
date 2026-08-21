<script lang="ts">
  // About view (#0016, PRD §5.1): who Open Circuit SF is, how workshops run,
  // how venues work, how to get involved, and contact. Composed entirely from
  // the existing motif/layout components (TerminalPanel, Prompt, StatusList,
  // Panel, TraceDivider) rather than one-off page-specific styling, per the
  // acceptance criteria.
  //
  // Copy notes (flagged rather than invented where uncertain): "monthly" cadence
  // is sourced directly from PRD §4.1's own reference layout example
  // ("[ OK ] monthly · beginners"), not invented here. The Discord invite and
  // "hosted anywhere, including a neighborhood garage" framing are carried
  // over verbatim from placeholder/index.html, the validated reference copy.
  // Nothing else about internal group structure, founding date, or specific
  // policies is asserted -- if the group has more specific facts (e.g. an
  // exact cadence, who to contact for what), replace the relevant paragraph
  // rather than treating this copy as final.
  import TerminalPanel from '../lib/TerminalPanel.svelte';
  import Prompt from '../lib/Prompt.svelte';
  import StatusList from '../lib/StatusList.svelte';
  import Panel from '../lib/Panel.svelte';
  import TraceDivider from '../lib/TraceDivider.svelte';
  import { APP_NAME } from '../lib/branding';
  import { DISCORD_URL, LUMA_URL } from '../lib/links';

  const CONTACT_EMAIL = 'hello@opencircuitsf.com';
</script>

<main id="main-content" class="app-shell about-shell">
  <TerminalPanel title="about // open_circuit_sf">
    <Prompt text="whoami" />
    <h1 class="headline">About {APP_NAME}</h1>
    <p>
      {APP_NAME} is a San Francisco group running hands-on electronics workshops —
      soldering, microcontrollers, homelab, home automation, and whatever the room
      wants to build next. No experience required, and no membership either — if a
      session sounds interesting, show up.
    </p>
  </TerminalPanel>

  <TraceDivider />

  <section aria-labelledby="how-h">
    <h2 id="how-h">How workshops run</h2>
    <Panel>
      <StatusList
        items={[
          'roughly monthly, beginner-friendly by default',
          'tools and parts provided — bring curiosity, not a kit',
          'already know the material? bring your own project and build alongside everyone else',
          'announced through Discord, the Luma calendar, and the mailing list',
        ]}
      />
    </Panel>
  </section>

  <TraceDivider />

  <section aria-labelledby="venues-h">
    <h2 id="venues-h">Venues</h2>
    <Panel>
      <p>
        {APP_NAME} doesn't have a home base. A workshop might happen in a makerspace,
        a co-working room, or someone's garage — the location moves depending on
        what's available and who's hosting that month. Wherever it lands, tools and a
        soldering-safe table are the only requirements.
      </p>
    </Panel>
  </section>

  <TraceDivider />

  <section aria-labelledby="involved-h">
    <h2 id="involved-h">Get involved</h2>
    <Panel>
      <p>
        The easiest way in is Discord — that's where locations get shared and
        people organize builds between sessions. Upcoming sessions are listed
        on the Luma calendar, where you can RSVP directly.
      </p>
      <p>
        <a href={DISCORD_URL} target="_blank" rel="noopener noreferrer">Join the Discord</a>
        &middot;
        <a href={LUMA_URL} target="_blank" rel="noopener noreferrer">View the calendar</a>
      </p>
      <p class="text-muted">
        An interest-based workshop mailing list is on the way, for anyone who'd rather
        get an email than watch Discord.
      </p>
    </Panel>
  </section>

  <TraceDivider />

  <section aria-labelledby="contact-h">
    <h2 id="contact-h">Contact</h2>
    <Panel>
      <p>
        Reach the group directly at
        <a href="mailto:{CONTACT_EMAIL}">{CONTACT_EMAIL}</a>.
      </p>
    </Panel>
  </section>
</main>

<style>
  .about-shell {
    display: flex;
    flex-direction: column;
    gap: var(--space-6);
  }

  .headline {
    margin: var(--space-3) 0 var(--space-4);
    font-size: var(--fs-xl);
    font-weight: 700;
  }

  .about-shell section h2 {
    font-size: var(--fs-lg);
    margin: 0 0 var(--space-3);
  }

  .about-shell p {
    max-width: var(--measure);
    margin: 0 0 var(--space-3);
  }
  .about-shell p:last-child {
    margin-bottom: 0;
  }
</style>
