// #0717: main.ts must not mount App until the current route's lazy chunks are
// in. Mounting first would clear the server fallback (#0519) and show a blank
// shell or "Loading…" while the chunk downloads -- the flash the split must
// not introduce. This runs main.ts itself with mount() and the preload stubbed.
import { describe, expect, it, vi } from 'vitest';

const mount = vi.fn();
let release: () => void = () => {};
const preloadForRoute = vi.fn(() => new Promise<void>((r) => (release = r)));

vi.mock('svelte', async (orig) => ({ ...(await orig<typeof import('svelte')>()), mount }));
vi.mock('../App.svelte', () => ({ default: {} }));
vi.mock('./mountTarget', () => ({ prepareMountTarget: () => ({}) }));
vi.mock('./routeViews', () => ({ preloadForRoute }));

describe('main.ts waits for the route before mounting (#0717)', () => {
  it('mounts only after preloadForRoute settles', async () => {
    await import('../main');
    expect(preloadForRoute).toHaveBeenCalledTimes(1);
    await Promise.resolve();
    expect(mount).not.toHaveBeenCalled();
    release();
    await vi.waitFor(() => expect(mount).toHaveBeenCalledTimes(1));
  });
});
