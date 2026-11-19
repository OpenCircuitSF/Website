// Raspberry Pi guides, batch 2 (#0723): finding a headless Pi on the
// network, choosing between the Zero 2W, Pi 4 and Pi 5, and the 40-pin GPIO
// header. The demos are thin; every outcome they show is computed here so it
// can be unit-tested without a DOM.
//
// The facts follow Raspberry Pi's own documentation
// (raspberrypi.com/documentation: "Raspberry Pi hardware" / GPIO,
// "Remote access") and the Zero 2 W, Pi 4 and Pi 5 product pages. Items
// written from memory are listed under #0723's Assumptions.
//
// Illustrations (#0727):
//   pi-find-on-network — a laptop looking for a Pi across a network
//   pi-models-compared — three boards side by side
//   pi-gpio-pinout     — the 40-pin header close up

// ---------------------------------------------------------------------------
// 1. Finding the Pi (pi-find-on-network)

export const FIND_HOST = 'mypi.local'; // matches pi-imager-setup's hostname
export const FIND_USER = 'maker'; // matches piSetup.ts DEMO_USER

export type NetworkId = 'home' | 'guest' | 'cafe' | 'work' | 'hotspot';
export interface FindNetwork {
  id: NetworkId;
  label: string;
  detail: string;
  short: string;
  barrier: string | null;
}

export const FIND_NETWORKS: FindNetwork[] = [
  { id: 'home', label: 'Home WiFi', detail: 'your own router', short: 'router', barrier: null },
  { id: 'guest', label: 'Guest network', detail: 'a separate network for visitors', short: 'guest WiFi', barrier: 'client isolation' },
  { id: 'cafe', label: 'Café or hotel WiFi', detail: 'you click Accept on a sign-in page', short: 'café WiFi', barrier: 'sign-in page' },
  { id: 'work', label: 'Office or campus network', detail: 'run by an IT department', short: 'office', barrier: 'multicast blocked' },
  { id: 'hotspot', label: 'Phone hotspot', detail: 'your phone shares its connection', short: 'hotspot', barrier: null },
];

export type LaptopOs = 'mac' | 'windows' | 'linux';
export const LAPTOP_OSES: { id: LaptopOs; label: string }[] = [
  { id: 'mac', label: 'macOS' },
  { id: 'windows', label: 'Windows' },
  { id: 'linux', label: 'Linux' },
];

export type MethodId = 'mdns' | 'router' | 'scan' | 'phone';
export const FIND_METHODS: { id: MethodId; label: string; detail: string }[] = [
  { id: 'mdns', label: FIND_HOST, detail: 'the hostname you set in Imager, plus .local' },
  { id: 'router', label: 'Router’s device list', detail: 'the list of devices on the router’s web page' },
  { id: 'scan', label: 'Network scan', detail: 'ping, arp -a or nmap from the laptop' },
  { id: 'phone', label: 'Phone scanner app', detail: 'a network scanner app on a phone on the same WiFi' },
];

export type Verdict = 'yes' | 'maybe' | 'no';
export interface MethodResult {
  id: MethodId;
  label: string;
  verdict: Verdict;
  why: string;
}
export interface FindResult {
  tone: 'good' | 'meh' | 'bad';
  title: string;
  methods: MethodResult[];
  best: MethodId | null;
  advice: string;
}

const MDNS_HOME: Record<LaptopOs, string> = {
  mac: 'macOS understands .local names built in.',
  windows: 'Windows 10 and 11 understand .local names; older Windows needed Apple’s Bonjour.',
  linux: 'Most desktop Linux understands .local names through Avahi.',
};

