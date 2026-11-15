#!/usr/bin/env python3
"""Generate the solder spool and tinning SVGs for #0667 into argv[1]."""
import math
import os
import sys

OUT = sys.argv[1]
OUTLINE = "#1F2621"

DEFS = """  <defs>
    <linearGradient id="shiny" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0" stop-color="#8F99A0"/>
      <stop offset="0.35" stop-color="#E9EEF1"/>
      <stop offset="0.6" stop-color="#C3CBD0"/>
      <stop offset="1" stop-color="#7D868C"/>
    </linearGradient>
    <linearGradient id="insul" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0" stop-color="#E0564A"/>
      <stop offset="0.4" stop-color="#C8372D"/>
      <stop offset="1" stop-color="#8E2119"/>
    </linearGradient>
    <linearGradient id="pcb" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0" stop-color="#3C8A4A"/>
      <stop offset="1" stop-color="#245C30"/>
    </linearGradient>
    <linearGradient id="copper" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0" stop-color="#E9A066"/>
      <stop offset="1" stop-color="#B8672F"/>
    </linearGradient>
    <radialGradient id="flange" cx="0.4" cy="0.35" r="0.75">
      <stop offset="0" stop-color="#6F7E8A"/>
      <stop offset="1" stop-color="#3E4851"/>
    </radialGradient>
  </defs>
"""


def svg(name, body, w=400, h=200):
    with open(os.path.join(OUT, name), "w") as f:
        f.write(
            f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {w} {h}" width="{w}" height="{h}">\n'
            + DEFS + body + "</svg>\n"
        )


def insulation(x1=200, y=100):
    return (
        f'  <rect x="16" y="{y - 18}" width="{x1 - 16}" height="36" rx="10" fill="url(#insul)" stroke="{OUTLINE}" stroke-width="2"/>\n'
        f'  <rect x="24" y="{y - 12}" width="{x1 - 40}" height="5" rx="2.5" fill="#FFFFFF" opacity="0.25"/>\n'
    )


# --- Spool -------------------------------------------------------------
cx, cy = 140, 125
rings = "".join(
    f'  <circle cx="{cx}" cy="{cy}" r="{r}" fill="none" stroke="#8C959B" stroke-width="1.5" opacity="0.7"/>\n'
    for r in range(48, 86, 6)
)
spool = (
    f'  <circle cx="{cx}" cy="{cy}" r="104" fill="url(#flange)" stroke="{OUTLINE}" stroke-width="2.5"/>\n'
    f'  <circle cx="{cx}" cy="{cy}" r="88" fill="#C9D0D5" stroke="{OUTLINE}" stroke-width="2"/>\n'
    + rings
    + f'  <path d="M{cx - 60},{cy - 52} A80,80 0 0 1 {cx + 30},{cy - 74}" fill="none" stroke="#FFFFFF" stroke-width="5" stroke-linecap="round" opacity="0.7"/>\n'
    f'  <circle cx="{cx}" cy="{cy}" r="40" fill="url(#flange)" stroke="{OUTLINE}" stroke-width="2"/>\n'
    f'  <circle cx="{cx}" cy="{cy}" r="16" fill="#151A16" stroke="{OUTLINE}" stroke-width="2"/>\n'
    # the free end of the wire, leaving the spool
    f'  <path d="M{cx + 60},{cy - 64} C{cx + 110},{cy - 80} {cx + 140},{cy - 30} {cx + 176},{cy - 22}" fill="none" stroke="{OUTLINE}" stroke-width="9" stroke-linecap="round"/>\n'
    f'  <path d="M{cx + 60},{cy - 64} C{cx + 110},{cy - 80} {cx + 140},{cy - 30} {cx + 176},{cy - 22}" fill="none" stroke="#C9D0D5" stroke-width="6" stroke-linecap="round"/>\n'
    # magnifier: cut end of the wire showing the flux core
    f'  <path d="M{cx + 180},{cy - 22} L{cx + 196},{cy + 14}" stroke="#87977F" stroke-width="2" stroke-dasharray="4 4"/>\n'
    f'  <circle cx="{cx + 210}" cy="{cy + 50}" r="46" fill="#121712" stroke="#87977F" stroke-width="3"/>\n'
    f'  <circle cx="{cx + 210}" cy="{cy + 50}" r="34" fill="url(#shiny)" stroke="{OUTLINE}" stroke-width="2"/>\n'
    f'  <circle cx="{cx + 210}" cy="{cy + 50}" r="13" fill="#D9A441" stroke="#8A5A00" stroke-width="2"/>\n'
    f'  <circle cx="{cx + 206}" cy="{cy + 46}" r="4" fill="#F4D58A"/>\n'
)
svg("solder-spool.svg", spool, h=250)

