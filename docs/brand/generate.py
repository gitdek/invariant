"""Generate Invariant brand SVGs.

Mark: one state travelling an orbit around a fixed point. The state moves
through every reachable position; the fixed point never moves. That point is
the invariant, so it carries the accent colour.
Wordmark: monoline geometric, same stroke as the ring. The i's are dotted in
the accent colour.
"""
import math
import os
import sys

OUT = sys.argv[1] if len(sys.argv) > 1 else os.path.dirname(os.path.abspath(__file__))

S = 5.0   # wordmark stroke
R = 21.0  # ring radius
# Lockup geometry matches the wordmark stroke; the standalone mark is heavier
# so it survives favicon sizes.
LOCKUP = {"S": 5.0, "BEAD": 5.0, "CORE": 8.8, "CLEAR": 1.8}
ICON = {"S": 6.0, "BEAD": 5.6, "CORE": 9.6, "CLEAR": 2.0}
NODES = [315]  # degrees, SVG screen coords (y down, clockwise)

LIGHT = {"ink": "#0E1116", "accent": "#0CA678"}
DARK = {"ink": "#E6EDF3", "accent": "#38D9A9"}


def f(v):
    s = f"{v:.2f}".rstrip("0").rstrip(".")
    return "0" if s == "-0" else s


def pt(cx, cy, r, deg):
    a = math.radians(deg)
    return cx + r * math.cos(a), cy + r * math.sin(a)


def mark(cx, cy, c, indent="  ", g=LOCKUP):
    S, BEAD, CORE, CLEAR = g["S"], g["BEAD"], g["CORE"], g["CLEAR"]
    delta = math.degrees((BEAD + CLEAR + S / 2) / R)
    arcs = []
    for i, n in enumerate(NODES):
        nxt = NODES[(i + 1) % len(NODES)]
        start, end = n + delta, (nxt if nxt > n else nxt + 360) - delta
        x1, y1 = pt(cx, cy, R, start)
        x2, y2 = pt(cx, cy, R, end)
        large = 1 if end - start > 180 else 0
        arcs.append(f"M{f(x1)},{f(y1)}A{f(R)},{f(R)} 0 {large} 1 {f(x2)},{f(y2)}")
    out = [f'<path d="{"".join(arcs)}" stroke="{c["ink"]}" stroke-width="{f(S)}"/>']
    for n in NODES:
        x, y = pt(cx, cy, R, n)
        out.append(f'<circle cx="{f(x)}" cy="{f(y)}" r="{f(BEAD)}" fill="{c["ink"]}"/>')
    out.append(f'<circle cx="{f(cx)}" cy="{f(cy)}" r="{f(CORE)}" fill="{c["accent"]}"/>')
    return ("\n" + indent).join(out)


# Wordmark geometry: baseline 58, x-height 30, ascender 20. Centreline coords.
INK_STROKES = [
    "M2.5,30V58",                                                   # i
    "M17,58V40.5A10.5,10.5 0 0 1 27.5,30A10.5,10.5 0 0 1 38,40.5V58",   # n
    "M49.2,30L61.2,58L73.2,30",                                      # v
    "M106.2,30V58",                                                  # a (stem)
    "M120.7,58V40A10,10 0 0 1 130.7,30H132.7",                       # r
    "M143.2,30V58",                                                  # i
    "M185,30V58",                                                    # a (stem)
    "M199.5,58V40.5A10.5,10.5 0 0 1 210,30A10.5,10.5 0 0 1 220.5,40.5V58",  # n
    "M238,20V51A7,7 0 0 0 245,58",                                   # t (stem)
]
BOWLS = [(91.8, 44), (170.6, 44)]   # a bowls, r = 14.4
TITTLES = [(2.5, 20.5), (143.2, 20.5)]
CROSSBAR = "M231,30H246"
WORD_W = 248.5


def wordmark(x0, c, indent="  "):
    out = [f'<g transform="translate({f(x0)} 0)">']
    out.append(f'  <path d="{"".join(INK_STROKES)}" stroke="{c["ink"]}" stroke-width="{f(S)}"/>')
    for cx, cy in BOWLS:
        out.append(f'  <circle cx="{f(cx)}" cy="{f(cy)}" r="14.4" stroke="{c["ink"]}" stroke-width="{f(S)}"/>')
    for cx, cy in TITTLES:
        out.append(f'  <circle cx="{f(cx)}" cy="{f(cy)}" r="3.4" fill="{c["accent"]}"/>')
    out.append(f'  <path d="{CROSSBAR}" stroke="{c["ink"]}" stroke-width="{f(S)}"/>')
    out.append("</g>")
    return ("\n" + indent).join(out)


def svg(viewbox, w, h, body, title="Invariant"):
    return (
        f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="{viewbox}" width="{w}" height="{h}" '
        f'role="img" aria-label="{title}">\n'
        f"  <title>{title}</title>\n"
        f'  <g fill="none" stroke-linecap="round" stroke-linejoin="round">\n'
        f"    {body}\n"
        f"  </g>\n"
        f"</svg>\n"
    )


def write(name, text):
    os.makedirs(OUT, exist_ok=True)
    with open(os.path.join(OUT, name), "w") as fh:
        fh.write(text)


# Lockup: mark centred on the x-height band, wordmark to its right.
PAD, GAP = 4.0, 16.0
MX, MY = PAD + R + S / 2, 41.5
WX = MX + R + S / 2 + GAP
W = WX + WORD_W + PAD
TOP, BOT = 12.5, 70.0


def main():
    for name, c in (("logo.svg", LIGHT), ("logo-dark.svg", DARK)):
        body = mark(MX, MY, c, "    ") + "\n    " + wordmark(WX, c, "    ")
        write(name, svg(f"0 {f(TOP)} {f(W)} {f(BOT - TOP)}", f(W * 2), f((BOT - TOP) * 2), body))

    # Standalone mark, square.
    for name, c in (("mark.svg", LIGHT), ("mark-dark.svg", DARK)):
        write(name, svg("-27 -27 54 54", 256, 256, mark(0, 0, c, "    ", ICON)))

    # App-style tile for avatars, favicons and the portfolio card.
    tile = (
        '<rect x="-28" y="-28" width="56" height="56" rx="12.5" fill="#0E1116" stroke="none"/>\n'
        '    <g transform="scale(0.64)">\n      '
        + mark(0, 0, DARK, "      ", ICON)
        + "\n    </g>"
    )
    write("mark-tile.svg", svg("-28 -28 56 56", 512, 512, tile))
    print("wrote", sorted(os.listdir(OUT)))


if __name__ == "__main__":
    main()
