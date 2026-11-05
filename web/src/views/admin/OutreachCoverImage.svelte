<!--
  A post preview's cover picture (#0622): small by default, with a control that
  shows it full size. Toggling is per preview and is not saved.
-->
<script lang="ts">
  import Button from '../../lib/Button.svelte';

  interface Props {
    src: string;
    badge?: string;
  }
  let { src, badge }: Props = $props();

  let full = $state(false);
</script>

<div class="cover" class:full>
  <span class="frame">
    <img {src} alt="" loading="lazy" />
    {#if badge}<span class="alt-badge">{badge}</span>{/if}
  </span>
  <Button aria-expanded={full} onclick={() => (full = !full)}>{full ? 'Small picture' : 'Full-size picture'}</Button>
</div>

<style>
  .cover {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-2);
    margin: 0 0 var(--space-2);
  }
  .frame {
    display: block;
    position: relative;
    flex: none;
    width: 6rem;
  }
  .cover.full .frame {
    width: 100%;
  }
  img {
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
    background: var(--bg-panel);
    color: var(--text);
    border-radius: var(--radius);
  }
</style>
