import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import type { IncomingMessage } from 'node:http';

// The SPA is built to `dist/` and embedded into the Go binary via //go:embed at
// compile time. In development the dev server proxies the API, auth, account,
// and admin namespaces to the Go service so the browser sees everything as
// same-origin (no CORS). There is no /u redirect namespace in this project —
// that belongs to the separate ShortLinks deploy at go.opencircuitsf.com, not
// this site.
//
// The API port comes from $PORT (#0213). scripts/dev.sh exports PORT (default
// 8080) before running `npm run dev`, and this file is evaluated by Node at
// config-evaluation time — before any request arrives — so process.env is the
// only channel a shell variable can reach it through; there is no per-request
// hook to read it later. Running `npm run dev` directly (outside dev.sh)
// leaves $PORT unset and falls back to 8080, matching dev.sh's own default.
const apiPort = process.env.PORT || '8080';
const apiTarget = `http://localhost:${apiPort}`;

// #0408: `/account` and `/admin` are proxied to Go above so the SPA's own
// fetch()/XHR calls to those prefixes reach the API, but both prefixes are
// ALSO SPA routes in router.ts's STATIC_ROUTES ('/account', '/admin') — keep
// this list and that one in sync; a future SPA route colliding with a
// proxied prefix here reproduces the same defect. A plain proxy entry cannot
// tell a browser's top-level navigation (a direct load or reload of the URL)
// from the SPA's own same-path API calls, so both were forwarded to Go,
// which returned its *embedded* dist/index.html — the tracked placeholder,
// with no bundle — instead of the dev server's real shell. That is why a
// reload of /admin rendered a blank page: 0-byte body, no JS error.
//
// Browsers set `Sec-Fetch-Mode: navigate` on top-level document navigations;
// the SPA's own fetch() calls to these same paths ask for JSON and do not
// carry that header value. Vite's proxy `bypass` option is checked before a
// request is forwarded: returning a path from it makes Vite serve that file
// itself instead of proxying, so a navigation gets the dev server's own
// index.html (which loads /@vite/client) while every other request still
// proxies straight through to Go.
//
// Only /admin and /account THEMSELVES are SPA routes — the admin console's
// tabs are components rendered inside that one route, not URL routes of
// their own. Everything deeper under either prefix is a Go API path,
// including /admin/subscribers/export, which web/src/views/Admin.svelte
// reaches with a real `<a href download>` browser navigation rather than
// fetch() (see subscribersExportHref in web/src/lib/admin.ts). A bypass
// keyed on the Sec-Fetch-Mode header alone cannot tell that request apart
// from a genuine top-level navigation to /admin itself, and would serve it
// Vite's index.html instead of the CSV — and would do the same to any
// method-agnostic POST navigation under either prefix, not just GET. So the
// path is checked first, against this explicit allowlist, and only GET/HEAD
// requests to exactly these two paths are eligible for the header check at
// all. Adding a new SPA route under either prefix means adding it here too.
const SPA_BYPASS_PATHS = new Set(['/admin', '/account']);

function spaNavigationBypass(req: IncomingMessage): string | undefined {
  if (req.method !== 'GET' && req.method !== 'HEAD') return undefined;
  const path = (req.url ?? '').split('?')[0];
  if (!SPA_BYPASS_PATHS.has(path)) return undefined;
  const mode = req.headers['sec-fetch-mode'];
  const accept = String(req.headers.accept ?? '');
  if (mode === 'navigate' || (mode === undefined && accept.includes('text/html'))) {
    return '/index.html';
  }
  return undefined;
}

export default defineConfig({
  plugins: [svelte()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  server: {
    // #0214: previously unset, so 5173 was only Vite's own default rather
    // than something this file stated — a bare `npm run dev` with 5173 taken
    // would slide silently to 5174 while README.md, docs/dev.md, CLAUDE.md,
    // and scripts/dev.sh's messages all still say 5173. Stated explicitly so
    // the port matches what every doc claims, and strictPort:true makes a
    // collision fail loudly (matching this project's general preference for
    // refusing over silently doing something else — see scripts/dev.sh's own
    // free_port) instead of masking it with a different port nobody expects.
    // Unreachable through scripts/dev.sh itself: #0117's preflight
    // (free_port 5173) already refuses before Vite ever starts, so Vite here
    // only ever sees a free port in that path. This only changes behavior on
    // the bare `npm run dev` path, which dev.sh's guard tests do not cover.
    port: 5173,
    strictPort: true,
    proxy: {
      // Plain string form: neither collides with an SPA route ('/login',
      // '/register/verify', '/recover/verify' are the auth-adjacent SPA
      // paths, and none is under /auth), so every request proxies straight
      // through with no navigation/XHR distinction needed.
      '/api': apiTarget,
      '/auth': apiTarget,
      // '/account' and '/admin' collide with SPA routes — see
      // spaNavigationBypass above.
      '/account': { target: apiTarget, bypass: spaNavigationBypass },
      '/admin': { target: apiTarget, bypass: spaNavigationBypass },
    },
  },
  // #0094: under Vitest, Vite's default module resolution picks Svelte's
  // SERVER build (the `node`/default export condition) even though tests
  // run in jsdom, which is what made an early attempt at this file's own
  // component-mount test fail with "lifecycle_function_unavailable: mount()
  // is not available on the server" — svelte-testing-library's `render()`
  // calls Svelte's client `mount()`, but without this the resolver was
  // handing it the server module instead. Scoped to `process.env.VITEST`
  // (set by Vitest itself) so `vite build`/`vite dev` are untouched.
  resolve: process.env.VITEST ? { conditions: ['browser'] } : undefined,
});
