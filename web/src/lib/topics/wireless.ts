// The logic behind the ESP32-C3 wireless and sleep guides (#0672): WiFi
// connection outcomes, BLE vs WiFi, a BLE light switch, WiFi brownouts and
// deep-sleep battery life. Plain data and pure functions so the demos stay
// thin and the numbers are unit-tested.
//
// Chip figures come from the ESP32-C3 Series Datasheet v2.5, section 5.6:
//   WiFi TX peak 335 mA (802.11b, 1 Mbps, 21 dBm), 285 mA (802.11g),
//   276 mA (802.11n); RX 84 mA; Modem-sleep at 160 MHz, CPU running
//   23–28 mA; Light-sleep 130 µA; Deep-sleep (RTC timer + memory) 5 µA.
// Board-level figures (regulator, power LED, cable resistance) are typical
// values, not datasheet ones; the guides say so.

// ---------------------------------------------------------------------------
// WiFi in 3 minutes

export type NetworkId = 'home' | 'five' | 'password' | 'cafe' | 'far';

export interface Network {
  id: NetworkId;
  label: string;
  /** What the network is, in a few words. */
  detail: string;
}

export const NETWORKS: Network[] = [
  { id: 'home', label: 'Home WiFi', detail: '2.4 GHz, WPA2 password' },
  { id: 'five', label: '5 GHz only', detail: 'a router with 2.4 GHz turned off' },
  { id: 'password', label: 'Wrong password', detail: 'home WiFi, one letter off' },
  { id: 'cafe', label: 'Café WiFi', detail: 'open, with a “click to accept” page' },
  { id: 'far', label: 'Far from the router', detail: 'two walls away, signal −88 dBm' },
];

export type WifiTone = 'good' | 'meh' | 'bad';

export interface WifiOutcome {
  /** The value WiFi.status() ends on. */
  status: 'WL_CONNECTED' | 'WL_NO_SSID_AVAIL' | 'WL_CONNECT_FAILED' | 'WL_CONNECTION_LOST' | 'WL_DISCONNECTED';
  tone: WifiTone;
  /** Serial monitor lines, in order. */
  log: string[];
  title: string;
  fix: string;
}

/** How long the sketch waits for WL_CONNECTED before giving up. */
export const CONNECT_TIMEOUT_S = 15;

const DOTS = '. . . . . . . . . . . . . . .';

/**
 * What happens when the sketch connects to a network. `badAntenna` is a
 * SuperMini with the known poor antenna layout; `lowTx` is the
 * WiFi.setTxPower(WIFI_POWER_8_5dBm) workaround for it.
 */
