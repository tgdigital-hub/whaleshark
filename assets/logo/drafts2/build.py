#!/usr/bin/env python3
"""Draws the round-two WhaleShark marks and the page that shows them.

The shark is laid out along a spine: every point is (t, n), where t runs from the
snout (0) to the root of the tail (1) and n is the distance off the spine, upward
positive, in shark lengths. A straight spine gives the diagonal shark, a curved one
the turn. Outlines are smoothed into cubic curves, so each SVG holds a few clean
paths: the silhouette, its details (spots, bars, ridge, gills, mouth, eye) and the
school of small fish. Run from anywhere:

    python3 assets/logo/drafts2/build.py
"""
import math
import os

HERE = os.path.dirname(os.path.abspath(__file__))

# ---------------------------------------------------------------- geometry


def bez(p0, p1, p2, p3, s):
    u = 1 - s
    return tuple(u**3 * a + 3 * u * u * s * b + 3 * u * s * s * c + s**3 * d
                 for a, b, c, d in zip(p0, p1, p2, p3))


class Spine:
    """Maps (t, n) to the page. `curve(s)` gives the path from tail (s=0) to snout
    (s=1); the shark of length L lies on it with its snout at s=1."""

    def __init__(self, curve, length, samples=400):
        self.curve, self.L = curve, length
        pts = [curve(i / samples) for i in range(samples + 1)]
        acc = [0.0]
        for a, b in zip(pts, pts[1:]):
            acc.append(acc[-1] + math.dist(a, b))
        self.pts, self.acc = pts, acc

    def at(self, d):
        """Point and heading at arc distance d back from the snout."""
        total = self.acc[-1]
        target = total - d
        if target <= 0:  # past the tail end: carry straight on
            p, h = self.at(total - 1e-6)
            over = -target
            return (p[0] - h[0] * over, p[1] - h[1] * over), h
        if target >= total:  # ahead of the snout: carry straight on
            p, h = self.at(1e-6)
            over = target - total
            return (p[0] + h[0] * over, p[1] + h[1] * over), h
        i = max(1, next(k for k, a in enumerate(self.acc) if a >= target))
        a, b = self.pts[i - 1], self.pts[i]
        f = (target - self.acc[i - 1]) / ((self.acc[i] - self.acc[i - 1]) or 1)
        p = (a[0] + (b[0] - a[0]) * f, a[1] + (b[1] - a[1]) * f)
        dx, dy = b[0] - a[0], b[1] - a[1]
        m = math.hypot(dx, dy) or 1
        return p, (dx / m, dy / m)

    def __call__(self, t, n):
        p, h = self.at(t * self.L)
        up = (h[1], -h[0])
        return (p[0] + up[0] * n * self.L, p[1] + up[1] * n * self.L)


def straight(x0, y0, angle_deg, length):
    a = math.radians(angle_deg)
    hx, hy = math.cos(a), -math.sin(a)
    tail = (x0 - hx * length, y0 - hy * length)
    return Spine(lambda s: (tail[0] + hx * length * s, tail[1] + hy * length * s), length)


# body half-heights (top, bottom) along t, in shark lengths
PROFILE = [
    (0.00, 0.056, 0.040), (0.02, 0.070, 0.054), (0.07, 0.086, 0.072),
    (0.15, 0.098, 0.084), (0.25, 0.102, 0.086), (0.37, 0.096, 0.078),
    (0.50, 0.082, 0.064), (0.62, 0.064, 0.049), (0.74, 0.046, 0.036),
    (0.86, 0.030, 0.024), (0.95, 0.021, 0.018), (1.00, 0.018, 0.016),
]


def half(t, which):
    for (t0, *a), (t1, *b) in zip(PROFILE, PROFILE[1:]):
        if t0 <= t <= t1:
            f = (t - t0) / (t1 - t0)
            return a[which] + (b[which] - a[which]) * f
    return PROFILE[-1][1 + which]


