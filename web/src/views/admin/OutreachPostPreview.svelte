<!--
  Chooses the preview for a post's channel (#0583): Bluesky and Mastodon get
  their platform-faithful mock, any other channel the plain text and count.
-->
<script lang="ts">
  import { countCharacters, isOverLimit } from '../../lib/outreach';
  import type { OutreachChannel, OutreachPost, WorkshopOutreach } from '../../lib/types';
  import OutreachBlueskyPreview from './OutreachBlueskyPreview.svelte';
  import OutreachMastodonPreview from './OutreachMastodonPreview.svelte';

  interface Props {
    view: Pick<WorkshopOutreach, 'workshop'> & { bluesky_card?: 'short' | 'canonical'; card_host?: string; thumb_ok?: boolean };
    channel: OutreachChannel;
    post: OutreachPost;
    body?: string;
  }
  let { view, channel, post, body }: Props = $props();

  const text = $derived(body ?? post.body);
  const counted = $derived(countCharacters(channel, text, post.prepared));
  const over = $derived(isOverLimit(channel, counted.count));
</script>

{#if channel.name === 'bluesky'}
  <OutreachBlueskyPreview {view} {channel} {post} {body} />
{:else if channel.name === 'mastodon'}
  <OutreachMastodonPreview {view} {channel} {post} {body} />
{:else}
  <pre class="body">{text}</pre>
  <p class={over ? 'text-error' : 'text-muted'}>
    {counted.exact ? '' : '≈ '}{counted.count}{channel.char_limit > 0 ? ` / ${channel.char_limit}` : ''} characters{over ? ' — over the limit' : ''}
  </p>
{/if}

<style>
  .body {
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    font-family: var(--font);
    margin: 0 0 var(--space-2);
  }
</style>
