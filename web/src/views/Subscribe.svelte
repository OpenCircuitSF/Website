<script lang="ts">
  // Standalone /subscribe view (#0029, PRD §5.1/§7.3): the same SubscribeForm
  // used inline on Home.svelte, given its own page chrome and single <h1> so
  // it works as a direct, shareable link (e.g. from a workshop flyer QR code)
  // rather than only reachable by scrolling Home.
  import SubscribeForm from '../lib/SubscribeForm.svelte';
  import TerminalPanel from '../lib/TerminalPanel.svelte';
</script>

<main id="main-content" class="app-shell subscribe-shell">
  <h1 tabindex="-1">Subscribe</h1>
  <p class="text-muted">
    Get notified about new workshops. Pick the topics you care about — nothing else.
  </p>
  <TerminalPanel title="subscribe">
    <SubscribeForm />
  </TerminalPanel>
</main>

<style>
  .subscribe-shell {
    max-width: 560px;
  }
  .subscribe-shell h1 {
    margin: 0 0 var(--space-2);
  }
  .subscribe-shell > p {
    margin: 0 0 var(--space-4);
  }
</style>
