// When a click on a link should first ask about unsaved Outreach work (#0632).
// Pure, so it is unit-testable. Only same-site navigation is asked about here;
// leaving the site goes through the browser's own beforeunload prompt.
export interface LinkClick {
  button: number;
  metaKey: boolean;
  ctrlKey: boolean;
  shiftKey: boolean;
  altKey: boolean;
  defaultPrevented: boolean;
}

export function asksBeforeLeaving(
  e: LinkClick,
  a: { href: string; target: string; hasDownload: boolean },
  here: { href: string; origin: string },
): boolean {
  if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return false;
  if (a.hasDownload || (a.target !== '' && a.target !== '_self')) return false;
  let to: URL;
  let from: URL;
  try {
    to = new URL(a.href, here.href);
    from = new URL(here.href);
  } catch {
    return false;
  }
  if (to.origin !== here.origin) return false;
  if (to.pathname !== from.pathname || to.search !== from.search) return true;
  // Same document: only an admin route (#/…) leaves this screen; #main-content and the like do not.
  return to.hash !== from.hash && to.hash.startsWith('#/');
}
