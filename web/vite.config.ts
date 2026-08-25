import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

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
      '/api': apiTarget,
      '/auth': apiTarget,
      '/account': apiTarget,
      '/admin': apiTarget,
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
