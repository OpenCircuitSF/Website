import { afterEach, describe, expect, it, vi } from 'vitest';
import { handChannelLabel, handCopies, handLinkLabel, isVideoChannel, textOnly, withLink, writeClipboard } from './outreachHandCopy';

const U = 'https://go.example/u/tt';

describe('textOnly', () => {
  it.each([
    ['a TikTok draft', `Programming LEDs: Monday, Oct 19 at Sequoia Fabrica.\n\nWire up a strip.\n\nSign up: ${U}\n\nOpen Circuit SF: https://www.opencircuitsf.com\n\n#SanFrancisco #Electronics`, 'Programming LEDs: Monday, Oct 19 at Sequoia Fabrica.\n\nWire up a strip.\n\n#SanFrancisco #Electronics'],
    ['a post with the token still in it', 'Hello\n\nSign up: {link}\n\nOpen Circuit SF: https://www.opencircuitsf.com', 'Hello'],
    ['a Mastodon line', `Join us.\n\nSpots are limited, so you can sign up here: ${U}\n\n#sf`, 'Join us.\n\n#sf'],
    ['an Instagram draft, unchanged', 'A: b.\n\nSign up at the link in our bio.\n\n#sf', 'A: b.\n\nSign up at the link in our bio.\n\n#sf'],
    ['mid-sentence', `Sign up at ${U} today. Bring a friend.`, 'Bring a friend.'],
    ['mid-sentence token', 'Sign up at {link} today. Bring a friend.', 'Bring a friend.'],
    ['ending a sentence', `Join us Monday. Details at ${U}.`, 'Join us Monday.'],
    ['before an exclamation mark', `See ${U}! Doors at six.`, 'Doors at six.'],
    ['between commas', `Join us, sign up at ${U}, and bring a laptop.\n\nSee you there.`, 'See you there.'],
    ['in brackets', `Join us (details: ${U}) on Monday.`, 'Join us on Monday.'],
    ['in brackets at the end', `Join us on Monday (${U}).`, 'Join us on Monday.'],
    ['lead-in on the line above', `Hello\n\nSign up:\n${U}`, 'Hello'],
    ['lead-in a paragraph above', `Hello\n\nSign up:\n\n${U}\n\n#sf`, 'Hello\n\n#sf'],
    ['lead-in with no colon', `Hello\nSign up here\n${U}`, 'Hello'],
    ['lead-in after a sentence', `Great news. Sign up:\n${U}`, 'Great news.'],
    ['lead-in as its own sentence', `Spots are limited. Sign up here. ${U}`, 'Spots are limited.'],
    ["hashtags on the link's line", `Hi\n\nSign up: ${U} #sf #maker`, 'Hi\n\n#sf #maker'],
    ['an abbreviation in a kept sentence', `Led by Dr. Lee. Info at ${U}.`, 'Led by Dr. Lee.'],
    ['an abbreviation in the removed sentence', `Led by Dr. Lee, info at ${U}.`, ''],
    ['e.g.', `Bring tools, e.g. pliers. Info at ${U}.`, 'Bring tools, e.g. pliers.'],
    ['only a link', U, ''],
    ['the short link with no scheme', 'Hi.\n\nSign up: go.example/u/tt', 'Hi.'],
    ['a www address', 'Hi.\n\nMore at www.example.com/x.', 'Hi.'],
    ['other URLs, any case', 'Hi.\n\nA: http://a.example/1 and HTTPS://b.example/2', 'Hi.'],
    ['colons with no link are left alone', 'When: Monday\nBring:\nA laptop', 'When: Monday\nBring:\nA laptop'],
    ['a list before the link', `Bring:\n- a laptop\n- pliers\n\nSign up: ${U}`, 'Bring:\n- a laptop\n- pliers'],
    ['Windows line ends', `Hi\r\n\r\nSign up: ${U}\r\n\r\n#sf`, 'Hi\n\n#sf'],
    ['a pointing emoji above', `Hi\n\nSign up 👇\n${U}`, 'Hi'],
    ['a pointing emoji beside', `Hi\n\n👉 ${U}`, 'Hi'],
    ['a time keeps its colon', `Doors at 6:30. Sign up: ${U}`, 'Doors at 6:30.'],
    ['a price keeps its point', `It costs $5.50 to attend. See ${U}`, 'It costs $5.50 to attend.'],
    ['a question before a link', `What's next? ${U}`, "What's next?"],
    ['a sentence ending in "shift register" is not a lead-in', `Title.\n\nLearn to drive LEDs with a shift register.\n\nSign up: ${U}`, 'Title.\n\nLearn to drive LEDs with a shift register.'],
    ['a sentence ending in "details" is not a lead-in', `We sweat all the details.\n\nSign up: ${U}`, 'We sweat all the details.'],
    ['a sentence ending in "learn more" is not a lead-in', `Meet the crew and learn more.\n\nSign up: ${U}\n\n#sf`, 'Meet the crew and learn more.\n\n#sf'],
    ['a sentence ending in "links" beside the link stays', `Build a chain of radio links. Info at ${U}.`, 'Build a chain of radio links.'],
    ['the same with no full stop', `A class on the shift register\n\nSign up: ${U}`, 'A class on the shift register'],
  ])('%s', (_name, input, expected) => {
    expect(textOnly(input, U)).toBe(expected);
  });
});

