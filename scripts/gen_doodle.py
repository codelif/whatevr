#!/usr/bin/env python3
"""Generate the tiled chat-wallpaper doodle (whatkevr/data/wallpapers/doodle.svg).

One self-contained generator: a library of line-art motifs, a toroidal
(seamlessly tiling) layout solver, and an SVG renderer, driven by a single seed
so a given version always produces the same wallpaper.

Layout of this file, top to bottom:
  1. Tunables        — counts, sizes, colours; tweak these first.
  2. Motif library   — the doodles, grouped by theme. Add shapes here.
  3. Registries      — REG / FILLER pick which motifs are used and how big.
  4. Generator       — placement + relaxation + SVG rendering.
  5. CLI             — `--seed` (defaults to the repo version) and `--output`.

Motifs are drawn in their own local coordinates around (0, 0); strokes inherit
`fill="none"` and `stroke=STROKE`, while solid accents use the `__DOT__`
placeholder (swapped for DOT_COLOUR at render time). Stdlib only.
"""

from __future__ import annotations

import argparse
import math
import random
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DEFAULT_OUTPUT = ROOT / "whatkevr" / "data" / "wallpapers" / "doodle.svg"


# ============================== 1. tunables ===============================
# The whole look is controlled from here.

TILE = 880            # SVG tile size in px; the motif tiles seamlessly at this period.
N_BIG = 9             # count of large "hero" motifs.
N_MED = 42            # count of medium motifs.
N_SMALL = 60          # count of small icons / symbols.
N_FILL = 95           # tiny filler glyphs dropped into the remaining gaps.
MARGIN = 9            # minimum gap kept between motifs, in px.
RELAX_ITERS = 100     # repulsion-relaxation passes for even spacing.

STROKE = "#000000"        # line colour.
STROKE_WIDTH = 2.3        # line width in px.
DOT_COLOUR = "#000000"    # filled accents (eyes, dots, ...). Replaces __DOT__.


# ============================ 2. motif library ============================
# Each motif is a zero-arg function returning SVG fragment(s) drawn around the
# origin. Shared primitives come first.

def d(cx, cy, r):
    return f'<circle cx="{cx:.1f}" cy="{cy:.1f}" r="{r:.1f}" fill="__DOT__" stroke="none"/>'

def poly(pts):
    return '<polygon points="' + ' '.join(f'{x:.1f},{y:.1f}' for x, y in pts) + '"/>'

def star_small(cx, cy, r):
    pts = []
    for i in range(10):
        a = -math.pi / 2 + i * math.pi / 5
        rr = r if i % 2 == 0 else r * 0.42
        pts.append((cx + rr * math.cos(a), cy + rr * math.sin(a)))
    return poly(pts)

# ----------------------------- general -----------------------------
def coffee():
    return ('<path d="M-22,-6 L22,-6 L17,26 Q16,32 10,32 L-10,32 Q-16,32 -17,26 Z"/>'
            '<path d="M22,0 Q35,1 35,11 Q35,21 21,19"/>'
            '<path d="M-9,-20 Q-4,-26 -9,-32"/><path d="M3,-20 Q8,-26 3,-32"/>')
def heart():
    return ('<path d="M0,12 C-3,5 -16,-1 -16,-13 C-16,-22 -5,-23 0,-13 '
            'C5,-23 16,-22 16,-13 C16,-1 3,5 0,12 Z"/>')
def star5():
    pts = []
    for i in range(10):
        a = -math.pi / 2 + i * math.pi / 5
        r = 22 if i % 2 == 0 else 9
        pts.append((r * math.cos(a), r * math.sin(a)))
    return poly(pts)
def sun():
    s = '<circle cx="0" cy="0" r="13"/>'
    for i in range(8):
        a = i * math.pi / 4
        s += f'<line x1="{19*math.cos(a):.1f}" y1="{19*math.sin(a):.1f}" x2="{28*math.cos(a):.1f}" y2="{28*math.sin(a):.1f}"/>'
    return s
def cloud():
    return '<path d="M-26,12 a11,11 0 0 1 2,-22 a15,15 0 0 1 28,-4 a12,12 0 0 1 8,26 Z"/>'
def note():
    return ('<path d="M-6,18 a8,5 0 1 0 12,0 a8,5 0 1 0 -12,0"/>'
            '<path d="M6,16 L6,-22 L22,-26 L22,-12"/>'
            '<path d="M10,-12 a7,4 0 1 0 12,0 a7,4 0 1 0 -12,0"/>'
            '<path d="M6,-22 L22,-26"/>')
def smiley():
    return ('<circle cx="0" cy="0" r="22"/><path d="M-10,4 Q0,14 10,4"/>' + d(-8, -6, 2.4) + d(8, -6, 2.4))
def plane():
    return ('<path d="M-26,-2 L26,-20 L8,24 L2,6 Z"/><path d="M-26,-2 L2,6"/><path d="M2,6 L26,-20"/>')
def bolt():
    return poly([(6, -26), (-16, 6), (-2, 6), (-8, 26), (18, -8), (2, -8)])
def balloon():
    return ('<ellipse cx="0" cy="-8" rx="15" ry="18"/><path d="M0,10 l-3,4 l6,0 Z"/>'
            '<path d="M0,14 q5,5 -1,11 q-5,6 -1,12"/>')
def gift():
    return ('<rect x="-20" y="-6" width="40" height="30" rx="2"/>'
            '<rect x="-23" y="-16" width="46" height="12" rx="2"/>'
            '<line x1="0" y1="-16" x2="0" y2="24"/>'
            '<path d="M0,-16 C-14,-16 -14,-30 0,-16 C14,-30 14,-16 0,-16 Z"/>')
def camera():
    return ('<rect x="-26" y="-14" width="52" height="34" rx="5"/>'
            '<path d="M-12,-14 l4,-7 l16,0 l4,7"/><circle cx="0" cy="4" r="11"/>'
            '<circle cx="0" cy="4" r="5"/>' + d(17, -7, 2.4))
def umbrella():
    return ('<path d="M-28,2 A28,28 0 0 1 28,2 Q21,-6 14,2 Q7,-6 0,2 Q-7,-6 -14,2 Q-21,-6 -28,2 Z"/>'
            '<line x1="0" y1="2" x2="0" y2="26"/><path d="M0,26 q0,8 -9,8"/>')
def sprout():
    return ('<path d="M0,28 L0,-6"/><path d="M0,2 C-16,2 -20,-12 -20,-12 C-6,-14 0,-2 0,2 Z"/>'
            '<path d="M0,-4 C14,-4 18,-20 18,-20 C4,-20 0,-8 0,-4 Z"/>')
def flower():
    s = ''
    for i in range(6):
        a = i * math.pi / 3
        s += f'<circle cx="{15*math.cos(a):.1f}" cy="{15*math.sin(a):.1f}" r="9"/>'
    return s + '<circle cx="0" cy="0" r="6"/>'
def moon():
    return ('<path d="M10,-22 A22,22 0 1 0 10,22 A17,17 0 1 1 10,-22 Z"/>' + star_small(20, -14, 4) + star_small(24, 4, 3))
def bubble():
    return ('<path d="M-24,-16 H24 a6,6 0 0 1 6,6 V8 a6,6 0 0 1 -6,6 H-6 l-10,10 l1,-10 H-24 a6,6 0 0 1 -6,-6 V-10 a6,6 0 0 1 6,-6 Z"/>'
            + d(-12, -1, 2.6) + d(0, -1, 2.6) + d(12, -1, 2.6))
