// #0622: jsdom has no layout, so it cannot prove pinning. This reads the
// sources: it proves the rules exist and the one panel that would defeat them
// opts out. It does not prove the tabs pin in a real browser.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const here = path.dirname(fileURLToPath(import.meta.url));
const read = (rel: string) => readFileSync(path.join(here, rel), 'utf-8');
const styleOf = (src: string) => src.slice(src.indexOf('<style>'));

describe('the pinned section tabs (#0622, #0640)', () => {
  it('the pin rule is sticky at the top with a solid background', () => {
    const rule = /\.wave-pin\s*\{([^}]*)\}/.exec(read('../../app.css'))?.[1] ?? '';
    expect(rule).toMatch(/position:\s*sticky/);
    expect(rule).toMatch(/top:\s*var\(--reload-banner-h,\s*0px\)/);
    expect(rule).toMatch(/background:\s*var\(--bg-panel\)/);
  });
  it('the tabs pin below the failure banner (#0633)', () => {
    expect(read('OutreachPanel.svelte')).toContain('style:--reload-banner-h=');
  });
  it('the Outreach panel opts out of overflow hidden', () => {
    expect(read('../../lib/Panel.svelte')).toMatch(/\.panel\.pinnable\s*\{[^}]*overflow:\s*clip/);
    expect(read('OutreachWorkshop.svelte')).toContain('<Panel pinnable>');
  });
  it('the tabs have one site and it carries the pin class', () => {
    expect(read('OutreachSectionTabs.svelte')).toContain('<div class="wave-pin section-tabs"');
    expect(read('OutreachSection.svelte')).not.toContain('wave-pin');
    expect(read('OutreachAnnouncement.svelte')).not.toContain('wave-pin');
  });
  it('the failed-reload banner pins above the strip, so it stays on screen (#0625)', () => {
    const rule = /\.reload-banner\s*\{([^}]*)\}/.exec(styleOf(read('OutreachPanel.svelte')))?.[1] ?? '';
    const pin = /\.wave-pin\s*\{([^}]*)\}/.exec(read('../../app.css'))?.[1] ?? '';
    expect(rule).toMatch(/position:\s*sticky/);
    expect(rule).toMatch(/top:\s*0/);
    expect(rule).toMatch(/background:\s*var\(--bg-panel\)/);
    expect(Number(/z-index:\s*(\d+)/.exec(rule)?.[1])).toBeGreaterThan(Number(/z-index:\s*(\d+)/.exec(pin)?.[1]));
  });
  it('the scroll margin clears the tallest strip (#0625)', () => {
    expect(read('../../app.css')).toMatch(/\.wave-section[^{]*\{\s*scroll-margin-top:\s*calc\(7rem \+ var\(--reload-banner-h, 0px\)\)/);
  });
  it('nothing between the panel and the strip clips it (#0625)', () => {
    for (const f of ['OutreachSection.svelte', 'OutreachSectionTabs.svelte']) {
      const style = styleOf(read(f));
      expect(style).not.toContain('overflow');
      expect(style).not.toContain('contain');
    }
  });
  it('the tabs and thumbnail use tokens, not literal colours', () => {
    for (const f of ['OutreachSectionTabs.svelte', 'OutreachCoverImage.svelte']) {
      expect(styleOf(read(f))).not.toMatch(/#[0-9a-fA-F]{3,8}\b|rgb\(|hsl\(/);
    }
  });
});