const withLinkRows: [string, string, string][] = [
  ['Instagram swaps the bio sentence', 'A.\n\nSign up at the link in our bio.\n\n#sf', `A.\n\nSign up: ${U}\n\n#sf`],
  ['a body that has it stays as it is', `A\n\nSign up: ${U}\n\n#sf`, `A\n\nSign up: ${U}\n\n#sf`],
  ['fills the token', 'A\n\nSign up: {link}', `A\n\nSign up: ${U}`],
  ['customized, before the hashtags', 'Custom words\n\n#sf', `Custom words\n\nSign up: ${U}\n\n#sf`],
  ['customized, no hashtags', 'Custom words', `Custom words\n\nSign up: ${U}`],
  ['the link is already in a sentence', `Mine at ${U} ok`, `Mine at ${U} ok`],
  ['only hashtags', '#sf', `#sf\n\nSign up: ${U}`],
];

describe('withLink', () => {
  it.each(withLinkRows)('%s', (_name, input, expected) => {
    expect(withLink(input, U)).toBe(expected);
  });
  it('is null with no link, rather than text with a token', () => {
    expect(withLink('A\n\nSign up: {link}', null)).toBeNull();
    expect(withLink('A\n\nSign up: {link}', '')).toBeNull();
  });
  it('never returns a raw token', () => {
    for (const [, input] of withLinkRows) expect(withLink(input, U)).not.toContain('{link}');
  });
  it('three channels give three different texts', () => {
    const body = 'A\n\nSign up: {link}';
    const urls = ['https://go.example/u/ig', 'https://go.example/u/tt', 'https://go.example/u/yt'];
    const texts = urls.map((short_url) => handCopies({ body, short_url }).withLink);
    expect(new Set(texts).size).toBe(3);
    urls.forEach((u, i) => expect(texts[i]).toContain(u));
  });
  it('a URL with a dollar sign goes in unchanged', () => {
    const url = 'https://go.example/$&$1';
    expect(withLink('A\n\nSign up: {link}', url)).toBe(`A\n\nSign up: ${url}`);
    expect(withLink('A.\n\nSign up at the link in our bio.', url)).toBe(`A.\n\nSign up: ${url}`);
  });
});

describe('labels and groups', () => {
  it('the Video group is Instagram, TikTok and YouTube', () => {
    for (const name of ['instagram', 'tiktok', 'youtube']) expect(isVideoChannel({ name })).toBe(true);
    for (const name of ['bluesky', 'mastodon', 'reddit']) expect(isVideoChannel({ name })).toBe(false);
  });
  it('Instagram reads as Reels, others keep their label', () => {
    expect(handChannelLabel({ name: 'instagram', label: 'Instagram' })).toBe('Instagram Reels');
    expect(handChannelLabel({ name: 'tiktok', label: 'TikTok' })).toBe('TikTok');
  });
  it('the link label says bio only for Instagram', () => {
    expect(handLinkLabel({ name: 'instagram', automatic: false })).toBe('Put this in your bio');
    expect(handLinkLabel({ name: 'tiktok', automatic: false })).toBe('ShortLink');
    expect(handLinkLabel({ name: 'bluesky', automatic: true })).toBe('Link to paste');
  });
});

describe('writeClipboard', () => {
  afterEach(() => vi.unstubAllGlobals());
  it('is true when the browser copies', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal('navigator', { clipboard: { writeText } });
    expect(await writeClipboard('x')).toBe(true);
    expect(writeText).toHaveBeenCalledWith('x');
  });
  it('is false when the browser refuses', async () => {
    vi.stubGlobal('navigator', { clipboard: { writeText: vi.fn().mockRejectedValue(new Error('no')) } });
    expect(await writeClipboard('x')).toBe(false);
  });
  it('is false when there is no clipboard', async () => {
    vi.stubGlobal('navigator', {});
    expect(await writeClipboard('x')).toBe(false);
  });
});
