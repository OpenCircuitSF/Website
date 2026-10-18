<!--
  The links table on a workshop's outreach page (#0568): every tracked short
  link (one per channel x wave, one per flyer location) with its destination,
  click count and a Copy button, plus Create links / Create missing links.
  Wording and decisions are calls into lib/outreachLinks.ts; this file is
  markup and wiring. Status text goes through always-rendered role="status" /
  role="alert" elements whose content changes.
-->
<script lang="ts">
  import { untrack } from 'svelte';
  import { getWorkshopOutreachLinks, createWorkshopOutreachLinks, ApiError } from '../../lib/api';
  import { adminSettingsHash, adminWorkshopHash } from '../../lib/adminRouter';
  import { linksEmptyState, linkReportMessage, linkRowLabel, linkClicksLabel } from '../../lib/outreachLinks';
  import type { OutreachLinksView } from '../../lib/types';
  import Button from '../../lib/Button.svelte';

  interface Props {
    workshopId: number;
    available: boolean;
    reloadKey?: number;
    /** #0569: the parent's "Tracked links (N)" summary shows the count. */
    onCount?: (n: number) => void;
  }
  let { workshopId, available, reloadKey = 0, onCount }: Props = $props();

  let view = $state<OutreachLinksView | null>(null);
  let loadError = $state<string | null>(null);
  let busy = $state(false);
  let reportMessage = $state<string | null>(null);
  let actionError = $state<string | null>(null);
  let copied = $state('');

  const empty = $derived(view ? linksEmptyState(view) : { kind: null, message: '' });

  function message(err: unknown, fallback: string): string {
    return err instanceof ApiError ? err.message : fallback;
  }

  async function load(): Promise<void> {
    loadError = null;
    try {
      view = await getWorkshopOutreachLinks(workshopId);
      onCount?.(view.links.length);
    } catch (err) {
      loadError = message(err, 'Could not load the links. Please try again.');
    }
  }

  $effect(() => {
    void workshopId;
    void reloadKey;
    untrack(() => void load());
  });

  async function create(): Promise<void> {
    busy = true;
    actionError = null;
    reportMessage = null;
    try {
      const v = await createWorkshopOutreachLinks(workshopId);
      view = v;
      onCount?.(v.links.length);
      reportMessage = linkReportMessage(v.report);
    } catch (err) {
      actionError = message(err, 'Could not create the links. Please try again.');
    } finally {
      busy = false;
    }
  }

  async function copy(url: string): Promise<void> {
    try {
      await navigator.clipboard.writeText(url);
      copied = `Copied ${url}`;
    } catch {
      copied = 'Could not copy. Select the link and copy it by hand.';
    }
  }
</script>

<section class="links" aria-labelledby="outreach-links-heading">
  <h3 id="outreach-links-heading">Links</h3>
  {#if loadError}
    <p class="text-error">{loadError}</p>
    <Button onclick={load}>Retry</Button>
  {:else if view}
    {#if empty.message}
      <p class="text-muted">
        {empty.message}
        {#if empty.kind === 'not_connected'}
          <a href={adminSettingsHash('outreach')}>Open Settings</a>
        {:else if empty.kind === 'not_published'}
          <a href={adminWorkshopHash(workshopId)}>Open the workshop editor</a>
        {/if}
      </p>
    {/if}
    {#if empty.kind === 'no_links' || empty.kind === 'some_missing'}
      <div class="row">
        <Button variant="primary" onclick={create} disabled={busy || !available}>
          {busy ? 'Creating…' : empty.kind === 'no_links' ? 'Create links' : 'Create missing links'}
        </Button>
      </div>
    {/if}
    {#if view.clicks_status !== 'ok' && view.clicks_status !== 'not_started'}
      <p class="text-muted">Click counts are not available right now.</p>
    {/if}
    <div class="table-scroll">
      <table>
        <thead>
          <tr>
            <th>Channel / location</th>
            <th>Post</th>
            <th>Short URL</th>
            <th>Destination</th>
            <th>Clicks</th>
            <th><span class="visually-hidden">Copy</span></th>
          </tr>
        </thead>
        <tbody>
          {#each view.links as row (row.kind + ':' + row.channel + ':' + row.wave + ':' + (row.location_id ?? 0))}
            <tr>
              <td>{linkRowLabel(row)}</td>
              <td>{row.wave_label}</td>
              <td class="mono">{#if row.short_url}{row.short_url}{:else}<span class="text-muted">missing</span>{/if}</td>
              <td class="mono dest">{row.destination_url}</td>
              <td>{linkClicksLabel(row, view.clicks_status)}</td>
              <td>
                {#if row.short_url}
                  <Button aria-label="Copy short URL for {linkRowLabel(row)} {row.wave_label}" onclick={() => copy(row.short_url ?? '')}>Copy</Button>
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {:else}
    <p class="text-muted">Loading links…</p>
  {/if}
  <p class="text-muted" role="status">{copied}</p>
  <p class="text-error" role="alert">{actionError ?? reportMessage ?? ''}</p>
</section>

<style>
  .mono {
    font-family: var(--font-mono);
  }
  .dest {
    overflow-wrap: anywhere;
    min-width: 16rem;
  }
  .visually-hidden {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip: rect(0 0 0 0);
    white-space: nowrap;
  }
  [role='status']:empty,
  [role='alert']:empty {
    display: none;
  }
</style>