type Row = Record<MethodId, [Verdict, string]>;
function verdicts(net: NetworkId, os: LaptopOs): Row {
  switch (net) {
    case 'home':
      return {
        mdns: ['yes', MDNS_HOME[os]],
        router: ['yes', `Log in to the router’s web page (often 192.168.1.1) and look for mypi.`],
        scan: ['yes', `Ping ${FIND_HOST} first, then arp -a lists it; or scan with nmap.`],
        phone: ['yes', 'An app such as Fing lists it with the maker Raspberry Pi.'],
      };
    case 'guest':
      return {
        mdns: ['no', 'Client isolation keeps devices apart, so nothing on the laptop can reach the Pi.'],
        router: ['no', 'It isn’t your router, and even with the address you can’t connect.'],
        scan: ['no', 'Isolation blocks the scan as well.'],
        phone: ['no', 'The phone is kept apart from the Pi too.'],
      };
    case 'cafe':
      return {
        mdns: ['no', 'The Pi can’t click Accept on the sign-in page, so it never gets online.'],
        router: ['no', 'You can’t see the café’s router.'],
        scan: ['no', 'The Pi isn’t on the network to be found, and many café networks keep devices apart too.'],
        phone: ['no', 'The Pi isn’t on the network for the phone to find, and many café networks keep devices apart too.'],
      };
    case 'work': {
      const maybe = 'May work, may be blocked, and may break the network’s rules: ask IT first.';
      return {
        mdns: ['no', 'Many office and campus networks block the multicast that .local names need.'],
        router: ['no', 'You can’t log in to an office router.'],
        scan: ['maybe', maybe],
        phone: ['maybe', maybe],
      };
    }
    case 'hotspot':
      return {
        mdns: ['yes', 'Devices on one phone hotspot can usually see each other, so .local works.'],
        router: ['maybe', 'Android lists connected devices in its hotspot settings; an iPhone only shows how many.'],
        scan: ['yes', 'A scan finds it on the hotspot’s small network.'],
        phone: ['maybe', 'A scanner app on the hotspot phone itself may not list devices.'],
      };
  }
}

const ADVICE: Record<NetworkId, string> = {
  home: `Give a fresh Pi a few minutes after its first boot, then ssh ${FIND_USER}@${FIND_HOST}.`,
  guest: 'Put the laptop and the Pi on a phone hotspot for setup. A Zero 2W needs the hotspot set to 2.4 GHz.',
  cafe: 'Put the laptop and the Pi on a phone hotspot for setup. A Zero 2W needs the hotspot set to 2.4 GHz.',
  work: 'Ask IT, or use a phone hotspot for setup.',
  hotspot: 'Good for first setup; add your home network later.',
};

export function findPi(net: NetworkId, os: LaptopOs): FindResult {
  const row = verdicts(net, os);
  const methods = FIND_METHODS.map((m) => ({ id: m.id, label: m.label, verdict: row[m.id][0], why: row[m.id][1] }));
  const bestM = methods.find((m) => m.verdict === 'yes') ?? null;
  const best = bestM ? bestM.id : null;
  let tone: FindResult['tone'];
  let title: string;
  if (bestM) {
    tone = 'good';
    // Only mdns is ever best today; keep the general branch for future rows.
    title = best === 'mdns' ? `Try ${FIND_HOST} first` : `Try the ${bestM.label} first`;
  } else if (methods.some((m) => m.verdict === 'maybe')) {
    tone = 'meh';
    title = 'Maybe, but a hotspot is easier';
  } else {
    tone = 'bad';
    title = 'You won’t find it on this network';
  }
  return { tone, title, methods, best, advice: ADVICE[net] };
}

export function findCommands(os: LaptopOs): string[] {
  const ping = os === 'windows' ? `ping ${FIND_HOST}` : `ping -c 3 ${FIND_HOST}`;
  return [ping, 'arp -a', `ssh ${FIND_USER}@${FIND_HOST}`];
}

// ---------------------------------------------------------------------------
// 2. Choosing a model (pi-models-compared)

export type PiModelId = 'zero2w' | 'pi4' | 'pi5';
export type SpecKey = 'cpu' | 'ram' | 'wifi' | 'ethernet' | 'usb' | 'video' | 'audio' | 'power' | 'size' | 'idle';
export interface PiModel {
  id: PiModelId;
  label: string;
  specs: Record<SpecKey, string>;
}

export const SPEC_ROWS: { key: SpecKey; label: string }[] = [
  { key: 'cpu', label: 'Processor' },
  { key: 'ram', label: 'Memory' },
  { key: 'wifi', label: 'WiFi' },
  { key: 'ethernet', label: 'Ethernet' },
  { key: 'usb', label: 'USB' },
  { key: 'video', label: 'Video out' },
  { key: 'audio', label: 'Headphone jack' },
  { key: 'power', label: 'Power in' },
  { key: 'size', label: 'Size' },
  { key: 'idle', label: 'Idle power (rough)' },
];