def clock():
    s = '<circle cx="0" cy="0" r="22"/>'
    for i in range(12):
        a = i * math.pi / 6
        s += f'<line x1="{18*math.cos(a):.1f}" y1="{18*math.sin(a):.1f}" x2="{22*math.cos(a):.1f}" y2="{22*math.sin(a):.1f}"/>'
    return s + '<line x1="0" y1="0" x2="0" y2="-14"/><line x1="0" y1="0" x2="10" y2="5"/>'
def diamond():
    return ('<path d="M-22,-8 L-12,-22 L12,-22 L22,-8 L0,24 Z"/><path d="M-22,-8 H22"/>'
            '<path d="M-12,-22 L-6,-8 L0,24 L6,-8 L12,-22"/><path d="M-6,-8 H6"/>')
def bulb():
    return ('<path d="M0,-26 A18,18 0 0 1 11,8 Q9,12 9,16 H-9 Q-9,12 -11,8 A18,18 0 0 1 0,-26 Z"/>'
            '<line x1="-8" y1="20" x2="8" y2="20"/><line x1="-6" y1="25" x2="6" y2="25"/>'
            '<path d="M-5,8 L-2,-4 L2,-4 L5,8"/>')
def envelope():
    return '<rect x="-26" y="-16" width="52" height="34" rx="3"/><path d="M-26,-13 L0,6 L26,-13"/>'
def raindrop():
    return '<path d="M0,-22 C12,-6 16,4 12,12 A13,13 0 1 1 -12,12 C-16,4 -12,-6 0,-22 Z"/><path d="M-6,10 a8,8 0 0 0 4,8"/>'
def flame():
    return ('<path d="M0,28 C-14,20 -17,4 -7,-8 C-6,2 1,3 -1,-12 C9,-6 17,4 13,18 C11,25 5,28 0,28 Z"/>'
            '<path d="M0,28 C-6,24 -7,16 -2,10 C0,16 4,16 4,10 C8,14 8,22 3,26 Z"/>')
def snowflake():
    s = ''
    for i in range(6):
        a = i * math.pi / 3
        x, y = math.cos(a), math.sin(a)
        s += f'<line x1="0" y1="0" x2="{22*x:.1f}" y2="{22*y:.1f}"/>'
        for t in (10, 16):
            bx, by = t * x, t * y
            px, py = math.cos(a + 0.5), math.sin(a + 0.5)
            nx, ny = math.cos(a - 0.5), math.sin(a - 0.5)
            s += f'<line x1="{bx:.1f}" y1="{by:.1f}" x2="{bx+6*px:.1f}" y2="{by+6*py:.1f}"/>'
            s += f'<line x1="{bx:.1f}" y1="{by:.1f}" x2="{bx+6*nx:.1f}" y2="{by+6*ny:.1f}"/>'
    return s
def kite():
    return ('<path d="M0,-26 L18,2 L0,22 L-18,2 Z"/><line x1="0" y1="-26" x2="0" y2="22"/>'
            '<line x1="-18" y1="2" x2="18" y2="2"/><path d="M0,22 q-4,8 2,12 q6,4 0,12"/>')
def crown():
    return ('<path d="M-22,16 L-22,-10 L-10,2 L0,-16 L10,2 L22,-10 L22,16 Z"/>'
            '<line x1="-22" y1="16" x2="22" y2="16"/>' + d(-22, -12, 2.4) + d(0, -18, 2.4) + d(22, -12, 2.4))
def bell():
    return ('<path d="M0,-20 a4,4 0 0 1 4,4 c8,3 10,12 10,20 q0,6 5,10 H-19 q5,-4 5,-10 c0,-8 2,-17 10,-20 a4,4 0 0 1 4,-4 Z"/>'
            '<path d="M-6,18 a6,5 0 0 0 12,0"/>')

# ----------------------------- food -----------------------------
def taco():
    return ('<path d="M-26,14 A30,30 0 0 1 26,14"/><line x1="-26" y1="14" x2="26" y2="14"/>'
            '<path d="M-21,11 q5,-9 9,0 q5,-9 9,0 q5,-9 9,0 q4,-7 7,-1"/>'
            + d(-12, 7, 2.2) + d(6, 7, 2.2))
def icecream():
    return ('<path d="M-13,-2 L0,30 L13,-2"/><line x1="-9" y1="6" x2="2" y2="2"/><line x1="-5" y1="16" x2="6" y2="12"/>'
            '<path d="M-14,-2 a14,12 0 0 1 28,0 Z"/><circle cx="-5" cy="-8" r="7"/><circle cx="7" cy="-6" r="7"/>'
            + d(0, -18, 2.6))
def watermelon():
    return ('<path d="M-28,-10 A30,30 0 0 0 28,-10 Z"/><path d="M-23,-10 A25,25 0 0 0 23,-10"/>'
            + d(-8, 2, 2) + d(6, 6, 2) + d(0, 12, 2) + d(12, 0, 2))
def pizza():
    return ('<path d="M0,28 L-19,-16 A42,42 0 0 1 19,-16 Z"/><path d="M-19,-16 A42,42 0 0 1 19,-16"/>'
            + d(-4, -4, 3) + d(6, 4, 3) + d(-2, 12, 2.4))
def donut():
    s = '<circle r="20"/><circle r="8"/>'
    for i in range(8):
        a = i * math.pi / 4
        bx, by = 14 * math.cos(a), 14 * math.sin(a)
        s += f'<line x1="{bx-2:.1f}" y1="{by:.1f}" x2="{bx+2:.1f}" y2="{by:.1f}" transform="rotate({a*57:.0f} {bx:.1f} {by:.1f})"/>'
    return s
def friedegg():
    return ('<path d="M-18,4 q-8,-16 8,-16 q6,-10 16,0 q14,0 8,14 q6,12 -8,12 q-10,8 -18,-2 q-12,0 -6,-8 Z"/>'
            '<circle cx="2" cy="0" r="8"/>')
def mushroom():
    return ('<path d="M-20,4 A20,15 0 0 1 20,4 Z"/><line x1="-20" y1="4" x2="20" y2="4"/>'
            '<path d="M-7,4 L-6,18 q6,5 12,0 L7,4"/>' + d(-7, -4, 2.4) + d(6, -2, 3))
def apple():
    return ('<path d="M0,-10 C-5,-19 -17,-16 -17,-2 C-17,12 -8,20 0,17 C8,20 17,12 17,-2 C17,-16 5,-19 0,-10 Z"/>'
            '<path d="M0,-10 q1,-8 -2,-12"/><path d="M0,-16 q8,-6 12,0 q-8,4 -12,0 Z"/>')
def cherry():
    return ('<circle cx="-8" cy="14" r="7"/><circle cx="10" cy="16" r="7"/>'
            '<path d="M-8,7 C-6,-6 4,-10 4,-16"/><path d="M10,9 C8,-4 6,-12 4,-16"/>'
            '<path d="M4,-16 q10,-2 14,2 q-10,4 -14,-2 Z"/>')
def hotpepper():
    return ('<path d="M4,-14 q6,4 2,10 q12,6 4,18 q-9,12 -19,2 q-7,-8 3,-13 q9,-5 7,-17 Z"/>'
            '<path d="M4,-14 q-2,-6 4,-6 q4,0 6,4"/>')
def lollipop():
    return ('<circle r="13"/><path d="M0,0 a3,3 0 1 1 3,3 a6,6 0 1 1 -6,6 a9,9 0 1 1 9,9"/>'
            '<line x1="-7" y1="11" x2="-7" y2="30"/>')
def fish():
    return ('<path d="M14,0 Q2,-12 -10,-9 Q-20,-6 -20,0 Q-20,6 -10,9 Q2,12 14,0 Z"/>'
            '<path d="M14,0 L26,-9 L26,9 Z"/>' + d(-12, -2, 2.2) + '<path d="M-2,-9 q4,4 0,9"/>')
