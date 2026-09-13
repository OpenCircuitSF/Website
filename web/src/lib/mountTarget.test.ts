// @vitest-environment jsdom
//
// #0519: proves prepareMountTarget clears #app before mount, and that
// mounting a view over a server-rendered fallback leaves exactly one <h1>
// with tabindex="-1" -- the property #0238's focus-move effect and #0517's
// styling both depend on. jsdom is not a browser (CLAUDE.md §1): this
// proves there is no duplicate <h1> under jsdom, not in a real browser.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { mount, unmount } from 'svelte';
import { afterEach, describe, expect, it } from 'vitest';
import { prepareMountTarget } from './mountTarget';
import NotFound from '../views/NotFound.svelte';

afterEach(() => {
  document.body.innerHTML = '';
});

describe('prepareMountTarget', () => {
  it('removes server fallback before mount', () => {
    document.body.innerHTML =
      '<div id="app"><header></header><main id="main-content"><h1>Server heading</h1><p>Server text.</p></main></div>';

    const target = prepareMountTarget();

    expect(target.id).toBe('app');
    expect(target.children.length).toBe(0);
  });

  it('throws when #app is missing', () => {
    document.body.innerHTML = '';
    expect(() => prepareMountTarget()).toThrow();
  });

  it('exactly one h1 after mounting a view over server fallback', () => {
    document.body.innerHTML =
      '<div id="app"><header></header><main id="main-content"><h1>Server heading</h1><p>Server text.</p></main></div>';

    // NotFound.svelte is static and makes no fetch -- see its own file for
    // why it's a safe component to mount in a test with no API mocking.
    const instance = mount(NotFound, { target: prepareMountTarget() });

    const headings = document.querySelectorAll('h1');
    expect(headings.length).toBe(1);
    expect(headings[0].getAttribute('tabindex')).toBe('-1');
    expect(document.body.textContent).not.toContain('Server heading');
    expect(document.querySelectorAll('#main-content').length).toBe(1);

    unmount(instance);
  });
});

describe('main.ts wires prepareMountTarget', () => {
  it('mounts through prepareMountTarget, not a raw getElementById', () => {
    const __filename = fileURLToPath(import.meta.url);
    const mainTsPath = path.resolve(path.dirname(__filename), '..', 'main.ts');
    const source = readFileSync(mainTsPath, 'utf-8');

    expect(source).toContain("import { prepareMountTarget } from './lib/mountTarget';");
    expect(source).toMatch(/mount\(App,\s*\{\s*target:\s*prepareMountTarget\(/);
    expect(source).not.toContain("getElementById('app')");
  });
});
