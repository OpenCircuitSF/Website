// Raspberry Pi batch 2: Zero audio, RGB panel basics and power (#0726)
import { describe, expect, it } from 'vitest';
import {
  AUDIO_AXES,
  ZERO_AUDIO,
  amps,
  audioVerdict,
  bestFor,
  flickerClass,
  layoutRefreshHz,
  matrixLayout,
  orList,
  panelPower,
  panelSize,
  recommendedSupplyA,
  scanRows,
  wireGauge,
  zeroAudio,
  type PanelPowerInput,
  type PanelSizeId,
} from './piProjects2';
import { MATRIX_START, refreshHz } from './piProjects';

const sources = import.meta.glob(['./ZeroAudioDemo.svelte', './RgbLayoutDemo.svelte', './RgbPowerDemo.svelte'], {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;

describe('zero 2w audio out', () => {
  it('four options in order, each with a chain starting at the Zero', () => {
    expect(ZERO_AUDIO.map((o) => o.id)).toEqual(['i2s', 'usb', 'hdmi', 'bt']);
    for (const o of ZERO_AUDIO) expect(o.nodes[0]).toBe('Pi Zero 2W');
  });

  it('the DAC HAT setup is a config.txt overlay line, marked as an example', () => {
    const o = zeroAudio('i2s');
    expect(o.setupFile).toBe('/boot/firmware/config.txt');
    expect(o.setup).toEqual(['dtoverlay=hifiberry-dac']);
    expect(o.setupNote.startsWith('Example only')).toBe(true);
  });

  it('a USB DAC needs no setup and links the USB ports guide', () => {
    const o = zeroAudio('usb');
    expect(o.setupFile).toBeNull();
    expect(o.setup).toEqual(['aplay -l']);
    expect(o.link?.href).toBe('/guides/pi-zero-usb-ports');
  });

  it('bestFor lists the good options in order', () => {
    expect(bestFor('quality')).toEqual(['i2s', 'hdmi']);
    expect(bestFor('cost')).toEqual(['usb', 'hdmi', 'bt']);
    expect(bestFor('latency')).toEqual(['i2s', 'usb', 'hdmi']);
    expect(bestFor('parts')).toEqual(['bt']);
  });

  it('orList joins with commas and or', () => {
    expect(orList(['a', 'b', 'c'])).toBe('a, b or c');
    expect(orList(['a', 'b'])).toBe('a or b');
    expect(orList(['a'])).toBe('a');
  });

  it('verdicts: Bluetooth is poor for delay, a USB DAC is workable for quality', () => {
    expect(audioVerdict('bt', 'latency')).toEqual({
      tone: 'bad',
      line: 'A poor pick when low delay matters most; try a DAC HAT, a USB DAC or HDMI.',
    });
    expect(audioVerdict('usb', 'quality')).toEqual({
      tone: 'meh',
      line: 'Workable when sound quality matters most; a DAC HAT or HDMI would be better.',
    });
    expect(audioVerdict('i2s', 'quality')).toEqual({ tone: 'good', line: 'A good pick when sound quality matters most.' });
  });

  it('every option has a rating on every axis', () => {
    for (const o of ZERO_AUDIO) {
      for (const a of AUDIO_AXES) {
        expect(['good', 'ok', 'poor']).toContain(o.ratings[a.id].rating);
        expect(o.ratings[a.id].text.length).toBeGreaterThan(5);
      }
    }
  });
});

describe('rgb panel layout', () => {
  it('scan rows are half the panel rows', () => {
    expect((['32x16', '32x32', '64x32', '64x64'] as PanelSizeId[]).map((id) => scanRows(panelSize(id)))).toEqual([8, 16, 16, 32]);
  });

  it('refresh falls as the chain grows', () => {
    const chains = [1, 2, 3, 4, 6, 8];
    const want: Record<PanelSizeId, number[]> = {
      '32x16': [405, 357, 318, 287, 241, 207],
      '32x32': [203, 178, 159, 144, 120, 103],
      '64x32': [178, 144, 120, 103, 81, 66],
      '64x64': [89, 72, 60, 52, 40, 33],
    };
    for (const [id, hz] of Object.entries(want)) {
      expect(chains.map((c) => layoutRefreshHz(panelSize(id as PanelSizeId), c)), id).toEqual(hz);
    }
  });

  it('the refresh model matches the flicker guide for 64×32 panels', () => {
    for (const chain of [1, 2, 3])
      for (const bits of [7, 11])
        for (const sd of [2, 4]) {
          expect(layoutRefreshHz(panelSize('64x32'), chain, bits, sd)).toBe(
            refreshHz({ ...MATRIX_START, chain, pwmBits: bits, slowdown: sd }),
          );
        }
    expect(layoutRefreshHz(panelSize('64x32'), 3, 11, 4)).toBe(91);
    expect(layoutRefreshHz(panelSize('64x32'), 3, 7, 4)).toBe(219);
  });

  it('parallel chains add rows without lowering the refresh', () => {
    const par = matrixLayout('64x32', 2, 3, 'triple');
    const long = matrixLayout('64x32', 6, 1, 'single');
    expect([par.refreshHz, par.pixels, par.width, par.height]).toEqual([144, 12288, 128, 96]);
    expect(par.title).toBe('Fine to the eye: 144 Hz');
    expect([long.refreshHz, long.pixels, long.width, long.height]).toEqual([81, 12288, 384, 32]);
    expect(long.title).toBe('Flicker likely: 81 Hz');
    const one = matrixLayout('64x32', 1, 1, 'single');
    expect([one.width, one.height, one.pixels, one.refreshHz, one.title]).toEqual([64, 32, 2048, 178, 'Fine to the eye: 178 Hz']);
    expect(matrixLayout('32x16', 1, 1, 'single').title).toBe('Steady: 405 Hz');
  });

  it('a single-chain adapter refuses parallel chains', () => {
    const l = matrixLayout('64x32', 2, 3, 'single');
    expect(l.title).toBe('One chain only on this adapter');
    expect(l.tone).toBe('bad');
    expect(l.problem).toMatch(/drives one chain/);
    expect(matrixLayout('64x32', 2, 3, 'triple').problem).toBeNull();
  });

  it('panels are laid out row by row', () => {
    const p = matrixLayout('64x32', 2, 3, 'triple').panels;
    expect(p.length).toBe(6);
    expect(p[1]).toEqual({ chainIndex: 1, parallelIndex: 0, x: 64, y: 0 });
    expect(p[5]).toEqual({ chainIndex: 1, parallelIndex: 2, x: 64, y: 64 });
  });

  it('flicker classes use 100 and 200 Hz', () => {
    expect([99, 100, 199, 200].map(flickerClass)).toEqual(['visible', 'camera', 'camera', 'steady']);
  });

  it('the command carries rows, cols, chain and parallel', () => {
    expect(matrixLayout('64x32', 2, 3, 'triple').command).toBe(
      'sudo ./demo -D 0 --led-rows=32 --led-cols=64 --led-chain=2 --led-parallel=3 --led-slowdown-gpio=2 --led-show-refresh',
    );
  });
});

describe('rgb panel power', () => {
  function check(
    i: PanelPowerInput,
    w: { per: number; total: number; worst: number; status: string; rec: number | null; panel: string | null; main: string | null },
  ) {
    const p = panelPower(i);
    expect(p.perPanelA).toBeCloseTo(w.per, 2);
    expect(p.totalA).toBeCloseTo(w.total, 2);
    expect(p.worstTotalA).toBeCloseTo(w.worst, 2);
    expect(p.status).toBe(w.status);
    expect(p.recommendedA).toBe(w.rec);
    expect(p.panelWire?.split(' (')[0] ?? null).toBe(w.panel);
    expect(p.mainWire?.split(' (')[0] ?? null).toBe(w.main);
  }

  it('two panels showing a picture fit 4 A, but full white would not', () => {
    check({ size: '64x32', panels: 2, brightness: 100, content: 'image', supplyA: 4 }, { per: 1.4, total: 2.8, worst: 8, status: 'whiteOver', rec: 10, panel: '18 AWG', main: '16 AWG' });
  });
  it('one panel at full white leaves no headroom on 4 A', () => {
    check({ size: '64x32', panels: 1, brightness: 100, content: 'white', supplyA: 4 }, { per: 4, total: 4, worst: 4, status: 'noRoom', rec: 5, panel: '18 AWG', main: '18 AWG' });
  });
  it('two panels at full white overload 4 A', () => {
    check({ size: '64x32', panels: 2, brightness: 100, content: 'white', supplyA: 4 }, { per: 4, total: 8, worst: 8, status: 'over', rec: 10, panel: '18 AWG', main: '16 AWG' });
  });
  it('half brightness halves the current', () => {
    check({ size: '64x32', panels: 2, brightness: 50, content: 'white', supplyA: 10 }, { per: 2, total: 4, worst: 4, status: 'ok', rec: 5, panel: '20 AWG', main: '18 AWG' });
  });
  it('text on black draws a fraction', () => {
    check({ size: '32x32', panels: 4, brightness: 100, content: 'text', supplyA: 4 }, { per: 0.16, total: 0.64, worst: 8, status: 'whiteOver', rec: 10, panel: '20 AWG', main: '16 AWG' });
  });
  it('six 64×64 panels need more than one main wire', () => {
    check({ size: '64x64', panels: 6, brightness: 100, content: 'white', supplyA: 40 }, { per: 4, total: 24, worst: 24, status: 'ok', rec: 30, panel: '18 AWG', main: null });
  });
  it('six panels at full white overload 20 A', () => {
    check({ size: '64x32', panels: 6, brightness: 100, content: 'white', supplyA: 20 }, { per: 4, total: 24, worst: 24, status: 'over', rec: 30, panel: '18 AWG', main: null });
  });

  it('recommendedSupplyA adds 20% and picks a standard size', () => {
    expect(recommendedSupplyA(50)).toBe(60);
    expect(recommendedSupplyA(50.1)).toBeNull();
    expect(recommendedSupplyA(4)).toBe(5);
  });

  it('wireGauge thresholds', () => {
    expect(wireGauge(3)).toBe('20 AWG (0.5 mm²)');
    expect(wireGauge(3.01)).toBe('18 AWG (0.8 mm²)');
    expect(wireGauge(20)).toBe('12 AWG (3.3 mm²)');
    expect(wireGauge(20.5)).toBeNull();
  });

  it('amps formats one decimal', () => {
    expect(amps(2.8)).toBe('2.8 A');
    expect(amps(8)).toBe('8.0 A');
  });
});

it('the new demo sources have no motion', () => {
  const banned = /@keyframes|animation\s*:|transition\s*:|transition:|<animate|setInterval|setTimeout|requestAnimationFrame/;
  expect(Object.keys(sources).sort()).toEqual(['./RgbLayoutDemo.svelte', './RgbPowerDemo.svelte', './ZeroAudioDemo.svelte']);
  for (const [path, src] of Object.entries(sources)) {
    expect(src.length, path).toBeGreaterThan(100);
    expect(src, path).not.toMatch(banned);
  }
});
