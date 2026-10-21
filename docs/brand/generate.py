#!/usr/bin/env python3
"""Generates the fck-fips brand assets into docs/public/.

The mark is an isometric cube, the projection of an engineering drawing,
whose silhouette is a hexagon: three slabs for the layers of an image, the
right faces section-hatched, the top face carrying a check cut out of it
for the verified chain of trust. The full mark sits in a drafting frame with
registration ticks. Wordmark text is converted to outlines from IBM Plex
Mono (SIL Open Font License 1.1, via @fontsource), so the logo renders the
same everywhere, with no font installed.

    python3 -m venv .venv && .venv/bin/pip install fonttools
    .venv/bin/python docs/brand/generate.py   # also needs rsvg-convert (librsvg)
"""

from __future__ import annotations

import math
import subprocess
from pathlib import Path

from fontTools.pens.svgPathPen import SVGPathPen
from fontTools.pens.transformPen import TransformPen
from fontTools.ttLib import TTFont

DOCS = Path(__file__).resolve().parent.parent
PUBLIC = DOCS / "public"
BRAND = DOCS / "brand"
FONTS = DOCS / "node_modules" / "@fontsource" / "ibm-plex-mono" / "files"


# --- Geometry ---------------------------------------------------------------

def cube(cx: float, cy: float, r: float, gap: float):
    """Faces of an isometric cube centred on (cx, cy) with circumradius r."""
    dx, dy = r * math.cos(math.radians(30)), r * 0.5
    top, ur, lr = (cx, cy - r), (cx + dx, cy - dy), (cx + dx, cy + dy)
    bot, ll, ul, c = (cx, cy + r), (cx - dx, cy + dy), (cx - dx, cy - dy), (cx, cy)
    h = lr[1] - ur[1]
    slab = (h - 2 * gap) / 3
    spans = [(i * (slab + gap), i * (slab + gap) + slab) for i in range(3)]
    left = [[(ul[0], ul[1] + a), (c[0], c[1] + a), (c[0], c[1] + b), (ul[0], ul[1] + b)] for a, b in spans]
    right = [[(c[0], c[1] + a), (ur[0], ur[1] + a), (ur[0], ur[1] + b), (c[0], c[1] + b)] for a, b in spans]
    return {"top": [top, ur, c, ul], "left": left, "right": right, "hex": [top, ur, lr, bot, ll, ul], "c": c}


def d(points) -> str:
    return "M" + "L".join(f"{x:.2f},{y:.2f}" for x, y in points) + "Z"


def check_path(cx: float, cy: float, s: float) -> str:
    return f"M{cx - 7.2 * s:.2f},{cy + 0.2 * s:.2f}L{cx - 2.2 * s:.2f},{cy + 4.6 * s:.2f}L{cx + 7.4 * s:.2f},{cy - 4.6 * s:.2f}"


def mark(color: str, *, framed: bool, hatched: bool, uid: str, size: float = 64) -> str:
    """The mark as SVG elements in a 64x64 box (no <svg> wrapper)."""
    r = 21.6 if framed else 27.5
    g = cube(32, 32, r, gap=2.3 if framed else 2.6)
    s = r / 27.5
    out = [
        "<defs>",
        f'<mask id="{uid}-seal"><rect x="-1" y="-1" width="66" height="66" fill="#fff"/>'
        f'<path d="{check_path(32, g["top"][0][1] + r * 0.5, s)}" fill="none" stroke="#000" stroke-width="{3.6 * s:.2f}" stroke-linecap="square"/></mask>',
    ]
    if hatched:
        out.append(
            f'<pattern id="{uid}-hatch" width="2.2" height="2.2" patternUnits="userSpaceOnUse" patternTransform="rotate(-30)">'
            f'<rect width="0.75" height="2.2" fill="{color}"/></pattern>'
        )
    out.append("</defs>")
    out.append(f'<path d="{d(g["top"])}" fill="{color}" mask="url(#{uid}-seal)"/>')
    out.append(f'<g fill="{color}">' + "".join(f'<path d="{d(p)}"/>' for p in g["left"]) + "</g>")
    if hatched:
        out.append(f'<g fill="url(#{uid}-hatch)" stroke="{color}" stroke-width="0.9">' + "".join(f'<path d="{d(p)}"/>' for p in g["right"]) + "</g>")
    else:
        out.append(f'<g fill="{color}">' + "".join(f'<path d="{d(p)}"/>' for p in g["right"]) + "</g>")
        # A seam between the faces keeps the solid version readable.
        c = g["c"]
        out.append(f'<path d="M{c[0]:.2f},{c[1]:.2f}V{c[1] + r:.2f}" stroke="var(--seam, #fff)" stroke-width="1.4"/>')

    if framed:
        # Drafting frame: hexagon offset from the cube, registration ticks at
        # its vertices, and the vertical axis running through.
        R = 27.6
        hexf = [(32 + R * math.cos(math.radians(a)), 32 + R * math.sin(math.radians(a))) for a in range(-90, 270, 60)]
        out.append(f'<path d="{d(hexf)}" fill="none" stroke="{color}" stroke-width="0.8"/>')
        ticks = []
        for x, y in hexf:
            ux, uy = (x - 32) / R, (y - 32) / R
            ticks.append(f"M{x + ux * 1.2:.2f},{y + uy * 1.2:.2f}L{x + ux * 3.4:.2f},{y + uy * 3.4:.2f}")
        out.append(f'<path d="{"".join(ticks)}" stroke="{color}" stroke-width="0.8"/>')
    return "\n  ".join(out)


