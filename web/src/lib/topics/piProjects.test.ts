// Raspberry Pi: Volumio and RGB panels (#0721)
import { describe, expect, it } from 'vitest';
import {
  MATRIX_START,
  PANEL_H,
  PANEL_W,
  PLAY_SOURCES,
  matrixCommand,
  matrixState,
  panelPixels,
  refreshHz,
  rowOpacity,
  volumioChain,
  type MatrixConfig,
} from './piProjects';

const sources = import.meta.glob(['./RgbMatrixDemo.svelte', './VolumioDemo.svelte'], {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;

const FIXED: MatrixConfig = { ...MATRIX_START, soundOff: true, mapping: 'adafruit-hat', slowdown: 2, isolatedCore: true };

describe('Volumio', () => {
  it('Spotify, AirPlay, UPnP, radio and files are free; Tidal, Qobuz and Bluetooth need Premium', () => {
    const tiers = Object.fromEntries(PLAY_SOURCES.map((s) => [s.id, s.tier]));
    expect(tiers).toEqual({
      spotify: 'free',
      airplay: 'free',
      upnp: 'free',
      radio: 'free',
      files: 'free',
      tidal: 'premium',
      qobuz: 'premium',
      bluetooth: 'premium',
    });
  });

  it('Spotify needs each listener’s own Spotify Premium account', () => {
    expect(PLAY_SOURCES.find((s) => s.id === 'spotify')?.note).toContain('own Spotify Premium account');
  });

  it('a Zero 2W has no headphone jack, so the jack chain fails with a reason', () => {
    const c = volumioChain('airplay', 'zero2w', 'jack');
    expect(c.ok).toBe(false);
    expect(c.problem).toContain('no headphone jack');
    expect(c.nodes.map((n) => n.ok)).toEqual([true, true, false, false, false]);
  });

  it('a Pi 4 headphone jack and a Zero 2W DAC HAT both work', () => {
    expect(volumioChain('airplay', 'pi4', 'jack').ok).toBe(true);
    const c = volumioChain('radio', 'zero2w', 'dac-hat');
    expect(c.ok).toBe(true);
    expect(c.problem).toBeNull();
    expect(c.nodes[0].label).toBe('Internet');
    expect(c.feature).toBe('Web radio (built in)');
  });

  it('every chain runs source, Pi, output, amplifier, speakers', () => {
    for (const s of PLAY_SOURCES) {
      expect(volumioChain(s.id, 'pi4', 'hdmi').nodes.map((n) => n.id)).toEqual(['source', 'pi', 'out', 'amp', 'speakers']);
    }
  });
});

describe('RGB matrix', () => {
  it('a fresh install will not start because of the sound driver', () => {
    const s = matrixState(MATRIX_START);
    expect(s.runs).toBe(false);
    expect(s.look).toBe('dark');
    expect(s.problems).toEqual(['sound']);
  });

  it('the wrong mapping gives garbage; adafruit-hat-pwm needs the mod', () => {
    expect(matrixState({ ...FIXED, mapping: 'regular' }).look).toBe('garbage');
    expect(matrixState({ ...FIXED, mapping: 'regular' }).problems[0]).toBe('mapping');
    expect(matrixState({ ...FIXED, mapping: 'adafruit-hat-pwm' }).look).toBe('garbage');
    expect(matrixState({ ...FIXED, mapping: 'adafruit-hat-pwm', pwmMod: true }).look).toBe('clean');
  });

  it('slowdown 0 is garbage, 1 shimmers, 2 is clean', () => {
    expect(matrixState({ ...FIXED, slowdown: 0 }).look).toBe('garbage');
    expect(matrixState({ ...FIXED, slowdown: 1 }).look).toBe('shimmer');
    expect(matrixState({ ...FIXED, slowdown: 1 }).problems).toContain('slowdown');
    expect(matrixState({ ...FIXED, slowdown: 2 }).look).toBe('clean');
  });

  it('refresh model values', () => {
    expect(refreshHz({ ...FIXED, pwmBits: 11, chain: 1, slowdown: 2 })).toBe(178);
    expect(refreshHz({ ...FIXED, pwmBits: 11, chain: 3, slowdown: 4 })).toBe(91);
    expect(refreshHz({ ...FIXED, pwmBits: 7, chain: 3, slowdown: 4 })).toBe(219);
    expect(refreshHz({ ...FIXED, pwmBits: 11, chain: 3, slowdown: 2 })).toBe(120);
  });

  it('without an isolated core the refresh dips to 70%', () => {
    const s = matrixState({ ...FIXED, isolatedCore: false });
    expect(s.jitter).toBe(true);
    expect(s.refreshHz).toBe(178);
    expect(s.refreshLowHz).toBe(125);
    expect(s.problems).toEqual(['core']);
  });

  it('fewer PWM bits fix visible flicker on three chained panels', () => {
    const s11 = matrixState({ ...FIXED, slowdown: 4, chain: 3 });
    expect(s11.flicker).toBe('visible');
    expect(s11.problems).toEqual(['refresh']);
    expect(matrixState({ ...FIXED, slowdown: 4, chain: 3, pwmBits: 7 }).flicker).toBe('steady');
  });

  it('all fixed is steady with no problems', () => {
    const s = matrixState(FIXED);
    expect(s.runs).toBe(true);
    expect(s.look).toBe('clean');
    expect(s.jitter).toBe(false);
    expect(s.problems).toEqual([]);
  });

  it('panel pixels are deterministic and the right size', () => {
    for (const cfg of [MATRIX_START, FIXED, { ...FIXED, slowdown: 1 }, { ...FIXED, mapping: 'regular' as const }]) {
      const s = matrixState(cfg);
      const a = panelPixels(s, cfg.pwmBits);
      expect(a).toHaveLength(512);
      expect(a.length).toBe(PANEL_W * PANEL_H);
      expect(panelPixels(s, cfg.pwmBits)).toEqual(a);
    }
    expect(new Set(panelPixels(matrixState(MATRIX_START), 11))).toEqual(new Set(['#111111']));
  });

  it('few PWM bits band the grey ramp', () => {
    const ramp = (bits: number) => new Set(panelPixels(matrixState({ ...FIXED, pwmBits: bits }), bits).slice(12 * PANEL_W));
    expect(ramp(1).size).toBe(2);
    expect(ramp(11).size).toBe(32);
  });

  it('rowOpacity is static banding, never time-based', () => {
    const clean = matrixState(FIXED);
    expect([...Array(PANEL_H).keys()].every((r) => rowOpacity(r, clean) === 1)).toBe(true);
    const jit = matrixState({ ...FIXED, isolatedCore: false });
    expect(rowOpacity(4, jit)).toBe(0.55);
    expect(rowOpacity(13, jit)).toBe(0.55);
    expect(rowOpacity(0, jit)).toBe(1);
    const both = matrixState({ ...FIXED, isolatedCore: false, slowdown: 4, chain: 3 });
    expect(both.flicker).toBe('visible');
    expect(rowOpacity(2, both)).toBe(0.45);
    expect(rowOpacity(4, both)).toBe(0.55);
    expect(rowOpacity(6, both)).toBe(0.45);
  });

  it('matrixCommand carries every flag', () => {
    const cmd = matrixCommand({ ...FIXED, chain: 2, pwmBits: 8 });
    for (const f of ['--led-chain=2', '--led-gpio-mapping=adafruit-hat', '--led-slowdown-gpio=2', '--led-pwm-bits=8', '--led-show-refresh', 'sudo ']) {
      expect(cmd).toContain(f);
    }
  });

  it('the panel demo source has no motion', () => {
    const banned = /@keyframes|animation\s*:|transition\s*:|transition:|<animate|setInterval|setTimeout|requestAnimationFrame/;
    expect(Object.keys(sources).sort()).toEqual(['./RgbMatrixDemo.svelte', './VolumioDemo.svelte']);
    for (const [path, src] of Object.entries(sources)) {
      expect(src.length, path).toBeGreaterThan(100);
      expect(src, path).not.toMatch(banned);
    }
  });
});