def top(t):
    return half(t, 0)


def bot(t):
    return half(t, 1)


def flank(t, v):
    """A point across the body: v=1 on the back, v=-1 on the belly."""
    return v * top(t) if v >= 0 else v * bot(t)


XF = [1.0, 0.0, 0.0]  # scale, dx, dy applied to every coordinate written
SEEN = []  # every point drawn, for fitting the mark to the square


def xf(p):
    SEEN.append((p[0], p[1]))
    return (p[0] * XF[0] + XF[1], p[1] * XF[0] + XF[2])


def smooth(points, closed=True, k=1.0):
    """Catmull-Rom through points; a point given as (x, y, True) is a sharp corner."""
    P = [xf(p) for p in points]
    sharp = [len(p) > 2 and p[2] for p in points]
    n = len(P)
    f = lambda v: f"{v:.1f}".rstrip("0").rstrip(".")
    out = [f"M{f(P[0][0])} {f(P[0][1])}"]
    segs = n if closed else n - 1
    for i in range(segs):
        a, b = P[i], P[(i + 1) % n]
        pa = a if sharp[i] or (not closed and i == 0) else P[(i - 1) % n]
        nb = b if sharp[(i + 1) % n] or (not closed and i + 1 == n - 1) else P[(i + 2) % n]
        c1 = (a[0] + (b[0] - pa[0]) * k / 6, a[1] + (b[1] - pa[1]) * k / 6)
        c2 = (b[0] - (nb[0] - a[0]) * k / 6, b[1] - (nb[1] - a[1]) * k / 6)
        out.append(f"C{f(c1[0])} {f(c1[1])} {f(c2[0])} {f(c2[1])} {f(b[0])} {f(b[1])}")
    return "".join(out) + ("Z" if closed else "")


def circle(c, r):
    f = lambda v: f"{v:.1f}".rstrip("0").rstrip(".")
    x, y = xf(c)
    SEEN.extend([(c[0] - r, c[1] - r), (c[0] + r, c[1] + r)])
    r *= XF[0]
    return (f"M{f(x - r)} {f(y)}a{f(r)} {f(r)} 0 1 0 {f(2 * r)} 0"
            f"a{f(r)} {f(r)} 0 1 0 {f(-2 * r)} 0Z")


# ---------------------------------------------------------------- the shark

def outline(S, fins=True):
    def T(t, n, sharp=False):
        x, y = S(t, n)
        return (x, y, True) if sharp else (x, y)

    pts = []
    # square snout: a nearly flat front face with rounded corners
    pts += [T(0.006, -0.038, True), T(-0.001, -0.02), T(-0.003, 0.012), T(0.004, 0.048)]
    # back, with the first and second dorsal fins
    for t in (0.02, 0.07, 0.15, 0.25, 0.36):
        pts.append(T(t, top(t)))
    pts += [T(0.43, top(0.43)), T(0.47, top(0.47) + 0.045), T(0.535, top(0.53) + 0.105, True),
            T(0.535, top(0.535) + 0.06), T(0.55, top(0.55) + 0.006, True)]
    pts.append(T(0.64, top(0.64)))
    if fins:
        pts += [T(0.74, top(0.74)), T(0.765, top(0.765) + 0.03, True),
                T(0.79, top(0.79), True)]
    for t in (0.86, 0.95):
        pts.append(T(t, top(t)))
    # crescent tail: long upper lobe, short lower lobe
    pts += [T(1.0, top(1.0)), T(1.06, 0.07), T(1.17, 0.19, True),
            T(1.11, 0.09), T(1.065, 0.0, True), T(1.105, -0.06), T(1.15, -0.115, True),
            T(1.05, -0.05), T(1.0, -bot(1.0))]
    # belly, with anal, pelvic and the long pectoral fin
    for t in (0.95, 0.86):
        pts.append(T(t, -bot(t)))
    if fins:
        pts += [T(0.80, -bot(0.80)), T(0.815, -bot(0.815) - 0.025, True),
                T(0.83, -bot(0.83), True)]
    pts += [T(0.72, -bot(0.72)), T(0.66, -bot(0.66), True), T(0.685, -bot(0.68) - 0.04, True),
            T(0.64, -bot(0.64), True), T(0.52, -bot(0.52)), T(0.36, -bot(0.36)),
            T(0.33, -bot(0.33), True), T(0.36, -bot(0.36) - 0.05),
            T(0.43, -bot(0.36) - 0.17, True), T(0.33, -bot(0.30) - 0.10),
            T(0.25, -bot(0.25) - 0.035), T(0.19, -bot(0.19), True)]
    for t in (0.12, 0.06, 0.025):
        pts.append(T(t, -bot(t)))
    return smooth(pts)


