import { describe, expect, it } from 'vitest';
import {
  FIND_NETWORKS,
  LAPTOP_OSES,
  NEEDS,
  PI_HEADER,
  PI_MODELS,
  FITS,
  findCommands,
  findPi,
  gpiozeroLine,
  pinByGpio,
  pinByPhysical,
  pinLabel,
  pinTitle,
  pinsWith,
  recommendBoard,
  type MethodId,
  type Verdict,
} from './piStart';

const v = (r: ReturnType<typeof findPi>) =>
  Object.fromEntries(r.methods.map((m) => [m.id, m.verdict])) as Record<MethodId, Verdict>;

describe('finding the pi', () => {
  it('home WiFi: every method works and mypi.local comes first', () => {
    const r = findPi('home', 'mac');
    expect(Object.values(v(r))).toEqual(['yes', 'yes', 'yes', 'yes']);
    expect(r.best).toBe('mdns');
    expect(r.tone).toBe('good');
    expect(r.title).toBe('Try mypi.local first');
  });

  it('guest network blocks every method on every laptop', () => {
    for (const os of LAPTOP_OSES) {
      const r = findPi('guest', os.id);
      expect(Object.values(v(r))).toEqual(['no', 'no', 'no', 'no']);
      expect(r.tone).toBe('bad');
      expect(r.title).toBe('You won’t find it on this network');
      expect(r.advice).toContain('phone hotspot');
    }
  });

  it('café WiFi: the Pi can’t get past the sign-in page', () => {
    const r = findPi('cafe', 'linux');
    expect(r.best).toBeNull();
    expect(r.methods[0].why).toContain('sign-in page');
  });

  it('office network: .local is blocked, a scan is a maybe', () => {
    const r = findPi('work', 'windows');
    expect(v(r).mdns).toBe('no');
    expect(v(r).scan).toBe('maybe');
    expect(r.tone).toBe('meh');
    expect(r.title).toBe('Maybe, but a hotspot is easier');
  });

  it('phone hotspot finds it by name', () => {
    const r = findPi('hotspot', 'mac');
    expect(r.best).toBe('mdns');
    expect(r.tone).toBe('good');
  });

  it('the laptop changes only the wording, never a verdict', () => {
    for (const n of FIND_NETWORKS) {
      const base = v(findPi(n.id, 'mac'));
      for (const os of LAPTOP_OSES) expect(v(findPi(n.id, os.id))).toEqual(base);
    }
  });

  it('windows explains .local support', () => {
    expect(findPi('home', 'windows').methods[0].why).toContain('Windows 10');
  });

  it('findCommands: ping -c on macOS and Linux, plain ping on Windows, then arp -a and ssh', () => {
    expect(findCommands('mac')).toEqual(['ping -c 3 mypi.local', 'arp -a', 'ssh maker@mypi.local']);
    expect(findCommands('linux')).toEqual(findCommands('mac'));
    expect(findCommands('windows')).toEqual(['ping mypi.local', 'arp -a', 'ssh maker@mypi.local']);
  });
});

describe('choosing a model', () => {
  it('PI_MODELS: only the Zero 2W is 2.4 GHz only with no Ethernet', () => {
    expect(PI_MODELS.filter((m) => m.specs.wifi === '2.4 GHz only').map((m) => m.id)).toEqual(['zero2w']);
    expect(PI_MODELS.filter((m) => m.specs.ethernet === 'None').map((m) => m.id)).toEqual(['zero2w']);
  });

  it('only the Pi 4 has a headphone jack', () => {
    expect(PI_MODELS.filter((m) => m.specs.audio !== 'None').map((m) => m.id)).toEqual(['pi4']);
  });

  it('every model has a fit with a reason for every need', () => {
    for (const m of PI_MODELS) for (const n of NEEDS) expect(FITS[m.id][n.id].why.length).toBeGreaterThan(10);
  });

  it('nothing ticked picks the Zero 2W', () => {
    const p = recommendBoard([]);
    expect(p.board).toBe('zero2w');
    expect(p.reasons[0]).toMatch(/^Nothing ticked yet/);
    expect(p.ruledOut).toEqual([]);
  });

  it('5 GHz WiFi rules out the Zero 2W and picks the Pi 4', () => {
    const p = recommendBoard(['wifi5']);
    expect(p.board).toBe('pi4');
    expect(p.ruledOut.map((r) => r.board)).toEqual(['zero2w']);
    expect(p.ruledOut[0].why).toContain('2.4 GHz');
  });

  it('two displays, Ethernet, an LED panel or a desktop pick the Pi 4', () => {
    for (const n of ['twoDisplays', 'ethernet', 'ledPanel', 'desktop'] as const) expect(recommendBoard([n]).board).toBe('pi4');
  });

  it('battery and small pick the Zero 2W with nothing given up', () => {
    const p = recommendBoard(['battery', 'small']);
    expect(p.board).toBe('zero2w');
    expect(p.compromises).toEqual([]);
    expect(recommendBoard(['dac']).board).toBe('zero2w');
  });

  it('heavy computing picks the Pi 5', () => {
    const p = recommendBoard(['fast']);
    expect(p.board).toBe('pi5');
    expect(p.ruledOut.map((r) => r.board)).toEqual(['zero2w']);
    expect(recommendBoard(['wifi5', 'fast']).board).toBe('pi5');
  });

  it('ties go to the smaller board', () => {
    const p = recommendBoard(['battery', 'ethernet']);
    expect(p.board).toBe('zero2w');
    expect(p.compromises).toHaveLength(1);
    expect(p.compromises[0]).toContain('USB adapter');
  });

  it('everything ticked picks the Pi 4 with three compromises', () => {
    const p = recommendBoard(NEEDS.map((n) => n.id));
    expect(p.board).toBe('pi4');
    expect(p.compromises).toEqual([FITS.pi4.battery.why, FITS.pi4.small.why, FITS.pi4.fast.why]);
  });
});