def corn():
    return ('<path d="M0,-22 q11,6 11,22 q0,16 -11,22 q-11,-6 -11,-22 q0,-16 11,-22 Z"/>'
            '<line x1="0" y1="-20" x2="0" y2="20"/><line x1="-6" y1="-12" x2="6" y2="-8"/>'
            '<line x1="-7" y1="0" x2="7" y2="4"/><line x1="-6" y1="12" x2="6" y2="16"/>'
            '<path d="M-9,18 q-10,2 -8,12"/><path d="M9,18 q10,2 8,12"/>')
def cupcake():
    return ('<path d="M-15,2 L15,2 L11,24 L-11,24 Z"/><line x1="-9" y1="2" x2="-7" y2="24"/>'
            '<line x1="0" y1="2" x2="0" y2="24"/><line x1="9" y1="2" x2="7" y2="24"/>'
            '<path d="M-16,2 q0,-12 8,-12 q2,-10 8,-6 q10,-4 8,8 q8,2 0,10 Z"/>' + d(0, -18, 2.6))

# ----------------------------- animals / nature -----------------------------
def cat():
    return ('<path d="M-15,30 C-19,8 -13,-1 0,-1 C13,-1 19,8 15,30 Z"/>'
            '<path d="M-12,-20 L-16,-33 L-4,-25 Z"/><path d="M12,-20 L16,-33 L4,-25 Z"/>'
            '<circle cx="0" cy="-12" r="13"/>' + d(-5, -13, 2) + d(5, -13, 2) +
            '<path d="M0,-9 l-2,2 h4 Z" fill="__DOT__" stroke="none"/>'
            '<path d="M-2,-6 q2,2 4,0"/>'
            '<line x1="-7" y1="-9" x2="-16" y2="-11"/><line x1="-7" y1="-7" x2="-16" y2="-6"/>'
            '<line x1="7" y1="-9" x2="16" y2="-11"/><line x1="7" y1="-7" x2="16" y2="-6"/>'
            '<path d="M15,28 q14,-1 12,-15"/>')
def foxhead():
    return ('<path d="M0,20 L-19,-4 L-25,-26 L-9,-15 L0,-19 L9,-15 L25,-26 L19,-4 Z"/>'
            + d(-7, -6, 2.2) + d(7, -6, 2.2) + '<path d="M0,6 l-3,-4 h6 Z" fill="__DOT__" stroke="none"/>'
            '<path d="M-9,-15 L-6,-22 M9,-15 L6,-22"/>')
def butterfly():
    return ('<line x1="0" y1="-14" x2="0" y2="16"/>'
            '<path d="M0,-6 C-22,-24 -26,-2 -3,2"/><path d="M0,-6 C22,-24 26,-2 3,2"/>'
            '<path d="M0,4 C-18,8 -18,24 -3,16"/><path d="M0,4 C18,8 18,24 3,16"/>'
            '<path d="M0,-14 q-6,-8 -10,-6"/><path d="M0,-14 q6,-8 10,-6"/>'
            + d(-10, -20, 2) + d(10, -20, 2))
def pinetree():
    return ('<path d="M0,-28 L-13,-6 L-7,-6 L-17,8 L-8,8 L-20,22 L20,22 L8,8 L17,8 L7,-6 L13,-6 Z"/>'
            '<rect x="-4" y="22" width="8" height="8"/>')
def cactus():
    return ('<path d="M-5,30 L-5,-8 q0,-12 5,-12 q5,0 5,12 L5,30 Z"/>'
            '<path d="M-5,2 q-11,0 -11,-9 q0,-5 -4,-5"/>'
            '<path d="M5,-4 q11,0 11,-9 q0,-5 4,-5"/>' + star_small(0, -22, 4))
def palmtree():
    return ('<path d="M-3,30 Q0,4 -2,-12"/>'
            '<path d="M-2,-12 Q-18,-18 -26,-10"/><path d="M-2,-12 Q-20,-8 -24,4"/>'
            '<path d="M-2,-12 Q4,-28 18,-26"/><path d="M-2,-12 Q16,-20 26,-12"/>'
            '<path d="M-2,-12 Q12,-6 16,4"/>' + d(-2, -12, 2.6))
def snail():
    return ('<path d="M2,4 a12,12 0 1 1 -4,-11 a7,7 0 1 1 2,6 a3,3 0 1 1 -2,-2"/>'
            '<path d="M-22,18 q-3,-9 9,-12 L0,2"/><path d="M-22,18 q-4,2 -8,0"/>'
            '<path d="M-20,8 l-4,-8 M-15,7 l-1,-9"/>' + d(-24, 0, 1.8) + d(-16, -2, 1.8))
def turtle():
    return ('<path d="M-22,8 A22,15 0 0 1 22,8 Z"/><line x1="-22" y1="8" x2="22" y2="8"/>'
            '<path d="M-11,-6 L-6,-2 L-9,5 L-15,3 Z M11,-6 L6,-2 L9,5 L15,3 Z M-3,-9 L3,-9 L4,-2 L-4,-2 Z"/>'
            '<path d="M22,2 q8,-2 9,4"/>' + d(28, 4, 1.8) +
            '<path d="M-16,9 l-3,7 M16,9 l3,7 M-7,11 l-2,7 M7,11 l2,7"/>')
def pawprint():
    return (d(0, 8, 9) + d(-12, -6, 4.5) + d(-4, -13, 4.5) + d(4, -13, 4.5) + d(12, -6, 4.5))
def leaf():
    return ('<path d="M0,-24 C16,-14 16,16 0,24 C-16,16 -16,-14 0,-24 Z"/>'
            '<line x1="0" y1="-20" x2="0" y2="22"/>'
            '<path d="M0,-8 L-10,-14 M0,0 L-12,-4 M0,8 L-10,8 M0,-8 L10,-14 M0,0 L12,-4 M0,8 L10,8"/>')
def mountain():
    return ('<path d="M-28,18 L-9,-15 L1,1 L11,-19 L28,18 Z"/>'
            '<path d="M-13,-9 L-9,-15 L-5,-9 L-8,-6 L-11,-8 Z"/>'
            '<path d="M7,-11 L11,-19 L15,-11 L12,-8 L9,-9 Z"/>')
def seashell():
    s = '<path d="M0,20 C-18,20 -24,2 -18,-12 C-12,-22 12,-22 18,-12 C24,2 18,20 0,20 Z"/>'
    for k in (-12, -6, 0, 6, 12):
        s += f'<path d="M{k*0.2:.1f},20 Q{k:.1f},-4 {k*0.8:.1f},-18"/>'
    return s

# ----------------------------- objects -----------------------------
def book():
    return ('<path d="M0,-12 C-8,-18 -22,-16 -24,-14 L-24,14 C-22,12 -8,14 0,18 C8,14 22,12 24,14 L24,-14 C22,-16 8,-18 0,-12 Z"/>'
            '<line x1="0" y1="-12" x2="0" y2="18"/>'
            '<path d="M-19,-9 q8,-3 14,1 M-19,-2 q8,-3 14,1 M5,-8 q8,-4 14,0 M5,-1 q8,-4 14,0"/>')
def key():
    return ('<circle cx="-12" cy="0" r="9"/><circle cx="-12" cy="0" r="3"/>'
            '<line x1="-3" y1="0" x2="22" y2="0"/><line x1="16" y1="0" x2="16" y2="8"/><line x1="22" y1="0" x2="22" y2="7"/>')
