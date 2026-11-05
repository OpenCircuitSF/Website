<!--
  The pinned section tabs on the workshop outreach page (#0640): one tab per
  section, numbered and named, with the selected section's one line under them.
  Exactly one section is shown at a time; the parent owns which. Left/Right
  (and Home/End) move between tabs, the usual tablist keyboard rule. The
  .wave-pin class is app.css's sticky rule (#0622); nothing here may set
  overflow or contain, which would stop the pin.
-->
<script lang="ts">
  import { sectionHeadId, sectionPanelId } from '../../lib/outreachCards';
  import { lineParts, sectionLine } from '../../lib/outreachSection';
  import type { WorkshopOutreach } from '../../lib/types';

  interface Props {
    view: WorkshopOutreach;
    selected: string;
    unsaved: Record<string, boolean>;
    onSelect: (name: string) => void;
  }
  let { view, selected, unsaved, onSelect }: Props = $props();

  const parts = $derived(lineParts(sectionLine(view, selected).text));

  function onKeydown(e: KeyboardEvent): void {
    const names = view.waves.map((w) => w.name);
    const i = names.indexOf(selected);
    let next = -1;
    if (e.key === 'ArrowRight') next = (i + 1) % names.length;
    else if (e.key === 'ArrowLeft') next = (i - 1 + names.length) % names.length;
    else if (e.key === 'Home') next = 0;
    else if (e.key === 'End') next = names.length - 1;
    if (next < 0 || names.length === 0) return;
    e.preventDefault();
    onSelect(names[next]);
  }
</script>

<div class="wave-pin section-tabs" id="outreach-section-tabs">
  <div role="tablist" aria-label="Section" class="tablist" tabindex="-1" onkeydown={onKeydown}>
    {#each view.waves as w, i (w.name)}
      <button
        type="button"
        role="tab"
        class="tab"
        id={sectionHeadId(w.name)}
        aria-selected={w.name === selected ? 'true' : 'false'}
        aria-controls={sectionPanelId(w.name)}
        tabindex={w.name === selected ? 0 : -1}
        onclick={() => onSelect(w.name)}
      >
        <span class="ordinal">{i + 1}</span> <span class="name">{w.label}</span>{#if unsaved[w.name]}<span class="tab-unsaved"> · not saved</span>{/if}
      </button>
    {/each}
  </div>
  <p class="section-line">{parts[0]}{#if parts[1]}<span class="when">{parts[1]}</span>{/if}{parts[2]}</p>
</div>

<style>
  .tablist {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
  }
  .tab {
    display: inline-flex;
    align-items: baseline;
    gap: var(--space-1);
    margin: 0;
    padding: var(--space-1) var(--space-2);
    border: var(--border-w) solid var(--border);
    border-radius: var(--radius);
    background: none;
    color: inherit;
    font: inherit;
    cursor: pointer;
  }
  .tab[aria-selected='true'] {
    border-color: var(--accent);
  }
  .tab[aria-selected='true'] .name {
    text-transform: uppercase;
    font-weight: 600;
  }
  .ordinal {
    font-family: var(--font-mono);
    font-weight: 600;
  }
  .tab-unsaved {
    color: var(--warning);
  }
  .section-line {
    margin: var(--space-2) 0 0;
    color: var(--text-muted);
  }
  .when {
    white-space: nowrap;
  }
</style>
