<!--
  One post card (#0569, extracted in #0581): the post text as it will go out,
  one plain status line and the actions that apply. Post now, Retry and Mark
  posted open an in-app confirmation (OutreachPostDialog) instead of a
  native browser dialog. Each card carries its own always-rendered live regions
  (role="status" / role="alert") at the bottom, so feedback appears next to
  the button that was pressed; a notice is only shown while the post is still
  in the state it was written for (lib/outreachCards.ts visibleNotice). A
  queued post shows a CSS spinner. Copy text / Copy link flip to "Copied" for
  two seconds. The notice itself is held by the list so a both-networks post
  can write to two cards.
-->
<script lang="ts">
  import { onDestroy } from 'svelte';
  import {
    createOutreachPost,
    sendOutreachPost,
    unscheduleOutreachPost,
    markOutreachPosted,
    ApiError,
  } from '../../lib/api';
  import { countCharacters, isOverLimit } from '../../lib/outreach';
  import {
    cardButtons,
    copyLabel,
    statusLine,
    visibleNotice,
    type CardNotice,
    type PostCard,
    type PostDialogMode,
  } from '../../lib/outreachCards';
  import { cellClicksLabel, cellReactionsLabel } from '../../lib/outreachResults';
  import { isSafeLinkHref } from '../../lib/linkSafety';
  import { adminOutreachFlyersHash } from '../../lib/adminRouter';
  import type { OutreachPost, OutreachResults } from '../../lib/types';
  import Button from '../../lib/Button.svelte';
  import OutreachComposer from './OutreachComposer.svelte';
  import OutreachPostDialog from './OutreachPostDialog.svelte';

  interface Props {
    card: PostCard;
    waveLabel: string;
    available: boolean;
    outreachId: number | null;
    results: OutreachResults | null;
    editing: boolean;
    notice: CardNotice | null;
    onNotice: (n: CardNotice | null) => void;
    onChange: (post: OutreachPost) => void;
    reload: () => void;
    onEdit: (post: OutreachPost) => void;
    onCloseEditor: (id: number) => void;
    onDirtyChange: (dirty: boolean) => void;
    onDeleted: () => void;
  }
  let {
    card: c,
    waveLabel,
    available,
    outreachId,
    results,
    editing,
    notice,
    onNotice,
    onChange,
    reload,
    onEdit,
    onCloseEditor,
    onDirtyChange,
    onDeleted,
  }: Props = $props();

  const p = $derived(c.post);
  const status = $derived(statusLine(p, c.channel));
  const btn = $derived(cardButtons(p, c.channel, available));
  const counted = $derived(p ? countCharacters(c.channel, p.body, p.prepared) : null);

  let busy = $state(false);
  let error = $state('');
  let copied = $state<'text' | 'link' | null>(null);
  let dialog = $state<PostDialogMode | null>(null);
  let dialogError = $state<string | null>(null);
  let copyTimer: ReturnType<typeof setTimeout> | undefined;

  onDestroy(() => clearTimeout(copyTimer));

  function message(err: unknown, fallback: string): string {
    return err instanceof ApiError ? err.message : fallback;
  }

  async function run(label: string, fn: () => Promise<void>): Promise<void> {
    busy = true;
    error = '';
    try {
      await fn();
    } catch (err) {
      error = message(err, `Could not ${label}. Please try again.`);
    } finally {
      busy = false;
    }
  }

  const draft = () =>
    run('create the draft', async () => {
      if (outreachId === null) return;
      await createOutreachPost(outreachId, c.channel.name, c.wave.name);
      reload();
    });

  const unschedule = (post: OutreachPost) =>
    run('unschedule', async () => {
      const updated = await unscheduleOutreachPost(post.id);
      onChange(updated);
      onNotice({ text: 'Back to draft.', kind: 'status', status: updated.status });
    });

  function openDialog(mode: PostDialogMode): void {
    dialogError = null;
    dialog = mode;
  }
  function closeDialog(): void {
    if (!busy) dialog = null;
  }

  async function confirmDialog(remoteUrl?: string): Promise<void> {
    if (!p || !dialog) return;
    const post = p;
    const mode = dialog;
    busy = true;
    dialogError = null;
    try {
      if (mode === 'markPosted') {
        const updated = await markOutreachPosted(post.id, remoteUrl);
        onChange(updated);
        onNotice({ text: 'Marked as posted.', kind: 'status', status: updated.status });
      } else {
        const updated = await sendOutreachPost(post.id);
        onChange(updated);
        onNotice({ text: 'Queued. It posts within a few seconds; this page updates when it does.', kind: 'status', status: updated.status });
      }
      dialog = null;
    } catch (err) {
      dialogError = message(err, mode === 'markPosted' ? 'Could not mark posted. Please try again.' : 'Could not post. Please try again.');
    } finally {
      busy = false;
    }
  }

  async function copy(kind: 'text' | 'link', value: string): Promise<void> {
    await run(kind === 'text' ? 'copy' : 'copy the link', async () => {
      await navigator.clipboard.writeText(value);
      copied = kind;
      const n: CardNotice = { text: kind === 'text' ? 'Copied.' : 'Link copied.', kind: 'status', status: p?.status ?? null };
      onNotice(n);
      clearTimeout(copyTimer);
      copyTimer = setTimeout(() => {
        copied = null;
        if (notice === n) onNotice(null);
      }, 2000);
    });
  }
</script>

<article class="card" aria-label="{c.channel.label} {waveLabel}">
  <h5 class="card-title">{c.channel.label} &middot; {waveLabel}{p?.stale ? ' (stale)' : ''}</h5>
  {#if p && editing}
    {#key p.id}
      <OutreachComposer
        post={p}
        channel={c.channel}
        wave={c.wave}
        {available}
        {onChange}
        {onDeleted}
        onClose={() => onCloseEditor(p.id)}
        {onDirtyChange}
      />
    {/key}
  {:else}
    {#if p}
      <pre class="body">{p.body}</pre>
      {#if counted}
        {@const over = isOverLimit(c.channel, counted.count)}
        <p class={over ? 'text-error' : 'text-muted'}>
          {counted.exact ? '' : '≈ '}{counted.count}{c.channel.char_limit > 0 ? ` / ${c.channel.char_limit}` : ''} characters{over ? ' — over the limit' : ''}
        </p>
      {/if}
    {/if}
    <p class="status status-{status.kind}">
      {#if status.kind === 'queued'}<span class="spinner" aria-hidden="true"></span>{/if}
      {status.text}{#if status.kind === 'queued'}{' '}usually under ten seconds{/if}{#if status.kind === 'posted' && status.href && isSafeLinkHref(status.href)}{' · '}<a href={status.href} rel="noopener noreferrer">view post</a>{/if}
    </p>
    {#if p && cellClicksLabel(p, results)}<p class="text-muted">{cellClicksLabel(p, results)}</p>{/if}
    {#if p && cellReactionsLabel(p, results)}<p class="text-muted">{cellReactionsLabel(p, results)}</p>{/if}
    {#if p && p.short_url && !c.channel.automatic}
      <p class="link-row">
        <span class="link-label">Link to paste</span>
        <span class="mono short">{p.short_url}</span>
        <Button onclick={() => copy('link', p.short_url ?? '')} disabled={busy}>{copyLabel('Copy link', copied === 'link')}</Button>
      </p>
    {/if}
    <div class="row">
      {#if btn.draft}
        <Button variant="primary" onclick={draft} disabled={busy}>Draft</Button>
      {/if}
      {#if p}
        {#if btn.postNow}
          <Button variant="primary" onclick={() => openDialog('post')} disabled={busy}>Post now</Button>
        {/if}
        {#if btn.retry}
          <Button variant="primary" onclick={() => openDialog('retry')} disabled={busy}>Retry</Button>
        {/if}
        {#if btn.schedule}
          <Button onclick={() => onEdit(p)} disabled={busy}>Schedule…</Button>
        {/if}
        {#if btn.unschedule}
          <Button onclick={() => unschedule(p)} disabled={busy}>Unschedule</Button>
        {/if}
        {#if btn.copy}
          <Button onclick={() => copy('text', p.body)} disabled={busy}>{copyLabel('Copy text', copied === 'text')}</Button>
        {/if}
        {#if btn.copy && c.channel.name === 'instagram'}
          <a class="open-flyer" href={adminOutreachFlyersHash()}>Open flyer</a>
        {/if}
        {#if btn.markPosted}
          <Button onclick={() => openDialog('markPosted')} disabled={busy}>Mark posted</Button>
        {/if}
        {#if btn.edit}
          <Button onclick={() => onEdit(p)} data-edit-for={p.id}>Edit</Button>
        {/if}
      {/if}
    </div>
  {/if}
  <p class="notice {notice?.kind === 'error' ? 'text-error' : 'text-muted'}" role="status">{visibleNotice(notice, p)}</p>
  <p class="text-error" role="alert">{error}</p>
</article>
{#if dialog && p}
  <OutreachPostDialog
    mode={dialog}
    items={[{ channel: c.channel, post: p }]}
    inFlight={busy}
    errorMessage={dialogError}
    onConfirm={confirmDialog}
    onClose={closeDialog}
  />
{/if}

<style>
  .card {
    border: var(--border-w) solid var(--border);
    border-radius: var(--radius);
    padding: var(--space-3);
    margin-bottom: var(--space-3);
  }
  .card-title {
    margin: 0 0 var(--space-2);
    font-size: var(--fs-base);
  }
  .body {
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    font-family: var(--font);
    margin: 0 0 var(--space-2);
  }
  .link-row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-2);
    margin: 0 0 var(--space-2);
  }
  .link-label {
    font-weight: 600;
  }
  .mono {
    font-family: var(--font-mono);
    overflow-wrap: anywhere;
  }
  .status {
    font-weight: 600;
    margin: var(--space-2) 0;
  }
  .status-failed {
    color: var(--error, inherit);
  }
  .spinner {
    display: inline-block;
    width: 0.9em;
    height: 0.9em;
    margin-right: var(--space-1);
    border: 2px solid var(--border);
    border-top-color: var(--accent);
    border-radius: 50%;
    vertical-align: -0.1em;
    animation: spin 0.9s linear infinite;
  }
  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .spinner {
      animation: none;
    }
  }
  [role='status']:empty,
  [role='alert']:empty {
    display: none;
  }
</style>
