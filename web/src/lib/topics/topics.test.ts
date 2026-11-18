import { describe, expect, it } from 'vitest';
import { TOPIC_GUIDE_SLUGS, parsePath } from '../router';
import { TOPICS, TOPIC_BODIES } from './topics';
import { TOPIC_GUIDES } from '../guides';
import { renderTemps } from '../temperature';
import IronTempDemo from './IronTempDemo.svelte';
import SolderWireDemo from './SolderWireDemo.svelte';
import MosfetSwitchDemo from './MosfetSwitchDemo.svelte';
import SwitchPickerDemo from './SwitchPickerDemo.svelte';

const demoSources = import.meta.glob('./*.svelte', { query: '?raw', import: 'default', eager: true }) as Record<string, string>;

describe('topic guides', () => {
  it('router slugs match the topic list exactly', () => {
    expect([...TOPIC_GUIDE_SLUGS].sort()).toEqual(TOPICS.map((t) => t.slug).sort());
  });

  it('routes known topics and 404s unknown ones', () => {
    expect(parsePath('/guides').name).toBe('guides');
    expect(parsePath('/guides/baud-rate').name).toBe('topic-guide');
    expect(parsePath('/guides/nope').name).toBe('not-found');
  });

  it('every prose token renders', () => {
    for (const t of TOPICS)
      for (const u of ['F', 'C'] as const) {
        const texts = [t.tryIt, t.takeaway, ...t.ideas.flatMap((i) => [i.title, i.body])];
        for (const x of texts) expect(renderTemps(x, u), t.slug).not.toContain('{temp');
      }
  });

  it('temps flag matches content', () => {
    const tempDemos = new Set<unknown>([IronTempDemo, SolderWireDemo, MosfetSwitchDemo, SwitchPickerDemo]);
    for (const [slug, b] of Object.entries(TOPIC_BODIES)) {
      const prose = [b.tryIt, b.takeaway, ...b.ideas.flatMap((i) => [i.title, i.body])].some((x) => x.includes('{temp:'));
      expect(b.temps === true, slug).toBe(prose || tempDemos.has(b.demo));
    }
    const importers = Object.entries(demoSources)
      .filter(([, src]) => src.includes('tempUnit.svelte'))
      .map(([k]) => k)
      .sort();
    expect(importers).toEqual(['./IronTempDemo.svelte', './MosfetSwitchDemo.svelte', './SolderWireDemo.svelte', './SwitchPickerDemo.svelte']);
  });

  it('every topic has a post short enough for Bluesky with a link', () => {
    for (const t of TOPICS) expect(t.post.length + 1 + 60).toBeLessThanOrEqual(300);
  });

  it('every catalog topic has a body and every body a catalog entry', () => {
    expect(Object.keys(TOPIC_BODIES).sort()).toEqual(TOPIC_GUIDES.map((g) => g.slug).sort());
  });
});
