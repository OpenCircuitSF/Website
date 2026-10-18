<!--
  The Post now / Retry / Mark posted confirmation (#0581): shows each post's
  text and the public account it goes out as, and says plainly that it cannot
  be unsent. One item for a single card, two for the Announce
  "Post to Bluesky and Mastodon" button. Mark posted adds an optional link to
  the post, checked with validateRemoteUrl. Wording comes from
  lib/outreachCards.ts (postDialogCopy); the shell is OutreachConfirmDialog.
-->
<script lang="ts">
  import { postDialogCopy, type PostDialogMode } from '../../lib/outreachCards';
  import { validateRemoteUrl } from '../../lib/outreach';
  import { identicalAutomaticBodies } from '../../lib/outreachPreview';
  import type { OutreachChannel, OutreachPost, WorkshopOutreach } from '../../lib/types';
  import OutreachConfirmDialog from './OutreachConfirmDialog.svelte';
  import OutreachPostPreview from './OutreachPostPreview.svelte';

  interface Props {
    mode: PostDialogMode;
    /** When given, each item is drawn as its platform preview (#0583); otherwise as plain text. */
    view?: Pick<WorkshopOutreach, 'workshop'> & { bluesky_card?: 'short' | 'canonical' };
    items: { channel: OutreachChannel; post: OutreachPost }[];
    inFlight: boolean;
    errorMessage: string | null;
    onConfirm: (remoteUrl?: string) => void;
    onClose: () => void;
  }
  let { mode, view, items, inFlight, errorMessage, onConfirm, onClose }: Props = $props();

  let remoteUrl = $state('');
  const copy = $derived(
    postDialogCopy(
      mode,
      items.map((i) => ({ label: i.channel.label, account: i.channel.account })),
    ),
  );
  const sameText = $derived(
    identicalAutomaticBodies(
      { posts: items.map((i) => ({ channel: i.channel.name, wave: i.post.wave, body: i.post.body })) },
      items[0]?.post.wave ?? '',
    ),
  );
  const urlError = $derived(mode === 'markPosted' && remoteUrl.trim() !== '' ? validateRemoteUrl(remoteUrl) : null);
</script>

<OutreachConfirmDialog
  title={copy.title}
  {inFlight}
  errorMessage={errorMessage ?? urlError}
  confirmLabel={copy.confirmLabel}
  confirmDisabled={urlError !== null}
  onConfirm={() => onConfirm(remoteUrl.trim() || undefined)}
  {onClose}
>
  <p class="warning">{copy.warning}</p>
  {#each copy.accountLine as line (line)}
    <p class="text-muted">{line}</p>
  {/each}
  {#each items as item (item.post.id)}
    <h3 class="item-label">{item.channel.label}</h3>
    {#if view}
      <OutreachPostPreview {view} channel={item.channel} post={item.post} />
    {:else}
      <pre class="body">{item.post.body}</pre>
    {/if}
  {/each}
  {#if sameText && items.length > 1}<p class="text-warn">Bluesky and Mastodon will get exactly the same text.</p>{/if}
  {#if mode === 'markPosted'}
    <label class="field">
      <span class="field-label">Link to the post (optional)</span>
      <input type="url" bind:value={remoteUrl} placeholder="https://…" />
    </label>
  {/if}
</OutreachConfirmDialog>

<style>
  .warning {
    font-weight: 600;
  }
  .item-label {
    font-size: var(--fs-base);
    margin: var(--space-3) 0 var(--space-1);
  }
  .body {
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    font-family: var(--font);
    margin: 0 0 var(--space-2);
  }
  .field {
    display: block;
    margin: var(--space-3) 0;
  }
  .field-label {
    display: block;
    margin: 0 0 var(--space-1);
    font-size: var(--fs-sm);
    color: var(--text-muted);
  }
  input {
    width: 100%;
  }
</style>