// Order is also the tie-break order: smaller and lower power first.
export const PI_MODELS: PiModel[] = [
  {
    id: 'zero2w',
    label: 'Pi Zero 2W',
    specs: {
      cpu: '4 × Cortex-A53, 1 GHz',
      ram: '512 MB',
      wifi: '2.4 GHz only',
      ethernet: 'None',
      usb: '1 micro-USB (needs an adapter)',
      video: '1 mini-HDMI',
      audio: 'None',
      power: 'micro-USB, 5.1 V 2.5 A',
      size: '65 × 30 mm',
      idle: 'under 1 W',
    },
  },
  {
    id: 'pi4',
    label: 'Pi 4',
    specs: {
      cpu: '4 × Cortex-A72, 1.8 GHz',
      ram: '1 to 8 GB',
      wifi: '2.4 and 5 GHz',
      ethernet: 'Gigabit',
      usb: '2 × USB 3.0, 2 × USB 2.0',
      video: '2 micro-HDMI, 4K',
      audio: '3.5 mm',
      power: 'USB-C, 5.1 V 3 A',
      size: '85 × 56 mm',
      idle: 'about 3 W',
    },
  },
  {
    id: 'pi5',
    label: 'Pi 5',
    specs: {
      cpu: '4 × Cortex-A76, 2.4 GHz',
      ram: '1 to 16 GB',
      wifi: '2.4 and 5 GHz',
      ethernet: 'Gigabit',
      usb: '2 × USB 3.0, 2 × USB 2.0',
      video: '2 micro-HDMI, 4K 60 Hz',
      audio: 'None',
      power: 'USB-C, 5.1 V 5 A',
      size: '85 × 56 mm',
      idle: 'about 3 W',
    },
  },
];

export type NeedId = 'battery' | 'small' | 'ethernet' | 'wifi5' | 'desktop' | 'twoDisplays' | 'dac' | 'ledPanel' | 'fast';
export const NEEDS: { id: NeedId; label: string }[] = [
  { id: 'battery', label: 'Runs on a battery' },
  { id: 'small', label: 'Small and light' },
  { id: 'ethernet', label: 'Wired Ethernet' },
  { id: 'wifi5', label: '5 GHz WiFi' },
  { id: 'desktop', label: 'A desktop with a web browser' },
  { id: 'twoDisplays', label: 'Two displays' },
  { id: 'dac', label: 'An audio DAC HAT' },
  { id: 'ledPanel', label: 'Drives an RGB LED panel' },
  { id: 'fast', label: 'Heavy computing (camera AI, compiling)' },
];

export type FitVerdict = 'yes' | 'ok' | 'no';
export interface Fit {
  verdict: FitVerdict;
  why: string;
}
const f = (verdict: FitVerdict, why: string): Fit => ({ verdict, why });

export const FITS: Record<PiModelId, Record<NeedId, Fit>> = {
  zero2w: {
    battery: f('yes', 'Under 1 W at idle, by far the least of the three.'),
    small: f('yes', '65 × 30 mm, less than half the area of a Pi 4.'),
    ethernet: f('ok', 'No Ethernet port: only through a USB adapter on its one USB port.'),
    wifi5: f('no', '2.4 GHz WiFi only: it can’t see a 5 GHz network.'),
    desktop: f('ok', '512 MB of memory: the desktop runs, but a web browser crawls.'),
    twoDisplays: f('no', 'One mini-HDMI port: one display.'),
    dac: f('yes', 'A DAC HAT fits its 40-pin header, and it has no headphone jack anyway.'),
    ledPanel: f('ok', 'Works with most panels; a Pi 4 refreshes a little faster.'),
    fast: f('no', 'A 1 GHz processor with 512 MB: fine for one job, not heavy work.'),
  },
  pi4: {
    battery: f('ok', 'About 3 W at idle and up to 3 A when busy: it needs a big power bank.'),
    small: f('ok', '85 × 56 mm, credit-card size.'),
    ethernet: f('yes', 'Gigabit Ethernet built in.'),
    wifi5: f('yes', 'WiFi on 2.4 and 5 GHz.'),
    desktop: f('yes', 'Runs the desktop and a browser well with 4 GB or more.'),
    twoDisplays: f('yes', 'Two micro-HDMI ports, up to 4K each.'),
    dac: f('yes', 'A DAC HAT fits its 40-pin header.'),
    ledPanel: f('yes', 'The usual board for HUB75 panels with the rpi-rgb-led-matrix library.'),
    fast: f('ok', 'Capable, but a Pi 5 is two to three times faster.'),
  },
  pi5: {
    battery: f('ok', 'About 3 W at idle and it wants a 5 A supply; on less it limits USB power.'),
    small: f('ok', '85 × 56 mm, credit-card size.'),
    ethernet: f('yes', 'Gigabit Ethernet built in.'),
    wifi5: f('yes', 'WiFi on 2.4 and 5 GHz.'),
    desktop: f('yes', 'The fastest desktop of the three.'),
    twoDisplays: f('yes', 'Two micro-HDMI ports, 4K at 60 Hz each.'),
    dac: f('yes', 'A DAC HAT fits its 40-pin header; there is no headphone jack.'),
    ledPanel: f('ok', 'Library support for the Pi 5 is new; check the rpi-rgb-led-matrix README first.'),
    fast: f('yes', '2.4 GHz and up to 16 GB: the fastest Pi.'),
  },
};

