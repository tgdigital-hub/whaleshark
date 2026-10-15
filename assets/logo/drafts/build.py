#!/usr/bin/env python3
"""Builds index.html from the SVG drafts beside it: each SVG is inlined once as a
<symbol>, and every sample on the page draws it with <use>. Run from anywhere:

    python3 assets/logo/drafts/build.py
"""
import os
import re

HERE = os.path.dirname(os.path.abspath(__file__))

DESIGNS = [
    ("a-silhouette", "A · Silhouette",
     "Bold flat shape: broad flat head, straight mouth, pectoral and dorsal fins, tall crescent tail."),
    ("b-spots", "B · Spots",
     "The same turn with the pale stripes and spots cut out as negative space."),
    ("c-icon", "C · App icon",
     "The spotted shark on a rounded navy tile, for app, tab and favicon use."),
    ("d-line", "D · Line mark",
     "One even stroke: back, flat snout, mouth, belly and crescent tail, with a row of spots."),
]

NAVY, CREAM, TEXT_DARK, TEXT_LIGHT = "#0b1620", "#faf6ec", "#e6edf3", "#1f2933"


def symbol(name):
    svg = open(os.path.join(HERE, name + ".svg")).read()
    svg = re.sub(r"<title>.*?</title>\s*", "", svg)
    svg = re.sub(r"<svg[^>]*>", f'<symbol id="{name}" viewBox="0 0 128 128">', svg, count=1)
    return svg.replace("</svg>", "</symbol>").strip()


def mark(name, size):
    return f'<svg width="{size}" height="{size}" aria-hidden="true"><use href="#{name}"/></svg>'


def ground(name, dark):
    cls = "dark" if dark else "light"
    big = f'<div class="big">{mark(name, 192)}</div>'
    rows = "".join(
        f'<div class="lockup s{s}">{mark(name, s)}<span>WhaleShark</span></div>' for s in (64, 32, 16))
    return f'<div class="ground {cls}">{big}<div class="lockups">{rows}</div></div>'


def section(name, title, note):
    out = [f'<section><h2>{title}</h2><p>{note}</p>']
    for variant, label in ((name, "colour"), (name + "-mono", "one colour")):
        out.append(f'<h3>{label}</h3><div class="pair">{ground(variant, True)}{ground(variant, False)}</div>')
    out.append("</section>")
    return "".join(out)


symbols = "\n".join(symbol(n) for d in DESIGNS for n in (d[0], d[0] + "-mono"))
sections = "\n".join(section(*d) for d in DESIGNS)

page = f"""<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>WhaleShark logo drafts</title>
<style>
  body {{ margin: 0; padding: 32px; background: #e9edf1; color: {TEXT_LIGHT};
         font: 15px/1.4 -apple-system, BlinkMacSystemFont, "Helvetica Neue", Arial, sans-serif; }}
  h1 {{ margin: 0 0 4px; font-size: 26px; }}
  h1 + p {{ margin: 0 0 24px; color: #5a6672; }}
  section {{ margin: 0 0 40px; }}
  h2 {{ margin: 0; font-size: 20px; }}
  h2 + p {{ margin: 2px 0 10px; color: #5a6672; }}
  h3 {{ margin: 10px 0 6px; font-size: 13px; text-transform: uppercase; letter-spacing: .06em; color: #5a6672; }}
  .pair {{ display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }}
  .ground {{ display: flex; align-items: center; gap: 28px; padding: 20px 24px; border-radius: 12px; }}
  .dark {{ background: {NAVY}; color: {TEXT_DARK}; }}
  .light {{ background: {CREAM}; color: {NAVY}; }}
  .big svg {{ display: block; }}
  .lockups {{ display: flex; flex-direction: column; gap: 14px; }}
  .lockup {{ display: flex; align-items: center; gap: .35em; font-weight: 700; letter-spacing: -.01em; }}
  .lockup svg {{ display: block; flex: none; }}
  .s64 {{ font-size: 40px; }}
  .s32 {{ font-size: 22px; }}
  .s16 {{ font-size: 13px; }}
</style>
</head>
<body>
<svg width="0" height="0" style="position:absolute">
{symbols}
</svg>
<h1>WhaleShark logo drafts</h1>
<p>Four marks of a whale shark doing a U-turn. Each at 192 px, and at 64, 32 and 16 px beside the name, on the reef navy and on a light ground. One-colour versions take the text colour.</p>
{sections}
</body>
</html>
"""

open(os.path.join(HERE, "index.html"), "w").write(page)
