<!--
  One post card (#0569, extracted in #0581): the post text as it will go out,
  one plain status line and the actions that apply. Post now, Retry and Mark
  posted open an in-app confirmation (OutreachPostDialog) instead of a
  native browser dialog. Each card carries its own always-rendered live regions
  (role="status" / role="alert") at the bottom, so feedback appears next to
  the button that was pressed; a notice is only shown while the post is still
  in the state it was written for (lib/outreachCards.ts visibleNotice). A
  queued post shows a CSS spinner. A hand-posted row (#0626) offers Copy with
  link, Copy text only and Copy link; each flips to "Copied" for two seconds,
  and when the browser refuses, the exact text is selected in a box instead. The
  notice itself is held by the list so a both-networks post can write to two
  cards.
-->
<script lang="ts">
  import { onDestroy, tick, untrack } from 'svelte';
  import {
    createOutreachPost,
    sendOutreachPost,
    unscheduleOutreachPost,
    prepareOutreachPost,
    markOutreachPosted,
    unmarkOutreachPosted,
    takedownOutreachPost,
    ApiError,
  } from '../../lib/api';
  import { composerActions } from '../../lib/outreach';
  import { handChannelLabel, handCopies, handLinkLabel, isVideoChannel, writeClipboard } from '../../lib/outreachHandCopy';
  import { copyablePostLink } from '../../lib/outreachPreview';
  import {
    cardButtons,
    copyLabel,
    sendsItself,
    statusLine,
    visibleNotice,
    type CardNotice,
    type PostCard,
    type PostDialogMode,
  } from '../../lib/outreachCards';
  import { cellClicksLabel, cellReactionsLabel } from '../../lib/outreachResults';
  import { isSafeLinkHref } from '../../lib/linkSafety';
  import { adminOutreachFlyersHash } from '../../lib/adminRouter';
  import type { OutreachPost, OutreachResults, WorkshopOutreach } from '../../lib/types';
  import Button from '../../lib/Button.svelte';
  import OutreachComposer from './OutreachComposer.svelte';
  import OutreachPostDialog from './OutreachPostDialog.svelte';
  import OutreachPostPreview from './OutreachPostPreview.svelte';

  interface Props {
    card: PostCard;
    waveLabel: string;
    available: boolean;
    outreachId: number | null;
    results: OutreachResults | null;
    view: WorkshopOutreach;
    editing: boolean;
    notice: CardNotice | null;
    onNotice: (n: CardNotice | null) => void;
    onChange: (post: OutreachPost) => void;
    reload: () => void;
    onEdit: (post: OutreachPost) => void;
    onCloseEditor: (id: number) => void;
    onDirtyChange: (dirty: boolean) => void;
    onDeleted: () => void;
    /** Where Open flyer goes on the Instagram card (#0588); defaults to the flyers list. */
    flyer?: { href: string; label: string };
  }
  let {
    card: c,
    waveLabel,
    available,
    outreachId,
    results,
    view,
    editing,
    notice,
    onNotice,
    onChange,
    reload,
    onEdit,
    onCloseEditor,
    onDirtyChange,
    onDeleted,
    flyer = { href: adminOutreachFlyersHash(), label: 'Open flyer' },
  }: Props = $props();

  const p = $derived(c.post);
  const status = $derived(statusLine(p, c.channel));
  const btn = $derived(cardButtons(p, c.channel, available));
  const hand = $derived(!sendsItself(c.channel));
  const title = $derived(hand ? handChannelLabel(c.channel) : c.channel.label);
  const copies = $derived(p ? handCopies(p) : null);
  const canPrepare = $derived(p ? composerActions(p, c.channel, available).canPrepare : false);

  let busy = $state(false);
  let error = $state('');
  let copied = $state<'withlink' | 'text' | 'link' | 'postlink' | null>(null);
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
      } else if (mode === 'unmark') {
        const updated = await unmarkOutreachPosted(post.id);
        onChange(updated);
        onNotice({ text: 'Unmarked. It is a post to make by hand again.', kind: 'status', status: updated.status });
      } else if (mode === 'takedown') {
        const updated = await takedownOutreachPost(post.id);
        onChange(updated);
        onNotice({ text: `Deleted from ${c.channel.label}. It is a draft again, with the current link.`, kind: 'status', status: updated.status });
        // Nothing may be posted now, so the page's Reset availability changed.
        reload();
      } else {
        const updated = await sendOutreachPost(post.id);
        onChange(updated);
        onNotice({ text: 'Queued. It posts within a few seconds; this page updates when it does.', kind: 'status', status: updated.status });
      }
      dialog = null;
    } catch (err) {
      dialogError = message(
        err,
        mode === 'markPosted' ? 'Could not mark posted. Please try again.' : mode === 'unmark' ? 'Could not unmark. Please try again.' : mode === 'takedown' ? 'Could not delete the post. Please try again.' : 'Could not post. Please try again.',
      );
    } finally {
      busy = false;
    }
  }

  let manualCopy = $state<string | null>(null);
  let manualEl = $state<HTMLTextAreaElement | undefined>();
  const COPIED: Record<'withlink' | 'text' | 'link' | 'postlink', string> = {
    withlink: 'Copied with the link.',
    text: 'Copied without the link.',
    link: 'Link copied.',
    postlink: 'Link to the post copied.',
  };
  async function copy(kind: 'withlink' | 'text' | 'link' | 'postlink', value: string): Promise<void> {
    clearTimeout(copyTimer);
    copied = null;
    manualCopy = null;
    error = '';
    if (!(await writeClipboard(value))) {
      onNotice(null);
      manualCopy = value;
      error = 'Your browser did not allow copying. The text is selected below: press Command-C or Ctrl-C.';
      await tick();
      manualEl?.focus();
      manualEl?.select();
      return;
    }
    copied = kind;
    const n: CardNotice = { text: COPIED[kind], kind: 'status', status: p?.status ?? null };
    onNotice(n);
    copyTimer = setTimeout(() => {
      copied = null;
      // Compared by content, not identity: the list stores notices in $state,
      // which hands back a proxy, so `notice === n` is never true.
      if (notice && notice.text === n.text && notice.kind === n.kind && notice.status === n.status) onNotice(null);
    }, 2000);
  }
  // Selected text must never outlive the post it was taken from.
  $effect(() => {
    void p?.body;
    void p?.short_url;
    untrack(() => {
      if (manualCopy !== null) {
        manualCopy = null;
        error = '';
      }
    });
  });
  const createLink = (post: OutreachPost) =>
    run('create the link', async () => {
      onChange(await prepareOutreachPost(post.id));
    });
