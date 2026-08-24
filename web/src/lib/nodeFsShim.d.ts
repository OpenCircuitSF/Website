// #0220: minimal ambient declarations for the three "node:*" built-ins
// citationTargetGuard.test.ts needs (real filesystem existence checks, for
// which import.meta.glob has no equivalent -- it only loads the STATIC set
// of files matching a glob known at build time, not an arbitrary
// repo-relative path string discovered at runtime from a comment).
//
// A real @types/node package would cover this more completely, but this
// project deliberately does not depend on it: #0181's own review pass
// (see citationGuard.test.ts's header) removed an earlier attempt's
// @types/node dependency and a "node" entry in tsconfig.json's `types`
// array in favor of import.meta.glob, specifically to avoid the
// dependency. `npm ls --all` confirms @types/node is not installed here --
// only present, unmet, as an OPTIONAL peer of vite/vitest. Declaring just
// the handful of functions this one test file actually calls keeps that
// precedent intact: zero new devDependencies, and the shipped
// `web/dist/` bundle never sees any of this (these three modules are only
// ever imported from _test.ts files, which `vite build` does not touch).
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