def scissors():
    return ('<circle cx="-12" cy="10" r="6"/><circle cx="-12" cy="-10" r="6"/>'
            '<line x1="-7" y1="7" x2="22" y2="-10"/><line x1="-7" y1="-7" x2="22" y2="10"/>'
            + d(2, 0, 1.6))
def pencil():
    return ('<path d="M-22,18 L12,-16 L20,-8 L-14,26 Z"/><path d="M12,-16 L20,-8"/>'
            '<path d="M-22,18 L-16,20 L-14,26 Z" fill="__DOT__" stroke="none"/>'
            '<line x1="14" y1="-14" x2="18" y2="-10"/>')
def paintbrush():
    return ('<rect x="-4" y="-26" width="8" height="22" rx="2" transform="rotate(35)"/>'
            '<path d="M-6,8 q-10,8 -16,22 q14,-2 22,-12 Z" transform="rotate(35)"/>')
def magnet():
    return ('<path d="M-14,-16 L-14,6 a14,14 0 0 0 28,0 L14,-16"/>'
            '<path d="M-6,-16 L-6,6 a6,6 0 0 0 12,0 L6,-16"/>'
            '<line x1="-14" y1="-16" x2="-6" y2="-16"/><line x1="6" y1="-16" x2="14" y2="-16"/>'
            '<line x1="-14" y1="-12" x2="-6" y2="-12"/><line x1="6" y1="-12" x2="14" y2="-12"/>')
def anchor():
    return ('<circle cx="0" cy="-18" r="5"/><line x1="0" y1="-13" x2="0" y2="22"/>'
            '<line x1="-10" y1="-4" x2="10" y2="-4"/>'
            '<path d="M-18,6 q0,16 18,16 q18,0 18,-16"/>'
            '<path d="M-18,6 l-5,2 l4,5 M18,6 l5,2 l-4,5"/>')
def compass():
    return ('<circle r="20"/>' + poly([(0, -12), (5, 0), (0, 12), (-5, 0)]) + d(0, 0, 2) +
            '<line x1="0" y1="-20" x2="0" y2="-16"/><line x1="0" y1="20" x2="0" y2="16"/>')
def mappin():
    return ('<path d="M0,26 C-13,8 -15,-3 -10,-12 A12,12 0 1 1 10,-12 C15,-3 13,8 0,26 Z"/><circle cx="0" cy="-8" r="5"/>')
def lock():
    return ('<rect x="-14" y="-2" width="28" height="22" rx="3"/>'
            '<path d="M-9,-2 V-10 a9,9 0 0 1 18,0 V-2"/>' + d(0, 6, 2.6) + '<line x1="0" y1="8" x2="0" y2="14"/>')
def battery():
    return ('<rect x="-22" y="-12" width="40" height="24" rx="3"/><rect x="18" y="-5" width="4" height="10" rx="1"/>'
            '<rect x="-18" y="-8" width="6" height="16"/><rect x="-9" y="-8" width="6" height="16"/><rect x="0" y="-8" width="6" height="16"/>')
def plug():
    return ('<rect x="-12" y="-14" width="24" height="20" rx="6"/>'
            '<line x1="-6" y1="-14" x2="-6" y2="-24"/><line x1="6" y1="-14" x2="6" y2="-24"/>'
            '<path d="M0,6 v8 q0,8 -10,8"/>')
def magnifier():
    return ('<circle cx="-4" cy="-4" r="14"/><line x1="6" y1="6" x2="20" y2="20"/><path d="M-9,-6 a6,6 0 0 1 5,-5"/>')
def tagicon():
    return ('<path d="M-20,-14 L4,-14 L22,4 L4,22 L-20,22 Z" transform="rotate(-45)"/>'
            + '<circle cx="-9" cy="-9" r="3" transform="rotate(-45)"/>')
def trophy():
    return ('<path d="M-12,-18 H12 V-6 a12,12 0 0 1 -24,0 Z"/>'
            '<path d="M-12,-14 q-9,0 -9,8 q0,6 9,7"/><path d="M12,-14 q9,0 9,8 q0,6 -9,7"/>'
            '<line x1="0" y1="6" x2="0" y2="16"/><rect x="-9" y="16" width="18" height="6"/>')
def bellalarm():
    return ('<circle cx="0" cy="2" r="17"/><path d="M-16,-10 l-7,-6 M16,-10 l7,-6"/>'
            '<line x1="-9" y1="18" x2="-13" y2="24"/><line x1="9" y1="18" x2="13" y2="24"/>'
            '<line x1="0" y1="2" x2="0" y2="-8"/><line x1="0" y1="2" x2="8" y2="6"/>')
def candle():
    return ('<rect x="-9" y="-6" width="18" height="26" rx="2"/><line x1="0" y1="-6" x2="0" y2="-14"/>'
            '<path d="M0,-14 C-5,-19 -2,-26 0,-30 C2,-26 5,-19 0,-14 Z"/>'
            '<path d="M-12,20 h24"/>')
def hourglass():
    return ('<line x1="-14" y1="-20" x2="14" y2="-20"/><line x1="-14" y1="20" x2="14" y2="20"/>'
            '<path d="M-12,-20 C-12,-6 12,-6 12,20 M12,-20 C12,-6 -12,-6 -12,20"/>'
            '<path d="M-7,-16 L7,-16 L0,-4 Z" fill="__DOT__" stroke="none"/>')
def flask():
    return ('<path d="M-6,-20 L-6,-2 L-18,18 a4,4 0 0 0 4,6 H14 a4,4 0 0 0 4,-6 L6,-2 L6,-20 Z"/>'
            '<line x1="-9" y1="-20" x2="9" y2="-20"/><path d="M-12,8 H12"/>' + d(-4, 14, 2) + d(5, 18, 2.4))
def testtube():
    return ('<path d="M-6,-20 L-6,16 a6,6 0 0 0 12,0 L6,-20"/><line x1="-9" y1="-20" x2="9" y2="-20"/>'
            '<path d="M-6,2 H6"/>' + d(-2, 8, 2) + d(3, 12, 1.8))
def dna():
    s = '<path d="M-8,-22 C12,-12 -12,-2 8,8 C-12,18 12,28 -8,38" transform="translate(0,-8)"/>'
    s += '<path d="M8,-22 C-12,-12 12,-2 -8,8 C12,18 -12,28 8,38" transform="translate(0,-8)"/>'
    for y in (-22, -8, 6, 20):
        s += f'<line x1="-7" y1="{y}" x2="7" y2="{y}"/>'
    return s
def telescope():
    return ('<rect x="-6" y="-22" width="14" height="30" rx="3" transform="rotate(35)"/>'
            '<rect x="-9" y="-26" width="8" height="10" rx="2" transform="rotate(35)"/>'
            '<line x1="-2" y1="14" x2="-2" y2="26"/><line x1="-12" y1="26" x2="8" y2="26"/>' + star_small(16, -16, 4))
def dice():
    return ('<rect x="-18" y="-18" width="36" height="36" rx="6"/>' + d(-8, -8, 3) + d(8, -8, 3) + d(0, 0, 3) + d(-8, 8, 3) + d(8, 8, 3))
def puzzle():
    return ('<path d="M-18,-18 H-4 a4,4 0 0 1 8,0 H18 V-4 a4,4 0 0 1 0,8 V18 H4 a4,4 0 0 0 -8,0 H-18 V4 a4,4 0 0 0 0,-8 Z"/>')
