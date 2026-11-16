// Logic for the "getting code on the board" ESP32-C3 guides (#0670): the
// BOOT and RESET buttons, the upload troubleshooter, where Serial output
// goes, the platformio.ini builder and the OTA update. Components stay thin;
// every rule lives here so it is unit-testable without a DOM.
//
// Facts, checked against Espressif's docs:
// - RST on the SuperMini drives the chip's CHIP_EN (enable) pin: holding it
//   keeps the chip off, releasing it starts the chip from scratch.
// - GPIO9 is a strapping pin. The chip samples it as it comes out of reset:
//   high (its default, pulled up) boots your program from flash; low (BOOT
//   held) enters the ROM download mode, which waits for new code.
// - The C3's USB-C port is wired to its built-in USB-Serial/JTAG peripheral,
//   a standard USB serial device, so no driver is needed. It stops while
//   the chip is in reset or deep sleep, so the port disappears then.
// - arduino-esp32 points Serial at that USB port only when both
//   ARDUINO_USB_MODE and ARDUINO_USB_CDC_ON_BOOT are 1 (cores/esp32/HWCDC.h);
//   otherwise Serial is UART0 on GPIO21 (TX) and GPIO20 (RX).

// ---------------------------------------------------------------------------
// 1. BOOT and RESET buttons

export type Sketch = 'ok' | 'crash' | 'sleep';
export type ChipMode = 'running' | 'reset' | 'download';

export interface ButtonsState {
  /** Is BOOT (GPIO9) held down? */
  boot: boolean;
  /** Is RST (CHIP_EN) held down? */
  reset: boolean;
  mode: ChipMode;
  /** What the code in flash does once it runs. */
  sketch: Sketch;
  /** A fresh upload is in flash but has not been started yet. */
  pendingNew: boolean;
  log: string[];
}

export const SKETCH_LABELS: Record<Sketch, string> = {
  ok: 'Works normally',
  crash: 'Crashes right away',
  sleep: 'Deep sleeps right away',
};

export function initialButtons(sketch: Sketch = 'ok'): ButtonsState {
  return { boot: false, reset: false, mode: 'running', sketch, pendingNew: false, log: [runningLine(sketch)] };
}

function runningLine(s: Sketch): string {
  if (s === 'ok') return 'Your program is running. The USB port is there.';
  if (s === 'crash') return 'Your program crashes and restarts over and over. The port keeps vanishing.';
  return 'Your program went straight into deep sleep. USB is off, so there is no port.';
}

/** Does the computer see a serial port right now? */
export function portPresent(s: ButtonsState): boolean {
  if (s.mode === 'download') return true;
  if (s.mode === 'reset') return false;
  return s.sketch === 'ok';
}

export function modeLabel(s: ButtonsState): string {
  if (s.mode === 'reset') return 'Held in reset';
  if (s.mode === 'download') return 'Download mode';
  if (s.sketch === 'crash') return 'Crash loop';
  if (s.sketch === 'sleep') return 'Deep sleep';
  return 'Running your program';
}

function withLog(s: ButtonsState, line: string): ButtonsState {
  return { ...s, log: [...s.log, line].slice(-6) };
}

/** Coming out of reset, the chip reads GPIO9 (BOOT) to decide how to start. */
function startUp(s: ButtonsState): ButtonsState {
  if (s.boot) {
    return withLog({ ...s, mode: 'download' }, 'RST released with BOOT held: GPIO9 read low, so the chip waits in download mode. A port appears.');
  }
  const sketch = s.pendingNew ? 'ok' : s.sketch;
  const next = { ...s, mode: 'running' as const, sketch, pendingNew: false };
  return withLog(next, s.pendingNew ? 'RST released: the new code starts. ' + runningLine('ok') : 'RST released: the chip restarts. ' + runningLine(sketch));
}

