// Short topic guides (/guides/{slug}): one idea each, built around one
// interactive demo, short enough to share on Bluesky and Mastodon. The
// catalog metadata (slug, title, hook, minutes, tags, post copy) lives in
// web/src/content/guides/guides.json, which the router and the Go server also read;
// this file holds only each topic's page body, keyed by slug.
import { TOPIC_GUIDES, type TopicMeta } from '../guides';
import type { Component } from 'svelte';
import ChainDemo from '../guide/ChainDemo.svelte';
import BaudDemo from './BaudDemo.svelte';
import CableDemo from './CableDemo.svelte';
import DelayDemo from './DelayDemo.svelte';
import PinoutExplorer from './PinoutExplorer.svelte';
import PowerDemo from './PowerDemo.svelte';
import ProbesDemo from './ProbesDemo.svelte';
import ContinuityDemo from './ContinuityDemo.svelte';
import VoltageDemo from './VoltageDemo.svelte';
import CurrentDemo from './CurrentDemo.svelte';

/** The page body of one topic guide; its metadata comes from the catalog. */
export interface TopicBody {
  demo: Component;
  /** Shown above the demo: what to do with it. */
  tryIt: string;
  /** The short explanation after the demo. */
  ideas: { title: string; body: string }[];
  /** The one thing to remember. */
  takeaway: string;
}

export type Topic = TopicMeta & TopicBody;

export const TOPIC_BODIES: Record<string, TopicBody> = {
  'one-wire-many-leds': {
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
  },
  'delay-vs-millis': {
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
  },
  'baud-rate': {
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
  },
  'charge-only-cables': {
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
  },
  'led-power-budget': {
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
  },
  'esp32-c3-supermini-pinout': {
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
  },
  'multimeter-probes': {
    demo: ProbesDemo,
    tryIt: 'Pick a meter, then click the jack where the black probe goes, then the red one. Try the wrong jacks too.',
    ideas: [
      {
        title: 'Black always goes in COM',
        body: 'COM means common. It is the reference every reading is measured from. On every meter, for every measurement, black goes in COM. It is usually in the middle, often with a black ring.',
      },
      {
        title: 'Red goes in V almost every time',
        body: 'The jack marked V (with Ω, and sometimes mA) is for volts, resistance and continuity. That covers nearly everything you will measure. On a 3-jack meter it is labeled VΩmA. On a 4-jack meter it is VΩ.',
      },
      {
        title: 'The A jacks are only for current',
        body: 'Jacks marked A, mA or 10A measure current. Inside, they are almost a plain wire to COM. Touch a battery with red in one of them and you short it through the meter, which blows the fuse. Some cheap 10A jacks have no fuse at all.',
      },
    ],
    takeaway: 'Black in COM, red in V. Move red to an A jack only to measure current, then move it straight back.',
  },
  'multimeter-continuity': {
    demo: ContinuityDemo,
    tryIt: 'Turn the dial to the beep symbol (a diode with sound waves). Then touch the probes to each thing and listen, or here, look.',
    ideas: [
      {
        title: 'A beep means connected',
        body: 'On continuity the meter pushes a tiny current out of red and checks whether it comes back through black. If the path between them is under about 30 to 50 Ω, it beeps. You can keep your eyes on the probes instead of the display.',
      },
      {
        title: 'No beep means open',
        body: 'A broken wire, a cracked solder joint or an open switch shows OL, or a lone 1 on a budget meter. Nothing is getting through. A resistor reads its value but stays quiet: it is a path, just not a connection.',
      },
      {
        title: 'Only on unpowered circuits',
        body: 'The meter supplies its own small voltage to test with. Unplug the board or take the battery out first, or the reading means nothing. Use it to find a bad jumper wire, check a switch, or confirm a solder joint before you power up.',
      },
    ],
    takeaway: 'Power off, dial to the beep, touch both ends. Beep is connected; silence is open.',
  },
  'multimeter-voltage': {
    demo: VoltageDemo,
    tryIt: 'Turn the dial to DC volts (V with a straight line), pick something to measure, then swap the probes. On the 3-jack meter, try a range that is too low.',
    ideas: [
      {
        title: 'DC volts, probes across',
        body: 'Voltage is measured across two points, with the circuit running. Black on ground or the minus end, red on the point you want. Batteries, USB and your board are all DC: the V with a straight line, not the V with a wave (~), which is for wall power.',
      },
      {
        title: 'A minus sign is harmless',
        body: 'Swap the probes and the meter shows the same number with a minus in front. It just means red is on the lower voltage. Nothing is damaged. On the SuperMini, black on G and red on 5V should read about 5 V; red on 3.3 should read about 3.3 V.',
      },
      {
        title: 'Manual range: start high',
        body: 'An auto-ranging meter picks the range for you. On a manual meter, the number on the dial is the most that range can show. Not sure what to expect? Start high and turn down. For a 9 V battery, 20 is right. Too low and the display shows a lone 1 at the left: over range.',
      },
    ],
    takeaway: 'Dial to V with a straight line, black on ground, red on the point. A minus sign just means the probes are swapped.',
  },
  'multimeter-current': {
    demo: CurrentDemo,
    tryIt: 'Measure the LED’s current: move red to the right jack, set the dial, and put the meter in series. Then try the probes across the battery.',
    ideas: [
      {
        title: 'The meter goes in the circuit',
        body: 'Current is what flows through a wire, so the meter has to be in its path. Break the circuit open, then touch one probe to each side of the gap. All the current now flows through the meter. This is called measuring in series.',
      },
      {
        title: 'Never across a battery',
        body: 'An A jack is almost a plain wire to COM. Put those probes across a battery and you short the battery through the meter. Several amps flow and the fuse blows. A 10A jack with no fuse can get hot or be damaged. After measuring current, move red back to V.',
      },
      {
        title: 'mA for small, 10A for big',
        body: 'An LED draws 10 to 20 mA, so use the mA jack and a mA setting. Use the 10A or A jack only for big loads, like an LED strip on full white, which can draw an amp or more (see How many LEDs can USB power?). Many 10A jacks are rated for short measurements only, often 10 to 30 seconds.',
      },
    ],
    takeaway: 'Current: red to mA, break the circuit, meter in series. Never across a battery. Then move red back to V.',
  },
};

export const TOPICS: Topic[] = TOPIC_GUIDES.map((m) => {
  const body = TOPIC_BODIES[m.slug];
  if (!body) throw new Error(`topics: no body for catalog topic ${m.slug}`);
  return { ...m, ...body };
});

export function topicBySlug(slug: string): Topic | undefined {
  return TOPICS.find((t) => t.slug === slug);
}