export function connectOutcome(id: NetworkId, badAntenna = false, lowTx = false): WifiOutcome {
  const start = `Connecting to ${id === 'cafe' ? 'CafeGuest' : 'MyHomeWiFi'}`;
  const gaveUp = `Gave up after ${CONNECT_TIMEOUT_S} s.`;
  if (id === 'home' && badAntenna && !lowTx) {
    return {
      status: 'WL_DISCONNECTED',
      tone: 'bad',
      log: [start, DOTS, gaveUp, 'status: WL_DISCONNECTED'],
      title: 'Never connects, even next to the router',
      fix: 'Some SuperMini boards have a poorly placed antenna. A known workaround is to lower the transmit power right after WiFi.begin(): WiFi.setTxPower(WIFI_POWER_8_5dBm). Tick it above and try again.',
    };
  }
  switch (id) {
    case 'home':
      return {
        status: 'WL_CONNECTED',
        tone: 'good',
        log: [start, '. . .', 'Connected!', 'IP address: 192.168.1.42'],
        title: 'Connected',
        fix: lowTx
          ? 'Connected with the lower transmit power. The range is a little shorter, which is fine in most rooms.'
          : 'WiFi.localIP() prints the address the router gave the board. Your laptop can reach it on the same network.',
      };
    case 'five':
      return {
        status: 'WL_NO_SSID_AVAIL',
        tone: 'bad',
        log: [start, DOTS, gaveUp, 'status: WL_NO_SSID_AVAIL'],
        title: 'Network not found',
        fix: 'The ESP32-C3 has a 2.4 GHz radio only (802.11 b/g/n). It cannot even see a 5 GHz network. Turn on the router’s 2.4 GHz band, or use a phone hotspot set to 2.4 GHz.',
      };
    case 'password':
      return {
        status: 'WL_CONNECT_FAILED',
        tone: 'bad',
        log: [start, '. . . . . .', gaveUp, 'status: WL_CONNECT_FAILED'],
        title: 'Password rejected',
        fix: 'The board found the network, but the router refused it. Passwords are case-sensitive: check every letter. Depending on the core version and the router, it may end on WL_DISCONNECTED or WL_CONNECTION_LOST instead.',
      };
    case 'cafe':
      return {
        status: 'WL_CONNECTED',
        tone: 'meh',
        log: ['Connecting to CafeGuest', '. .', 'Connected!', 'IP address: 10.0.3.17', 'GET http://example.com/ → 302 redirect to login page'],
        title: 'Connected, but no internet',
        fix: 'Open café, hotel and school networks often send you to a “click to accept” page first. The board can’t click it. Office networks that ask for a username (WPA2-Enterprise) won’t work with WiFi.begin(ssid, pass) either. Use a phone hotspot instead.',
      };
    case 'far':
      return {
        status: 'WL_CONNECTION_LOST',
        tone: 'meh',
        log: [start, '. . . . . . . .', 'Connected!', 'IP address: 192.168.1.42', 'RSSI: -88 dBm', 'WiFi lost. Reconnecting…'],
        title: 'Connects, then keeps dropping',
        fix: 'Around −80 dBm and below, WiFi gets unreliable. Move closer or add a repeater. Keep loop() checking WiFi.status() so the board reconnects instead of freezing.',
      };
  }
}

/** The sketch shown beside the WiFi demo, reflecting the TX-power option. */
export function wifiSketch(lowTx: boolean): string {
  return [
    '#include <WiFi.h>',
    '#include "secrets.h"  // WIFI_SSID, WIFI_PASS',
    '',
    'void setup() {',
    '  Serial.begin(115200);',
    '  WiFi.mode(WIFI_STA);',
    '  WiFi.begin(WIFI_SSID, WIFI_PASS);',
    ...(lowTx ? ['  WiFi.setTxPower(WIFI_POWER_8_5dBm);'] : []),
    '  WiFi.setAutoReconnect(true);',
    '  unsigned long start = millis();',
    '  while (WiFi.status() != WL_CONNECTED &&',
    `         millis() - start < ${CONNECT_TIMEOUT_S * 1000}) {`,
    '    delay(500);',
    '    Serial.print(".");',
    '  }',
    '  Serial.println(WiFi.localIP());',
    '}',
  ].join('\n');
}

// ---------------------------------------------------------------------------
// BLE or WiFi?

export type NeedId = 'internet' | 'phone' | 'battery' | 'lotsOfData' | 'noRouter' | 'audio';

export interface Need {
  id: NeedId;
  label: string;
}

export const NEEDS: Need[] = [
  { id: 'internet', label: 'Reach the internet or a server' },
  { id: 'phone', label: 'Talk to a phone that’s nearby' },
  { id: 'battery', label: 'Run for weeks on a battery' },
  { id: 'lotsOfData', label: 'Send a lot of data (photos, a web page)' },
  { id: 'noRouter', label: 'Work where there’s no WiFi router' },
  { id: 'audio', label: 'Stream audio or use Bluetooth serial' },
];

export type Pick = 'wifi' | 'ble' | 'both' | 'classic' | 'none';

export interface Recommendation {
  pick: Pick;
  title: string;
  reasons: string[];
}

