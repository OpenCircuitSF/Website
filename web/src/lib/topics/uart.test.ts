import { describe, expect, it } from 'vitest';
import { decode, encode } from './uart';

describe('UART simulation', () => {
  it('round-trips when both ends agree', () => {
    for (const baud of [9600, 115200]) {
      const d = decode(encode('Hello LEDs', baud), baud);
      expect(d.text).toBe('Hello LEDs');
      expect(d.framingErrors).toBe(0);
    }
  });

  it('produces garbage, not silence, when the receiver is too slow', () => {
    const d = decode(encode('Hello LEDs', 115200), 9600);
    expect(d.bytes.length).toBeGreaterThan(0);
    expect(d.text).not.toBe('Hello LEDs');
  });

  it('produces garbage when the receiver is too fast', () => {
    const d = decode(encode('Hello', 9600), 115200);
    expect(d.bytes.length).toBeGreaterThan(0);
    expect(d.text).not.toContain('Hello');
  });
});
