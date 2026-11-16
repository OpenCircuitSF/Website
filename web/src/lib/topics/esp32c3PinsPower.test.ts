import { describe, expect, it } from 'vitest';
import {
  GPIO_PINS,
  JOBS,
  LEDC_CHANNELS,
  averageReading,
  analogZone,
  bootResult,
  divider,
  dutyValue,
  gammaDuty,
  hasAdc,
  isStrapping,
  levelAtReset,
  maxResolutionBits,
  milliVolts,
  perceived,
  pinAdvice,
  powerResult,
  rawReading,
} from './esp32c3PinsPower';
import { SUPERMINI_PINS } from './supermini';

describe('pin facts come from supermini.ts', () => {
  it('the strapping pins are exactly GPIO 2, 8 and 9 (datasheet §3)', () => {
    expect(GPIO_PINS.filter(isStrapping).map((p) => p.id)).toEqual(['gpio2', 'gpio8', 'gpio9']);
  });
  it('ADC1 is GPIO 0–4; GPIO 5 (ADC2) is not tagged ADC', () => {
    expect(GPIO_PINS.filter(hasAdc).map((p) => p.id).sort()).toEqual(['gpio0', 'gpio1', 'gpio2', 'gpio3', 'gpio4']);
  });
  it('the SuperMini breaks out no flash (11–17) or USB (18, 19) pins', () => {
    for (const n of [11, 12, 13, 14, 15, 16, 17, 18, 19]) expect(SUPERMINI_PINS.some((p) => p.id === `gpio${n}`)).toBe(false);
  });
});

describe('boot pins', () => {
  it('GPIO 9 low at reset means download mode', () => {
    expect(bootResult('gpio9', 'button', true).outcome).toBe('download');
    expect(bootResult('gpio9', 'pulldown', false).outcome).toBe('download');
    expect(bootResult('gpio9', 'sensor-low', false).outcome).toBe('download');
  });
  it('GPIO 9 with its internal pull-up, or a pull-up, runs your program', () => {
    expect(levelAtReset('gpio9', 'nothing', false)).toBe('high');
    expect(bootResult('gpio9', 'nothing', false).outcome).toBe('runs');
    expect(bootResult('gpio9', 'button', false).outcome).toBe('runs');
    expect(bootResult('gpio9', 'pullup', false).outcome).toBe('runs');
  });
  it('an LED to ground on GPIO 9 is a gamble', () => {
    expect(bootResult('gpio9', 'led', false).outcome).toBe('risky');
  });
  it('GPIO 8 low still boots but breaks download mode', () => {
    const r = bootResult('gpio8', 'pulldown', false);
    expect(r.outcome).toBe('risky');
    expect(r.body).toContain('download mode needs GPIO 8 high');
  });
  it('GPIO 2 low is flagged; high or floating is fine', () => {
    expect(bootResult('gpio2', 'pulldown', false).outcome).toBe('risky');
    expect(bootResult('gpio2', 'nothing', false).outcome).toBe('runs');
    expect(bootResult('gpio2', 'pullup', false).outcome).toBe('runs');
  });
  it('a plain pin never affects booting', () => {
    for (const part of ['button', 'led', 'pulldown', 'sensor-low'] as const) expect(bootResult('gpio4', part, true).outcome).toBe('runs');
  });
});

describe('reserved pins', () => {
  it('every job has at least three good pins', () => {
    for (const j of JOBS) expect(GPIO_PINS.filter((p) => pinAdvice(j.id, p.id).verdict === 'good').length, j.id).toBeGreaterThanOrEqual(3);
  });
  it('analog: ADC1 pins are good, GPIO 5 and digital-only pins are avoided', () => {
    expect(pinAdvice('analog', 'gpio3').verdict).toBe('good');
    expect(pinAdvice('analog', 'gpio5').verdict).toBe('avoid');
    expect(pinAdvice('analog', 'gpio6').verdict).toBe('avoid');
    expect(pinAdvice('analog', 'gpio2').verdict).toBe('care');
  });
  it('buttons stay off GPIO 2 and 8, and GPIO 9 already is one', () => {
    expect(pinAdvice('button', 'gpio2').verdict).toBe('avoid');
    expect(pinAdvice('button', 'gpio8').verdict).toBe('avoid');
    expect(pinAdvice('button', 'gpio9').verdict).toBe('care');
    expect(pinAdvice('button', 'gpio6').verdict).toBe('good');
  });
  it('UART0 pins are usable with care; TX flickers an LED strip at boot', () => {
    expect(pinAdvice('strip', 'gpio21').reason).toContain('boot messages');
    expect(pinAdvice('button', 'gpio20').verdict).toBe('care');
  });
});

