// #0220: minimal ambient declarations for the three "node:*" built-ins
// citationTargetGuard.test.ts needs (real filesystem existence checks, for
// which import.meta.glob has no equivalent -- it only loads the STATIC set
// of files matching a glob known at build time, not an arbitrary
// repo-relative path string discovered at runtime from a comment).
//
// #0181's own review pass (see citationGuard.test.ts's header) removed an
// earlier attempt's @types/node dependency and a "node" entry in
// tsconfig.json's `types` array in favor of import.meta.glob, specifically
// to avoid the dependency -- and `npm ls --all` used to confirm @types/node
// was not installed here at all, only present, unmet, as an OPTIONAL peer
// of vite/vitest. #0485 overturned that precedent for a different file:
// web/vite.config.ts genuinely runs in Node and has no import.meta.glob-shaped
// alternative, so #0485 added @types/node as a devDependency and a narrow
// web/tsconfig.node.json (types: ["node"]) scoped to that one file. `npm ls
// @types/node` now reports it as a direct devDependency, with vite's and
// vitest's formerly-unmet optional peers deduped against it.
//
// That does not make this shim redundant. web/tsconfig.json -- the config
// that actually checks this file, since citationTargetGuard.test.ts lives
// under src/**/*.ts -- still lists only `types: ["svelte", "vite/client"]`,
// byte-unchanged by #0485, so none of @types/node's ambients are visible
// here. Declaring just the handful of functions this one test file actually
// calls remains the way its three "node:*" imports resolve, and the shipped
// `web/dist/` bundle still never sees any of this (these three modules are
// only ever imported from _test.ts files, which `vite build` does not
// touch).
declare module 'node:fs' {
  export interface Dirent {
    name: string;
    isDirectory(): boolean;
  }
  export function readFileSync(path: string, encoding: 'utf-8'): string;
  export function readdirSync(path: string, options: { withFileTypes: true }): Dirent[];
}

declare module 'node:path' {
  export function join(...parts: string[]): string;
  export function resolve(...parts: string[]): string;
  export function relative(from: string, to: string): string;
  export function dirname(p: string): string;
  export const sep: string;
  const defaultExport: {
    join: typeof join;
    resolve: typeof resolve;
    relative: typeof relative;
    dirname: typeof dirname;
    sep: typeof sep;
  };
  export default defaultExport;
}

declare module 'node:url' {
  export function fileURLToPath(url: string | URL): string;
}
