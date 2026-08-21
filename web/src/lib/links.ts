// Shared external link URLs used across multiple components (Footer.svelte,
// About.svelte, ...). A sibling to branding.ts rather than an addition to it
// -- branding.ts's own comment says not to use it for functional values, and
// these are functional external destinations, not brand display strings.
//
// LUMA_URL: confirmed 2026-08-21 via `curl -sI` -- luma.com/opencircuitsf
// returns 200 directly, while lu.ma/opencircuitsf 301s to the luma.com form.
// luma.com is therefore the canonical URL; do not swap in the lu.ma shortener
// form.
export const DISCORD_URL = 'https://discord.gg/Fq9ug6QXV3';
export const LUMA_URL = 'https://luma.com/opencircuitsf';
