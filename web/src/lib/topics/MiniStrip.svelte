<script lang="ts">
  // A small decorative row of lit LEDs for guide cards. Each card gets its
  // own colors from its position, so the index reads like a lit strip.
  import { NAMED_COLORS, displayCss } from '../guide/leds';

  interface Props {
    seed: number;
    count?: number;
  }
  let { seed, count = 6 }: Props = $props();
  const PICKS = ['Red', 'Orange', 'Yellow', 'Lime', 'Cyan', 'Blue', 'Purple', 'Magenta', 'HotPink'];
  const leds = $derived(
    Array.from({ length: count }, (_, i) => {
      const name = PICKS[(seed * 2 + i) % PICKS.length];
      return displayCss(NAMED_COLORS.find((c) => c.name === name)!.rgb, 200);
    }),
  );
</script>

<div class="mini" aria-hidden="true">
  {#each leds as c, i (i)}<span style:--led={c} style:--i={i}></span>{/each}
</div>

<style>
  .mini {
    display: flex;
    gap: 8px;
    padding: 8px 10px;
    background: var(--guide-pcb);
    border-radius: 4px;
  }
  span {
    width: 12px;
    height: 12px;
    border-radius: 2px;
    background: var(--led);
    box-shadow: 0 0 8px 2px var(--led);
    animation: breathe 2.4s ease-in-out infinite;
    animation-delay: calc(var(--i) * 0.2s);
  }
  @keyframes breathe {
    50% {
      opacity: 0.45;
    }
  }
  @media (prefers-reduced-motion: reduce) {
    span {
      animation: none;
    }
  }
</style>
