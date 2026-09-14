<script lang="ts">
  // Privacy policy view (#0070, PRD §11): what the mailing list collects,
  // why, how long it's kept, and how to leave. Filed because the footer
  // (#0017) already links /privacy and, until this issue, the route didn't
  // exist -- a 404 behind a link PRD §11 requires.
  //
  // #0520: the page's full text moved into web/src/content/privacy.json --
  // ONE source this view shares with the Go server renderer
  // (internal/seo/staticpages.go, which embeds the same file and substitutes
  // the same text into the raw server HTML). This view is now a thin shell:
  // terminal chrome ("privacy // open_circuit_sf", "cat privacy-policy.md")
  // plus StaticPage.svelte doing the rendering. Edit the copy in
  // privacy.json, never here -- anything typed back into this file would
  // drift from the server HTML by construction. web/src/views/
  // PrivacyPolicy.guard.test.ts (#0226) still pins the erasure-retained
  // categories against that JSON, which is where the list now lives.
  //
  // The copy's accuracy notes (#0226's two review passes, #0237's
  // widenings, #0503's parked-record disclosure) all travelled with the
  // text into privacy.json; its "How to leave" list must keep naming what
  // internal/subscribers/erase.go and internal/handlers actually retain
  // after erasure -- PrivacyPolicy.guard.test.ts fails naming any category
  // that drifts.
  import StaticPage from '../lib/StaticPage.svelte';
  import { parseStaticPage } from '../lib/staticPage';
  import raw from '../content/privacy.json';

  const content = parseStaticPage(raw);
</script>

<StaticPage {content} terminalTitle="privacy // open_circuit_sf" promptText="cat privacy-policy.md" />
