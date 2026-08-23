import { describe, it, expect } from 'vitest';
import { isSafeLinkHref } from './markdown';

// #0136 deleted renderMarkdownPreview and every test that covered it (the
// Markdown->HTML rendering now happens server-side, goldmark, see
// internal/handlers/admin_workshop_preview_test.go and
// public_workshops_test.go) -- markdown.ts's header comment explains why
// isSafeLinkHref alone survives. These are the same isSafeLinkHref tests
// that existed before #0136, unchanged.

describe('isSafeLinkHref', () => {
  it('accepts http and https', () => {
    expect(isSafeLinkHref('http://example.com')).toBe(true);
    expect(isSafeLinkHref('https://example.com')).toBe(true);
  });

  it('accepts mailto', () => {
    expect(isSafeLinkHref('mailto:hello@opencircuitsf.com')).toBe(true);
  });

  it('accepts a scheme-less relative path', () => {
    expect(isSafeLinkHref('/workshops/soldering-101')).toBe(true);
  });

  it('rejects javascript:, data:, and vbscript: schemes', () => {
    expect(isSafeLinkHref('javascript:alert(1)')).toBe(false);
    expect(isSafeLinkHref('data:text/html,evil')).toBe(false);
    expect(isSafeLinkHref('vbscript:msgbox(1)')).toBe(false);
  });

  it('rejects an empty href', () => {
    expect(isSafeLinkHref('')).toBe(false);
    expect(isSafeLinkHref('   ')).toBe(false);
  });

  // #0052 review, finding 1 (bounced 2026-08-22): a browser strips ASCII
  // tab/CR/LF anywhere in a URL and strips leading C0 controls before
  // resolving it, so these five inputs previously slipped past the scheme
  // check (they don't *look* like they have a scheme) and resolved to
  // `javascript:` once normalized. Each was confirmed executable against the
  // WHATWG URL parser in the review. All five must now be rejected.
  describe('control-character scheme bypass (#0052 finding 1)', () => {
    it('rejects a tab spliced into the scheme name', () => {
      expect(isSafeLinkHref('java\tscript:alert(1)')).toBe(false);
    });

    it('rejects a carriage return spliced into the scheme name', () => {
      expect(isSafeLinkHref('java\rscript:alert(1)')).toBe(false);
    });

    it('rejects a tab spliced between the scheme name and colon', () => {
      expect(isSafeLinkHref('javascript\t:alert(1)')).toBe(false);
    });

    it('rejects a leading U+0001 control character', () => {
      expect(isSafeLinkHref(String.fromCharCode(1) + 'javascript:alert(1)')).toBe(false);
    });

    it('rejects a leading NUL byte', () => {
      expect(isSafeLinkHref(String.fromCharCode(0) + 'javascript:alert(1)')).toBe(false);
    });

    it('rejects a line-feed spliced into the scheme name', () => {
      expect(isSafeLinkHref('java\nscript:alert(1)')).toBe(false);
    });

    it('rejects a trailing DEL character', () => {
      expect(isSafeLinkHref('javascript:alert(1)\x7f')).toBe(false);
    });

    it('still accepts the legitimate cases with no control characters', () => {
      expect(isSafeLinkHref('https://example.com')).toBe(true);
      expect(isSafeLinkHref('/workshops/soldering-101')).toBe(true);
      expect(isSafeLinkHref('mailto:hello@opencircuitsf.com')).toBe(true);
      expect(isSafeLinkHref('/a_b_c?utm_source=newsletter')).toBe(true);
    });
  });

  // #0138, found by #0052's pass-2 review: "no scheme = relative, therefore
  // safe" let a PROTOCOL-RELATIVE destination through -- `//evil.host/x` has
  // no scheme by HAS_SCHEME's letter-first test, but a browser resolves it
  // against the current page's own scheme with a DIFFERENT host, same as an
  // absolute URL to that host would. A leading backslash normalizes to the
  // same thing for a special (http/https) base. #0054's hasExternalSignup
  // (workshopDetail.ts) reuses this exact function for signup_url, so it
  // inherits this same fix.
  describe('protocol-relative off-site destinations (#0138)', () => {
    it('rejects a bare protocol-relative href', () => {
      expect(isSafeLinkHref('//evil.host/x')).toBe(false);
    });

    it('rejects backslash-disguised protocol-relative hrefs', () => {
      expect(isSafeLinkHref('\\\\evil.host/x')).toBe(false);
      expect(isSafeLinkHref('/\\evil.host/x')).toBe(false);
      expect(isSafeLinkHref('\\/evil.host/x')).toBe(false);
    });

    it('still accepts the narrower, legitimate cases', () => {
      expect(isSafeLinkHref('/img/x.png')).toBe(true);
      expect(isSafeLinkHref('/workshops/soldering-101')).toBe(true);
      expect(isSafeLinkHref('https://eventbrite.com/e/soldering-101')).toBe(true);
      expect(isSafeLinkHref('mailto:hello@opencircuitsf.com')).toBe(true);
    });
  });
});