def sliver(S, a, b, v, w0, w1=None, steps=10, dv=0.0):
    """A thin pale stripe from (t=a) to (t=b) at flank height v, w in lengths."""
    w1 = w0 if w1 is None else w1
    up, down = [], []
    for i in range(steps + 1):
        s = i / steps
        t = a + (b - a) * s
        vv = v + dv * s
        w = (w0 + (w1 - w0) * s) * math.sin(math.pi * s) ** 0.35
        mid = flank(t, vv)
        up.append(S(t, mid + w / 2))
        down.append(S(t, mid - w / 2))
    pts = [up[0] + (True,)] + up[1:-1] + [up[-1] + (True,)] + down[::-1][1:-1]
    return smooth(pts)


def bar(S, t, v0, v1, w, lean=0.0):
    """A pale bar across the back from v0 up to v1, leaning back as it rises."""
    pts = []
    n = 6
    for i in range(n + 1):
        s = i / n
        tt = t + lean * s
        pts.append(S(tt + w / 2 * math.sin(math.pi * s) ** 0.4, flank(tt, v0 + (v1 - v0) * s)))
    back = []
    for i in range(n + 1):
        s = i / n
        tt = t + lean * s
        back.append(S(tt - w / 2 * math.sin(math.pi * s) ** 0.4, flank(tt, v0 + (v1 - v0) * s)))
    pts = [pts[0] + (True,)] + pts[1:-1] + [pts[-1] + (True,)] + back[::-1][1:-1]
    return smooth(pts)


