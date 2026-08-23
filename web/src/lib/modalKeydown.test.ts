// #0120: unit coverage for the one function that now decides whether a
// keydown event should dismiss an admin modal. This is deliberately the
// pure-logic half of the fix -- #0094 is still open (no DOM harness, no
// .svelte file is imported by a test), so a real click-and-Escape
// interaction is not something this suite can drive. What IS provable here:
// the function's own truth table, and (in
// web/src/views/Admin.escapeWiring.structuralGuard.test.ts,
// CampaignEditor.escapeWiring.structuralGuard.test.ts, and
// Campaigns.escapeWiring.structuralGuard.test.ts) that every modal this
// issue touched actually calls this function from its panel's onkeydown
// instead of the old blind `e.stopPropagation()`.
import { describe, it, expect } from 'vitest';
import { isModalEscape } from './modalKeydown';

describe('isModalEscape', () => {
  it('is true for Escape when dismissible (the default)', () => {
    expect(isModalEscape({ key: 'Escape' })).toBe(true);
  });

  it('is false for any other key', () => {
    expect(isModalEscape({ key: 'Enter' })).toBe(false);
    expect(isModalEscape({ key: 'a' })).toBe(false);
    expect(isModalEscape({ key: 'Tab' })).toBe(false);
    expect(isModalEscape({ key: '' })).toBe(false);
  });

  it('is false for Escape when dismissible is explicitly false', () => {
    expect(isModalEscape({ key: 'Escape' }, false)).toBe(false);
  });

  it('is true for Escape when dismissible is explicitly true', () => {
    expect(isModalEscape({ key: 'Escape' }, true)).toBe(true);
  });

  it('is false for a non-Escape key regardless of dismissible', () => {
    expect(isModalEscape({ key: 'Enter' }, true)).toBe(false);
    expect(isModalEscape({ key: 'Enter' }, false)).toBe(false);
  });
});
