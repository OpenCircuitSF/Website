<!--
  The Outreach home timeline (#0646): each published event in the next 30
  days as a lane, its posts placed at their times and styled by status, and
  under each lane what every section still needs. The track is a picture for
  the mouse; the lane header, gap list and an sr-only table carry the same
  facts and links for the keyboard and screen readers. Logic is in
  lib/outreachTimeline.ts. Desktop only.
-->
<script lang="ts">
  import { formatDateTime } from '../../lib/admin';
  import { boardRows } from '../../lib/outreachCards';
  import { handChannelLabel } from '../../lib/outreachHandCopy';
  import { outreachRowAriaLabel, outreachRowButtonLabel } from '../../lib/outreachList';
  import { timelineLanes } from '../../lib/outreachTimeline';
  import type { OutreachTimeline } from '../../lib/types';

  let { timeline }: { timeline: OutreachTimeline } = $props();
  // The server's clock is the one "now" for the axis and the section lines.
  const built = $derived(timelineLanes(timeline, new Date(timeline.now)));
  const rowSummary = (started: boolean) => (started ? { counts: {} } : undefined);
</script>

<p class="legend text-muted">
  <span class="pill posted">Posted</span>
  <span class="pill scheduled">Scheduled</span>
  <span class="pill drafted">Drafted</span>
  <span class="pill missing">! Not written or failed</span>
</p>

<div class="axis" aria-hidden="true">
  <span class="gutter-label">Earlier</span>
  <div class="track">
    {#each built.axis.ticks as tick (tick.at)}
      <span class="tick" style="left: {tick.pct}%">{tick.label}</span>
    {/each}
  </div>
</div>

{#each built.lanes as lane (lane.event.workshop.id)}
  {@const ev = lane.event}
  <section class="lane" class:not-started={!lane.started} aria-label={ev.workshop.title}>
    <header class="lane-head">
      <a class="lane-title" href={lane.href}>{ev.workshop.title}</a>
      {#if ev.workshop.starts_at}<span class="text-muted">{formatDateTime(ev.workshop.starts_at)}</span>{/if}
      <span class="badge badge-{lane.chip.tone}">{lane.chip.label}</span>
      <a class="row-button" href={lane.href} aria-label={outreachRowAriaLabel(rowSummary(lane.started), ev.workshop.title)}>{outreachRowButtonLabel(rowSummary(lane.started))}</a>
    </header>

    <div class="rows" aria-hidden="true">
      {#each lane.rows as row (row.label)}
        {@const levels = Math.max(1, ...row.markers.map((m) => m.level + 1))}
        <div class="row" style="--levels: {levels}">
          <span class="row-label">{row.label}</span>
          <div class="gutter">
            {#each row.markers.filter((m) => m.earlier) as m (m.key)}
              <a class="pill {m.kind}" tabindex="-1" href={m.href} title={m.label} style="top: calc({m.level} * var(--pill-h))">{m.kind === 'missing' || m.kind === 'failed' ? '! ' : ''}{m.waveLabel}</a>
            {/each}
          </div>
          <div class="track">
            <span class="today"></span>
            {#if lane.eventPct !== null}<span class="event" style="left: {lane.eventPct}%">Event</span>{/if}
            {#each row.markers.filter((m) => !m.earlier) as m (m.key)}
              <a class="pill {m.kind}" tabindex="-1" href={m.href} title={m.label} style="left: {m.pct}%; top: calc({m.level} * var(--pill-h))">{m.kind === 'missing' || m.kind === 'failed' ? '! ' : ''}{m.waveLabel}</a>
            {/each}
          </div>
        </div>
      {/each}
    </div>

    <ul class="gaps">
      {#each lane.gaps as g (g.wave)}
        <li><a href={g.href}>{g.label}: {g.text}</a></li>
      {/each}
    </ul>

    {#if lane.started}
      {@const board = boardRows(ev)}
      <table class="sr-only">
        <caption>{ev.workshop.title}</caption>
        <thead><tr><th>Section</th>{#each ev.channels as c (c.name)}<th>{handChannelLabel(c)}</th>{/each}</tr></thead>
        <tbody>
          {#each board as br (br.wave)}
            <tr><th>{br.label}</th>{#each br.cells as cell (cell.channel)}<td>{cell.status}{cell.detail ? ' ' + cell.detail : ''}</td>{/each}</tr>
          {/each}
        </tbody>
      </table>
    {/if}
  </section>
{/each}

<style>
  .legend { display: flex; gap: var(--space-3); align-items: center; }
  .axis, .row { display: grid; grid-template-columns: 14rem 5rem 1fr; gap: var(--space-2); }
  /* The axis sits outside any lane, so it adds the lane's 3px border and
     padding plus the row's label-column gap to line up with the markers. */
  .axis { margin-left: calc(14rem + 3px + 2 * var(--space-2)); grid-template-columns: 5rem 1fr; font-size: 0.85em; color: var(--text-muted); }
  .track { position: relative; min-height: 1.5rem; }
  .tick { position: absolute; white-space: nowrap; transform: translateX(-50%); }
  .tick:first-child { transform: none; }
  .lane { border-left: 3px solid var(--border); padding-left: var(--space-2); margin-top: var(--space-4); }
  .lane.not-started { border-left-color: var(--warning); }
  .lane-head { display: flex; gap: var(--space-3); align-items: center; }
  .lane-title { font-weight: 600; }
  .row { --pill-h: 1.6rem; min-height: calc(var(--levels) * var(--pill-h)); margin-top: var(--space-1); }
  .row .track, .gutter { position: relative; min-height: calc(var(--levels) * var(--pill-h)); }
  .row-label { color: var(--text-muted); }
  .today { position: absolute; left: 0; top: 0; bottom: 0; border-left: 1px solid var(--accent); }
  .event { position: absolute; top: 0; bottom: 0; border-left: 2px solid var(--text-muted); padding-left: 2px; font-size: 0.75em; color: var(--text-muted); }
  .pill { position: absolute; white-space: nowrap; font-size: 0.8em; padding: 0 var(--space-1); border-radius: var(--radius); border: var(--border-w) solid var(--accent); text-decoration: none; color: inherit; background: var(--bg-panel); z-index: 1; }
  .legend .pill { position: static; }
  .pill.posted { background: var(--success); border-color: var(--success); color: var(--bg-panel); }
  .pill.scheduled { border-style: solid; border-color: var(--success); }
  .pill.drafted { border-style: dashed; }
  .pill.missing, .pill.failed { border-style: dashed; border-color: var(--danger); color: var(--danger); }
  .gaps { margin: var(--space-2) 0 0; padding-left: var(--space-4); }
  .row-button {
    display: inline-block;
    padding: var(--space-1) var(--space-3);
    border: var(--border-w) solid var(--accent);
    border-radius: var(--radius);
    color: var(--accent);
    text-decoration: none;
  }
</style>
