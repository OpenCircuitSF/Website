import { describe, expect, it } from 'vitest';
import {
  COOLING,
  HEAT_LOADS,
  HOT_THROTTLED,
  ZERO_DEVICES,
  ZERO_PORTS,
  clockMhz,
  hdmiOutcome,
  heatRun,
  minSec,
  plugZero,
  powerW,
  type CoolingId,
  type HeatLoadId,
  type HeatStatus,
} from './piHardware';

describe('zero 2w ports', () => {
  it('a keyboard in PWR IN gets power but no data', () => {
    const r = plugZero('keyboard', 'pwr', true);
    expect(r.outcome).toBe('noData');
    expect(r.fits).toBe(true);
  });
  it('USB devices work in the USB port with an OTG adapter', () => {
    for (const d of ['keyboard', 'drive', 'dac', 'hub'] as const) expect(plugZero(d, 'usb', true).outcome, d).toBe('works');
  });
  it('without an adapter a full-size USB plug fits neither micro-USB port', () => {
    for (const p of ['usb', 'pwr'] as const) {
      const r = plugZero('keyboard', p, false);
      expect(r.outcome).toBe('noAdapter');
      expect(r.fits).toBe(false);
    }
  });
  it('power belongs in PWR IN; the data port works but is used up', () => {
    expect(plugZero('charger', 'pwr', true).outcome).toBe('powered');
    const r = plugZero('charger', 'usb', true);
    expect(r.outcome).toBe('poweredDataPort');
    expect(r.tone).toBe('meh');
  });
  it('mini-HDMI is the video port and a micro-HDMI cable does not fit', () => {
    expect(plugZero('monitor', 'hdmi', true).outcome).toBe('video');
    const r = plugZero('microHdmi', 'hdmi', true);
    expect(r.outcome).toBe('wrongShape');
    expect(r.title.startsWith('Doesn’t fit')).toBe(true);
  });
  it('every device, port and adapter combination has an answer', () => {
    for (const d of ZERO_DEVICES)
      for (const p of ZERO_PORTS)
        for (const a of [true, false]) {
          const r = plugZero(d.id, p.id, a);
          expect(r.title.length).toBeGreaterThan(0);
          expect(r.body.length).toBeGreaterThan(0);
        }
  });
});

describe('pi 4 micro-hdmi', () => {
  it('HDMI0 with the screen on first and a proper cable gives a picture', () => {
    const r = hdmiOutcome({ port: 'hdmi0', screenOn: 'before', cable: 'microCable' });
    expect(r.picture).toBe('yes');
    expect(r.tone).toBe('good');
    expect(r.title).toBe('Picture');
  });
  it('HDMI1 works but points back to HDMI0', () => {
    const r = hdmiOutcome({ port: 'hdmi1', screenOn: 'before', cable: 'microAdapter' });
    expect(r.picture).toBe('yes');
    expect(r.tone).toBe('meh');
  });
  it('a screen switched on after boot may stay blank, with the right force line', () => {
    expect(hdmiOutcome({ port: 'hdmi0', screenOn: 'after', cable: 'microCable' }).fixLine).toBe('video=HDMI-A-1:1280x720@60D');
    const r = hdmiOutcome({ port: 'hdmi1', screenOn: 'after', cable: 'microCable' });
    expect(r.picture).toBe('maybe');
    expect(r.fixLine).toBe('video=HDMI-A-2:1280x720@60D');
  });
  it('a mini-HDMI cable does not fit a Pi 4', () => {
    const r = hdmiOutcome({ port: 'hdmi0', screenOn: 'before', cable: 'miniCable' });
    expect(r.picture).toBe('no');
    expect(r.fixLine).toBeNull();
  });
  it('a cheap cable beats every other setting', () => {
    expect(hdmiOutcome({ port: 'hdmi1', screenOn: 'after', cable: 'cheapCable' }).title).toBe('No picture: the cable');
  });
  it('VGA is a maybe', () => {
    const r = hdmiOutcome({ port: 'hdmi0', screenOn: 'before', cable: 'vga' });
    expect(r.picture).toBe('maybe');
    expect(r.fixLine).toBe('video=HDMI-A-1:1280x720@60D');
  });
});

