<!--
  One section on the workshop outreach page (#0582, #0625): the pinned strip
  with its number, label and status (#0622), and under it the full message
  section (OutreachAnnouncement), then the hand-posted rows for this section
  (OutreachPostCards, #0626). The parent decides which one is open. A closed
  panel stays mounted under the hidden attribute, so typed text survives
  switching sections, and this row says in words when that text is not saved.
  "Write it" sits beside the strip, not inside its button; it drafts just this
  section and then selects it. The sticky strip is a direct child of the wrapper
  that also holds the panel, so it pins for the panel's height. Neither may set
  overflow or contain, which would stop the pin (#0622).
-->
<script lang="ts">
  import { draftAllOutreach, ApiError } from '../../lib/api';
  import { sectionHeadId, sectionPanelId, sectionRowId, sendsItself } from '../../lib/outreachCards';
  import { handChannelLabel } from '../../lib/outreachHandCopy';
  import { waveStrip } from '../../lib/outreachMessage';
  import type { OutreachPost, OutreachResults, OutreachWave, WorkshopOutreach } from '../../lib/types';
  import Button from '../../lib/Button.svelte';
  import OutreachAnnouncement from './OutreachAnnouncement.svelte';
  import OutreachPostCards from './OutreachPostCards.svelte';
  import OutreachWaveStrip from './OutreachWaveStrip.svelte';

  interface Props {
    view: WorkshopOutreach;
    results: OutreachResults | null;
    wave: OutreachWave;
    workshopId: number;
    open: boolean;
    onSelect: () => void;
    onUnsavedChange?: (unsaved: boolean) => void;
    onChange: (post: OutreachPost) => void;
    reload: () => void;
    /** Where the Instagram row's flyer link goes. */
    flyer?: { href: string; label: string };
  }
  let { view, results, wave, workshopId, open, onSelect, onUnsavedChange, onChange, reload, flyer }: Props = $props();

  let busy = $state(false);
  let errorText = $state('');
  let unsaved = $state(false);

  const strip = $derived(waveStrip(view, wave.name));

  // Two sources feed `unsaved`, and neither may overwrite the other.
  let messageUnsaved = false;
  let handUnsaved = false;
  function report(): void {
    unsaved = messageUnsaved || handUnsaved;
    onUnsavedChange?.(unsaved);
  }
  const handChannels = $derived(view.channels.filter((c) => !sendsItself(c)));
  const written = $derived(view.posts.some((p) => p.wave === wave.name));

  async function write(): Promise<void> {
    busy = true;
    errorText = '';
    try {
      await draftAllOutreach(workshopId, [wave.name]);
      reload();
      onSelect();
    } catch (err) {
      errorText = err instanceof ApiError ? err.message : 'Could not write the posts. Please try again.';
    } finally {
      busy = false;
    }
  }
</script>

<div class="wave-row" id={sectionRowId(wave.name)}>
  <div class="wave-pin row-head">
    <OutreachWaveStrip {strip} {open} headId={sectionHeadId(wave.name)} panelId={sectionPanelId(wave.name)} {onSelect} />
    {#if strip.action === 'write' && wave.name !== 'announce'}
      <Button onclick={write} disabled={busy || !view.available}>Write it</Button>
    {/if}
  </div>
  <div id={sectionPanelId(wave.name)} hidden={!open}>
    {#if strip.action === 'open' || wave.name === 'announce'}
      <OutreachAnnouncement {view} {results} wave={wave.name} {workshopId} {onChange} {reload} onUnsavedChange={(u) => { messageUnsaved = u; report(); }} />
    {:else}
      <p class="text-muted">Nothing written for this one yet.</p>
    {/if}
    {#if handChannels.length > 0}
      {#if written}
        <OutreachPostCards {view} {results} {onChange} {reload} wave={wave.name} channelFilter={(c) => !sendsItself(c)} {flyer} onUnsavedChange={(u) => { handUnsaved = u; report(); }} />
      {:else}
        <p class="text-muted">The text for {handChannels.map((c) => handChannelLabel(c)).join(', ')} appears here once this section is written.</p>
      {/if}
    {/if}
  </div>
  <p class="text-warn unsaved-note" role="status">{!open && unsaved ? 'Your changes here are not saved yet. Open this section to save them.' : ''}</p>
  <p class="text-error" role="alert">{errorText}</p>
</div>

<style>
  .row-head {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-start;
    justify-content: space-between;
    gap: var(--space-2);
  }
  .unsaved-note:empty {
    display: none;
  }
  [role='alert']:empty {
    display: none;
  }
</style>
