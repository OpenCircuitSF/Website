<script lang="ts">
  // Share a guide: one button that copies the page link. The guide pages
  // unfurl into a card on Bluesky, Mastodon and elsewhere, so the link is all
  // a reader needs to paste wherever they post. If the clipboard is refused,
  // the link is shown selected so it can be copied by hand.
  interface Props {
    path: string;
  }
  let { path }: Props = $props();

  const url = $derived(typeof window === 'undefined' ? path : new URL(path, window.location.origin).href);

  let state = $state<'idle' | 'copied' | 'failed'>('idle');
  let timer: ReturnType<typeof setTimeout> | undefined;
  async function copy() {
    clearTimeout(timer);
    try {
      await navigator.clipboard.writeText(url);
      state = 'copied';
      timer = setTimeout(() => (state = 'idle'), 1800);
    } catch {
      state = 'failed';
    }
  }
  function selectAll(e: FocusEvent) {
    (e.currentTarget as HTMLInputElement).select();
  }
</script>

<div class="share">
  <button type="button" class="btn" onclick={copy}>{state === 'copied' ? 'Link copied ✓' : 'Share'}</button>
  {#if state === 'failed'}
    <!-- svelte-ignore a11y_autofocus -->
    <input class="manual" readonly value={url} aria-label="Link to this guide" onfocus={selectAll} autofocus />
  {/if}
  <span class="sr-only" role="status" aria-live="polite">{state === 'copied' ? 'Link copied' : ''}</span>
</div>

<style>
  .share {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-2);
  }
  .btn {
    font: inherit;
    font-size: var(--fs-base);
    padding: 4px 14px;
    border-radius: 999px;
    border: var(--border-w) solid var(--border-strong);
    background: var(--bg-panel);
    color: var(--text);
    cursor: pointer;
  }
  .btn:hover {
    background: var(--bg-subtle);
  }
  .manual {
    font: inherit;
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    min-width: 0;
    flex: 1 1 16rem;
    padding: 4px 8px;
    border: var(--border-w) solid var(--border-strong);
    border-radius: var(--radius);
    background: var(--bg-panel);
    color: var(--text);
  }
</style>