export function recommend(needs: readonly NeedId[]): Recommendation {
  const has = (n: NeedId) => needs.includes(n);
  if (has('audio')) {
    return {
      pick: 'classic',
      title: 'The C3 can’t do this one',
      reasons: [
        'Audio and Bluetooth serial use Classic Bluetooth. The ESP32-C3 only has Bluetooth Low Energy.',
        'The original ESP32 has Classic Bluetooth. Or send the data over WiFi instead.',
      ],
    };
  }
  const wifi: string[] = [];
  const ble: string[] = [];
  if (has('internet')) wifi.push('Only WiFi reaches the internet: BLE talks to nearby devices, not to your router.');
  if (has('lotsOfData')) wifi.push('WiFi moves far more data per second than BLE.');
  if (has('phone')) ble.push('A phone can connect straight to the board over BLE, with no network to join.');
  if (has('noRouter')) ble.push('BLE needs no router: the board and the phone talk directly.');
  if (has('battery')) ble.push('BLE uses much less power than WiFi, so batteries last longer.');

  if (wifi.length && ble.length) {
    return {
      pick: 'both',
      title: 'Both, at different times',
      reasons: [
        ...wifi,
        ...ble,
        'The C3 can run both radios, but they share one antenna and both draw power. Many projects use BLE for setup and WiFi for the data.',
      ],
    };
  }
  if (wifi.length) {
    return {
      pick: 'wifi',
      title: 'WiFi',
      reasons: [...wifi, 'WiFi on the C3 is 2.4 GHz only, so check your network has a 2.4 GHz band.'],
    };
  }
  if (ble.length) {
    return {
      pick: 'ble',
      title: 'Bluetooth LE',
      reasons: [...ble, 'Send small things: a sensor reading, an on/off, a color.'],
    };
  }
  return { pick: 'none', title: 'Tick what your project needs', reasons: [] };
}

// ---------------------------------------------------------------------------
// BLE basics: a phone and a light-switch characteristic

export const LED_SERVICE_UUID = '19b10000-e8f2-537e-4f6c-d104768a1214';
export const LED_CHAR_UUID = '19b10001-e8f2-537e-4f6c-d104768a1214';
export const DEVICE_NAME = 'SuperMini-LED';

export type BleStage = 'idle' | 'scanning' | 'found' | 'connected';

export interface BleState {
  stage: BleStage;
  /** The LED characteristic's value on the board: 0 off, 1 on. */
  led: 0 | 1;
  /** The value the phone last saw, or null if it hasn't read it. */
  phoneValue: 0 | 1 | null;
  subscribed: boolean;
  log: string[];
}

export type BleAction =
  | { type: 'scan' }
  | { type: 'scanDone' }
  | { type: 'connect' }
  | { type: 'disconnect' }
  | { type: 'read' }
  | { type: 'write'; value: 0 | 1 }
  | { type: 'subscribe' }
  | { type: 'pressBoot' };

export const BLE_START: BleState = { stage: 'idle', led: 0, phoneValue: null, subscribed: false, log: [] };

const add = (s: BleState, line: string): string[] => [...s.log, line].slice(-6);

