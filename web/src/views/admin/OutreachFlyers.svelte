<!--
  The Outreach tab's Flyers sub-view (#0549): every flyer, and a form to start
  a new one for a workshop that already has outreach. Designing a flyer needs
  no ShortLinks; only Generate does. Status text lands in the always-rendered
  role="status" line.
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { listOutreachFlyers, listOutreach, createOutreachFlyer, ApiError } from '../../lib/api';
  import { adminOutreachFlyersHash } from '../../lib/adminRouter';
  import { formatDateTime } from '../../lib/admin';
  import { FLYER_TEMPLATE_LABELS } from '../../lib/flyers';
  import type { FlyerRow, OutreachSummary } from '../../lib/types';
  import Button from '../../lib/Button.svelte';
  import Panel from '../../lib/Panel.svelte';

  let flyers = $state<FlyerRow[]>([]);
  let workshops = $state<OutreachSummary[]>([]);
  let loaded = $state(false);
  let status = $state('Loading flyers.');
  let outreachId = $state<number | null>(null);
  let creating = $state(false);

  function message(err: unknown, fallback: string): string {
    return err instanceof ApiError ? err.message : fallback;
  }

  async function load(): Promise<void> {
    try {
      const [f, o] = await Promise.all([listOutreachFlyers(), listOutreach()]);
      flyers = f.flyers;
      workshops = o.outreach;
      outreachId = outreachId ?? workshops[0]?.outreach_id ?? null;
      status = '';
    } catch (err) {
      status = message(err, 'Could not load flyers. Please try again.');
    } finally {
      loaded = true;
    }
  }

  async function create(e: Event): Promise<void> {
    e.preventDefault();
    if (outreachId === null || creating) return;
    creating = true;
    status = '';
    try {
      const v = await createOutreachFlyer({ outreach_id: outreachId });
      window.location.hash = adminOutreachFlyersHash(v.flyer.id);
    } catch (err) {
      status = message(err, 'Could not create the flyer. Please try again.');
    } finally {
      creating = false;
    }
  }

  onMount(load);
</script>

<Panel title="Flyers">
  <p class="text-muted">
    Printable flyers with one QR code per location. Start outreach for a workshop first, then design its flyer here.
  </p>
  <p class="status" role="status">{status}</p>

  {#if loaded && workshops.length === 0}
    <p class="text-muted">Start outreach for a workshop first.</p>
  {:else if loaded}
    <form class="new" onsubmit={create}>
      <label for="flyer-workshop">Workshop</label>
      <select id="flyer-workshop" bind:value={outreachId}>
        {#each workshops as w (w.outreach_id)}
          <option value={w.outreach_id}>{w.workshop_title}</option>
        {/each}
      </select>
      <Button type="submit" variant="primary" disabled={creating || outreachId === null}>New flyer</Button>
    </form>
  {/if}

  {#if loaded && flyers.length === 0 && workshops.length > 0}
    <p class="text-muted">No flyers yet.</p>
  {:else if flyers.length > 0}
    <div class="table-scroll">
      <table>
        <thead>
          <tr>
            <th>Workshop</th>
            <th>Template</th>
            <th>Tagline</th>
            <th>Created</th>
          </tr>
        </thead>
        <tbody>
          {#each flyers as f (f.id)}
            <tr>
              <td><a href={adminOutreachFlyersHash(f.id)}>{f.workshop_title}</a></td>
              <td>{FLYER_TEMPLATE_LABELS[f.template]}</td>
              <td>{f.tagline || '—'}</td>
              <td>{formatDateTime(f.created_at)}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</Panel>

<style>
  .status {
    min-height: 1.5em;
    margin: 0 0 var(--space-2);
    color: var(--text-muted);
  }
  .new {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-2);
    margin: var(--space-3) 0;
  }
</style>