# --- Stranded wire, bare and frayed --------------------------------------
strands = []
for i in range(9):
    a = (i - 4) * 5.5
    rad = math.radians(a)
    x0, y0 = 198, 100 + (i - 4) * 2.4
    x1 = x0 + 130 * math.cos(rad)
    y1 = y0 + 130 * math.sin(rad)
    mx = x0 + 60
    my = y0 + (i - 4) * 1.5
    strands.append(
        f'  <path d="M{x0},{y0:.1f} Q{mx},{my:.1f} {x1:.1f},{y1:.1f}" fill="none" stroke="#7A4420" stroke-width="5" stroke-linecap="round"/>\n'
        f'  <path d="M{x0},{y0:.1f} Q{mx},{my:.1f} {x1:.1f},{y1:.1f}" fill="none" stroke="#E39A5E" stroke-width="3" stroke-linecap="round"/>\n'
    )
svg("wire-bare.svg", "".join(strands) + insulation())

# --- Stranded wire, tinned -------------------------------------------------
tinned = (
    f'  <path d="M196,88 L318,90 Q330,100 318,110 L196,112 Z" fill="url(#shiny)" stroke="{OUTLINE}" stroke-width="2"/>\n'
    + "".join(
        f'  <path d="M{x},90 l14,20" stroke="#8F99A0" stroke-width="1.5" opacity="0.7"/>\n' for x in range(204, 312, 12)
    )
    + insulation()
)
svg("wire-tinned.svg", tinned)


def board_with_pad():
    return (
        f'  <rect x="30" y="120" width="340" height="40" rx="3" fill="url(#pcb)" stroke="{OUTLINE}" stroke-width="2"/>\n'
        f'  <rect x="32" y="122" width="336" height="4" fill="#5FAF6B" opacity="0.5"/>\n'
        f'  <rect x="190" y="112" width="150" height="8" rx="1" fill="url(#copper)" stroke="{OUTLINE}" stroke-width="1.5"/>\n'
    )


svg("pad-bare.svg", board_with_pad())
svg(
    "pad-tinned.svg",
    board_with_pad()
    + f'  <path d="M192,113 C210,98 320,98 338,113 Z" fill="url(#shiny)" stroke="{OUTLINE}" stroke-width="2"/>\n'
    + f'  <path d="M220,104 C250,99 290,99 310,103" fill="none" stroke="#FFFFFF" stroke-width="2.5" stroke-linecap="round" opacity="0.8"/>\n',
)

# --- Joined: two tinned surfaces flowed together ----------------------------
svg(
    "joined-good.svg",
    board_with_pad()
    + f'  <path d="M192,113 C214,110 222,100 230,98 L318,98 C328,100 332,110 338,113 Z" fill="url(#shiny)" stroke="{OUTLINE}" stroke-width="2"/>\n'
    + f'  <path d="M186,95 L318,95 Q328,103 318,111 L186,111 Z" fill="url(#shiny)" stroke="{OUTLINE}" stroke-width="1.5"/>\n'
    + "".join(f'  <path d="M{x},96 l10,14" stroke="#8F99A0" stroke-width="1.5" opacity="0.6"/>\n' for x in range(194, 312, 12))
    + f'  <path d="M200,112 C216,108 222,104 230,103" fill="none" stroke="#FFFFFF" stroke-width="2.5" stroke-linecap="round" opacity="0.8"/>\n'
    + insulation(x1=186, y=103),
)

# --- Joined untinned: a blob on frayed strands, solder wicked under the insulation
frayed = []
for i, (dx, dy) in enumerate([(150, -34), (154, -10), (146, 14), (136, -48), (150, 30)]):
    frayed.append(
        f'  <path d="M182,{94 + (i - 2) * 3} Q240,{94 + dy / 2:.0f} {182 + dx},{94 + dy}" fill="none" stroke="#7A4420" stroke-width="5" stroke-linecap="round"/>\n'
        f'  <path d="M182,{94 + (i - 2) * 3} Q240,{94 + dy / 2:.0f} {182 + dx},{94 + dy}" fill="none" stroke="#E39A5E" stroke-width="3" stroke-linecap="round"/>\n'
    )
svg(
    "joined-blob.svg",
    board_with_pad()
    + "".join(frayed)
    + f'  <path d="M218,112 C196,104 200,58 248,54 C296,52 304,102 280,112 Z" fill="url(#shiny)" stroke="{OUTLINE}" stroke-width="2"/>\n'
    + f'  <path d="M220,86 C222,72 232,64 244,62" fill="none" stroke="#FFFFFF" stroke-width="3" stroke-linecap="round" opacity="0.8"/>\n'
    + insulation(x1=180, y=94)
    # solder wicked up under the insulation: a stiff silver bulge
    + f'  <path d="M150,78 C162,72 176,74 182,80 L182,108 C176,114 162,116 150,110 Z" fill="url(#shiny)" opacity="0.85" stroke="{OUTLINE}" stroke-width="1.5"/>\n',
)
