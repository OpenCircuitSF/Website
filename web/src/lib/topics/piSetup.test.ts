import { describe, expect, it } from 'vitest';
import {
  BOOKWORM_STEPS,
  DEFAULT_CHOICE,
  PI_BOARDS,
  ROUTERS,
  firstBoot,
  hostFor,
  isValidHostname,
  joinOutcome,
  stepById,
  type ImagerChoice,
  type SettingId,
  type StepId,
} from './piSetup';

function choice(off: SettingId[] = [], extra: Partial<ImagerChoice> = {}): ImagerChoice {
  const on = { ...DEFAULT_CHOICE.on };
  for (const id of off) on[id] = false;
  return { ...DEFAULT_CHOICE, ...extra, on };
}

describe('imager first boot', () => {
  it('all settings on: ready to log in over SSH with a password prompt', () => {
    const b = firstBoot(choice());
    expect(b.tone).toBe('good');
    expect(b.joins && b.canLogIn).toBe(true);
    expect(b.title).toBe('Ready: log in over SSH');
    expect(b.problems).toEqual([]);
    expect(b.terminal).toEqual(['$ ssh maker@mypi.local', "maker@mypi.local's password:"]);
  });

  it('public key login skips the password prompt', () => {
    const b = firstBoot(choice([], { sshAuth: 'key' }));
    expect(b.terminal).toEqual(['$ ssh maker@mypi.local', 'maker@mypi:~ $']);
  });

  it('wifi off never joins, whatever else is set', () => {
    for (const off of [[], ['ssh'], ['user', 'ssh'], ['hostname', 'locale']] as SettingId[][]) {
      const b = firstBoot(choice(['wifi', ...off]));
      expect(b.joins).toBe(false);
      expect(b.tone).toBe('bad');
      expect(b.title).toBe('Never joins the network');
    }
    expect(firstBoot(choice(['wifi'])).terminal[1]).toContain('Unknown host');
  });

  it('ssh off joins but the connection is refused', () => {
    const b = firstBoot(choice(['ssh']));
    expect(b.joins).toBe(true);
    expect(b.canLogIn).toBe(false);
    expect(b.title).toBe('On the network, but it refuses connections');
    expect(b.terminal.at(-1)).toContain('Connection refused');
  });

  it('no user joins but nobody can log in', () => {
    const b = firstBoot(choice(['user']));
    expect(b.joins).toBe(true);
    expect(b.canLogIn).toBe(false);
    expect(b.title).toBe('On the network, but there is no one to log in as');
    expect(b.terminal.at(-1)).toBe('(no account to log in with)');
  });

  it('problems lists every setting that is off, in order', () => {
    const b = firstBoot(choice(['ssh', 'hostname', 'wifi', 'locale', 'user']));
    expect(b.problems.map((p) => p.split(':')[0])).toEqual(['No hostname', 'No user', 'No WiFi details', 'No locale', 'No SSH']);
  });

  it('hostname off answers to raspberrypi.local and notes the clash', () => {
    const c = choice(['hostname']);
    expect(hostFor(c)).toBe('raspberrypi.local');
    const b = firstBoot(c);
    expect(b.tone).toBe('meh');
    expect(b.problems[0]).toContain('clash');
    expect(b.terminal[0]).toBe('$ ssh maker@raspberrypi.local');
    expect(hostFor(choice([], { hostname: '-bad' }))).toBe('raspberrypi.local');
  });

  it('isValidHostname accepts mypi and pi-4, rejects -pi, pi-, empty, 64 characters and spaces', () => {
    expect(isValidHostname('mypi')).toBe(true);
    expect(isValidHostname('pi-4')).toBe(true);
    expect(isValidHostname('a'.repeat(63))).toBe(true);
    for (const bad of ['-pi', 'pi-', '', 'a'.repeat(64), 'my pi']) expect(isValidHostname(bad)).toBe(false);
  });
});

