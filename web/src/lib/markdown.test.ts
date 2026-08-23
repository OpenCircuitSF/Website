import { describe, it, expect } from 'vitest';
import { renderMarkdownPreview, isSafeLinkHref } from './markdown';

describe('renderMarkdownPreview', () => {
  it('returns empty string for empty/whitespace input', () => {
    expect(renderMarkdownPreview('')).toBe('');
    expect(renderMarkdownPreview('   \n  ')).toBe('');
  });

  it('escapes raw HTML instead of executing it', () => {
    const out = renderMarkdownPreview('<script>alert(1)</script>');
    expect(out).not.toContain('<script>');
    expect(out).toContain('&lt;script&gt;');
  });

  it('renders headings h1 through h6', () => {
    expect(renderMarkdownPreview('# Title')).toBe('<h1>Title</h1>');
    expect(renderMarkdownPreview('### Sub')).toBe('<h3>Sub</h3>');
    expect(renderMarkdownPreview('###### Deep')).toBe('<h6>Deep</h6>');
  });

  it('renders a plain paragraph', () => {
    expect(renderMarkdownPreview('Hello world.')).toBe('<p>Hello world.</p>');
  });

  it('joins consecutive non-blank lines into one paragraph', () => {
    expect(renderMarkdownPreview('Line one\nLine two')).toBe('<p>Line one Line two</p>');
  });

  it('separates paragraphs on a blank line', () => {
    expect(renderMarkdownPreview('First\n\nSecond')).toBe('<p>First</p>\n<p>Second</p>');
  });

  it('renders bold with ** and __', () => {
    expect(renderMarkdownPreview('**bold**')).toBe('<p><strong>bold</strong></p>');
    expect(renderMarkdownPreview('__bold__')).toBe('<p><strong>bold</strong></p>');
  });

  it('renders italic with * and _', () => {
    expect(renderMarkdownPreview('*italic*')).toBe('<p><em>italic</em></p>');
    expect(renderMarkdownPreview('_italic_')).toBe('<p><em>italic</em></p>');
  });

  it('renders an unordered list', () => {
    expect(renderMarkdownPreview('- one\n- two')).toBe('<ul><li>one</li><li>two</li></ul>');
    expect(renderMarkdownPreview('* one\n* two')).toBe('<ul><li>one</li><li>two</li></ul>');
  });

  it('renders an ordered list', () => {
    expect(renderMarkdownPreview('1. one\n2. two')).toBe('<ol><li>one</li><li>two</li></ol>');
  });

  it('renders a safe link', () => {
    expect(renderMarkdownPreview('[site](https://example.com)')).toBe(
      '<p><a href="https://example.com" rel="noopener noreferrer">site</a></p>',
    );
  });

  it('renders a relative link (no scheme) as safe', () => {
    expect(renderMarkdownPreview('[home](/workshops)')).toBe(
      '<p><a href="/workshops" rel="noopener noreferrer">home</a></p>',
    );
  });

  it('drops markup for a dangerous link scheme, keeping only the text', () => {
    expect(renderMarkdownPreview('[click](javascript:evil)')).toBe('<p>click</p>');
  });

  it('drops markup for a data: link scheme', () => {
    expect(renderMarkdownPreview('[x](data:text/html,evil)')).toBe('<p>x</p>');
  });

  // #0138: a protocol-relative destination has no scheme by the naive
  // letter-first test, but resolves off-site exactly like an absolute URL
  // would -- it must be dropped the same way javascript:/data: are, not
  // treated as a safe relative path.
  it('drops markup for a protocol-relative link destination', () => {
    expect(renderMarkdownPreview('[x](//evil.host/y)')).toBe('<p>x</p>');
  });

  it('mixes block and inline elements in one document', () => {
    const out = renderMarkdownPreview('# Soldering 101\n\nLearn to **solder** with us.\n\n- Bring safety glasses\n- Bring a multimeter');
    expect(out).toBe(
      '<h1>Soldering 101</h1>\n<p>Learn to <strong>solder</strong> with us.</p>\n<ul><li>Bring safety glasses</li><li>Bring a multimeter</li></ul>',
    );
  });
});

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

describe('renderMarkdownPreview control-character link bypass (#0052 finding 1)', () => {
  it('drops the link (renders inert text) for every confirmed bypass input', () => {
    const cases = [
      '[x](java\tscript:alert(1))',
      '[x](java\rscript:alert(1))',
      '[x](javascript\t:alert(1))',
      '[x](' + String.fromCharCode(1) + 'javascript:alert(1))',
      '[x](' + String.fromCharCode(0) + 'javascript:alert(1))',
    ];
    for (const md of cases) {
      const out = renderMarkdownPreview(md);
      expect(out).not.toContain('<a ');
      expect(out).not.toContain('javascript:');
    }
  });
});

describe('renderMarkdownPreview link/emphasis ordering (#0052 finding 1, correctness bug)', () => {
  it('does not let an underscore in a URL trigger italic markup inside href', () => {
    const out = renderMarkdownPreview('[x](/a_b_c)');
    expect(out).toBe('<p><a href="/a_b_c" rel="noopener noreferrer">x</a></p>');
    expect(out).not.toContain('<em>');
  });

  it('does not let asterisks in a URL trigger bold markup inside href', () => {
    const out = renderMarkdownPreview('[x](/a**b**c)');
    expect(out).toBe('<p><a href="/a**b**c" rel="noopener noreferrer">x</a></p>');
    expect(out).not.toContain('<strong>');
  });

  it('still applies emphasis to text around a link on the same line', () => {
    const out = renderMarkdownPreview('Read **this** and [click here](/workshops/x) for _details_.');
    expect(out).toBe(
      '<p>Read <strong>this</strong> and <a href="/workshops/x" rel="noopener noreferrer">click here</a> for <em>details</em>.</p>',
    );
  });

  it('handles two links on the same line without cross-contaminating hrefs', () => {
    const out = renderMarkdownPreview('[a](/x_y) and [b](/p_q)');
    expect(out).toBe(
      '<p><a href="/x_y" rel="noopener noreferrer">a</a> and <a href="/p_q" rel="noopener noreferrer">b</a></p>',
    );
  });
});
