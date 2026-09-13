// #0519: Svelte 5's mount() APPENDS to its target rather than replacing it
// (see the Svelte docs for `mount`). The server now injects fallback
// content into #app for every public route (internal/seo/fallback.go) --
// an <h1>, the route's main text, and plain nav links -- so a client that
// never runs JavaScript still sees a heading and text. That fallback and
// the real Svelte view must never coexist in the DOM: #0238 moves focus to
// the first `h1[tabindex="-1"]`, and #0517 styles that same selector, so a
// second, non-focusable server <h1> left in place would confuse both.
//
// prepareMountTarget empties #app synchronously, BEFORE main.ts calls
// mount() -- so the server's nodes are gone before Svelte creates its
// first one, and the two node sets never share the DOM or the
// accessibility tree.
export function prepareMountTarget(doc: Document = document): HTMLElement {
  const el = doc.getElementById('app');
  if (!el) {
    throw new Error('prepareMountTarget: #app not found in the document');
  }
  el.replaceChildren();
  return el;
}
