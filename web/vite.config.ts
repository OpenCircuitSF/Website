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
    proxy: {
      '/api': apiTarget,
      '/auth': apiTarget,
      '/account': apiTarget,
      '/admin': apiTarget,
    },
  },
});
