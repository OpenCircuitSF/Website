import { describe, expect, it } from 'vitest';
import {
  BLE_START,
  BROWNOUT_V,
  NETWORKS,
  averageCurrentMa,
  batteryHours,
  bleStep,
  connectOutcome,
  formatCurrent,
  formatDuration,
  recommend,
  simulateSupply,
  sleepCurrentMa,
  wifiSketch,
  type BleAction,
  type BleState,
} from './wireless';

describe('WiFi connection outcomes', () => {
  it('connects to the 2.4 GHz home network and prints an IP', () => {
    const o = connectOutcome('home');
    expect(o.status).toBe('WL_CONNECTED');
    expect(o.log.some((l) => l.startsWith('IP address:'))).toBe(true);
  });

  it('cannot see a 5 GHz-only network', () => {
    expect(connectOutcome('five').status).toBe('WL_NO_SSID_AVAIL');
    expect(connectOutcome('five').fix).toContain('2.4 GHz');
  });

  it('a wrong password fails; a captive portal connects without internet', () => {
    expect(connectOutcome('password').status).toBe('WL_CONNECT_FAILED');
    const cafe = connectOutcome('cafe');
    expect(cafe.status).toBe('WL_CONNECTED');
    expect(cafe.tone).toBe('meh');
  });

  it('a bad-antenna SuperMini only connects with lowered TX power', () => {
    expect(connectOutcome('home', true, false).status).toBe('WL_DISCONNECTED');
    expect(connectOutcome('home', true, true).status).toBe('WL_CONNECTED');
  });

  it('every network has an outcome, and the sketch adds setTxPower only when asked', () => {
    for (const n of NETWORKS) expect(connectOutcome(n.id).log.length).toBeGreaterThan(1);
    expect(wifiSketch(false)).not.toContain('setTxPower');
    expect(wifiSketch(true)).toContain('WiFi.setTxPower(WIFI_POWER_8_5dBm);');
    // setTxPower comes after begin, per the workaround.
    const s = wifiSketch(true);
    expect(s.indexOf('WiFi.begin')).toBeLessThan(s.indexOf('setTxPower'));
  });
});

describe('BLE or WiFi', () => {
  it('internet means WiFi, a nearby phone on a battery means BLE', () => {
    expect(recommend(['internet']).pick).toBe('wifi');
    expect(recommend(['phone', 'battery']).pick).toBe('ble');
    expect(recommend(['internet', 'phone']).pick).toBe('both');
    expect(recommend([]).pick).toBe('none');
  });

  it('audio needs Classic Bluetooth, which the C3 lacks', () => {
    const r = recommend(['audio', 'phone']);
    expect(r.pick).toBe('classic');
    expect(r.reasons.join(' ')).toContain('Low Energy');
  });
});

describe('BLE light switch', () => {
  const run = (...actions: BleAction[]): BleState => actions.reduce(bleStep, BLE_START);

  it('scan, connect, write 1 turns the LED on', () => {
    const s = run({ type: 'scan' }, { type: 'scanDone' }, { type: 'connect' }, { type: 'write', value: 1 });
    expect(s.stage).toBe('connected');
    expect(s.led).toBe(1);
  });

  it('cannot read or write before connecting', () => {
    const s = run({ type: 'read' }, { type: 'write', value: 1 });
    expect(s).toEqual(BLE_START);
  });

  it('notifications reach the phone only when subscribed', () => {
    const base = run({ type: 'scan' }, { type: 'scanDone' }, { type: 'connect' }, { type: 'read' });
    expect(base.phoneValue).toBe(0);
    const quiet = bleStep(base, { type: 'pressBoot' });
    expect(quiet.led).toBe(1);
    expect(quiet.phoneValue).toBe(0);
    const told = bleStep(bleStep(base, { type: 'subscribe' }), { type: 'pressBoot' });
    expect(told.phoneValue).toBe(1);
  });

  it('disconnecting keeps the LED state and goes back to advertising', () => {
    const s = run({ type: 'scan' }, { type: 'scanDone' }, { type: 'connect' }, { type: 'write', value: 1 }, { type: 'disconnect' });
    expect(s.stage).toBe('idle');
    expect(s.led).toBe(1);
    expect(s.subscribed).toBe(false);
  });
});

describe('WiFi brownout', () => {
  const base = { thinCable: false, capacitor: false, strip: false, lowTx: false };

  it('a good cable rides through the bursts', () => {
    const t = simulateSupply(base);
    expect(t.resets).toBe(false);
    expect(t.marginal).toBe(false);
    expect(t.minV).toBeGreaterThan(4.5);
  });

  it('a thin cable browns out on a burst; a capacitor or lower TX power saves it', () => {
    expect(simulateSupply({ ...base, thinCable: true }).resets).toBe(true);
    const cap = simulateSupply({ ...base, thinCable: true, capacitor: true });
    expect(cap.resets).toBe(false);
    expect(cap.minV).toBeGreaterThan(BROWNOUT_V);
    expect(simulateSupply({ ...base, thinCable: true, lowTx: true }).resets).toBe(false);
  });

  it('an LED strip on a thin cable resets even with the capacitor', () => {
    expect(simulateSupply({ ...base, thinCable: true, strip: true, capacitor: true }).resets).toBe(true);
  });

  it('the trace dips during bursts and recovers between them', () => {
    const t = simulateSupply(base);
    expect(t.volts[0]).toBeGreaterThan(t.minV);
    expect(t.volts[t.volts.length - 1]).toBeCloseTo(t.volts[0], 2);
  });
});

describe('deep sleep battery life', () => {
  const plan = { awakeS: 2, sleepMin: 10, wifi: false, boardParts: false, batteryMah: 1000 };

  it('the chip alone sleeps at 5 µA; board parts add about a milliamp', () => {
    expect(sleepCurrentMa(false)).toBeCloseTo(0.005);
    expect(sleepCurrentMa(true)).toBeGreaterThan(1);
  });

  it('average current sits between the sleep and awake currents', () => {
    const a = averageCurrentMa(plan);
    expect(a).toBeGreaterThan(0.005);
    expect(a).toBeLessThan(25);
    // 25 mA × 2 s + 0.005 mA × 600 s over 602 s
    expect(a).toBeCloseTo((25 * 2 + 0.005 * 600) / 602, 6);
  });

  it('WiFi and the power LED both shorten battery life a lot', () => {
    const chip = batteryHours(plan);
    expect(batteryHours({ ...plan, wifi: true })).toBeLessThan(chip / 3);
    expect(batteryHours({ ...plan, boardParts: true })).toBeLessThan(chip);
  });

  it('formats durations and currents', () => {
    expect(formatDuration(5)).toBe('5 hours');
    expect(formatDuration(24 * 30)).toBe('30 days');
    expect(formatDuration(24 * 365 * 1.5)).toBe('1.5 years');
    expect(formatCurrent(25)).toBe('25 mA');
    expect(formatCurrent(1.055)).toBe('1.05 mA');
    expect(formatCurrent(0.005)).toBe('5 µA');
    expect(formatCurrent(0.055)).toBe('55 µA');
  });
});
