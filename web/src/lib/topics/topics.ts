// Short topic guides (/guides/{slug}): one idea each, built around one
// interactive demo, short enough to share on Bluesky and Mastodon. The
// slugs must match TOPIC_GUIDE_SLUGS in lib/router.ts (topics.test.ts
// checks). The router keeps its own copy so it doesn't import every demo.
import type { Component } from 'svelte';
import ChainDemo from '../guide/ChainDemo.svelte';
import BaudDemo from './BaudDemo.svelte';
import CableDemo from './CableDemo.svelte';
import DelayDemo from './DelayDemo.svelte';
import PinoutExplorer from './PinoutExplorer.svelte';
import PowerDemo from './PowerDemo.svelte';

export interface Topic {
  slug: string;
  title: string;
  /** One line: the hook on cards and in social posts. */
  hook: string;
  minutes: number;
  tags: string[];
  demo: Component;
  /** Shown above the demo: what to do with it. */
  tryIt: string;
  /** The short explanation after the demo. */
  ideas: { title: string; body: string }[];
  /** The one thing to remember. */
  takeaway: string;
  /** Suggested text for a social post (the link is appended). */
  post: string;
}

export const TOPICS: Topic[] = [
  {
    slug: 'one-wire-many-leds',
    title: 'How one wire drives a whole strip of LEDs',
    hook: 'No LED on the strip has an address. So how does LED 7 know which color is its?',
    minutes: 3,
    tags: ['LEDs', 'serial'],
    demo: ChainDemo,
    tryIt: 'Send a frame and watch each LED take the first color and pass the rest along. Then break it.',
    ideas: [
      {
        title: 'Position is the address',
        body: 'The board sends one color per LED, in order, down a single data wire. The first LED keeps the first color and forwards the rest. The second keeps the next. Nothing in the data says "LED 7". Being seventh in line is the address.',
      },
      {
        title: 'Silence means "show it"',
        body: 'After the last color the wire goes quiet for about 50 microseconds. Every LED takes that silence as the signal to display what it is holding, so the whole strip changes at once instead of rippling.',
      },
      {
        title: 'It fails quietly',
        body: 'Feed the wrong end and nothing lights. Send too few colors and the tail stays dark. Send too many and the extras fall off the end. A broken LED cuts off everything after it. None of these give an error message.',
      },
    ],
    takeaway: 'Addressable LEDs are a relay, not a bus. Follow the arrows printed on the strip.',
    post: 'No LED on an addressable strip has an address. Watch how one wire tells a whole strip what to do, and the ways it fails silently.',
  },
  {
    slug: 'delay-vs-millis',
    title: 'Why delay() makes your project ignore you',
    hook: 'Press the button. The board with delay() will miss most of your presses.',
    minutes: 3,
    tags: ['code', 'timing'],
    demo: DelayDemo,
    tryIt: 'Both boards blink the same LED. Tap the button a few times and compare the scores.',
    ideas: [
      {
        title: 'delay() stops everything',
        body: 'delay(1000) doesn’t schedule anything. It makes the whole program sit still for a second. While it waits, the board can’t read a button, a sensor or a serial command.',
      },
      {
        title: 'Check the clock instead',
        body: 'millis() is the number of milliseconds since the board powered on. Instead of waiting, the loop asks "has a second passed since I last blinked?" and moves on if not. The loop runs thousands of times a second, so it always notices the button.',
      },
      {
        title: 'Subtract, don’t compare',
        body: 'Write millis() - last >= interval, never millis() >= last + interval. The subtraction form keeps working when millis() rolls over to zero after 49.7 days.',
      },
    ],
    takeaway: 'If anything else will ever need to happen, use millis(), not delay().',
    post: 'Press the button. The board using delay() misses most presses. The one using millis() never does. A 3-minute interactive explainer.',
  },
  {
    slug: 'baud-rate',
    title: 'Baud rate: why your serial monitor shows garbage',
    hook: '"­¾ýÿ¿þ" in your serial monitor is good news: the wiring works, only the speed is wrong.',
    minutes: 4,
    tags: ['serial', 'debugging'],
    demo: BaudDemo,
    tryIt: 'The board sends at 115200 baud. Change the speed the monitor listens at and watch where it samples the wire.',
    ideas: [
      {
        title: 'No clock wire',
        body: 'Serial sends bits one after another down one wire, with nothing marking where one bit ends and the next begins. Both ends simply agree on a speed in advance: 115200 bits per second, or 9600, or something else.',
      },
      {
        title: 'Sampling at the wrong moments',
        body: 'The receiver waits for a start bit, then reads the line once per bit at its own speed. If its speed is wrong, those reads land on the wrong bits. It still decodes something, so you get garbage instead of silence.',
      },
      {
        title: 'Two places must match',
        body: 'In a PlatformIO project, Serial.begin(115200) in your code and monitor_speed = 115200 in platformio.ini must be the same number.',
      },
    ],
    takeaway: 'Garbage means the connection works. Check that both baud rates match.',
    post: 'Garbage characters in your serial monitor? Your wiring is probably fine. See exactly why a baud-rate mismatch scrambles text, with a live decoder.',
  },
  {
    slug: 'charge-only-cables',
    title: 'The USB cable that looks fine and isn’t',
    hook: 'The board lights up, but your laptop can’t see it. It’s probably the cable.',
    minutes: 2,
    tags: ['USB', 'debugging'],
    demo: CableDemo,
    tryIt: 'Pick a cable, plug it in, and compare what the board and the laptop each notice.',
    ideas: [
      {
        title: 'Some cables only carry power',
        body: 'A charge-only USB cable has the power and ground wires but leaves out the two data wires, D+ and D−. It looks exactly like a data cable from the outside.',
      },
      {
        title: 'The power light lies',
        body: 'With a charge-only cable the board still gets 5V, so its power light comes on and everything looks normal. The laptop never sees a device, so there is no serial port to upload to.',
      },
      {
        title: 'How to tell',
        body: 'If a cable has ever moved files, photos or connected a phone to a computer, it carries data. Cables that came with chargers, lights and cheap gadgets are often charge-only. Label your good ones.',
      },
    ],
    takeaway: 'No serial port? Swap the cable first. It’s the answer most of the time.',
    post: 'Board lights up but your computer can’t see it? Charge-only USB cables look identical to data cables. See what’s missing inside.',
  },
  {
    slug: 'led-power-budget',
    title: 'How many LEDs can USB power?',
    hook: 'A ten-LED strip on full white can draw more than a USB port supplies, and the board starts rebooting.',
    minutes: 3,
    tags: ['LEDs', 'power'],
    demo: PowerDemo,
    tryIt: 'Push brightness to full on white and watch the current. Then turn on the power limit.',
    ideas: [
      {
        title: 'About 60 mA per LED, at full white',
        body: 'Each WS2812B LED has a red, a green and a blue part, each drawing up to about 20 mA. Full white lights all three. Ten LEDs at full white can draw around 600 mA.',
      },
      {
        title: 'USB gives about 500 mA',
        body: 'A standard USB port is specified for 500 mA. Ask for more and the voltage sags, the chip’s brownout detector trips, and the board resets, again and again.',
      },
      {
        title: 'Let the library budget for you',
        body: 'FastLED.setMaxPowerInVoltsAndMilliamps(5, 500) estimates every frame’s draw and dims the strip just enough to stay under the limit. For bigger strips, use a separate 5V supply and connect its ground to the board.',
      },
    ],
    takeaway: 'Color and brightness decide the current. Budget for white at full brightness.',
    post: 'Why does my LED strip keep rebooting the board on white? An interactive look at how much current LEDs draw, and how much USB can give.',
  },
  {
    slug: 'esp32-c3-supermini-pinout',
    title: 'ESP32-C3 SuperMini pinout, explained',
    hook: 'Sixteen pins, a few with surprises. Tap any pin to see what it does.',
    minutes: 3,
    tags: ['ESP32', 'reference'],
    demo: PinoutExplorer,
    tryIt: 'Tap a pin on the photo, or use the filters to find the analog pins, the strapping pins and the onboard LED.',
    ideas: [
      {
        title: 'Two power pins, one ground',
        body: '5V comes straight from USB and is what an LED strip wants. 3.3 is the chip’s own regulated supply, for small sensors. G is ground, shared by everything.',
      },
      {
        title: 'Strapping pins: 2, 8 and 9',
        body: 'The chip reads these at power-on to decide how to start. They work as normal pins afterwards, but don’t wire anything that holds them low while booting. 9 is also the BOOT button, and 8 drives the onboard blue LED.',
      },
      {
        title: '3.3V only',
        body: 'Every GPIO works at 3.3V. Putting 5V on one can damage the chip. That’s why an LED strip’s 5V goes to the 5V pin, and only its data line goes to a GPIO.',
      },
    ],
    takeaway: 'Most pins are free to use. Treat 2, 8 and 9 with care, and never put 5V on a GPIO.',
    post: 'An interactive pinout for the tiny ESP32-C3 SuperMini: tap any pin to see what it does, including the three that can stop your board from booting.',
  },
];

export function topicBySlug(slug: string): Topic | undefined {
  return TOPICS.find((t) => t.slug === slug);
}
