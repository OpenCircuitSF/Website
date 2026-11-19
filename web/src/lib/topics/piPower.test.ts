import { describe, expect, it } from 'vitest';
import {
  CABLES,
  CAPTIVE_CABLE,
  STAGES,
  THROTTLE_FLAGS,
  cableOhms,
  cutPower,
  decodeThrottled,
  hex,
  parseThrottled,
  piStatus,
  sag,
  stageAt,
  type CableId,
  type SagInput,
} from './piPower';

const cable = (id: CableId) => CABLES.find((c) => c.id === id)!;

function expectSag(i: SagInput, amps: number, supplyV: number, ohms: number, piV: number, status: string) {
  const s = sag(i);
  expect(s.amps).toBeCloseTo(amps, 2);
  expect(s.supplyV).toBeCloseTo(supplyV, 2);
  expect(cableOhms(s.cable)).toBeCloseTo(ohms, 2);
  expect(s.piV).toBeCloseTo(piV, 2);
  expect(s.status).toBe(status);
}

describe('voltage sag', () => {
  it('cable resistance counts both wires and the contacts', () => {
    expect(cableOhms(cable('thick'))).toBeCloseTo(0.096, 3);
    expect(cableOhms(cable('typical'))).toBeCloseTo(0.198, 3);
    expect(cableOhms(cable('thin'))).toBeCloseTo(0.882, 3);
    expect(cableOhms(CAPTIVE_CABLE)).toBeCloseTo(0.093, 3);
  });

  it('piStatus switches at 4.63 V and 4.75 V', () => {
    expect(piStatus(4.62)).toBe('under');
    expect(piStatus(4.63)).toBe('marginal');
    expect(piStatus(4.749)).toBe('marginal');
    expect(piStatus(4.75)).toBe('ok');
  });

  it('the default Pi 4 setup is close to the line', () => {
    expectSag({ board: 'pi4', supply: 'charger', cable: 'typical', load: 'busy' }, 1.2, 4.94, 0.198, 4.702, 'marginal');
  });

  it('a thin 2 m cable puts a busy Pi 4 under 4.63 V', () => {
    expectSag({ board: 'pi4', supply: 'charger', cable: 'thin', load: 'busy' }, 1.2, 4.94, 0.882, 3.882, 'under');
  });

  it('the official supply always uses its own cable', () => {
    for (const c of CABLES) {
      expectSag({ board: 'pi4', supply: 'official', cable: c.id, load: 'busy' }, 1.2, 5.076, 0.093, 4.964, 'ok');
      expect(sag({ board: 'pi4', supply: 'official', cable: c.id, load: 'busy' }).cable.id).toBe('captive');
    }
  });

  it('a supply past its rating sags hard', () => {
    expectSag({ board: 'pi4', supply: 'phone', cable: 'typical', load: 'busy' }, 1.2, 4.42, 0.198, 4.182, 'under');
  });

  it('a USB hard drive tips a 2.4 A charger under', () => {
    expectSag({ board: 'pi4', supply: 'charger', cable: 'typical', load: 'usbDrive' }, 1.7, 4.915, 0.198, 4.578, 'under');
  });

  it('a Zero 2W draws less: the thin cable is fine at idle, not when busy', () => {
    expectSag({ board: 'zero2w', supply: 'charger', cable: 'thin', load: 'idle' }, 0.15, 4.9925, 0.882, 4.86, 'ok');
    expectSag({ board: 'zero2w', supply: 'charger', cable: 'thin', load: 'busy' }, 0.45, 4.9775, 0.882, 4.581, 'under');
  });

  it('under-voltage reports 0x50005, close reports 0x50000, ok reports 0x0', () => {
    expect(sag({ board: 'pi4', supply: 'charger', cable: 'thin', load: 'busy' }).throttled).toBe(0x50005);
    expect(sag({ board: 'pi4', supply: 'charger', cable: 'typical', load: 'busy' }).throttled).toBe(0x50000);
    expect(sag({ board: 'pi4', supply: 'official', cable: 'typical', load: 'busy' }).throttled).toBe(0);
  });
});