export function toggleBoot(s: ButtonsState): ButtonsState {
  const boot = !s.boot;
  const next = { ...s, boot };
  if (boot) {
    return withLog(next, s.mode === 'reset' ? 'BOOT held down while RST is held.' : 'BOOT held down. Nothing changes yet: the chip only looks at it when it starts.');
  }
  return withLog(next, s.mode === 'download' ? 'BOOT released. The chip stays in download mode until the next reset.' : 'BOOT released.');
}

export function toggleReset(s: ButtonsState): ButtonsState {
  if (!s.reset) {
    return withLog({ ...s, reset: true, mode: 'reset' }, 'RST held down: the chip is switched off. The port disappears.');
  }
  return startUp({ ...s, reset: false });
}

export interface UploadOutcome {
  state: ButtonsState;
  ok: boolean;
  message: string;
}

export function upload(s: ButtonsState): UploadOutcome {
  if (!portPresent(s)) {
    const message =
      s.mode === 'reset'
        ? 'No port: the chip is held in reset.'
        : s.sketch === 'crash'
          ? 'Failed to connect: the port vanished mid-upload. Use download mode.'
          : 'No port found. Use download mode.';
    return { state: withLog(s, `Upload failed. ${message}`), ok: false, message };
  }
  if (s.mode === 'download') {
    const message = 'Upload done. Press RST to run the new code.';
    return { state: withLog({ ...s, pendingNew: true }, message), ok: true, message };
  }
  const message = 'Upload done. The tool reset the board for you and the new code is running.';
  return { state: withLog({ ...s, sketch: 'ok', pendingNew: false }, message), ok: true, message };
}

// ---------------------------------------------------------------------------
// 2. The board that won't upload: a small decision tree

export interface TroubleStep {
  id: string;
  question: string;
  /** Plain-language detail shown under the question. */
  detail: string;
  yes: string;
  no: string;
}

export interface TroubleFix {
  id: string;
  title: string;
  body: string;
}

export const TROUBLE_STEPS: Record<string, TroubleStep> = {
  start: {
    id: 'start',
    question: 'Plug the board in. Does a new port show up?',
    detail: 'In PlatformIO, open the Devices list (or run pio device list). On a Mac it looks like /dev/cu.usbmodem101, on Windows COM3 or similar, on Linux /dev/ttyACM0.',
    yes: 'port-right',
    no: 'cable',
  },
  cable: {
    id: 'cable',
    question: 'Have you used this exact cable to move files or connect a phone to a computer?',
    detail: 'Many USB cables only carry power. The board lights up, but the computer never sees it.',
    yes: 'sleep',
    no: 'fix-cable',
  },
  sleep: {
    id: 'sleep',
    question: 'Does the code already on the board crash, reboot, or go to sleep straight away?',
    detail: 'The USB port lives on the chip itself. When the chip resets or sleeps, the port goes with it. Not sure? Assume yes and try the next step anyway.',
    yes: 'fix-download',
    no: 'hub',
  },
  hub: {
    id: 'hub',
    question: 'Is it plugged straight into the computer, with nothing in between?',
    detail: 'Some hubs, docks and adapters don’t pass data reliably.',
    yes: 'fix-download',
    no: 'fix-hub',
  },
  'port-right': {
    id: 'port-right',
    question: 'Is the upload using that port?',
    detail: 'PlatformIO picks a port automatically, and with two boards or a Bluetooth port around it can pick the wrong one. You can set upload_port in platformio.ini.',
    yes: 'monitor',
    no: 'fix-port',
  },
  monitor: {
    id: 'monitor',
    question: 'Is a serial monitor open anywhere?',
    detail: 'Another PlatformIO terminal, the Arduino IDE, or a second window. Only one program can hold the port at a time.',
    yes: 'fix-monitor',
    no: 'connect',
  },
  connect: {
    id: 'connect',
    question: 'Does the upload stop at “Connecting…” or say “Failed to connect”?',
    detail: 'That means the port is there but the chip isn’t answering, often because the program on it keeps it too busy or keeps restarting.',
    yes: 'fix-download',
    no: 'fix-read',
  },
};

