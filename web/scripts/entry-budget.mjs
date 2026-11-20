// Entry-chunk size guard (#0736, PRD §7.6). Builds the SPA into a temp dir
// (never web/dist, which is tracked), reads Vite's manifest to find the
// index.html entry and its static-import closure, and fails if the initial
// JS or CSS exceeds the budget.
//
// Budgets are decimal KB, as #0717 read PRD §7.6's "60 KB" / "15 KB". Node's
// zlib.gzipSync runs about 18 B under `gzip -c`, which stores the file name.
// There is deliberately no env or CLI override: a guard with a bypass knob is
// a bypass.
import { build } from 'vite';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { gzipSync } from 'node:zlib';

const JS_BUDGET_BYTES = 60_000;
const CSS_BUDGET_BYTES = 15_000;

const webRoot = join(dirname(fileURLToPath(import.meta.url)), '..');
const outDir = mkdtempSync(join(tmpdir(), 'oc-entry-budget-'));

function fail(msg) {
  console.error(`entry-budget: ${msg}`);
  process.exitCode = 2;
}

try {
  await build({
    root: webRoot,
    logLevel: 'warn',
    build: { outDir, emptyOutDir: true, manifest: true },
  });
  const manifest = JSON.parse(readFileSync(join(outDir, '.vite', 'manifest.json'), 'utf8'));
  const entry = manifest['index.html'];
  if (!entry || !entry.isEntry) {
    fail('no isEntry chunk keyed index.html in the manifest');
  } else {
    const js = new Set();
    const css = new Set();
    const seen = new Set();
    const walk = (key) => {
      if (seen.has(key)) return;
      seen.add(key);
      const chunk = manifest[key];
      if (!chunk) return;
      if (chunk.file && chunk.file.endsWith('.js')) js.add(chunk.file);
      for (const c of chunk.css ?? []) css.add(c);
      for (const imp of chunk.imports ?? []) walk(imp); // static only; dynamicImports skipped
    };
    walk('index.html');
    if (js.size === 0) {
      fail('entry static-import closure contains no JS files');
    } else {
      const total = (files) => {
        let sum = 0;
        for (const f of files) {
          const n = gzipSync(readFileSync(join(outDir, f))).length;
          console.log(`  ${f}: ${n} B gzip`);
          sum += n;
        }
        return sum;
      };
      const jsBytes = total(js);
      const cssBytes = total(css);
      console.log(`initial JS (gzip): ${jsBytes} B / ${JS_BUDGET_BYTES} B budget — headroom ${JS_BUDGET_BYTES - jsBytes} B`);
      console.log(`initial CSS (gzip): ${cssBytes} B / ${CSS_BUDGET_BYTES} B budget — headroom ${CSS_BUDGET_BYTES - cssBytes} B`);
      if (jsBytes > JS_BUDGET_BYTES || cssBytes > CSS_BUDGET_BYTES) {
        console.error('entry-budget: OVER BUDGET');
        process.exitCode = 1;
      }
    }
  }
} catch (err) {
  fail(String(err && err.stack ? err.stack : err));
} finally {
  rmSync(outDir, { recursive: true, force: true });
}
