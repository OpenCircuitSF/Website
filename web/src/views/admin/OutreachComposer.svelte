<!--
  The composer for one outreach post (#0544): the text with an exact character
  count against the channel limit, the tracked link and its UTM chips under a
  "Link details" disclosure (#0582), and (#0591) an action row of Save and Cancel
  (plus Create link when the link is missing), with Delete set apart at the far
  end; Regenerate sits with its warning, and Schedule / Unschedule (#0545) sit
  with the schedule picker for an automatic channel the server has credentials
  for. It always replaces a card's display
  and always has Cancel; the card owns Post now, Retry and Mark posted behind
  their confirmation dialog (#0581), and since #0582 nothing in this file posts
  or marks posted. `markCustomized` makes Save record the post as customized,
  so a shared-message save leaves it alone. Which buttons apply is decided in
  lib/outreach.ts (composerActions); this file is markup and wiring only.
-->
<script lang="ts">
  import { untrack } from 'svelte';
  import {
    updateOutreachPost,
    prepareOutreachPost,
    deleteOutreachPost,
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
    scheduleHint,
    toLocalInputValue,
    utmChips,
  } from '../../lib/outreach';
  import type { OutreachChannel, OutreachPost, OutreachWave } from '../../lib/types';
  import { formatDateTime } from '../../lib/admin';
  import Button from '../../lib/Button.svelte';
  import OutreachConfirmDialog from './OutreachConfirmDialog.svelte';

  interface Props {
    post: OutreachPost;
    channel: OutreachChannel;
    wave: OutreachWave;
    available: boolean;
    onChange: (post: OutreachPost) => void;
    onDeleted: () => void;
    /** The composer replaces the card's display (#0579): Save and Cancel call it to return to display mode. */
    onClose: () => void;
    /** #0582: Save marks the post customized (the Customize… path). */
    markCustomized?: boolean;
    /** Reports whether the text has unsaved edits, so the card list can ask before discarding them. */
    onDirtyChange?: (dirty: boolean) => void;
  }
  let { post, channel, wave, available, onChange, onDeleted, onClose, markCustomized = false, onDirtyChange }: Props = $props();

  // svelte-ignore state_referenced_locally
  let body = $state(post.body);
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
    return iso ? toLocalInputValue(iso) : '';
  }
  // svelte-ignore state_referenced_locally
  let scheduleLocal = $state(initialScheduleValue());

  const actions = $derived(composerActions(post, channel, available));
  const channelNote = $derived(channelStatusNote(channel));
  const locked = $derived(post.status === 'posted' || post.status === 'queued');
  const counted = $derived(countCharacters(channel, body, post.prepared));
  const over = $derived(isOverLimit(channel, counted.count));
  const dirty = $derived(body !== post.body);
  $effect(() => {
    const d = dirty;
    untrack(() => onDirtyChange?.(d));
    return () => untrack(() => onDirtyChange?.(false));
  });
  let textarea = $state<HTMLTextAreaElement | undefined>();
  $effect(() => {
    if (textarea && !textarea.disabled) untrack(() => textarea?.focus());
  });
  let discardOpen = $state(false);
  function cancel(): void {
    if (dirty) {
      discardOpen = true;
      return;
    }
    onClose();
  }
  function discard(): void {
    discardOpen = false;
    onClose();
  }

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
      onChange(await updateOutreachPost(post.id, markCustomized ? { body, customized: true } : { body }));
      notice = 'Saved.';
      onClose();
    });
  const regenerate = () =>
    run('regenerate', async () => {
      onChange(await updateOutreachPost(post.id, { regenerate: true }));
      notice = 'Regenerated from the current workshop details.';
    });
  const prepare = () =>
    run('create the link', async () => {
      onChange(await prepareOutreachPost(post.id));
      notice = 'Short link created.';
    });
  const schedule = () =>
    run('schedule', async () => {
      // The picker is Pacific time, converted per-date (lib/pacificTime.ts).
      const iso = fromLocalInputValue(scheduleLocal);
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
  {#if actions.canRegenerate}
    <p class="text-warn">
      The workshop's title, date or place changed since this was drafted.
      <Button onclick={regenerate} disabled={busy}>Regenerate</Button>
    </p>
  {/if}

  <label class="field">
    <span class="field-label">Post text</span>
    <textarea rows="7" bind:value={body} bind:this={textarea} disabled={locked}></textarea>
  </label>
  <p class={over ? 'text-error' : 'text-muted'}>
    {counted.exact ? '' : '≈ '}{counted.count}{channel.char_limit > 0 ? ` / ${channel.char_limit}` : ''} characters{over ? ' — over the limit' : ''}
  </p>

  <details class="link-box">
    <summary>Link details</summary>
    <p class="field-label">Short link</p>
    {#if post.short_url}
      <p class="mono">{post.short_url}</p>
    {:else}
      <p class="text-muted">Not created yet. Create link puts the real short URL in the text.</p>
    {/if}
    <p class="text-muted dest">Goes to <span class="mono">{post.destination_url}</span></p>
    <ul class="chips">
      {#each utmChips(post) as chip (chip.label)}
        <li class="chip"><span class="text-muted">{chip.label}</span> {chip.value}</li>
      {/each}
    </ul>
  </details>

  {#if channelNote}
    <p class="text-muted">{channelNote}</p>
  {/if}
  {#if post.held}
    <p class="text-warn">On hold — the workshop is not published, so this will not be sent.</p>
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

  {#if actions.canSchedule}
    <div class="schedule">
      <label class="field">
        <span class="field-label">Schedule for (Pacific time)</span>
        <input type="datetime-local" bind:value={scheduleLocal} />
      </label>
      {#if !scheduleLocal}
        <p class="text-muted">{scheduleHint(wave.name, [wave])}</p>
      {/if}
      <Button onclick={schedule} disabled={busy || dirty || over || !scheduleLocal}>Schedule</Button>
    </div>
  {/if}
  {#if actions.canUnschedule}
    <div class="schedule"><Button onclick={unschedule} disabled={busy}>Unschedule</Button></div>
  {/if}

  <div class="row">
    {#if actions.canSave}
      <Button variant="primary" onclick={save} disabled={busy || !dirty || over}>Save</Button>
    {/if}
    <Button onclick={cancel}>Cancel</Button>
    {#if actions.canPrepare}
      <Button onclick={prepare} disabled={busy || dirty}>Create link</Button>
    {/if}
    {#if actions.canDelete}
      <span class="danger-end"><Button variant="danger" onclick={remove} disabled={busy}>Delete</Button></span>
    {/if}
  </div>
  {#if dirty && !locked}
    <p class="text-muted">Save your edits before creating the link or scheduling.</p>
  {/if}

  {#if error}
    <p class="text-error" role="alert">{error}</p>
  {/if}
  <p class="text-notice" role="status">{notice ?? ''}</p>
  <!-- Always rendered, so a failure the publisher records later is announced. -->
  <p class="text-warn send-error" role="status">{post.last_error ?? ''}</p>
</div>
{#if discardOpen}
  <OutreachConfirmDialog
    title="Discard your unsaved changes?"
    inFlight={false}
    errorMessage={null}
    confirmLabel="Discard changes"
    danger
    onConfirm={discard}
    onClose={() => (discardOpen = false)}
  >
    <p>The text of this post has changes that have not been saved.</p>
  </OutreachConfirmDialog>
{/if}

<style>
  .composer {
    margin-top: var(--space-4);
    padding-top: var(--space-4);
    border-top: var(--border-w) solid var(--border);
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
  .danger-end {
    margin-left: auto;
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
