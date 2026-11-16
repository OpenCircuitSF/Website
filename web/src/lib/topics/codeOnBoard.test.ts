import { describe, expect, it } from 'vitest';
import {
  DEFAULT_INI,
  SETUP_LINE,
  TROUBLE_FIXES,
  TROUBLE_STEPS,
  buildIni,
  cableUpload,
  checkIni,
  initialButtons,
  initialOta,
  isFix,
  modeLabel,
  otaUpload,
  portPresent,
  runSerial,
  toggleBoot,
  toggleReset,
  troubleNext,
  upload,
  type OtaOptions,
} from './codeOnBoard';

describe('BOOT and RESET', () => {
  it('hold BOOT, tap RST, release BOOT enters download mode', () => {
    let s = initialButtons('sleep');
    expect(portPresent(s)).toBe(false);
    s = toggleBoot(s);
    expect(s.mode).toBe('running');
    s = toggleReset(s);
    expect(s.mode).toBe('reset');
    expect(portPresent(s)).toBe(false);
    s = toggleReset(s);
    expect(s.mode).toBe('download');
    s = toggleBoot(s);
    expect(s.mode).toBe('download');
    expect(portPresent(s)).toBe(true);
    expect(modeLabel(s)).toBe('Download mode');
  });

  it('RST alone restarts the same program', () => {
    const s = toggleReset(toggleReset(initialButtons('crash')));
    expect(s.mode).toBe('running');
    expect(s.sketch).toBe('crash');
    expect(portPresent(s)).toBe(false);
  });

  it('upload in download mode waits for RST before running', () => {
    let s = toggleBoot(toggleReset(toggleReset(toggleBoot(initialButtons('crash')))));
    const r = upload(s);
    expect(r.ok).toBe(true);
    expect(r.state.pendingNew).toBe(true);
    expect(r.state.mode).toBe('download');
    s = toggleReset(toggleReset(r.state));
    expect(s.mode).toBe('running');
    expect(s.sketch).toBe('ok');
    expect(portPresent(s)).toBe(true);
  });

  it('upload fails without a port and works on a healthy board', () => {
    expect(upload(initialButtons('sleep')).ok).toBe(false);
    expect(upload(initialButtons('crash')).ok).toBe(false);
    expect(upload(toggleReset(initialButtons('ok'))).ok).toBe(false);
    expect(upload(initialButtons('ok')).ok).toBe(true);
  });

  it('uploading with BOOT still held comes back up in download mode', () => {
    const r = upload(toggleBoot(initialButtons('ok')));
    expect(r.ok).toBe(true);
    expect(r.state.mode).toBe('download');
    const run = toggleReset(toggleReset(toggleBoot(r.state)));
    expect(run.mode).toBe('running');
    expect(run.sketch).toBe('ok');
  });

  it('keeps the log short', () => {
    let s = initialButtons();
    for (let i = 0; i < 20; i++) s = toggleBoot(s);
    expect(s.log.length).toBeLessThanOrEqual(6);
  });
});

describe('upload troubleshooter', () => {
  it('every answer leads to a known step or fix', () => {
    for (const step of Object.values(TROUBLE_STEPS)) {
      for (const a of ['yes', 'no'] as const) {
        const n = troubleNext(step.id, a);
        expect(n in TROUBLE_STEPS || n in TROUBLE_FIXES, `${step.id}/${a} -> ${n}`).toBe(true);
      }
    }
  });

  it('every step and fix is reachable from the start', () => {
    const seen = new Set<string>();
    const walk = (id: string) => {
      if (seen.has(id)) return;
      seen.add(id);
      if (isFix(id)) return;
      walk(troubleNext(id, 'yes'));
      walk(troubleNext(id, 'no'));
    };
    walk('start');
    for (const id of Object.keys(TROUBLE_FIXES)) expect(seen.has(id), id).toBe(true);
    for (const id of Object.keys(TROUBLE_STEPS)) expect(seen.has(id), id).toBe(true);
  });

  it('no port with an untested cable says swap the cable', () => {
    expect(troubleNext(troubleNext('start', 'no'), 'no')).toBe('fix-cable');
  });

  it('a port plus an open monitor says close the monitor', () => {
    expect(troubleNext(troubleNext(troubleNext('start', 'yes'), 'yes'), 'yes')).toBe('fix-monitor');
  });

  it('throws on an unknown step', () => {
    expect(() => troubleNext('nope', 'yes')).toThrow();
  });
});

