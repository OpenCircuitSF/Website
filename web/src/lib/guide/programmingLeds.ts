// Content for the Programming LEDs student guide (/workshops/programming-leds).
// Source of truth is the Workshops repo (workshops/programming-leds/ --
// STUDENT_GUIDE.md, README.md, CLASS_PLAN.md); this file adapts that copy for
// the page and should be updated when those change.
import type { StripConfig } from './leds';

export const GUIDE = {
  slug: 'programming-leds',
  title: 'Programming LEDs',
  lede:
    'Flash a program onto a tiny computer, light up a strip of LEDs, then change the code and make it do something new.',
  duration: '60 minutes',
  level: 'Absolute beginner',
  repoUrl: 'https://github.com/OpenCircuitSF/Workshops',
  firmwarePath: 'workshops/programming-leds/firmware',
};

export interface GuideStep {
  id: string;
  title: string;
  /** Plain-text body; rendered as paragraphs split on blank lines. */
  body: string;
  /** "You should see…" -- every step states its expected result. */
  expect: string;
  /** Optional link shown with the step. */
  link?: { href: string; label: string };
  /** Optional code to copy. */
  code?: string;
}

export interface GuideSection {
  id: string;
  label: string;
  title: string;
  steps: GuideStep[];
}

export const BEFORE_CLASS: GuideSection = {
  id: 'before',
  label: 'At home',
  title: 'Before you arrive',
  steps: [
    {
      id: 'vscode',
      title: 'Install Visual Studio Code',
      body: 'The free code editor from Microsoft. Mac, Windows, or Linux all work.',
      expect: 'VS Code opens to a Welcome tab.',
      link: { href: 'https://code.visualstudio.com/', label: 'code.visualstudio.com' },
    },
    {
      id: 'platformio',
      title: 'Install the PlatformIO extension',
      body: 'In VS Code, click the Extensions icon in the left sidebar (four squares), search for PlatformIO IDE, and click Install.',
      expect: 'An ant-head icon appears in the left sidebar.',
    },
    {
      id: 'pio-setup',
      title: 'Let PlatformIO finish setting itself up',
      body: 'Leave VS Code open. PlatformIO downloads its own tools the first time it runs. This takes several minutes and needs internet. Restart VS Code when it asks.',
      expect: 'A blue status bar along the bottom of the window with a ✓, a → and a plug icon.',
    },
    {
      id: 'get-code',
      title: 'Get the project code',
      body: 'Clone the repository, or click the green Code button on GitHub, choose Download ZIP, and unzip it somewhere you can find again.',
      expect: 'A Workshops folder on your laptop.',
      link: { href: 'https://github.com/OpenCircuitSF/Workshops', label: 'github.com/OpenCircuitSF/Workshops' },
      code: 'git clone https://github.com/OpenCircuitSF/Workshops.git',
    },
    {
      id: 'first-build',
      title: 'Open the firmware folder and build it once',
      body: 'File → Open Folder, then pick workshops/programming-leds/firmware. Open the firmware folder itself, not the top of the repository: PlatformIO looks for platformio.ini in the folder you open.\n\nClick the ✓ in the blue bar to Build. The first build downloads the ESP32 compiler and the FastLED library, which is the big download.',
      expect: 'A green SUCCESS in the terminal panel.',
    },
    {
      id: 'cable',
      title: 'Find a USB-C cable that carries data',
      body: 'Many USB-C cables only charge. They look identical to data cables, but your laptop never sees the board. If a cable has ever moved files or photos, it carries data.',
      expect: 'A cable you trust in your bag. Bring a second if you are unsure.',
    },
  ],
};

