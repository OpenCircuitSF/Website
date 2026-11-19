import { describe, expect, it } from 'vitest';
import {
  ALL_ON,
  DISKS,
  SCENARIOS,
  UNIT_TOGGLES,
  WIFI_FIXES,
  WIFI_STEPS,
  ddPlan,
  isRefused,
  isWifiFix,
  serviceOutcome,
  unitText,
  wifiNext,
  wifiSpot,
  type HostOs,
  type UnitOptions,
} from './piSoftware';

describe('wifi doctor', () => {
  const walk = (answers: ('yes' | 'no')[]) => answers.reduce((at, a) => wifiNext(at, a), 'start');

  it('every answer leads to a known step or fix', () => {
    for (const s of Object.values(WIFI_STEPS)) {
      for (const next of [s.yes, s.no]) expect(next in WIFI_STEPS || isWifiFix(next)).toBe(true);
    }
  });

  it('every step and fix is reachable from the start', () => {
    const seen = new Set<string>();
    const queue = ['start'];
    while (queue.length) {
      const id = queue.pop()!;
      if (seen.has(id)) continue;
      seen.add(id);
      if (!isWifiFix(id)) queue.push(WIFI_STEPS[id].yes, WIFI_STEPS[id].no);
    }
    for (const id of [...Object.keys(WIFI_STEPS), ...Object.keys(WIFI_FIXES)]) expect(seen.has(id)).toBe(true);
  });

  it('an under-voltage flag ends at fixing the power', () => {
    expect(walk(['yes'])).toBe('fix-power');
  });

  it('clean power and power save on ends at turning it off', () => {
    const id = walk(['no', 'yes']);
    expect(id).toBe('fix-powersave');
    expect(WIFI_FIXES[id].commands.join('\n')).toContain('802-11-wireless.powersave 2');
  });

  it('each later yes reaches its own fix', () => {
    expect(walk(['no', 'no', 'yes'])).toBe('fix-address');
    expect(walk(['no', 'no', 'no', 'yes'])).toBe('fix-signal');
    expect(walk(['no', 'no', 'no', 'no', 'yes'])).toBe('fix-steering');
    expect(walk(['no', 'no', 'no', 'no', 'no'])).toBe('fix-log');
  });

  it('every step and fix has a spot', () => {
    for (const id of [...Object.keys(WIFI_STEPS), ...Object.keys(WIFI_FIXES)]) expect(wifiSpot(id)).toBeTruthy();
    expect(wifiSpot('fix-power')).toBe('supply');
    expect(wifiSpot('fix-signal')).toBe('air');
  });

  it('links point at guides', () => {
    for (const f of Object.values(WIFI_FIXES)) for (const l of f.links) expect(l.href).toMatch(/^\/guides\/[a-z0-9-]+$/);
  });

  it('throws on an unknown step', () => {
    expect(() => wifiNext('nope', 'yes')).toThrow('wifi doctor: no step nope');
    expect(() => wifiSpot('nope')).toThrow();
  });
});

describe('systemd service', () => {
  const off = (...t: (keyof UnitOptions)[]): UnitOptions => {
    const o = { ...ALL_ON };
    for (const k of t) o[k] = false;
    return o;
  };

  it('all lines on: the file has every directive', () => {
    const t = unitText(ALL_ON);
    for (const s of [
      'ExecStart=/usr/bin/python3 /home/maker/plants/log.py',
      'After=network-online.target',
      'Wants=network-online.target',
      'Restart=on-failure',
      'RestartSec=5',
      'WantedBy=multi-user.target',
    ])
      expect(t).toContain(s);
    expect(t.split('\n').some((l) => l.startsWith('# '))).toBe(false);
  });

  it('a line turned off becomes a comment', () => {
    expect(unitText(off('restart'))).toContain('# Restart=on-failure');
    const n = unitText(off('network'));
    expect(n).toContain('# After=network-online.target');
    expect(n).toContain('# Wants=network-online.target');
  });

  it('without full paths ExecStart is relative', () => {
    expect(unitText(off('fullPath'))).toContain('ExecStart=python3 log.py');
  });

  it('all on: running at boot, back 5 s after a crash', () => {
    expect(serviceOutcome(ALL_ON, 'boot').id).toBe('running');
    expect(serviceOutcome(ALL_ON, 'crash').id).toBe('crashOk');
  });

  it('no [Install] never starts at boot', () => {
    const r = serviceOutcome(off('install'), 'boot');
    expect(r.id).toBe('noInstall');
    expect(r.log.join('\n')).toContain('no installation config');
  });

  it('a relative path with no WorkingDirectory can’t find its file', () => {
    for (const s of ['boot', 'crash'] as const) {
      const r = serviceOutcome(off('fullPath', 'workdir'), s);
      expect(r.id).toBe('badPath');
      expect(r.log.join('\n')).toContain('/log.py');
      expect(r.status).toContain('activating (auto-restart)');
      expect(serviceOutcome(off('fullPath', 'workdir', 'restart'), s).status).toContain('failed');
    }
  });

  it('a relative path with WorkingDirectory still works', () => {
    expect(serviceOutcome(off('fullPath'), 'boot').id).toBe('running');
  });

  it('no network wait: the restart saves it, without restart it stays dead', () => {
    expect(serviceOutcome(off('network'), 'boot').id).toBe('netRetry');
    expect(serviceOutcome(off('network', 'restart'), 'boot').id).toBe('netDead');
  });

  it('no network wait and no RestartSec: retries hit the start limit before the WiFi is up', () => {
    const r = serviceOutcome(off('network', 'restartSec'), 'boot');
    expect(r.id).toBe('netGaveUp');
    expect(r.status).toContain('start-limit-hit');
    const b = serviceOutcome(off('fullPath', 'workdir', 'restartSec'), 'boot');
    expect(b.id).toBe('badPath');
    expect(b.status).toContain('failed');
  });

  it('no Restart= stays dead after a crash', () => {
    expect(serviceOutcome(off('restart'), 'crash').id).toBe('crashDead');
  });

  it('no RestartSec restarts at once', () => {
    const r = serviceOutcome(off('restartSec'), 'crash');
    expect(r.id).toBe('crashFast');
    expect(r.tone).toBe('meh');
  });

  it('no User= runs as root', () => {
    const r = serviceOutcome(off('user'), 'boot');
    expect(r.id).toBe('root');
    expect(r.tone).toBe('meh');
  });

  it('timeline events are in order and within 0 to 100', () => {
    const ids = UNIT_TOGGLES.map((t) => t.id);
    for (let mask = 0; mask < 1 << ids.length; mask++) {
      const o = { ...ALL_ON };
      ids.forEach((id, i) => (o[id] = (mask & (1 << i)) !== 0));
      for (const s of SCENARIOS) {
        const ev = serviceOutcome(o, s.id).events;
        expect(ev.length).toBeGreaterThan(0);
        for (let i = 0; i < ev.length; i++) {
          expect(ev[i].at).toBeGreaterThanOrEqual(0);
          expect(ev[i].at).toBeLessThanOrEqual(100);
          if (i) expect(ev[i].at).toBeGreaterThan(ev[i - 1].at);
        }
      }
    }
  });
});

