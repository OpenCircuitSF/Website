// Link-destination safety check shared across the SPA (#0136).
//
// Renamed from markdown.ts to linkSafety.ts (#0157, per #0136's reviewer):
// this module used to also export `renderMarkdownPreview`, a dependency-free
// Markdown->HTML renderer written for #0052's admin workshop-body preview
// and rendered client-side via `{@html}`. Its first version shipped a live
// XSS hole (control characters smuggling `javascript:` past this exact
// scheme allowlist, past 22 passing tests -- fixed in b562800), and #0052's
// own reviewer named the right long-term fix: render server-side through
// goldmark, the same engine internal/mailing/campaign_markdown.go already
// uses for email_campaigns bodies, so preview and publish share one
// renderer instead of a browser-side one trying to independently re-derive
// what a browser's URL parser will do with untrusted markup.
//
// #0136 did exactly that: `POST /admin/workshops/{id}/preview`
// (internal/handlers/admin_workshop_preview.go) and the public
// `GET /api/workshops/{slug}` route (public_workshops.go) both render
// body_md through goldmark now, and WorkshopEditor.svelte /
// WorkshopDetail.svelte consume that server-rendered HTML instead of
// calling a client renderer. `renderMarkdownPreview` and every block/inline
// rendering helper it used (escapeHtml, applyEmphasis, renderInline,
// renderBlocks, the heading/list-item regexes) were deleted along with it.
//
// What's left is `isSafeLinkHref` alone, which survives because two other
// call sites depend on it and neither is a Markdown renderer -- so this
// module is no longer "markdown" anything, just the link-safety rule:
//
//   - workshopDetail.ts's `hasExternalSignup` (#0054) -- gates whether a
//     workshop's signup_url is safe to render as an `<a href>` on the public
//     detail page.
//   - internal/handlers/admin_workshops.go's `isSafeLinkHref` (#0152) -- the
//     Go twin of this exact function, validating signup_url AT THE API
//     BOUNDARY. #0157 owns keeping the two in parity (see
//     testdata/url_validators.json and urlValidatorFixture.test.ts);
//     #0138/#0152 own this function's URL-rule semantics. Neither is
//     touched by #0136 or #0157.

const SAFE_LINK_SCHEME = /^(https?|mailto):/i;
const HAS_SCHEME = /^[a-z][a-z0-9+.-]*:/i;
// C0 controls (0x00-0x1f) plus DEL (0x7f). A browser strips ASCII tab/CR/LF
// anywhere in a URL and strips leading C0 controls/whitespace before
// resolving it, so a destination containing one of these characters must be
// rejected outright rather than scheme-checked as it stands -- checking the
// raw string lets e.g. "java\x09script:..." or "\x00javascript:..." fail to
// look like they have a scheme (so they fall through to the "safe, relative"
// branch) and then resolve to "javascript:" once the browser normalizes them.
const HAS_CONTROL_CHAR = /[\x00-\x1f\x7f]/;

/**
 * Whether a link destination is safe to emit: an allowed scheme
 * (http/https/mailto), or a relative/root-relative destination with no
 * scheme at all.
 *
 * #0138 (found by #0052's pass-2 review): "no scheme" alone isn't a safe
 * test. `//evil.host/x` has no scheme (HAS_SCHEME requires a leading
 * letter, and `/` isn't one) but a browser resolves it as
 * PROTOCOL-RELATIVE -- same scheme as the current page, but a DIFFERENT
 * host -- which is exactly as off-site as an absolute URL to that host
 * would be. A browser's URL parser also treats a leading backslash the
 * same as a forward slash when resolving a relative reference against a
 * special (http/https) base, so `\evil.host/x` and `/\evil.host/x` both
 * normalize to that same `//evil.host/x` form. Normalizing backslashes to
 * slashes before checking for a leading "//" catches every spelling with
 * one rule.
 *
 * This is deliberately narrower than the "no scheme = relative, therefore
 * safe" shortcut used to be, but no narrower than it needs to be: a link is
 * a navigation the reader chooses to follow (unlike a workshop cover image,
 * which the page loads unasked -- see workshopAdmin.ts's isSafeCoverImage,
 * hardened same-origin-only by the same issue), so an absolute https:// URL
 * to ANY host stays allowed here -- #0054's hasExternalSignup
 * (workshopDetail.ts) reuses this exact function for a workshop's
 * signup_url, which is routinely an external ticketing site.
 */
export function isSafeLinkHref(href: string): boolean {
  if (HAS_CONTROL_CHAR.test(href)) return false;
  const trimmed = href.trim();
  if (trimmed === '') return false;
  if (HAS_SCHEME.test(trimmed)) {
    return SAFE_LINK_SCHEME.test(trimmed);
  }
  const normalized = trimmed.replace(/\\/g, '/');
  return !normalized.startsWith('//');
}
