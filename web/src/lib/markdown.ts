// Minimal, dependency-free Markdown -> HTML renderer for the workshop body
// preview (#0052 acceptance criterion: "Markdown editing and preview for the
// body"). NOT goldmark parity: internal/mailing/campaign_render.go and
// campaign_markdown.go (goldmark + a sanitizing HTML style pass) are the
// send-time renderer for email_campaigns.body_md, and #0051's workshop admin
// API has no equivalent /preview route (adding one is a backend change
// outside this issue's admin-UI-only scope). This module exists purely to
// give the admin author a fast, client-side approximation while typing: the
// common subset an event write-up actually needs -- headings, paragraphs,
// bold/italic, links, and lists.
//
// Security: the ENTIRE input is HTML-escaped first, and only escaped text is
// ever passed through the markup regexes below -- a literal "<script>" typed
// into the body renders as inert text, never as markup, mirroring goldmark's
// own raw-HTML-omitted default (see internal/mailing/campaign_markdown_test.go's
// "raw-HTML-omitted marker" test). Link destinations are checked against a
// scheme allowlist (http/https/mailto, or a relative/root-relative
// destination with no scheme and no leading "//" -- see isSafeLinkHref's
// doc comment, #0138) before being emitted, rejecting
// javascript:/data:/vbscript: and protocol-relative off-site URLs alike.
//
// #0052 review (bounced 2026-08-22, finding 1): a browser normalizes a URL
// before resolving it -- stripping ASCII tab/CR/LF anywhere in the string and
// stripping leading C0 controls -- so a destination containing one of those
// characters could sail past a naive scheme check (it doesn't *look* like it
// has a scheme, so it fell through to the "no scheme = relative, therefore
// safe" branch) and still resolve to `javascript:` once the browser strips
// the control character back out. Confirmed executable against the WHATWG
// URL parser for all of `java<TAB>script:...`, `java<CR>script:...`,
// `javascript<TAB>:...`, `<U+0001>javascript:...`, and `<NUL>javascript:...`.
// `isSafeLinkHref` now rejects any destination containing a character below
// U+0020 (or U+007F/DEL) outright, before the scheme test ever runs, which
// closes the gap for all five without a per-string denylist. See
// `markdown.test.ts`'s "control-character scheme bypass" tests for the exact
// inputs and the corresponding legitimate cases that still pass.
//
// This module's output IS safe to render with `{@html}` under that model:
// escaping is unconditional and comes first, and link destinations are now
// validated against their post-normalization form. It is not goldmark parity
// and never claims to be.
//
// Also fixed in the same pass: link substitution now happens in a single
// left-to-right scan that never re-visits already-built `<a href="...">`
// markup, so the later bold/italic passes run only against the literal text
// between links, not against the link HTML itself. Previously the emphasis
// regexes ran on the whole line *after* links were substituted in, so a `_`
// or `*` inside an ordinary URL (a query string like `?utm_source=x`, or a
// snake_case path like `/a_b_c`) was rewritten in place, corrupting the
// `href` attribute (`href="/a<em>b</em>c"`) even though the link itself was
// perfectly safe.
//
// The return value is a plain HTML string. The caller decides how to insert
// it (WorkshopEditor.svelte renders it into the preview pane with {@html}).

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

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

const LINK_RE = /\[([^\]]+)\]\(([^)]+)\)/g;

function applyEmphasis(s: string): string {
  let out = s;
  out = out.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
  out = out.replace(/__([^_]+)__/g, '<strong>$1</strong>');
  out = out.replace(/\*([^*]+)\*/g, '<em>$1</em>');
  out = out.replace(/_([^_]+)_/g, '<em>$1</em>');
  return out;
}

/** Apply inline markup (links, bold, italic) to a line that has already been HTML-escaped. */
function renderInline(escaped: string): string {
  const parts: string[] = [];
  let lastIndex = 0;
  LINK_RE.lastIndex = 0;
  let match: RegExpExecArray | null;
  while ((match = LINK_RE.exec(escaped)) !== null) {
    const [whole, text, href] = match;
    parts.push(applyEmphasis(escaped.slice(lastIndex, match.index)));
    parts.push(
      isSafeLinkHref(href) ? `<a href="${href}" rel="noopener noreferrer">${text}</a>` : text,
    );
    lastIndex = match.index + whole.length;
  }
  parts.push(applyEmphasis(escaped.slice(lastIndex)));
  return parts.join('');
}

const HEADING_RE = /^(#{1,6})\s+(.*)$/;
const UL_ITEM_RE = /^[-*]\s+/;
const OL_ITEM_RE = /^\d+\.\s+/;

function renderBlocks(lines: string[]): string {
  const out: string[] = [];
  let i = 0;

  while (i < lines.length) {
    const trimmed = lines[i].trim();

    if (trimmed === '') {
      i++;
      continue;
    }

    const heading = HEADING_RE.exec(trimmed);
    if (heading) {
      const level = heading[1].length;
      out.push(`<h${level}>${renderInline(heading[2])}</h${level}>`);
      i++;
      continue;
    }

    if (UL_ITEM_RE.test(trimmed)) {
      const items: string[] = [];
      while (i < lines.length && UL_ITEM_RE.test(lines[i].trim())) {
        items.push(`<li>${renderInline(lines[i].trim().replace(UL_ITEM_RE, ''))}</li>`);
        i++;
      }
      out.push(`<ul>${items.join('')}</ul>`);
      continue;
    }

    if (OL_ITEM_RE.test(trimmed)) {
      const items: string[] = [];
      while (i < lines.length && OL_ITEM_RE.test(lines[i].trim())) {
        items.push(`<li>${renderInline(lines[i].trim().replace(OL_ITEM_RE, ''))}</li>`);
        i++;
      }
      out.push(`<ol>${items.join('')}</ol>`);
      continue;
    }

    const paraLines: string[] = [];
    while (
      i < lines.length &&
      lines[i].trim() !== '' &&
      !HEADING_RE.test(lines[i].trim()) &&
      !UL_ITEM_RE.test(lines[i].trim()) &&
      !OL_ITEM_RE.test(lines[i].trim())
    ) {
      paraLines.push(lines[i].trim());
      i++;
    }
    out.push(`<p>${renderInline(paraLines.join(' '))}</p>`);
  }

  return out.join('\n');
}

/**
 * Render `source` (a workshop's body_md, or the create/edit form's live
 * buffer) as an HTML preview fragment. Empty/whitespace-only input renders
 * to the empty string, letting the caller show its own "nothing to preview
 * yet" placeholder rather than an empty `<p></p>`.
 */
export function renderMarkdownPreview(source: string): string {
  if (source.trim() === '') return '';
  const escaped = escapeHtml(source);
  return renderBlocks(escaped.split('\n'));
}