export const TROUBLE_FIXES: Record<string, TroubleFix> = {
  'fix-cable': {
    id: 'fix-cable',
    title: 'Swap the cable',
    body: 'It is probably a charge-only cable: power wires but no data wires. Try a cable you know carries data. See the guide “The USB cable that looks fine and isn’t”.',
  },
  'fix-download': {
    id: 'fix-download',
    title: 'Use download mode',
    body: 'Hold BOOT, press and release RST, then let go of BOOT. The chip now waits for new code and the port stays put. Upload, then press RST to run it. See the guide on the BOOT and RESET buttons.',
  },
  'fix-hub': {
    id: 'fix-hub',
    title: 'Plug in directly',
    body: 'Take the hub or dock out of the chain and plug the board straight into the computer. If that works, the hub was the problem.',
  },
  'fix-port': {
    id: 'fix-port',
    title: 'Pick the right port',
    body: 'Unplug the board and see which port disappears. That is the one. Choose it in PlatformIO, or add upload_port = /dev/cu.usbmodem101 (your name will differ) to platformio.ini.',
  },
  'fix-monitor': {
    id: 'fix-monitor',
    title: 'Close the monitor first',
    body: 'Close every serial monitor, then upload again. The error usually says the port is busy, or “Could not exclusively lock port”. You can reopen the monitor once the upload finishes.',
  },
  'fix-read': {
    id: 'fix-read',
    title: 'Read the first error line',
    body: 'The port works and the chip answers, so it is not a connection problem. Scroll up to the first red line: a compile error, or “This chip is ESP32-C3 not ESP32”, which means the wrong board in platformio.ini.',
  },
};

/** No driver is needed: the C3's USB port is a standard USB serial device. */
export const DRIVER_NOTE =
  'You don’t need to install a driver. The ESP32-C3’s USB port is built into the chip and uses the standard USB serial driver every computer already has.';

export function isFix(id: string): boolean {
  return id in TROUBLE_FIXES;
}

export function troubleNext(id: string, answer: 'yes' | 'no'): string {
  const step = TROUBLE_STEPS[id];
  if (!step) throw new Error(`troubleshooter: no step ${id}`);
  return step[answer];
}

// ---------------------------------------------------------------------------
// 3. Where did my Serial output go?

export interface SerialSetup {
  /** Both USB build flags are in platformio.ini. */
  usbFlags: boolean;
  /** setup() waits up to 2 s for the monitor before printing. */
  waitForMonitor: boolean;
}

export interface SerialRun {
  /** What the serial monitor on USB shows. */
  usb: string[];
  /** What goes out of GPIO21 (TX), where nobody is listening. */
  txPin: string[];
  explain: string;
}

export const SETUP_LINE = 'Hello from setup()';

/**
 * After RST the board restarts and its USB port disappears and comes back,
 * so the monitor reconnects about a second later. The sketch prints one line
 * in setup() and one per second in loop().
 */
export function runSerial(cfg: SerialSetup): SerialRun {
  const all = [SETUP_LINE, 'loop 1', 'loop 2', 'loop 3'];
  if (!cfg.usbFlags) {
    return {
      usb: [],
      txPin: all,
      explain: 'Without the two build flags, Serial means the hardware serial port on GPIO21 (TX) and GPIO20 (RX). Everything went out of pin 21, and nothing went over USB.',
    };
  }
  if (!cfg.waitForMonitor) {
    return {
      usb: all.slice(1),
      txPin: [],
      explain: 'Serial goes over USB now, but setup() printed its line before the monitor had reconnected, and that line was lost.',
    };
  }
  return {
    usb: all,
    txPin: [],
    explain: 'setup() waited until the monitor connected, so every line arrived.',
  };
}

export const USB_FLAGS = ['-D ARDUINO_USB_MODE=1', '-D ARDUINO_USB_CDC_ON_BOOT=1'];

// ---------------------------------------------------------------------------
// 4. platformio.ini builder