# --- Type to outlines ---------------------------------------------------------

class Type:
    def __init__(self, weight: int):
        self.font = TTFont(FONTS / f"ibm-plex-mono-latin-{weight}-normal.woff")
        self.glyphs = self.font.getGlyphSet()
        self.cmap = self.font.getBestCmap()
        self.upm = self.font["head"].unitsPerEm
        self.cap = self.font["OS/2"].sCapHeight

    def path(self, text: str, x: float, baseline: float, cap_height: float, tracking: float = 0.0) -> tuple[str, float]:
        """Outlines text whose capitals are cap_height tall; returns (d, width)."""
        scale = cap_height / self.cap
        pen = SVGPathPen(self.glyphs)
        cursor = x
        for ch in text:
            name = self.cmap[ord(ch)]
            glyph = self.glyphs[name]
            glyph.draw(TransformPen(pen, (scale, 0, 0, -scale, cursor, baseline)))
            cursor += glyph.width * scale + tracking
        return pen.getCommands(), cursor - tracking - x


# --- Assets -----------------------------------------------------------------------

HEADER = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="{vb}" role="img" aria-labelledby="{uid}-t"{extra}>\n  <title id="{uid}-t">fck-fips · Node.js buildpacks</title>\n  '


def standalone_mark(color: str, framed: bool, hatched: bool, uid: str, style: str = "") -> str:
    return HEADER.format(vb="0 0 64 64", uid=uid, extra="") + style + mark(color, framed=framed, hatched=hatched, uid=uid) + "\n</svg>\n"


def wordmark(color: str, uid: str, seam: str = "#fff") -> str:
    bold, regular = Type(700), Type(400)
    x0 = 76
    name, wn = bold.path("FCK", x0, 38, 24, tracking=1.5)
    name2, wn2 = bold.path("FIPS", x0 + wn + 16, 38, 24, tracking=1.5)
    total = wn + 16 + wn2
    # Justify the caption: track it so it spans exactly the name and the rule.
    caption = "NODE.JS BUILDPACKS"
    _, natural = regular.path(caption, x0, 58, 7.2)
    sub, ws = regular.path(caption, x0, 58, 7.2, tracking=(total - natural) / (len(caption) - 1))
    width = x0 + total + 2
    dot_x = x0 + wn + 8
    parts = [
        f'<g style="--seam:{seam}">' + mark(color, framed=True, hatched=True, uid=uid) + "</g>",
        f'<path d="{name}{name2}" fill="{color}"/>',
        # The joint between FCK and FIPS: a small square, like a datum point.
        f'<rect x="{dot_x - 2.4:.2f}" y="{38 - 12 - 2.4:.2f}" width="4.8" height="4.8" fill="{color}"/>',
        f'<path d="M{x0},{46.5}H{x0 + total:.2f}" stroke="{color}" stroke-width="0.9"/>',
        f'<path d="{sub}" fill="{color}"/>',
    ]
    return HEADER.format(vb=f"0 0 {width:.0f} 64", uid=uid, extra="") + "\n  ".join(parts) + "\n</svg>\n"