/** One step of the phone/board conversation. Invalid steps change nothing. */
export function bleStep(s: BleState, a: BleAction): BleState {
  switch (a.type) {
    case 'scan':
      if (s.stage !== 'idle') return s;
      return { ...s, stage: 'scanning', log: add(s, 'Phone: scanning for advertisements…') };
    case 'scanDone':
      if (s.stage !== 'scanning') return s;
      return { ...s, stage: 'found', log: add(s, `Found “${DEVICE_NAME}” advertising, −52 dBm`) };
    case 'connect':
      if (s.stage !== 'found') return s;
      return {
        ...s,
        stage: 'connected',
        log: add(s, 'Connected. Discovered 1 service, 1 characteristic.'),
      };
    case 'disconnect':
      if (s.stage !== 'connected') return s;
      return { ...BLE_START, led: s.led, log: add(s, 'Disconnected. The board starts advertising again.') };
    case 'read':
      if (s.stage !== 'connected') return s;
      return { ...s, phoneValue: s.led, log: add(s, `Read LED → 0x0${s.led}`) };
    case 'write':
      if (s.stage !== 'connected') return s;
      return {
        ...s,
        led: a.value,
        phoneValue: s.subscribed ? a.value : s.phoneValue,
        log: add(s, `Write LED ← 0x0${a.value}. Board: LED ${a.value ? 'on' : 'off'}`),
      };
    case 'subscribe':
      if (s.stage !== 'connected' || s.subscribed) return s;
      return { ...s, subscribed: true, log: add(s, 'Subscribed to notifications on LED') };
    case 'pressBoot': {
      // The sketch toggles the LED when BOOT is pressed and calls notify().
      const led = (s.led ? 0 : 1) as 0 | 1;
      const told = s.stage === 'connected' && s.subscribed;
      return {
        ...s,
        led,
        phoneValue: told ? led : s.phoneValue,
        log: add(s, told ? `BOOT pressed. Notify LED → 0x0${led}` : `BOOT pressed. LED ${led ? 'on' : 'off'} (the phone isn’t told)`),
      };
    }
  }
}

// ---------------------------------------------------------------------------
// WiFi brownout resets

/** WiFi transmit burst, mA: the datasheet's 802.11b peak (335 mA). */
export const TX_PEAK_MA = 335;
/**
 * Burst current with WiFi.setTxPower(WIFI_POWER_8_5dBm). The datasheet has
 * no figure for reduced power; this is an assumed, illustrative value.
 */
export const TX_LOW_MA = 200;
/** The rest of the board while running, mA (datasheet: 23–28 mA at 160 MHz). */
export const BOARD_MA = 30;
/** Ten LEDs on full white, mA (see "How many LEDs can USB power?"). */
export const STRIP_MA = 600;
/**
 * Below about this on the 5V pin, the 3.3 V regulator can't hold 3.3 V and
 * the chip's brownout detector resets it. Approximate: it depends on the
 * regulator's dropout and the brownout level the firmware sets.
 */
export const BROWNOUT_V = 3.6;
export const USB_V = 4.8;

export interface SupplyOptions {
  /** A thin, long or worn cable (and a weak port) instead of a good one. */
  thinCable: boolean;
  /** A 470 µF capacitor across 5V and GND at the board. */
  capacitor: boolean;
  /** An LED strip powered from the board's 5V pin. */
  strip: boolean;
  /** WiFi.setTxPower(WIFI_POWER_8_5dBm). */
  lowTx: boolean;
}

/** Ohms of cable, connectors and port, round trip. Illustrative values. */
export const CABLE_OHMS = { good: 0.5, thin: 3.5 };
/** Farads already on a SuperMini near the 5V pin (assumed), and the added cap. */
export const BOARD_FARADS = 22e-6;
export const CAP_FARADS = 470e-6;

export const SIM_MS = 20;
export const STEP_MS = 0.02;
/** Each burst: start time and length, ms. Real packets are about this short. */
export const BURSTS: [number, number][] = [
  [3, 1],
  [9, 1.5],
  [15, 1],
];

export interface SupplyTrace {
  /** Voltage at the board's 5V pin, one sample per STEP_MS. */
  volts: number[];
  minV: number;
  resets: boolean;
  /** Close to the edge without resetting. */
  marginal: boolean;
}

export function inBurst(t: number): boolean {
  return BURSTS.some(([s, len]) => t >= s && t < s + len);
}

/**
 * The 5V pin's voltage over time: USB through the cable's resistance into
 * the board's capacitance, with the load stepping up during each burst.
 * Simple Euler integration of dV/dt = (I_in − I_load) / C.
 */