export const IN_CLASS: GuideSection = {
  id: 'class',
  label: 'In class',
  title: 'Light it up',
  steps: [
    {
      id: 'trace',
      title: 'Trace the three wires',
      body: 'Before plugging anything in, put a finger on each wire and name it: power, ground, data. Use the board map in Part 2 to find each pin. Trust the printed labels, not the wire colors.',
      expect: 'You can point at 5V, GND and GPIO 4 on the board, and +5V, GND and Din on the strip.',
    },
    {
      id: 'plug',
      title: 'Plug the board into your laptop',
      body: 'Connect the USB-C cable to the board and to your laptop.',
      expect: 'A small power light on the board turns on.',
    },
    {
      id: 'upload',
      title: 'Upload the program',
      body: 'Click → in the blue bar (Upload). PlatformIO builds the code, then copies it into the chip over USB.',
      expect: 'The terminal ends with SUCCESS, and the strip starts blending red → orange → green → blue → magenta.',
    },
    {
      id: 'monitor',
      title: 'Open the serial monitor',
      body: 'Click the plug icon in the blue bar. This shows messages the board sends back to your laptop.',
      expect: 'A === ProgrammingLEDs === banner, then a "blending toward …" line every two seconds.',
    },
    {
      id: 'count',
      title: 'Count your LEDs',
      body: 'Count the LEDs on your strip and check NUM_LEDS at the top of src/main.cpp matches. Nothing warns you if it is wrong.',
      expect: 'Every LED on the strip is lit, and none are dark at the end.',
    },
    {
      id: 'change',
      title: 'Change the code, three times',
      body: 'Use the playground below to try each change on screen first. Predict what will happen, then make the same edit in main.cpp and upload. Close the serial monitor before each upload.',
      expect: 'Your real strip matches the one on this page.',
    },
  ],
};

export interface Pin {
  id: '5v' | 'gnd' | 'gpio4';
  /** Silkscreen label on the board. */
  board: string;
  /** Label printed on the strip. */
  strip: string;
  name: string;
  carries: string;
}

export const PINS: Pin[] = [
  { id: '5v', board: '5V', strip: '+5V', name: 'Power', carries: 'Electricity to light the LEDs' },
  { id: 'gnd', board: 'G', strip: 'GND', name: 'Ground', carries: 'The return path, the other half of every circuit' },
  { id: 'gpio4', board: '4', strip: 'Din', name: 'Data', carries: 'The colors, sent one bit at a time' },
];

/**
 * Pad centers on the two product photos, in the photos' own pixel space.
 * The 5V / G / 3.3 / 4 pads are the top four of one edge: the right edge
 * seen from the component side, the left edge from underneath.
 */
export const BOARD_VIEWS = {
  top: {
    src: '/img/workshops/programming-leds/esp32-c3-top.webp',
    width: 532,
    height: 702,
    alt: 'ESP32-C3 SuperMini, component side, USB-C port at the top',
    labelSide: 'left' as const,
    pads: { '5v': [470, 97], gnd: [470, 166], gpio4: [470, 303] } as Record<Pin['id'], [number, number]>,
    padR: 30,
  },
  bottom: {
    src: '/img/workshops/programming-leds/esp32-c3-bottom.webp',
    width: 498,
    height: 666,
    alt: 'ESP32-C3 SuperMini, underside with printed pin labels, USB-C port at the top',
    labelSide: 'right' as const,
    pads: { '5v': [38, 97], gnd: [38, 162], gpio4: [38, 293] } as Record<Pin['id'], [number, number]>,
    padR: 28,
  },
};

export interface Exercise {
  id: string;
  group: 'pattern' | 'time' | 'data' | 'stretch' | 'count';
  label: string;
  /** The edit, as the student would type it. */
  edit: string;
  predict: string;
  explain: string;
  apply: (c: StripConfig) => StripConfig;
}