def gamepad():
    return ('<path d="M-24,-6 Q-30,14 -18,15 Q-12,15 -8,8 H8 Q12,15 18,15 Q30,14 24,-6 Q20,-13 8,-11 H-8 Q-20,-13 -24,-6 Z"/>'
            '<line x1="-16" y1="-2" x2="-8" y2="-2"/><line x1="-12" y1="-6" x2="-12" y2="2"/>'
            + d(10, -4, 2.6) + d(17, 2, 2.6) + d(13, 4, 2.6))
def headphones():
    return ('<path d="M-20,6 V-2 A20,20 0 0 1 20,-2 V6"/>'
            '<rect x="-25" y="4" width="9" height="15" rx="3"/><rect x="16" y="4" width="9" height="15" rx="3"/>')
def micicon():
    return ('<rect x="-7" y="-22" width="14" height="26" rx="7"/>'
            '<path d="M-13,-4 a13,13 0 0 0 26,0"/><line x1="0" y1="9" x2="0" y2="20"/><line x1="-8" y1="20" x2="8" y2="20"/>')
def vinyl():
    return ('<circle r="20"/><circle r="8"/>' + d(0, 0, 2))
def guitar():
    return ('<path d="M0,2 C-15,2 -15,32 0,32 C15,32 15,2 0,2 Z"/>'
            '<path d="M0,-4 C-9,-4 -10,8 0,8 C10,8 9,-4 0,-4 Z"/>'
            '<circle cx="0" cy="18" r="5"/>'
            '<rect x="-3.5" y="-30" width="7" height="26"/>'
            '<rect x="-5.5" y="-37" width="11" height="9" rx="1.5"/>'
            '<line x1="-1" y1="-28" x2="-1" y2="18"/><line x1="1" y1="-28" x2="1" y2="18"/>'
            + d(-3, -34, 1.4) + d(3, -34, 1.4) + d(-3, -31, 1.4) + d(3, -31, 1.4))
def basketball():
    return ('<circle r="20"/><line x1="-20" y1="0" x2="20" y2="0"/><line x1="0" y1="-20" x2="0" y2="20"/>'
            '<path d="M-14,-14 Q0,0 -14,14"/><path d="M14,-14 Q0,0 14,14"/>')
def soccer():
    return ('<circle r="20"/>' + poly([(0, -9), (8, -3), (5, 7), (-5, 7), (-8, -3)]) +
            '<line x1="0" y1="-9" x2="0" y2="-20"/><line x1="8" y1="-3" x2="17" y2="-9"/>'
            '<line x1="5" y1="7" x2="11" y2="16"/><line x1="-5" y1="7" x2="-11" y2="16"/><line x1="-8" y1="-3" x2="-17" y2="-9"/>')
def football():
    return ('<path d="M-22,0 Q0,-16 22,0 Q0,16 -22,0 Z"/><line x1="-9" y1="0" x2="9" y2="0"/>'
            '<line x1="-5" y1="-4" x2="-5" y2="4"/><line x1="0" y1="-5" x2="0" y2="5"/><line x1="5" y1="-4" x2="5" y2="4"/>')
def snowman():
    return ('<circle cx="0" cy="12" r="13"/><circle cx="0" cy="-9" r="9"/>'
            + d(-3, -11, 1.6) + d(3, -11, 1.6) + d(0, -6, 1.4) +
            '<rect x="-7" y="-27" width="14" height="9" rx="1"/><line x1="-12" y1="-18" x2="12" y2="-18"/>'
            '<line x1="-9" y1="6" x2="-22" y2="2"/><line x1="9" y1="6" x2="22" y2="2"/>' + d(0, 8, 1.4) + d(0, 16, 1.4))
def lantern():
    return ('<line x1="-10" y1="-20" x2="10" y2="-20"/><path d="M0,-24 q-6,2 0,4"/>'
            '<rect x="-12" y="-16" width="24" height="28" rx="6"/>'
            '<line x1="-12" y1="-9" x2="12" y2="-9"/><line x1="-12" y1="6" x2="12" y2="6"/>'
            '<line x1="-9" y1="14" x2="9" y2="14"/>' + d(0, 0, 3))
def ticket():
    return ('<path d="M-24,-12 H24 a4,4 0 0 0 0,8 V4 a4,4 0 0 0 0,8 H-24 a4,4 0 0 0 0,-8 V-4 a4,4 0 0 0 0,-8 Z"/>'
            '<line x1="8" y1="-12" x2="8" y2="12" stroke-dasharray="3 3"/>' + star_small(-9, 0, 5))
def shoppingbag():
    return ('<path d="M-16,-8 H16 L13,22 H-13 Z"/><path d="M-8,-8 V-12 a8,8 0 0 1 16,0 V-8"/>')

# ----------------------------- tech / dev -----------------------------
def tux():
    return ('<path d="M0,-42 C-13,-42 -17,-31 -15,-22 C-26,-17 -28,2 -26,14 C-25,29 -15,40 0,40 '
            'C15,40 25,29 26,14 C28,2 26,-17 15,-22 C17,-31 13,-42 0,-42 Z"/>'
            '<path d="M0,-15 C-10,-15 -14,1 -13,15 C-12,29 -6,37 0,37 C6,37 12,29 13,15 C14,1 10,-15 0,-15 Z"/>'
            '<ellipse cx="-6" cy="-27" rx="4.5" ry="6"/><ellipse cx="6" cy="-27" rx="4.5" ry="6"/>'
            + d(-6, -26, 2) + d(6, -26, 2) +
            '<path d="M0,-23 L5,-18 L0,-13 L-5,-18 Z"/>'
            '<ellipse cx="-9" cy="40" rx="9" ry="4.5"/><ellipse cx="9" cy="40" rx="9" ry="4.5"/>'
            '<path d="M-26,2 q-7,10 -2,20"/><path d="M26,2 q7,10 2,20"/>')
def terminal():
    return ('<rect x="-28" y="-22" width="56" height="44" rx="5"/><line x1="-28" y1="-11" x2="28" y2="-11"/>'
            + d(-22, -16.5, 2) + d(-15, -16.5, 2) + d(-8, -16.5, 2) +
            '<path d="M-19,-2 L-9,5 L-19,12"/><line x1="-3" y1="13" x2="14" y2="13"/>')
def braces():
    return ('<path d="M-8,-24 q-8,0 -8,8 q0,8 -7,8 q7,0 7,8 q0,8 8,8"/>'
            '<path d="M8,-24 q8,0 8,8 q0,8 7,8 q-7,0 -7,8 q0,8 -8,8"/>')
def codetags():
    return ('<polyline points="-12,-14 -26,0 -12,14"/><polyline points="12,-14 26,0 12,14"/><line x1="6" y1="-18" x2="-6" y2="18"/>')
def gitbranch():
    return ('<line x1="-14" y1="-22" x2="-14" y2="22"/><path d="M-14,-6 q0,14 14,14"/>'
            '<circle cx="-14" cy="-22" r="6"/><circle cx="-14" cy="22" r="6"/><circle cx="14" cy="8" r="6"/>')
def invader():
    return ('<path d="M-22,-10 L-22,-22 L-14,-22 L-14,-14 L-6,-14 L-6,-22 L6,-22 L6,-14 L14,-14 L14,-22 L22,-22 '
            'L22,-10 L18,-10 L18,-2 L22,-2 L22,10 L14,10 L14,18 L6,18 L6,10 L-6,10 L-6,18 L-14,18 L-14,10 '
            'L-22,10 L-22,-2 L-18,-2 L-18,-10 Z"/>' + d(-9, -4, 2.6) + d(9, -4, 2.6))
