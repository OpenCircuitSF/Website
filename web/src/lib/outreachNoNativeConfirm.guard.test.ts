// #0581: the outreach pages confirm in-app (OutreachConfirmDialog /
// OutreachPostDialog / OutreachScheduleAllDialog), never with window.confirm,
// which is unstyled, unlabelled for assistive tech and untestable in jsdom.
// Scans the raw source of every web/src/views/admin/Outreach*.svelte.
import { describe, expect, it } from 'vitest';

const sources = import.meta.glob('../views/admin/Outreach*.svelte', { query: '?raw', import: 'default', eager: true }) as Record<string, string>;

// A call to confirm with a string, template or identifier argument (or none),
// not preceded by an identifier character or a dot and not a declaration
// (`function confirm(`), plus any explicit window.confirm.
const nativeConfirm = new RegExp('(?<![\\w.])(?<!function\\s+)(?:window\\.)?confirm\\(\\s*[`\'"\\w$)]');
const windowConfirm = /window\.confirm/;

describe('no native confirm in the outreach pages (#0581)', () => {
  it('scans a non-trivial number of files', () => {
    expect(Object.keys(sources).length).toBeGreaterThanOrEqual(10);
  });

  it('no Outreach*.svelte calls window.confirm or confirm with a message', () => {
    const offenders = Object.entries(sources)
      .filter(([, src]) => nativeConfirm.test(src) || windowConfirm.test(src))
      .map(([path]) => path);
    expect(offenders).toEqual([]);
  });

  it('the detector flags the shapes it is written for', () => {
    expect(nativeConfirm.test("if (!confirm('Sure?')) return;")).toBe(true);
    expect(nativeConfirm.test('if (!window.confirm(`Sure?`)) return;')).toBe(true);
    expect(nativeConfirm.test('function confirm(x) {}')).toBe(false);
    expect(nativeConfirm.test('onConfirm(')).toBe(false);
    // #0593: an identifier argument is a native confirm too.
    expect(nativeConfirm.test('if (!confirm(message)) return;')).toBe(true);
    expect(nativeConfirm.test('if (!confirm()) return;')).toBe(true);
    expect(nativeConfirm.test('async function confirm(postIds: number[]) {}')).toBe(false);
    expect(nativeConfirm.test('await confirm(postIds, times);')).toBe(true);
  });
});
