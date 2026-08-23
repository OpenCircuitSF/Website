// #0157: nothing in the build previously guarded the Go<->TypeScript
// isSafeCoverImage/isSafeLinkHref twins against drifting apart. Parity was
// established three separate times (#0138 x2, #0152) by throwaway sweeps
// that were deleted right after -- one of those found a live bypass
// (strings.TrimSpace vs JS trim() disagreeing on U+FEFF) invisible to every
// test that existed at the time.
//
// testdata/url_validators.json is that sweep, made permanent -- see
// internal/handlers/url_validator_fixture_test.go's header comment for the
// full generation method (V8-ground-truth ECMA-262 whitespace sweep, every
// codepoint 0x00-0xFF in four positions, scheme shapes, protocol-relative
// and backslash forms, legitimate cases). Both files read the SAME JSON, so
// a divergence between the two languages -- or a regression in either one --
// is a red build here and in `go test`, not a discovery three issues later.

import { describe, it, expect } from 'vitest';
import { isSafeLinkHref } from './linkSafety';
import { isSafeCoverImage } from './workshopAdmin';
// tsconfig.json sets resolveJsonModule, so this is a typed static import --
// no node:fs/node:path (this project carries no @types/node dependency,
// per web/tsconfig.json's `types: ["svelte", "vite/client"]`) and no
// runtime path resolution to get wrong. The path reaches outside web/ on
// purpose: the fixture is shared with internal/handlers's Go test, which
// reads the identical file via ../../testdata/url_validators.json relative
// to its own package directory.
import rawCases from '../../../testdata/url_validators.json';

interface FixtureCase {
  id: string;
  rule: 'link_href' | 'cover_image';
  source: string;
  value: string;
  want: boolean;
}

const cases = rawCases as FixtureCase[];

describe('Go<->TypeScript URL validator fixture (#0157)', () => {
  it('is non-empty and covers both rules', () => {
    expect(cases.length).toBeGreaterThan(1000);
    const rules = new Set(cases.map((c) => c.rule));
    expect(rules).toEqual(new Set(['link_href', 'cover_image']));
  });

  for (const c of cases) {
    it(`${c.rule} ${c.id}: ${JSON.stringify(c.value).slice(0, 50)} -> ${c.want}`, () => {
      const fn = c.rule === 'link_href' ? isSafeLinkHref : isSafeCoverImage;
      expect(fn(c.value)).toBe(c.want);
    });
  }
});
