// #0590 (J1): the 480px card rule for OutreachLocations.svelte. jsdom does not
// evaluate media queries, so this reads the component source: it proves the
// rule exists and targets the hooks OutreachLocations.behavior.test.ts proves
// are in the markup. It does not prove 390px has no horizontal scroll.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const src = readFileSync(path.join(path.dirname(fileURLToPath(import.meta.url)), 'OutreachLocations.svelte'), 'utf-8');

describe('OutreachLocations narrow layout CSS (#0590)', () => {
  const media = src.slice(src.indexOf('@media (max-width: 480px)'));
  it('has a max-width 480px block that turns rows into cards', () => {
    expect(src).toContain('@media (max-width: 480px)');
    expect(media).toContain('.loc-table tr');
    expect(media).toMatch(/\.loc-table td\[data-label\]::before/);
  });
  it('stacks the action buttons full width inside it', () => {
    expect(media).toMatch(/\.loc-row-actions\s*\{[^}]*flex-direction:\s*column/);
    expect(media).toMatch(/\.loc-row-actions > :global\(\*\)\s*\{[^}]*width:\s*100%/);
    // The flex rule must outrank `.loc-table td { display: block }`, or it is dead.
    expect(media).toMatch(/\.loc-table td\.loc-row-actions\s*\{[^}]*display:\s*flex/);
  });
});
