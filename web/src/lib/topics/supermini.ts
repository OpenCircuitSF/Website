// Every pad on the ESP32-C3 SuperMini, for the pinout guide. Positions are
// pad centers in each product photo's own pixel space (see
// lib/guide/programmingLeds.ts BOARD_VIEWS for the photos themselves).
//
// Seen from underneath (where the numbers are printed), the left column is
// 5V, G, 3.3, 4, 3, 2, 1, 0 and the right column 5, 6, 7, 8, 9, 10, 20, 21.
// From the top, the columns swap sides.

export type PinKind = 'power' | 'ground' | 'gpio';

export interface SuperMiniPin {
  id: string;
  label: string;
  kind: PinKind;
  title: string;
  /** Short facts, most useful first. */
  facts: string[];
  /** Small capability badges. */
  tags: string[];
  /** Row index 0..7 from the USB end, and which column seen from underneath. */
  row: number;
  col: 'a' | 'b';
}

export const SUPERMINI_PINS: SuperMiniPin[] = [
  {
    id: '5v', label: '5V', kind: 'power', row: 0, col: 'a',
    title: '5V: USB power',
    facts: [
      'Straight from the USB-C port. Use it to power 5V parts like an LED strip.',
      'Limited by what USB can supply: plan on about 500 mA total.',
      'Never connect this to a GPIO pin.',
    ],
    tags: ['power out'],
  },
  {
    id: 'g', label: 'G', kind: 'ground', row: 1, col: 'a',
    title: 'G: ground',
    facts: [
      'The return path for every circuit. Voltage is always measured against it.',
      'When a part has its own power supply, its ground must still connect here.',
    ],
    tags: ['ground'],
  },
  {
    id: '3v3', label: '3.3', kind: 'power', row: 2, col: 'a',
    title: '3.3: regulated 3.3 V',
    facts: [
      'The chip runs at 3.3 V. Use this for sensors and modules that want 3.3 V.',
      'The small regulator has limited current to spare. It is not for LED strips.',
    ],
    tags: ['power out'],
  },
  {
    id: 'gpio4', label: '4', kind: 'gpio', row: 3, col: 'a',
    title: 'GPIO 4',
    facts: [
      'General-purpose input/output. The Programming LEDs workshop drives the strip from here.',
      'Can read an analog voltage (ADC).',
    ],
    tags: ['digital', 'ADC', 'PWM'],
  },
  {
    id: 'gpio3', label: '3', kind: 'gpio', row: 4, col: 'a',
    title: 'GPIO 3',
    facts: ['General-purpose input/output.', 'Can read an analog voltage (ADC).'],
    tags: ['digital', 'ADC', 'PWM'],
  },
  {
    id: 'gpio2', label: '2', kind: 'gpio', row: 5, col: 'a',
    title: 'GPIO 2: strapping pin',
    facts: [
      'The chip reads this pin at power-on to decide how to boot.',
      'Fine to use, but avoid wiring anything that pulls it low at startup.',
      'Can read an analog voltage (ADC).',
    ],
    tags: ['digital', 'ADC', 'strapping'],
  },
  {
    id: 'gpio1', label: '1', kind: 'gpio', row: 6, col: 'a',
    title: 'GPIO 1',
    facts: ['General-purpose input/output.', 'Can read an analog voltage (ADC).'],
    tags: ['digital', 'ADC', 'PWM'],
  },
  {
    id: 'gpio0', label: '0', kind: 'gpio', row: 7, col: 'a',
    title: 'GPIO 0',
    facts: ['General-purpose input/output.', 'Can read an analog voltage (ADC).'],
    tags: ['digital', 'ADC', 'PWM'],
  },
  {
    id: 'gpio5', label: '5', kind: 'gpio', row: 0, col: 'b',
    title: 'GPIO 5',
    facts: ['General-purpose input/output.', 'Its ADC channel is unreliable while WiFi is on, so prefer 0–4 for analog.'],
    tags: ['digital', 'PWM'],
  },
  {
    id: 'gpio6', label: '6', kind: 'gpio', row: 1, col: 'b',
    title: 'GPIO 6',
    facts: ['General-purpose input/output. A safe, plain pin for buttons and LEDs.'],
    tags: ['digital', 'PWM'],
  },
  {
    id: 'gpio7', label: '7', kind: 'gpio', row: 2, col: 'b',
    title: 'GPIO 7',
    facts: ['General-purpose input/output. A safe, plain pin for buttons and LEDs.'],
    tags: ['digital', 'PWM'],
  },
  {
    id: 'gpio8', label: '8', kind: 'gpio', row: 3, col: 'b',
    title: 'GPIO 8: onboard LED, strapping pin',
    facts: [
      'The small blue LED on the board is wired here. It lights when the pin is LOW.',
      'Also a strapping pin: it must not be held low at power-on.',
    ],
    tags: ['digital', 'onboard LED', 'strapping'],
  },
  {
    id: 'gpio9', label: '9', kind: 'gpio', row: 4, col: 'b',
    title: 'GPIO 9: BOOT button',
    facts: [
      'The BOOT button connects this pin to ground. Your program can read it as a free button.',
      'Held low at power-on, the chip waits for new code instead of running yours. That is the "hold BOOT, tap RST" trick.',
    ],
    tags: ['digital', 'BOOT button', 'strapping'],
  },
  {
    id: 'gpio10', label: '10', kind: 'gpio', row: 5, col: 'b',
    title: 'GPIO 10',
    facts: ['General-purpose input/output. A safe, plain pin for buttons and LEDs.'],
    tags: ['digital', 'PWM'],
  },
  {
    id: 'gpio20', label: '20', kind: 'gpio', row: 6, col: 'b',
    title: 'GPIO 20: RX',
    facts: [
      'The receive line of the chip’s hardware serial port (UART0).',
      'Usable as a normal pin when you are not using that serial port. The USB-C port has its own serial connection.',
    ],
    tags: ['digital', 'UART RX'],
  },
  {
    id: 'gpio21', label: '21', kind: 'gpio', row: 7, col: 'b',
    title: 'GPIO 21: TX',
    facts: [
      'The transmit line of the chip’s hardware serial port (UART0).',
      'Usable as a normal pin when you are not using that serial port.',
    ],
    tags: ['digital', 'UART TX'],
  },
];

/** Pad geometry per photo: x of each column and y of each row. */
export const PAD_GEOMETRY = {
  bottom: { a: 38, b: 460, rows: [97, 162, 228, 293, 358, 425, 490, 557], r: 26 },
  top: { a: 470, b: 58, rows: [97, 166, 235, 303, 372, 440, 510, 580], r: 28 },
};

export const ALL_GPIO_NOTE =
  'Every GPIO runs at 3.3 V and is not 5 V tolerant: putting 5 V on one can damage the chip.';