describe('sd card backup', () => {
  it('card to file is a backup with a warning', () => {
    const m = ddPlan('mac', 'disk4', 'file');
    expect(m.verdict).toBe('backup');
    expect(m.command).toBe('sudo dd if=/dev/rdisk4 of=pi.img bs=4m status=progress');
    expect(m.writesDisk).toBe(false);
    expect(m.warning?.strong).toBe('Check the disk number twice.');
    expect(m.before).toEqual(['diskutil list', 'diskutil unmountDisk /dev/disk4']);
    expect(ddPlan('linux', 'sdb', 'file').command).toBe('sudo dd if=/dev/sdb of=pi.img bs=4M status=progress conv=fsync');
  });

  it('file to card is a restore that warns it erases the card', () => {
    const m = ddPlan('mac', 'file', 'disk4');
    expect(m.verdict).toBe('restore');
    expect(m.command).toContain('of=/dev/rdisk4');
    expect(m.writesDisk).toBe(true);
    expect(m.warning?.strong).toContain('/dev/disk4');
    expect(m.warning?.strong).toContain('31.9 GB');
  });

  it('the computer’s own disk is never a destination', () => {
    for (const [os, src, dst] of [
      ['mac', 'disk4', 'disk0'],
      ['mac', 'file', 'disk0'],
      ['linux', 'sdb', 'nvme0n1'],
    ] as const) {
      const p = ddPlan(os, src, dst);
      expect(p.verdict).toBe('eraseOwnDisk');
      expect(p.command).toBeNull();
    }
  });

  it('the backup drive is never a destination', () => {
    expect(ddPlan('mac', 'disk4', 'disk6').verdict).toBe('eraseOtherDrive');
    expect(ddPlan('linux', 'sdb', 'sda').verdict).toBe('eraseOtherDrive');
  });

  it('a disk onto the card is refused', () => {
    expect(ddPlan('mac', 'disk0', 'disk4').verdict).toBe('diskOverCard');
  });

  it('copying the wrong disk to a file is caught', () => {
    const p = ddPlan('mac', 'disk0', 'file');
    expect(p.verdict).toBe('notTheCard');
    expect(p.body).toContain('31.9 GB');
  });

  it('same source and destination', () => {
    expect(ddPlan('mac', 'file', 'file').verdict).toBe('same');
    expect(ddPlan('linux', 'sdb', 'sdb').verdict).toBe('same');
  });

  it('wrong-disk guard holds for every pair', () => {
    for (const os of ['mac', 'linux'] as HostOs[]) {
      const ends = [...DISKS[os].map((d) => d.id), 'file'];
      const card = DISKS[os].find((d) => d.kind === 'sdcard')!;
      const forbidden = DISKS[os].filter((d) => d.kind !== 'sdcard').flatMap((d) => [d.dev, d.rawDev]);
      for (const src of ends)
        for (const dst of ends) {
          const p = ddPlan(os, src, dst);
          if (p.command !== null) {
            expect(p.warning).not.toBeNull();
            const of = / of=(\S+)/.exec(p.command)![1];
            expect(forbidden).not.toContain(of);
            if (p.writesDisk) expect([card.dev, card.rawDev]).toContain(of);
          }
        }
    }
  });

  it('refused plans show no commands', () => {
    for (const os of ['mac', 'linux'] as HostOs[]) {
      const ends = [...DISKS[os].map((d) => d.id), 'file'];
      for (const src of ends)
        for (const dst of ends) {
          const p = ddPlan(os, src, dst);
          if (isRefused(p)) {
            expect(p.before).toEqual([]);
            expect(p.after).toEqual([]);
            expect(p.warning).toBeNull();
          }
        }
    }
  });
});
