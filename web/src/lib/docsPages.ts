// #0735: the website Docs section's page list -- the docs nav's order and
// labels, and each page's h1 for document.title. The page text itself lives
// in web/src/content/docs/*.json; docsPages.test.ts proves this list and
// those files name exactly the same pages with the same h1s. Kept tiny
// because pageTitle.ts imports it into the entry chunk.

export interface DocsPage {
  path: string;
  label: string;
  h1: string;
}

export const DOCS_PAGES: readonly DocsPage[] = [
  { path: '/docs', label: 'Overview', h1: 'Docs' },
  { path: '/docs/using-the-site', label: 'Using the site', h1: 'Using the site' },
  { path: '/docs/newsletter', label: 'Newsletter', h1: 'The newsletter' },
  { path: '/docs/api', label: 'Public API', h1: 'Public API' },
  { path: '/docs/mcp', label: 'MCP', h1: 'Use Open Circuit SF with AI agents' },
  { path: '/docs/feeds', label: 'Feeds', h1: 'Feeds' },
];

export function docsPageByPath(path: string): DocsPage | undefined {
  return DOCS_PAGES.find((p) => p.path === path);
}

/** Maps an import.meta.glob key for a docs content file to its route:
 * '../content/docs/index.json' -> '/docs', '.../newsletter.json' ->
 * '/docs/newsletter'. */
export function docsPathForFile(globKey: string): string {
  const stem = (globKey.split('/').pop() ?? '').replace(/\.json$/, '');
  return stem === 'index' ? '/docs' : `/docs/${stem}`;
}