def details(S, level):
    """Spots, bars, ridge, gills, mouth and eye as closed paths inside the outline.
    level 3 = full, 2 = compact (32 px), 1 = tiny (16 px)."""
    L = S.L
    d = []
    if level == 1:
        d.append(circle(S(0.07, -0.005), 0.016 * L))
        for t, v, r in ((0.30, 0.45, 0.022), (0.44, 0.35, 0.018), (0.57, 0.3, 0.014)):
            d.append(circle(S(t, flank(t, v)), r * L))
        return d
    # mouth: a wide straight slit at the very front, and the eye behind its corner
    d.append(sliver(S, 0.0, 0.07 if level == 3 else 0.08, -0.42, 0.007 if level == 3 else 0.012, 0.004, steps=6))
    d.append(circle(S(0.085, -0.02), (0.0085 if level == 3 else 0.014) * L))
    if level == 3:
        # gill slits
        for i, t in enumerate((0.165, 0.18, 0.195, 0.21, 0.225)):
            d.append(sliver(S, t, t + 0.004, 0, 0.0, steps=2) if False else
                     bar(S, t, -0.62, 0.25 - 0.04 * i, 0.0045, lean=-0.006))
        # fine spots over the head
        for t in [0.035 + 0.019 * i for i in range(7)]:
            for v in [-0.2 + 0.19 * j for j in range(7)]:
                vv = v + (0.09 if round((t - 0.035) / 0.019) % 2 else 0)
                if vv > 0.9 or (t < 0.11 and vv < 0.12) or t + 0.0 > 0.16:
                    continue
                d.append(circle(S(t, flank(t, vv)), 0.0055 * L * (1 - 0.15 * (t < 0.07))))
        # ridge along the flank
        d.append(sliver(S, 0.25, 0.94, 0.12, 0.0065, 0.003, steps=16, dv=-0.05))
        # bars across the back, with columns of spots between them
        ts = [0.27 + 0.055 * i for i in range(11)]
        for i, t in enumerate(ts):
            if 0.43 <= t <= 0.56 or t > 0.85:
                pass
            if t < 0.88:
                d.append(bar(S, t, 0.24, 0.88, 0.007))
            if i + 1 < len(ts) and t + 0.0275 < 0.9:
                m = t + 0.0275
                for v in (0.4, 0.66):
                    d.append(circle(S(m, flank(m, v)), 0.0058 * L))
        # spots along the lower flank
        for i in range(17):
            t = 0.24 + 0.042 * i
            if t > 0.93:
                break
            for v, r in ((-0.22, 0.0062), (-0.6, 0.0052)):
                if v < -0.5 and (0.24 < t < 0.4 or t > 0.82):
                    continue
                d.append(circle(S(t + (0.021 if v < -0.5 else 0), flank(t, v)), r * L))
        # spots on the pectoral fin
        for t, n in ((0.27, -0.115), (0.30, -0.135), (0.32, -0.11), (0.335, -0.16), (0.36, -0.19), (0.29, -0.095)):
            d.append(circle(S(t, n), 0.0055 * L))
    else:
        # compact: a few bold bars and spots
        d.append(sliver(S, 0.27, 0.86, 0.1, 0.012, 0.006, steps=12, dv=-0.05))
        for t in (0.30, 0.40, 0.58, 0.69):
            d.append(bar(S, t, 0.28, 0.86, 0.014))
        for t, v in ((0.05, 0.45), (0.10, 0.5), (0.13, 0.05), (0.17, 0.48), (0.35, 0.55),
                     (0.35, -0.45), (0.50, -0.45), (0.63, 0.55), (0.65, -0.45), (0.78, -0.4)):
            d.append(circle(S(t, flank(t, v)), 0.0105 * L))
    return d


# ---------------------------------------------------------------- small fish

FISH = [(0.5, 0, True), (0.33, -0.1), (0.05, -0.13), (-0.22, -0.07), (-0.33, -0.015, True),
        (-0.5, -0.15, True), (-0.43, 0, True), (-0.5, 0.15, True), (-0.33, 0.015, True),
        (-0.22, 0.07), (0.05, 0.12), (0.33, 0.09)]


def fish(x, y, heading_deg, size):
    a = math.radians(heading_deg)
    c, s = math.cos(a), -math.sin(a)
    pts = []
    for p in FISH:
        px, py = p[0] * size, p[1] * size
        q = (x + px * c - py * s, y + px * s + py * c)
        pts.append(q + (True,) if len(p) > 2 else q)
    return smooth(pts)


# ---------------------------------------------------------------- the variants


def mark_a():
    """Diagonal: the shark rising to the upper right, a school following it."""
    ang = 32
    S = straight(214, 66, ang, 196)
    school = []
    # in the shark's frame: (t along, n across) in lengths, fish size in px
    for t, n, sz in ((0.45, 0.33, 19), (0.62, 0.27, 18), (0.78, 0.36, 17), (0.88, 0.22, 16),
                     (1.02, 0.30, 15), (0.66, 0.45, 15), (0.17, -0.27, 17), (0.40, -0.30, 18),
                     (0.58, -0.25, 17), (0.74, -0.33, 16), (0.92, -0.28, 15), (0.51, -0.43, 15),
                     (0.05, 0.22, 16)):
        x, y = S(t, n)
        school.append(fish(x, y, ang, sz))
    return S, 3, school


