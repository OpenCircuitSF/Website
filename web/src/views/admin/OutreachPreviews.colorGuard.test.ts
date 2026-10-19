// #0601: hashtags and links in the post previews are text on the page, so they
// use --accent (light #2F7D0C is 4.76:1 on --bg-page and 5.17:1 on
// --bg-panel; dark #68FF23 is 13.7:1 or better). --accent-text is the
// colour for text ON an accent fill (#0A0D0B dark, #FFFFFF light), which made
// the tags vanish. jsdom does not resolve custom properties, so this reads the
// component sources.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const dir = path.dirname(fileURLToPath(import.meta.url));

describe.each(['OutreachBlueskyPreview.svelte', 'OutreachMastodonPreview.svelte'])('%s (#0601)', (file) => {
  const src = readFileSync(path.join(dir, file), 'utf-8');
  it('never uses --accent-text', () => {
    expect(src).not.toContain('--accent-text');
  });
  it('colours hashtags and links with --accent', () => {
    expect(src).toMatch(/\.hashtag,\s*\n\s*\.link\s*\{[^}]*color:\s*var\(--accent\);/);
  });
});
