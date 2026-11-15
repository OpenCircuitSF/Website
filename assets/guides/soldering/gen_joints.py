#!/usr/bin/env python3
"""Generate the soldering joint cross-section SVGs for #0667.

Writes into the directory given as argv[1]. The SVGs are the committed
source; this generator only saves typing the shared board and defs.
"""
import os
import sys

OUT = sys.argv[1]

DEFS = """  <defs>
    <linearGradient id="shiny" x1="0" y1="0" x2="1" y2="0">
      <stop offset="0" stop-color="#8F99A0"/>
      <stop offset="0.35" stop-color="#E9EEF1"/>
      <stop offset="0.55" stop-color="#C3CBD0"/>
      <stop offset="1" stop-color="#7D868C"/>
    </linearGradient>
    <linearGradient id="dull" x1="0" y1="0" x2="1" y2="0">
      <stop offset="0" stop-color="#7C807E"/>
      <stop offset="0.45" stop-color="#A9ACA8"/>
      <stop offset="1" stop-color="#6F7371"/>
    </linearGradient>
    <linearGradient id="lead" x1="0" y1="0" x2="1" y2="0">
      <stop offset="0" stop-color="#7A8287"/>
      <stop offset="0.4" stop-color="#D7DDE0"/>
      <stop offset="1" stop-color="#6E767B"/>
    </linearGradient>
    <linearGradient id="pcb" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0" stop-color="#3C8A4A"/>
      <stop offset="1" stop-color="#245C30"/>
    </linearGradient>
    <linearGradient id="copper" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0" stop-color="#E9A066"/>
      <stop offset="1" stop-color="#B8672F"/>
    </linearGradient>
  </defs>
"""

OUTLINE = "#1F2621"


def board(x0=20, x1=380):
    return (
        f'  <rect x="{x0}" y="172" width="{x1 - x0}" height="40" rx="3" fill="url(#pcb)" stroke="{OUTLINE}" stroke-width="2"/>\n'
        f'  <rect x="{x0 + 2}" y="174" width="{x1 - x0 - 4}" height="4" fill="#5FAF6B" opacity="0.5"/>\n'
    )


def hole(cx):
    # plated barrel + pads above and below; lead goes through
    return (
        f'  <rect x="{cx - 15}" y="172" width="30" height="40" fill="#123018"/>\n'
        f'  <rect x="{cx - 15}" y="172" width="4" height="40" fill="url(#copper)"/>\n'
        f'  <rect x="{cx + 11}" y="172" width="4" height="40" fill="url(#copper)"/>\n'
        f'  <rect x="{cx - 52}" y="212" width="104" height="6" rx="1" fill="url(#copper)" stroke="{OUTLINE}" stroke-width="1.5"/>\n'
    )


def pad(cx, lifted=False):
    if lifted:
        return (
            f'  <path d="M{cx - 52},172 L{cx - 52},166 L{cx + 50},150 L{cx + 52},156 Z" fill="url(#copper)" stroke="{OUTLINE}" stroke-width="1.5"/>\n'
        )
    return f'  <rect x="{cx - 52}" y="166" width="104" height="6" rx="1" fill="url(#copper)" stroke="{OUTLINE}" stroke-width="1.5"/>\n'


def lead(cx, top=36, bottom=236):
    return (
        f'  <rect x="{cx - 8}" y="{top}" width="16" height="{bottom - top}" rx="3" fill="url(#lead)" stroke="{OUTLINE}" stroke-width="2"/>\n'
    )


def svg(name, body, w=400, h=250):
    text = (
        f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {w} {h}" width="{w}" height="{h}">\n'
        + DEFS
        + body
        + "</svg>\n"
    )
    with open(os.path.join(OUT, name), "w") as f:
        f.write(text)


def good_fillet(cx, top=92):
    # concave "volcano": wets the whole pad, climbs the lead in a smooth curve
    return (
        f'  <path d="M{cx - 54},167 C{cx - 22},164 {cx - 10},146 {cx - 9},{top} L{cx + 9},{top} '
        f'C{cx + 10},146 {cx + 22},164 {cx + 54},167 Z" fill="url(#shiny)" stroke="{OUTLINE}" stroke-width="2"/>\n'
        f'  <path d="M{cx - 40},162 C{cx - 22},158 {cx - 14},140 {cx - 13},112" fill="none" stroke="#FFFFFF" stroke-width="3" stroke-linecap="round" opacity="0.85"/>\n'
    )


