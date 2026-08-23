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
// scheme allowlist (http/https/mailto, or no scheme at all for a relative
// path) before being emitted, rejecting javascript:/data:/vbscript: etc. --
// the same "dangerous URL" rule that test file's TestRenderMarkdownHTML_*
// dangerous-link cases document for goldmark's own renderer.
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

/** Whether a link destination is safe to emit: an allowed scheme, or no scheme at all (relative/root-relative). */
export function isSafeLinkHref(href: string): boolean {
  const trimmed = href.trim();
  if (trimmed === '') return false;
  if (HAS_SCHEME.test(trimmed)) {
    return SAFE_LINK_SCHEME.test(trimmed);
  }
  return true;
}

/** Apply inline markup (links, bold, italic) to a line that has already been HTML-escaped. */
function renderInline(escaped: string): string {
  let out = escaped;
  out = out.replace(/\[([^\]]+)\]\(([^)]+)\)/g, (whole, text: string, href: string) => {
    if (!isSafeLinkHref(href)) return text;
    return `<a href="${href}" rel="noopener noreferrer">${text}</a>`;
  });
  out = out.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
  out = out.replace(/__([^_]+)__/g, '<strong>$1</strong>');
  out = out.replace(/\*([^*]+)\*/g, '<em>$1</em>');
  out = out.replace(/_([^_]+)_/g, '<em>$1</em>');
  return out;
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