describe('analog input', () => {
  it('readings are 12-bit and rise with voltage', () => {
    expect(rawReading(0, 1)).toBe(0);
    expect(averageReading(1, 1)).toBeLessThan(averageReading(2, 1));
    expect(rawReading(5, 3)).toBeLessThanOrEqual(4095);
  });
  it('averaging is steadier than single readings', () => {
    const singles = [1, 2, 3, 4, 5, 6].map((s) => rawReading(1.2, s));
    const avgs = [1, 2, 3, 4, 5, 6].map((s) => averageReading(1.2, s));
    const spread = (a: number[]) => Math.max(...a) - Math.min(...a);
    expect(spread(avgs)).toBeLessThan(spread(singles));
  });
  it('analogReadMilliVolts tracks the voltage within a few tens of mV in range', () => {
    expect(Math.abs(milliVolts(1.5, 4) - 1500)).toBeLessThanOrEqual(20);
  });
  it('flags readings above 2.5 V and voltages above 3.3 V', () => {
    expect(analogZone(2)).toBe('ok');
    expect(analogZone(2.8)).toBe('top');
    expect(analogZone(4)).toBe('danger');
  });
  it('a 100k/100k divider halves a LiPo into range; weaker division is flagged', () => {
    expect(divider(4.2, 100, 100)).toMatchObject({ vout: 2.1, ok: true });
    expect(divider(4.2, 47, 100)).toMatchObject({ vout: 2.86, ok: false });
    expect(divider(9, 10, 100).message).toContain('damage');
  });
});

describe('pwm', () => {
  it('the C3 has six LEDC channels and up to 14-bit duty', () => {
    expect(LEDC_CHANNELS).toBe(6);
    expect(maxResolutionBits(1000)).toBe(14);
  });
  it('higher frequency leaves fewer bits', () => {
    expect(maxResolutionBits(5000)).toBe(13);
    expect(maxResolutionBits(20000)).toBe(11);
    expect(maxResolutionBits(1000000)).toBe(6);
  });
  it('maps duty percent to the counter value', () => {
    expect(dutyValue(50, 8)).toBe(128);
    expect(dutyValue(100, 8)).toBe(255);
    expect(dutyValue(0, 8)).toBe(0);
  });
  it('half duty looks much brighter than half; gamma undoes that', () => {
    expect(perceived(50)).toBeGreaterThan(70);
    expect(gammaDuty(50)).toBeLessThan(25);
    expect(perceived(gammaDuty(50))).toBeCloseTo(50, -1);
  });
});

describe('power', () => {
  it('USB charger into USB-C or the 5V pin is safe', () => {
    expect(powerResult('usb-charger', 'usb-c', false).verdict).toBe('safe');
    expect(powerResult('usb-charger', '5v', false).verdict).toBe('safe');
  });
  it('never 5 V or a LiPo on the 3.3 pin', () => {
    expect(powerResult('usb-charger', '3v3', false).verdict).toBe('damage');
    expect(powerResult('lipo', '3v3', false).verdict).toBe('damage');
    expect(powerResult('reg33', '3v3', false).verdict).toBe('safe');
  });
  it('3 × AA on 5V is fine, a LiPo works with limits, 9 V does not', () => {
    expect(powerResult('aa3', '5v', false).verdict).toBe('safe');
    expect(powerResult('lipo', '5v', false).verdict).toBe('works');
    expect(powerResult('nine', '5v', false).verdict).toBe('damage');
  });
  it('an external supply plus USB is flagged', () => {
    expect(powerResult('aa3', '5v', true).verdict).toBe('damage');
    expect(powerResult('usb-charger', '5v', true).body).toContain('check your board');
  });
  it('batteries do not plug into USB-C', () => {
    expect(powerResult('aa3', 'usb-c', false).verdict).toBe('wont');
  });
});
