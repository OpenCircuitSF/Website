<!--
  One section on the workshop outreach page (#0582, #0625, #0640): the tab
  panel behind its section tab (OutreachSectionTabs). It holds the full message
  section (OutreachAnnouncement, which also owns the unwritten state and its
  Write button), then the hand-posted rows for this section (OutreachPostCards,
  #0626). The parent decides which one is open. A closed panel stays mounted
  under the hidden attribute, so typed text survives switching sections; the
  tab says when that text is not saved. The panel opens with a link down to
  the section's Video group (#0635), a button because the admin router owns
  the hash. Nothing here may set overflow or contain (#0622).
-->
<script lang="ts">
  import { onChannels, sectionHeadId, sectionPanelId, sendsItself } from '../../lib/outreachCards';
  import { handChannelLabel, sectionVideoId, videoJumpLabel } from '../../lib/outreachHandCopy';
  import type { OutreachPost, OutreachResults, OutreachWave, WorkshopOutreach } from '../../lib/types';
  import OutreachAnnouncement from './OutreachAnnouncement.svelte';
  import OutreachPostCards from './OutreachPostCards.svelte';

  interface Props {
    view: WorkshopOutreach;
    results: OutreachResults | null;
    wave: OutreachWave;
    workshopId: number;
    open: boolean;
    onUnsavedChange?: (unsaved: boolean) => void;
    onChange: (post: OutreachPost) => void;
    reload: () => void;
    /** Where the Instagram row's flyer link goes. */
    flyer?: { href: string; label: string };
  }
  let { view, results, wave, workshopId, open, onUnsavedChange, onChange, reload, flyer }: Props = $props();

  // Two sources feed the unsaved report, and neither may overwrite the other.
  let messageUnsaved = false;
  let handUnsaved = false;
  function report(): void {
    onUnsavedChange?.(messageUnsaved || handUnsaved);
  }
  const handChannels = $derived(view.channels.filter((c) => !sendsItself(c)));
  const handOn = $derived(onChannels(view).filter((c) => !sendsItself(c)));
  const written = $derived(view.posts.some((p) => p.wave === wave.name));

  const videoJump = $derived(videoJumpLabel(view, wave.name));
  // Safari has no smooth anchoring to rely on, and a real hash link would go through the admin router.
  function goToVideo(): void {
    const h = document.getElementById(sectionVideoId(wave.name));
    h?.scrollIntoView({ block: 'start' });
    h?.focus({ preventScroll: true });
  }
</script>

<div class="wave-section" id={sectionPanelId(wave.name)} role="tabpanel" aria-labelledby={sectionHeadId(wave.name)} hidden={!open}>
  {#if videoJump}<p class="video-jump"><button type="button" class="link-button" onclick={goToVideo}>{videoJump}</button></p>{/if}
  <OutreachAnnouncement {view} {results} wave={wave.name} {workshopId} {onChange} {reload} onUnsavedChange={(u) => { messageUnsaved = u; report(); }} />
  {#if handChannels.length > 0}
    {#if written}
      <OutreachPostCards {view} {results} {onChange} {reload} wave={wave.name} channelFilter={(c) => !sendsItself(c)} {flyer} onUnsavedChange={(u) => { handUnsaved = u; report(); }} />
    {:else if handOn.length > 0}
      <p class="text-muted">The text for {handOn.map((c) => handChannelLabel(c)).join(', ')} appears here once this section is written.</p>
    {/if}
  {/if}
</div>

<style>
  .video-jump {
    margin: var(--space-2) 0;
  }
  .link-button {
    background: none;
    border: none;
    color: var(--accent);
    cursor: pointer;
    text-decoration: underline;
    font-family: var(--font);
    font-size: var(--fs-base);
    padding: 0;
  }
</style>
