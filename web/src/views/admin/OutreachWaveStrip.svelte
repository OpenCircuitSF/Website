<!--
  The line naming an Outreach section (#0622): its number, its name and when it
  goes out. The number, the word and the time are the cues, none of them a
  colour. The root is a heading because HTML allows one inside summary, and
  this goes in both a summary and a plain div. It stays inline-flex because a
  block child pushes the summary's disclosure triangle onto its own line.
-->
<script lang="ts">
  import type { WaveStrip } from '../../lib/outreachMessage';

  interface Props {
    strip: WaveStrip;
  }
  let { strip }: Props = $props();
</script>

<h3 class="wave-strip">
  <span class="ordinal">{strip.ordinal}</span>
  <strong class="name">{strip.label}</strong>
  <span class="timing">{strip.text}</span>
</h3>

<style>
  .wave-strip {
    display: inline-flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: var(--space-2);
    margin: 0;
    font-size: var(--fs-base);
    font-weight: 400;
  }
  .ordinal {
    font-family: var(--font-mono);
    font-weight: 600;
    border: var(--border-w) solid var(--border-strong);
    border-radius: var(--radius);
    padding: 0 var(--space-1);
  }
  .name {
    font-size: var(--fs-md);
    text-transform: uppercase;
  }
  .timing {
    color: var(--text-muted);
  }
</style>