CX = 200

# Good: concave volcano, shiny, pad fully wetted, lead outline visible.
svg(
    "joint-good.svg",
    board() + hole(CX) + pad(CX) + lead(CX) + good_fillet(CX),
)

# Bare: no solder yet (used when the solder will not melt).
svg(
    "joint-bare.svg",
    board() + hole(CX) + pad(CX) + lead(CX)
    + f'  <circle cx="{CX + 34}" cy="58" r="16" fill="url(#dull)" stroke="{OUTLINE}" stroke-width="2"/>\n'
    + f'  <path d="M{CX + 46},48 C{CX + 70},30 {CX + 110},26 {CX + 150},30" fill="none" stroke="url(#dull)" stroke-width="10" stroke-linecap="round"/>\n',
)

# Cold: dull, grainy, lumpy, sits on the pad without wetting it; ring crack at the lead.
grain = "".join(
    f'  <circle cx="{x}" cy="{y}" r="{r}" fill="#5E625F" opacity="0.55"/>\n'
    for x, y, r in [
        (166, 150, 2.5), (178, 138, 2), (190, 152, 3), (214, 146, 2.5), (226, 156, 2),
        (232, 140, 2.5), (172, 160, 2), (220, 128, 2), (182, 124, 2.5), (238, 152, 1.8),
        (160, 156, 1.8), (204, 160, 2.2), (196, 134, 1.6), (228, 132, 1.6),
    ]
)
svg(
    "joint-cold.svg",
    board() + hole(CX) + pad(CX) + lead(CX)
    + f'  <path d="M{CX - 44},166 C{CX - 56},150 {CX - 44},118 {CX - 24},112 C{CX - 14},106 {CX - 9},116 {CX - 9},112 '
    f'L{CX + 9},112 C{CX + 12},108 {CX + 26},104 {CX + 38},116 C{CX + 56},130 {CX + 50},158 {CX + 42},166 Z" '
    f'fill="url(#dull)" stroke="{OUTLINE}" stroke-width="2"/>\n'
    + grain
    + f'  <path d="M{CX - 10},114 L{CX - 10},150 M{CX + 10},114 L{CX + 10},150" stroke="#2A2E2B" stroke-width="2.5"/>\n'
    + f'  <path d="M{CX - 50},167 L{CX - 42},164 M{CX + 50},167 L{CX + 42},164" stroke="{OUTLINE}" stroke-width="2"/>\n',
)

# Insufficient (dry): a thin ring on the lead, copper still showing on the pad.
svg(
    "joint-insufficient.svg",
    board() + hole(CX) + pad(CX) + lead(CX)
    + f'  <path d="M{CX - 20},167 C{CX - 12},165 {CX - 9},156 {CX - 9},142 L{CX + 9},142 C{CX + 9},156 {CX + 12},165 {CX + 20},167 Z" '
    f'fill="url(#shiny)" stroke="{OUTLINE}" stroke-width="2"/>\n'
    + f'  <rect x="{CX - 15}" y="172" width="30" height="16" fill="url(#shiny)" opacity="0.9"/>\n',
)

# Blob (too much): a convex ball that hides the lead and the pad edge.
svg(
    "joint-blob.svg",
    board() + hole(CX) + pad(CX) + lead(CX, top=36)
    + f'  <path d="M{CX - 40},167 C{CX - 92},150 {CX - 74},62 {CX},60 C{CX + 74},62 {CX + 92},150 {CX + 40},167 Z" '
    f'fill="url(#shiny)" stroke="{OUTLINE}" stroke-width="2"/>\n'
    + f'  <path d="M{CX - 44},132 C{CX - 52},104 {CX - 36},82 {CX - 16},76" fill="none" stroke="#FFFFFF" stroke-width="4" stroke-linecap="round" opacity="0.85"/>\n'
    + f'  <rect x="{CX - 8}" y="36" width="16" height="26" rx="3" fill="url(#lead)" stroke="{OUTLINE}" stroke-width="2"/>\n',
)

