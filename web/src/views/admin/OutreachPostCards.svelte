<!--
  The posts list on a workshop's outreach page (#0569): one card per channel
  x wave, grouped by wave. Each card (OutreachPostCard.svelte, #0581) shows the
  full post text as it will be posted, one plain status line and the actions
  that apply, and carries its own feedback line. This file owns what spans
  cards: the grouping, which card is being edited, the "discard changes"
  confirmation, the Announce "Post to Bluesky and Mastodon" button with its one
  confirmation, and the per-card notices. Edit opens the existing
  OutreachComposer inline in the card. No native browser dialog: every confirmation is
  an in-app dialog.
-->
<script lang="ts">
  import { tick } from 'svelte';
  import { sendOutreachPost, ApiError } from '../../lib/api';
  import {
    combinedPostResult,
    groupByWave,
    postableAutomatic,
    type CardNotice,
    type PostCard,
  } from '../../lib/outreachCards';
  import type { OutreachChannel, OutreachPost, OutreachResults, WorkshopOutreach } from '../../lib/types';
  import Button from '../../lib/Button.svelte';
  import OutreachPostCard from './OutreachPostCard.svelte';
  import OutreachPostDialog from './OutreachPostDialog.svelte';
  import OutreachConfirmDialog from './OutreachConfirmDialog.svelte';

  interface Props {
    view: WorkshopOutreach;
    results: OutreachResults | null;
    onChange: (post: OutreachPost) => void;
    /** Refetch the whole view (after a draft is created or a post is deleted). */
    reload: () => void;
  }
  let { view, results, onChange, reload }: Props = $props();

  let editingId = $state<number | null>(null);
  let pendingEditId = $state<number | null>(null);
  let notices = $state<Record<number, CardNotice | null>>({});
  let listStatus = $state('');
  let bothOpen = $state(false);
  let bothBusy = $state(false);
  let bothError = $state<string | null>(null);
  // Fixed when the dialog opens: each send updates the view, which would shrink the live list mid-flight.
  let bothSnapshot = $state<{ post: OutreachPost; channel: OutreachChannel }[]>([]);

  const groups = $derived(groupByWave(view));
  const bothItems = $derived(postableAutomatic(view, 'announce'));
  const bothLabel = $derived(`Post to ${bothItems.map((i) => i.channel.label).join(' and ')}`);

  /** TikTok and YouTube need a video, so their cards sit under Video channels. */
  const isVideo = (c: PostCard): boolean => c.channel.utm_medium === 'video';

  let editorDirty = false;

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

  function openBoth(): void {
    bothSnapshot = bothItems;
    bothError = null;
    bothOpen = true;
  }

  /** Post both Announce cards from one dialog, one after the other, reporting both on one line. */
  async function postBoth(): Promise<void> {
    bothBusy = true;
    bothError = null;
    const outcomes: { label: string; ok: boolean; error?: string }[] = [];
    const next: Record<number, CardNotice | null> = { ...notices };
    for (const { post, channel } of bothSnapshot) {
      try {
        const updated = await sendOutreachPost(post.id);
        onChange(updated);
        outcomes.push({ label: channel.label, ok: true });
        next[post.id] = { text: 'Queued. It posts within a few seconds; this page updates when it does.', kind: 'status', status: updated.status };
      } catch (err) {
        const reason = err instanceof ApiError ? err.message : 'please try again';
        outcomes.push({ label: channel.label, ok: false, error: reason });
        next[post.id] = { text: `Could not post: ${reason}`, kind: 'error', status: post.status };
      }
    }
    notices = next;
    listStatus = combinedPostResult(outcomes);
    bothBusy = false;
    bothOpen = false;
  }
</script>

<section class="cards" aria-labelledby="outreach-posts-heading">
  <h3 id="outreach-posts-heading">Posts</h3>
  <!-- The both-networks result (D6). Always rendered (a persistent live
       region), and at the top, beside the Announce heading and its button,
       not after the last card (C5, M3). -->
  <p class="text-muted" role="status">{listStatus}</p>
  {#snippet card(c: PostCard, wave: { label: string })}
    <OutreachPostCard
      card={c}
      waveLabel={wave.label}
      available={view.available}
      outreachId={view.outreach?.id ?? null}
      {results}
      editing={c.post !== null && editingId === c.post.id}
      notice={c.post ? (notices[c.post.id] ?? null) : null}
      onNotice={(n) => c.post && (notices[c.post.id] = n)}
      {onChange}
      {reload}
      onEdit={openEditor}
      onCloseEditor={closeEditor}
      onDirtyChange={(d) => (editorDirty = d)}
      onDeleted={deleted}
    />
  {/snippet}
  {#each groups as group (group.wave.name)}
    <h4 class="wave">{group.wave.label}</h4>
    {#if group.wave.name === 'announce' && bothItems.length >= 2}
      <p class="row">
        <Button variant="primary" onclick={openBoth}>{bothLabel}</Button>
      </p>
    {/if}
    {#each group.cards.filter((c) => !isVideo(c)) as c (c.channel.name)}
      {@render card(c, group.wave)}
    {/each}
    {#if group.cards.some(isVideo)}
      <details class="video">
        <summary>Video channels ({group.wave.label})</summary>
        {#each group.cards.filter(isVideo) as c (c.channel.name)}
          {@render card(c, group.wave)}
        {/each}
      </details>
    {/if}
  {/each}
</section>

{#if bothOpen}
  <OutreachPostDialog
    mode="post"
    items={bothSnapshot}
    inFlight={bothBusy}
    errorMessage={bothError}
    onConfirm={() => void postBoth()}
    onClose={() => !bothBusy && (bothOpen = false)}
  />
{/if}
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
  .wave {
    margin: var(--space-4) 0 var(--space-2);
  }
  .video {
    margin-bottom: var(--space-3);
  }
  [role='status']:empty {
    display: none;
  }
</style>
