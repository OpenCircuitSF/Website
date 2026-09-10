# EU CRT screen studio

A live terminal screen projectively warped onto a photograph of a rounded
European portable television, with everything about it editable in the page:
where the glass is, what the terminal says, and how the phosphor is drawn.

**Open `index.html`.** Double-click it from the Finder — it needs no server and
no build step, because every asset is inlined into it. `index.src.html` is the
file you *edit*; `index.html` is generated from it and carries a do-not-edit
banner.

The workflow this is built for is: fiddle in the page until it looks right,
download the JSON, paste that JSON back into the source as the new defaults,
then hide the controls. The four sections below are that loop in order.

---

## 1. Edit the corners, the commands and the output

### The corners

The live screen is a flat 640×512 rectangle mapped onto four points on the
photograph by a projective transform — real perspective, not a rotate/skew. The
four points are the **corners of the glass**, given as percentages of the
photograph, so the fit survives any resize of the page.

1. Tick **Show corner handles**. Four green dots appear on the stage, joined by
   a dashed quadrilateral.
2. Drag each one onto its corner of the glass. The screen re-warps as you drag.
3. For fine work use the keyboard: focus a handle with <kbd>Tab</kbd>, then
   arrow keys move it 0.1%, <kbd>Shift</kbd>+arrows 1%.
4. Or type the numbers straight into the **Top left / Top right / Bottom right
   / Bottom left** boxes.

**Reset corners** puts back the measured starting values.

> **Each theme has its own calibration.** The two photographs are *not*
> registered to each other — the light one is about 2% larger and sits further
> left — so one quadrilateral cannot serve both. The heading tells you which
> one you are editing (`Screen corners — dark photograph`). Switch the theme
> with the **Auto / Light / Dark** button at the top right and calibrate the
> other one too, or the theme you did not check will be visibly off.

The quadrilateral wants to be the **bounding box of the glass** — touching the
middle of each edge — not tucked inside the rounded corners. The plane is
clipped to a rounded shape of its own, so its corners will not spill onto the
bezel.

### The commands and output

Under **Session — commands and output**, each step is one command and the lines
it prints.

| Control | Does |
|---|---|
| **+ Add command** | appends a new step and focuses its command field |
| **↑** / **↓** | reorder |
| **⧉** | duplicate |
| **✕** | remove |
| **Restart session** | replays from the boot lines |

Type the command **without** the `>` prompt — that is added when it is drawn.
Put **one output line per row** in the box below it; a blank row is a blank line
on the screen, which is a legitimate thing to want.

Lines are **not wrapped**. Anything longer than fits across the raster runs off
the glass, so the editor measures every line and flags the step:

    2 line(s) over 29 chars — will run off the glass

That budget is computed from the actual font advance at the current size, and it
shrinks as `bulge` grows, because the bulge pushes the end of a line further out
than where it was laid out. Raising `font px` or `pad left` lowers it.

Edits to a command or its output take effect on the **next pass** of the loop —
the session runs continuously, so you do not need to restart it. Adding,
removing or reordering a step restarts immediately.

### Render options

| Option | What it does |
|---|---|
| `font px` | glyph size on the 640×512 raster |
| `line height` | baseline-to-baseline spacing |
| `pad left`, `pad top` | inset of the text block from the raster's edge |
| `max lines` | how many lines are visible — **this is what makes it scroll**; pushing a line past this shifts everything up by one |
| `bulge` | how far the glyphs bow outward off the middle of the tube. `0` draws flat; `0.07` is the default and reads as a curved tube; past `0.2` it reads as a fisheye |
| `type ms/char` | typing speed |
| `pause ms` | how long a finished command is held before the next one types |
| `phosphor`, `cursor` | any CSS colour |

**Scanlines** and **glass vignette** are off by default. The photograph already
carries its own scan texture and its own corner falloff, so both of these double
something that is already in the picture — over the light photograph the pair
reads as a grey wash sitting on the glass rather than as the glass. They are
worth having over a flatter photo, which is why they are still switches.

---

## 2. Download the JSON

The **Data** box at the bottom is the entire state of the page — both corner
calibrations, every command, and every render option — as one JSON document. It
is rewritten on every edit, so it is always current.

- **Download .json** writes `eu-crt-screen.json` to your downloads folder.
- **Copy** puts the same text on the clipboard. If the browser blocks the
  clipboard (some do over `file://`) it selects the text instead and tells you
  to press <kbd>⌘C</kbd>.