# Disturbed: the lead moved while the solder cooled; wrinkled, frosty surface with cracks.
wrinkles = "".join(
    f'  <path d="{d}" fill="none" stroke="#5B605D" stroke-width="2" stroke-linecap="round"/>\n'
    for d in [
        f"M{CX - 40},160 q8,-10 16,-4 t16,-6",
        f"M{CX - 30},146 q6,-8 12,-2 t10,-8",
        f"M{CX + 16},150 q8,-8 14,0 t14,-4",
        f"M{CX + 14},132 q6,-6 12,0",
        f"M{CX - 24},128 q6,-6 10,0",
    ]
)
DIST = (f"M{CX - 58},167 C{CX - 28},160 {CX - 14},142 {CX - 6},100 L{CX + 12},100 "
        f"C{CX + 18},142 {CX + 28},160 {CX + 58},167 Z")
svg(
    "joint-disturbed.svg",
    board() + hole(CX) + pad(CX)
    # lead visibly tilted
    + f'  <g transform="rotate(5 {CX} 190)">\n' + lead(CX) + "  </g>\n"
    + f'  <clipPath id="fillet"><path d="{DIST}"/></clipPath>\n'
    + f'  <path d="{DIST}" fill="url(#dull)" stroke="{OUTLINE}" stroke-width="2"/>\n'
    + '  <g clip-path="url(#fillet)">\n' + wrinkles + '  </g>\n'
    + f'  <path d="{DIST}" fill="none" stroke="{OUTLINE}" stroke-width="2"/>\n'
    + f'  <path d="M{CX - 4},104 L{CX - 10},126 L{CX - 4},140 M{CX + 12},110 L{CX + 18},128" fill="none" stroke="#262A27" stroke-width="2.5" stroke-linecap="round"/>\n',
)

# Overheated: burnt flux, scorched board, pad lifting off the laminate.
svg(
    "joint-overheated.svg",
    board()
    + f'  <ellipse cx="{CX}" cy="178" rx="96" ry="8" fill="#5A3A1A" opacity="0.75"/>\n'
    + hole(CX) + pad(CX, lifted=True) + lead(CX)
    + f'  <path d="M{CX - 54},170 C{CX - 22},164 {CX - 10},146 {CX - 9},108 L{CX + 9},108 '
    f'C{CX + 10},140 {CX + 20},150 {CX + 52},150 Z" fill="url(#dull)" stroke="{OUTLINE}" stroke-width="2"/>\n'
    + "".join(
        f'  <circle cx="{x}" cy="{y}" r="{r}" fill="#3B2410" opacity="0.9"/>\n'
        for x, y, r in [(CX - 66, 166, 5), (CX + 70, 160, 4), (CX - 76, 170, 3), (CX + 82, 166, 3.5), (CX - 30, 154, 2.5)]
    )
    + f'  <path d="M{CX + 58},146 q4,-12 -2,-22 q-6,-10 0,-22" fill="none" stroke="#9AA29C" stroke-width="3" stroke-linecap="round" opacity="0.8"/>\n'
    + f'  <path d="M{CX - 70},158 q4,-12 -2,-22 q-6,-10 0,-22" fill="none" stroke="#9AA29C" stroke-width="3" stroke-linecap="round" opacity="0.8"/>\n',
)

# Bridge: two neighbouring pins joined by a solder bridge.
L, R = 140, 260


def small_pad(cx):
    return f'  <rect x="{cx - 34}" y="166" width="68" height="6" rx="1" fill="url(#copper)" stroke="{OUTLINE}" stroke-width="1.5"/>\n'


def small_hole(cx):
    return (
        f'  <rect x="{cx - 15}" y="172" width="30" height="40" fill="#123018"/>\n'
        f'  <rect x="{cx - 15}" y="172" width="4" height="40" fill="url(#copper)"/>\n'
        f'  <rect x="{cx + 11}" y="172" width="4" height="40" fill="url(#copper)"/>\n'
    )


svg(
    "joint-bridge.svg",
    board()
    + small_hole(L) + small_hole(R) + small_pad(L) + small_pad(R)
    + lead(L) + lead(R)
    + f'  <path d="M{L - 36},167 C{L - 16},164 {L - 10},146 {L - 9},96 L{L + 9},96 C{L + 10},126 {L + 26},120 {CX},122 '
    f'C{R - 26},120 {R - 10},126 {R - 9},96 L{R + 9},96 C{R + 10},146 {R + 16},164 {R + 36},167 Z" '
    f'fill="url(#shiny)" stroke="{OUTLINE}" stroke-width="2"/>\n'
    + f'  <path d="M{L + 24},128 C{CX - 20},124 {CX + 20},124 {R - 24},128" fill="none" stroke="#FFFFFF" stroke-width="3" stroke-linecap="round" opacity="0.8"/>\n',
)
