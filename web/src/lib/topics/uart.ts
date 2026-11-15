// A small UART simulation for the baud-rate guide: encode text into the
// line levels a transmitter puts on the wire, then decode it the way a
// receiver does -- wait for a falling edge (start bit), then sample the
// middle of each bit at ITS OWN idea of the bit time. When the two baud
// rates disagree, the samples land on the wrong bits and real garbage
// comes out, exactly the symptom students see in the serial monitor.
// Pure functions, no DOM.

/** One bit on the wire: line level and how long it lasts, in microseconds. */
export interface Segment {
  level: 0 | 1;
  start: number;
  end: number;
}

/** 8N1 framing: start bit (0), 8 data bits LSB first, stop bit (1). */
export function encode(text: string, baud: number, idleBits = 2): Segment[] {
  const bit = 1e6 / baud;
  const bits: (0 | 1)[] = Array(idleBits).fill(1);
  for (const ch of text) {
    const code = ch.charCodeAt(0) & 0xff;
    bits.push(0);
    for (let i = 0; i < 8; i++) bits.push(((code >> i) & 1) as 0 | 1);
    bits.push(1);
  }
  for (let i = 0; i < idleBits; i++) bits.push(1);
  return bits.map((level, i) => ({ level, start: i * bit, end: (i + 1) * bit }));
}

export function levelAt(wire: readonly Segment[], t: number): 0 | 1 {
  for (const s of wire) if (t >= s.start && t < s.end) return s.level;
  return 1;
}

export interface Sample {
  t: number;
  level: 0 | 1;
  kind: 'start' | 'data' | 'stop';
}

export interface Decoded {
  text: string;
  bytes: number[];
  samples: Sample[];
  /** Frames whose stop bit read 0: a receiver reports these as errors. */
  framingErrors: number;
}

/** Decode the wire as a receiver set to `baud` would. */
export function decode(wire: readonly Segment[], baud: number): Decoded {
  const bit = 1e6 / baud;
  const end = wire.length ? wire[wire.length - 1].end : 0;
  const step = Math.min(bit, 1e6 / 115200) / 8;
  const samples: Sample[] = [];
  const bytes: number[] = [];
  let framingErrors = 0;
  let t = 0;
  let prev = 1;
  while (t < end) {
    const level = levelAt(wire, t);
    if (prev === 1 && level === 0) {
      // Falling edge: start bit. Check its middle, then each data bit's.
      const mid = t + bit / 2;
      if (levelAt(wire, mid) !== 0) {
        prev = level;
        t += step;
        continue;
      }
      samples.push({ t: mid, level: 0, kind: 'start' });
      let byte = 0;
      for (let i = 0; i < 8; i++) {
        const st = mid + (i + 1) * bit;
        const b = levelAt(wire, st);
        samples.push({ t: st, level: b, kind: 'data' });
        byte |= b << i;
      }
      const stopT = mid + 9 * bit;
      const stop = levelAt(wire, stopT);
      samples.push({ t: stopT, level: stop, kind: 'stop' });
      if (stop === 0) framingErrors++;
      bytes.push(byte);
      t = stopT;
      prev = stop;
      continue;
    }
    prev = level;
    t += step;
  }
  return { text: bytes.map(printable).join(''), bytes, samples, framingErrors };
}

/** What a serial monitor shows for a byte: ASCII as-is, anything else as a
 * Latin-1 glyph (the familiar ­¾ýÿ¿þ garbage), control bytes as a dot. */
export function printable(b: number): string {
  if (b === 10 || b === 13) return '↵';
  if (b >= 32 && b < 127) return String.fromCharCode(b);
  if (b >= 160) return String.fromCharCode(b);
  return '·';
}
