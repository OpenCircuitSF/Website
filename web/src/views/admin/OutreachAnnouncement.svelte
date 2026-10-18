<!--
  One wave's message section on the workshop outreach page (#0582): the
  Announcement, and the body of the Reminder and Last call rows. One Message
  field holds the text every automatic post shares; Save message writes it to
  each post that is not customized (lib/outreachMessage.ts decides what is
  written), "How it will look" shows each post as it will go out, and one
  button posts them all through the same in-app dialog the cards use.
  Customize… opens the composer for a single post and marks it customized, so
  a later Save message leaves it alone until "Use shared message" hands it
  back. Schedule for later… takes one time for every postable post.

  The live regions (role="status", role="alert") sit unconditionally at the top
  level of this file, never inside an {#if} or {#each}, so the structural guard
  and screen readers see them from the start.
-->
<script lang="ts">
  import { untrack } from 'svelte';
  import {
    draftAllOutreach,
    updateOutreachPost,
    scheduleOutreachPost,
    unscheduleOutreachPost,
    ApiError,
  } from '../../lib/api';
  import {
    countCharacters,
    defaultScheduleAt,
    fromLocalInputValue,
    isOverLimit,
    toLocalInputValue,
  } from '../../lib/outreach';
  import { cardButtons, postableAutomatic, sendsItself, statusLine, visibleNotice, type CardNotice } from '../../lib/outreachCards';
  import { composeBody, messageTargets, planSaveMessage, sharedMessage, splitBody } from '../../lib/outreachMessage';
  import { postAll } from '../../lib/outreachPostAll';
  import type { OutreachChannel, OutreachPost, OutreachResults, WorkshopOutreach } from '../../lib/types';
  import Button from '../../lib/Button.svelte';
  import OutreachComposer from './OutreachComposer.svelte';
  import OutreachPostDialog from './OutreachPostDialog.svelte';

  interface Props {
    view: WorkshopOutreach;
    results: OutreachResults | null;
    wave: string;
    workshopId: number;
    onChange: (post: OutreachPost) => void;
    /** Refetch the whole view (after drafting, or a post is deleted). */
    reload: () => void;
  }
  let { view, wave, workshopId, onChange, reload }: Props = $props();

  const waveInfo = $derived(view.waves.find((w) => w.name === wave) ?? null);
  const autos = $derived(
    view.channels
      .filter(sendsItself)
      .map((channel) => ({ channel, post: view.posts.find((p) => p.channel === channel.name && p.wave === wave) ?? null }))
      .filter((a): a is { channel: OutreachChannel; post: OutreachPost } => a.post !== null),
  );

  const shared = $derived(sharedMessage(view, wave));
  // svelte-ignore state_referenced_locally
  let message = $state(shared);
  let touched = $state(false);
  // Pick up what the server holds while the field has no unsaved typing.
  $effect(() => {
    const s = shared;
    untrack(() => {
      if (!touched) message = s;
    });
  });

  const trimmed = $derived(message.trim());
  const plan = $derived(planSaveMessage(view, wave, trimmed));
  const dirty = $derived(touched && trimmed !== shared);
  const anyOver = $derived(plan.some((i) => i.over));
  const canSave = $derived(dirty && plan.length > 0 && trimmed !== '' && !anyOver);
  const customizedLeft = $derived(autos.filter((a) => a.post.customized && a.post.status !== 'posted' && a.post.status !== 'queued'));
  const postable = $derived(postableAutomatic(view, wave));
  const scheduleTargets = $derived(autos.filter((a) => cardButtons(a.post, a.channel, view.available).schedule));
  const unscheduleTargets = $derived(autos.filter((a) => cardButtons(a.post, a.channel, view.available).unschedule));

  let busy = $state(false);
  let status = $state('');
  // The post result line and what it reported on; it clears with the cards' own rule (visibleNotice).
  let postLine = $state('');
  let postNotices = $state<Record<number, CardNotice>>({});
  let errorText = $state('');
  const shownPostLine = $derived(
    Object.entries(postNotices).some(([id, n]) => visibleNotice(n, view.posts.find((p) => p.id === Number(id)) ?? null) !== '') ? postLine : '',
  );
  let customizingId = $state<number | null>(null);
  let postOpen = $state(false);
  let postBusy = $state(false);
  let postSnapshot = $state<{ post: OutreachPost; channel: OutreachChannel }[]>([]);
  let scheduling = $state(false);
  // svelte-ignore state_referenced_locally
  let scheduleLocal = $state(initialSchedule());

  function initialSchedule(): string {
    const iso = defaultScheduleAt(wave, view.waves, new Date());
    return iso ? toLocalInputValue(iso) : '';
  }

  function msg(err: unknown, fallback: string): string {
    return err instanceof ApiError ? err.message : fallback;
  }
  const names = (labels: string[]): string => labels.join(' and ');

  async function guarded(fallback: string, fn: () => Promise<void>): Promise<void> {
    busy = true;
    status = '';
    postLine = '';
    errorText = '';
    try {
      await fn();
    } catch (err) {
      errorText = msg(err, fallback);
    } finally {
      busy = false;
    }
  }

  const write = () =>
    guarded('Could not write the posts. Please try again.', async () => {
      await draftAllOutreach(workshopId, wave === 'announce' ? [] : [wave]);
      touched = false;
      reload();
    });

  const saveMessage = () =>
    guarded('Could not save the message. Please try again.', async () => {
      const done: string[] = [];
      for (const item of plan) {
        onChange(await updateOutreachPost(item.post.id, { body: item.body }));
        done.push(item.channel.label);
      }
      touched = false;
      status =
        `Saved to ${names(done)}.` +
        (customizedLeft.length > 0 ? ` ${names(customizedLeft.map((a) => a.channel.label))} ${customizedLeft.length > 1 ? 'are customized and were' : 'is customized and was'} left alone.` : '');
    });

  const useShared = (post: OutreachPost) =>
    guarded('Could not use the shared message. Please try again.', async () => {
      const { tail } = splitBody(post.body, post.short_url);
      onChange(await updateOutreachPost(post.id, { body: composeBody(trimmed || shared, tail), customized: false }));
    });

  function openPost(): void {
    postSnapshot = postable;
    postOpen = true;
  }
  async function confirmPost(): Promise<void> {
    postBusy = true;
    status = '';
    const r = await postAll(postSnapshot, onChange);
    postNotices = r.notices;
    postLine = r.line;
    postBusy = false;
    postOpen = false;
  }

  const schedule = () =>
    guarded('Could not schedule the posts. Please try again.', async () => {
      const iso = fromLocalInputValue(scheduleLocal);
      if (iso === null) {
        errorText = 'Pick a date and time to schedule.';
        return;
      }
      const done: string[] = [];
      for (const { post, channel } of scheduleTargets) {
        onChange(await scheduleOutreachPost(post.id, iso));
        done.push(channel.label);
      }
      scheduling = false;
      status = `Scheduled on ${names(done)}.`;
    });

  const unschedule = () =>
    guarded('Could not unschedule. Please try again.', async () => {
      for (const { post } of unscheduleTargets) onChange(await unscheduleOutreachPost(post.id));
      status = 'Back to draft.';
    });

  /** The text a post will go out with: the unsaved message while typing, else what is stored. */
  function shownBody(post: OutreachPost): string {
    if (!dirty) return post.body;
    return plan.find((i) => i.post.id === post.id)?.body ?? post.body;
  }
  function closeComposer(): void {
    customizingId = null;
  }
  function composerDeleted(): void {
    customizingId = null;
    reload();
  }
</script>

<section class="announcement" aria-label={waveInfo?.label ?? wave}>
  {#if wave === 'announce'}<h3>Announcement</h3>{/if}

  {#if autos.length === 0}
    <p class="text-muted">No posts yet. Start with the {wave === 'announce' ? 'announcement' : (waveInfo?.label ?? wave).toLowerCase()}.</p>
    <p class="row">
      <Button variant="primary" onclick={write} disabled={busy || !view.available}>
        Write the {wave === 'announce' ? 'announcement' : (waveInfo?.label ?? wave).toLowerCase()}
      </Button>
    </p>
  {:else}
    <label class="field">
      <span class="field-label">Message</span>
      <textarea
        rows="5"
        bind:value={message}
        oninput={() => (touched = true)}
        disabled={busy || messageTargets(view, wave).length === 0}
      ></textarea>
    </label>
    <p class="row">
      <Button variant="primary" onclick={saveMessage} disabled={busy || !canSave}>Save message</Button>
    </p>
    {#if anyOver}
      <p class="text-error">This message is over the limit for {names(plan.filter((i) => i.over).map((i) => i.channel.label))}.</p>
    {/if}

    <h4 class="how">How it will look</h4>
    {#each autos as { channel, post } (post.id)}
      {@const body = shownBody(post)}
      {@const counted = countCharacters(channel, body, post.prepared)}
      {@const over = isOverLimit(channel, counted.count)}
      {@const line = statusLine(post, channel)}
      <article class="preview" aria-label="{channel.label} preview">
        <h5 class="preview-title">
          {channel.label}{channel.account ? ` · ${channel.account}` : ''}
          {#if post.customized}<span class="badge">Customized</span>{/if}
        </h5>
        {#if customizingId === post.id}
          {#key post.id}
            <OutreachComposer
              {post}
              {channel}
              wave={waveInfo ?? { name: wave, label: wave, suggested_at: null }}
              available={view.available}
              {onChange}
              onDeleted={composerDeleted}
              onClose={closeComposer}
              markCustomized
            />
          {/key}
        {:else}
          <pre class="body">{body}</pre>
          <p class={over ? 'text-error' : 'text-muted'}>
            {counted.exact ? '' : '≈ '}{counted.count}{channel.char_limit > 0 ? ` / ${channel.char_limit}` : ''} characters{over ? ' — over the limit' : ''}
          </p>
          <p class="status status-{line.kind}">{line.text}</p>
          <p class="row">
            {#if post.status !== 'posted' && post.status !== 'queued'}
              <Button onclick={() => (customizingId = post.id)} disabled={busy || customizingId !== null}>Customize…</Button>
            {/if}
            {#if post.customized && post.status !== 'posted' && post.status !== 'queued'}
              <Button onclick={() => useShared(post)} disabled={busy || customizingId !== null}>Use shared message</Button>
            {/if}
          </p>
        {/if}
      </article>
    {/each}

    <p class="row">
      {#if postable.length > 0}
        <Button variant="primary" onclick={openPost} disabled={busy || dirty}>Post to {names(postable.map((i) => i.channel.label))}</Button>
      {/if}
      {#if scheduleTargets.length > 0 && !scheduling}
        <Button onclick={() => (scheduling = true)} disabled={busy || dirty}>Schedule for later…</Button>
      {/if}
      {#if unscheduleTargets.length > 0}
        <Button onclick={unschedule} disabled={busy}>Unschedule</Button>
      {/if}
    </p>
    {#if dirty}<p class="text-muted">Save the message before posting or scheduling.</p>{/if}
    {#if scheduling}
      <div class="schedule">
        <label class="field">
          <span class="field-label">Schedule for (Pacific time)</span>
          <input type="datetime-local" bind:value={scheduleLocal} />
        </label>
        <p class="row">
          <Button variant="primary" onclick={schedule} disabled={busy || !scheduleLocal}>Schedule</Button>
          <Button onclick={() => (scheduling = false)} disabled={busy}>Cancel</Button>
        </p>
      </div>
    {/if}
  {/if}

  <p class="text-muted" role="status">{status || shownPostLine}</p>
  <p class="text-error" role="alert">{errorText}</p>
</section>

{#if postOpen}
  <OutreachPostDialog
    mode={postSnapshot.every((i) => i.post.status === 'failed') ? 'retry' : 'post'}
    items={postSnapshot}
    inFlight={postBusy}
    errorMessage={null}
    onConfirm={() => void confirmPost()}
    onClose={() => !postBusy && (postOpen = false)}
  />
{/if}

<style>
  .announcement {
    margin: var(--space-4) 0;
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
  .how {
    margin: var(--space-4) 0 var(--space-2);
  }
  .preview {
    border: var(--border-w) solid var(--border);
    border-radius: var(--radius);
    padding: var(--space-3);
    margin-bottom: var(--space-3);
  }
  .preview-title {
    margin: 0 0 var(--space-2);
    font-size: var(--fs-base);
  }
  .badge {
    margin-left: var(--space-2);
    font-size: var(--fs-sm);
    font-weight: 400;
    color: var(--text-muted);
    border: var(--border-w) solid var(--border);
    border-radius: var(--radius);
    padding: 0 var(--space-2);
  }
  .body {
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    font-family: var(--font);
    margin: 0 0 var(--space-2);
  }
  .status {
    font-weight: 600;
    margin: var(--space-2) 0;
  }
  [role='status']:empty,
  [role='alert']:empty {
    display: none;
  }
</style>
