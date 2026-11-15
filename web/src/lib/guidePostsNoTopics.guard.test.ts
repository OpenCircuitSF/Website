// #0660: the admin Guide outreach code takes its topic data from the API,
// never by importing the SPA's topics module. Scans the raw source of
// Admin.svelte, every views/admin/*.svelte and lib/guidePosts.ts.
import { describe, expect, it } from 'vitest';

const sources = import.meta.glob(['../views/Admin.svelte', '../views/admin/*.svelte', './guidePosts.ts'], {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;

const topicsImport = /(?:from\s+|import\s*\()\s*['"][^'"]*\/topics(?:\/topics)?(?:\.ts)?['"]/;

describe('admin code does not import the topics module', () => {
  it('scans the admin sources', () => {
    const paths = Object.keys(sources);
    expect(paths.length).toBeGreaterThanOrEqual(30);
    expect(paths.some((p) => p.endsWith('/GuidePosts.svelte'))).toBe(true);
  });

  it('no admin source imports the topics module', () => {
    const offenders = Object.entries(sources)
      .filter(([, src]) => topicsImport.test(src))
      .map(([path]) => path);
    expect(offenders).toEqual([]);
  });

  it('the detector flags the shapes it is written for', () => {
    expect(topicsImport.test("import { TOPICS } from '../lib/topics/topics';")).toBe(true);
    expect(topicsImport.test("await import('../../lib/topics/topics')")).toBe(true);
    expect(topicsImport.test('topics: GuideTopic[]')).toBe(false);
    expect(topicsImport.test("from './guidePosts'")).toBe(false);
  });
});