export interface IniConfig {
  env: string;
  platform: 'espressif32' | 'atmelavr';
  board: 'esp32-c3-devkitm-1' | 'esp32dev' | 'uno';
  framework: 'arduino' | 'espidf';
  monitorSpeed: '115200' | '9600';
  usbFlags: boolean;
  fastled: boolean;
}

export const DEFAULT_INI: IniConfig = {
  env: 'supermini',
  platform: 'espressif32',
  board: 'esp32-c3-devkitm-1',
  framework: 'arduino',
  monitorSpeed: '115200',
  usbFlags: true,
  fastled: true,
};

export const INI_OPTIONS = {
  platform: ['espressif32', 'atmelavr'] as const,
  board: ['esp32-c3-devkitm-1', 'esp32dev', 'uno'] as const,
  framework: ['arduino', 'espidf'] as const,
  monitorSpeed: ['115200', '9600'] as const,
};

export type IniKey = 'env' | 'platform' | 'board' | 'framework' | 'monitor_speed' | 'build_flags' | 'lib_deps';

export interface IniLine {
  key: IniKey;
  text: string;
}

export const INI_EXPLAIN: Record<IniKey, string> = {
  env: 'Each [env:name] section is one way to build. The name is yours; it shows up in the PlatformIO toolbar. One is enough to start.',
  platform: 'The chip family. espressif32 covers every ESP32, the C3 included. PlatformIO downloads the compiler for it the first time.',
  board: 'Which board, so PlatformIO knows the chip, flash size and pins. There is no SuperMini entry; esp32-c3-devkitm-1 is the same chip and works.',
  framework: 'The style of code. arduino gives you setup(), loop(), Serial and pinMode().',
  monitor_speed: 'The serial monitor’s baud rate. Over the C3’s own USB port the number is ignored, but keep it matching Serial.begin() in case you ever use pins 20 and 21.',
  build_flags: 'Makes Serial print over the USB-C port. Without these two lines, Serial goes to pins 20 and 21 instead.',
  lib_deps: 'Libraries to download and build with your code. Write the library’s name; PlatformIO fetches it on the next build.',
};

export function buildIni(c: IniConfig): IniLine[] {
  const lines: IniLine[] = [
    { key: 'env', text: `[env:${c.env}]` },
    { key: 'platform', text: `platform = ${c.platform}` },
    { key: 'board', text: `board = ${c.board}` },
    { key: 'framework', text: `framework = ${c.framework}` },
    { key: 'monitor_speed', text: `monitor_speed = ${c.monitorSpeed}` },
  ];
  if (c.usbFlags) lines.push({ key: 'build_flags', text: `build_flags =\n  ${USB_FLAGS.join('\n  ')}` });
  if (c.fastled) lines.push({ key: 'lib_deps', text: 'lib_deps =\n  fastled/FastLED' });
  return lines;
}

export interface IniProblem {
  key: IniKey;
  severity: 'error' | 'warning';
  text: string;
}

export function checkIni(c: IniConfig): IniProblem[] {
  const out: IniProblem[] = [];
  if (!/^[A-Za-z0-9_-]+$/.test(c.env)) {
    out.push({ key: 'env', severity: 'error', text: 'Keep the environment name to letters, numbers, - and _. No spaces.' });
  }
  if (c.platform !== 'espressif32') {
    out.push({ key: 'platform', severity: 'error', text: 'atmelavr is for Arduino Uno-style boards. The ESP32-C3 needs espressif32.' });
  } else if (c.board === 'uno') {
    out.push({ key: 'board', severity: 'error', text: 'The build stops with “Unknown board ID”: uno isn’t an espressif32 board.' });
  }
  if (c.board === 'esp32dev') {
    out.push({ key: 'board', severity: 'error', text: 'esp32dev is the original ESP32. It builds, then the upload fails: “This chip is ESP32-C3 not ESP32”.' });
  }
  if (c.framework !== 'arduino') {
    out.push({ key: 'framework', severity: 'error', text: 'espidf is Espressif’s own framework. Arduino code with setup() and loop() won’t build with it.' });
  }
  if (!c.usbFlags) {
    out.push({ key: 'build_flags', severity: 'warning', text: 'No USB build flags: your Serial.print() lines will go to pin 21, not to the serial monitor.' });
  }
  if (c.monitorSpeed !== '115200') {
    out.push({ key: 'monitor_speed', severity: 'warning', text: 'Harmless over USB, but if your code says Serial.begin(115200), match it here.' });
  }
  return out;
}

