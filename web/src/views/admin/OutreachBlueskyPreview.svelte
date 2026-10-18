<!--
  A Bluesky post as it will look (#0583): the text with hashtags and links
  styled, the external card Bluesky builds from the link (picture, title,
  clamped description, host) and the character count. The picture appears only
  for an uploaded /media/ cover; without one the card goes out without a
  picture and the preview says so. The host is the short link's, or the
  site's own when Server settings send the canonical URL.
-->
<script lang="ts">
  import { countCharacters, isOverLimit } from '../../lib/outreach';
  import { blueskyCard, segments } from '../../lib/outreachPreview';
  import type { OutreachChannel, OutreachPost, WorkshopOutreach } from '../../lib/types';

  interface Props {
    view: Pick<WorkshopOutreach, 'workshop'> & { bluesky_card?: 'short' | 'canonical' };
    channel: OutreachChannel;
    post: OutreachPost;
    /** The text to draw; defaults to the stored body. */
    body?: string;
  }
  let { view, channel, post, body }: Props = $props();

  const text = $derived(body ?? post.body);
  const card = $derived(blueskyCard(view, post, typeof location === 'undefined' ? '' : location.host));
  const counted = $derived(countCharacters(channel, text, post.prepared));
  const over = $derived(isOverLimit(channel, counted.count));
</script>

<div class="preview bluesky">
  <p class="text">{#each segments(text) as s, i (i)}{#if s.kind === 'text'}{s.text}{:else}<span class={s.kind}>{s.text}</span>{/if}{/each}</p>
  <div class="card">
    {#if card.image}
      <img src={card.image} alt="" loading="lazy" />
    {:else}
      <p class="no-image text-muted">No image: the card goes out without a picture.</p>
    {/if}
    <div class="card-text">
      <p class="card-title">{card.title}</p>
      {#if card.description}<p class="card-desc text-muted">{card.description}</p>{/if}
      <p class="card-host text-muted">{card.host}</p>
    </div>
  </div>
  <p class={over ? 'text-error' : 'text-muted'}>
    {counted.exact ? '' : '≈ '}{counted.count}{channel.char_limit > 0 ? ` / ${channel.char_limit}` : ''} characters{over ? ' — over the limit' : ''}
  </p>
</div>

<style>
  .text {
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    margin: 0 0 var(--space-2);
  }
  .hashtag,
  .link {
    color: var(--accent-text, var(--accent));
  }
  .link {
    text-decoration: underline;
  }
  .card {
    border: var(--border-w) solid var(--border);
    border-radius: var(--radius);
    overflow: hidden;
    margin: 0 0 var(--space-2);
  }
  img {
    display: block;
    width: 100%;
    aspect-ratio: 1.91 / 1;
    object-fit: cover;
  }
  .no-image {
    margin: 0;
    padding: var(--space-2) var(--space-3);
    font-size: var(--fs-sm);
    border-bottom: var(--border-w) solid var(--border);
  }
  .card-text {
    padding: var(--space-2) var(--space-3);
  }
  .card-text p {
    margin: 0;
  }
  .card-title {
    font-weight: 600;
  }
  .card-desc {
    display: -webkit-box;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
    font-size: var(--fs-sm);
  }
  .card-host {
    font-size: var(--fs-sm);
  }
</style>
