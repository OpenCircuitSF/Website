<!--
  The status board (#0624): one row per section, one column per channel, saying
  in words what each post has done and when. It stacks at 860 px and below
  rather than scrolling sideways, because the Outreach panel clips overflow and
  a scrolling table would hide channels on a phone. Each cell carries its own
  channel name for that stacked layout.
-->
<script lang="ts">
  import { boardRows } from '../../lib/outreachCards';
  import { waveExcerpt } from '../../lib/outreachMessage';
  import type { WorkshopOutreach } from '../../lib/types';

  let { view }: { view: WorkshopOutreach } = $props();

  const rows = $derived(boardRows(view));
</script>

<table class="status-board">
  <caption class="board-caption">Posts and when they go out</caption>
  <thead>
    <tr>
      <th scope="col" class="board-post-head">Post</th>
      {#each view.channels as channel (channel.name)}<th scope="col">{channel.label}</th>{/each}
    </tr>
  </thead>
  <tbody>
    {#each rows as row (row.wave)}
      {@const excerpt = waveExcerpt(view, row.wave)}
      <tr data-wave={row.wave}>
        <th scope="row" class="board-row-head">
          <span class="board-name"><span class="board-ordinal">{row.ordinal}</span> <span class="board-label">{row.label}</span></span>
          {#if row.note}<span class="board-note">{row.note}</span>{/if}
          {#if excerpt}<span class="board-excerpt text-muted">{excerpt}</span>{/if}
        </th>
        {#each row.cells as cell (cell.channel)}
          <td data-channel={cell.channel} data-kind={cell.kind}>
            <span class="board-channel">{cell.channelLabel}</span>
            <span class="board-cell">
              <span class="board-status">{cell.status}</span>
              {#if cell.detail}<span class="board-detail text-muted">{cell.detail}</span>{/if}
            </span>
          </td>
        {/each}
      </tr>
    {/each}
  </tbody>
</table>

<style>
  .status-board { table-layout: fixed; margin-bottom: var(--space-4); }
  .board-caption { text-align: left; font-weight: 600; padding-bottom: var(--space-2); }
  .board-post-head { width: 11rem; }
  th, td { padding: var(--space-2); overflow-wrap: break-word; vertical-align: top; }
  .board-row-head { text-align: left; font-weight: 400; border-bottom: var(--border-w) solid var(--border); }
  tbody tr:last-child .board-row-head { border-bottom: none; }
  .board-name, .board-note, .board-excerpt, .board-status, .board-detail { display: block; }
  .board-ordinal { font-family: var(--font-mono); font-weight: 600; border: var(--border-w) solid var(--border-strong); border-radius: var(--radius); padding: 0 var(--space-1); }
  .board-label { font-weight: 600; text-transform: uppercase; }
  .board-note, .board-status { font-weight: 600; }
  .board-excerpt, .board-detail { font-size: var(--fs-sm); }
  .board-channel { display: none; }

  @media (max-width: 860px) {
    .status-board, .status-board caption, tbody, tr, .board-row-head { display: block; }
    thead { display: none; }
    tbody tr { padding: var(--space-2) 0; border-top: var(--border-w) solid var(--border); }
    .board-row-head { padding: 0 0 var(--space-1); border-bottom: none; }
    .board-excerpt { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
    td { display: grid; grid-template-columns: 8rem minmax(0, 1fr); gap: var(--space-2); padding: var(--space-1) 0; border-bottom: none; }
    .board-channel { display: block; }
  }
</style>