def social_card(paper: str, ink: str, uid: str) -> str:
    """1200x630 link preview: graph paper, drawing frame, mark and name."""
    W, H = 1200, 630
    bold, regular = Type(700), Type(400)
    grid = (
        f'<pattern id="{uid}-minor" width="15" height="15" patternUnits="userSpaceOnUse">'
        f'<path d="M15 0H0V15" fill="none" stroke="{ink}" stroke-opacity="0.06" stroke-width="1"/></pattern>'
        f'<pattern id="{uid}-major" width="75" height="75" patternUnits="userSpaceOnUse">'
        f'<path d="M75 0H0V75" fill="none" stroke="{ink}" stroke-opacity="0.13" stroke-width="1"/></pattern>'
    )
    # Mark, scaled from its 64-unit box, vertically centred on the left.
    m, mx, my = 5.0, 92, (H - 64 * 5.0) / 2 - 18
    x0 = 470
    room = W - 24 - 56 - x0  # up to the frame, with a margin

    # The name fills the room exactly: size it from its width at a unit size.
    gap_em = 0.62  # space for the datum square, in cap heights
    _, w1 = bold.path("FCK", 0, 0, 1, tracking=0.06)
    _, w2 = bold.path("FIPS", 0, 0, 1, tracking=0.06)
    ch = room / (w1 + w2 + gap_em)
    name, wn = bold.path("FCK", x0, 262, ch, tracking=0.06 * ch)
    name2, wn2 = bold.path("FIPS", x0 + wn + gap_em * ch, 262, ch, tracking=0.06 * ch)
    total = wn + gap_em * ch + wn2
    caption = "NODE.JS BUILDPACKS"
    cap_h = ch * 0.3
    _, natural = regular.path(caption, x0, 262 + ch * 0.95, cap_h)
    cap, _ = regular.path(caption, x0, 262 + ch * 0.95, cap_h, tracking=(total - natural) / (len(caption) - 1))

    def fit(text: str, baseline: float, size: float) -> str:
        """Outlines text at size, shrunk if needed to fit the room."""
        _, w = regular.path(text, x0, baseline, size)
        return regular.path(text, x0, baseline, min(size, size * room / w))[0]

    line1 = fit("Verified Node.js: npm, yarn and pnpm", 430, 17)
    line2 = fit("FIPS 140-3 validated OpenSSL, opt-in", 466, 17)
    foot, fw = regular.path("CLOUD NATIVE BUILDPACKS  /  WOLFI  /  SLSA BUILD L3", 0, 0, 13, tracking=2.2)
    dot = x0 + wn + gap_em * ch / 2
    parts = [
        f'<defs>{grid}</defs>',
        f'<rect width="{W}" height="{H}" fill="{paper}"/>',
        f'<rect width="{W}" height="{H}" fill="url(#{uid}-minor)"/>',
        f'<rect width="{W}" height="{H}" fill="url(#{uid}-major)"/>',
        # Drawing sheet border with registration corners.
        f'<rect x="24" y="24" width="{W - 48}" height="{H - 48}" fill="none" stroke="{ink}" stroke-width="2"/>',
        f'<path d="M36 70V36H70M{W - 70} {H - 36}H{W - 36}V{H - 70}" fill="none" stroke="{ink}" stroke-width="4"/>',
        f'<g transform="translate({mx} {my}) scale({m})" style="--seam:{paper}">' + mark(ink, framed=True, hatched=True, uid=uid) + "</g>",
        f'<path d="{name}{name2}" fill="{ink}"/>',
        f'<rect x="{dot - ch * 0.095:.1f}" y="{262 - ch / 2 - ch * 0.095:.1f}" width="{ch * 0.19:.1f}" height="{ch * 0.19:.1f}" fill="{ink}"/>',
        f'<path d="M{x0} {262 + ch * 0.37:.1f}H{x0 + total:.1f}" stroke="{ink}" stroke-width="3"/>',
        f'<path d="{cap}" fill="{ink}"/>',
        f'<path d="{line1}{line2}" fill="{ink}" fill-opacity="0.72"/>',
        # Title-block strip along the bottom edge.
        f'<path d="M24 {H - 84}H{W - 24}" stroke="{ink}" stroke-width="1.5"/>',
        f'<g transform="translate({(W - fw) / 2:.1f} {H - 48})"><path d="{foot}" fill="{ink}" fill-opacity="0.8"/></g>',
    ]
    return (f'<svg xmlns="http://www.w3.org/2000/svg" width="{W}" height="{H}" viewBox="0 0 {W} {H}">\n  '
            + "\n  ".join(parts) + "\n</svg>\n")


def main() -> None:
    PUBLIC.mkdir(exist_ok=True)
    # Large: framed and hatched, follows the text colour.
    (PUBLIC / "logo.svg").write_text(standalone_mark("currentColor", framed=True, hatched=True, uid="lg"))
    # Small (header, 16-48 px): no frame, solid faces split by a seam.
    (PUBLIC / "logo-mark.svg").write_text(standalone_mark("currentColor", framed=False, hatched=False, uid="mk"))
    # Favicon: follows the browser theme by itself.
    fav_style = '<style>.ink{fill:#111;--seam:#fff}@media (prefers-color-scheme:dark){.ink{fill:#ededea;--seam:#0c0c0c}}</style>\n  <g class="ink">'
    fav = standalone_mark("inherit", framed=False, hatched=False, uid="fv", style=fav_style).replace("\n</svg>", "\n  </g>\n</svg>")
    (PUBLIC / "favicon.svg").write_text(fav)
    # Wordmarks: currentColor for the site, fixed colours for README <picture>.
    (PUBLIC / "wordmark.svg").write_text(wordmark("currentColor", "wm"))
    (PUBLIC / "wordmark-light.svg").write_text(wordmark("#111111", "wl"))
    (PUBLIC / "wordmark-dark.svg").write_text(wordmark("#ededea", "wd", seam="#0d1117"))
    # Link previews (Open Graph / Twitter cards). Platforms want a raster
    # image, so the SVG is rendered to PNG with rsvg-convert (librsvg).
    card = BRAND / "og.svg"
    card.write_text(social_card("#0c0c0c", "#ededea", "og"))
    subprocess.run(["rsvg-convert", "-w", "1200", "-h", "630", str(card), "-o", str(PUBLIC / "og.png")], check=True)
    print("wrote", ", ".join(p.name for p in sorted(PUBLIC.glob("*.svg"))), "og.png")


if __name__ == "__main__":
    main()
