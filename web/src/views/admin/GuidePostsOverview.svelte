<!--
  Guide outreach's first view (#0660): every topic guide, when it was last
  shared, where, under which campaign, and its clicks and signups. Sorting
  and wording live in lib/guidePosts.ts.
-->
<script lang="ts">
  import Panel from '../../lib/Panel.svelte';
  import { adminGuidePostsHash } from '../../lib/adminRouter';
  import { formatClicks, formatSignups, SHARE_SORTS, shareLastPosted, sortShares, type ShareSort } from '../../lib/guidePosts';
  import { formatPacificDateTime } from '../../lib/pacificTime';
  import type { GuidePostChannel, GuideShare } from '../../lib/types';

  let { guides, channels }: { guides: GuideShare[]; channels: GuidePostChannel[] } = $props();

  let sort = $state<ShareSort>('oldest');
  const rows = $derived(sortShares(guides, sort));
  const channelLabel = (name: string) => channels.find((c) => c.name === name)?.label ?? name;
</script>

<Panel title="Recently shared">
  <label class="sort">Sort
    <select bind:value={sort}>
      {#each SHARE_SORTS as s (s.id)}
        <option value={s.id}>{s.label}</option>
      {/each}
    </select>
  </label>
  <div class="table-wrap">
    <table>
      <thead>
        <tr>
          <th scope="col">Guide</th>
          <th scope="col">Last posted</th>
          <th scope="col">Channels</th>
          <th scope="col">Last campaign</th>
          <th scope="col">Next scheduled</th>
          <th scope="col">Clicks</th>
          <th scope="col">Signups since first shared</th>
          <th scope="col"><span class="sr-only">Actions</span></th>
        </tr>
      </thead>
      <tbody>
        {#each rows as row (row.slug)}
          <tr class:never={!row.last_posted_at} data-slug={row.slug}>
            <td><a href={row.url} target="_blank" rel="noopener">{row.title}</a></td>
            <td><span class:chip={!row.last_posted_at}>{shareLastPosted(row)}</span></td>
            <td>{row.channels.length ? row.channels.map(channelLabel).join(', ') : '—'}</td>
            <td>{row.last_campaign || '—'}</td>
            <td>{row.next_scheduled_at ? formatPacificDateTime(row.next_scheduled_at) : '—'}</td>
            <td>{formatClicks(row.clicks)}</td>
            <td>{formatSignups(row.signups)}</td>
            <td><a href={adminGuidePostsHash('posts', row.slug)}>Draft a post</a></td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
</Panel>

<style>
  .sort {
    display: inline-flex;
    gap: var(--space-2);
    align-items: center;
    margin-bottom: var(--space-3);
  }
  .table-wrap {
    overflow-x: auto;
  }
  table {
    width: 100%;
    border-collapse: collapse;
  }
  th,
  td {
    text-align: left;
    padding: var(--space-2);
    border-bottom: var(--border-w) solid var(--border);
    vertical-align: top;
  }
  .chip {
    color: var(--text-muted);
    border: var(--border-w) solid var(--border);
    border-radius: var(--radius);
    padding: 0 var(--space-2);
  }
  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip: rect(0 0 0 0);
    white-space: nowrap;
  }
</style>
