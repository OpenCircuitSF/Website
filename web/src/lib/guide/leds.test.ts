import { describe, expect, it } from 'vitest';
import { DEFAULT_CONFIG, colorAt, namedColor, physicalStrip, positionAt, renderFrame } from './leds';

const palette = DEFAULT_CONFIG.palette.map((n) => namedColor(n).rgb);

describe('firmware render math', () => {
  it('position 0 is pure red, 256 pure orange, 128 halfway', () => {
    expect(colorAt(0, palette)).toEqual({ r: 255, g: 0, b: 0 });
    expect(colorAt(256, palette)).toEqual({ r: 255, g: 165, b: 0 });
    expect(colorAt(128, palette)).toEqual({ r: 255, g: 82, b: 0 });
  });

  it('wraps from magenta back to red', () => {
    expect(colorAt(256 * 5, palette)).toEqual(colorAt(0, palette));
  });

  it('STEP_MS of elapsed time is one color step', () => {
    expect(positionAt(2000, 2000, 5)).toBe(256);
    expect(positionAt(10000, 2000, 5)).toBe(0);
  });

  it('LED_SPREAD 0 makes every LED the same color', () => {
    const frame = renderFrame(1234, { ...DEFAULT_CONFIG, ledSpread: 0 });
    expect(new Set(frame.map((c) => JSON.stringify(c))).size).toBe(1);
  });

  it('NUM_LEDS too low leaves the tail dark; too high spills off the end', () => {
    const low = physicalStrip(renderFrame(0, { ...DEFAULT_CONFIG, numLeds: 5 }), DEFAULT_CONFIG);
    expect(low.slice(5)).toEqual([null, null, null]);
    const high = physicalStrip(renderFrame(0, { ...DEFAULT_CONFIG, numLeds: 11 }), DEFAULT_CONFIG);
    expect(high).toHaveLength(8);
    expect(high.every(Boolean)).toBe(true);
  });

  it('RGB order on a GRB strip swaps red and green', () => {
    const cfg = { ...DEFAULT_CONFIG, colorOrder: 'RGB' as const, ledSpread: 0 };
    expect(physicalStrip(renderFrame(0, cfg), cfg)[0]).toEqual({ r: 0, g: 255, b: 0 });
  });
});