- Or just select the text in the box by hand. It is a plain textarea.

Keep the file wherever you keep such things. It is the only thing you need to
reproduce a screen exactly.

To restore one: paste it into the box and press **Load from text**. A document
that is not valid JSON, or whose shape is wrong, is **rejected with a message
and the live screen is left exactly as it was** — it never half-loads. A
document that is valid but missing a field takes the default for that field, so
a fragment is a bad thing to paste; see §4.

**Reset everything** returns the page to the defaults baked into it.

---

## 3. Hide the inputs

One boolean, near the top of the script in `index.src.html`:

```js
const SHOW_CONTROLS = true;
```

Set it to `false` and the page becomes nothing but the CRT — no panel, no page
header, no corner handles — centred on the page ground and grown until either
the viewport's width or its height runs out, keeping the photograph's aspect
ratio. That is the presentation artefact: send it, project it, screenshot it.

Then rebuild:

```sh
python3 build.py
```

and open `index.html`.

> `index.html` is generated. Editing it directly works and is fine for a quick
> look, but the next `build.py` overwrites it. Anything you want to keep goes in
> `index.src.html`.

Set it back to `true` and rebuild to get the studio again. Nothing else changes
between the two modes — the screen renders identically.

---

## 4. Bake your JSON in as the new default

§2 gets you a file you have to load by hand every time. To make your content
*be* the page — which is what you want before hiding the inputs, since a hidden
panel is a panel nobody can paste into — replace the defaults in the source.

1. Get the screen how you want it and press **Download .json** (or **Copy**).
2. Open `index.src.html` and find:

   ```js
   const DEFAULTS = {
     corners: {
   ```

   It is about 15 lines below `const SHOW_CONTROLS`, and it runs to the `};`
   after the last `session` entry — roughly 50 lines in all.
3. Replace **the whole object literal** with your JSON, keeping the
   `const DEFAULTS = ` and the trailing `;`:

   ```js
   const DEFAULTS = {
     "corners": { … },
     "options": { … },
     "session": [ … ]
   };
   ```

   JSON is valid JavaScript here, so the quoted keys the download produces are
   fine as-is. There is no need to reformat it.
4. `python3 build.py`, then open `index.html`.

**Paste the whole document, never a fragment.** The downloaded JSON always is
one, so this only bites if you hand-write a replacement. `DEFAULTS` is not only
the starting state — it is also what **Reset everything** restores, and what
supplies any field a *later* pasted document leaves out. A `DEFAULTS` missing
its `corners` or its `options` key will throw the first time somebody presses
Load.

Two consequences worth knowing:

- The comments currently inside that object (what `bulge` means, why the
  overlays are off) are prose, not data, and pasting over them deletes them.
  That is expected — the explanations live in this file and in `placeGlyph()`.
- Corner calibrations for **both** themes travel in the document. If you only
  ever looked at the dark one, the light one is still carrying the measured
  starting values, which is fine; it is not carrying whatever you dragged.

---

## Files

| Path | What it is |
|---|---|
| `index.html` | **the artefact — open this.** Generated, every asset inlined, opens over `file://`, sends on its own |
| `index.src.html` | the source. Edit this |
| `build.py` | regenerates `index.html`; also derives the `.webp` photographs and checks the fonts against `web/public/fonts/` |
| `assets/` | the two photographs and three webfonts, inlined at build time |
| `eu-crt-dark.png`, `eu-crt-light.png` | the photographs as delivered |
| `tools/key-light-photo.py` | reconstructs the light photograph's alpha channel — see below |

`eu-crt-light.png` arrived as opaque RGB with the editor's **transparency
checkerboard baked into its pixels** (somebody exported a screenshot of the
canvas rather than the canvas). Dropped on the page it renders the checkerboard,
which reads as a broken image. `tools/key-light-photo.py` keys it back out using
the checkerboard's own grid structure; its docstring explains why a flood fill
does not work here. If a properly exported RGBA original turns up, replace the
PNG, delete the script, and drop its entry from `build.py`'s `KEYED` map.

## Rebuilding

```sh
python3 build.py            # regenerate index.html
python3 build.py --photos   # ...and re-derive assets/*.webp from the PNGs
python3 build.py --sync     # ...and refresh the fonts from web/public/fonts/
```

Run inside the repository, `build.py` compares the fonts in `assets/` against
`web/public/fonts/` by SHA-256 and reports drift. Run from an unzipped copy with
no repository around it, there is nothing to compare against, so it says so and
carries on.
