<!--
  The admin Guide outreach section (#0660): the shell. It loads the posts
  list and the recently-shared overview, owns the dialogs and the status
  line, and mounts one of three sub-views from the hash query. Every
  decision is in lib/guidePosts.ts; topic data comes from the API. Posts made
  together on several channels show as one share (#0665).
-->
<script lang="ts">
  import Button from '../../lib/Button.svelte';
  import SubNav from '../../lib/SubNav.svelte';
  import Panel from '../../lib/Panel.svelte';
  import {
    copyGuidePost,
    deleteGuidePost,
    getGuidePostsOverview,
    listGuidePosts,
    scheduleGuidePost,
    sendGuidePost,
    unscheduleGuidePost,
    updateGuidePost,
  } from '../../lib/api';
  import { adminGuidePostsHash, guidePostsGuideFromQuery, guidePostsViewFromQuery } from '../../lib/adminRouter';
  import { fromLocalInputValue } from '../../lib/outreach';
  import {
    asSentence,
    campaignHint,
    canScheduleAgainTogether,
    createAcross,
    clicksErrorMessage,
    defaultScheduleAt,
    groupShares,
    hasQueued,
    isValidCampaign,
    linkErrorMessage,
    outcomeSummary,
    shareChannelLabels,
    signupsErrorMessage,
    type PostAction,
  } from '../../lib/guidePosts';
  import type { GuidePostCreated, GuidePostRow, GuidePostsList, GuidePostsOverview } from '../../lib/types';
  import GuidePostsOverviewView from './GuidePostsOverview.svelte';
  import GuidePostComposer from './GuidePostComposer.svelte';
  import GuidePostCard from './GuidePostCard.svelte';
  import GuidePostsResults from './GuidePostsResults.svelte';
  import OutreachConfirmDialog from './OutreachConfirmDialog.svelte';

  let { query = new URLSearchParams() }: { query?: URLSearchParams } = $props();
  const view = $derived(guidePostsViewFromQuery(query));
  const guide = $derived(guidePostsGuideFromQuery(query));

  let list = $state<GuidePostsList | null>(null);
  let overview = $state<GuidePostsOverview | null>(null);
  let listError = $state<string | null>(null);
  let overviewError = $state<string | null>(null);
  let loading = $state(true);
  let actionMessage = $state<string | null>(null);
  let composerOpen = $state(false);
  let loadSeq = 0;

  async function load(): Promise<void> {
    const seq = ++loadSeq;
    const [l, o] = await Promise.allSettled([listGuidePosts(), getGuidePostsOverview()]);
    if (seq !== loadSeq) return;
    if (l.status === 'fulfilled') {
      list = l.value;
      listError = null;
    } else listError = asSentence(l.reason instanceof Error ? l.reason.message : 'The posts could not be loaded');
    if (o.status === 'fulfilled') {
      overview = o.value;
      overviewError = null;
    } else overviewError = asSentence(o.reason instanceof Error ? o.reason.message : 'The guide list could not be loaded');
    loading = false;
  }

  $effect(() => {
    void load();
  });

  const queued = $derived(list ? hasQueued(list.posts) : false);
  $effect(() => {
    if (!queued) return;
    const timer = setInterval(() => void load(), 5000);
    return () => clearInterval(timer);
  });

  const statusText = $derived.by(() => {
    if (loading) return 'Loading guide outreach…';
    const parts = [
      actionMessage,
      clicksErrorMessage(list?.clicks_error ?? overview?.clicks_error ?? null),
      signupsErrorMessage(list?.signups_error ?? overview?.signups_error ?? null),
    ].filter((s): s is string => !!s);
    return parts.join(' ');
  });

  const groups = $derived(list ? groupShares(list.posts, view === 'posts' ? guide : null) : { active: [], posted: [] });
  const channelFor = (name: string) =>
    list?.channels.find((c) => c.name === name) ?? { name, label: name, char_limit: 0, configured: false };
  const titleFor = (slug: string) => list?.topics.find((t) => t.slug === slug)?.title ?? slug;

  // ── Dialogs ──
  type Dialog =
    | { kind: 'schedule'; post: GuidePostRow; value: string }
    | { kind: 'send'; post: GuidePostRow; retry: boolean }
    | { kind: 'delete'; post: GuidePostRow }
    | { kind: 'again'; post: GuidePostRow; campaign: string }
    | { kind: 'againShare'; posts: GuidePostRow[]; campaign: string };
  let dialog = $state<Dialog | null>(null);
  let dialogInFlight = $state(false);
  let dialogError = $state<string | null>(null);

  function openDialog(d: Dialog): void {
    dialogError = null;
    dialog = d;
  }

  async function runAction(fn: () => Promise<unknown>, done: string): Promise<void> {
    dialogInFlight = true;
    dialogError = null;
    try {
      const r = await fn();
      dialog = null;
      actionMessage = typeof r === 'string' ? r : done;
      await load();
    } catch (e) {
      dialogError = asSentence(e instanceof Error ? e.message : String(e));
    } finally {
      dialogInFlight = false;
    }
  }

  function createdMessage(r: GuidePostCreated, ok: string): string {
    return linkErrorMessage(r.link_error) ?? (r.post.short_url ? `${ok} with short link ${r.post.short_url}.` : `${ok}.`);
  }

  function confirmDialog(): void {
    const d = dialog;
    if (!d) return;
    if (d.kind === 'schedule') {
      const at = fromLocalInputValue(d.value);
      if (at === null) return;
      void runAction(() => scheduleGuidePost(d.post.id, at), 'Scheduled.');
    } else if (d.kind === 'send') {
      void runAction(() => sendGuidePost(d.post.id), d.retry ? 'Retrying.' : 'Posting.');
    } else if (d.kind === 'delete') {
      void runAction(() => deleteGuidePost(d.post.id), 'Draft deleted.');
    } else if (d.kind === 'again') {
      void runAction(
        async () => createdMessage(await copyGuidePost(d.post.id, d.campaign.trim()), 'Draft created'),
        'Draft created.',
      );
    } else {
      void runAgainShare(d.posts, d.campaign.trim());
    }
  }

  // One campaign prompt, one copy per channel; each channel reports on its own.
  async function runAgainShare(posts: GuidePostRow[], campaign: string): Promise<void> {
    dialogInFlight = true;
    dialogError = null;
    const idFor = new Map(posts.map((p) => [p.channel, p.id]));
    const outcomes = await createAcross(
      posts.map((p) => channelFor(p.channel)),
      (channel) => copyGuidePost(idFor.get(channel) as number, campaign),
      null,
    );
    dialogInFlight = false;
    const message = outcomeSummary(outcomes, null);
    if (outcomes.every((o) => o.post === null)) {
      dialogError = message;
      return;
    }
    dialog = null;
    actionMessage = message;
    await load();
  }

  async function onAction(action: PostAction, post: GuidePostRow): Promise<void> {
    if (action === 'schedule') openDialog({ kind: 'schedule', post, value: defaultScheduleAt(new Date(), list?.post_hour ?? 9) });
    else if (action === 'postNow' || action === 'retry') openDialog({ kind: 'send', post, retry: action === 'retry' });
    else if (action === 'delete') openDialog({ kind: 'delete', post });
    else if (action === 'again') openDialog({ kind: 'again', post, campaign: list?.suggested_campaign ?? '' });
    else if (action === 'unschedule') {
      try {
        await unscheduleGuidePost(post.id);
        actionMessage = 'Unscheduled.';
      } catch (e) {
        actionMessage = asSentence(e instanceof Error ? e.message : String(e));
      }
      await load();
    }
  }

  async function onSave(post: GuidePostRow, body: string): Promise<void> {
    await updateGuidePost(post.id, body);
    actionMessage = 'Saved.';
    await load();
  }

  async function onCreated(message: string, done: boolean): Promise<void> {
    if (done) composerOpen = false;
    actionMessage = message;
    await load();
  }

  const dialogTitle = $derived.by(() => {
    if (!dialog) return '';
    if (dialog.kind === 'schedule') return 'Schedule post';
    if (dialog.kind === 'send') return dialog.retry ? 'Retry post' : 'Post now';
    if (dialog.kind === 'delete') return 'Delete draft';
    if (dialog.kind === 'againShare') return `Schedule again on ${shareChannelLabels(dialog.posts, list?.channels ?? [])}`;
    return 'Schedule again';
  });
  const dialogConfirmDisabled = $derived(
    dialog?.kind === 'schedule'
      ? fromLocalInputValue(dialog.value) === null
      : dialog?.kind === 'again' || dialog?.kind === 'againShare'
        ? !isValidCampaign(dialog.campaign)
        : false,
  );
</script>

{#snippet shareBlock(share: GuidePostRow[])}
  {#if share.length > 1}
    {@const labels = shareChannelLabels(share, list?.channels ?? [])}
    <section class="share" data-share={share.map((p) => p.id).join(',')} aria-label={`Shared together on ${labels}`}>
      <p class="share-head">Shared together on {labels}</p>
      {#each share as post (post.id)}
        <GuidePostCard {post} channel={channelFor(post.channel)} topicTitle={titleFor(post.guide_slug)} onAction={(a, p) => void onAction(a, p)} {onSave} />
      {/each}
      {#if canScheduleAgainTogether(share)}
        <div>
          <Button onclick={() => openDialog({ kind: 'againShare', posts: share, campaign: list?.suggested_campaign ?? '' })}>
            Schedule again on {labels}…
          </Button>
        </div>
      {/if}
    </section>
  {:else}
    <GuidePostCard post={share[0]} channel={channelFor(share[0].channel)} topicTitle={titleFor(share[0].guide_slug)} onAction={(a, p) => void onAction(a, p)} {onSave} />
  {/if}
{/snippet}

<SubNav label="Guide outreach sections" items={[
  { href: adminGuidePostsHash('overview'), label: 'Recently shared', current: view === 'overview' },
  { href: adminGuidePostsHash('posts'), label: 'Posts', current: view === 'posts' },
  { href: adminGuidePostsHash('results'), label: 'Clicks and signups', current: view === 'results' },
]} />

<p class="status" role="status">{statusText}</p>
{#if listError || overviewError}
  <p class="text-error" role="alert">{listError ?? overviewError}</p>
{/if}

{#if view === 'overview'}
  {#if overview}
    <GuidePostsOverviewView guides={overview.guides} channels={list?.channels ?? []} />
  {/if}
{:else if view === 'posts'}
  {#if list}
    <Panel title="Posts">
      {#if composerOpen || guide}
        {#key guide}
          <GuidePostComposer
            topics={list.topics}
            channels={list.channels}
            suggestedCampaign={list.suggested_campaign}
            postHour={list.post_hour}
            initialGuide={guide}
            onCreated={(m, done) => void onCreated(m, done)}
            onCancel={() => {
              composerOpen = false;
              if (guide) window.location.hash = adminGuidePostsHash('posts');
            }}
          />
        {/key}
      {:else}
        <Button variant="primary" onclick={() => (composerOpen = true)}>New post</Button>
      {/if}
      {#if guide}
        <p><a href={adminGuidePostsHash('posts')}>Show all guides</a></p>
      {/if}

      {#each groups.active as share (share[0].id)}
        {@render shareBlock(share)}
      {/each}
      {#if groups.posted.length > 0}
        <h3>Posted</h3>
        {#each groups.posted as share (share[0].id)}
          {@render shareBlock(share)}
        {/each}
      {/if}
      {#if groups.active.length === 0 && groups.posted.length === 0}
        <p>No posts yet.</p>
      {/if}
    </Panel>
  {/if}
{:else if list}
  <GuidePostsResults posts={list.posts} topics={list.topics} channels={list.channels} />
{/if}

{#if dialog}
  <OutreachConfirmDialog
    title={dialogTitle}
    inFlight={dialogInFlight}
    errorMessage={dialogError}
    confirmLabel={dialog.kind === 'delete' ? 'Delete' : dialog.kind === 'schedule' ? 'Schedule' : dialog.kind === 'again' ? 'Create draft' : dialog.kind === 'againShare' ? 'Create drafts' : 'Post now'}
    danger={dialog.kind === 'delete'}
    confirmDisabled={dialogConfirmDisabled}
    onConfirm={confirmDialog}
    onClose={() => (dialog = null)}
  >
    {#if dialog.kind === 'schedule'}
      <label class="field">Time (Pacific)
        <input type="datetime-local" bind:value={dialog.value} />
      </label>
    {:else if dialog.kind === 'send'}
      <pre class="post-body">{dialog.post.body}</pre>
      <p>This posts to {channelFor(dialog.post.channel).label} right away and can't be undone.</p>
    {:else if dialog.kind === 'delete'}
      <p>Delete this draft? Its short link stays in ShortLinks.</p>
    {:else if dialog.kind === 'againShare'}
      <p>A new draft on each channel, with the same text and a new short link each.</p>
      <label class="field">Campaign
        <input type="text" bind:value={dialog.campaign} />
      </label>
      {#if !isValidCampaign(dialog.campaign)}
        <p class="note">{campaignHint}</p>
      {/if}
    {:else}
      <p>A new draft with the same text and a new short link.</p>
      <label class="field">Campaign
        <input type="text" bind:value={dialog.campaign} />
      </label>
      {#if !isValidCampaign(dialog.campaign)}
        <p class="note">{campaignHint}</p>
      {/if}
    {/if}
  </OutreachConfirmDialog>
{/if}

<style>
  .status {
    min-height: 1.5em;
    color: var(--text-muted);
  }
  .field {
    display: grid;
    gap: var(--space-1);
    margin-bottom: var(--space-3);
  }
  .note {
    color: var(--text-muted);
  }
  .share {
    border-left: 3px solid var(--accent);
    padding-left: var(--space-3);
    margin-bottom: var(--space-3);
  }
  .share-head {
    margin: 0 0 var(--space-2);
    color: var(--text-muted);
  }
  .post-body {
    white-space: pre-wrap;
    word-break: break-word;
    font-family: var(--font);
  }
</style>