export function simulateSupply(o: SupplyOptions): SupplyTrace {
  const r = o.thinCable ? CABLE_OHMS.thin : CABLE_OHMS.good;
  const c = BOARD_FARADS + (o.capacitor ? CAP_FARADS : 0);
  const steady = (BOARD_MA + (o.strip ? STRIP_MA : 0)) / 1000;
  const burst = (o.lowTx ? TX_LOW_MA : TX_PEAK_MA) / 1000;
  const dt = STEP_MS / 1000;
  // Start settled at the steady-state voltage.
  let v = USB_V - steady * r;
  const volts: number[] = [];
  const n = Math.round(SIM_MS / STEP_MS);
  // Sub-steps keep the integration stable when R·C is shorter than a step.
  const sub = Math.max(1, Math.ceil((dt * 4) / (r * c)));
  for (let i = 0; i <= n; i++) {
    volts.push(v);
    const load = steady + (inBurst(i * STEP_MS) ? burst : 0);
    for (let k = 0; k < sub; k++) {
      const iin = (USB_V - v) / r;
      v += ((iin - load) / c) * (dt / sub);
    }
  }
  const minV = Math.min(...volts);
  return { volts, minV, resets: minV < BROWNOUT_V, marginal: minV >= BROWNOUT_V && minV < 4.1 };
}

// ---------------------------------------------------------------------------
// Deep sleep battery life

/** Chip only, in deep sleep with the RTC timer running, mA (datasheet: 5 µA). */
export const DEEP_SLEEP_MA = 0.005;
/** Awake without WiFi, mA (datasheet Modem-sleep, 160 MHz, CPU running: 23–28 mA). */
export const AWAKE_MA = 25;
/**
 * Average while connecting and sending over WiFi, mA. Assumed: the radio
 * mixes 84 mA receiving with 276–335 mA transmit peaks.
 */
export const AWAKE_WIFI_MA = 100;
/** A SuperMini's 3.3 V regulator idle current, mA (typical, varies by board). */
export const REGULATOR_MA = 0.05;
/** The always-on power LED, mA (typical, varies by board and resistor). */
export const POWER_LED_MA = 1;

export interface SleepPlan {
  /** Seconds awake each time it wakes. */
  awakeS: number;
  /** Minutes asleep between wakes. */
  sleepMin: number;
  wifi: boolean;
  /** Count the board's regulator and power LED, not just the chip. */
  boardParts: boolean;
  batteryMah: number;
}

export function sleepCurrentMa(boardParts: boolean): number {
  return DEEP_SLEEP_MA + (boardParts ? REGULATOR_MA + POWER_LED_MA : 0);
}

export function awakeCurrentMa(wifi: boolean): number {
  return wifi ? AWAKE_WIFI_MA : AWAKE_MA;
}

/** Average current over one wake/sleep cycle, mA. */
export function averageCurrentMa(p: SleepPlan): number {
  const sleepS = p.sleepMin * 60;
  const total = p.awakeS + sleepS;
  if (total <= 0) return awakeCurrentMa(p.wifi);
  return (awakeCurrentMa(p.wifi) * p.awakeS + sleepCurrentMa(p.boardParts) * sleepS) / total;
}

/** Hours the battery lasts, ignoring self-discharge and voltage cut-off. */
export function batteryHours(p: SleepPlan): number {
  return p.batteryMah / averageCurrentMa(p);
}

/** "3 hours", "12 days", "1.4 years". */
export function formatDuration(hours: number): string {
  if (hours < 48) return `${Math.round(hours)} hours`;
  const days = hours / 24;
  if (days < 120) return `${Math.round(days)} days`;
  const years = days / 365;
  return `${years < 10 ? years.toFixed(1) : Math.round(years)} years`;
}

/** "25 mA", "1.06 mA", "5 µA". */
export function formatCurrent(ma: number): string {
  if (ma >= 10) return `${Math.round(ma)} mA`;
  if (ma >= 1) return `${ma.toFixed(2)} mA`;
  const ua = ma * 1000;
  return `${ua >= 10 ? Math.round(ua) : ua.toFixed(1).replace(/\.0$/, '')} µA`;
}
