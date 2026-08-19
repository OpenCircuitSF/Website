<script lang="ts">
  // NotFound view (#0022): what the router falls through to for any
  // unmatched path (web/src/lib/router.ts's parsePath returns the
  // 'not-found' route name for anything not in its static table or matching
  // /workshops/:slug). Styled as a terminal error rather than a browser
  // default, per the acceptance criteria.
  //
  // The Go SPA handler (internal/handlers/static.go, this same issue) returns
  // an actual HTTP 404 for any path outside its own known-route table while
  // still serving this same index.html/bundle, so a hard refresh on a typo'd
  // URL gets both the correct status code AND this view.
  import TerminalPanel from '../lib/TerminalPanel.svelte';
  import Prompt from '../lib/Prompt.svelte';
  import CommandLine from '../lib/CommandLine.svelte';
  import { currentRoute } from '../lib/router';
</script>

<main id="main-content" class="app-shell not-found-shell">
  <TerminalPanel title="404">
    <Prompt text="open_circuit // not_found" />
    <h1 class="headline">404 // Not Found</h1>
    <CommandLine command="open {$currentRoute.path}" />
    <p class="error-line">
      bash: <code>{$currentRoute.path}</code>: command not found
    </p>
    <p class="links">
      <a href="/">Home</a>
      <span aria-hidden="true"> · </span>
      <a href="/workshops">Workshops</a>
    </p>
  </TerminalPanel>
</main>

<style>
  .not-found-shell {
    max-width: 640px;
  }

  .headline {
    margin: var(--space-3) 0 var(--space-4);
    font-family: var(--font);
    font-weight: 800;
    text-transform: uppercase;
    letter-spacing: 0.01em;
    font-size: clamp(24px, 6vw, 36px);
    color: var(--text);
  }

  .error-line {
    margin: var(--space-3) 0 0;
    font-family: var(--font-mono);
    font-size: var(--fs-base);
    color: var(--text-muted);
    overflow-wrap: anywhere;
  }
  .error-line code {
    color: var(--text);
  }

  .links {
    margin: var(--space-5) 0 0;
    font-size: var(--fs-base);
  }
</style>
