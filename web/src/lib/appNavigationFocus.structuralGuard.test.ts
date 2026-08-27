// #0238 review (2afea58): heading?.focus() in App.svelte's route-change
// effect used the default `preventScroll: false`, which scrolled the new
// heading into view AFTER router.ts's restoreScroll() had already put the
// page back where the user was on a Back navigation -- and the throttled
// scroll listener then stamped that bogus offset into
// history.state.scrollY, destroying the saved position rather than merely
// overriding it for one paint. Measured in real Safari (26.6.2, via
// safaridriver, against a production build): window.scrollY and
// history.state.scrollY went from 1200/1200 (immediately before #0238,
// 35288dd) to 0/0 (with #0238's original fix, 74bdccf). The fix --
// `heading?.focus({ preventScroll: true })` -- was verified the same way:
// re-running the identical script after the fix restored 1200/1200 while
// title and activeElement stayed correct.
//
// THAT is a browser-behaviour fact (jsdom does not implement scroll-into-
// view-on-focus at all, with or without preventScroll, so a jsdom test
// asserting window.scrollY would pass identically whether this option is
// present, true, or false -- it would be zero evidence, not weak evidence).
// This guard does NOT attempt to reproduce it. What IS mechanically
// testable without a browser is the one fact a jsdom/mount test cannot
// verify any more reliably than reading the source: that the ACTUAL call
// site in App.svelte still passes `{ preventScroll: true }` to focus(),
// so a future edit that drops the option (or flips it to false) fails
// loudly here instead of silently reintroducing the scroll-restoration
// defect. Structural (AST only, svelte/compiler, no mounting) -- same
// class of guard as liveRegionGuard/modalFocusWiring/pageTitle.guard, and
// for the same reason: this is a source-code invariant, not a runtime one.
import { describe, expect, it } from 'vitest';
import { parse as parseSvelte } from 'svelte/compiler';
// eslint-disable-next-line import/no-unresolved -- vite ?raw import, not a module
import appSource from '../App.svelte?raw';

type AstNode = Record<string, unknown>;

function findFocusCalls(node: unknown, out: AstNode[]): void {
  if (node === null || typeof node !== 'object') return;
  if (Array.isArray(node)) {
    for (const item of node) findFocusCalls(item, out);
    return;
  }
  const obj = node as AstNode;
  if (obj.type === 'CallExpression') {
    const callee = obj.callee as AstNode | undefined;
    if (callee?.type === 'MemberExpression') {
      const property = callee.property as AstNode | undefined;
      if (property?.type === 'Identifier' && property.name === 'focus') {
        out.push(obj);
      }
    }
  }
  for (const key of Object.keys(obj)) {
    if (key === 'parent') continue;
    findFocusCalls(obj[key], out);
  }
}

describe('App.svelte route-change focus() guard (#0238)', () => {
  it('calls heading.focus({ preventScroll: true }), not the scroll-restoring default', () => {
    const ast = parseSvelte(appSource, { filename: 'App.svelte', modern: true }) as unknown as AstNode;
    const scriptContent = (ast.instance as AstNode | undefined)?.content;
    if (!scriptContent) {
      throw new Error('appNavigationFocus.guard: App.svelte has no <script> instance content to scan');
    }

    const focusCalls: AstNode[] = [];
    findFocusCalls(scriptContent, focusCalls);

    // Fail loudly rather than silently passing an empty scan -- this guard
    // is worthless if the call it exists to check has moved or been
    // renamed out from under it (CLAUDE.md's fail-open warning).
    if (focusCalls.length === 0) {
      throw new Error(
        'appNavigationFocus.guard: found no .focus() call in App.svelte\'s <script> -- has the route-change focus move been removed or restructured?',
      );
    }

    for (const call of focusCalls) {
      const args = call.arguments as AstNode[] | undefined;
      const firstArg = args?.[0];
      if (!firstArg || firstArg.type !== 'ObjectExpression') {
        throw new Error(
          `appNavigationFocus.guard: App.svelte:${(call.loc as { start: { line: number } }).start.line} calls .focus() without an options object -- this defaults to preventScroll: false, which broke back/forward scroll restoration (#0238's review)`,
        );
      }
      const properties = (firstArg.properties as AstNode[] | undefined) ?? [];
      const preventScrollProp = properties.find((p) => {
        const key = p.key as AstNode | undefined;
        return key?.type === 'Identifier' && key.name === 'preventScroll';
      });
      const value = preventScrollProp?.value as AstNode | undefined;
      const isTrue = value?.type === 'Literal' && value.value === true;

      if (!isTrue) {
        throw new Error(
          `appNavigationFocus.guard: App.svelte:${(call.loc as { start: { line: number } }).start.line}'s .focus() call does not pass { preventScroll: true } -- this is the exact regression #0238's review measured in real Safari (window.scrollY/history.state.scrollY went from 1200/1200 to 0/0 after Back)`,
        );
      }
    }

    expect(focusCalls.length).toBeGreaterThanOrEqual(1);
  });
});