describe('gpio header', () => {
  it('40 pins in physical order', () => {
    expect(PI_HEADER.map((p) => p.physical)).toEqual(Array.from({ length: 40 }, (_, i) => i + 1));
  });

  it('power and ground pins match Raspberry Pi’s documentation', () => {
    const of = (k: string) => PI_HEADER.filter((p) => p.kind === k).map((p) => p.physical);
    expect(of('power5')).toEqual([2, 4]);
    expect(of('power33')).toEqual([1, 17]);
    expect(of('ground')).toEqual([6, 9, 14, 20, 25, 30, 34, 39]);
  });

  it('GPIO 0 to 27 each appear exactly once', () => {
    const gs = PI_HEADER.filter((p) => p.gpio !== null).map((p) => p.gpio!).sort((a, b) => a - b);
    expect(gs).toEqual(Array.from({ length: 28 }, (_, i) => i));
  });

  it('pin 11 is GPIO 17 and pin 3 is GPIO 2', () => {
    expect(pinByPhysical(11).gpio).toBe(17);
    expect(pinByPhysical(3).gpio).toBe(2);
    expect(pinByGpio(17).physical).toBe(11);
  });

  it('pinsWith finds I2C, SPI, UART, PWM, I2S and HAT ID pins', () => {
    expect(pinsWith('I2C')).toEqual([3, 5]);
    expect(pinsWith('SPI')).toEqual([19, 21, 23, 24, 26]);
    expect(pinsWith('UART')).toEqual([8, 10]);
    expect(pinsWith('PWM')).toEqual([12, 32, 33, 35]);
    expect(pinsWith('I2S')).toEqual([12, 35, 38, 40]);
    expect(pinsWith('HAT ID')).toEqual([27, 28]);
  });

  it('pinLabel numbers every pin in the physical scheme and names them in the GPIO scheme', () => {
    for (const p of PI_HEADER) expect(pinLabel(p, 'physical')).toBe(String(p.physical));
    expect(pinLabel(pinByPhysical(11), 'gpio')).toBe('GPIO17');
    expect(pinLabel(pinByPhysical(2), 'gpio')).toBe('5V');
    expect(pinLabel(pinByPhysical(1), 'gpio')).toBe('3V3');
    expect(pinLabel(pinByPhysical(6), 'gpio')).toBe('GND');
  });

  it('pinTitle reads Pin 11: GPIO 17, Pin 2: 5 V power, Pin 6: Ground', () => {
    expect(pinTitle(pinByPhysical(11))).toBe('Pin 11: GPIO 17');
    expect(pinTitle(pinByPhysical(2))).toBe('Pin 2: 5 V power');
    expect(pinTitle(pinByPhysical(1))).toBe('Pin 1: 3.3 V power');
    expect(pinTitle(pinByPhysical(6))).toBe('Pin 6: Ground');
  });

  it('only the 5 V pins carry the warning', () => {
    expect(PI_HEADER.filter((p) => p.warning).map((p) => p.physical)).toEqual([2, 4]);
  });

  it('every GPIO pin states 3.3 V, 16 mA and 50 mA', () => {
    for (const p of PI_HEADER.filter((x) => x.kind === 'gpio')) {
      const t = p.facts.join(' ');
      expect(t).toContain('3.3 V');
      expect(t).toContain('16 mA');
      expect(t).toContain('50 mA');
    }
  });

  it('gpiozeroLine gives LED(17) for pin 11 and null for power', () => {
    expect(gpiozeroLine(pinByPhysical(11))).toBe('led = LED(17)  # GPIO 17 is physical pin 11');
    expect(gpiozeroLine(pinByPhysical(2))).toBeNull();
  });

  it('pinByPhysical and pinByGpio throw on pins that do not exist', () => {
    expect(() => pinByPhysical(0)).toThrow();
    expect(() => pinByPhysical(41)).toThrow();
    expect(() => pinByGpio(28)).toThrow();
  });
});
