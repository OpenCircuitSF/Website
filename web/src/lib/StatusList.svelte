<script lang="ts">
  // Terminal motif (PRD §4.4): rows of "[ OK ] label". Ported from
  // placeholder/index.html's .status / .status .ok.
  //
  // The "[ OK ]" glyph is aria-hidden: it carries no meaning a screen reader
  // needs beyond the label text itself, so it is marked aria-hidden rather
  // than letting a screen reader read "left bracket O K right bracket"
  // before every item.
  interface Props {
    items: string[];
  }

  let { items }: Props = $props();
</script>

<ul class="status-list">
  {#each items as item, i (i + item)}
    <li>
      <span class="ok" aria-hidden="true">[ OK ]</span>
      <span>{item}</span>
    </li>
  {/each}
</ul>

<style>
  .status-list {
    display: grid;
    gap: var(--space-2);
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .status-list li {
    display: grid;
    grid-template-columns: auto 1fr;
    gap: var(--space-3);
    align-items: baseline;
    font-family: var(--font-mono);
    font-size: clamp(13px, 2.3vw, 15px);
    color: var(--text-muted);
  }

  .ok {
    color: var(--accent);
    white-space: nowrap;
  }
</style>
