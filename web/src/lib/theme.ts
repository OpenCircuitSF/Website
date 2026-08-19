// Theme toggle logic (PRD §4.2). The palette is driven by prefers-color-scheme
// with an explicit `data-theme` override; this module owns the "explicit
// override" half — reading/writing the `data-theme` attribute and persisting
// the choice to localStorage.
//
// Deliberately DOM-injectable rather than reaching for `document`/`localStorage`
// directly, so it is unit-testable without jsdom (project convention — see
// events.ts). The actual toggle button is wired up in #0017 (site header);
// this module is the tested logic it will call.
//
// The blocking inline script in index.html's <head> applies the stored theme
// before first paint using the same 'theme' key and the same light/dark
// validity check, duplicated there (not imported) because it must run before
// any module script loads.

/** The three states the toggle cycles through. "auto" means "no override" —
 * the page follows the OS `prefers-color-scheme` setting. */
export type Theme = 'auto' | 'light' | 'dark';

const ORDER: readonly Theme[] = ['auto', 'light', 'dark'];
const STORAGE_KEY = 'theme';

/** The minimal Element surface this module needs from document.documentElement. */
export interface ThemeRoot {
  getAttribute(name: string): string | null;
  setAttribute(name: string, value: string): void;
  removeAttribute(name: string): void;
}

/** The minimal Storage surface this module needs from window.localStorage. */
export interface ThemeStorage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

/** Read the active theme off `data-theme`. Any value other than "light" or
 * "dark" (including absent) reads as "auto". */
export function currentTheme(root: ThemeRoot): Theme {
  const t = root.getAttribute('data-theme');
  return t === 'light' || t === 'dark' ? t : 'auto';
}

/** The next theme in the auto -> light -> dark -> auto cycle. */
export function nextTheme(theme: Theme): Theme {
  return ORDER[(ORDER.indexOf(theme) + 1) % ORDER.length];
}

/**
 * Apply a theme to the root element and persist the choice.
 *
 * "auto" removes the attribute entirely (rather than pinning whatever the OS
 * value currently resolves to) so prefers-color-scheme keeps driving the page
 * afterward, and clears the stored choice so a later visit is auto too.
 *
 * Storage access is wrapped in try/catch: Safari private-browsing mode throws
 * on localStorage.setItem/removeItem rather than silently no-op'ing, and a
 * storage failure must never block applying the visual theme.
 */
export function applyTheme(root: ThemeRoot, storage: ThemeStorage | undefined, theme: Theme): void {
  if (theme === 'auto') {
    root.removeAttribute('data-theme');
    try {
      storage?.removeItem(STORAGE_KEY);
    } catch {
      // Safari private mode, storage disabled, quota, etc. — the visual
      // theme is already applied; persistence is best-effort.
    }
  } else {
    root.setAttribute('data-theme', theme);
    try {
      storage?.setItem(STORAGE_KEY, theme);
    } catch {
      // See above.
    }
  }
}

/** Advance to the next theme in the cycle and apply it. Returns the theme now
 * in effect, so a caller (e.g. a toggle button) can update its own label. */
export function cycleTheme(root: ThemeRoot, storage?: ThemeStorage): Theme {
  const next = nextTheme(currentTheme(root));
  applyTheme(root, storage, next);
  return next;
}

/** Read a previously-stored explicit theme, or null if none/invalid/storage
 * inaccessible. Mirrors the blocking inline script's read in index.html. */
export function readStoredTheme(storage: ThemeStorage): 'light' | 'dark' | null {
  try {
    const t = storage.getItem(STORAGE_KEY);
    return t === 'light' || t === 'dark' ? t : null;
  } catch {
    return null;
  }
}
