<script lang="ts">
  // About view (#0016, PRD §5.1): who Open Circuit SF is, how workshops run,
  // how venues work, how to get involved, and contact.
  //
  // #0520: the page's full text moved into web/src/content/about.json --
  // ONE source this view shares with the Go server renderer
  // (internal/seo/staticpages.go, which embeds the same file and substitutes
  // the same text into the raw server HTML). This component is now a thin
  // shell: it picks the terminal chrome ("about // open_circuit_sf",
  // "whoami") and hands the parsed content to StaticPage.svelte. Edit the
  // copy in about.json, never here -- anything typed back into this file
  // would drift from the server HTML by construction.
  //
  // Copy notes (flagged rather than invented where uncertain): "monthly" cadence
  // is sourced directly from PRD §4.1's own reference layout example
  // ("[ OK ] monthly · beginners"), not invented here. The Discord invite and
  // "hosted anywhere, including a neighborhood garage" framing are carried
  // over verbatim from placeholder/index.html, the validated reference copy.
  import StaticPage from '../lib/StaticPage.svelte';
  import { parseStaticPage } from '../lib/staticPage';
  import raw from '../content/about.json';

  const content = parseStaticPage(raw);
</script>

<StaticPage {content} terminalTitle="about // open_circuit_sf" promptText="whoami" />
