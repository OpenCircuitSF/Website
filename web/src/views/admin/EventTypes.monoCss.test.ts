// #0616: the slug cell's local .mono rule. The rule is scoped CSS, so this reads
// the component source: it proves the rule exists beside the markup that uses
// it. It does not prove the rendered font.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const src = readFileSync(path.join(path.dirname(fileURLToPath(import.meta.url)), 'EventTypes.svelte'), 'utf-8');

describe('EventTypes slug cell CSS (#0616)', () => {
  it('styles the slug cell with a local .mono rule', () => {
    expect(src).toContain('<td class="mono">{type.slug}</td>');
    const style = src.slice(src.indexOf('<style>'));
    expect(style).toMatch(/\.mono\s*\{[^}]*font-family:\s*var\(--font-mono\)/);
  });
});