describe('heat throttling', () => {
  it('clockMhz steps down from 80 °C and bottoms out at 90 °C', () => {
    const cases: [number, number][] = [
      [79.9, 1500],
      [80, 1500],
      [82.5, 1250],
      [85, 1000],
      [87.5, 800],
      [90, 600],
      [100, 600],
    ];
    for (const [t, mhz] of cases) expect(clockMhz(t), String(t)).toBeCloseTo(mhz, 6);
  });
  it('powerW scales the busy part with the clock', () => {
    expect(powerW('idle', 1500)).toBeCloseTo(2.7, 9);
    expect(powerW('idle', 600)).toBeCloseTo(2.7, 9);
    expect(powerW('compile', 1500)).toBeCloseTo(6.4, 9);
    expect(powerW('compile', 750)).toBeCloseTo(4.55, 9);
  });

  const row = (load: HeatLoadId, cooling: CoolingId, startC: number, endC: number, endMhz: number, softAtS: number | null, hardAtS: number | null, status: HeatStatus) => {
    const r = heatRun(load, cooling);
    expect(r.startC).toBeCloseTo(startC, 1);
    expect(r.endC).toBeCloseTo(endC, 1);
    expect(r.endMhz).toBe(endMhz);
    expect(r.softAtS).toBe(softAtS);
    expect(r.hardAtS).toBe(hardAtS);
    expect(r.status).toBe(status);
  };
  it('an RGB panel in a closed box throttles after 2 min 20 s', () => row('rgb', 'closedBox', 60.1, 83.06, 1194, 140, null, 'soft'));
  it('compiling in a closed box reaches 85 °C', () => row('compile', 'closedBox', 60.1, 87.01, 839, 64, 103, 'hard'));
  it('a bare board compiling just crosses 80 °C', () => row('compile', 'bare', 49.3, 80.81, 1419, 229, null, 'soft'));
  it('heatsinks keep a compile at full speed', () => row('compile', 'heatsink', 43.9, 69.33, 1500, null, null, 'ok'));
  it('a fan keeps it cool', () => row('compile', 'fan', 33.1, 44.2, 1500, null, null, 'ok'));
  it('music in a closed box is warm, not throttled', () => row('music', 'closedBox', 60.1, 72.05, 1500, null, null, 'warm'));
  it('an RGB panel on a bare board in open air stays under 70 °C', () => row('rgb', 'bare', 49.3, 69.26, 1500, null, null, 'ok'));
  it('heatsinks keep an RGB panel cool', () => row('rgb', 'heatsink', 43.9, 59.16, 1500, null, null, 'ok'));
  it('idle stays where it starts', () => row('idle', 'fan', 33.1, 33.1, 1500, null, null, 'ok'));

  it('runs have 601 samples and never cool below where they start', () => {
    for (const l of HEAT_LOADS)
      for (const c of COOLING) {
        const r = heatRun(l.id, c.id);
        expect(r.samples.length).toBe(601);
        for (const s of r.samples) expect(s.tempC).toBeGreaterThanOrEqual(r.startC - 1e-9);
      }
  });
  it('throttled is 0x60006 when hot and 0 otherwise', () => {
    expect(heatRun('rgb', 'closedBox').throttled).toBe(HOT_THROTTLED);
    expect(heatRun('compile', 'closedBox').throttled).toBe(0x60006);
    expect(heatRun('music', 'closedBox').throttled).toBe(0);
    expect(heatRun('compile', 'fan').throttled).toBe(0);
  });
  it('minSec formats seconds', () => {
    expect(minSec(64)).toBe('1 min 4 s');
    expect(minSec(229)).toBe('3 min 49 s');
    expect(minSec(45)).toBe('45 s');
    expect(minSec(120)).toBe('2 min');
  });
});
