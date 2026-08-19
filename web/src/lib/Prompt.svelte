<script lang="ts">
  // Terminal motif (PRD §4.4): a single "> text" line — the recurring device
  // that opens a terminal section (e.g. "open_circuit_sf // san_francisco").
  // Ported from placeholder/index.html's .prompt.
  interface Props {
    text: string;
  }

  let { text }: Props = $props();
</script>

<p class="prompt">{text}</p>

<style>
  .prompt {
    margin: 0;
    font-family: var(--font-mono);
    font-size: clamp(13px, 2.4vw, 15px);
    color: var(--accent);
    overflow-wrap: anywhere;
  }

  /* The "> " chevron is decorative punctuation, not part of the copy, so it
   * uses --accent-dim rather than the full --accent the prose text carries —
   * PRD §4.2 names this exact chevron as an --accent-dim example ("rules,
   * dividers, the `>` chevron"), and placeholder/index.html (the validated
   * reference) implements it that way. */
  .prompt::before {
    content: '> ';
    color: var(--accent-dim);
  }
</style>
