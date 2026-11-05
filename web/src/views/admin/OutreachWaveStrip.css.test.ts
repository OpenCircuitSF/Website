// #0622: jsdom has no layout, so it cannot prove pinning. This reads the
// sources: it proves the rules exist and the one panel that would defeat them
// opts out. It does not prove the strip pins in a real browser.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const here = path.dirname(fileURLToPath(import.meta.url));
const read = (rel: string) => readFileSync(path.join(here, rel), 'utf-8');
const styleOf = (src: string) => src.slice(src.indexOf('<style>'));

describe('the pinned strip (#0622)', () => {
  it('the pin rule is sticky at the top with a solid background', () => {
    const rule = /\.wave-pin\s*\{([^}]*)\}/.exec(read('../../app.css'))?.[1] ?? '';
    expect(rule).toMatch(/position:\s*sticky/);
    expect(rule).toMatch(/top:\s*0/);
    expect(rule).toMatch(/background:\s*var\(--bg-panel\)/);
  });
  it('the Outreach panel opts out of overflow hidden', () => {
    expect(read('../../lib/Panel.svelte')).toMatch(/\.panel\.pinnable\s*\{[^}]*overflow:\s*clip/);
    expect(read('OutreachWorkshop.svelte')).toContain('<Panel pinnable>');
  });
  it('the strip has one site and it carries the pin class', () => {
    expect(read('OutreachWaveRow.svelte')).toContain('<div class="wave-pin row-head">');
    expect(read('OutreachAnnouncement.svelte')).not.toContain('wave-pin');
  });
  it('the scroll margin clears the tallest strip (#0625)', () => {
    expect(read('../../app.css')).toMatch(/\.wave-section[^{]*\{\s*scroll-margin-top:\s*5\.5rem/);
  });
  it('nothing between the panel and the strip clips it (#0625)', () => {
    const style = styleOf(read('OutreachWaveRow.svelte'));
    expect(style).not.toContain('overflow');
    expect(style).not.toContain('contain');
  });
  it('the strip has no native disclosure, and its marker cannot take its own line (#0625)', () => {
    const row = read('OutreachWaveRow.svelte');
    expect(row).not.toContain('<details');
    expect(row).not.toContain('<summary');
    const style = styleOf(read('OutreachWaveStrip.svelte'));
    expect(style).toMatch(/\.strip-button\s*\{[^}]*display:\s*flex/);
    expect(style).toMatch(/\.chevron\s*\{[^}]*flex:\s*none/);
  });
  it('the strip and thumbnail use tokens, not literal colours', () => {
    for (const f of ['OutreachWaveStrip.svelte', 'OutreachCoverImage.svelte']) {
      expect(styleOf(read(f))).not.toMatch(/#[0-9a-fA-F]{3,8}\b|rgb\(|hsl\(/);
    }
  });
});