export interface BoardPick {
  board: PiModelId;
  label: string;
  reasons: string[];
  compromises: string[];
  ruledOut: { board: PiModelId; label: string; why: string }[];
}

export function recommendBoard(needs: NeedId[]): BoardPick {
  if (needs.length === 0) {
    return {
      board: 'zero2w',
      label: PI_MODELS[0].label,
      reasons: ['Nothing ticked yet: the smallest board, drawing the least power, is a fine place to start.'],
      compromises: [],
      ruledOut: [],
    };
  }
  const ticked = NEEDS.filter((n) => needs.includes(n.id)).map((n) => n.id);
  const firstNo = (m: PiModel) => ticked.find((n) => FITS[m.id][n].verdict === 'no');
  const okCount = (m: PiModel) => ticked.filter((n) => FITS[m.id][n].verdict === 'ok').length;
  // The Pi 4 has no 'no' for any need, so at least one candidate always remains.
  const candidates = PI_MODELS.filter((m) => firstNo(m) === undefined);
  let pick = candidates[0];
  for (const m of candidates) if (okCount(m) < okCount(pick)) pick = m;
  const ruledOut = PI_MODELS.filter((m) => m.id !== pick.id && firstNo(m) !== undefined).map((m) => ({
    board: m.id,
    label: m.label,
    why: FITS[m.id][firstNo(m)!].why,
  }));
  return {
    board: pick.id,
    label: pick.label,
    reasons: ticked.filter((n) => FITS[pick.id][n].verdict === 'yes').map((n) => FITS[pick.id][n].why),
    compromises: ticked.filter((n) => FITS[pick.id][n].verdict === 'ok').map((n) => FITS[pick.id][n].why),
    ruledOut,
  };
}

// ---------------------------------------------------------------------------
// 3. The GPIO header (pi-gpio-pinout)

export const GPIO_LOGIC_V = 3.3;
export const GPIO_MAX_MA_PER_PIN = 16; // raspberrypi.com/documentation, GPIO section
export const GPIO_MAX_MA_TOTAL = 50;

export type PinKind = 'power5' | 'power33' | 'ground' | 'gpio';
export type PinFunction = 'I2C' | 'SPI' | 'UART' | 'PWM' | 'I2S' | 'HAT ID';
export interface HeaderPin {
  physical: number;
  kind: PinKind;
  gpio: number | null;
  functions: PinFunction[];
  roles: string[];
  facts: string[];
  warning: string | null;
}
export type PinScheme = 'physical' | 'gpio';

const FIVE_V_WARNING =
  'Never connect a 5 V pin to a GPIO pin. GPIO pins are 3.3 V and not 5 V tolerant: 5 V can permanently damage the Pi.';

// Per-GPIO extras: functions, role tags and role-specific fact lines.
const GPIO_EXTRAS: Record<number, { functions: PinFunction[]; roles: string[]; facts: string[] }> = {};
function extra(g: number, fn: PinFunction, role: string, fact: string) {
  const e = (GPIO_EXTRAS[g] ??= { functions: [], roles: [], facts: [] });
  if (!e.functions.includes(fn)) e.functions.push(fn);
  e.roles.push(role);
  if (!e.facts.includes(fact)) e.facts.push(fact);
}
const I2C_FACT =
  'I2C bus 1 (SDA / SCL), for sensors and small displays. It has a fixed 1.8 kΩ pull-up to 3.3 V, so it reads high with nothing connected.';
const PWM_FACT = 'Hardware PWM: steady pulses for dimming and servos, without the processor’s help.';
const I2S_FACT = 'Used by audio DAC HATs (I2S).';
const HAT_FACT = 'Reserved for the ID chip on HAT boards. Leave it free.';
extra(2, 'I2C', 'I2C SDA', I2C_FACT);
extra(3, 'I2C', 'I2C SCL', I2C_FACT);
extra(0, 'HAT ID', 'HAT ID_SD', HAT_FACT);
extra(1, 'HAT ID', 'HAT ID_SC', HAT_FACT);
extra(14, 'UART', 'UART TX', 'UART TX: serial data out. Connect it to the other device’s RX.');
extra(15, 'UART', 'UART RX', 'UART RX: serial data in. Connect it to the other device’s TX.');
extra(10, 'SPI', 'SPI MOSI', 'SPI MOSI: data from the Pi to the device.');
extra(9, 'SPI', 'SPI MISO', 'SPI MISO: data from the device to the Pi.');
extra(11, 'SPI', 'SPI SCLK', 'SPI SCLK: the clock.');
extra(8, 'SPI', 'SPI CE0', 'SPI CE0: chip select for the first device.');
extra(7, 'SPI', 'SPI CE1', 'SPI CE1: chip select for a second device.');
extra(12, 'PWM', 'PWM0', PWM_FACT);
extra(13, 'PWM', 'PWM1', PWM_FACT);
extra(18, 'PWM', 'PWM0', PWM_FACT);
extra(19, 'PWM', 'PWM1', PWM_FACT);
extra(18, 'I2S', 'I2S clock', I2S_FACT);
extra(19, 'I2S', 'I2S frame', I2S_FACT);
extra(20, 'I2S', 'I2S data in', I2S_FACT);
extra(21, 'I2S', 'I2S data out', I2S_FACT);