describe('serial output', () => {
  it('without the flags everything goes to pin 21', () => {
    const r = runSerial({ usbFlags: false, waitForMonitor: true });
    expect(r.usb).toEqual([]);
    expect(r.txPin).toContain(SETUP_LINE);
  });
  it('without waiting, the setup line is lost', () => {
    const r = runSerial({ usbFlags: true, waitForMonitor: false });
    expect(r.usb).not.toContain(SETUP_LINE);
    expect(r.usb.length).toBeGreaterThan(0);
  });
  it('flags plus waiting shows everything', () => {
    expect(runSerial({ usbFlags: true, waitForMonitor: true }).usb[0]).toBe(SETUP_LINE);
  });
});

describe('platformio.ini', () => {
  it('the default config is clean and complete', () => {
    expect(checkIni(DEFAULT_INI)).toEqual([]);
    const text = buildIni(DEFAULT_INI)
      .map((l) => l.text)
      .join('\n');
    expect(text).toContain('[env:supermini]');
    expect(text).toContain('board = esp32-c3-devkitm-1');
    expect(text).toContain('-D ARDUINO_USB_CDC_ON_BOOT=1');
    expect(text).toContain('-D ARDUINO_USB_MODE=1');
    expect(text).toContain('fastled/FastLED');
  });
  it('flags the wrong chip and the wrong framework', () => {
    expect(checkIni({ ...DEFAULT_INI, board: 'esp32dev' })[0].text).toContain('This chip is ESP32-C3 not ESP32');
    expect(checkIni({ ...DEFAULT_INI, board: 'uno' })[0].text).toContain('Unknown board ID');
    expect(checkIni({ ...DEFAULT_INI, framework: 'espidf' })[0].key).toBe('framework');
    expect(checkIni({ ...DEFAULT_INI, platform: 'atmelavr' })[0].key).toBe('platform');
    expect(checkIni({ ...DEFAULT_INI, env: 'my board' })[0].key).toBe('env');
    // Under atmelavr the esp32dev upload message would be wrong; only the platform is flagged.
    expect(checkIni({ ...DEFAULT_INI, platform: 'atmelavr', board: 'esp32dev' }).map((p) => p.key)).toEqual(['platform']);
  });
  it('missing USB flags and an odd monitor speed are warnings', () => {
    const p = checkIni({ ...DEFAULT_INI, usbFlags: false, monitorSpeed: '9600' });
    expect(p.map((x) => x.severity)).toEqual(['warning', 'warning']);
    expect(buildIni({ ...DEFAULT_INI, usbFlags: false, fastled: false }).map((l) => l.key)).not.toContain('build_flags');
  });
});

describe('OTA', () => {
  const good: OtaOptions = { sameNetwork: true, passwordMatches: true, dropout: false, newListens: true };

  it('the first upload must be by cable', () => {
    expect(otaUpload(initialOta(), good).ok).toBe(false);
    const c = cableUpload(initialOta());
    expect(c.state.slots.app0.version).toBe(1);
    expect(c.state.active).toBe('app0');
  });

  it('an update lands in the other slot and becomes active', () => {
    let s = cableUpload(initialOta()).state;
    const r = otaUpload(s, good);
    expect(r.ok).toBe(true);
    expect(r.state.active).toBe('app1');
    expect(r.state.slots.app1.version).toBe(2);
    expect(r.state.slots.app0.version).toBe(1);
    s = otaUpload(r.state, good).state;
    expect(s.active).toBe('app0');
    expect(s.slots.app0.version).toBe(3);
  });

  it('a dropped update leaves the old version running', () => {
    const s = cableUpload(initialOta()).state;
    const r = otaUpload(s, { ...good, dropout: true });
    expect(r.ok).toBe(false);
    expect(r.state.active).toBe('app0');
    expect(r.state.slots.app1.version).toBeNull();
  });

  it('wrong network or password fails before writing', () => {
    const s = cableUpload(initialOta()).state;
    expect(otaUpload(s, { ...good, sameNetwork: false }).state.slots.app1.version).toBeNull();
    expect(otaUpload(s, { ...good, passwordMatches: false }).message).toContain('Authentication failed');
  });

  it('firmware that never calls handle() needs the cable again', () => {
    let s = otaUpload(cableUpload(initialOta()).state, { ...good, newListens: false }).state;
    const r = otaUpload(s, good);
    expect(r.ok).toBe(false);
    expect(r.message).toContain('Plug in the cable');
    s = cableUpload(s).state;
    expect(otaUpload(s, good).ok).toBe(true);
  });
});