describe('wifi bands', () => {
  it('PI_BOARDS bands: Zero W, Zero 2 W, 3 B are 2.4 only; 3 B+, 4, 5 do both', () => {
    const m = Object.fromEntries(PI_BOARDS.map((b) => [b.id, b.bands]));
    expect(m).toEqual({
      zero2w: ['2.4'],
      zerow: ['2.4'],
      pi3b: ['2.4'],
      pi3bplus: ['2.4', '5'],
      pi4: ['2.4', '5'],
      pi5: ['2.4', '5'],
    });
  });

  it('zero 2 W on 5 GHz only never appears', () => {
    const o = joinOutcome('zero2w', 'only-5');
    expect(o.joins).toBe(false);
    expect(o.title).toBe('Never appears');
    expect(o.reason).toContain('Zero 2 W only has 2.4 GHz');
  });

  it('pi 4 on 5 GHz only joins on 5 GHz with freq 5180', () => {
    const o = joinOutcome('pi4', 'only-5');
    expect(o.joins).toBe(true);
    expect(o.band).toBe('5');
    expect(o.check).toContain('\tfreq: 5180');
  });

  it('split network: zero 2 W joins Home on 2.4, not Home-5G', () => {
    const a = joinOutcome('zero2w', 'split', 'Home');
    expect(a.joins).toBe(true);
    expect(a.band).toBe('2.4');
    expect(a.check).toContain('\tfreq: 2437');
    const b = joinOutcome('zero2w', 'split', 'Home-5G');
    expect(b.joins).toBe(false);
    expect(b.reason).toContain('Home-5G');
  });

  it('one name for both bands: zero 2 W joins on 2.4, pi 4 on 5 with a band-steering note', () => {
    const z = joinOutcome('zero2w', 'one-name');
    expect(z.tone).toBe('good');
    expect(z.band).toBe('2.4');
    const p = joinOutcome('pi4', 'one-name');
    expect(p.tone).toBe('meh');
    expect(p.band).toBe('5');
    expect(p.title).toBe('Joins, usually on 5 GHz');
    expect(p.reason).toContain('Band steering');
  });

  it('hotspot works for every board', () => {
    for (const b of PI_BOARDS) {
      const o = joinOutcome(b.id, 'hotspot');
      expect(o.joins).toBe(true);
      expect(o.tone).toBe('good');
    }
  });

  it('check lines are empty when it does not join', () => {
    expect(joinOutcome('zerow', 'only-5').check).toEqual([]);
    expect(ROUTERS.map((r) => r.id)).toEqual(['one-name', 'split', 'only-24', 'only-5', 'hotspot']);
  });
});

describe('bookworm steps', () => {
  it('every step has old, now, fix, why and both boxes', () => {
    expect(BOOKWORM_STEPS.map((s) => s.id)).toEqual(['boot-files', 'wifi', 'static-ip', 'pip', 'camera', 'desktop']);
    for (const s of BOOKWORM_STEPS) {
      expect(s.label).not.toBe('');
      expect(s.oldCmd.length).toBeGreaterThan(0);
      expect(s.now.length).toBeGreaterThan(0);
      expect(s.fix.length).toBeGreaterThan(0);
      expect(s.why).not.toBe('');
      expect(s.oldBox).not.toBe('');
      expect(s.newBox).not.toBe('');
    }
  });

  it('config.txt moves to /boot/firmware', () => {
    const s = stepById('boot-files');
    expect(s.oldCmd[0]).toContain('/boot/config.txt');
    expect(s.fix[0]).toBe('sudo nano /boot/firmware/config.txt');
    expect(s.newBox).toBe('/boot/firmware/config.txt');
  });

  it('pip shows externally-managed-environment and a venv fix', () => {
    const s = stepById('pip');
    expect(s.now.join('\n')).toContain('externally-managed-environment');
    expect(s.fix.join('\n')).toContain('python3 -m venv');
  });

  it('stepById throws on an unknown id', () => {
    expect(() => stepById('nope' as StepId)).toThrow();
  });
});
