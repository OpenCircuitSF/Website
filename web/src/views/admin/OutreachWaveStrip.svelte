<!--
  The line naming an Outreach section (#0622, #0625): its number, its name and
  when it goes out. The number, the word and the time are the cues, none of them
  a colour. It is the accordion header for its section: a heading holding a
  native button. One section is always open, so the open row's button says so
  with aria-disabled and activating it does nothing. The chevron is a flex item,
  so it cannot take its own line.
-->
<script lang="ts">
  import type { WaveStrip } from '../../lib/outreachMessage';

  interface Props {
    strip: WaveStrip;
    open: boolean;
    headId: string;
    panelId: string;
    onSelect: () => void;
  }
  let { strip, open, headId, panelId, onSelect }: Props = $props();
</script>

<h3 class="wave-strip">
  <button
    type="button"
    id={headId}
    class="strip-button"
    aria-expanded={open ? 'true' : 'false'}
    aria-controls={panelId}
    aria-disabled={open ? 'true' : undefined}
    onclick={() => { if (!open) onSelect(); }}
  >
    <span class="chevron" aria-hidden="true">{open ? '▾' : '▸'}</span>
    <span class="strip-text">
      <span class="strip-name"><span class="ordinal">{strip.ordinal}</span> <strong class="name">{strip.label}</strong></span>
      <span class="timing">{strip.text}</span>
    </span>
  </button>
</h3>

<style>
  .wave-strip {
    flex: 1 1 16rem;
    min-width: 0;
    margin: 0;
    font-size: var(--fs-base);
    font-weight: 400;
  }
  .strip-button {
    display: flex;
    align-items: baseline;
    gap: var(--space-2);
    width: 100%;
    min-height: 0;
    margin: 0;
    padding: 0;
    border: none;
    background: none;
    color: inherit;
    font: inherit;
    text-align: left;
    cursor: pointer;
  }
  .strip-button[aria-disabled='true'] {
    cursor: default;
  }
  .chevron {
    flex: none;
  }
  .strip-text {
    display: flex;
    flex: 1;
    flex-wrap: wrap;
    align-items: baseline;
    column-gap: var(--space-2);
    min-width: 0;
  }
  .strip-name {
    display: inline-flex;
    flex: none;
    align-items: baseline;
    gap: var(--space-2);
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
    min-width: 0;
    max-width: 100%;
    color: var(--text-muted);
  }
</style>