</script>

<article class="card" aria-label="{title} {waveLabel}">
  <h5 class="card-title">{title} &middot; {waveLabel}{p?.stale ? ' (stale)' : ''}</h5>
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
      {#if hand && isVideoChannel(c.channel) && copies}
        <!-- Focusable so a keyboard user can scroll it. -->
        <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
        <div class="hand-text" tabindex="0" role="group" aria-label="{title} text">
          <OutreachPostPreview {view} channel={c.channel} post={p} body={copies.withLink ?? copies.textOnly} />
        </div>
      {:else}
        <OutreachPostPreview {view} channel={c.channel} post={p} />
      {/if}
    {/if}
    <p class="status status-{status.kind}">
      {#if status.kind === 'queued'}<span class="spinner" aria-hidden="true"></span>{/if}
      {status.text}{#if status.kind === 'queued'}{' '}usually under ten seconds{/if}{#if status.kind === 'posted' && status.href && isSafeLinkHref(status.href)}{' · '}<a href={status.href} rel="noopener noreferrer">view post</a>{/if}
    </p>
    {#if p && cellClicksLabel(p, results)}<p class="text-muted">{cellClicksLabel(p, results)}</p>{/if}
    {#if p && cellReactionsLabel(p, results)}<p class="text-muted">{cellReactionsLabel(p, results)}</p>{/if}
    {#if p && hand}
      {#if p.short_url}
        <p class="link-row">
          <span class="link-label">{handLinkLabel(c.channel)}</span>
          <span class="mono short">{p.short_url}</span>
          <Button onclick={() => copy('link', p.short_url ?? '')} disabled={busy}>{copyLabel('Copy link', copied === 'link')}</Button>
        </p>
      {:else}
        <p class="text-warn no-link">
          This post has no ShortLink yet, so it cannot be copied with a link or marked posted. Copy text only still works.
          {#if canPrepare}<Button onclick={() => createLink(p)} disabled={busy}>Create link</Button>{/if}
        </p>
      {/if}
    {/if}
    <div class="row">
      {#if c.channel.name === 'instagram'}<a class="open-flyer" href={flyer.href}>{flyer.label}</a>{/if}
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
        {#if btn.copyText && copies}
          <Button onclick={() => copy('withlink', copies.withLink ?? '')} disabled={busy || copies.withLink === null}>{copyLabel('Copy with link', copied === 'withlink')}</Button>
          <Button onclick={() => copy('text', copies.textOnly)} disabled={busy || copies.textOnly === ''}>{copyLabel('Copy text only', copied === 'text')}</Button>
        {/if}
        {#if p.status === 'posted' && copyablePostLink(p)}
          <Button onclick={() => copy('postlink', copyablePostLink(p) ?? '')} disabled={busy}>{copyLabel('Copy link to post', copied === 'postlink')}</Button>
        {/if}
        {#if btn.markPosted}
          <Button onclick={() => openDialog('markPosted')} disabled={busy}>Mark posted</Button>
        {/if}
        {#if btn.unmark}
          <Button onclick={() => openDialog('unmark')} disabled={busy}>Unmark</Button>
        {/if}
        {#if btn.takedown}
          <Button onclick={() => openDialog('takedown')} disabled={busy}>Delete and redo…</Button>
        {/if}
        {#if btn.edit}
          <Button onclick={() => onEdit(p)} data-edit-for={p.id}>Edit</Button>
        {/if}
      {/if}
    </div>
    {#if btn.copyText && copies}
      {#if copies.textOnly === ''}
        <p class="text-muted">Nothing is left of this post without its link, so there is no text to copy alone.</p>
      {:else}
        <details class="text-only">
          <summary>See the text only version</summary>
          <pre class="text-only-body">{copies.textOnly}</pre>
        </details>
      {/if}
    {/if}
    {#if manualCopy !== null}
      <label class="manual-copy">
        <span class="link-label">Text to copy</span>
        <textarea readonly rows="6" value={manualCopy} bind:this={manualEl} onfocus={(e) => e.currentTarget.select()}></textarea>
      </label>
      <p><Button onclick={() => { manualCopy = null; error = ''; }}>Done</Button></p>
    {/if}
  {/if}
  <p class="notice {notice?.kind === 'error' ? 'text-error' : 'text-muted'}" role="status">{visibleNotice(notice, p)}</p>
  <p class="text-error" role="alert">{error}</p>
</article>
{#if dialog && p}
  <OutreachPostDialog
    mode={dialog}
    {view}
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
  .hand-text {
    max-height: 9rem;
    overflow-y: auto;
    margin-bottom: var(--space-2);
  }
  .no-link {
    margin: 0 0 var(--space-2);
  }
  .text-only {
    margin: var(--space-2) 0;
  }
  .text-only-body {
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    font-family: var(--font);
    margin: var(--space-2) 0 0;
  }
  .manual-copy {
    display: block;
    margin: var(--space-2) 0;
  }
  .manual-copy textarea {
    display: block;
    width: 100%;
    font-family: var(--font);
    font-size: var(--fs-base);
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
