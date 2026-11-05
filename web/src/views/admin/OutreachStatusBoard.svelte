<!--
  The status board (#0624): one row per section, one column per channel, saying
  in words what each post has done and when. It stacks at 880 px and below
  rather than scrolling sideways, because the Outreach panel clips overflow and
  a scrolling table would hide channels on a phone. Each cell carries its own
  channel name for that stacked layout. Each row's name is a button that selects
  that section, and its hit area covers the whole heading cell (#0625). A line
  under the table names every switched-off channel and links to Settings (#0628).
  Each row carries its section's one line in the page's words, with its date
  kept on one line, and the board says which channels are posted by hand (#0640).
-->
<script lang="ts">
  import { adminSettingsHash } from '../../lib/adminRouter';
  import { boardRows, boardChannelLabel, boardOffLine, sectionPanelId } from '../../lib/outreachCards';
  import { waveExcerpt } from '../../lib/outreachMessage';
  import { handChannelLabel } from '../../lib/outreachHandCopy';
  import { hasVideoByHandLine, lineParts } from '../../lib/outreachSection';
  import type { WorkshopOutreach } from '../../lib/types';

  interface Props {
    view: WorkshopOutreach;
    openWave: string | null;
    unsaved: Record<string, boolean>;
    onSelect: (wave: string) => void;
  }
  let { view, openWave, unsaved, onSelect }: Props = $props();

  const rows = $derived(boardRows(view));
  const offLine = $derived(boardOffLine(view));
  const handLine = $derived(hasVideoByHandLine(view));
</script>

<table class="status-board">
  <caption class="board-caption">Posts and when they go out</caption>
  <thead>
    <tr>
      <th scope="col" class="board-post-head">Post</th>
      {#each view.channels as channel (channel.name)}<th scope="col">{boardChannelLabel(handChannelLabel(channel), (view.off_channels ?? []).some((o) => o.name === channel.name))}</th>{/each}
    </tr>
  </thead>
  <tbody>
    {#each rows as row (row.wave)}
      {@const excerpt = waveExcerpt(view, row.wave)}
      {@const parts = lineParts(row.line.text)}
      <tr data-wave={row.wave} class:board-open={row.wave === openWave}>
        <th scope="row" class="board-row-head">
          <button type="button" class="board-select" aria-controls={sectionPanelId(row.wave)} aria-current={row.wave === openWave ? 'true' : undefined} onclick={() => onSelect(row.wave)}>
            <span class="board-name"><span class="board-chevron" aria-hidden="true">{row.wave === openWave ? '▾' : '▸'}</span> <span class="board-ordinal">{row.ordinal}</span> <span class="board-label">{row.label}</span></span>
          </button>
          <span class="board-line">{parts[0]}{#if parts[1]}<span class="board-when">{parts[1]}</span>{/if}{parts[2]}</span>
          {#if unsaved[row.wave]}<span class="board-note">Not saved yet</span>{/if}
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
{#if handLine}<p class="board-hand text-muted">{handLine}</p>{/if}
{#if offLine}<p class="board-off text-muted">{offLine} <a href={adminSettingsHash('outreach')}>Open Settings</a></p>{/if}

<style>
  .status-board { table-layout: fixed; margin-bottom: var(--space-4); }
  .board-caption { text-align: left; font-weight: 600; padding-bottom: var(--space-2); }
  .board-post-head { width: 14rem; }
  th, td { padding: var(--space-2); overflow-wrap: break-word; vertical-align: top; }
  .board-row-head { position: relative; text-align: left; font-weight: 400; border-bottom: var(--border-w) solid var(--border); }
  .board-select { display: block; min-height: 0; margin: 0; padding: 0; border: none; background: none; color: inherit; font: inherit; text-align: left; cursor: pointer; }
  .board-select::after { content: ''; position: absolute; inset: 0; }
  .board-open .board-row-head { box-shadow: inset 2px 0 0 var(--accent); }
  tbody tr:last-child .board-row-head { border-bottom: none; }
  .board-name, .board-line, .board-note, .board-excerpt, .board-status, .board-detail { display: block; }
  .board-ordinal { font-family: var(--font-mono); font-weight: 600; border: var(--border-w) solid var(--border-strong); border-radius: var(--radius); padding: 0 var(--space-1); }
  .board-label { font-weight: 600; text-transform: uppercase; text-decoration: underline; }
  .board-note, .board-status { font-weight: 600; }
  .board-excerpt, .board-detail { font-size: var(--fs-sm); }
  .board-when { white-space: nowrap; }
  .board-channel { display: none; }
  .board-hand { font-size: var(--fs-sm); margin: 0 0 var(--space-2); }
  .board-off { font-size: var(--fs-sm); margin: 0 0 var(--space-4); }

  @media (max-width: 880px) {
    .status-board, .status-board caption, tbody, tr, .board-row-head { display: block; }
    thead { display: none; }
    tbody tr { padding: var(--space-2) 0; border-top: var(--border-w) solid var(--border); }
    .board-caption { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; padding: 0; }
    .board-row-head { padding: 0 0 var(--space-1) var(--space-2); border-bottom: none; }
    .board-excerpt { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
    td { display: inline; padding: 0; border-bottom: none; }
    td + td::before { content: ' · '; }
    .board-channel { display: inline; margin-right: var(--space-1); }
    .board-cell, .board-status, .board-detail { display: inline; }
    .board-status { margin-right: var(--space-1); }
  }
</style>
