// #0627: the editor's wrapping rules. jsdom does no layout, so this reads the
// component source: it proves the rule exists and not how it looks.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const src = readFileSync(path.join(path.dirname(fileURLToPath(import.meta.url)), 'OutreachComposer.svelte'), 'utf-8');

describe('OutreachComposer layout CSS (#0627)', () => {
  it('the action row wraps on a phone', () => {
    expect(src).toContain('class="row actions"');
    expect(src).toMatch(/\.actions\s*\{[^}]*flex-wrap:\s*wrap/);
  });
});
