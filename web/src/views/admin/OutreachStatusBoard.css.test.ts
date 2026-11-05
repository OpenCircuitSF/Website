// #0624: jsdom has no layout, so it cannot prove the board fits or stacks. This
// reads the source: it proves the rules exist. It does not prove the layout in
// a real browser.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const here = path.dirname(fileURLToPath(import.meta.url));
const src = readFileSync(path.join(here, 'OutreachStatusBoard.svelte'), 'utf-8');
const style = src.slice(src.indexOf('<style>'));
const narrow = style.slice(style.indexOf('@media (max-width: 860px)'));

describe('the status board styles (#0624)', () => {
  it('stacks at narrow widths and hides the column headers', () => {
    expect(style).toContain('@media (max-width: 860px)');
    expect(narrow).toMatch(/thead\s*\{\s*display:\s*none/);
  });
  it("shows each cell's channel only when stacked", () => {
    const wide = style.slice(0, style.indexOf('@media'));
    expect(wide).toMatch(/\.board-channel\s*\{\s*display:\s*none/);
    expect(narrow).toMatch(/\.board-channel\s*\{\s*display:\s*block/);
  });
  it('fits the width instead of scrolling sideways', () => {
    expect(style).toMatch(/table-layout:\s*fixed/);
    expect(style).not.toContain('overflow-x');
  });
  it('uses tokens and gives no status its own colour', () => {
    expect(style).not.toMatch(/#[0-9a-fA-F]{3,8}\b|rgb\(|hsl\(/);
    expect(style).not.toContain('data-kind');
    const statusRules = [...style.matchAll(/([^{}]*\.board-status[^{}]*)\{([^}]*)\}/g)];
    expect(statusRules.length).toBeGreaterThan(0);
    for (const m of statusRules) expect(m[2]).not.toMatch(/(^|[\s;])color:/);
  });
  it('the whole row heading is the target (#0625)', () => {
    expect(style).toMatch(/\.board-select::after\s*\{[^}]*inset:\s*0/);
    expect(style).toMatch(/\.board-row-head\s*\{[^}]*position:\s*relative/);
  });
  it('hides the caption visually, not from screen readers, when stacked (#0625)', () => {
    expect(narrow).toMatch(/\.board-caption\s*\{[^}]*clip-path/);
    expect(style).not.toMatch(/\.board-caption\s*\{[^}]*display:\s*none/);
  });
});
