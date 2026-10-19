<!--
  A Mastodon post as it will look (#0583): links and hashtags styled, the link
  preview Mastodon builds from the first URL (none when the image is
  attached, as Mastodon draws no card for a status with media), and the character count (every
  URL counts as 23). When the workshop exposes its cover's alt text (#0572) an
  image-attachment block shows the cover with an ALT badge, or says the image
  cannot go out because the alt text is missing.
-->
<script lang="ts">
  import { countCharacters, isOverLimit } from '../../lib/outreach';
  import { mastodonLinkCard, segments } from '../../lib/outreachPreview';
  import type { OutreachChannel, OutreachPost, WorkshopOutreach } from '../../lib/types';

  interface Props {
    view: Pick<WorkshopOutreach, 'workshop'> & { bluesky_card?: 'short' | 'canonical'; card_host?: string; thumb_ok?: boolean };
    channel: OutreachChannel;
    post: OutreachPost;
    body?: string;
  }
  let { view, channel, post, body }: Props = $props();

  const text = $derived(body ?? post.body);
  const card = $derived(mastodonLinkCard(view, text));
  const counted = $derived(countCharacters(channel, text, post.prepared));
  const over = $derived(isOverLimit(channel, counted.count));
  const cover = $derived(view.workshop.cover_image ?? null);
  // Only a workshop that exposes cover_alt can attach the image.
  const attachment = $derived(cover !== null && view.workshop.cover_alt !== undefined);
  const alt = $derived((view.workshop.cover_alt ?? '').trim());
  // Mastodon draws no link preview for a status that carries media.
  const attached = $derived(attachment && alt !== '');
</script>

<div class="preview mastodon">
  <p class="text">{#each segments(text) as s, i (i)}{#if s.kind === 'text'}{s.text}{:else}<span class={s.kind}>{s.text}</span>{/if}{/each}</p>
  {#if attachment && cover}
    {#if alt}
      <div class="attachment">
        <img src={cover} alt="" loading="lazy" />
        <span class="alt-badge">ALT</span>
      </div>
    {:else}
      <p class="text-warn">No image: alt text missing. Add the cover's alt text so the picture can go out.</p>
    {/if}
  {/if}
  {#if card && !attached}
    <div class="card">
      {#if card.image}<img src={card.image} alt="" loading="lazy" />{/if}
      <div class="card-text">
        <p class="card-host text-muted">{card.host}</p>
        <p class="card-title">{card.title}</p>
        {#if card.description}<p class="card-desc text-muted">{card.description}</p>{/if}
      </div>
    </div>
  {/if}
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
  .attachment {
    position: relative;
    margin: 0 0 var(--space-2);
  }
  .attachment img,
  .card img {
    display: block;
    width: 100%;
    aspect-ratio: 1.91 / 1;
    object-fit: cover;
  }
  .alt-badge {
    position: absolute;
    left: var(--space-2);
    bottom: var(--space-2);
    padding: 0 var(--space-2);
    font-size: var(--fs-sm);
    font-weight: 600;
    background: var(--bg, #000);
    color: var(--text, #fff);
    border-radius: var(--radius);
  }
  .card {
    border: var(--border-w) solid var(--border);
    border-radius: var(--radius);
    overflow: hidden;
    margin: 0 0 var(--space-2);
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