def robot():
    return ('<rect x="-22" y="-16" width="44" height="36" rx="7"/><line x1="0" y1="-16" x2="0" y2="-26"/>'
            + d(0, -28, 3) + d(-9, -2, 3) + d(9, -2, 3) +
            '<line x1="-10" y1="11" x2="10" y2="11"/><line x1="-22" y1="0" x2="-28" y2="0"/><line x1="22" y1="0" x2="28" y2="0"/>')
def gear():
    n = 8
    rin, rout = 13, 22
    pts = []
    for i in range(n * 2):
        a = i * math.pi / n
        r = rout if i % 2 == 0 else rin
        pts.append((r * math.cos(a), r * math.sin(a)))
    return poly(pts) + '<circle r="7"/>'
def rocket():
    return ('<path d="M0,-30 C12,-18 12,2 8,16 H-8 C-12,2 -12,-18 0,-30 Z"/><circle cx="0" cy="-8" r="5"/>'
            '<path d="M-8,8 L-18,20 L-8,16"/><path d="M8,8 L18,20 L8,16"/><path d="M-4,16 Q0,28 4,16"/>')
def bug():
    s = ('<ellipse cx="0" cy="2" rx="14" ry="20"/><line x1="0" y1="-18" x2="0" y2="22"/>'
         '<circle cx="0" cy="-22" r="6"/><path d="M-4,-27 L-9,-33"/><path d="M4,-27 L9,-33"/>')
    for sy in (-8, 2, 12):
        s += f'<path d="M-14,{sy} L-26,{sy-6}"/><path d="M14,{sy} L26,{sy-6}"/>'
    return s
def floppy():
    return ('<path d="M-22,-22 H14 L22,-14 V22 H-22 Z"/><rect x="-12" y="-22" width="16" height="12"/>'
            '<rect x="-14" y="4" width="28" height="18"/>' + d(-2, -16, 1.8))
def wifi():
    return ('<path d="M-22,-6 A30,30 0 0 1 22,-6"/><path d="M-14,4 A18,18 0 0 1 14,4"/>'
            '<path d="M-6,14 A8,8 0 0 1 6,14"/>' + d(0, 22, 3))
def atom():
    s = '<circle r="4.5"/>'
    for a in (0, 60, 120):
        s += f'<g transform="rotate({a})"><ellipse rx="26" ry="10"/></g>'
    return s
def lam():
    return '<path d="M-16,22 L0,-20 L16,22"/><path d="M-7,0 L-18,-22"/>'
def hashtag():
    return ('<line x1="-8" y1="-22" x2="-14" y2="22"/><line x1="12" y1="-22" x2="6" y2="22"/>'
            '<line x1="-20" y1="-8" x2="22" y2="-8"/><line x1="-22" y1="8" x2="20" y2="8"/>')
def keyboard():
    s = '<rect x="-30" y="-14" width="60" height="28" rx="4"/>'
    for r in range(2):
        for c in range(5):
            s += f'<rect x="{-24+c*10}" y="{-9+r*9}" width="7" height="6" rx="1.2"/>'
    return s + '<rect x="-16" y="9" width="32" height="5" rx="1.5"/>'
def powerbtn():
    return '<path d="M-13,-12 A18,18 0 1 0 13,-12"/><line x1="0" y1="-22" x2="0" y2="2"/>'
def semicolon():
    return d(0, -12, 4.2) + d(0, 8, 4.2) + '<path d="M2,11 q3,4 -2,10 q-2,3 -5,5"/>'
def monitor():
    return ('<rect x="-24" y="-18" width="48" height="32" rx="3"/>'
            '<line x1="0" y1="14" x2="0" y2="20"/><line x1="-10" y1="20" x2="10" y2="20"/>'
            '<path d="M-17,-10 L-11,-4 L-17,2"/><line x1="-6" y1="3" x2="6" y2="3"/>')
def laptop():
    return ('<path d="M-18,-16 H18 V8 H-18 Z"/><path d="M-26,8 H26 L29,16 H-29 Z"/>'
            '<line x1="-8" y1="16" x2="8" y2="16"/>')
def phone():
    return ('<rect x="-12" y="-22" width="24" height="44" rx="5"/><line x1="-4" y1="-18" x2="4" y2="-18"/>' + d(0, 17, 2))
def database():
    return ('<path d="M-14,-16 a14,5 0 0 1 28,0 v28 a14,5 0 0 1 -28,0 Z"/>'
            '<path d="M-14,-16 a14,5 0 0 0 28,0"/><path d="M-14,-2 a14,5 0 0 0 28,0"/>')
def folder():
    return ('<path d="M-22,-12 H-4 L0,-6 H22 V16 H-22 Z"/><line x1="-22" y1="-6" x2="22" y2="-6"/>')
def fileicon():
    return ('<path d="M-14,-20 H6 L16,-10 V20 H-14 Z"/><path d="M6,-20 V-10 H16"/>'
            '<line x1="-8" y1="-2" x2="10" y2="-2"/><line x1="-8" y1="6" x2="10" y2="6"/><line x1="-8" y1="14" x2="4" y2="14"/>')
def brackets():
    return ('<path d="M-8,-22 H-18 V22 H-8"/><path d="M8,-22 H18 V22 H8"/>')
def parens():
    return ('<path d="M-8,-24 q-12,12 0,48"/><path d="M8,-24 q12,12 0,48"/>')
def arrowfn():
    return ('<line x1="-20" y1="-6" x2="6" y2="-6"/><line x1="-20" y1="6" x2="6" y2="6"/>'
            '<polyline points="10,-12 22,0 10,12"/>')
def asterisk():
    s = ''
    for i in range(3):
        a = i * math.pi / 3
        s += f'<line x1="{-18*math.cos(a):.1f}" y1="{-18*math.sin(a):.1f}" x2="{18*math.cos(a):.1f}" y2="{18*math.sin(a):.1f}"/>'
    return s
def infinity():
    return ('<path d="M0,0 C-6,-12 -22,-12 -22,0 C-22,12 -6,12 0,0 C6,-12 22,-12 22,0 C22,12 6,12 0,0 Z"/>')
def atsign():
    return ('<circle r="6"/><path d="M6,0 q0,8 8,6 q8,-2 6,-12 A20,20 0 1 0 12,18"/>')
def cpuchip():
    s = '<rect x="-14" y="-14" width="28" height="28" rx="2"/><rect x="-6" y="-6" width="12" height="12"/>'
    for t in (-7, 0, 7):
        s += f'<line x1="{t}" y1="-14" x2="{t}" y2="-20"/><line x1="{t}" y1="14" x2="{t}" y2="20"/>'
        s += f'<line x1="-14" y1="{t}" x2="-20" y2="{t}"/><line x1="14" y1="{t}" x2="20" y2="{t}"/>'
    return s
def serverstack():
    s = ''
    for i, y in enumerate((-16, 0, 16)):
        s += f'<rect x="-20" y="{y-7}" width="40" height="14" rx="2"/>' + d(-13, y, 2) + f'<line x1="6" y1="{y}" x2="14" y2="{y}"/>'
    return s
def bluetooth():
    return ('<path d="M-10,-12 L10,8 L0,18 L0,-18 L10,-8 L-10,12"/>')
def cursorarrow():
    return ('<path d="M-12,-16 L14,-2 L2,2 L8,16 L2,18 L-4,4 L-12,8 Z"/>')
def commandkey():
    return ('<rect x="-9" y="-9" width="18" height="18"/>'
            '<path d="M-9,-9 a6,6 0 1 1 6,6 M9,-9 a6,6 0 1 0 -6,6 M-9,9 a6,6 0 1 0 6,-6 M9,9 a6,6 0 1 1 -6,-6"/>')
def bookmark():
    return '<path d="M-12,-20 H12 V22 L0,12 L-12,22 Z"/>'