export const EXERCISES: Exercise[] = [
  {
    id: 'spread-0',
    group: 'pattern',
    label: 'LED_SPREAD = 0',
    edit: 'static const uint16_t LED_SPREAD = 0;',
    predict: 'What happens if every LED sits at the same spot in the blend?',
    explain: 'The whole strip becomes one solid color that fades as a block. The traveling look was only ever an offset between neighbors.',
    apply: (c) => ({ ...c, ledSpread: 0 }),
  },
  {
    id: 'spread-128',
    group: 'pattern',
    label: 'LED_SPREAD = 128',
    edit: 'static const uint16_t LED_SPREAD = 128;',
    predict: 'Neighbors now sit half a color apart. How much of the palette fits on the strip?',
    explain: 'Each LED is half a color step ahead of the one before it, so the strip shows several colors at once.',
    apply: (c) => ({ ...c, ledSpread: 128 }),
  },
  {
    id: 'step-200',
    group: 'time',
    label: 'STEP_MS = 200',
    edit: 'static const uint32_t STEP_MS = 200;',
    predict: 'Each color now gets a tenth of the time.',
    explain: 'It races. STEP_MS alone decides how fast the colors change.',
    apply: (c) => ({ ...c, stepMs: 200 }),
  },
  {
    id: 'step-10000',
    group: 'time',
    label: 'STEP_MS = 10000',
    edit: 'static const uint32_t STEP_MS = 10000;',
    predict: 'Ten seconds per color. Can you catch it changing?',
    explain: 'A slow drift. The colors still move every frame, just by too little to notice.',
    apply: (c) => ({ ...c, stepMs: 10000 }),
  },
  {
    id: 'frame-250',
    group: 'time',
    label: 'FRAME_MS = 250',
    edit: 'static const uint32_t FRAME_MS = 250;',
    predict: 'The strip is now redrawn 4 times a second instead of 60. Is it slower?',
    explain: 'Same speed, chunkier steps. FRAME_MS is smoothness, not speed.',
    apply: (c) => ({ ...c, frameMs: 250 }),
  },
  {
    id: 'palette-fire',
    group: 'data',
    label: 'Fire palette',
    edit: 'Red, Orange, Yellow, Orange',
    predict: 'Swap the palette for warm colors only. Does any other code need to change?',
    explain: 'No. PALETTE_LEN is computed from the array, so the pattern lives in data, not in the logic.',
    apply: (c) => ({ ...c, palette: ['Red', 'Orange', 'Yellow', 'Orange'] }),
  },
  {
    id: 'palette-ocean',
    group: 'data',
    label: 'Ocean palette',
    edit: 'Blue, Cyan, Lime, Cyan',
    predict: 'Cool colors only, and a different number of them.',
    explain: 'Four entries instead of five: the cycle gets shorter on its own.',
    apply: (c) => ({ ...c, palette: ['Blue', 'Cyan', 'Lime', 'Cyan'] }),
  },
  {
    id: 'bright-255',
    group: 'stretch',
    label: 'BRIGHTNESS 255',
    edit: '#define BRIGHTNESS 255',
    predict: 'Full brightness. Will the board be able to supply it over USB?',
    explain: 'Brighter, but FastLED caps the current at 500 mA. Watch the meter: the power limit scales brightness back down when it would go over.',
    apply: (c) => ({ ...c, brightness: 255 }),
  },
  {
    id: 'bright-10',
    group: 'stretch',
    label: 'BRIGHTNESS 10',
    edit: '#define BRIGHTNESS 10',
    predict: 'Very dim. Do the colors still look the same?',
    explain: 'Dim, and at low brightness the blends get steppy: there are fewer levels left to fade through.',
    apply: (c) => ({ ...c, brightness: 10 }),
  },
  {
    id: 'even',
    group: 'stretch',
    label: 'Even LEDs only',
    edit: 'if (i % 2) leds[i] = CRGB::Black;',
    predict: 'Add this inside the for loop in renderFrame().',
    explain: 'Every odd LED is forced off. You are now editing the logic, not just the numbers.',
    apply: (c) => ({ ...c, evenOnly: true }),
  },
  {
    id: 'reverse',
    group: 'stretch',
    label: 'Reverse',
    edit: 'leds[i] = colorAt(position - i * LED_SPREAD);',
    predict: 'One character changes: + becomes −.',
    explain: 'The colors travel toward the board instead of away from it.',
    apply: (c) => ({ ...c, reverse: true }),
  },
  {
    id: 'rgb',
    group: 'stretch',
    label: 'GRB → RGB',
    edit: 'FastLED.addLeds<WS2812B, DATA_PIN, RGB>(leds, NUM_LEDS);',
    predict: 'Tell FastLED the wrong color order.',
    explain: 'Red shows as green and green as red. The strip is wired fine; the library just needs to know which order this chip expects. That is a color-order bug, not a wiring bug.',
    apply: (c) => ({ ...c, colorOrder: 'RGB' }),
  },
  {
    id: 'count-low',
    group: 'count',
    label: 'NUM_LEDS too low',
    edit: '#define NUM_LEDS 5',
    predict: 'The code now sends 5 colors to an 8-LED strip.',
    explain: 'The last 3 LEDs never receive anything, so they stay dark. No error, no warning.',
    apply: (c) => ({ ...c, numLeds: 5 }),
  },
  {
    id: 'count-high',
    group: 'count',
    label: 'NUM_LEDS too high',
    edit: '#define NUM_LEDS 11',
    predict: 'The code sends 11 colors to an 8-LED strip.',
    explain: 'The strip looks normal: the 3 extra colors are passed off the end of the chain into nothing.',
    apply: (c) => ({ ...c, numLeds: 11 }),
  },
];

