<!--
  The click chart on the Outreach results view (#0546): clicks per UTC day,
  one line per channel (plus "Other links"), with a dashed marker on each day
  something was posted. All shaping is lib/outreachResults.ts's buildChart;
  this file is markup. Colors are the --series-* tokens only. A visually
  hidden table carries the same numbers for screen readers.
-->
<script lang="ts">
  import { buildChart, chartDescription, formatClicks } from '../../lib/outreachResults';
  import type { OutreachResults } from '../../lib/types';

  let { results }: { results: OutreachResults } = $props();

  const model = $derived(buildChart(results));
  const description = $derived(chartDescription(results));
</script>

{#if model}
  {#if model.empty}
    <p class="text-muted">No clicks in this window yet.</p>
  {:else}
    <svg
      class="chart"
      viewBox={`0 0 ${model.geo.width} ${model.geo.height}`}
      width="100%"
      role="img"
      aria-labelledby="outreach-chart-title outreach-chart-desc"
    >
      <title id="outreach-chart-title">Clicks per day</title>
      <desc id="outreach-chart-desc">{description}</desc>
      {#each model.yTicks as tick (tick.value)}
        <line class="grid" x1={model.geo.padLeft} x2={model.geo.width - model.geo.padRight} y1={tick.y} y2={tick.y} />
        <text class="axis" x={model.geo.padLeft - 6} y={tick.y} text-anchor="end" dominant-baseline="middle">{tick.value}</text>
      {/each}
      {#each model.xLabels as label (label.x)}
        <text class="axis" x={label.x} y={model.geo.height - 10} text-anchor="middle">{label.label}</text>
      {/each}
      {#each model.markers as marker (marker.date)}
        <g>
          <title>{marker.labels.join(', ')}</title>
          <line
            class="marker"
            style:stroke={marker.color}
            x1={marker.x}
            x2={marker.x}
            y1={model.geo.padTop}
            y2={model.geo.padTop + model.geo.innerH}
          />
          <circle style:fill={marker.color} cx={marker.x} cy={model.geo.padTop} r="3.5" />
        </g>
      {/each}
      {#each model.lines as line (line.id)}
        <polyline class="line" style:stroke={line.color} points={line.points} fill="none" />
      {/each}
    </svg>
    <p class="axis-note text-muted">
      Days are UTC.{model.partialLastDay ? ' The last day is today so far.' : ''}
      {#if model.markers.length > 0}Dashed lines mark days posts went out.{/if}
    </p>
    <ul class="legend">
      {#each model.lines as line (line.id)}
        <li>
          <span class="swatch" style:background={line.color}></span>
          {line.label}: {formatClicks(line.total)}
        </li>
      {/each}
    </ul>
    <table class="sr-only">
      <caption>Clicks per day</caption>
      <thead>
        <tr>
          <th scope="col">Date (UTC)</th>
          {#each model.lines as line (line.id)}
            <th scope="col">{line.label}</th>
          {/each}
        </tr>
      </thead>
      <tbody>
        {#each model.rows as row (row.date)}
          <tr>
            <th scope="row">{row.label}</th>
            {#each row.values as v, i (i)}
              <td>{v}</td>
            {/each}
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
{/if}

<style>
  .chart {
    display: block;
    max-width: 100%;
    height: auto;
  }
  .grid {
    stroke: var(--border);
    stroke-width: 1;
  }
  .axis {
    fill: var(--text-faint);
    font-size: 11px;
  }
  .line {
    stroke-width: 2;
    stroke-linejoin: round;
    stroke-linecap: round;
  }
  .marker {
    stroke-width: 1;
    stroke-dasharray: 3 3;
  }
  .axis-note {
    font-size: var(--fs-sm);
    margin: var(--space-1) 0;
  }
  .legend {
    list-style: none;
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-3);
    padding: 0;
    margin: var(--space-2) 0 0;
  }
  .swatch {
    display: inline-block;
    width: 0.75rem;
    height: 0.75rem;
    border-radius: 2px;
    margin-right: var(--space-1);
    vertical-align: middle;
  }
</style>
