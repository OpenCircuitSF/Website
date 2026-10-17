<!--
  The inline composer for one outreach post (#0544): the text with an exact
  character count against the channel limit, the tracked-link box with its UTM
  chips, and Save / Regenerate / Prepare / Copy content / Mark posted /
  Delete, plus (#0545) Post now / Retry and Schedule / Unschedule for an
  automatic channel the server has credentials for. Which buttons apply is
  decided in lib/outreach.ts (composerActions); this file is markup and
  wiring only. Copy is a one-line clipboard call so a test can stub
  navigator.clipboard.
-->
<script lang="ts">
  import { untrack } from 'svelte';
  import {
    updateOutreachPost,
    prepareOutreachPost,
    markOutreachPosted,
    deleteOutreachPost,
    sendOutreachPost,
    scheduleOutreachPost,
    unscheduleOutreachPost,
    ApiError,
  } from '../../lib/api';
  import {
    channelStatusNote,
    composerActions,
    countCharacters,
    defaultScheduleAt,
    fromLocalInputValue,
    isOverLimit,
    toLocalInputValue,
    utmChips,
    validateRemoteUrl,
  } from '../../lib/outreach';
  import type { OutreachChannel, OutreachPost, OutreachWave } from '../../lib/types';
  import { formatDateTime } from '../../lib/admin';
  import Button from '../../lib/Button.svelte';

  interface Props {
    post: OutreachPost;
    channel: OutreachChannel;
    wave: OutreachWave;
    available: boolean;
    onChange: (post: OutreachPost) => void;
    onDeleted: () => void;
  }
  let { post, channel, wave, available, onChange, onDeleted }: Props = $props();

  // svelte-ignore state_referenced_locally
  let body = $state(post.body);
  let remoteUrl = $state('');
  let busy = $state(false);
  let error = $state<string | null>(null);
  let notice = $state<string | null>(null);

  // Pick up a body the server rewrote (Prepare, Regenerate). Compared against
  // the last server body, not just re-run on every new post object: the panel
  // refreshes while a post is queued, and that must not wipe an unsaved edit.
  // svelte-ignore state_referenced_locally
  let serverBody = post.body;
  $effect(() => {
    const incoming = post.body;
    untrack(() => {
      if (incoming !== serverBody) {
        serverBody = incoming;
        body = incoming;
      }
    });
  });

  // The schedule picker: the post's own time when scheduled, otherwise the
  // wave's suggestion when it is still in the future (#0545).
  function initialScheduleValue(): string {
    const iso = post.scheduled_at ?? defaultScheduleAt(wave.name, [wave], new Date());
    return iso ? toLocalInputValue(iso, new Date(iso).getTimezoneOffset()) : '';
  }
  // svelte-ignore state_referenced_locally
  let scheduleLocal = $state(initialScheduleValue());

  const actions = $derived(composerActions(post, channel, available));
  const channelNote = $derived(channelStatusNote(channel));
  const locked = $derived(post.status === 'posted' || post.status === 'queued');
  const counted = $derived(countCharacters(channel, body, post.prepared));
  const over = $derived(isOverLimit(channel, counted.count));
  const dirty = $derived(body !== post.body);

  function message(err: unknown, fallback: string): string {
    return err instanceof ApiError ? err.message : fallback;
  }

  async function run(label: string, fn: () => Promise<void>): Promise<void> {
    busy = true;
    error = null;
    notice = null;
    try {
      await fn();
    } catch (err) {
      error = message(err, `Could not ${label}. Please try again.`);
    } finally {
      busy = false;
    }
  }

  const save = () =>
    run('save', async () => {
      onChange(await updateOutreachPost(post.id, { body }));
      notice = 'Saved.';
    });
  const regenerate = () =>
    run('regenerate', async () => {
      onChange(await updateOutreachPost(post.id, { regenerate: true }));
      notice = 'Regenerated from the current workshop details.';
    });
  const prepare = () =>
    run('prepare the link', async () => {
      onChange(await prepareOutreachPost(post.id));
      notice = 'Tracked link created.';
    });
  const copy = () =>
    run('copy', async () => {
      await navigator.clipboard.writeText(post.body);
      notice = 'Copied.';
    });
  const markPosted = () =>
    run('mark posted', async () => {
      const bad = validateRemoteUrl(remoteUrl);
      if (bad) {
        error = bad;
        return;
      }
      onChange(await markOutreachPosted(post.id, remoteUrl.trim() || undefined));
      notice = 'Marked as posted.';
    });
  const sendNow = () =>
    run(actions.canRetry ? 'retry' : 'post', async () => {
      onChange(await sendOutreachPost(post.id));
      notice = 'Queued. It posts within a few seconds; this page updates when it does.';
    });
  const schedule = () =>
    run('schedule', async () => {
      // Resolve the offset for the chosen moment, not today, so a date across
      // a daylight-saving change converts correctly.
      const offset = new Date(scheduleLocal).getTimezoneOffset();
      const iso = fromLocalInputValue(scheduleLocal, offset);
      if (iso === null) {
        error = 'Pick a date and time to schedule.';
        return;
      }
      onChange(await scheduleOutreachPost(post.id, iso));
      notice = 'Scheduled.';
    });
  const unschedule = () =>
    run('unschedule', async () => {
      onChange(await unscheduleOutreachPost(post.id));
      notice = 'Back to draft.';
    });
  const remove = () =>
    run('delete', async () => {
      await deleteOutreachPost(post.id);
      onDeleted();
    });
</script>

<div class="composer">
  <h3 class="composer-title">{channel.label} &middot; {wave.label}</h3>

  {#if actions.canRegenerate}
    <p class="text-warn">
      The workshop's title, date or place changed since this was drafted.
      <Button onclick={regenerate} disabled={busy}>Regenerate</Button>
    </p>
  {/if}

  <label class="field">
    <span class="field-label">Post text</span>
    <textarea rows="7" bind:value={body} disabled={locked}></textarea>
  </label>
  <p class={over ? 'text-error' : 'text-muted'}>
    {counted.exact ? '' : '≈ '}{counted.count}{channel.char_limit > 0 ? ` / ${channel.char_limit}` : ''} characters{over ? ' — over the limit' : ''}
  </p>

  <div class="link-box">
    <p class="field-label">Tracked link</p>
    {#if post.short_url}
      <p class="mono">{post.short_url}</p>
    {:else}
      <p class="text-muted">Not created yet. Prepare puts the real short URL in the text.</p>
    {/if}
    <p class="text-muted dest">Goes to <span class="mono">{post.destination_url}</span></p>
    <ul class="chips">
      {#each utmChips(post) as chip (chip.label)}
        <li class="chip"><span class="text-muted">{chip.label}</span> {chip.value}</li>
      {/each}
    </ul>
  </div>

  {#if channelNote}
    <p class="text-muted">{channelNote}</p>
  {/if}
  {#if post.held}
    <p class="text-warn">Held — the workshop is not published, so this will not be sent.</p>
  {/if}
  {#if post.status === 'queued'}
    <p class="text-notice">Queued to post. It is locked until it is sent{post.last_error ? ' or fails' : ''}.</p>
  {/if}
  {#if post.status === 'scheduled' && post.scheduled_at}
    <p class="text-notice">Scheduled for {formatDateTime(post.scheduled_at)}.</p>
  {/if}

  {#if post.status === 'posted'}
    <p class="text-notice">
      Posted{post.posted_at ? ` ${formatDateTime(post.posted_at)}` : ''}.
      {#if post.remote_url}<a href={post.remote_url} rel="noopener noreferrer">View post</a>{/if}
    </p>
  {/if}

  {#if actions.canMarkPosted}
    <label class="field">
      <span class="field-label">Post URL (optional)</span>
      <input type="url" bind:value={remoteUrl} placeholder="https://…" />
    </label>
  {/if}

  {#if actions.canSchedule}
    <div class="schedule">
      <label class="field">
        <span class="field-label">Schedule for</span>
        <input type="datetime-local" bind:value={scheduleLocal} />
      </label>
      {#if !scheduleLocal}
        <p class="text-muted">No suggested time for this wave, or it has passed. Pick one.</p>
      {/if}
    </div>
  {/if}

  <div class="row">
    {#if actions.canPostNow || actions.canRetry}
      <Button variant="primary" onclick={sendNow} disabled={busy || dirty || over}>
        {actions.canRetry ? 'Retry' : 'Post now'}
      </Button>
    {/if}
    {#if actions.canSchedule}
      <Button onclick={schedule} disabled={busy || dirty || over || !scheduleLocal}>Schedule</Button>
    {/if}
    {#if actions.canUnschedule}
      <Button onclick={unschedule} disabled={busy}>Unschedule</Button>
    {/if}
    {#if actions.canSave}
      <Button variant="primary" onclick={save} disabled={busy || !dirty || over}>Save</Button>
    {/if}
    {#if actions.canPrepare}
      <Button onclick={prepare} disabled={busy || dirty}>Prepare link</Button>
    {/if}
    {#if actions.canCopy}
      <Button onclick={copy} disabled={busy || dirty}>Copy content</Button>
    {/if}
    {#if actions.canMarkPosted}
      <Button onclick={markPosted} disabled={busy || dirty}>Mark posted</Button>
    {/if}
    {#if actions.canDelete}
      <Button variant="danger" onclick={remove} disabled={busy}>Delete</Button>
    {/if}
  </div>
  {#if dirty && !locked}
    <p class="text-muted">Save your edits before preparing, copying, posting or marking posted.</p>
  {/if}

  {#if error}
    <p class="text-error" role="alert">{error}</p>
  {/if}
  <p class="text-notice" role="status">{notice ?? ''}</p>
  <!-- Always rendered, so a failure the publisher records later is announced. -->
  <p class="text-warn send-error" role="status">{post.last_error ?? ''}</p>
</div>

<style>
  .composer {
    margin-top: var(--space-4);
    padding-top: var(--space-4);
    border-top: var(--border-w) solid var(--border);
  }
  .composer-title {
    font-size: var(--fs-lg);
    margin: 0 0 var(--space-3);
  }
  .field {
    display: block;
    margin-bottom: var(--space-2);
  }
  .field-label {
    display: block;
    margin: 0 0 var(--space-1);
    font-size: var(--fs-sm);
    color: var(--text-muted);
  }
  textarea {
    width: 100%;
    font-family: var(--font);
    font-size: var(--fs-base);
  }
  .link-box {
    margin: var(--space-3) 0;
  }
  .mono {
    font-family: var(--font-mono);
    overflow-wrap: anywhere;
  }
  .dest {
    font-size: var(--fs-sm);
  }
  .send-error:empty {
    display: none;
  }
  .chips {
    list-style: none;
    padding: 0;
    margin: var(--space-2) 0 0;
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
  }
  .chip {
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    border: var(--border-w) solid var(--border);
    border-radius: var(--radius);
    padding: 0 var(--space-2);
  }
</style>
