<!--
  One wave's message section on the workshop outreach page (#0582): the body
  of each row. One Message
  field holds the text every automatic post shares; Save message writes it to
  each post that is not customized (lib/outreachMessage.ts decides what is
  written), "How it will look" shows each post as it will go out, and one
  button posts them all through the same in-app dialog the cards use. "When it
  goes out" holds the section's one time (#0640): Schedule… sets it for every
  automatic post in one call, Unschedule takes them all back to draft.
  Customize… opens the composer for a single post and marks it customized, so
  a later Save message leaves it alone until "Use shared message" hands it
  back. Once no
  automatic post can change any more the Message box goes and the section shows
  what was posted (#0626).

  The live regions (role="status", role="alert") sit unconditionally at the top
  level of this file, never inside an {#if} or {#each}, so the structural guard
  and screen readers see them from the start.
-->
<script lang="ts">
  import { untrack } from 'svelte';
  import {
    draftAllOutreach,
    updateOutreachPost,
    scheduleOutreachSection,
    unscheduleOutreachSection,
    takedownOutreachPost,
    ApiError,
  } from '../../lib/api';
  import {
    composerActions,
    defaultScheduleAt,
    fromLocalInputValue,
    scheduleHint,
    toLocalInputValue,
  } from '../../lib/outreach';
  import { scheduleTargets, sectionLine, sectionTimeText, unscheduleTargets } from '../../lib/outreachSection';
  import { cardButtons, onChannels, postableAutomatic, sendsItself, statusLine, visibleNotice, weekdayTime, type CardNotice } from '../../lib/outreachCards';
  import { composeBody, messageTargets, planSaveMessage, restoredTail, sharedMessage } from '../../lib/outreachMessage';
  import { postAll } from '../../lib/outreachPostAll';
  import { identicalAutomaticBodies } from '../../lib/outreachPreview';
  import type { OutreachChannel, OutreachPost, OutreachResults, WorkshopOutreach } from '../../lib/types';
  import Button from '../../lib/Button.svelte';
  import OutreachComposer from './OutreachComposer.svelte';
  import OutreachPostDialog from './OutreachPostDialog.svelte';
  import OutreachPostPreview from './OutreachPostPreview.svelte';

  interface Props {
    view: WorkshopOutreach;
    results: OutreachResults | null;
    wave: string;
    workshopId: number;
    onChange: (post: OutreachPost) => void;
    /** Refetch the whole view (after drafting, or a post is deleted). */
    reload: () => void;
    /** Whether this section holds typing not saved yet (#0625). */
    onUnsavedChange?: (unsaved: boolean) => void;
  }
  let { view, wave, workshopId, onChange, reload, onUnsavedChange }: Props = $props();

  const waveInfo = $derived(view.waves.find((w) => w.name === wave) ?? null);
  const waveName = $derived(waveInfo?.label ?? wave);
  const autos = $derived(
    view.channels
      .filter(sendsItself)
      .map((channel) => ({ channel, post: view.posts.find((p) => p.channel === channel.name && p.wave === wave) ?? null }))
      .filter((a): a is { channel: OutreachChannel; post: OutreachPost } => a.post !== null),
  );

  const locked = $derived(autos.length > 0 && autos.every((a) => a.post.status === 'posted' || a.post.status === 'queued'));
  const allPosted = $derived(autos.length > 0 && autos.every((a) => a.post.status === 'posted'));
  const handWritten = $derived(view.posts.some((p) => p.wave === wave));
  const autoOn = $derived(onChannels(view).filter(sendsItself));
  const hasAutoChannel = $derived(autoOn.length > 0);
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
  let composerDirty = $state(false);
  const unsaved = $derived((dirty && autos.length > 0 && !locked) || composerDirty);
  $effect(() => {
    const u = unsaved;
    untrack(() => onUnsavedChange?.(u));
    return () => untrack(() => onUnsavedChange?.(false));
  });
  const anyOver = $derived(plan.some((i) => i.over));
  const canSave = $derived(dirty && plan.length > 0 && trimmed !== '' && !anyOver);
  const customizedLeft = $derived(autos.filter((a) => a.post.customized && a.post.status !== 'posted' && a.post.status !== 'queued'));
  const postable = $derived(postableAutomatic(view, wave));
  const canSchedule = $derived(scheduleTargets(view, wave).length > 0);
  const canUnschedule = $derived(unscheduleTargets(view, wave).length > 0);
  const scheduledNow = $derived(canUnschedule);
  // Reminder and Final lead with Schedule…; Announce, which has no set day, with Post.
  const primarySchedule = $derived(defaultScheduleAt(wave, view.waves, new Date()) !== null);
  const timeText = $derived(sectionTimeText(view, wave));

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
  // Delete and redo (#0604, #0606): the posted auto post being confirmed, if any.
  let takedownTarget = $state<{ post: OutreachPost; channel: OutreachChannel } | null>(null);
  let takedownBusy = $state(false);
  let takedownError = $state<string | null>(null);
  // svelte-ignore state_referenced_locally
  let scheduleLocal = $state(initialSchedule());

  function initialSchedule(): string {
    const own = sectionLine(view, wave).at;
    const iso = scheduledNow && own ? own : defaultScheduleAt(wave, view.waves, new Date());
    return iso ? toLocalInputValue(iso) : '';
  }
  function openSchedule(): void {
    scheduleLocal = initialSchedule();
    scheduling = true;
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
      const tail = restoredTail(view, post);
      onChange(await updateOutreachPost(post.id, { body: composeBody(trimmed || shared, tail), customized: false }));
    });

  // The workshop changed since a post was drafted (#0593): said here, not only inside Customize….
  const staleAutos = $derived(autos.filter((a) => composerActions(a.post, a.channel, view.available).canRegenerate));
  // A customized post is the editor's own words: it is only regenerated from its own Customize….
  const regenerable = $derived(staleAutos.filter((a) => !a.post.customized));
  const regenerateStale = () =>
    guarded('Could not regenerate. Please try again.', async () => {
      for (const { post } of regenerable) onChange(await updateOutreachPost(post.id, { regenerate: true }));
      status = `Regenerated ${names(regenerable.map((a) => a.channel.label))} from the current event details.`;
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

  async function confirmTakedown(): Promise<void> {
    const t = takedownTarget;
    if (!t) return;
    takedownBusy = true;
    takedownError = null;
    try {
      onChange(await takedownOutreachPost(t.post.id));
      status = `Deleted from ${t.channel.label}. It is a draft again, with the current link.`;
      takedownTarget = null;
      reload();
    } catch (err) {
      takedownError = msg(err, 'Could not delete the post. Please try again.');
      errorText = takedownError;
    } finally {
      takedownBusy = false;
    }
  }

  const schedule = () =>
    guarded('Could not schedule the posts. Please try again.', async () => {
      const iso = fromLocalInputValue(scheduleLocal);
      if (iso === null) {
        errorText = 'Pick a date and time to schedule.';
        return;
      }
      const r = await scheduleOutreachSection(workshopId, wave, iso);
      scheduling = false;
      status =
        `Scheduled for ${weekdayTime(iso)} on ${names(r.posts.map((p) => view.channels.find((c) => c.name === p.channel)?.label ?? p.channel))}.` +
        r.left_alone.filter((l) => l.reason === 'posted').map((l) => ` ${l.channel_label} was already posted and was left alone.`).join('');
      reload();
    });

  const unschedule = () =>
    guarded('Could not unschedule. Please try again.', async () => {
      await unscheduleOutreachSection(workshopId, wave);
      status = 'Back to draft.';
      reload();
    });

  // What each post would go out with, so the warning follows unsaved typing too.
  const sameText = $derived(
    identicalAutomaticBodies({ posts: autos.map((a) => ({ channel: a.channel.name, wave, body: shownBody(a.post) })) }, wave),
  );

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

<section class="announcement wave-section" aria-label={waveInfo?.label ?? wave}>
  {#if autos.length === 0 && !hasAutoChannel && handWritten}
    <!-- Only hand-posted channels: the rows below are the whole section. -->
  {:else if autos.length === 0}
    <p class="text-muted">{handWritten ? `No post yet for ${names(autoOn.map((c) => c.label))}.` : `No posts yet. Start with the ${wave === 'announce' ? 'announcement' : `${waveName.toLowerCase()} post`}.`}</p>
    <p class="row">
      <Button variant="primary" onclick={write} disabled={busy || !view.available}>
        Write the {wave === 'announce' ? 'announcement' : `${waveName.toLowerCase()} post`}
      </Button>
    </p>
  {:else}
    {#if staleAutos.length > 0}
      <p class="text-warn">
        The event's title, date or place changed since {names(staleAutos.map((a) => a.channel.label))} {staleAutos.length > 1 ? 'were' : 'was'} drafted.
        {#if regenerable.length > 0}
          <Button onclick={regenerateStale} disabled={busy}>Regenerate</Button>
        {:else}
          Open Customize… to regenerate.
        {/if}
      </p>
    {/if}
    {#if !locked}
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
    {/if}

    <h4 class="when">When it goes out</h4>
    <p class="when-line">{timeText}</p>
    <p class="row">
      {#if canSchedule && !scheduling}
        <Button variant={primarySchedule ? 'primary' : undefined} onclick={openSchedule} disabled={busy || dirty}>{scheduledNow ? 'Change time…' : 'Schedule…'}</Button>
      {/if}
      {#if canUnschedule}<Button onclick={unschedule} disabled={busy}>Unschedule</Button>{/if}
      {#if postable.length > 0}
        <Button variant={primarySchedule ? undefined : 'primary'} onclick={openPost} disabled={busy || dirty}>Post to {names(postable.map((i) => i.channel.label))}</Button>
      {/if}
    </p>
    {#if dirty && !locked}<p class="text-muted">Save the message before posting or scheduling.</p>{/if}
    {#if scheduling}
      <div class="schedule">
        <label class="field">
          <span class="field-label">Post on (Pacific time)</span>
          <input type="datetime-local" bind:value={scheduleLocal} />
        </label>
        <p class="row">
          <Button variant="primary" onclick={schedule} disabled={busy || !scheduleLocal}>Schedule</Button>
          <Button onclick={() => (scheduling = false)} disabled={busy}>Cancel</Button>
        </p>
        {#if !scheduleLocal}<p class="text-muted">{scheduleHint(wave, view.waves)}</p>{/if}
      </div>
    {/if}

    <h4 class="how">{allPosted ? 'What was posted' : 'How it will look'}</h4>
    {#if sameText}<p class="text-warn">Bluesky and Mastodon will get exactly the same text.</p>{/if}
    {#each autos as { channel, post } (post.id)}
      {@const body = shownBody(post)}
      {@const line = statusLine(post, channel)}
      <article class="preview" aria-label="{channel.label} {waveName} preview">
        <h5 class="preview-title">
          {channel.label} · {waveName}
          {#if channel.account}<span class="account text-muted">{channel.account}</span>{/if}
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
              onDirtyChange={(d) => (composerDirty = d)}
            />
          {/key}
        {:else}
          <OutreachPostPreview {view} {channel} {post} {body} />
          {#if line.kind === 'posted' || line.kind === 'failed' || line.kind === 'queued'}<p class="status status-{line.kind}">{line.text}</p>{/if}
          <p class="row">
            {#if post.status !== 'posted' && post.status !== 'queued'}
              <Button onclick={() => (customizingId = post.id)} disabled={busy || customizingId !== null}>Customize…</Button>
            {/if}
            {#if cardButtons(post, channel, view.available).takedown}
              <Button onclick={() => { takedownError = null; takedownTarget = { post, channel }; }} disabled={busy}>Delete and redo…</Button>
            {/if}
            {#if post.customized && post.status !== 'posted' && post.status !== 'queued'}
              <Button onclick={() => useShared(post)} disabled={busy || customizingId !== null}>Use shared message</Button>
            {/if}
          </p>
        {/if}
      </article>
    {/each}

  {/if}

  <p class="text-muted" role="status">{status || shownPostLine}</p>
  <p class="text-error" role="alert">{errorText}</p>
</section>

{#if takedownTarget}
  <OutreachPostDialog
    mode="takedown"
    {view}
    items={[takedownTarget]}
    inFlight={takedownBusy}
    errorMessage={takedownError}
    onConfirm={() => void confirmTakedown()}
    onClose={() => !takedownBusy && (takedownTarget = null)}
  />
{/if}
{#if postOpen}
  <OutreachPostDialog
    mode={postSnapshot.every((i) => i.post.status === 'failed') ? 'retry' : 'post'}
    {view}
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
  .when {
    margin: var(--space-4) 0 var(--space-2);
  }
  .when-line {
    margin: 0 0 var(--space-2);
  }
  .schedule {
    margin: var(--space-2) 0;
  }
  .schedule input {
    max-width: 100%;
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
  .account {
    margin-left: var(--space-2);
    font-size: var(--fs-sm);
    font-weight: 400;
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
  .status {
    font-weight: 600;
    margin: var(--space-2) 0;
  }
  [role='status']:empty,
  [role='alert']:empty {
    display: none;
  }
</style>