describe('get_throttled decoder', () => {
  it('each documented bit decodes to its own flag', () => {
    expect(THROTTLE_FLAGS).toHaveLength(8);
    for (const f of THROTTLE_FLAGS) {
      const d = decodeThrottled(2 ** f.bit);
      expect(d.flags).toHaveLength(1);
      expect(d.flags[0].key).toBe(f.key);
      expect(d.flags[0].now).toBe(f.bit < 16);
      expect(d.unknownBits).toEqual([]);
    }
  });

  it('0x50005 is under-voltage and throttling, now and since boot', () => {
    const d = decodeThrottled(0x50005);
    expect(d.flags.map((f) => f.bit)).toEqual([0, 2, 16, 18]);
    expect(d.title).toBe('Power problem now');
    expect(d.tone).toBe('bad');
  });

  it('0x0 is all clear', () => {
    const d = decodeThrottled(0);
    expect(d.title).toBe('All clear');
    expect(d.tone).toBe('good');
    expect(d.flags).toEqual([]);
  });

  it('0x50000 is a power problem since boot only', () => {
    const d = decodeThrottled(0x50000);
    expect(d.title).toBe('Power problem since boot');
    expect(d.flags.map((f) => f.bit)).toEqual([16, 18]);
  });

  it('0x80008 is running hot, not power', () => {
    const d = decodeThrottled(0x80008);
    expect(d.title).toBe('Running hot');
    expect(d.flags.map((f) => f.key)).toEqual(['softTemp', 'softTemp']);
  });

  it('reports undocumented bits instead of ignoring them', () => {
    const d = decodeThrottled(0x10);
    expect(d.unknownBits).toEqual([4]);
    expect(d.title).toBe('No documented flags');
    expect(decodeThrottled(0x80000000).unknownBits).toEqual([31]);
  });

  it('parses throttled=0x50005, 0x50005, 50005 and upper case alike', () => {
    for (const s of ['throttled=0x50005', '0x50005', '50005', 'THROTTLED=0X50005', '  0x50005 \n']) {
      expect(parseThrottled(s)).toEqual({ ok: true, value: 0x50005 });
    }
    expect(parseThrottled('0xFFFFFFFF')).toEqual({ ok: true, value: 0xffffffff });
  });

  it('rejects empty, non-hex and over-long input with a message', () => {
    expect(parseThrottled('')).toEqual({ ok: false, error: 'Type a value, like 0x50005.' });
    expect(parseThrottled('  ')).toEqual({ ok: false, error: 'Type a value, like 0x50005.' });
    for (const s of ['zebra', '0x']) {
      const p = parseThrottled(s);
      expect(p.ok).toBe(false);
      if (!p.ok) expect(p.error).toContain('isn’t a hex number');
    }
    expect(parseThrottled('0x123456789')).toEqual({ ok: false, error: 'Too many digits: get_throttled is at most 8 hex digits.' });
  });

  it('hex() prints lower case with 0x', () => {
    expect(hex(0x50005)).toBe('0x50005');
    expect(hex(0)).toBe('0x0');
    expect(hex(0xabcdef)).toBe('0xabcdef');
  });
});

describe('sd card write timeline', () => {
  it('stages cover 0 to 100 with no gaps or overlaps', () => {
    expect(STAGES[0].from).toBe(0);
    expect(STAGES[STAGES.length - 1].to).toBe(100);
    for (let i = 1; i < STAGES.length; i++) expect(STAGES[i].from).toBe(STAGES[i - 1].to + 1);
    for (let t = 0; t <= 100; t++) expect(STAGES.filter((s) => t >= s.from && t <= s.to)).toHaveLength(1);
  });

  it('stageAt clamps and rounds', () => {
    expect(stageAt(-5).id).toBe('before');
    expect(stageAt(34.4).id).toBe('data');
    expect(stageAt(120).id).toBe('done');
  });

  it('cut before anything is written: card fine', () => {
    const r = cutPower(5, false);
    expect(r.title).toBe('Nothing written yet: card fine');
    expect(r.file).toBe('old');
    expect(r.card.others).toBe('ok');
  });

  it('cut mid-data or mid-journal: change lost, card fine', () => {
    for (const t of [20, 40]) {
      const r = cutPower(t, false);
      expect(r.title).toBe('Change lost, card fine');
      expect(r.file).toBe('old');
      expect(r.card.others).toBe('ok');
    }
    expect(cutPower(20, false).card.data).toBe('partial');
    expect(cutPower(40, false).card.journal).toBe('partial');
  });

  it('cut after the commit: the journal finishes the save', () => {
    for (const t of [57, 70]) {
      const r = cutPower(t, false);
      expect(r.title).toBe('Saved: the journal finishes it at boot');
      expect(r.file).toBe('new');
    }
    expect(cutPower(70, false).card.directory).toBe('partial');
  });

  it('cut during the flash rewrite: other files damaged', () => {
    const r = cutPower(90, false);
    expect(r.title).toBe('Corrupted: other files damaged');
    expect(r.tone).toBe('bad');
    expect(r.card.others).toBe('damaged');
    expect(cutPower(100, false).title).toBe('Saved');
  });

  it('with the overlay every cut leaves the card untouched', () => {
    for (let t = 0; t <= 100; t += 5) {
      const r = cutPower(t, true);
      expect(r.title).toBe('Card untouched: the save was in memory');
      expect(r.card).toEqual({ data: 'blank', journal: 'blank', directory: 'blank', others: 'ok', inRam: true });
      expect(r.file).toBe('old');
    }
  });
});
