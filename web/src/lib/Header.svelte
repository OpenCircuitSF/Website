<script lang="ts">
  // The persistent header shell for every public marketing page (#0017):
  // logo mark + wordmark, primary nav (Workshops / About / Subscribe, plus
  // Admin for signed-in staff), and the theme toggle. Staff sign-in is
  // deliberately NOT here -- it lives in Footer.svelte as an unobtrusive
  // link, per the acceptance criteria ("reachable but unobtrusive").
  //
  // The skip-to-content link is the first element in the DOM on every page
  // that mounts this component (App.svelte renders <Header /> before the
  // route's <main>), satisfying "a skip-to-content link is the first
  // focusable element". Its target is #main-content, which every public view
  // (Home, About, NotFound, and the routes-not-yet-built placeholder in
  // App.svelte) sets on its <main>.
  import { onMount } from 'svelte';
  import Logo from './Logo.svelte';
  import { currentRoute } from './router';
  import { currentUser } from './stores';
  import { APP_NAME } from './branding';
  import { cycleTheme, currentTheme, type Theme } from './theme';

  interface NavLink {
    href: string;
    label: string;
    /** Route names that should mark this link aria-current="page". A link
     * covers more than one route name where they're the same section from a
     * user's point of view (e.g. a workshop detail page is still "on" the
     * Workshops nav item). */
    routes: readonly string[];
  }

  const NAV_LINKS: readonly NavLink[] = [
    { href: '/workshops', label: 'Workshops', routes: ['workshops', 'workshop-detail'] },
    { href: '/about', label: 'About', routes: ['about'] },
    { href: '/subscribe', label: 'Subscribe', routes: ['subscribe', 'subscribe-thanks'] },
  ];

  function isActive(routes: readonly string[]): boolean {
    return routes.includes($currentRoute.name);
  }

  const THEME_LABEL: Record<Theme, string> = { auto: 'Auto', light: 'Light', dark: 'Dark' };

  // theme.ts is DOM-injectable and unit-tested on its own (theme.test.ts);
  // this component just calls it against the real document/localStorage,
  // matching the pattern documented in theme.ts's header comment.
  let theme = $state<Theme>('auto');

  onMount(() => {
    theme = currentTheme(document.documentElement);
  });

  function toggleTheme() {
    theme = cycleTheme(document.documentElement, window.localStorage);
  }
</script>

<a class="skip-link" href="#main-content">Skip to content</a>

<header class="site-header">
  <div class="header-inner">
    <a class="brand-link" href="/" aria-label="{APP_NAME} — Home">
      <Logo variant="mark" size={34} />
      <span class="wordmark">{APP_NAME}</span>
    </a>

    <nav class="primary-nav" aria-label="Primary">
      <ul>
        {#each NAV_LINKS as link (link.href)}
          <li>
            <a href={link.href} aria-current={isActive(link.routes) ? 'page' : undefined}>
              {link.label}
            </a>
          </li>
        {/each}
        {#if $currentUser?.is_admin}
          <li>
            <a href="/admin" aria-current={$currentRoute.name === 'admin' ? 'page' : undefined}>
              Admin
            </a>
          </li>
        {/if}
      </ul>
    </nav>

    <button
      type="button"
      class="theme-toggle"
      onclick={toggleTheme}
      aria-label="Theme: {THEME_LABEL[theme]}. Activate to change."
    >
      {THEME_LABEL[theme]}
    </button>
  </div>
</header>

<style>
  /* Visually hidden until focused -- standard skip-link pattern. Positioned
   * fixed so it can't be clipped by an ancestor's overflow:hidden. */
  .skip-link {
    position: fixed;
    top: -100%;
    left: var(--space-3);
    z-index: 100;
    padding: var(--space-2) var(--space-3);
    background: var(--bg-panel);
    color: var(--text);
    border: var(--border-w) solid var(--border-strong);
    border-radius: var(--radius);
    font-family: var(--font);
    font-size: var(--fs-sm);
    text-decoration: none;
  }
  .skip-link:focus {
    top: var(--space-3);
  }

  .site-header {
    border-bottom: var(--border-w) solid var(--border);
    background: var(--bg-panel);
  }

  .header-inner {
    max-width: 1040px;
    margin: 0 auto;
    padding: var(--space-3) var(--space-4);
    display: flex;
    align-items: center;
    gap: var(--space-4);
  }

  .brand-link {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    color: var(--text);
    text-decoration: none;
    flex: none;
  }
  .brand-link:hover { color: var(--text); }

  .wordmark {
    font-family: var(--font-mono);
    font-weight: 600;
    font-size: var(--fs-md);
    letter-spacing: 0.01em;
    white-space: nowrap;
  }

  .primary-nav {
    flex: 1;
    min-width: 0;
  }

  .primary-nav ul {
    display: flex;
    align-items: center;
    gap: var(--space-4);
    margin: 0;
    padding: 0;
    list-style: none;
    flex-wrap: wrap;
  }

  .primary-nav a {
    display: inline-flex;
    align-items: center;
    color: var(--text-muted);
    text-decoration: none;
    font-size: var(--fs-base);
    padding: var(--space-1) 0;
    border-bottom: 2px solid transparent;
  }

  .primary-nav a:hover {
    color: var(--text);
  }

  /* Active route indication: both a visual underline AND aria-current="page"
   * (set in markup) -- the acceptance criteria call for both, and the
   * underline alone would not survive a screen reader. */
  .primary-nav a[aria-current='page'] {
    color: var(--accent);
    border-bottom-color: var(--accent);
    font-weight: 600;
  }

  .theme-toggle {
    flex: none;
    font-family: var(--font-mono);
    font-size: var(--fs-sm);
    padding: var(--space-1) var(--space-3);
    min-width: 64px;
    background: var(--bg-subtle);
    color: var(--text);
    border: var(--border-w) solid var(--border-strong);
    border-radius: var(--radius);
    cursor: pointer;
  }
  .theme-toggle:hover {
    background: var(--bg-header);
  }

  /* Mobile: wrap the nav onto its own row and give every link/button a
   * >=40px tap target, per the acceptance criteria (<=480px). */
  @media (max-width: 480px) {
    .header-inner {
      flex-wrap: wrap;
      row-gap: var(--space-2);
    }

    .primary-nav {
      order: 3;
      flex-basis: 100%;
    }

    .primary-nav ul {
      gap: var(--space-3);
    }

    .primary-nav a {
      min-height: 40px;
      align-items: center;
    }

    .theme-toggle {
      min-height: 40px;
    }
  }
</style>
