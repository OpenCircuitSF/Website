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
});
