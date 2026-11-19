# Guide unfurl images: generator prompts

One image prompt per guide in the catalog (`web/src/content/guides/guides.json`),
50 today, for an image generator such as Codex. Each finished image becomes that
guide's link preview on Bluesky, Mastodon, Slack and iMessage (`og:image`), and
its Bluesky post thumbnail. Filed as `#0709`.

## How to use this file

1. Give the generator the **STYLE** block below, followed by one guide's prompt.
2. Generate a landscape image (1536×1024 or wider is fine), then make it exactly
   **1200×630 PNG**, centre-cropped. With the stock macOS `sips`:

       sips --resampleWidth 1200 in.png --out /tmp/w.png
       sips --cropToHeightWidth 630 1200 /tmp/w.png --out <slug>.png

   This removes about a fifth of the height, top and bottom, so keep the subject
   in the middle band (the safe area below).
3. Keep the file **under 1,000,000 bytes**, which is Bluesky's thumbnail limit.
   A detailed 1200×630 PNG can go over it; `pngquant --quality 65-90` brings it
   down without visible loss.
4. Save it at the **Output** path named in the prompt:
   `internal/seo/cardassets/guides/<slug>.png`. The file name is the guide's
   slug. The server embeds that directory: a guide with a file there unfurls
   with it, and a guide without one keeps the generated title card it has
   today. Images can therefore arrive one at a time.
5. Look at it at full size and at about 400 px wide (roughly how a feed shows
   it) before committing.

**Adding a guide later:** add a `### <slug>` section under its set with the
same four lines (Title, Hook, Output, prompt). A test fails while a guide in
the catalog has no section here, and while a section here names a slug the
catalog doesn't have.

## STYLE (shared by every prompt)

Use this before every prompt:

> Wide 1200×630 social preview illustration for Open Circuit SF, a hands-on
> electronics workshop in San Francisco. **Near-black ground (#0A0D0B)** with a
> faint dark-green vignette. The main color is **electric brand green (#68FF23)**:
> glowing, slightly luminous strokes and light, the way a terminal or an old
> phosphor CRT glows. Supporting tones are deep greens (#1E4D10, #2F7D0C) and,
> sparingly, a pale green-white (#E8F0E8) for highlights. Use other colors only
> where the prompt asks for them (an amber or red warning, an LED's color,
> resistor bands), and keep them muted beside the green. The look is part
> crisp vector line art, like a circuit illustration, and part moody macro
> photography lit by green rim light. Each prompt says which. Terminal motifs
> may appear as shapes only: a three-dot window title bar, a `>` chevron, a
> solid block cursor, PCB traces with round pads, faint CRT scanlines.
> Composition: one strong idea, uncluttered, with plenty of dark negative
> space. **Keep everything important inside the central 1000×480 area**,
> because feeds crop the edges and step 2 above trims the top and bottom.
>
> **No text at all:** no words, letters, numbers, labels, part markings or
> captions, and no legible screen text. Where a screen or display appears, its
> content is abstract shapes or out of focus. No logos or brand marks of any
> kind, including Espressif, Arduino, PlatformIO, USB-IF, Bluetooth and WiFi
> logos, the Raspberry Pi raspberry, and Volumio, Spotify and Apple marks, and
> none on parts, tools, devices or packaging. No real people and no faces; a
> fingertip or hand is fine. Not a product photo of any real, identifiable
> product or its packaging; boards and tools are generic and unbranded.

Why no title: generators garble lettering, and every feed already prints the
guide's title under the image. The family resemblance to the site's default
card (`web/public/og-default.png`: green line art on near-black, a terminal
prompt and a block cursor) comes from the palette and the line work, not from
words.

Vary from one image to the next. The prompts below deliberately mix macro
photography, flat diagrams, isometric renders, graphs and metaphors, at very
different scales. Don't let them drift back into "a board on a dark
background".

## Set: LEDs

### programming-leds

- **Title:** Programming LEDs (workshop guide)
- **Hook:** Flash an ESP32-C3, light up an LED strip, then change the code and make it do something new.
- **Output:** `internal/seo/cardassets/guides/programming-leds.png`, 1200×630 PNG

Use the shared STYLE block. A long addressable LED strip snakes across the whole frame like a glowing circuit trace, seen from a low three-quarter angle with shallow depth of field. At the left it starts dark beside a tiny unmarked dev board; pixel by pixel it lights up toward the right, shifting from green into a few soft other hues, as if code is flowing down it. A faint, out-of-focus terminal window with a block cursor hangs in the dark background.

### one-wire-many-leds

- **Title:** How one wire drives a whole strip of LEDs
- **Hook:** No LED on the strip has an address. So how does LED 7 know which color is its?
- **Output:** `internal/seo/cardassets/guides/one-wire-many-leds.png`, 1200×630 PNG

Use the shared STYLE block. Flat top-down diagram: a single green data wire enters from the left and runs through a straight chain of eight LED pixels. The data is drawn as a little train of glowing square blocks; at each LED the train is one block shorter, and that LED glows the color of the block it kept. Clean vector line art, generous dark space above and below.

### delay-vs-millis

- **Title:** Why delay() makes your project ignore you
- **Hook:** Press the button. The board with delay() will miss most of your presses.
- **Output:** `internal/seo/cardassets/guides/delay-vs-millis.png`, 1200×630 PNG

Use the shared STYLE block. Split frame, two identical small boards side by side, each with a pushbutton being pressed by a fingertip. The left board is frozen under a faint hourglass shape and the ripples from the press fade out unanswered; the right board answers instantly, its LED bright and the press ripple received. Same lighting on both halves so the only difference is the response.

### baud-rate

- **Title:** Baud rate: why your serial monitor shows garbage
- **Hook:** "­¾ýÿ¿þ" in your serial monitor is good news: the wiring works, only the speed is wrong.
- **Output:** `internal/seo/cardassets/guides/baud-rate.png`, 1200×630 PNG

Use the shared STYLE block. Extreme close-up of a terminal window on a dark CRT screen. One horizontal line runs across it: at the left it is a jumble of broken, overlapping glyph fragments (abstract shapes, never readable letters); toward the right the fragments snap into neat, evenly spaced blocks, as if a tuning dial just found the right speed. Subtle scanlines and phosphor glow.

### charge-only-cables

- **Title:** The USB cable that looks fine and isn’t
- **Hook:** The board lights up, but your laptop can’t see it. It’s probably the cable.
- **Output:** `internal/seo/cardassets/guides/charge-only-cables.png`, 1200×630 PNG

Use the shared STYLE block. Macro cutaway of a USB cable, the jacket sliced open lengthwise. Inside, only two thick power conductors glow green; the channels where the data wires should be are empty and dark. One end disappears into a small lit board, the other toward a dim laptop edge that shows no sign of noticing it.

### led-power-budget

- **Title:** How many LEDs can USB power?
- **Hook:** A ten-LED strip on full white can draw more than a USB port supplies, and the board starts rebooting.
- **Output:** `internal/seo/cardassets/guides/led-power-budget.png`, 1200×630 PNG

Use the shared STYLE block. A big analog needle gauge dominates the frame, drawn in green line art; its needle has swung hard into an amber overload zone. Below it, a USB plug feeds a tightly coiled LED strip blazing pure white, and a tiny flicker of lightning shows at the board between them. Strong contrast between the over-bright strip and the dark ground.

## Set: ESP32-C3

### esp32-c3-supermini-pinout

- **Title:** ESP32-C3 SuperMini pinout, explained
- **Hook:** Sixteen pins, a few with surprises. Tap any pin to see what it does.
- **Output:** `internal/seo/cardassets/guides/esp32-c3-supermini-pinout.png`, 1200×630 PNG

Use the shared STYLE block. Extreme top-down macro of a thumb-sized, unmarked dev board, centered and small in the frame. From each of its sixteen pins a thin leader line fans outward to a blank rounded tag, like an exploded technical diagram; most tags are green, two or three glow amber to hint at surprises. Blueprint precision, lots of dark space around the fan.

### esp32-c3-boot-reset-buttons

- **Title:** The BOOT and RESET buttons on the ESP32-C3
- **Hook:** Hold BOOT, tap RESET, let go of BOOT. It rescues a board that won’t take new code.
- **Output:** `internal/seo/cardassets/guides/esp32-c3-boot-reset-buttons.png`, 1200×630 PNG

Use the shared STYLE block. Extreme close-up of two tiny tactile buttons on the edge of a small board. One fingertip holds the first button down while a second fingertip taps the other; short motion arcs show the tap. Very shallow depth of field, green rim light on the fingertips and the button caps.

### esp32-c3-wont-upload

- **Title:** When your ESP32-C3 won’t upload
- **Hook:** No port, the wrong port, a busy port, or a board too busy to listen. Answer a few questions and find the fix.
- **Output:** `internal/seo/cardassets/guides/esp32-c3-wont-upload.png`, 1200×630 PNG

Use the shared STYLE block. Top-down maze built entirely from PCB traces, running from a USB port at the bottom edge to a small chip at the top. Most corridors end in dim dead ends; exactly one path is lit bright green all the way through. Clean flat vector style, like a puzzle on a circuit board.

### esp32-c3-serial-output

- **Title:** Where did my Serial output go?
- **Hook:** Your code prints, the monitor stays blank. On an ESP32-C3, two missing lines in platformio.ini send it all to pin 21.
- **Output:** `internal/seo/cardassets/guides/esp32-c3-serial-output.png`, 1200×630 PNG

Use the shared STYLE block. A terminal window sits empty in the center, with only a blinking block cursor. Beside it, a stream of small green dash-shaped marks, clearly output, flows the wrong way: out of a pipe on the side of the window and down into a single pin of a tiny board in the corner. The emptiness of the window is the point.

### platformio-first-project

- **Title:** Your first PlatformIO project for the ESP32-C3
- **Hook:** A few lines of platformio.ini get you building. Tap each one to see what it does, and what breaks if it’s wrong.
- **Output:** `internal/seo/cardassets/guides/platformio-first-project.png`, 1200×630 PNG

Use the shared STYLE block. Isometric view of a short stack of thin glowing slabs, each one a single configuration line, clicking into place one on top of another like building blocks. The finished stack slots into a chip socket below, which lights up green. Plain slabs with no writing; precise, toy-like isometric rendering.

### esp32-c3-boot-pins

- **Title:** The pins that can stop your ESP32-C3 from booting
- **Hook:** Wire a button to the wrong pin and your board sits waiting for new code instead of running yours.
- **Output:** `internal/seo/cardassets/guides/esp32-c3-boot-pins.png`, 1200×630 PNG

Use the shared STYLE block. A small board sits in the center with a pushbutton wired to one of its pins by a single bright wire. Above the board hangs a large translucent pause symbol, and the board itself is dimmed and waiting. Moody, still, lit mainly by the glow of the pause symbol.

### esp32-c3-reserved-pins

- **Title:** Which ESP32-C3 pins are safe to use?
- **Hook:** Some pins already have a second job: USB, serial, the flash chip. Pick a job and see which pins are free.
- **Output:** `internal/seo/cardassets/guides/esp32-c3-reserved-pins.png`, 1200×630 PNG

Use the shared STYLE block. Low-angle view along one row of a board's header pins, receding into the distance like a row of seats. Some pins already have a small glowing icon sitting on top of them (a USB plug, a memory chip, a serial waveform), meaning they are taken; the rest are empty and glow green as free. Dramatic perspective, shallow depth of field.

### esp32-c3-analog-input

- **Title:** Reading analog voltages on the ESP32-C3
- **Hook:** analogRead() gives a number from 0 to 4095, but it’s noisy and stops being accurate above about 2.5 V.
- **Output:** `internal/seo/cardassets/guides/esp32-c3-analog-input.png`, 1200×630 PNG

Use the shared STYLE block. An old CRT oscilloscope screen fills the frame, with a phosphor-green grid. A noisy, jagged trace climbs diagonally from lower left alongside a straight dashed reference line; near the top it gets fuzzier and bends away from the line. Glowing phosphor, slight screen curvature.

### esp32-c3-pwm

- **Title:** Fading an LED with PWM on the ESP32-C3
- **Hook:** The pin is only ever on or off. Switch it fast enough and the LED looks dimmed.
- **Output:** `internal/seo/cardassets/guides/esp32-c3-pwm.png`, 1200×630 PNG

Use the shared STYLE block. A single LED in the center of the frame, surrounded by a ring of alternating bright and dark segments like a spinning film strip or a stroboscope. The ring blurs with motion into a soft, even half-brightness halo around the LED. Hypnotic and symmetric.

### esp32-c3-power

- **Title:** Powering the ESP32-C3 without USB
- **Hook:** Batteries, power banks, the 5V pin or the 3.3 pin: which ones are safe, and which one fries the chip?
- **Output:** `internal/seo/cardassets/guides/esp32-c3-power.png`, 1200×630 PNG

Use the shared STYLE block. Top-down still life: a tiny board in the middle, with four power sources arranged in an arc around it, all generic and unbranded: a pack of AA cells, a slim power bank, a coin cell and a pair of bench-supply leads. Three of the connecting wires glow green; the fourth glows red, with a small wisp of smoke rising from the chip.

### esp32-c3-wifi

- **Title:** WiFi on the ESP32-C3 in 3 minutes
- **Hook:** Four lines connect the board to WiFi. Here’s what to do when they don’t.
- **Output:** `internal/seo/cardassets/guides/esp32-c3-wifi.png`, 1200×630 PNG

Use the shared STYLE block. Minimalist: a very small board sits low in the frame, and concentric radio arcs expand upward from it, thinning out into a wide constellation of connected points across the dark sky above. Mostly negative space, with one bright source of light.

### ble-vs-wifi

- **Title:** Bluetooth LE or WiFi: which do you need?
- **Hook:** WiFi reaches the internet. BLE talks to the phone in your hand. Tick what your project needs.
- **Output:** `internal/seo/cardassets/guides/ble-vs-wifi.png`, 1200×630 PNG

Use the shared STYLE block. A diptych split on a diagonal. On one side, a phone held in a hand very close to a small board, joined by short, soft, tight ripples. On the other, a long thin beam travels from the same kind of board to a distant glowing globe on the horizon. The contrast is in the scale: arm's length against the whole world.

### ble-basics

- **Title:** Bluetooth LE basics: a light switch on your phone
- **Hook:** Advertise, connect, then read and write a value. That’s how a phone switches an LED on.
- **Output:** `internal/seo/cardassets/guides/ble-basics.png`, 1200×630 PNG

Use the shared STYLE block. A smartphone lies flat on a dark desk, its screen showing nothing but one large toggle switch in the on position. A dotted arc of radio waves runs from the phone to a breadboard nearby, where a single LED has just lit up green. Calm, clean product-shot lighting with no branding.

### wifi-brownout-resets

- **Title:** Why turning on WiFi resets your board
- **Hook:** WiFi transmits in bursts of over 300 mA. A thin cable can’t keep up, and the board reboots.
- **Output:** `internal/seo/cardassets/guides/wifi-brownout-resets.png`, 1200×630 PNG

Use the shared STYLE block. A heartbeat-monitor-style line runs across the frame as the supply voltage: steady, then plunging in sharp dips. Small radio-burst icons sit above the line, each one directly over a dip. At the deepest dip a circular restart arrow flashes. Clinical green on black, like a hospital monitor.

### esp32-c3-deep-sleep

- **Title:** Deep sleep: make a battery last months
- **Hook:** Asleep, the ESP32-C3 chip draws 5 microamps. Awake with WiFi, thousands of times more.
- **Output:** `internal/seo/cardassets/guides/esp32-c3-deep-sleep.png`, 1200×630 PNG

Use the shared STYLE block. A small board rests in a dark scene under a thin crescent moon. Next to it is a coin cell, and stretching away from it is a long calendar of tiny squares running into the distance, each a day; the cell's charge glow barely fades along the whole row. Quiet, nocturnal, very still.

### esp32-ota-updates

- **Title:** Updating your ESP32 over WiFi
- **Hook:** Once the first upload is done by cable, the next ones can go over WiFi. The board keeps two copies, so a dropped update leaves the old code running.
- **Output:** `internal/seo/cardassets/guides/esp32-ota-updates.png`, 1200×630 PNG

Use the shared STYLE block. Two glowing memory slots side by side, like two cartridges in a console. Packets of light fly in through the air on radio arcs and fill the right slot; the left slot keeps a steady, protective glow, the copy that is still running. Clean, slightly futuristic hardware render.

## Set: Multimeter

### multimeter-probes

- **Title:** Multimeter probes: which jack is which
- **Hook:** Black goes in COM. Red goes in V almost every time. The jacks marked A are the ones that blow fuses.
- **Output:** `internal/seo/cardassets/guides/multimeter-probes.png`, 1200×630 PNG

Use the shared STYLE block. Low-angle close-up of the lower face of a generic, unmarked multimeter, showing four input jacks in a row. A black probe plug is seated in one jack and a red probe plug hovers just above another; the jack at the far edge has a faint amber warning glow. No printed labels on the meter at all.

### multimeter-continuity

- **Title:** Continuity: is this wire actually connected?
- **Hook:** Touch the probes to both ends of a wire. A beep means it’s connected. Silence means it isn’t.
- **Output:** `internal/seo/cardassets/guides/multimeter-continuity.png`, 1200×630 PNG

Use the shared STYLE block. Two probe tips touch the two ends of a long, loosely coiled wire that loops across the frame. From the meter, just out of frame, concentric sound-wave rings ripple outward in green, the beep made visible. Dynamic and loopy, with lots of curves.

### multimeter-voltage

- **Title:** Measuring voltage with a multimeter
- **Hook:** Check a battery, then the 5V and 3.3 pins on your board. And find out what the minus sign means.
- **Output:** `internal/seo/cardassets/guides/multimeter-voltage.png`, 1200×630 PNG

Use the shared STYLE block. A row of batteries stands on a dark bench: a coin cell, an AA and a rectangular battery. A red and a black probe rest on the terminals of one of them. The meter's display glows in the background but is completely out of focus, so no digits can be read.

### multimeter-current

- **Title:** Measuring current without blowing the fuse
- **Hook:** To measure current the meter goes in the circuit, not across it. Put it across a battery and the fuse blows.
- **Output:** `internal/seo/cardassets/guides/multimeter-current.png`, 1200×630 PNG

Use the shared STYLE block. Extreme macro of a single glass cartridge fuse lit from behind. Its thin filament has snapped in the middle, and a tiny wisp of smoke curls up from the glowing break. One object, dead center, dramatic and almost sculptural.

## Set: Soldering

### soldering-solder-wire

- **Title:** Which solder wire should you buy?
- **Hook:** Leaded or lead-free, thick or thin, and the one kind that must never touch a circuit board.
- **Output:** `internal/seo/cardassets/guides/soldering-solder-wire.png`, 1200×630 PNG

Use the shared STYLE block. Still life of four spools of solder wire, each a different thickness, arranged on a dark workbench like products on a shelf. Set apart from them, one coil sits behind a glowing ring-and-slash prohibition symbol. Unbranded spools, soft studio lighting with green rim light.

### soldering-iron-temperature

- **Title:** What temperature should your soldering iron be?
- **Hook:** Too cold makes a dull, weak joint. Too hot burns the flux and lifts the pad. Find the window.
- **Output:** `internal/seo/cardassets/guides/soldering-iron-temperature.png`, 1200×630 PNG

Use the shared STYLE block. A soldering iron tip in side profile across the middle of the frame. Behind it the background is a horizontal heat band, cool dark green at one end and scorched amber at the other, with a narrow bright green window glowing in the middle: the sweet spot. Graphic, poster-like composition.

### soldering-tinning

- **Title:** Tin first, then join
- **Hook:** Two surfaces already coated in solder flow together in a second. Bare ones fight you.
- **Output:** `internal/seo/cardassets/guides/soldering-tinning.png`, 1200×630 PNG

Use the shared STYLE block. Macro shot of two wire ends, each already coated in bright solder, meeting in the middle of the frame. A single bead of molten solder flows between them, mirror-bright and reflecting a green light. Very shallow depth of field and a tiny curl of flux smoke.

### soldering-good-joint

- **Title:** Spot the bad solder joint
- **Hook:** A good joint looks like a tiny shiny volcano. Can you name what went wrong with the others?
- **Output:** `internal/seo/cardassets/guides/soldering-good-joint.png`, 1200×630 PNG

Use the shared STYLE block. A grid of six solder joints on a board, seen from 45 degrees in macro. One is a perfect, shiny little volcano cone lit green; the other five are subtly wrong: a round blob, a dull grainy one, two pads bridged together, one starved of solder, one with a burnt pad. The good one is the brightest thing in the frame.

### soldering-tip-care

- **Title:** Why your soldering tip stopped working
- **Hook:** A black tip won’t take solder, however hot it is. Here’s how it got that way, and how to keep it shiny.
- **Output:** `internal/seo/cardassets/guides/soldering-tip-care.png`, 1200×630 PNG

Use the shared STYLE block. Two soldering iron tips side by side, pointing down. The left one is charred black and crusted; the right one is mirror-shiny, with a bright bead of solder clinging to its end. A tuft of brass wool sits between them on the bench.

### soldering-header-pins

- **Title:** Soldering header pins onto the ESP32-C3 SuperMini
- **Hook:** Use a breadboard as a jig, tack two opposite corners, and the pins come out straight every time.
- **Output:** `internal/seo/cardassets/guides/soldering-header-pins.png`, 1200×630 PNG

Use the shared STYLE block. Low-angle macro of a row of header pins standing perfectly straight in a white breadboard used as a jig, with a tiny board lowered onto them. A soldering iron tip touches one corner pin, and a wisp of flux smoke rises in green backlight. Precise and satisfying.

## Set: Components

### resistor-color-codes

- **Title:** Reading resistor color bands
- **Hook:** Those colored stripes are a number. Tap the bands to change them, or type a value and see which colors it takes.
- **Output:** `internal/seo/cardassets/guides/resistor-color-codes.png`, 1200×630 PNG

Use the shared STYLE block. One giant resistor lies horizontally across the whole frame, its beige body ringed with four colored bands (brown, black, red, gold). Colored light from the bands bleeds upward and downward into the background as tall vertical stripes, like a barcode or a graphic equalizer. The bands are the one place where colors other than green are allowed.

### ohms-law-resistors

- **Title:** Ohm’s law, series and parallel
- **Hook:** Volts, ohms and milliamps on one small circuit. Drag the sliders and see the current, and which resistor gets too hot.
- **Output:** `internal/seo/cardassets/guides/ohms-law-resistors.png`, 1200×630 PNG

Use the shared STYLE block. Bird's-eye view of a glowing river delta made of current: one wide channel splits into parallel branches and then rejoins, with narrow resistor-shaped gates along the way. One narrow branch glows hot orange while the rest stay green. Like a map, flat and graphic.

### led-basics

- **Title:** LED basics: which way round, and why it needs a resistor
- **Hook:** The long leg goes to plus. Flip the LED, pull out its resistor and change its color to see what lights and what burns.
- **Output:** `internal/seo/cardassets/guides/led-basics.png`, 1200×630 PNG

Use the shared STYLE block. Two 5 mm LEDs standing upright side by side, with their long and short legs clearly different lengths. The left one glows clean green; the right one is dark, cracked and smoking faintly. Simple, symmetrical, product-shot lighting.

### led-resistor-calculator

- **Title:** Picking the resistor for an LED
- **Hook:** Supply voltage, LED color and current in; the resistor out, rounded to a value you can actually buy.
- **Output:** `internal/seo/cardassets/guides/led-resistor-calculator.png`, 1200×630 PNG

Use the shared STYLE block. A classic two-pan balance scale in green line art. A glowing LED sits on one pan, a small resistor sits on the other, and the beam is perfectly level. Elegant, minimal, centered on a lot of dark space.

### capacitor-types

- **Title:** Capacitor types: ceramic, electrolytic and tantalum
- **Hook:** What 104 means, which leg is negative, and why the voltage on the can matters.
- **Output:** `internal/seo/cardassets/guides/capacitor-types.png`, 1200×630 PNG

Use the shared STYLE block. Family-portrait lineup on a dark shelf, smallest to largest: a tiny ceramic disc capacitor, a teardrop-shaped tantalum and a tall cylindrical electrolytic can with a stripe down one side. No printed markings or values. Dramatic rim light, as in a museum display.

### capacitor-rc-charging

- **Title:** How a capacitor charges through a resistor
- **Hook:** Flip a switch and watch the voltage curve. R times C tells you how long it takes.
- **Output:** `internal/seo/cardassets/guides/capacitor-rc-charging.png`, 1200×630 PNG

Use the shared STYLE block. A smooth exponential curve in glowing green rises from the lower left and levels off toward the right. Beneath it, a capacitor drawn as a glass vessel fills with liquid light up to the curve's height. Elegant, mathematical, airy.

### decoupling-capacitors

- **Title:** Why every chip wants a 100 nF capacitor
- **Hook:** Chips pull current in nanosecond gulps. A small capacitor right at the power pin feeds them.
- **Output:** `internal/seo/cardassets/guides/decoupling-capacitors.png`, 1200×630 PNG

Use the shared STYLE block. Macro of a black chip on a board with a tiny surface-mount capacitor right next to its power pin. The capacitor is shown as a small glowing reservoir feeding quick little pulses of light into the chip. Shallow depth of field; the chip looms large and the capacitor is small but bright.

### bjt-transistor-switch

- **Title:** Switching a bigger load with a transistor
- **Hook:** An ESP32 pin gives a few milliamps at 3.3 V. An NPN transistor like the 2N2222 lets that pin switch a 5 V load drawing far more. Pick the base resistor and see when it fully turns on.
- **Output:** `internal/seo/cardassets/guides/bjt-transistor-switch.png`, 1200×630 PNG

Use the shared STYLE block. A tiny three-legged transistor in a black half-round body acts as a gatekeeper. A thin trickle of light from the left opens a large gate, letting a thick river of current flow to a lamp on the right. The contrast in scale between the trickle and the river is the whole idea.

### mosfet-switch

- **Title:** Switching with a MOSFET from a 3.3 V pin
- **Hook:** A logic-level MOSFET switches amps with almost no current from the pin. Pick a part and see why an IRFZ44N barely turns on at 3.3 V while an IRLZ44N or AO3400 runs cool.
- **Output:** `internal/seo/cardassets/guides/mosfet-switch.png`, 1200×630 PNG

Use the shared STYLE block. Two power MOSFETs in tabbed packages stand side by side, each fed by the same thin wire to its gate. One glows cool green; the other glows hot orange, with heat shimmer rising off its tab. Dramatic side lighting and no part numbers.

### flyback-diodes

- **Title:** Flyback diodes for motors and relays
- **Hook:** Switch off a relay, motor or solenoid and its coil kicks back with a voltage spike. One diode across the coil, band to the positive supply, soaks it up.
- **Output:** `internal/seo/cardassets/guides/flyback-diodes.png`, 1200×630 PNG

Use the shared STYLE block. A relay coil on the left releases a sharp, jagged lightning spike. A diode, drawn as a curved shield-like arrow, catches the spike and loops it back into a gentle circle around the coil, harmlessly. Dynamic, comic-like energy rendered in clean line art.

### bjt-vs-mosfet

- **Title:** Transistor or MOSFET: which to use
- **Hook:** For a small LED or buzzer, a transistor is fine. For amps, a logic-level MOSFET. Set your load and supply and see which part fits, and why.
- **Output:** `internal/seo/cardassets/guides/bjt-vs-mosfet.png`, 1200×630 PNG

Use the shared STYLE block. Top-down view of a single circuit trace that forks in two. The left branch runs to a small transistor and a little buzzer; the right branch runs to a chunky MOSFET and a big motor. A crossroads composition, flat and graphic.

## Set: Raspberry Pi

### pi-imager-setup

- **Title:** Setting up a Raspberry Pi with no keyboard or screen
- **Hook:** Raspberry Pi Imager can set the WiFi, your user and SSH before the Pi ever boots. Switch each one off and see how the Pi fails without it.
- **Output:** `internal/seo/cardassets/guides/pi-imager-setup.png`, 1200×630 PNG

Use the shared STYLE block. A microSD card on the left, with a short checklist of five ticked boxes floating above it, slides toward a small bare, unbranded single-board computer on the right. No keyboard or monitor anywhere; a few faint concentric radio arcs rise from the board (plain arcs, not a WiFi logo). Calm, orderly composition.

### pi-wifi-2-4ghz

- **Title:** Why your Pi never joins the WiFi: 2.4 GHz vs 5 GHz
- **Hook:** You flashed the card, typed the WiFi password, and the Pi never appears. If your network is 5 GHz only, a Zero 2W can’t even see it.
- **Output:** `internal/seo/cardassets/guides/pi-wifi-2-4ghz.png`, 1200×630 PNG

Use the shared STYLE block. A home router on the left sends out two sets of rings, one wide and one tight. A tiny, unbranded stick-shaped single-board computer on the right sits inside the reach of only the wide rings, while the tight rings stop short of it. No text or numbers.

### pi-bookworm-changes

- **Title:** Old Raspberry Pi tutorials on Bookworm: what moved
- **Hook:** config.txt moved, wpa_supplicant.conf is ignored and pip install refuses. Pick a step from an old tutorial and see what it does now, and what to type instead.
- **Output:** `internal/seo/cardassets/guides/pi-bookworm-changes.png`, 1200×630 PNG

Use the shared STYLE block. An open, dog-eared paper tutorial on the left, its steps drawn as plain grey bars (no readable writing), one bar struck through; an arrow leads to a clean terminal window on the right showing only a `>` chevron, a few abstract bars and a block cursor. A small bookworm peeks out of the old paper. Light, friendly mood.

### pi-undervoltage

- **Title:** Why a Raspberry Pi slows down: under-voltage
- **Hook:** Random reboots, SD card errors, WiFi dropping out. Often it isn’t the Pi: the supply or the cable can’t hold 5 V when it gets busy.
- **Output:** `internal/seo/cardassets/guides/pi-undervoltage.png`, 1200×630 PNG

Use the shared STYLE block. A small single-board computer at the end of a long, very thin power cable that sags like a tired rope. A dim yellow lightning bolt hovers above the board. The board sits just right of centre, well inside the frame, and the cable enters from the left edge. Lots of dark space; the thinness of the cable is the whole idea.

### pi-sd-card-corruption

- **Title:** Why pulling the plug corrupts a Pi’s SD card
- **Hook:** Linux is always writing something. Cut the power at the wrong moment and the card can come back damaged. Shut down first, or make the card read-only.
- **Output:** `internal/seo/cardassets/guides/pi-sd-card-corruption.png`, 1200×630 PNG

Use the shared STYLE block. A microSD card in green line art with a hairline crack running across it, and beside it a power plug pulled halfway out of its socket, frozen mid-motion. Minimal, centered, dramatic rim light. No printed markings.

### pi-volumio-streaming

- **Title:** A streaming speaker with Volumio
- **Hook:** Plug a Pi into any speaker and anyone on the WiFi can play to it: from Spotify, an iPhone over AirPlay, or internet radio. No app accounts to share.
- **Output:** `internal/seo/cardassets/guides/pi-volumio-streaming.png`, 1200×630 PNG

Use the shared STYLE block. A tiny bare circuit board sits on top of a large bookshelf speaker. Several generic, unbranded phones and a laptop (screens glowing with abstract shapes, no logos on their backs or lids) around it each send a thin glowing arc of sound waves to the little board, and broad rings of sound spread out from the speaker cone. Plain near-black ground with no room, furniture, plants, string lights or workbench props. Social, uncluttered, no logos or brand names.

### pi-rgb-matrix-flicker

- **Title:** RGB LED panels on a Pi 4: fixing flicker and garbage
- **Hook:** Garbage pixels, shimmer or flicker on a HUB75 panel usually comes down to five settings. Flip them one at a time and watch the panel settle.
- **Output:** `internal/seo/cardassets/guides/pi-rgb-matrix-flicker.png`, 1200×630 PNG

Use the shared STYLE block. A coarse grid of large square LEDs seen face-on, only about sixteen across and eight down: the left half is a scramble of random coloured pixels, and across the middle it settles into a few clean, broad vertical colour bars on the right. Five small toggle switches along the bottom edge, all flipped to on. Plain near-black ground, no tools or other props. Crisp and still, no motion blur.

### pi-find-on-network

- **Title:** Where’s my Pi? Finding a headless Raspberry Pi on the network
- **Hook:** It booted, the light is blinking, and you have no idea what address it has. Try mypi.local, the router’s list or a scan, and see which work on your network.
- **Output:** `internal/seo/cardassets/guides/pi-find-on-network.png`, 1200×630 PNG

Use the shared STYLE block. A small bare, unbranded single-board computer sits alone just right of centre in dark space. On the left an open laptop, its screen glowing with abstract shapes, sends out a few thin concentric search rings toward it, and a simple magnifying-glass outline hovers between them. Lots of negative space, calm and uncluttered.

### pi-models-compared

- **Title:** Zero 2W, Pi 4 or Pi 5: which Raspberry Pi for which job
- **Hook:** A speaker, a sign, a desktop and a sensor on a battery all want different boards. Tick what your project needs and see which Pi fits, and why.
- **Output:** `internal/seo/cardassets/guides/pi-models-compared.png`, 1200×630 PNG

Use the shared STYLE block. Three bare, unbranded single-board computers side by side on a near-black ground, small to large: a slim stick-shaped board on the left and two credit-card-sized boards to its right, the last with a small cooling fan. Each lit by green rim light, evenly spaced across the middle band. No logos and no printed markings.

### pi-gpio-pinout

- **Title:** The Raspberry Pi GPIO header: pin numbers, 3.3 V and what each pin does
- **Hook:** Pin 11 is GPIO 17, the pins run at 3.3 V, and 5 V on one can kill the Pi. Tap any pin on the 40-pin header to see what it does.
- **Output:** `internal/seo/cardassets/guides/pi-gpio-pinout.png`, 1200×630 PNG

Use the shared STYLE block. Macro photograph of a double row of header pins running diagonally across a dark circuit board, shallow depth of field so most pins fall softly out of focus. One pin near the centre glows bright green, with a single jumper wire rising from it. No silkscreen, labels or numbers.

### pi-zero-usb-ports

- **Title:** Pi Zero 2W: which micro-USB port is which
- **Hook:** Two micro-USB ports that look the same. One only takes power; the other is the only data port. Plug a keyboard into the wrong one and nothing happens.
- **Output:** `internal/seo/cardassets/guides/pi-zero-usb-ports.png`, 1200×630 PNG

Use the shared STYLE block. A tiny, bare, unbranded stick-shaped single-board computer lies flat in the middle band, its three small ports along the near edge. Two cables come in from below: a plain power plug reaching the corner port, and a small adapter with a keyboard cable reaching the port beside it. No keyboard shown, no text or markings. Lots of dark space.

### pi4-micro-hdmi

- **Title:** No picture from a Pi 4: the micro-HDMI ports
- **Hook:** A Pi 4 has two tiny HDMI ports, and the screen stays black more often than it should. Pick the port, the cable and when the screen comes on, and see why.
- **Output:** `internal/seo/cardassets/guides/pi4-micro-hdmi.png`, 1200×630 PNG

Use the shared STYLE block. A small, bare, unbranded single-board computer on the left with a thin video cable running to a plain monitor on the right whose screen is black with a faint glow at its edge. Simple line art, no logos, no lettering on the screen.

### pi4-heat-throttling

- **Title:** Why a Pi 4 slows down when it gets hot
- **Hook:** Once its chip gets too hot, a Pi 4 turns its own speed down to cool off. A bare board in a closed box can get there in a couple of minutes.
- **Output:** `internal/seo/cardassets/guides/pi4-heat-throttling.png`, 1200×630 PNG

Use the shared STYLE block. A small, bare, unbranded single-board computer in the centre with soft orange heat shimmer rising from its chip, and beside it a simple thermometer outline whose level sits near the top. A small stick-on heatsink floats just above the chip as if about to land. Dark, uncluttered, no numbers or text.
