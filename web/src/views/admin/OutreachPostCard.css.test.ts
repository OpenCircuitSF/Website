// #0635: jsdom has no layout, so this reads the sources. It proves the phone
// rule exists and the row keeps its place for app.css; it does not prove the
// row's height in a real browser.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const here = path.dirname(fileURLToPath(import.meta.url));
const src = readFileSync(path.join(here, 'OutreachPostCard.svelte'), 'utf-8');
const style = src.slice(src.indexOf('<style>'));

describe('OutreachPostCard hand buttons (#0635)', () => {
  it('at 480 and under the hand row is a two-column grid', () => {
    const phone = style.slice(style.indexOf('@media (max-width: 480px)'));
    expect(phone).toMatch(/\.row\.hand-buttons\s*\{[^}]*display:\s*grid[^}]*grid-template-columns:\s*repeat\(2,/);
    expect(phone).toMatch(/\.hand-buttons \.link-row\s*\{[^}]*grid-column:\s*1 \/ -1/);
  });
  it('the row is a direct child of the article, so article.card > .row still applies', () => {
    expect(readFileSync(path.join(here, '../../app.css'), 'utf-8')).toContain('article.card > .row');
    const markup = src.slice(src.indexOf('<article'), src.indexOf('</article>'));
    const before = markup.slice(0, markup.indexOf('<div class="row" class:hand-buttons={hand}>'));
    // Every div opened before the row is closed: the head; the hand-text div sits in a closed branch.
    expect((before.match(/<div\b/g) ?? []).length).toBe((before.match(/<\/div>/g) ?? []).length);
  });
});