export const EXERCISE_GROUPS: { id: Exercise['group']; title: string; blurb: string }[] = [
  { id: 'count', title: 'Count', blurb: 'Make NUM_LEDS match your strip.' },
  { id: 'pattern', title: '1 · The pattern', blurb: 'LED_SPREAD: how far apart neighbors sit.' },
  { id: 'time', title: '2 · Time', blurb: 'STEP_MS and FRAME_MS.' },
  { id: 'data', title: '3 · Data, not code', blurb: 'Edit the PALETTE[] array.' },
  { id: 'stretch', title: 'Stretch', blurb: 'For anyone racing ahead.' },
];

export interface Problem {
  id: string;
  symptom: string;
  cause: string;
  fix: string;
}

export const TROUBLESHOOTING: Problem[] = [
  {
    id: 'no-port',
    symptom: 'No port to upload to',
    cause: 'A charge-only USB cable. This is the answer about 70% of the time.',
    fix: 'Swap the cable. Really. If a second cable also fails, ask for help.',
  },
  {
    id: 'lock',
    symptom: 'Could not exclusively lock port',
    cause: 'The serial monitor is open and holding the USB port.',
    fix: 'Close the serial monitor (trash-can icon on its terminal tab), then upload again.',
  },
  {
    id: 'not-detected',
    symptom: 'Upload fails, chip not detected',
    cause: 'The board is not listening for new code.',
    fix: 'Hold BOOT, tap RST, release BOOT, then upload again. Both buttons are next to the USB port.',
  },
  {
    id: 'dark',
    symptom: 'Upload works, but no lights',
    cause: 'A loose jumper, the wrong pin, or data wired to the strip’s output end.',
    fix: 'Unplug USB, reseat all three wires, check DATA_PIN is 4, and check the strip’s wire goes to Din (follow the arrows).',
  },
  {
    id: 'tail-dark',
    symptom: 'Some LEDs light, the rest are dark',
    cause: 'NUM_LEDS is lower than the number of LEDs on the strip.',
    fix: 'Count the LEDs again and set NUM_LEDS to match.',
  },
  {
    id: 'colors',
    symptom: 'Colors are wrong (red shows as green)',
    cause: 'Color order. This chip expects GRB.',
    fix: 'Make sure addLeds<> says GRB, not RGB.',
  },
  {
    id: 'reboot',
    symptom: 'Board restarts on bright colors',
    cause: 'The strip is drawing more current than USB can supply.',
    fix: 'Lower BRIGHTNESS.',
  },
  {
    id: 'garbage',
    symptom: 'Serial monitor shows garbage like ­¾ýÿ¿þ',
    cause: 'A speed (baud rate) mismatch. The wiring is fine.',
    fix: 'monitor_speed in platformio.ini must equal Serial.begin() in main.cpp: both 115200.',
  },
  {
    id: 'stale',
    symptom: 'Build fails after edits that "should" work',
    cause: 'A stale build cache.',
    fix: 'Delete the firmware/.pio/ folder and build again.',
  },
];

export const NEXT_STEPS = [
  'Add a button that changes the palette. You could not do that if the code used delay().',
  'Use a longer strip with its own 5V supply. Share the ground with the board.',
  'Explore FastLED’s built-in effects: fill_rainbow(), beatsin8(), noise16().',
  'Turn on WiFi and control the strip from your phone or Home Assistant.',
  'Install WLED: the same hardware, running a finished product with a web interface.',
];
