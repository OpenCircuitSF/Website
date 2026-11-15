<!--
  One guide post (#0660): its text, status, link, counts and the actions its
  status allows (lib/guidePosts.ts postActions). Edit swaps the text for a
  textarea in place; every other action is handed to the shell.
-->
<script lang="ts">
  import Button from '../../lib/Button.svelte';
  import { adminSettingsHash } from '../../lib/adminRouter';
  import {
    ACTION_LABELS,
    asSentence,
    counterText,
    formatClicks,
    formatSignups,
    notConnectedNote,
    overLimit,
    postActions,
    statusLine,
    type PostAction,
  } from '../../lib/guidePosts';
  import type { GuidePostChannel, GuidePostRow } from '../../lib/types';

  interface Props {
    post: GuidePostRow;
    channel: GuidePostChannel;
    topicTitle: string;
    onAction: (action: PostAction, post: GuidePostRow) => void;
    onSave: (post: GuidePostRow, body: string) => Promise<void>;
  }
  let { post, channel, topicTitle, onAction, onSave }: Props = $props();

  let editing = $state(false);
  let draft = $state('');
  let saving = $state(false);
  let saveError = $state<string | null>(null);

  const actions = $derived(postActions(post, channel.configured));
  const anyDisabled = $derived(actions.some((a) => !a.enabled));
  const draftOver = $derived(overLimit(channel, draft, post.short_url));

  function startEdit(): void {
    draft = post.body;
    saveError = null;
    editing = true;
  }

  async function save(): Promise<void> {
    if (draftOver || saving) return;
    saving = true;
    saveError = null;
    try {
      await onSave(post, draft);
      editing = false;
    } catch (e) {
      saveError = asSentence(e instanceof Error ? e.message : String(e));
    } finally {
      saving = false;
    }
  }

  function act(action: PostAction): void {
    if (action === 'edit') startEdit();
    else onAction(action, post);
  }
</script>

<article class="card" data-post-id={post.id}>
  <header class="head">
    <strong>{topicTitle}</strong>
    <span>{channel.label}</span>
    <span class="chip">{post.campaign_slug}</span>
    <span class="status-words">{statusLine(post)}</span>
  </header>
  {#if post.last_error && (post.status === 'failed' || post.status === 'draft')}
    <p class="last-error">{post.last_error}</p>
  {/if}

  {#if editing}
    <label>Text
      <textarea rows="6" bind:value={draft}></textarea>
    </label>
    <p class="counter" class:over={draftOver}>
      {counterText(channel, draft, post.short_url)}{#if draftOver}{' '}Too long for {channel.label}.{/if}
    </p>
    {#if saveError}
      <p class="text-error" role="alert">{saveError}</p>
    {/if}
    <div class="row">
      <Button variant="primary" onclick={() => void save()} disabled={draftOver || saving}>{saving ? 'Saving…' : 'Save'}</Button>
      <Button onclick={() => (editing = false)} disabled={saving}>Cancel</Button>
    </div>
  {:else}
    <pre class="post-body">{post.body}</pre>
  {/if}

  <p class="link">{post.short_url ?? 'No short link yet'}</p>
  {#if post.short_url}
    <p class="counts">{formatClicks(post.clicks)} · {formatSignups(post.signups)}</p>
  {/if}

  {#if !editing && actions.length > 0}
    <div class="row">
      {#each actions as a (a.action)}
        {#if a.action === 'open'}
          <a href={post.remote_url ?? undefined} target="_blank" rel="noopener">{ACTION_LABELS.open}</a>
        {:else}
          <Button variant={a.action === 'delete' ? 'danger' : 'default'} disabled={!a.enabled} onclick={() => act(a.action)}>
            {ACTION_LABELS[a.action]}
          </Button>
        {/if}
      {/each}
    </div>
    {#if anyDisabled && notConnectedNote(channel)}
      <p class="note">{channel.label} is not connected. You can write drafts; connect it under
        <a href={adminSettingsHash('outreach')}>Settings → Outreach</a> to schedule or post.</p>
    {/if}
  {/if}
</article>

<style>
  .card {
    border: var(--border-w) solid var(--border);
    border-radius: var(--radius);
    padding: var(--space-3);
    margin-bottom: var(--space-3);
    display: grid;
    gap: var(--space-2);
  }
  .head {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    align-items: baseline;
  }
  .chip {
    border: var(--border-w) solid var(--border);
    border-radius: var(--radius);
    padding: 0 var(--space-2);
    color: var(--text-muted);
  }
  .post-body {
    white-space: pre-wrap;
    word-break: break-word;
    margin: 0;
    font-family: var(--font);
  }
  .link,
  .counts,
  .note,
  .last-error,
  .counter {
    margin: 0;
    color: var(--text-muted);
    word-break: break-all;
  }
  .counter.over {
    font-weight: 600;
    color: var(--text);
  }
  label {
    display: grid;
    gap: var(--space-1);
  }
  .row {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    align-items: center;
  }
</style>
