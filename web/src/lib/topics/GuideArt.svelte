<script lang="ts">
  // One guide illustration (a drawn PNG today, a photo later): 1x/2x srcset,
  // intrinsic size so the layout doesn't jump, scaled down to fit.
  import type { Art } from './soldering';

  let { art, class: cls = '' }: { art: Art; class?: string } = $props();
</script>

<img class="guide-art {cls}" src={art.src} srcset={art.srcset} width={art.width} height={art.height} alt={art.alt} loading="lazy" decoding="async" />

<style>
  .guide-art {
    display: block;
    max-width: 100%;
    height: auto;
  }
</style>