def qrcode():
    s = '<rect x="-20" y="-20" width="14" height="14"/><rect x="-16" y="-16" width="6" height="6" fill="__DOT__" stroke="none"/>'
    s += '<rect x="6" y="-20" width="14" height="14"/><rect x="10" y="-16" width="6" height="6" fill="__DOT__" stroke="none"/>'
    s += '<rect x="-20" y="6" width="14" height="14"/><rect x="-16" y="10" width="6" height="6" fill="__DOT__" stroke="none"/>'
    s += d(10, 4, 2.4) + d(16, 10, 2.4) + d(8, 16, 2.4) + d(16, 2, 2)
    return s
def usbplug():
    return ('<circle cx="0" cy="20" r="3.5"/><line x1="0" y1="20" x2="0" y2="-14"/>'
            '<path d="M-5,-8 L0,-16 L5,-8 Z" fill="__DOT__" stroke="none"/>'
            '<path d="M0,4 L-9,-4"/><circle cx="-9" cy="-7" r="3"/>'
            '<path d="M0,-2 L9,-10"/><rect x="6" y="-15" width="6" height="6"/>')

# ----------------------------- tiny fillers -----------------------------
def f_sparkle():
    return '<path d="M0,-11 Q1.5,-1.5 11,0 Q1.5,1.5 0,11 Q-1.5,1.5 -11,0 Q-1.5,-1.5 0,-11 Z"/>'
def f_sparkle_sm():
    return '<path d="M0,-7 Q1,-1 7,0 Q1,1 0,7 Q-1,1 -7,0 Q-1,-1 0,-7 Z"/>'
def f_dot():
    return d(0, 0, 2.4)
def f_dot_lg():
    return d(0, 0, 3.4)
def f_ring():
    return '<circle r="4.5"/>'
def f_ring_lg():
    return '<circle r="7"/>'
def f_plus():
    return '<line x1="-6" y1="0" x2="6" y2="0"/><line x1="0" y1="-6" x2="0" y2="6"/>'
def f_cross():
    return '<line x1="-5" y1="-5" x2="5" y2="5"/><line x1="-5" y1="5" x2="5" y2="-5"/>'
def f_ministar():
    return star_small(0, 0, 7)
def f_tri():
    return poly([(0, -7), (7, 6), (-7, 6)])
def f_squiggle():
    return '<path d="M-10,0 q5,-7 10,0 q5,7 10,0"/>'
def f_ccurve():
    return '<path d="M5,-7 A8,8 0 1 0 5,7"/>'
def f_twodots():
    return d(-5, 0, 2.2) + d(5, 0, 2.2)
def f_tinydiamond():
    return poly([(0, -7), (6, 0), (0, 7), (-6, 0)])
def f_threedots():
    return d(-7, 0, 1.8) + d(0, 0, 1.8) + d(7, 0, 1.8)
def f_arc():
    return '<path d="M-9,4 Q0,-10 9,4"/>'


# ============================ 3. registries ===============================
# REG:    (motif_fn, bounding_radius, tier)   — the main motifs and their tier.
# FILLER: (motif_fn, bounding_radius)         — tiny glyphs for the gaps.

REG = [
    # big / detailed heroes
    (tux, 46, 'big'), (cat, 40, 'big'), (robot, 34, 'big'), (terminal, 34, 'big'), (gamepad, 32, 'big'),
    (camera, 32, 'big'), (palmtree, 34, 'big'), (pinetree, 32, 'big'), (cactus, 34, 'big'), (snowman, 28, 'big'),
    (trophy, 28, 'big'), (lantern, 30, 'big'), (book, 28, 'big'), (corn, 30, 'big'), (turtle, 30, 'big'),
    (mountain, 30, 'big'), (bellalarm, 28, 'big'), (dna, 30, 'big'), (serverstack, 26, 'big'), (hourglass, 26, 'big'),
    # medium objects
    (coffee, 30, 'med'), (gift, 28, 'med'), (umbrella, 30, 'med'), (clock, 26, 'med'), (bulb, 28, 'med'),
    (envelope, 28, 'med'), (flask, 26, 'med'), (rocket, 30, 'med'), (bug, 30, 'med'), (flower, 24, 'med'),
    (diamond, 26, 'med'), (crown, 26, 'med'), (bell, 26, 'med'), (anchor, 28, 'med'), (compass, 24, 'med'),
    (pencil, 28, 'med'), (scissors, 26, 'med'), (key, 24, 'med'), (magnet, 22, 'med'), (headphones, 26, 'med'),
    (micicon, 26, 'med'), (guitar, 30, 'med'), (basketball, 22, 'med'), (soccer, 22, 'med'), (football, 24, 'med'),
    (taco, 28, 'med'), (icecream, 26, 'med'), (watermelon, 28, 'med'), (pizza, 28, 'med'), (donut, 22, 'med'),
    (cupcake, 26, 'med'), (apple, 22, 'med'), (cherry, 24, 'med'), (mushroom, 24, 'med'), (fish, 24, 'med'),
    (butterfly, 26, 'med'), (foxhead, 26, 'med'), (snail, 24, 'med'), (leaf, 26, 'med'), (kite, 28, 'med'),
    (monitor, 28, 'med'), (laptop, 28, 'med'), (database, 18, 'med'), (cpuchip, 24, 'med'), (dice, 22, 'med'),
    (puzzle, 22, 'med'), (ticket, 26, 'med'), (shoppingbag, 20, 'med'), (telescope, 28, 'med'), (invader, 24, 'med'),
    (bubble, 30, 'med'), (note, 26, 'med'), (plane, 28, 'med'), (camera, 32, 'med'), (vinyl, 22, 'med'),
    (smiley, 24, 'med'), (lock, 20, 'med'), (battery, 24, 'med'), (plug, 18, 'med'), (candle, 24, 'med'),
    (testtube, 22, 'med'), (folder, 22, 'med'), (fileicon, 22, 'med'), (seashell, 24, 'med'), (pawprint, 16, 'med'),
    # small icons / symbols
    (heart, 16, 'small'), (star5, 22, 'small'), (sun, 28, 'small'), (cloud, 26, 'small'), (bolt, 26, 'small'),
    (moon, 22, 'small'), (sprout, 26, 'small'), (balloon, 26, 'small'), (raindrop, 22, 'small'), (flame, 26, 'small'),
    (snowflake, 22, 'small'), (wifi, 22, 'small'), (atom, 28, 'small'), (gear, 22, 'small'), (floppy, 24, 'small'),
    (gitbranch, 24, 'small'), (braces, 22, 'small'), (codetags, 26, 'small'), (lam, 22, 'small'), (hashtag, 22, 'small'),
    (semicolon, 16, 'small'), (powerbtn, 22, 'small'), (brackets, 22, 'small'), (parens, 24, 'small'), (arrowfn, 22, 'small'),
    (asterisk, 18, 'small'), (infinity, 22, 'small'), (atsign, 20, 'small'), (bluetooth, 18, 'small'), (cursorarrow, 16, 'small'),
    (commandkey, 12, 'small'), (bookmark, 22, 'small'), (qrcode, 22, 'small'), (mappin, 26, 'small'), (magnifier, 20, 'small'),
    (tagicon, 22, 'small'), (hotpepper, 22, 'small'), (lollipop, 30, 'small'), (friedegg, 22, 'small'), (keyboard, 32, 'small'),
    (paintbrush, 26, 'small'), (usbplug, 26, 'small'), (phone, 24, 'small'),
]

