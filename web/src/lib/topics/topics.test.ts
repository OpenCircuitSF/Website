import { describe, expect, it } from 'vitest';
import { TOPIC_GUIDE_SLUGS, parsePath } from '../router';
import { TOPICS, TOPIC_BODIES } from './topics';
import { TOPIC_GUIDES } from '../guides';

describe('topic guides', () => {
  it('router slugs match the topic list exactly', () => {
    expect([...TOPIC_GUIDE_SLUGS].sort()).toEqual(TOPICS.map((t) => t.slug).sort());
  });

  it('routes known topics and 404s unknown ones', () => {
    expect(parsePath('/guides').name).toBe('guides');
    expect(parsePath('/guides/baud-rate').name).toBe('topic-guide');
    expect(parsePath('/guides/nope').name).toBe('not-found');
  });

  it('every topic has a post short enough for Bluesky with a link', () => {
    for (const t of TOPICS) expect(t.post.length + 1 + 60).toBeLessThanOrEqual(300);
  });

  it('every catalog topic has a body and every body a catalog entry', () => {
    expect(Object.keys(TOPIC_BODIES).sort()).toEqual(TOPIC_GUIDES.map((g) => g.slug).sort());
  });
});
