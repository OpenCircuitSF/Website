<!--
  One "Applies immediately" / "Applies at next deploy" badge per Settings
  section (#0584), replacing the per-field sentences. The text carries the
  meaning; the border style is decoration only.
-->
<script lang="ts">
  import { applyBadgeText, type ApplyKind } from './serverSettings';

  let { kind }: { kind: ApplyKind } = $props();
</script>

<span class="apply-badge" class:deploy={kind !== 'immediate'}>{applyBadgeText(kind)}</span>

<style>
  .apply-badge {
    display: inline-block;
    padding: 0 var(--space-2);
    border: var(--border-w) solid var(--border-strong);
    border-radius: var(--radius);
    background: var(--bg-subtle);
    color: var(--text);
    font-size: var(--fs-sm);
    font-weight: 600;
    white-space: nowrap;
  }
</style>