FILLER = [
    (f_sparkle, 11), (f_sparkle_sm, 7), (f_dot, 3), (f_dot_lg, 4), (f_ring, 5), (f_ring_lg, 7),
    (f_plus, 7), (f_cross, 7), (f_ministar, 8), (f_tri, 8), (f_squiggle, 11), (f_ccurve, 9),
    (f_twodots, 7), (f_tinydiamond, 7), (f_threedots, 9), (f_arc, 10),
]


# ============================= 4. generator ===============================

def _wrapdelta(ax, ay, bx, by, period):
    """Shortest delta between two points on a torus of the given period."""
    dx = ax - bx
    if dx > period / 2:
        dx -= period
    if dx < -period / 2:
        dx += period
    dy = ay - by
    if dy > period / 2:
        dy -= period
    if dy < -period / 2:
        dy += period
    return dx, dy


def build_layout(seed, *, tile=TILE, n_big=N_BIG, n_med=N_MED, n_small=N_SMALL,
                 n_fill=N_FILL, margin=MARGIN, relax_iters=RELAX_ITERS):
    """Place motifs on a seamless tile and return (placements, n_main, n_fill).

    Each placement is (motif_fn, x, y, rotation_deg, scale). The layout is a
    jittered grid relaxed by toroidal repulsion, then a no-overlap pass, then a
    sparse filler pass — all driven by `seed` so it is reproducible.
    """
    rnd = random.Random(seed)

    big = [m for m in REG if m[2] == 'big']
    med = [m for m in REG if m[2] == 'med']
    sml = [m for m in REG if m[2] == 'small']

    def pick(pool, k):
        bag, res = [], []
        for _ in range(k):
            if not bag:
                bag = pool[:]
                rnd.shuffle(bag)
            res.append(bag.pop())
        return res

    chosen = pick(big, n_big) + pick(med, n_med) + pick(sml, n_small)
    rnd.shuffle(chosen)
    n = len(chosen)

    # --- jittered-grid initialisation for even coverage ---
    g = max(1, int(math.ceil(math.sqrt(n))))
    cell = tile / g
    cells = [(gx, gy) for gy in range(g) for gx in range(g)]
    rnd.shuffle(cells)
    cells = cells[:n]

    P, R, F, SC, ROT = [], [], [], [], []   # position, radius, fn, scale, rotation
    for (gx, gy), (fn, r, tier) in zip(cells, chosen):
        jx = rnd.uniform(0.18, 0.82)
        jy = rnd.uniform(0.18, 0.82)
        P.append([(gx + jx) * cell, (gy + jy) * cell])
        sc = rnd.uniform(0.9, 1.08)
        SC.append(sc)
        R.append(r * sc)
        F.append(fn)
        ROT.append(rnd.uniform(-9, 9))

    # --- repulsion relaxation (toroidal) ---
    for _ in range(relax_iters):
        moves = [[0.0, 0.0] for _ in range(n)]
        damp = 0.5
        for i in range(n):
            xi, yi = P[i]
            for j in range(i + 1, n):
                dx, dy = _wrapdelta(xi, yi, P[j][0], P[j][1], tile)
                dist = math.hypot(dx, dy)
                need = R[i] + R[j] + margin
                if dist < need:
                    if dist < 1e-6:
                        ang = rnd.uniform(0, 2 * math.pi)
                        ux, uy = math.cos(ang), math.sin(ang)
                        dist = 0.01
                    else:
                        ux, uy = dx / dist, dy / dist
                    push = (need - dist) * damp
                    moves[i][0] += ux * push
                    moves[i][1] += uy * push
                    moves[j][0] -= ux * push
                    moves[j][1] -= uy * push
        for i in range(n):
            P[i][0] = (P[i][0] + moves[i][0]) % tile
            P[i][1] = (P[i][1] + moves[i][1]) % tile

    # --- guarantee zero overlap: drop any residual offenders (bigger wins) ---
    order = sorted(range(n), key=lambda i: -R[i])
    kept, placed = [], []   # placed: (x, y, R)
    for i in order:
        xi, yi = P[i]
        ok = True
        for (px, py, pr) in placed:
            dx, dy = _wrapdelta(xi, yi, px, py, tile)
            if math.hypot(dx, dy) < R[i] + pr + margin * 0.55:
                ok = False
                break
        if ok:
            kept.append(i)
            placed.append((xi, yi, R[i]))

    out = [(F[i], P[i][0], P[i][1], ROT[i], SC[i]) for i in kept]

    # --- sparse filler: only drop into genuine gaps, no collisions ---
    fil = FILLER[:]
    fcount, attempts, fmargin = 0, 0, 9
    while fcount < n_fill and attempts < 60000:
        attempts += 1
        fn, r = rnd.choice(fil)
        sc = rnd.uniform(0.85, 1.18)
        rr = r * sc
        x = rnd.uniform(0, tile)
        y = rnd.uniform(0, tile)
        ok = True
        for (px, py, pr) in placed:
            dx, dy = _wrapdelta(x, y, px, py, tile)
            if math.hypot(dx, dy) < rr + pr + fmargin:
                ok = False
                break
        if ok:
            placed.append((x, y, rr))
            out.append((fn, x, y, rnd.uniform(-50, 50), sc))
            fcount += 1

    return out, len(kept), fcount


def render(placements, *, tile=TILE, stroke=STROKE, stroke_width=STROKE_WIDTH,
           dot_colour=DOT_COLOUR):
    """Render placements to a seamless SVG string. Motifs near an edge are also
    drawn wrapped across the opposite edge so the tile repeats without seams."""
    cache = {}
    body = []
    for fn, x, y, rot, sc in placements:
        if fn not in cache:
            cache[fn] = fn().replace('__DOT__', dot_colour)
        markup = cache[fn]
        reach = 60 * sc   # conservative motif half-extent for the wrap check
        for dx in (-tile, 0, tile):
            for dy in (-tile, 0, tile):
                cx, cy = x + dx, y + dy
                if cx + reach < 0 or cx - reach > tile or cy + reach < 0 or cy - reach > tile:
                    continue
                body.append(f'<g transform="translate({cx:.2f},{cy:.2f}) '
                            f'rotate({rot:.2f}) scale({sc:.3f})">{markup}</g>')
    svg = [f'<svg xmlns="http://www.w3.org/2000/svg" width="{tile}" height="{tile}" viewBox="0 0 {tile} {tile}">',
           f'<defs><clipPath id="t"><rect width="{tile}" height="{tile}"/></clipPath></defs>',
           f'<g clip-path="url(#t)" fill="none" stroke="{stroke}" stroke-width="{stroke_width}" '
           f'stroke-linecap="round" stroke-linejoin="round">']
    svg += body
    svg.append('</g></svg>')
    return ''.join(svg)


# ================================ 5. CLI ==================================

def default_seed() -> str:
    """The repo's numeric version (e.g. "0.5.1"), reused from version.py so a
    bare run is reproducible. Falls back to "0.0.0" if it can't be resolved."""
    try:
        from version import numeric_version
        return numeric_version()
    except Exception:
        return "0.0.0"


def main() -> None:
    parser = argparse.ArgumentParser(description="generate the chat-wallpaper doodle SVG")
    parser.add_argument("--seed", default=None,
                        help="RNG seed (default: the repo's numeric version)")
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT,
                        help=f"output SVG path (default: {DEFAULT_OUTPUT})")
    args = parser.parse_args()

    seed = args.seed if args.seed is not None else default_seed()

    placements, n_main, n_fill = build_layout(seed)
    svg = render(placements)

    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(svg, encoding="utf-8")

    print(f"doodle: seed={seed!r} motifs={n_main} fillers={n_fill} "
          f"bytes={len(svg)} -> {args.output}", file=sys.stderr)


if __name__ == "__main__":
    main()
