<!--
  Guide outreach's results (#0660): clicks and signups by campaign and by
  post. Totals come from lib/guidePosts.ts.
-->
<script lang="ts">
  import Panel from '../../lib/Panel.svelte';
  import { campaignTotals, formatClicks, formatSignups, postResultRows } from '../../lib/guidePosts';
  import { formatPacificDateTime } from '../../lib/pacificTime';
  import type { GuidePostChannel, GuidePostRow, GuideTopic } from '../../lib/types';

  let { posts, topics, channels }: { posts: GuidePostRow[]; topics: GuideTopic[]; channels: GuidePostChannel[] } = $props();

  const totals = $derived(campaignTotals(posts));
  const rows = $derived(postResultRows(posts, topics, channels));
</script>

<p class="note">
  Signups by post count only people who arrived through that post's link. The Recently shared tab counts signups from a
  guide's page since the guide was first shared.
</p>

{#if rows.length === 0}
  <p>No posts have short links yet.</p>
{:else}
  <Panel title="By campaign">
    <div class="table-wrap">
      <table data-table="campaigns">
        <thead>
          <tr><th scope="col">Campaign</th><th scope="col">Posts</th><th scope="col">Clicks</th><th scope="col">Signups</th></tr>
        </thead>
        <tbody>
          {#each totals as t (t.campaign)}
            <tr><td>{t.campaign}</td><td>{t.posts}</td><td>{formatClicks(t.clicks)}</td><td>{formatSignups(t.signups)}</td></tr>
          {/each}
        </tbody>
      </table>
    </div>
  </Panel>
  <Panel title="By post">
    <div class="table-wrap">
      <table data-table="posts">
        <thead>
          <tr>
            <th scope="col">Guide</th><th scope="col">Channel</th><th scope="col">Campaign</th><th scope="col">Status</th>
            <th scope="col">Posted</th><th scope="col">Clicks</th><th scope="col">Signups</th><th scope="col">Short link</th>
          </tr>
        </thead>
        <tbody>
          {#each rows as r (r.id)}
            <tr>
              <td>{r.guide}</td><td>{r.channel}</td><td>{r.campaign}</td><td>{r.status}</td>
              <td>{r.posted_at ? formatPacificDateTime(r.posted_at) : '—'}</td>
              <td>{formatClicks(r.clicks)}</td><td>{formatSignups(r.signups)}</td><td><code>{r.short_url}</code></td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  </Panel>
{/if}

<style>
  .note {
    color: var(--text-muted);
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
  }
</style>