def mark_b():
    """The turn: the shark sweeping round in a gentle S, the school in its wake."""
    p0, p1, p2, p3 = (14, 30), (-10, 300), (200, 290), (236, 70)
    path = lambda s: bez(p0, p1, p2, p3, s)
    L = 172
    S = Spine(path, L)
    school = []
    # fish sit on the path behind the tail, a little either side of it
    for d, off, sz in ((L + 50, -12, 21), (L + 54, 26, 20), (L + 80, 8, 20), (L + 92, -26, 19),
                       (L + 104, 34, 19), (L + 122, -4, 19), (L + 146, 24, 18), (L + 152, -20, 18),
                       (L + 176, 2, 17), (L + 200, 22, 16), (L + 204, -16, 16), (L + 228, 2, 15)):
        p, h = S.at(d)
        up = (h[1], -h[0])
        x, y = p[0] + up[0] * off, p[1] + up[1] * off
        school.append(fish(x, y, math.degrees(math.atan2(-h[1], h[0])), sz))
    return S, 3, school


def mark_c():
    """Compact: a bigger shark, bold marks and four fish."""
    ang = 32
    S = straight(226, 58, ang, 212)
    school = []
    for t, n, sz in ((0.56, 0.36, 30), (0.86, 0.32, 28), (0.50, -0.34, 30), (0.80, -0.33, 28)):
        x, y = S(t, n)
        school.append(fish(x, y, ang, sz))
    return S, 2, school


def mark_c16():
    """The 16 px form: the shark alone, filling the square."""
    ang = 32
    S = straight(236, 52, ang, 230)
    return S, 1, []


VARIANTS = {"a": mark_a, "b": mark_b, "c": mark_c, "c16": mark_c16}

COLOURINGS = {
    # name: (background tile or None, shark, details, fish)
    "mono-black": (None, "#000", None, "#000"),
    "mono-white": (None, "#fff", None, "#fff"),
    "reef": ("#0f1720", "#35c9c0", "#fff", "#35c9c0"),
    "deep": (None, "#3b5b8f", "#8fc9ef", "#3b5b8f"),
}


def draw(variant):
    S, level, school = VARIANTS[variant]()
    return outline(S, fins=level > 1), "".join(details(S, level)), "".join(school)


def fitted(variant, margin):
    """Draw once to measure, then again scaled and centred in the 256 square."""
    XF[:] = [1.0, 0.0, 0.0]
    SEEN.clear()
    draw(variant)
    xs, ys = [p[0] for p in SEEN], [p[1] for p in SEEN]
    w, h = max(xs) - min(xs), max(ys) - min(ys)
    k = (256 - 2 * margin) / max(w, h)
    XF[:] = [k, 128 - k * (min(xs) + max(xs)) / 2, 128 - k * (min(ys) + max(ys)) / 2]
    out = draw(variant)
    XF[:] = [1.0, 0.0, 0.0]
    return out


def svg(variant, colouring):
    tile, body, spots, fishc = COLOURINGS[colouring]
    shape, det, school = fitted(variant, 26 if tile else 6)
    out = ['<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 256 256" width="256" height="256">',
           f'  <title>WhaleShark {variant.upper()} ({colouring})</title>']
    if tile:
        out.append(f'  <rect width="256" height="256" rx="48" fill="{tile}"/>')
    if spots is None:
        out.append(f'  <path fill="{body}" fill-rule="evenodd" d="{shape}{det}"/>')
    else:
        out.append(f'  <path fill="{body}" d="{shape}"/>')
        out.append(f'  <path fill="{spots}" d="{det}"/>')
    if school:
        out.append(f'  <path fill="{fishc}" d="{school}"/>')
    out.append("</svg>")
    return "\n".join(out) + "\n"


FILES = [("a", c) for c in ("mono-black", "mono-white", "reef", "deep")] + \
        [(v, c) for v in ("b", "c", "c16") for c in ("mono-black", "mono-white", "reef")]

for v, c in FILES:
    with open(os.path.join(HERE, f"{v}-{c}.svg"), "w") as fh:
        fh.write(svg(v, c))

