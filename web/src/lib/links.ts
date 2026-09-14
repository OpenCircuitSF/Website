// Shared external link URLs used across multiple components (Footer.svelte,
// About.svelte, ...). A sibling to branding.ts rather than an addition to it
// -- branding.ts's own comment says not to use it for functional values, and
// these are functional external destinations, not brand display strings.
//
// #0520: these two hrefs are re-exported from web/src/content/about.json
// (the "links" map) rather than retyped here, so the URL the SPA renders and
// the URL the Go server renderer (internal/seo/staticpages.go) resolves for
// About's "Get involved" links come from one place. Retype a URL here and
// it is no longer what the server serves.
//
// LUMA_URL: confirmed 2026-08-21 via `curl -sI` -- luma.com/opencircuitsf
// returns 200 directly, while lu.ma/opencircuitsf 301s to the luma.com form.
// luma.com is therefore the canonical URL; do not swap in the lu.ma shortener
// form.
import aboutContent from '../content/about.json';

export const DISCORD_URL = aboutContent.links.discord;
export const LUMA_URL = aboutContent.links.luma;
