<!--
  The new-draft form for Guide outreach (#0660): pick a guide, a channel and
  a campaign; the text starts as the guide's post copy. The counter measures
  the short link the way the server does (lib/guidePosts.ts).
-->
<script lang="ts">
  import Button from '../../lib/Button.svelte';
  import { createGuidePost } from '../../lib/api';
  import { adminSettingsHash } from '../../lib/adminRouter';
  import { asSentence, bodyAfterGuideChange, campaignHint, counterText, isValidCampaign, notConnectedNote, overLimit } from '../../lib/guidePosts';
  import type { GuidePostChannel, GuidePostCreated, GuideTopic } from '../../lib/types';

  interface Props {
    topics: GuideTopic[];
    channels: GuidePostChannel[];
    suggestedCampaign: string;
    initialGuide: string | null;
    onCreated: (result: GuidePostCreated) => void;
    onCancel: () => void;
  }
  let { topics, channels, suggestedCampaign, initialGuide, onCreated, onCancel }: Props = $props();

  const defaultFor = (slug: string) => topics.find((t) => t.slug === slug)?.default_body ?? '';

  // Seeded once from the props: the form owns its fields after that.
  // svelte-ignore state_referenced_locally
  let slug = $state(initialGuide && topics.some((t) => t.slug === initialGuide) ? initialGuide : (topics[0]?.slug ?? ''));
  // svelte-ignore state_referenced_locally
  let channelName = $state(channels.find((c) => c.name === 'bluesky')?.name ?? channels[0]?.name ?? '');
  // svelte-ignore state_referenced_locally
  let campaign = $state(suggestedCampaign);
  // svelte-ignore state_referenced_locally
  let body = $state(defaultFor(slug));
  let inFlight = $state(false);
  let error = $state<string | null>(null);

  const channel = $derived(channels.find((c) => c.name === channelName) ?? channels[0]);
  const over = $derived(channel ? overLimit(channel, body, null) : false);
  const campaignOk = $derived(isValidCampaign(campaign));
  const canCreate = $derived(!!channel && !over && body.trim() !== '' && campaignOk && !inFlight && slug !== '');

  function changeGuide(next: string): void {
    body = bodyAfterGuideChange(body, defaultFor(slug), defaultFor(next));
    slug = next;
  }

  async function create(): Promise<void> {
    if (!canCreate) return;
    inFlight = true;
    error = null;
    try {
      const result = await createGuidePost({ slug, channel: channelName, campaign: campaign.trim(), body });
      onCreated(result);
    } catch (e) {
      error = asSentence(e instanceof Error ? e.message : String(e));
    } finally {
      inFlight = false;
    }
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
    <legend>Channel</legend>
    {#each channels as c (c.name)}
      <label class="radio"><input type="radio" name="guide-post-channel" value={c.name} bind:group={channelName} /> {c.label}</label>
      {#if notConnectedNote(c)}
        <p class="note">{c.label} is not connected. You can write drafts; connect it under
          <a href={adminSettingsHash('outreach')}>Settings → Outreach</a> to schedule or post.</p>
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
  {#if channel}
    <p class="counter" class:over data-testid="guide-post-counter">
      {counterText(channel, body, null)}{#if over}{' '}Too long for {channel.label}.{/if}
    </p>
  {/if}

  {#if error}
    <p class="text-error" role="alert">{error}</p>
  {/if}

  <div class="row">
    <Button type="submit" variant="primary" disabled={!canCreate}>{inFlight ? 'Creating…' : 'Create draft'}</Button>
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
  .radio {
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