// Physical pin -> 'power5' | 'power33' | 'ground' | BCM GPIO number.
const LAYOUT: (PinKind | number)[] = [
  'power33', 'power5', 2, 'power5', 3, 'ground', 4, 14, 'ground', 15,
  17, 18, 27, 'ground', 22, 23, 'power33', 24, 10, 'ground',
  9, 25, 11, 8, 'ground', 7, 0, 1, 5, 'ground',
  6, 12, 13, 'ground', 19, 16, 26, 20, 'ground', 21,
];

function makePin(physical: number, slot: PinKind | number): HeaderPin {
  if (typeof slot === 'number') {
    const e = GPIO_EXTRAS[slot] ?? { functions: [], roles: [], facts: [] };
    return {
      physical,
      kind: 'gpio',
      gpio: slot,
      functions: [...e.functions],
      roles: [...e.roles],
      facts: [
        ...e.facts,
        `GPIO ${slot}: an input or an output at ${GPIO_LOGIC_V} V. Not 5 V tolerant.`,
        `Up to ${GPIO_MAX_MA_PER_PIN} mA from one pin and ${GPIO_MAX_MA_TOTAL} mA from all of them together: light an LED through a resistor, and switch anything bigger with a transistor.`,
      ],
      warning: null,
    };
  }
  const base = { physical, kind: slot, gpio: null, functions: [] as PinFunction[], warning: null as string | null };
  if (slot === 'power5') {
    return {
      ...base,
      roles: ['5 V'],
      facts: [
        'Connected to the board’s 5 V supply: it can power small 5 V parts, or power the Pi itself.',
        'How much it can give depends on your power supply, less what the Pi uses.',
      ],
      warning: FIVE_V_WARNING,
    };
  }
  if (slot === 'power33') {
    return {
      ...base,
      roles: ['3.3 V'],
      facts: [
        '3.3 V from the Pi’s own regulator, for sensors and modules that run at 3.3 V.',
        'Keep big loads like motors and LED strips off it.',
      ],
    };
  }
  return {
    ...base,
    roles: ['Ground'],
    facts: [
      'All eight ground pins are connected together.',
      'Join the ground of everything you connect, including a separate power supply, to one of these.',
    ],
  };
}

export const PI_HEADER: HeaderPin[] = LAYOUT.map((slot, i) => makePin(i + 1, slot));

export function pinByPhysical(n: number): HeaderPin {
  const p = PI_HEADER[n - 1];
  if (!Number.isInteger(n) || !p) throw new Error(`piStart: no physical pin ${n}`);
  return p;
}

export function pinByGpio(g: number): HeaderPin {
  const p = PI_HEADER.find((x) => x.gpio === g);
  if (!p) throw new Error(`piStart: GPIO ${g} is not on the header`);
  return p;
}

export function pinLabel(p: HeaderPin, scheme: PinScheme): string {
  if (scheme === 'physical') return String(p.physical);
  if (p.kind === 'gpio') return `GPIO${p.gpio}`;
  return p.kind === 'power5' ? '5V' : p.kind === 'power33' ? '3V3' : 'GND';
}

export function pinTitle(p: HeaderPin): string {
  const what =
    p.kind === 'gpio' ? `GPIO ${p.gpio}` : p.kind === 'power5' ? '5 V power' : p.kind === 'power33' ? '3.3 V power' : 'Ground';
  return `Pin ${p.physical}: ${what}`;
}

export function pinsWith(fn: PinFunction): number[] {
  return PI_HEADER.filter((p) => p.functions.includes(fn)).map((p) => p.physical);
}

export function gpiozeroLine(p: HeaderPin): string | null {
  if (p.kind !== 'gpio') return null;
  return `led = LED(${p.gpio})  # GPIO ${p.gpio} is physical pin ${p.physical}`;
}
