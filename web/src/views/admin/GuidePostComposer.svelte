<!--
  The new-draft form for Guide outreach (#0660, #0665): pick a guide, tick
  one or more channels, and a campaign. The text starts as the guide's post
  copy. Each ticked channel gets its own draft and short link, and its own
  counter, measured the way the server measures it (lib/guidePosts.ts). An
  optional time schedules every draft just made.
-->
<script lang="ts">
  import Button from '../../lib/Button.svelte';
  import { createGuidePost, scheduleGuidePost } from '../../lib/api';
  import { adminSettingsHash } from '../../lib/adminRouter';
  import { fromLocalInputValue } from '../../lib/outreach';
  import {
    allCreated,
    bodyAfterGuideChange,
    campaignHint,
    counterText,
    createAcross,
    createButtonLabel,
    defaultScheduleAt,
    defaultTickedChannels,
    isValidCampaign,
    notConnectedNote,
    outcomeSummary,
    overLimit,
  } from '../../lib/guidePosts';
  import type { GuidePostChannel, GuideTopic } from '../../lib/types';

  interface Props {
    topics: GuideTopic[];
    channels: GuidePostChannel[];
    suggestedCampaign: string;
    postHour: number;
    initialGuide: string | null;
    onCreated: (message: string, done: boolean) => void;
    onCancel: () => void;
  }
  let { topics, channels, suggestedCampaign, postHour, initialGuide, onCreated, onCancel }: Props = $props();

  const defaultFor = (slug: string) => topics.find((t) => t.slug === slug)?.default_body ?? '';

  // Seeded once from the props: the form owns its fields after that.
  // svelte-ignore state_referenced_locally
  let slug = $state(initialGuide && topics.some((t) => t.slug === initialGuide) ? initialGuide : (topics[0]?.slug ?? ''));
  // svelte-ignore state_referenced_locally
  let ticked = $state<string[]>(defaultTickedChannels(channels));
  // svelte-ignore state_referenced_locally
  let campaign = $state(suggestedCampaign);
  // svelte-ignore state_referenced_locally
  let body = $state(defaultFor(slug));
  // svelte-ignore state_referenced_locally
  let scheduleValue = $state(defaultScheduleAt(new Date(), postHour));
  let inFlight = $state(false);
  let error = $state<string | null>(null);

  const chosen = $derived(channels.filter((c) => c.configured && ticked.includes(c.name)));
  const overOn = $derived(chosen.filter((c) => overLimit(c, body, null)));
  const scheduleAt = $derived(scheduleValue.trim() === '' ? null : fromLocalInputValue(scheduleValue));
  const scheduleOk = $derived(scheduleValue.trim() === '' || scheduleAt !== null);
  const campaignOk = $derived(isValidCampaign(campaign));
  const canCreate = $derived(
    chosen.length > 0 && overOn.length === 0 && body.trim() !== '' && campaignOk && scheduleOk && !inFlight && slug !== '',
  );

  function changeGuide(next: string): void {
    body = bodyAfterGuideChange(body, defaultFor(slug), defaultFor(next));
    slug = next;
  }

  function toggle(name: string, on: boolean): void {
    ticked = on ? [...ticked.filter((n) => n !== name), name] : ticked.filter((n) => n !== name);
  }

  async function create(): Promise<void> {
    if (!canCreate) return;
    inFlight = true;
    error = null;
    const at = scheduleAt;
    const req = { slug, campaign: campaign.trim(), body };
    const outcomes = await createAcross(
      chosen,
      (channel) => createGuidePost({ ...req, channel }),
      at === null ? null : (id) => scheduleGuidePost(id, at),
    );
    inFlight = false;
    const message = outcomeSummary(outcomes, at);
    if (outcomes.every((o) => o.post === null)) {
      error = message;
      return;
    }
    // Keep the form for the channels that failed, so Create retries only those.
    ticked = outcomes.filter((o) => o.post === null).map((o) => o.channel);
    onCreated(message, allCreated(outcomes));
  }
</script>

<form class="composer" onsubmit={(e) => { e.preventDefault(); void create(); }}>
  <label>Guide
    <select value={slug} onchange={(e) => changeGuide((e.currentTarget as HTMLSelectElement).value)}>
      {#each topics as t (t.slug)}
        <option value={t.slug}>{t.title}</option>
      {/each}
    </select>
  </label>

  <fieldset>
    <legend>Channels</legend>
    {#each channels as c (c.name)}
      <label class="check">
        <input
          type="checkbox"
          value={c.name}
          checked={c.configured && ticked.includes(c.name)}
          disabled={!c.configured}
          onchange={(e) => toggle(c.name, (e.currentTarget as HTMLInputElement).checked)}
        /> {c.label}
      </label>
      {#if notConnectedNote(c)}
        <p class="note">{c.label} is not connected. Connect it under
          <a href={adminSettingsHash('outreach')}>Settings → Outreach</a> to draft, schedule or post there.</p>
      {/if}
    {/each}
  </fieldset>

  <label>Campaign
    <input type="text" bind:value={campaign} aria-invalid={!campaignOk} />
  </label>
  {#if !campaignOk}
    <p class="note">{campaignHint}</p>
  {/if}

  <label>Text
    <textarea rows="6" bind:value={body}></textarea>
  </label>
  {#each chosen as c (c.name)}
    {@const over = overLimit(c, body, null)}
    <p class="counter" class:over data-testid={`guide-post-counter-${c.name}`}>
      {c.label}: {counterText(c, body, null)}{#if over}{' '}Too long for {c.label}.{/if}
    </p>
  {/each}

  <label>Schedule for (Pacific, optional)
    <input type="datetime-local" bind:value={scheduleValue} aria-invalid={!scheduleOk} />
  </label>
  <p class="note">
    {scheduleValue.trim() === '' ? 'Empty: drafts only. Schedule them later from their cards.' : 'Every draft made here is scheduled for this time.'}
  </p>

  {#if error}
    <p class="text-error" role="alert">{error}</p>
  {/if}

  <div class="row">
    <Button type="submit" variant="primary" disabled={!canCreate}>{inFlight ? 'Creating…' : createButtonLabel(chosen.length, scheduleValue)}</Button>
    {#if scheduleValue.trim() !== ''}
      <Button onclick={() => (scheduleValue = '')} disabled={inFlight}>Drafts only</Button>
    {/if}
    <Button onclick={onCancel} disabled={inFlight}>Cancel</Button>
  </div>
</form>

<style>
  .composer {
    display: grid;
    gap: var(--space-3);
    margin-bottom: var(--space-4);
  }
  label {
    display: grid;
    gap: var(--space-1);
  }
  .check {
    display: inline-flex;
    gap: var(--space-2);
    align-items: center;
  }
  fieldset {
    border: var(--border-w) solid var(--border);
    border-radius: var(--radius);
    padding: var(--space-3);
  }
  .note {
    color: var(--text-muted);
    margin: 0;
  }
  .counter {
    margin: 0;
    color: var(--text-muted);
  }
  .counter.over {
    color: var(--error, var(--text));
    font-weight: 600;
  }
  .row {
    display: flex;
    gap: var(--space-2);
  }
</style>
