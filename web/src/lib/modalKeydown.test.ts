// #0120: unit coverage for the one function that now decides whether a
// keydown event should dismiss an admin modal. This is deliberately the
// pure-logic half of the fix -- what this file alone proves is the
// function's own truth table (`dismissible && key === 'Escape'`), nothing
// about wiring.
//
// The wiring claims live in sibling files, and here is what each actually
// proves, so this comment does not overstate coverage the way an earlier
// version of it did (#0216 -- that version cited three
// "*.escapeWiring.structuralGuard.test.ts" files under web/src/views/ that
// were never written):
//   - modalEscapeGuard.test.ts (this directory) proves the NEGATIVE half
//     repo-wide: no `role="dialog" aria-modal="true"` panel still carries
//     the blind `onkeydown={(e) => e.stopPropagation()}` shape this issue
//     removed.
//   - modalFocusWiring.structuralGuard.test.ts (this directory) proves the
//     POSITIVE half repo-wide: every such panel that binds focus via
//     `bind:this` also carries `onkeydown` on that SAME node, and that node
//     is what a `tick().then(() => ...?.focus())` call actually targets --
//     i.e. this function, when a modal calls it, is being asked about a
//     keydown on the node that has focus.
//   - ../views/admin/CampaignSendDialog.behavior.test.ts (#0094) proves the
//     same positive wiring behaviourally for one component: mounts it under
//     jsdom, lets the real focus effect run, dispatches a real Escape
//     keydown, and asserts the close callback fires.
// Between them, every fixed modal's panel is covered for BOTH "does the
// right function get called" (this file) and "is it called on the right
// node" (the other two) -- except that the AST check does not itself
// re-derive isModalEscape's truth table (this file does that), so a
// hypothetical panel that called isModalEscape with the args reversed would
// not be caught by any of these three files. Not currently possible here --
// every call site is `isModalEscape(e)`, a single argument -- but worth
// naming as this comment's own honest limit rather than leaving it implied.
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
