<script lang="ts">
  // Share a guide. Bluesky has a compose intent URL; Mastodon has no
  // universal one (every server is different), so it gets a "copy post"
  // button. Phones get the native share sheet when it exists.
  interface Props {
    text: string;
    path: string;
  }
  let { text, path }: Props = $props();

  const url = $derived(typeof window === 'undefined' ? path : new URL(path, window.location.origin).href);
  const full = $derived(`${text} ${url}`);
  const bsky = $derived(`https://bsky.app/intent/compose?text=${encodeURIComponent(full)}`);
  const canShare = typeof navigator !== 'undefined' && 'share' in navigator;

  let copied = $state<'post' | 'link' | null>(null);
  async function copy(what: 'post' | 'link') {
    try {
      await navigator.clipboard.writeText(what === 'post' ? full : url);
      copied = what;
      setTimeout(() => (copied = null), 1800);
    } catch {
      copied = null;
    }
  }
  async function nativeShare() {
    try {
      await navigator.share({ text, url });
    } catch {
      // Cancelled or unsupported: nothing to do.
    }
  }
</script>

<div class="share" role="group" aria-label="Share this guide">
  <span class="label">Share</span>
  <a class="btn" href={bsky} target="_blank" rel="noopener noreferrer">Bluesky ↗</a>
  <button type="button" class="btn" onclick={() => copy('post')}>{copied === 'post' ? 'Copied ✓' : 'Copy post for Mastodon'}</button>
  <button type="button" class="btn" onclick={() => copy('link')}>{copied === 'link' ? 'Copied ✓' : 'Copy link'}</button>
  {#if canShare}
    <button type="button" class="btn" onclick={nativeShare}>Share…</button>
  {/if}
</div>

<style>
  .share {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-2);
  }
  .label {
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--text-faint);
  }
  .btn {
    font: inherit;
    font-size: var(--fs-base);
    padding: 4px 12px;
    border-radius: 999px;
    border: var(--border-w) solid var(--border-strong);
    background: var(--bg-panel);
    color: var(--text);
    text-decoration: none;
    cursor: pointer;
  }
  .btn:hover {
    background: var(--bg-subtle);
  }
</style>
