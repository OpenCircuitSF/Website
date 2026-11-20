// Shared external link URLs used across multiple components (Footer.svelte,
// About.svelte, ...). A sibling to branding.ts rather than an addition to it
// -- branding.ts's own comment says not to use it for functional values, and
// these are functional external destinations, not brand display strings.
//
// #0520/#0736: these two hrefs are literals rather than imports so the
// entry chunk does not carry web/src/content/about.json. about.json's "links"
// map stays the source the Go server renderer (internal/seo/staticpages.go)
// reads, and internal/seo's TestLinksTSMatchesAboutJSON fails if either
// literal here drifts from it.
//
// LUMA_URL: confirmed 2026-08-21 via `curl -sI` -- luma.com/opencircuitsf
// returns 200 directly, while lu.ma/opencircuitsf 301s to the luma.com form.
// luma.com is therefore the canonical URL; do not swap in the lu.ma shortener
// form.
export const DISCORD_URL = 'https://discord.gg/Fq9ug6QXV3';
export const LUMA_URL = 'https://luma.com/opencircuitsf';
