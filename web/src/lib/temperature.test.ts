import { describe, expect, it } from 'vitest';
import { cToF, fToC, formatTemp, formatTempRange, parseTempUnit, renderTemps } from './temperature';

describe('temperature', () => {
  it('converts and rounds every temperature the guides show', () => {
    expect(formatTemp(183, 'F')).toBe('361 °F');
    expect(formatTemp(183, 'C')).toBe('183 °C');
    expect(formatTempRange(183, 183, 'F')).toBe('361 °F');
    expect(formatTempRange(183, 183, 'C')).toBe('183 °C');
    expect(formatTempRange(183, 190, 'F')).toBe('361–374 °F');
    expect(formatTempRange(183, 190, 'C')).toBe('183–190 °C');
    expect(formatTemp(190, 'F')).toBe('374 °F');
    expect(formatTemp(190, 'C')).toBe('190 °C');
    expect(formatTempRange(217, 220, 'F')).toBe('423–428 °F');
    expect(formatTempRange(217, 220, 'C')).toBe('217–220 °C');
    expect(formatTempRange(330, 350, 'F', 10)).toBe('630–660 °F');
    expect(formatTempRange(330, 350, 'C', 10)).toBe('330–350 °C');
    expect(formatTempRange(350, 380, 'F', 10)).toBe('670–710 °F');
    expect(formatTempRange(350, 380, 'C', 10)).toBe('350–380 °C');
    expect(formatTemp(25, 'F')).toBe('77 °F');
    expect(formatTemp(25, 'C')).toBe('25 °C');
    expect(formatTemp(125, 'F')).toBe('257 °F');
    expect(formatTemp(125, 'C')).toBe('125 °C');
    for (const x of [-40, 0, 32, 183, 451.5]) expect(cToF(fToC(x))).toBeCloseTo(x, 9);
  });

  it('°C ignores step', () => {
    expect(formatTemp(337, 'C', 10)).toBe('337 °C');
    expect(formatTemp(337.4, 'C', 10)).toBe('337 °C');
  });

  it('ranges collapse when both ends round equal', () => {
    expect(formatTempRange(330, 332, 'F', 10)).toBe('630 °F');
  });

  it('renderTemps replaces single, range and stepped tokens', () => {
    expect(renderTemps('at {temp:183}.', 'F')).toBe('at 361 °F.');
    expect(renderTemps('about {temp:217-220}', 'C')).toBe('about 217–220 °C');
    expect(renderTemps('Start at {temp:330-350:10}', 'F')).toBe('Start at 630–660 °F');
    expect(renderTemps('{temp:183} and {temp:190}', 'F')).toBe('361 °F and 374 °F');
  });

  it('renderTemps leaves a malformed token visible', () => {
    expect(renderTemps('at {temp:abc}', 'F')).toBe('at {temp:abc}');
    expect(renderTemps('at {temp:1.5}', 'F')).toBe('at {temp:1.5}');
  });

  it('parseTempUnit defaults to F for anything but C', () => {
    expect(parseTempUnit(null)).toBe('F');
    expect(parseTempUnit(undefined)).toBe('F');
    expect(parseTempUnit('')).toBe('F');
    expect(parseTempUnit('c')).toBe('F');
    expect(parseTempUnit('C')).toBe('C');
    expect(parseTempUnit('F')).toBe('F');
    expect(parseTempUnit('kelvin')).toBe('F');
  });
});
