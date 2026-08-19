import { describe, expect, it } from 'vitest';
import {
  applyTheme,
  currentTheme,
  cycleTheme,
  nextTheme,
  readStoredTheme,
  type Theme,
  type ThemeRoot,
  type ThemeStorage,
} from './theme';

/** A hand-rolled fake root (see events.test.ts for the same DOM-avoidance pattern). */
function fakeRoot(initial: string | null = null): ThemeRoot & { attr: string | null } {
  return {
    attr: initial,
    getAttribute(name: string) {
      return name === 'data-theme' ? this.attr : null;
    },
    setAttribute(name: string, value: string) {
      if (name === 'data-theme') this.attr = value;
    },
    removeAttribute(name: string) {
      if (name === 'data-theme') this.attr = null;
    },
  };
}

/** A fake localStorage backed by a plain object, with an option to throw on
 * write — exercising the Safari-private-mode path. */
function fakeStorage(opts: { throwOnWrite?: boolean } = {}): ThemeStorage & { data: Record<string, string> } {
  const data: Record<string, string> = {};
  return {
    data,
    getItem(key: string) {
      return key in data ? data[key] : null;
    },
    setItem(key: string, value: string) {
      if (opts.throwOnWrite) throw new Error('QuotaExceededError');
      data[key] = value;
    },
    removeItem(key: string) {
      if (opts.throwOnWrite) throw new Error('SecurityError');
      delete data[key];
    },
  };
}

describe('currentTheme', () => {
  it('reads "auto" when data-theme is absent', () => {
    expect(currentTheme(fakeRoot(null))).toBe('auto');
  });

  it('reads "light" and "dark" verbatim', () => {
    expect(currentTheme(fakeRoot('light'))).toBe('light');
    expect(currentTheme(fakeRoot('dark'))).toBe('dark');
  });

  it('treats an unrecognized attribute value as "auto"', () => {
    // Proves the guard actually checks the value, not just presence — a
    // corrupted or stale attribute doesn't crash the cycle, it just resets it.
    expect(currentTheme(fakeRoot('sepia'))).toBe('auto');
  });
});

describe('nextTheme', () => {
  it('cycles auto -> light -> dark -> auto', () => {
    expect(nextTheme('auto')).toBe('light');
    expect(nextTheme('light')).toBe('dark');
    expect(nextTheme('dark')).toBe('auto');
  });
});

describe('applyTheme', () => {
  it('"auto" removes the attribute and clears storage', () => {
    const root = fakeRoot('dark');
    const storage = fakeStorage();
    storage.data.theme = 'dark';

    applyTheme(root, storage, 'auto');

    expect(root.attr).toBeNull();
    expect(storage.data.theme).toBeUndefined();
  });

  it('"light"/"dark" sets the attribute and persists it', () => {
    const root = fakeRoot(null);
    const storage = fakeStorage();

    applyTheme(root, storage, 'light');
    expect(root.attr).toBe('light');
    expect(storage.data.theme).toBe('light');

    applyTheme(root, storage, 'dark');
    expect(root.attr).toBe('dark');
    expect(storage.data.theme).toBe('dark');
  });

  it('applies the visual theme even when storage throws (Safari private mode)', () => {
    const root = fakeRoot(null);
    const storage = fakeStorage({ throwOnWrite: true });

    // This is the assertion that actually bites: without the try/catch in
    // applyTheme, this call throws and the test fails here.
    expect(() => applyTheme(root, storage, 'dark')).not.toThrow();
    expect(root.attr).toBe('dark');

    expect(() => applyTheme(root, storage, 'auto')).not.toThrow();
    expect(root.attr).toBeNull();
  });

  it('works with no storage argument at all', () => {
    const root = fakeRoot(null);
    expect(() => applyTheme(root, undefined, 'light')).not.toThrow();
    expect(root.attr).toBe('light');
  });
});

describe('cycleTheme', () => {
  it('advances through the full cycle and returns the new theme', () => {
    const root = fakeRoot(null);
    const storage = fakeStorage();

    const seen: Theme[] = [];
    seen.push(cycleTheme(root, storage)); // auto -> light
    seen.push(cycleTheme(root, storage)); // light -> dark
    seen.push(cycleTheme(root, storage)); // dark -> auto

    expect(seen).toEqual(['light', 'dark', 'auto']);
    expect(root.attr).toBeNull();
    expect(storage.data.theme).toBeUndefined();
  });
});

describe('readStoredTheme', () => {
  it('returns the stored value when valid', () => {
    const storage = fakeStorage();
    storage.data.theme = 'dark';
    expect(readStoredTheme(storage)).toBe('dark');
  });

  it('returns null when nothing is stored', () => {
    expect(readStoredTheme(fakeStorage())).toBeNull();
  });

  it('returns null for an invalid stored value', () => {
    const storage = fakeStorage();
    storage.data.theme = 'sepia';
    expect(readStoredTheme(storage)).toBeNull();
  });

  it('returns null (not a throw) when storage access throws', () => {
    const storage: ThemeStorage = {
      getItem() {
        throw new Error('SecurityError');
      },
      setItem() {},
      removeItem() {},
    };
    expect(readStoredTheme(storage)).toBeNull();
  });
});
