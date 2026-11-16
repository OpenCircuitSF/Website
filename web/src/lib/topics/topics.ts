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
// Soldering guides (#0667)
import SolderWireDemo from './SolderWireDemo.svelte';
import IronTempDemo from './IronTempDemo.svelte';
import TinningDemo from './TinningDemo.svelte';
import JointQuiz from './JointQuiz.svelte';
import TipCareDemo from './TipCareDemo.svelte';
import HeaderPinsDemo from './HeaderPinsDemo.svelte';
// ESP32-C3: code on the board (#0670)
import BootButtonsDemo from './BootButtonsDemo.svelte';
import UploadDoctor from './UploadDoctor.svelte';
import SerialOutputDemo from './SerialOutputDemo.svelte';
import IniBuilderDemo from './IniBuilderDemo.svelte';
import OtaDemo from './OtaDemo.svelte';
// ESP32-C3: wireless and sleep (#0672)
import WifiConnectDemo from './WifiConnectDemo.svelte';
import BleWifiPicker from './BleWifiPicker.svelte';
import BleBasicsDemo from './BleBasicsDemo.svelte';
import BrownoutDemo from './BrownoutDemo.svelte';
import DeepSleepDemo from './DeepSleepDemo.svelte';

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
        body: 'Jacks marked A, mA µA or 10A, with no V, measure current. Inside, they are almost a plain wire to COM. Touch a battery with red in one of them and you short it through the meter, which blows the fuse. Some cheap 10A jacks have no fuse at all.',
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
        body: 'A broken wire, a cracked solder joint or an open switch shows OL, or a lone 1 on a budget meter. Nothing is getting through. A resistor stays quiet too: it is a path, just not a connection. Some meters show its value here; a budget meter may show over range.',
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
  // Soldering guides (#0667)
  'soldering-solder-wire': {
    demo: SolderWireDemo,
    tryIt: 'Pick a solder and a thickness, and compare what each one needs from your iron.',
    ideas: [
      {
        title: 'Leaded is easiest to learn',
        body: '63/37 solder melts at exactly 183 °C and sets cleanly, with a bright, shiny finish. Lead-free SAC305 melts at about 217–220 °C, so it needs a hotter iron, and even its good joints look a little dull. Both make good joints.',
      },
      {
        title: 'The flux is inside the wire',
        body: 'Electronics solder is a hollow wire filled with rosin or no-clean flux. As it melts, the flux strips the thin film of oxide off the metal so the solder can stick. For kits and header pins, pick wire 0.6–0.8 mm thick. Never use plumbing solder: its acid flux keeps eating the copper.',
      },
      {
        title: 'Wash your hands, open a window',
        body: 'The smoke is flux, not lead. An iron is nowhere near hot enough to boil lead, but flux fumes irritate your lungs, so work by an open window or let a small fan carry them away. With leaded solder, wash your hands afterwards and don’t eat while you work.',
      },
    ],
    takeaway: 'Start with 0.8 mm rosin-core solder. 63/37 is easiest; lead-free works too, with a hotter iron.',
  },
  'soldering-iron-temperature': {
    demo: IronTempDemo,
    tryIt: 'Start cold and turn the dial up. Then switch to lead-free, and try a fine tip at the same setting.',
    ideas: [
      {
        title: 'Start at 330–350 °C',
        body: 'That suits leaded solder. Lead-free wants 350–380 °C. Both are well above the melting point, because the joint pulls heat out of the tip the moment they touch.',
      },
      {
        title: 'Too cold, too hot',
        body: 'Too cold and the solder lumps up dull and grainy, and you hold the iron there so long the flux is gone anyway. Too hot and the flux burns off before it can clean the metal, the tip turns black, and copper pads can peel off the board.',
      },
      {
        title: 'Contact beats the dial',
        body: 'Heat flows through the spot where the tip touches. A wide chisel tip pressed against both the pad and the pin, with a tiny bit of solder between them to carry the heat, does more than turning the dial up. A good joint flows in 2–3 seconds.',
      },
    ],
    takeaway: 'Aim for joints that flow in 2–3 seconds. If it’s slow, use a bigger tip before you add heat.',
  },
  'soldering-tinning': {
    demo: TinningDemo,
    tryIt: 'Join the wire to the pad straight away. Then start over, tin both first, and compare.',
    ideas: [
      {
        title: 'Coat each side first',
        body: 'Heat the stripped end of the wire and feed in solder until it soaks through the strands. Then put a thin, shiny layer on the pad or terminal. That is tinning.',
      },
      {
        title: 'Then just touch them together',
        body: 'Hold the tinned wire on the tinned pad and touch the iron to both. The two layers melt into one in about a second. You don’t need a third hand for the solder.',
      },
      {
        title: 'Bare strands go wrong',
        body: 'Untinned stranded wire splays and frays, and solder wicks up under the insulation, leaving a stiff spot that snaps later. Don’t overdo tinning either: just enough to bind the strands together.',
      },
    ],
    takeaway: 'Tin both sides, then join. Two wet surfaces want to become one.',
  },
  'soldering-good-joint': {
    demo: JointQuiz,
    tryIt: 'Each picture is a joint cut in half. Name what you see, then read what caused it.',
    ideas: [
      {
        title: 'Look for the volcano',
        body: 'A good through-hole joint has smooth, slightly hollow sides, like a little volcano around the pin. The solder covers the whole pad and climbs the pin, and you can still see the pin’s shape under it.',
      },
      {
        title: 'Most problems are heat problems',
        body: 'Cold, disturbed and overheated joints all come from heat: too little, a wobble while it cooled, or too much for too long. Heat the pad and pin together, keep it to 2–3 seconds, and hold still until it sets.',
      },
      {
        title: 'Too much, too little',
        body: 'A ball of solder hides whether anything underneath connected. A thin ring is weak. A bridge between pins is a short circuit. Solder wick soaks up the extra, and a touch of fresh solder brings fresh flux to a joint that needs redoing.',
      },
    ],
    takeaway: 'Shape first, then shine. A smooth, hollow-sided cone is a good joint.',
  },
  'soldering-tip-care': {
    demo: TipCareDemo,
    tryIt: 'Treat the tip badly and watch it darken until solder won’t stick. Then find the habits that keep it shiny.',
    ideas: [
      {
        title: 'Keep it tinned',
        body: 'A coat of solder on the tip shields it from the air and carries heat into the joint. A bare hot tip oxidizes within minutes, and black oxide won’t take solder at all.',
      },
      {
        title: 'Brass wool, not a soaking sponge',
        body: 'Brass wool scrubs off old solder and burnt flux without cooling the tip. A sponge works if it is damp, not wet: each wipe on a soaking one chills the tip, and over time that wears out its plating.',
      },
      {
        title: 'Switch off with a blob on',
        body: 'Before you turn the iron off, melt a blob of solder onto the tip and leave it there. If a tip has gone black, use tip tinner paste. Never sand or file it: that strips the plating and ruins the tip.',
      },
    ],
    takeaway: 'A shiny tip is a working tip. Tin it before each joint and before you switch off.',
  },
  'soldering-header-pins': {
    demo: HeaderPinsDemo,
    tryIt: 'Step through soldering the header pins onto a SuperMini, seen from the side.',
    ideas: [
      {
        title: 'Let the breadboard hold it',
        body: 'A breadboard grips the header pins straight, square and exactly the right distance apart. Put the long ends in the breadboard and the board on top, and you have both hands free.',
      },
      {
        title: 'Tack, check, then commit',
        body: 'Solder just two opposite corners first. If the board sits crooked, you only have one joint to reheat. Once all sixteen pins are soldered, straightening it is nearly impossible.',
      },
      {
        title: 'Safe habits',
        body: 'Always put the iron back in its stand, never on the table. It stays hot enough to burn for several minutes after you switch it off. Work where the flux smoke can drift away, and wash your hands when you finish.',
      },
    ],
    takeaway: 'Breadboard as a jig, two opposite corners, check it’s flat, then the rest.',
  },
  // ESP32-C3: code on the board (#0670)
  'esp32-c3-boot-reset-buttons': {
    demo: BootButtonsDemo,
    tryIt: 'The code on this board went straight to sleep, so there is no port. Hold BOOT, tap RST, let go of BOOT, then upload.',
    ideas: [
      {
        title: 'RST restarts',
        body: 'RST is wired to the chip’s enable pin. While you hold it, the chip is off. When you let go, it starts from the beginning, just like unplugging it. Your code stays in flash and runs again.',
      },
      {
        title: 'BOOT is GPIO9',
        body: 'The chip checks GPIO9 at the moment it starts. Normally the pin is high and your program runs. If BOOT is held down, the pin is low and the chip goes into download mode instead: it runs nothing and just waits for new code. Pressing BOOT on its own does nothing until the next restart.',
      },
      {
        title: 'When you need it',
        body: 'Usually never: the upload tool restarts the board into download mode by itself. You need the buttons when that fails, often because the code on the board crashes, restarts or sleeps so fast that the USB port keeps vanishing. After uploading in download mode, press RST to run the new code.',
      },
    ],
    takeaway: 'Hold BOOT, press and release RST, let go of BOOT, upload, then press RST.',
  },
  'esp32-c3-wont-upload': {
    demo: UploadDoctor,
    tryIt: 'Answer each question about your board. Every path ends at something to try.',
    ideas: [
      {
        title: 'No port: cable first',
        body: 'If no new port appears when you plug in, the cable is the usual suspect. Many cables carry power but no data. See “The USB cable that looks fine and isn’t”.',
      },
      {
        title: 'A port, but the wrong one or a busy one',
        body: 'Only one program can use a serial port at a time, so an open serial monitor blocks the upload. With several boards or Bluetooth ports around, check that the upload is using the port that appears when you plug this board in.',
      },
      {
        title: 'A board too busy to listen',
        body: 'If the code on the board crashes, restarts or deep sleeps right away, the USB port comes and goes and the upload can’t connect. Download mode fixes it: hold BOOT, tap RST, let go of BOOT, upload.',
      },
    ],
    takeaway: 'Cable, port, monitor, then download mode. You don’t need a driver for the C3.',
  },
  'esp32-c3-serial-output': {
    demo: SerialOutputDemo,
    tryIt: 'Upload with the defaults and look at the monitor. Then add the build flags, and finally make setup() wait.',
    ideas: [
      {
        title: 'Two serial ports',
        body: 'The ESP32-C3 has a USB serial port built into the chip, on the USB-C connector, and a separate hardware serial port on GPIO21 (TX) and GPIO20 (RX). In Arduino code, Serial means the pins unless you add build_flags = -D ARDUINO_USB_MODE=1 -D ARDUINO_USB_CDC_ON_BOOT=1 to platformio.ini.',
      },
      {
        title: 'Early prints get lost',
        body: 'Every upload or reset restarts the chip, and its USB port disappears and comes back. The serial monitor takes a moment to reconnect, so anything printed in the first second can vanish. Wait for the monitor in setup(), or add delay(2000) before your first print.',
      },
      {
        title: 'The baud rate doesn’t matter here',
        body: 'Over the C3’s own USB port, data moves at USB speed whatever number you pass to Serial.begin(). Baud rate only matters on the TX and RX pins. See “Baud rate: why your serial monitor shows garbage”.',
      },
    ],
    takeaway: 'Blank monitor on a C3? Add the two USB build flags and wait for the monitor in setup().',
  },
  'platformio-first-project': {
    demo: IniBuilderDemo,
    tryIt: 'Tap any line to see what it does. Then change a setting and see what breaks.',
    ideas: [
      {
        title: 'One file describes the board',
        body: 'Every PlatformIO project has a platformio.ini at the top. It says which chip, which board and which framework, so PlatformIO can download the right compiler and upload tool for you.',
      },
      {
        title: 'Use the C3 DevKitM-1 entry',
        body: 'PlatformIO’s standard espressif32 platform has no SuperMini entry. board = esp32-c3-devkitm-1 is the same chip and works. Picking esp32dev, the original ESP32, builds fine and then fails to upload with “This chip is ESP32-C3 not ESP32”.',
      },
      {
        title: 'Build, Upload, Monitor',
        body: 'Your code lives in src/main.cpp. The PlatformIO toolbar has Build (✓) to compile, Upload (→) to compile and send it to the board, and Serial Monitor (the plug) to see what it prints. Libraries listed in lib_deps download on the next build.',
      },
    ],
    takeaway: 'platform espressif32, board esp32-c3-devkitm-1, framework arduino, plus the two USB flags.',
  },
  'esp32-ota-updates': {
    demo: OtaDemo,
    tryIt: 'Try WiFi first. Then upload by cable, and send a few updates over WiFi. Break one with the checkboxes.',
    ideas: [
      {
        title: 'Two slots',
        body: 'The ESP32’s flash has two places for a program, app0 and app1. The default 4 MB layout already has both. The board keeps running from one while the update is written into the other. Only when the new copy arrives complete and checks out does the board switch to it and restart. If it doesn’t, nothing changes.',
      },
      {
        title: 'The first upload is by cable',
        body: 'OTA is code in your sketch: ArduinoOTA.begin() in setup() and ArduinoOTA.handle() in loop(). Set a password with ArduinoOTA.setPassword(). In platformio.ini, add upload_protocol = espota, upload_port = the board’s IP address or its name.local, and the same password as upload_flags = --auth=yourpassword. Your computer must be on the same network.',
      },
      {
        title: 'Keep it listening',
        body: 'Each new version must still call ArduinoOTA.handle() in loop(). Upload code that forgets it, hangs, or sleeps all the time, and nothing is listening for the next update. Then it’s back to the cable.',
      },
    ],
    takeaway: 'First upload by cable, same network, a password, and every version keeps calling ArduinoOTA.handle().',
  },
  // ESP32-C3: wireless and sleep (#0672)
  'esp32-c3-wifi': {
    demo: WifiConnectDemo,
    tryIt: 'Pick a network and watch the serial monitor. Then tick the antenna problem and find the fix.',
    ideas: [
      {
        title: 'Four steps to connect',
        body: 'WiFi.mode(WIFI_STA) makes the board a normal WiFi client. WiFi.begin(ssid, password) starts connecting. Wait until WiFi.status() is WL_CONNECTED, but give up after 15 seconds or so, so a bad network can’t hang the board forever. Then WiFi.localIP() tells you its address.',
      },
      {
        title: '2.4 GHz only',
        body: 'The ESP32-C3 speaks 802.11 b/g/n on 2.4 GHz. It can’t see a 5 GHz-only network at all. Café and hotel networks with a “click to accept” page, and office networks that ask for a username (WPA2-Enterprise), won’t work with WiFi.begin(ssid, password) either. A phone hotspot set to 2.4 GHz is the easy way out.',
      },
      {
        title: 'Stay connected, keep secrets',
        body: 'WiFi drops sometimes. Call WiFi.setAutoReconnect(true), and in loop() check WiFi.status() instead of waiting, so the rest of your code keeps running. Put your network name and password in a secrets.h file, and list it in .gitignore so it never ends up in code you share.',
      },
      {
        title: 'The SuperMini antenna fix',
        body: 'Some SuperMini boards have a poorly placed antenna and won’t connect, even right next to the router. A known workaround for those boards is WiFi.setTxPower(WIFI_POWER_8_5dBm) straight after WiFi.begin(). It lowers the transmit power, and for many people it’s the difference between never connecting and connecting first time.',
      },
    ],
    takeaway: 'Connect with a timeout, use a 2.4 GHz network, and keep your password out of shared code.',
  },
  'ble-vs-wifi': {
    demo: BleWifiPicker,
    tryIt: 'Tick everything your project needs to do and read the recommendation.',
    ideas: [
      {
        title: 'WiFi: your network and the internet',
        body: 'WiFi joins your home network, so the board can reach the internet, a server or Home Assistant, and move a lot of data. It needs a router and uses more power: the radio draws about 85 mA receiving and short peaks over 300 mA when it transmits.',
      },
      {
        title: 'BLE: a phone nearby, on a battery',
        body: 'Bluetooth Low Energy talks straight to a phone or another board, with no router. It sends small amounts of data, like a reading or an on/off, and uses far less power, which is why it suits battery projects. Indoors both reach roughly the same: a room or two, often 10 to 30 metres, less through walls.',
      },
      {
        title: 'What the C3 doesn’t have',
        body: 'The ESP32-C3 has WiFi 4 (2.4 GHz) and Bluetooth 5 LE. It has no Classic Bluetooth, so no Bluetooth audio and no old-style Bluetooth serial (the BluetoothSerial library). For those, use the original ESP32, or send the data over WiFi instead.',
      },
    ],
    takeaway: 'Internet or lots of data: WiFi. A phone nearby, small data, a battery: BLE.',
  },
  'ble-basics': {
    demo: BleBasicsDemo,
    tryIt: 'Scan, connect, read the LED value, then write 01 to switch it on. Subscribe and press BOOT on the board.',
    ideas: [
      {
        title: 'Advertise, then connect',
        body: 'The board is the peripheral: it keeps advertising its name, many times a second. The phone is the central: it scans, sees the name, and connects. Free apps like nRF Connect and LightBlue let you poke at any BLE device this way.',
      },
      {
        title: 'Services hold characteristics',
        body: 'Once connected, the board offers services, and each service holds characteristics: small named values with a UUID, a long ID number. Here one service holds one characteristic, LED. A real project might add one for a button or a temperature.',
      },
      {
        title: 'Read, write, notify',
        body: 'The phone can read a characteristic, or write to it, and your code reacts: write 01 and the LED turns on. With notify, the board pushes a new value whenever it changes, so the phone doesn’t have to keep asking. On the C3, use the BLE library built into the Arduino core, or NimBLE-Arduino, which is smaller.',
      },
    ],
    takeaway: 'A BLE device is a set of named values. The phone reads them, writes them, or asks to be told when they change.',
  },
  'wifi-brownout-resets': {
    demo: BrownoutDemo,
    tryIt: 'Start with the thin cable and watch the voltage dip on each WiFi burst. Then try the fixes one at a time.',
    ideas: [
      {
        title: 'WiFi transmits in bursts',
        body: 'Espressif’s datasheet lists transmit peaks of up to 335 mA for the ESP32-C3, against about 25 mA when the radio is quiet. Each burst is only a millisecond or so long, but a thin or long USB cable, a weak port, or long jumper wires drop the voltage during it.',
      },
      {
        title: 'The brownout detector resets the chip',
        body: 'If the supply sags too far, the chip resets itself rather than run unreliably. The serial monitor shows “Brownout detector was triggered”, then the board boots, starts WiFi, and does it again. It looks like a crash, but your code is fine.',
      },
      {
        title: 'Fix the power, not the code',
        body: 'Use a short, thick cable and a good port or charger. Put a 100–470 µF capacitor across 5V and GND near the board, minus leg to GND. Don’t run an LED strip from the board’s 5V pin while using WiFi; give it its own supply (see How many LEDs can USB power?). Lowering the transmit power with WiFi.setTxPower() also helps.',
      },
    ],
    takeaway: 'Resets when WiFi starts are a power problem. Better cable first, then a capacitor.',
  },
  'esp32-c3-deep-sleep': {
    demo: DeepSleepDemo,
    tryIt: 'Turn WiFi and the board’s parts on and off, and change how long it sleeps. Watch the battery life.',
    ideas: [
      {
        title: 'Microamps asleep, milliamps awake',
        body: 'In deep sleep the ESP32-C3 chip draws about 5 µA (Espressif’s datasheet). Awake it draws around 25 mA, and far more with WiFi. But the SuperMini board adds its own: the power LED stays lit and the regulator draws current too, which can be hundreds of times the chip’s. Measure the real number with a multimeter (see Measuring current without blowing the fuse).',
      },
      {
        title: 'Waking up',
        body: 'esp_sleep_enable_timer_wakeup(60 * 1000000ULL) wakes it after a minute (the time is in microseconds). A button can wake it too, but only on GPIO 0 to 5, the C3’s RTC pins. Wire it from GPIO 3 to GND with a 10 kΩ pull-up resistor to 3.3V, then call esp_deep_sleep_enable_gpio_wakeup(1ULL << 3, ESP_GPIO_WAKEUP_GPIO_LOW). Then esp_deep_sleep_start() puts it to sleep.',
      },
      {
        title: 'Waking is a restart',
        body: 'After deep sleep the board starts setup() from the top, and ordinary variables are gone. Mark the ones you want to keep with RTC_DATA_ATTR, like RTC_DATA_ATTR int bootCount = 0;. While asleep, the USB serial port disappears from your computer, so to upload, hold BOOT and tap RST first (see the guide to the boot and reset buttons).',
      },
    ],
    takeaway: 'Sleep as much as you can, wake briefly, and measure the whole board, not just the chip.',
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
