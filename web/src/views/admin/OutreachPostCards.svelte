<!--
  The hand-posted rows of one section on a workshop's outreach page (#0569,
  #0626): one card per channel that is posted by hand, in a Video group and a
  Post by hand group. Each card (OutreachPostCard.svelte, #0581) shows the
  full post text, its status and the actions that apply, and carries its own
  feedback line. This file owns what spans cards: the grouping, which card is
  being edited, the "discard changes" confirmation and the per-card notices.
  The automatic posts are not here: OutreachAnnouncement.svelte posts and
  schedules them (#0640). Edit opens the existing OutreachComposer inline in
  the card. No native browser dialog: every confirmation is an in-app dialog.
-->
<script lang="ts">
  import { tick, untrack } from 'svelte';
  import { handPostWhen, isVideoChannel, sectionVideoId } from '../../lib/outreachHandCopy';
  import { groupByWave, sendsItself, type CardNotice, type PostCard } from '../../lib/outreachCards';
  import type { OutreachPost, OutreachResults, WorkshopOutreach } from '../../lib/types';
  import OutreachPostCard from './OutreachPostCard.svelte';
  import OutreachConfirmDialog from './OutreachConfirmDialog.svelte';

  interface Props {
    view: WorkshopOutreach;
    results: OutreachResults | null;
    onChange: (post: OutreachPost) => void;
    /** Refetch the whole view (after a draft is created or a post is deleted). */
    reload: () => void;
    /** Where the Instagram card's flyer link goes (#0588). */
    flyer?: { href: string; label: string };
    /** The section whose hand-posted cards are listed, with no heading of its own (#0626). */
    wave: string;
    /** Whether a card's editor holds typing not saved yet (#0626). */
    onUnsavedChange?: (unsaved: boolean) => void;
  }
  let { view, results, onChange, reload, flyer, wave, onUnsavedChange }: Props = $props();

  let editingId = $state<number | null>(null);
  let pendingEditId = $state<number | null>(null);
  let notices = $state<Record<number, CardNotice | null>>({});
  // An off channel's empty card would offer a Draft button that only gets a 409 (#0628).
  const off = $derived(view.off_channels ?? []);
  const groups = $derived(
    groupByWave(view)
      .filter((g) => g.wave.name === wave)
      .map((g) => ({ ...g, cards: g.cards.filter((c) => !sendsItself(c.channel) && (c.post !== null || !off.some((o) => o.name === c.channel.name))) })),
  );

  let editorDirty = $state(false);
  $effect(() => {
    const d = editorDirty;
    untrack(() => onUnsavedChange?.(d));
    return () => untrack(() => onUnsavedChange?.(false));
  });

  /** Open the composer in place of this card's display; one card edits at a time (#0579). */
  function openEditor(p: OutreachPost): void {
    if (editingId !== null && editingId !== p.id && editorDirty) {
      pendingEditId = p.id;
      return;
    }
    editingId = p.id;
  }
  function confirmDiscard(): void {
    editingId = pendingEditId;
    pendingEditId = null;
  }
  /** Back to display mode, with focus returned to the card's Edit button (C12). */
  function closeEditor(id: number): void {
    if (editingId === id) editingId = null;
    void tick().then(() => document.querySelector<HTMLElement>(`[data-edit-for="${id}"]`)?.focus({ preventScroll: true }));
  }
  function deleted(): void {
    editingId = null;
    reload();
  }

</script>

<section class="cards" aria-label="Post by hand">
  {#snippet card(c: PostCard, wave: { label: string })}
    <OutreachPostCard
      card={c}
      waveLabel={wave.label}
      available={view.available}
      outreachId={view.outreach?.id ?? null}
      {results}
      {view}
      editing={c.post !== null && editingId === c.post.id}
      notice={c.post ? (notices[c.post.id] ?? null) : null}
      onNotice={(n) => c.post && (notices[c.post.id] = n)}
      {onChange}
      {reload}
      onEdit={openEditor}
      onCloseEditor={closeEditor}
      onDirtyChange={(d) => (editorDirty = d)}
      onDeleted={deleted}
      {flyer}
    />
  {/snippet}
  {#each groups.filter((g) => g.cards.length > 0) as group (group.wave.name)}
    {@const video = group.cards.filter((c) => isVideoChannel(c.channel))}
    {@const other = group.cards.filter((c) => !isVideoChannel(c.channel))}
    {#if video.length > 0}
      <h4 class="group" id={sectionVideoId(group.wave.name)} tabindex="-1">Video</h4>
      <p class="text-muted">One video, posted in each place by hand. {handPostWhen(view, wave)} Each has its own link.</p>
      {#each video as c (c.channel.name)}{@render card(c, group.wave)}{/each}
    {/if}
    {#if other.length > 0}
      <h4 class="group">Post by hand</h4>
      {#each other as c (c.channel.name)}{@render card(c, group.wave)}{/each}
    {/if}
  {/each}
</section>

{#if pendingEditId !== null}
  <OutreachConfirmDialog
    title="Discard your unsaved changes?"
    inFlight={false}
    errorMessage={null}
    confirmLabel="Discard changes"
    danger
    onConfirm={confirmDiscard}
    onClose={() => (pendingEditId = null)}
  >
    <p>The card you are editing has changes that have not been saved.</p>
  </OutreachConfirmDialog>
{/if}

<style>
  .group {
    margin: var(--space-4) 0 var(--space-2);
    scroll-margin-top: 7rem;
  }
  .group:focus {
    outline: none;
  }
</style>
