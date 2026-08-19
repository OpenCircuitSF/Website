<script lang="ts">
  // /subscribe/thanks (#0029): the single, fixed post-submit destination for
  // EVERY successful POST /api/subscribe -- see lib/subscribe.ts's
  // onSubscribeSuccess. Its copy is static and does not read anything from
  // the previous response, by construction: that is what keeps this page
  // from ever becoming a place #0026's uniform-202 guarantee could leak
  // through (CLAUDE.md §9).
  import Panel from '../lib/Panel.svelte';
</script>

<main id="main-content" class="app-shell thanks-shell">
  <h1>Check your email</h1>
  <Panel>
    <p>
      If that address isn't already on our list, we've sent a confirmation link to it.
      Click the link to finish subscribing.
    </p>
    <p class="text-muted">
      Nothing arrived after a few minutes? Check spam, or try
      <a href="/subscribe">subscribing again</a>.
    </p>
  </Panel>
</main>

<style>
  .thanks-shell {
    max-width: 560px;
  }
  .thanks-shell h1 {
    margin: 0 0 var(--space-4);
  }
</style>
