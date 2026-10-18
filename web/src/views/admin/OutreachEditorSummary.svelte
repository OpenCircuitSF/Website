<!--
  The workshop editor's outreach summary (#0568): one line of state (the same
  summaryLine as the outreach page, never the campaign slug, #0582) and a
  prominent link to the workshop's outreach page.
-->
<script lang="ts">
  import { untrack } from 'svelte';
  import { getWorkshopOutreach, ApiError } from '../../lib/api';
  import { adminOutreachWorkshopHash } from '../../lib/adminRouter';
  import { unavailableMessage } from '../../lib/outreach';
  import { editorOutreachLinkLabel } from '../../lib/outreachList';
  import { summaryLine } from '../../lib/outreachCards';
  import type { WorkshopOutreach } from '../../lib/types';

  // workshopUpdatedAt changes when the editor saves (e.g. publishes), so the
  // summary line refetches instead of going stale.
  let { workshopId, workshopUpdatedAt = '' }: { workshopId: number; workshopUpdatedAt?: string } = $props();

  let view = $state<WorkshopOutreach | null>(null);
  let loadError = $state<string | null>(null);

  async function load(): Promise<void> {
    loadError = null;
    try {
      view = await getWorkshopOutreach(workshopId);
    } catch (err) {
      loadError = err instanceof ApiError ? err.message : 'Could not load outreach. Please try again.';
    }
  }

  $effect(() => {
    void workshopId;
    void workshopUpdatedAt;
    untrack(() => void load());
  });

  const linkLabel = $derived(
    editorOutreachLinkLabel(
      view?.outreach != null,
      view?.posts.filter((p) => p.status === 'posted').length ?? 0,
    ),
  );

  const line = $derived.by(() => {
    if (!view) return 'Loading outreach…';
    if (view.outreach === null) return view.available ? 'Outreach has not started.' : unavailableMessage(view.unavailable_reason);
    return summaryLine(view, null);
  });
</script>

<div class="summary">
  {#if loadError}
    <p class="text-error">{loadError}</p>
  {:else}
    <p class="text-muted">{line}</p>
  {/if}
  <a class="open-outreach" href={adminOutreachWorkshopHash(workshopId)}>{linkLabel}</a>
</div>

<style>
  .open-outreach {
    display: inline-block;
    font-family: var(--font);
    font-size: var(--fs-base);
    padding: var(--space-2) var(--space-3);
    border: var(--border-w) solid var(--accent);
    border-radius: var(--radius);
    background: var(--accent);
    color: var(--accent-text);
    text-decoration: none;
  }
  .open-outreach:hover {
    background: var(--accent-hover);
    border-color: var(--accent-hover);
  }
</style>