// ---------------------------------------------------------------------------
// 5. Over-the-air updates

export type SlotName = 'app0' | 'app1';

export interface Slot {
  /** Firmware version in this slot, or null when empty. */
  version: number | null;
  /** Does that firmware run ArduinoOTA and call handle() in loop()? */
  listens: boolean;
}

export interface OtaState {
  slots: Record<SlotName, Slot>;
  /** The slot the board boots from. */
  active: SlotName;
  log: string[];
}

export interface OtaOptions {
  /** Laptop and board are on the same WiFi network. */
  sameNetwork: boolean;
  /** The password in platformio.ini matches the sketch's. */
  passwordMatches: boolean;
  /** WiFi drops halfway through sending. */
  dropout: boolean;
  /** The new version still calls ArduinoOTA.handle() in loop(). */
  newListens: boolean;
}

export interface OtaResult {
  state: OtaState;
  ok: boolean;
  message: string;
}

export function initialOta(): OtaState {
  return {
    slots: { app0: { version: null, listens: false }, app1: { version: null, listens: false } },
    active: 'app0',
    log: ['A new board: nothing on it yet.'],
  };
}

export function otherSlot(s: SlotName): SlotName {
  return s === 'app0' ? 'app1' : 'app0';
}

function nextVersion(s: OtaState): number {
  return Math.max(0, s.slots.app0.version ?? 0, s.slots.app1.version ?? 0) + 1;
}

function otaLog(s: OtaState, line: string): OtaState {
  return { ...s, log: [...s.log, line].slice(-6) };
}

/** A cable upload always works, and writes straight into app0. */
export function cableUpload(s: OtaState): OtaResult {
  const v = nextVersion(s);
  const state: OtaState = {
    slots: { app0: { version: v, listens: true }, app1: s.slots.app1 },
    active: 'app0',
    log: s.log,
  };
  const message = `Uploaded v${v} by cable into app0. It runs ArduinoOTA, so it can be updated over WiFi from now on.`;
  return { state: otaLog(state, message), ok: true, message };
}

export function otaUpload(s: OtaState, o: OtaOptions): OtaResult {
  const running = s.slots[s.active];
  const fail = (message: string): OtaResult => ({ state: otaLog(s, message), ok: false, message });
  if (running.version === null) return fail('No answer. The board has nothing on it: the first upload must go by cable.');
  if (!running.listens) return fail(`No answer. v${running.version} never calls ArduinoOTA.handle(), so nothing is listening. Plug in the cable.`);
  if (!o.sameNetwork) return fail('No answer. The board isn’t on your network, so your computer can’t reach it.');
  if (!o.passwordMatches) return fail('Authentication failed. The password in platformio.ini doesn’t match the one in the sketch.');
  const target = otherSlot(s.active);
  const v = nextVersion(s);
  if (o.dropout) {
    return fail(`WiFi dropped halfway through writing v${v} into ${target}. The half-written copy fails its check, so the board keeps running v${running.version} from ${s.active}.`);
  }
  const state: OtaState = {
    slots: { ...s.slots, [target]: { version: v, listens: o.newListens } } as Record<SlotName, Slot>,
    active: target,
    log: s.log,
  };
  const message = o.newListens
    ? `Wrote v${v} into ${target}, checked it, switched to it and rebooted. v${s.slots[s.active].version} stays in ${s.active} until the next update overwrites it.`
    : `Wrote v${v} into ${target} and rebooted. It runs, but it never calls ArduinoOTA.handle(), so the next update will need the cable.`;
  return { state: otaLog(state, message), ok: true, message };
}