# ---------------------------------------------------------------- the page

NAVY, LIGHT = "#0f1720", "#f6f4ee"


def inline(v, c, size):
    body = svg(v, c).split("\n", 2)[2]  # drop the opening tag and the title
    return (f'<svg viewBox="0 0 256 256" width="{size}" height="{size}" aria-hidden="true">'
            f'{body.replace("</svg>", "").strip()}</svg>')


def ground(v, c, dark, small=None):
    small = small or v
    cls = "dark" if dark else "light"
    big = f'<div class="big">{inline(v, c, 256)}</div>'
    rows = "".join(f'<div class="lockup s{s}">{inline(small if s == 16 else v, c, s)}<span>WhaleShark</span></div>'
                   for s in (64, 32, 16))
    return f'<div class="ground {cls}">{big}<div class="lockups">{rows}</div></div>'


SECTIONS = [
    ("a", "A · Diagonal",
     "The shark rising on a diagonal, a school of thirteen small fish swimming with it.",
     [("one colour", "mono-white", "mono-black"), ("reef", "reef", "reef"), ("deep blue", "deep", "deep")]),
    ("b", "B · The turn",
     "The shark coming out of a U-turn and rising; the school of twelve follows the curve round behind it, a team in its wake.",
     [("one colour", "mono-white", "mono-black"), ("reef", "reef", "reef")]),
    ("c", "C · Compact",
     "A bigger shark, bold bars and spots, four fish: for 32 px. At 16 px it drops to the shark alone (C16).",
     [("one colour", "mono-white", "mono-black"), ("reef", "reef", "reef")]),
]

parts = []
for v, title, note, rows in SECTIONS:
    parts.append(f"<section><h2>{title}</h2><p>{note}</p>")
    for label, on_dark, on_light in rows:
        small = "c16" if v == "c" else None
        parts.append(f'<h3>{label}</h3><div class="pair">{ground(v, on_dark, True, small)}'
                     f'{ground(v, on_light, False, small)}</div>')
    parts.append("</section>")

page = f"""<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>WhaleShark logo, round two</title>
<style>
  body {{ margin: 0; padding: 32px; background: #e4e8ec; color: #1f2933;
         font: 15px/1.4 -apple-system, BlinkMacSystemFont, "Helvetica Neue", Arial, sans-serif; }}
  h1 {{ margin: 0 0 4px; font-size: 26px; }}
  h1 + p {{ margin: 0 0 24px; color: #5a6672; }}
  section {{ margin: 0 0 40px; }}
  h2 {{ margin: 0; font-size: 20px; }}
  h2 + p {{ margin: 2px 0 10px; color: #5a6672; }}
  h3 {{ margin: 12px 0 6px; font-size: 13px; text-transform: uppercase; letter-spacing: .06em; color: #5a6672; }}
  .pair {{ display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }}
  .ground {{ display: flex; align-items: center; gap: 28px; padding: 20px 24px; border-radius: 12px; }}
  .dark {{ background: {NAVY}; color: #e6edf3; }}
  .light {{ background: {LIGHT}; color: {NAVY}; }}
  .big svg {{ display: block; }}
  .lockups {{ display: flex; flex-direction: column; gap: 16px; }}
  .lockup {{ display: flex; align-items: center; gap: .3em; font-weight: 700; letter-spacing: -.01em; }}
  .lockup svg {{ display: block; flex: none; }}
  .s64 {{ font-size: 40px; }}
  .s32 {{ font-size: 22px; }}
  .s16 {{ font-size: 13px; }}
</style>
</head>
<body>
<h1>WhaleShark logo, round two</h1>
<p>A whale shark and the school that follows it: the small fish are the agents, the shark the orchestrator. Each mark at 256 px, and at 64, 32 and 16 px beside the name, on navy and on a light ground.</p>
{"".join(parts)}
</body>
</html>
"""

with open(os.path.join(HERE, "index.html"), "w") as fh:
    fh.write(page)
