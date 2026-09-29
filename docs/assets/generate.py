"""Generate the README's animated graphics from real tool output.

Run the gate first, then this script:

    go run ./cmd/invariant verify -out out/02-twophase-commit examples/02-twophase-commit
    python3 docs/assets/generate.py

Every number, state and line of code in these graphics comes from that run's
receipt and counterexample trace, or from the example's source files (D-0015).
The factory's timeline comes from factory-run.json, which
snapshot_factory_run.py records from GitHub. The animations are SVG SMIL,
which GitHub renders in READMEs.
"""
from datetime import datetime
import html
import json
import xml.dom.minidom
import math
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(os.path.dirname(HERE))
sys.path.insert(0, os.path.join(ROOT, "docs", "brand"))
import generate as brand  # noqa: E402  (the brand geometry)

RUN = os.path.join(ROOT, "out", "02-twophase-commit")
EXAMPLE = os.path.join(ROOT, "examples", "02-twophase-commit")
SANS = "-apple-system, BlinkMacSystemFont, 'Segoe UI', Helvetica, Arial, sans-serif"
MONO = "ui-monospace, SFMono-Regular, Menlo, Consolas, 'Liberation Mono', monospace"
f = brand.f

# GitHub's light and dark palettes, plus the brand accent.
LIGHT = dict(bg="#FFFFFF", panel="#F6F8FA", line="#D0D7DE", text="#1F2328", muted="#656D76",
             arrow="#8C959F", accent="#0CA678", accent_bg="#E6F7F0", accent_text="#0B7A58",
             amber="#BF8700", green="#1A7F37", red="#CF222E", blue="#0969DA")
DARK = dict(bg="#0D1117", panel="#161B22", line="#30363D", text="#E6EDF3", muted="#8B949E",
            arrow="#6E7681", accent="#38D9A9", accent_bg="#0F2A22", accent_text="#38D9A9",
            amber="#D29922", green="#3FB950", red="#F85149", blue="#58A6FF")


# ---------------------------------------------------------------- SMIL helpers

def esc(s):
    return html.escape(s, quote=True)


def keytimes(times, dur):
    return ";".join("0" if t == 0 else f"{t / dur:.4f}" for t in times)


def steps(attr, initial, changes, dur):
    """A discrete animation: attr starts at initial, then takes each
    (time, value) in changes. It restarts every dur seconds."""
    values, times = [initial], [0.0]
    for t, v in sorted(changes):
        if t <= 0:
            values[0] = v
        elif t < dur:
            values.append(v)
            times.append(t)
    return (f'<animate attributeName="{attr}" values="{";".join(values)}" keyTimes="{keytimes(times, dur)}" '
            f'dur="{dur}s" calcMode="discrete" repeatCount="indefinite"/>')


def shown(start, end, dur):
    """Visible during [start, end) of every cycle."""
    changes = [(start, "1")] + ([(end, "0")] if end < dur else [])
    return steps("opacity", "0", changes, dur)


def travel(path, start, end, dur):
    """Move along path during [start, end], resting at its ends otherwise."""
    times, points = [0.0], ["0"]
    if start > 0:
        times.append(start)
        points.append("0")
    times.append(end)
    points.append("1")
    if end < dur:
        times.append(dur)
        points.append("1")
    return (f'<animateMotion path="{path}" keyPoints="{";".join(points)}" keyTimes="{keytimes(times, dur)}" '
            f'calcMode="linear" dur="{dur}s" repeatCount="indefinite"/>')


def ramp(attr, frm, to, start, end, dur):
    """Animate attr linearly from frm to to during [start, end], holding
    frm before and to after."""
    times, values = [0.0], [frm]
    if start > 0:
        times.append(start)
        values.append(frm)
    times.append(end)
    values.append(to)
    if end < dur:
        times.append(dur)
        values.append(to)
    return (f'<animate attributeName="{attr}" values="{";".join(values)}" keyTimes="{keytimes(times, dur)}" '
            f'calcMode="linear" dur="{dur}s" repeatCount="indefinite"/>')


def svg_doc(width, height, body, label, extra_defs=""):
    return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {f(width)} {f(height)}" width="{f(width)}" '
            f'height="{f(height)}" role="img" aria-label="{esc(label)}">\n<title>{esc(label)}</title>\n'
            f'{extra_defs}{body}\n</svg>\n')


def card(width, height, c, title, subtitle):
    """A dark window: rounded frame, header rule, title and subtitle."""
    return (f'<rect x="0.5" y="0.5" width="{width - 1}" height="{height - 1}" rx="14" fill="{c["bg"]}" stroke="{c["line"]}"/>\n'
            + "".join(f'<circle cx="{22 + 16 * i}" cy="22" r="5" fill="{c["line"]}"/>' for i in range(3))
            + f'\n<text x="78" y="27" font-family="{SANS}" font-size="13" font-weight="600" fill="{c["text"]}">{esc(title)}</text>\n'
            f'<text x="{width - 20}" y="27" text-anchor="end" font-family="{MONO}" font-size="12" fill="{c["muted"]}">{esc(subtitle)}</text>\n'
            f'<path d="M1 44H{width - 1}" stroke="{c["line"]}"/>\n')


def poster(doc, t):
    """Give every animated attribute its value at time t as its static value,
    so a viewer that doesn't run SVG animation still shows a complete frame.
    Viewers that do animate are unaffected, since an animation overrides the
    static value."""
    dom = xml.dom.minidom.parseString(doc)
    for anim in dom.getElementsByTagName("animate"):
        attr = anim.getAttribute("attributeName")
        if attr not in ("opacity", "fill", "stroke", "width", "stroke-dashoffset"):
            continue
        values = anim.getAttribute("values").split(";")
        dur = float(anim.getAttribute("dur").rstrip("s"))
        times = [float(k) for k in anim.getAttribute("keyTimes").split(";")]
        x = (t % dur) / dur
        i = max(n for n, k in enumerate(times) if k <= x)
        value = values[i]
        if anim.getAttribute("calcMode") == "linear" and i + 1 < len(times) and times[i + 1] > times[i]:
            a, b = float(values[i]), float(values[i + 1])
            value = f(a + (b - a) * (x - times[i]) / (times[i + 1] - times[i]))
        anim.parentNode.setAttribute(attr, value)
    return dom.documentElement.toxml() + "\n"


def write(path, text):
    with open(path, "w") as fh:
        fh.write(text)
    print("wrote", os.path.relpath(path, ROOT))


# ---------------------------------------------------------------- 1. the logo

def logo(c):
    """The lockup, with the state travelling its orbit around the fixed point.
    A fading trail follows the state; the invariant pulses and never moves."""
    g, cx, cy, R = brand.LOCKUP, brand.MX, brand.MY, brand.R
    at = brand.NODES[0]
    lead = math.degrees((g["BEAD"] + g["CLEAR"] + g["S"] / 2) / R)
    start, end = at + lead, at + 360 - (lead + 30)
    x1, y1 = brand.pt(cx, cy, R, start)
    x2, y2 = brand.pt(cx, cy, R, end)
    bx, by = brand.pt(cx, cy, R, at)
    ghosts = ""
    for back, r, opacity in ((11, 3.7, 0.5), (21, 2.8, 0.3), (30, 1.9, 0.15)):
        gx, gy = brand.pt(cx, cy, R, at - back)
        ghosts += f'<circle cx="{f(gx)}" cy="{f(gy)}" r="{r}" fill="{c["text"]}" opacity="{opacity}"/>'
    orbit = (f'<g>\n<path d="M{f(x1)},{f(y1)}A{f(R)},{f(R)} 0 1 1 {f(x2)},{f(y2)}" fill="none" stroke="{c["text"]}" '
             f'stroke-width="{g["S"]}" stroke-linecap="round"/>\n{ghosts}\n'
             f'<circle cx="{f(bx)}" cy="{f(by)}" r="{g["BEAD"]}" fill="{c["text"]}"/>\n'
             f'<animateTransform attributeName="transform" type="rotate" from="0 {f(cx)} {f(cy)}" '
             f'to="360 {f(cx)} {f(cy)}" dur="12s" repeatCount="indefinite"/>\n</g>')
    pulse = (f'<circle cx="{f(cx)}" cy="{f(cy)}" r="{g["CORE"]}" fill="{c["accent"]}">'
             f'<animate attributeName="r" values="{g["CORE"]};{g["CORE"] + 11}" dur="3s" repeatCount="indefinite"/>'
             f'<animate attributeName="opacity" values="0.45;0" dur="3s" repeatCount="indefinite"/></circle>')
    core = f'<circle cx="{f(cx)}" cy="{f(cy)}" r="{g["CORE"]}" fill="{c["accent"]}"/>'
    word = brand.wordmark(brand.WX, {"ink": c["text"], "accent": c["accent"]})
    body = (f'<g fill="none" stroke-linecap="round" stroke-linejoin="round">\n{pulse}\n{orbit}\n{core}\n{word}\n</g>')
    w, top, h = brand.W, brand.TOP - 2, brand.BOT - brand.TOP + 4
    doc = svg_doc(w, h, body, "Invariant")
    return doc.replace(f'viewBox="0 0 {f(w)} {f(h)}" width="{f(w)}" height="{f(h)}"',
                       f'viewBox="0 {f(top)} {f(w)} {f(h)}" width="{f(w * 2)}" height="{f(h * 2)}"')


# ---------------------------------------------------------------- 2. the pipeline

PIPELINE = [("Issue", "what's asked", 92), ("Formalize", "statements + forks", 118),
            ("Ratify", "people decide", 104), ("Synthesize", "code + TLA+ model", 124),
            ("Gate", "TLC · proofs · tests", 84), ("Merge", "PR + receipt", 96)]


def pipeline(c):
    """The factory's loop, running: a change moves through each stage, fails
    the gate once, is repaired against the counterexample, then merges."""
    T, gap, x0, y, h = 12.0, 44, 21, 44, 44
    nodes, x = {}, x0
    for label, caption, w in PIPELINE:
        nodes[label] = (x, w, caption)
        x += w + gap
    width = x - gap + x0
    mid = lambda n: nodes[n][0] + nodes[n][1] / 2  # noqa: E731

    # (stage, start, end, colour key, status line)
    visits = [("Issue", 0.0, 0.7, "accent", "An issue asks for a change."),
              ("Formalize", 1.1, 2.1, "accent", "Invariant drafts the statements and surfaces the forks."),
              ("Ratify", 2.5, 4.1, "accent", "People ratify what must be true."),
              ("Synthesize", 4.5, 5.3, "accent", "Invariant writes the code and the model."),
              ("Gate", 5.7, 6.5, "amber", "The gate finds a counterexample."),
              ("Synthesize", 7.3, 8.1, "amber", "The counterexample drives a repair."),
              ("Gate", 8.5, 9.3, "green", "Every check passes."),
              ("Merge", 9.7, 11.6, "green", "Merged, with a receipt.")]
    hops = [("Issue", "Formalize", 0.7), ("Formalize", "Ratify", 2.1), ("Ratify", "Synthesize", 4.1),
            ("Synthesize", "Gate", 5.3), ("Synthesize", "Gate", 8.1), ("Gate", "Merge", 9.3)]
    arc = f"M{f(mid('Gate'))},{y - 3}C{f(mid('Gate'))},4 {f(mid('Synthesize'))},4 {f(mid('Synthesize'))},{y - 5}"

    out = [f'<defs><marker id="arrow" viewBox="0 0 10 10" refX="8.5" refY="5" markerWidth="7" markerHeight="7" '
           f'orient="auto-start-reverse"><path d="M1,1L9,5L1,9" fill="none" stroke="{c["arrow"]}" stroke-width="1.6" '
           f'stroke-linecap="round" stroke-linejoin="round"/></marker></defs>',
           f'<g font-family="{SANS}" text-anchor="middle">']
    # Halos sit behind the nodes and light up while a stage is working.
    for stage, start, end, colour, _ in visits:
        nx, w, _ = nodes[stage]
        out.append(f'<rect x="{nx - 4}" y="{y - 4}" width="{w + 8}" height="{h + 8}" rx="13" fill="{c[colour]}" '
                   f'opacity="0">{steps("opacity", "0", [(start, "0.22"), (end, "0")], T)}</rect>')
    for label, (nx, w, caption) in nodes.items():
        ratify = label == "Ratify"
        base = c["accent"] if ratify else c["line"]
        changes = []
        for stage, start, end, colour, _ in visits:
            if stage == label:
                changes += [(start, c[colour]), (end, base)]
        out.append(f'<rect x="{nx}" y="{y}" width="{w}" height="{h}" rx="10" fill="{c["accent_bg"] if ratify else c["bg"]}" '
                   f'stroke="{base}" stroke-width="1.5">{steps("stroke", base, changes, T)}</rect>')
        out.append(f'<text x="{f(nx + w / 2)}" y="{y + 27.5}" font-size="15" font-weight="600" '
                   f'fill="{c["accent_text"] if ratify else c["text"]}">{label}</text>')
        out.append(f'<text x="{f(nx + w / 2)}" y="{y + h + 22}" font-size="12" fill="{c["muted"]}">{esc(caption)}</text>')
    for a, b in zip(PIPELINE, PIPELINE[1:]):
        (ax, aw, _), (bx, _, _) = nodes[a[0]], nodes[b[0]]
        out.append(f'<path d="M{ax + aw + 7},{y + h / 2}H{bx - 7}" stroke="{c["arrow"]}" stroke-width="1.5" marker-end="url(#arrow)"/>')
    out.append(f'<path d="{arc}" fill="none" stroke="{c["arrow"]}" stroke-width="1.5" stroke-dasharray="4 4" marker-end="url(#arrow)"/>')
    out.append(f'<text x="{f((mid("Gate") + mid("Synthesize")) / 2)}" y="{y - 7}" font-size="12" fill="{c["muted"]}">counterexample</text>')
    # The change itself: a dot hopping between stages.
    for a, b, start in hops:
        (ax, aw, _), (bx, _, _) = nodes[a], nodes[b]
        path = f"M{ax + aw + 4},{y + h / 2}H{bx - 4}"
        colour = c["green"] if (a, start) in (("Synthesize", 8.1), ("Gate", 9.3)) else c["accent"]
        out.append(f'<circle r="4.5" fill="{colour}" opacity="0">{travel(path, start, start + 0.4, T)}'
                   f'{shown(start, start + 0.4, T)}</circle>')
    out.append(f'<circle r="4.5" fill="{c["amber"]}" opacity="0">{travel(arc, 6.5, 7.3, T)}{shown(6.5, 7.3, T)}</circle>')
    # A check lands on Merge.
    mx, mw, _ = nodes["Merge"]
    out.append(f'<g opacity="0">{shown(9.9, 11.6, T)}<circle cx="{mx + mw - 3}" cy="{y + 3}" r="9" fill="{c["green"]}"/>'
               f'<path d="M{mx + mw - 7},{y + 3}l3,3l5-6" fill="none" stroke="#FFFFFF" stroke-width="2" '
               f'stroke-linecap="round" stroke-linejoin="round"/></g>')
    # A status line narrates.
    for i, (stage, start, _, colour, words) in enumerate(visits):
        until = visits[i + 1][1] if i + 1 < len(visits) else T - 0.3
        tone = c["accent_text"] if stage == "Ratify" else (c[colour] if colour != "accent" else c["text"])
        out.append(f'<text x="{f(width / 2)}" y="150" font-size="13.5" fill="{tone}" opacity="0">{shown(start, until, T)}'
                   f'{esc(words)}</text>')
    out.append("</g>")
    return svg_doc(width, 164, "\n".join(out),
                   "Invariant's loop: an issue is formalized, people ratify the statements, Invariant synthesizes "
                   "code and a model, the gate finds a counterexample, the code is repaired, every check passes, and it merges.")


# ---------------------------------------------------------------- 3. the counterexample

STATE_COLOUR = {"working": "muted", "prepared": "blue", "committed": "green", "aborted": "red"}
EXPLAIN = {
    "Initial predicate": "Every resource manager starts out working. No messages yet.",
    "RMPrepare": "{r} prepares and sends Prepared to the coordinator.",
    "RMChooseToAbort": "{r} hasn't prepared yet, so it may abort on its own. It does.",
    "TMRcvPrepared": "The coordinator records {r}'s Prepared.",
    "TMCommit": "Every resource manager has prepared, so the coordinator commits.",
    "EarlyCommit": "The buggy coordinator commits after hearing from {heard} alone.",
    "TMAbort": "The coordinator aborts.",
    "RMRcvCommitMsg": "{r} receives Commit and commits.",
    "RMRcvAbortMsg": "{r} receives Abort and aborts.",
}


def trace_card(trace, receipt):
    """The real counterexample TLC found for the planted bug, replayed."""
    c, W, H = DARK, 860, 348
    states = trace["states"]
    rms = sorted(states[0]["vars"]["rmState"])
    starts = [0.0] + [1.8 + 2.5 * i for i in range(len(states) - 1)]
    T = starts[-1] + 4.6
    ends = starts[1:] + [T]
    coord = (40, 118, 230, 112)            # x, y, w, h
    hub = (coord[0] + coord[2], coord[1] + coord[3] / 2)
    box_y = {rm: 64 + i * 78 for i, rm in enumerate(rms)}
    rm_x, rm_w, rm_h = 600, 220, 60
    lane = {rm: (rm_x, box_y[rm] + rm_h / 2) for rm in rms}

    out = [card(W, H, c, "TLC counterexample · the planted early-commit bug", "RM = {" + ", ".join(rms) + "}"),
           f'<g font-family="{SANS}">']
    for rm in rms:  # message lanes
        out.append(f'<path d="M{hub[0]},{hub[1]}L{lane[rm][0]},{lane[rm][1]}" stroke="{c["line"]}" stroke-dasharray="3 5"/>')
    # The coordinator.
    x, y, w, h = coord
    out.append(f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="12" fill="{c["panel"]}" stroke="{c["line"]}"/>')
    out.append(f'<text x="{x + 18}" y="{y + 30}" font-size="14" font-weight="600" fill="{c["text"]}">Coordinator</text>')

    def runs(values):
        """Collapse per-step values into (value, start, end) runs."""
        out, i = [], 0
        while i < len(values):
            j = i
            while j + 1 < len(values) and values[j + 1] == values[i]:
                j += 1
            out.append((values[i], starts[i], ends[j]))
            i = j + 1
        return out

    for value, s, e in runs([st["vars"]["tmState"] for st in states]):
        out.append(f'<text x="{x + 18}" y="{y + 58}" font-family="{MONO}" font-size="12.5" fill="{c["muted"]}" opacity="0">'
                   f'{shown(s, e, T)}state <tspan fill="{c["text"]}">{esc(value)}</tspan></text>')
    for value, s, e in runs([", ".join(sorted(st["vars"]["tmPrepared"])) for st in states]):
        out.append(f'<text x="{x + 18}" y="{y + 84}" font-family="{MONO}" font-size="12.5" fill="{c["muted"]}" opacity="0">'
                   f'{shown(s, e, T)}heard from <tspan fill="{c["text"]}">{{{esc(value)}}}</tspan></text>')
    # The resource managers.
    last = states[-1]["vars"]["rmState"]
    committed = [r for r in rms if last[r] == "committed"]
    aborted = [r for r in rms if last[r] == "aborted"]
    violated_at = starts[-1] + 0.6
    for rm in rms:
        by = box_y[rm]
        seq = [st["vars"]["rmState"][rm] for st in states]
        if rm in committed[:1] or rm in aborted[:1]:
            out.append(f'<rect x="{rm_x - 5}" y="{by - 5}" width="{rm_w + 10}" height="{rm_h + 10}" rx="15" fill="none" '
                       f'stroke="{c["red"]}" stroke-width="2" opacity="0">{shown(violated_at, T, T)}'
                       f'<animate attributeName="stroke-opacity" values="1;0.35;1" dur="1.2s" repeatCount="indefinite"/></rect>')
        out.append(f'<rect x="{rm_x}" y="{by}" width="{rm_w}" height="{rm_h}" rx="10" fill="{c["panel"]}" stroke="{c["line"]}"/>')
        colour_changes = [(s, c[STATE_COLOUR[v]]) for v, s, _ in runs(seq)]
        out.append(f'<rect x="{rm_x}" y="{by}" width="6" height="{rm_h}" rx="3" fill="{c[STATE_COLOUR[seq[0]]]}">'
                   f'{steps("fill", c[STATE_COLOUR[seq[0]]], colour_changes, T)}</rect>')
        out.append(f'<text x="{rm_x + 22}" y="{by + 25}" font-size="14" font-weight="600" fill="{c["text"]}">{rm}</text>')
        for v, s, e in runs(seq):
            out.append(f'<text x="{rm_x + 22}" y="{by + 46}" font-family="{MONO}" font-size="12.5" '
                       f'fill="{c[STATE_COLOUR[v]]}" opacity="0">{shown(s, e, T)}{v}</text>')
    # Steps: which resource manager acted, the message it sent, and a caption.
    for i, st in enumerate(states):
        action, param, words = st["action"], "", EXPLAIN.get(st["action"], "")
        if i > 0:
            prev = states[i - 1]["vars"]
            changed = [r for r in rms if st["vars"]["rmState"][r] != prev["rmState"][r]]
            heard = sorted(set(st["vars"]["tmPrepared"]) - set(prev["tmPrepared"]))
            param = (changed or heard or [""])[0]
            words = words.format(r=param, heard=" and ".join(sorted(st["vars"]["tmPrepared"])))
        label = f"{action}({param})" if param else action
        s, e = starts[i], ends[i]
        if action == "RMPrepare":
            path = f"M{lane[param][0]},{lane[param][1]}L{hub[0]},{hub[1]}"
            out.append(f'<g opacity="0">{shown(s, s + 1.0, T)}{travel(path, s, s + 1.0, T)}'
                       f'<circle r="5" fill="{c["blue"]}"/><text y="-9" text-anchor="middle" font-size="11" '
                       f'fill="{c["blue"]}">Prepared</text></g>')
        if action in ("TMCommit", "EarlyCommit", "TMAbort"):
            kind, tone = ("Abort", c["red"]) if action == "TMAbort" else ("Commit", c["green"])
            for n, rm in enumerate(rms):
                path = f"M{hub[0]},{hub[1]}L{lane[rm][0]},{lane[rm][1]}"
                label = (f'<text y="-9" text-anchor="middle" font-size="11" fill="{tone}">{kind}</text>' if n == 0 else "")
                out.append(f'<g opacity="0">{shown(s, s + 1.0, T)}{travel(path, s, s + 1.0, T)}'
                           f'<circle r="5" fill="{tone}"/>{label}</g>')
        final = i == len(states) - 1
        out.append(f'<text x="40" y="{H - 44}" font-size="14" font-weight="600" fill="{c["text"]}" opacity="0">'
                   f'{shown(s, violated_at if final else e, T)}Step {i + 1} of {len(states)} · '
                   f'<tspan font-family="{MONO}" font-size="13" fill="{c["accent"]}">{esc(label)}</tspan></text>')
        out.append(f'<text x="40" y="{H - 22}" font-size="13" fill="{c["muted"]}" opacity="0">'
                   f'{shown(s, violated_at if final else e, T)}{esc(words)}</text>')
        dot_x = W - 40 - (len(states) - 1 - i) * 20
        out.append(f'<circle cx="{dot_x}" cy="{H - 34}" r="4.5" fill="{c["line"]}">'
                   f'{steps("fill", c["line"], [(s, c["accent"]), (e, c["line"])], T)}</circle>')
    steps_to_bug = len(states) - 1
    distinct = receipt["design"]["distinct_states"]
    out.append(f'<text x="40" y="{H - 44}" font-size="14" font-weight="600" fill="{c["red"]}" opacity="0">'
               f'{shown(violated_at, T, T)}✗ {esc(trace["violated"])} violated: {committed[0]} committed while '
               f'{aborted[0]} aborted</text>')
    out.append(f'<text x="40" y="{H - 22}" font-size="13" fill="{c["muted"]}" opacity="0">{shown(violated_at, T, T)}'
               f'Caught in {steps_to_bug} steps. With the real coordinator, none of the {distinct} reachable '
               f'states does this.</text>')
    out.append("</g>")
    return svg_doc(W, H, "\n".join(out),
                   f"Replay of the real TLC counterexample for the planted early-commit bug: {steps_to_bug} steps end "
                   f"with {committed[0]} committed while {aborted[0]} aborted, violating {trace['violated']}.")


# ---------------------------------------------------------------- 4. model and code, side by side

TLA_OPS = r'(?:\\/|/\\|==|\\cup|\|->|<<|>>|\'|!|EXCEPT|UNCHANGED)'


def tla_spans(line, c):
    out = []
    for part in re.split(r'("[^"]*"|' + TLA_OPS + ')', line):
        if not part:
            continue
        if part.startswith('"'):
            out.append((part, "#A5D6FF"))
        elif re.fullmatch(TLA_OPS, part):
            out.append((part, "#FF7B72" if part.isalpha() else "#D2A8FF"))
        else:
            out.append((part, c["text"]))
    return out


def go_spans(line, c):
    if line.lstrip().startswith("// @"):
        indent = line[:len(line) - len(line.lstrip())]
        rest = line.lstrip()[4:]
        word, _, tail = rest.lstrip().partition(" ")
        return [(indent + "// @ ", c["muted"]), (word, "#FF7B72"), (" " + tail, c["accent"])]
    if line.lstrip().startswith("//"):
        return [(line, c["muted"])]
    out = []
    for part in re.split(r'(\bfunc\b|\breturn\b|\bif\b|\bState\b|\bint\b|\bbool\b|\btrue\b|\bfalse\b)', line):
        if part in ("func", "return", "if"):
            out.append((part, "#FF7B72"))
        elif part in ("State", "int", "bool"):
            out.append((part, "#FFA657"))
        elif part in ("true", "false"):
            out.append((part, "#79C0FF"))
        elif part:
            out.append((part, c["text"]))
    return out


def source_lines():
    """The RMPrepare action, and the Go method whose contract restates it,
    without the clauses that only grant it access to memory."""
    spec = open(os.path.join(EXAMPLE, ".invariant", "specs", "TwoPhase.tla")).read().split("\n")
    i = next(i for i, l in enumerate(spec) if l.startswith("RMPrepare(r) =="))
    j = next(j for j in range(i + 1, len(spec)) if not spec[j].strip())
    tla = spec[i:j]
    code = open(os.path.join(EXAMPLE, "twophase", "twophase.go")).read().replace("\t", "    ").split("\n")
    k = next(k for k, l in enumerate(code) if re.match(r"func (\([^)]*\) )?RMPrepare\(", l))
    s = k
    while code[s - 1].startswith("//"):
        s -= 1
    e = next(e for e in range(k, len(code)) if code[e] == "}")
    return tla, [l for l in code[s:e + 1] if "acc(" not in l]


def dual_card(receipt):
    """The RMPrepare action above the Go method whose contract restates it,
    with each part of the action matched to the clauses that restate it. The
    code refuses a step the model can't take by itself, so its contract says
    both outcomes: what a step does when it's taken, and that a refusal
    changes nothing (D-0082, D-0090)."""
    c, W = DARK, 960
    tla, go = source_lines()
    lh, px, pw = 18, 20, W - 40
    # Which lines restate which: (the model's part, the code's part, words
    # in the model, words in the code).
    pairs = [("the enabling condition", "ok exactly when it held", ['= "working"'], ["ensures ok == ("]),
             ("the effect", "what a step it takes does", ["rmState'"], ["ensures ok ==> "]),
             ("no step when it doesn't hold", "a refusal changes nothing", ['= "working"'], ["ensures !ok ==> ", "return false"]),
             ("everything else unchanged", "nothing else changes", ["UNCHANGED"], ["ensures forall", "ensures t.TM ==", "ensures len("]),
             ("the message it sends", "the caller sends it", ["msgs'"], ["The caller sends"])]
    for model, code, mw, cw in pairs:
        if not any(w in l for l in tla for w in mw) or not any(w in l for l in go for w in cw):
            sys.exit(f"the example no longer has what the card pairs: {model} with {code}")
    panes, y = [], 56
    for heading, lines, spans in (("TwoPhase.tla · the model", tla, tla_spans),
                                  ("twophase.go · the code, without the clauses that grant it memory", go, go_spans)):
        h = 66 + (len(lines) - 1) * lh
        panes.append((y, h, heading, lines, spans))
        y += h + 12
    caption_y = y + 14
    box = caption_y + 12
    H = box + 58 + 14
    per = (0.3, 0.2)
    typed = 0.4 + sum(per[side] * len(p[3]) + 0.3 for side, p in enumerate(panes))
    cmd_start = typed + 0.2
    results_at = cmd_start + 1.4
    pair_start = results_at + 1.4
    T = round(pair_start + 2.0 * len(pairs) + 1.2, 1)

    defs = ['<defs>']
    t = 0.4
    for side, (py, _, _, lines, _) in enumerate(panes):
        for n in range(len(lines)):
            base = py + 48 + n * lh
            defs.append(f'<clipPath id="type{side}_{n}"><rect x="{px}" y="{base - 14}" height="{lh}" width="0">'
                        f'{ramp("width", "0", str(pw), t, t + per[side], T)}</rect></clipPath>')
            t += per[side]
        t += 0.3
    defs.append(f'<clipPath id="cmd"><rect x="36" y="{box + 8}" height="22" width="0">'
                f'{ramp("width", "0", "520", cmd_start, cmd_start + 1.0, T)}</rect></clipPath>')
    defs.append('</defs>')

    out = [card(W, H, c, "Each contract restates one TLA+ action", "RMPrepare"), f'<g font-family="{SANS}">']
    for side, (py, h, heading, lines, spans) in enumerate(panes):
        out.append(f'<rect x="{px}" y="{py}" width="{pw}" height="{h}" rx="10" fill="{c["panel"]}" stroke="{c["line"]}"/>')
        out.append(f'<text x="{px + 16}" y="{py + 22}" font-size="12" font-weight="600" fill="{c["muted"]}">{esc(heading)}</text>')
        for p, (_, _, model, code) in enumerate(pairs):
            words = model if side == 0 else code
            s = pair_start + p * 2.0
            for n, line in enumerate(lines):
                if any(wd in line for wd in words):
                    out.append(f'<rect x="{px + 6}" y="{py + 48 + n * lh - 14}" width="{pw - 12}" height="{lh}" rx="4" '
                               f'fill="{c["accent"]}" fill-opacity="0.16" opacity="0">{shown(s, s + 2.0, T)}</rect>')
        for n, line in enumerate(lines):
            tspans = "".join(f'<tspan fill="{col}">{esc(txt)}</tspan>' for txt, col in spans(line, c))
            out.append(f'<text x="{px + 16}" y="{py + 48 + n * lh}" xml:space="preserve" font-family="{MONO}" font-size="12" '
                       f'clip-path="url(#type{side}_{n})">{tspans}</text>')
    for p, (model, code, _, _) in enumerate(pairs):
        s = pair_start + p * 2.0
        out.append(f'<text x="{W / 2}" y="{caption_y}" text-anchor="middle" font-size="13" fill="{c["accent"]}" opacity="0">'
                   f'{shown(s, s + 2.0, T)}{esc(model)} in the model  ⟷  {esc(code)} in the code</text>')
    # The gate's verdict, from the receipt.
    yb = box + 22
    out.append(f'<rect x="20" y="{box}" width="{W - 40}" height="58" rx="10" fill="{c["panel"]}" stroke="{c["line"]}"/>')
    out.append(f'<text x="36" y="{yb}" xml:space="preserve" font-family="{MONO}" font-size="12.5" clip-path="url(#cmd)">'
               f'<tspan fill="{c["accent"]}">$</tspan><tspan fill="{c["text"]}"> invariant verify examples/02-twophase-commit</tspan></text>')
    d, code = receipt["design"], receipt["code"]
    tlc = f"TLC    {d['distinct_states']} states, no violations, no deadlock"
    gobra = f"Gobra  {len(code['functions'])} of {len(code['functions'])} functions verified, overflow checked"
    for n, (words, at) in enumerate(((tlc, results_at), (gobra, results_at + 0.5))):
        out.append(f'<text x="{36 + n * 440}" y="{yb + 24}" xml:space="preserve" font-family="{MONO}" font-size="12.5" '
                   f'opacity="0">{shown(at, T, T)}<tspan fill="{c["green"]}">✓ </tspan><tspan fill="{c["text"]}">{esc(words)}</tspan></text>')
    out.append('</g>')
    return svg_doc(W, H, "\n".join(out),
                   "The TLA+ action RMPrepare above the Go method RMPrepare, whose Gobra contract restates it: "
                   "ok exactly when the enabling condition held, the effect when the step is taken, nothing changed "
                   "when it's refused, and nothing else changed either; the caller sends the message. "
                   f"TLC checks {d['distinct_states']} states; Gobra verifies all {len(code['functions'])} functions.",
                   "\n".join(defs) + "\n")


# ---------------------------------------------------------------- 5. the receipt

def receipt_card(r):
    """The receipt, checking itself off row by row."""
    c, W = DARK, 860
    T = 12.0
    d, code = r["design"], r["code"]
    reached = [w for w in r["witnesses"] if w["reached"]]
    caught = [b for b in r["bugs"] if b["caught"]]
    a, larger, conf = r["agreement"], r.get("larger"), r.get("conformance")
    bounds = ", ".join(f"{k} = {v}" for k, v in sorted(r["bounds"].items()))
    ratified = r.get("ratified")
    unverified = code.get("unverified") or []
    rows = [
        ("Pinned statements", f"{sum(p['match'] for p in r['pins'])} of {len(r['pins'])} match",
         f"ratified by @{ratified['by']} on #{ratified['issue']}" if ratified else f"recorded in {r['decision']}"),
        ("Design · TLC", "no violations, no deadlock", f"{d['distinct_states']} distinct states, depth {d['depth']}"),
        ("Reachability", f"{len(reached)} of {len(r['witnesses'])} witnesses reached",
         ", ".join(f"{w['name']} in {w['steps']} steps" for w in reached)),
        ("Known bugs", f"{len(caught)} of {len(r['bugs'])} caught",
         ", ".join(f"{b['label']} after {b['steps']} steps" for b in caught)),
        ("Agreement", "code reaches the model's states", f"{a['states']} states, depth {a['depth']}"),
    ]
    if larger:
        within = ", ".join(f"{k} = {v}" for k, v in sorted(larger["bounds"].items()))
        rows.append(("One size larger", "code reaches the model's states", f"{larger['states']:,} states within {within}"))
    rows.append((f"Code · {code['verifier']}", f"{len(code['functions'])} of {len(code['functions'])} functions verified",
                 ", ".join((["overflow checked"] if code["overflow_checked"] else [])
                           + ([f"{len(unverified)} unverified"] if unverified else []))))
    if conf and conf.get("exhaustive"):
        rows.append(("Conformance", "every reachable state, no step outside it",
                     f"{conf['states']} of {conf['model_states']} model states"))
    if conf and conf.get("tried"):
        rows.append(("Every step tried", "in every state reached", f"{conf['tried']['attempts']:,} attempts, refusals included"))
    rows.append(("Build", ", ".join(s["name"] for s in r["build"]["steps"]), "sandboxed, no network"))
    # The card grows with the rows: the rule under them, then the verdict.
    rule = 84 + len(rows) * 40 - 18
    H = rule + 78
    out = [card(W, H, c, f"Invariant receipt · {r['project']}", "invariant verify"), f'<g font-family="{SANS}">']
    for i, (check, result, evidence) in enumerate(rows):
        s = 0.6 + i * 0.55
        y = 84 + i * 40
        out.append(f'<g opacity="0">{shown(s, T - 0.6, T)}'
                   f'<animateTransform attributeName="transform" type="translate" values="-10 0;-10 0;0 0;0 0" '
                   f'keyTimes="0;{s / T:.4f};{(s + 0.3) / T:.4f};1" dur="{T}s" repeatCount="indefinite"/>'
                   f'<circle cx="44" cy="{y}" r="10" fill="{c["green"]}" fill-opacity="0.18" stroke="{c["green"]}"/>'
                   f'<path d="M39,{y}l3.5,3.5l6.5-7" fill="none" stroke="{c["green"]}" stroke-width="2" stroke-linecap="round" '
                   f'stroke-linejoin="round" stroke-dasharray="16" stroke-dashoffset="16">'
                   f'{ramp("stroke-dashoffset", "16", "0", s + 0.15, s + 0.45, T)}</path>'
                   f'<text x="68" y="{y + 5}" font-size="14" font-weight="600" fill="{c["text"]}">{esc(check)}</text>'
                   f'<text x="250" y="{y + 5}" font-size="14" fill="{c["text"]}">{esc(result)}</text>'
                   f'<text x="{W - 32}" y="{y + 5}" text-anchor="end" font-family="{MONO}" font-size="12" '
                   f'fill="{c["muted"]}">{esc(evidence)}</text></g>')
    done = 0.6 + len(rows) * 0.55 + 0.4
    out.append(f'<path d="M20 {rule}H{W - 20}" stroke="{c["line"]}"/>')
    out.append(f'<g opacity="0">{shown(done, T - 0.6, T)}'
               f'<rect x="32" y="{rule + 20}" width="208" height="30" rx="15" fill="{c["green"]}" fill-opacity="0.16" stroke="{c["green"]}"/>'
               f'<text x="136" y="{rule + 40}" text-anchor="middle" font-size="13" font-weight="600" fill="{c["green"]}">'
               f'✓ invariant/gate passed</text>'
               f'<text x="{W - 32}" y="{rule + 30}" text-anchor="end" font-family="{MONO}" font-size="12" fill="{c["muted"]}">'
               f'exhaustive within {esc(bounds)}</text>'
               f'<text x="{W - 32}" y="{rule + 48}" text-anchor="end" font-family="{MONO}" font-size="12" fill="{c["muted"]}">'
               f'fingerprint {esc(r["fingerprint"][:19])}…</text></g>')
    out.append('</g>')
    return svg_doc(W, H, "\n".join(out),
                   f"Invariant receipt for {r['project']}: every check passed. " + " ".join(f"{a}: {b}." for a, b, _ in rows))


# ---------------------------------------------------------------- 6. the factory

def clock(stamp):
    return datetime.strptime(stamp, "%Y-%m-%dT%H:%M:%SZ")


def clip(text, n):
    """Shorten text to at most n characters, at a word boundary."""
    if len(text) <= n:
        return text
    cut = text[:n - 1]
    if " " in cut:
        cut = cut[:cut.rindex(" ")]
    return cut.rstrip(" ,.;:") + "…"


def factory_events(run):
    """The run's events, in order: who, what, and the evidence, all read from
    the snapshot."""
    issue, pr = run["issue"], run["pr"]
    events = [(issue["opened"], "person", f"@{issue['by']}", f"opened #{issue['number']}", clip(issue["title"], 58))]
    body = pr["body"]
    forks = {}
    for c in run["comments"]:
        m = c["marker"]
        if m is None:
            lines = [l.strip().strip("`") for l in c["body"].splitlines() if l.strip().strip("`").startswith("/invariant ")]
            verb = lines[0].split()[1] if lines else ""
            if verb == "choose":
                picks = []
                for l in lines:
                    fork, option = l.split()[2:4]
                    says = next(o["says"] for o in forks[fork]["options"] if o["id"] == option)
                    picks.append(f"{fork} {option}: {clip(says, 28)}")
                events.append((c["at"], "person", f"@{c['by']}", "decided", " · ".join(picks)))
            elif verb == "ratify":
                events.append((c["at"], "person", f"@{c['by']}", "ratified", "proposal " + lines[0].split()[2]))
            continue
        kind = m["kind"]
        if kind == "forks":
            forks = {f["id"]: f for f in m["forks"]}
            events.append((c["at"], "factory", "Invariant", f"asked {len(m['forks'])} questions instead of guessing",
                           " · ".join(f"{f['id']} {clip(f['question'], 30)}" for f in m["forks"])))
        elif kind == "proposal":
            p = m["proposal"]
            n = {k: sum(s["kind"] == k for s in p["statements"]) for k in ("invariant", "witness", "bug")}
            tlc = re.search(r"TLC explored ([\d,]+) states", c["body"])
            events.append((c["at"], "factory", "Invariant", f"proposed {len(p['statements'])} statements",
                           f"{n['invariant']} invariants, {n['witness']} witnesses, {n['bug']} known bugs · TLC {tlc.group(1)} states"))
        elif kind == "pr":
            code = re.search(r"Code · (\w+) \| ✅ proved: (\d+) of (\d+) functions", body)
            agree = re.search(r"Agreement \| ✅ [^|]*\| ([\d,]+) states", body)
            events.append((pr["opened"], "factory", "Invariant", f"opened #{pr['number']}: proved",
                           f"{code.group(1)} {code.group(2)} of {code.group(3)} functions · code reaches all {agree.group(1)} states"))
        elif kind == "merged":
            events.append((pr["merged"], "factory", "Invariant", f"merged #{pr['number']} on green",
                           f"invariant/gate passed in CI · {pr['merge_commit'][:7]}"))
    return events


def factory_card(run):
    """The factory's first issue, from opened to merged, as it happened."""
    c, W = DARK, 860
    events = factory_events(run)
    T = 3.0 + len(events) * 1.1 + 4.0
    top, gap = 78, 50
    H = top + gap * len(events) + 66
    start = clock(events[0][0])
    out = [card(W, H, c, f"Invariant · issue #{run['issue']['number']} → merged", run["repo"]), f'<g font-family="{SANS}">']
    rail_x = 132
    rail = f"M{rail_x} {top} V{top + gap * (len(events) - 1)}"
    length = gap * (len(events) - 1)
    out.append(f'<path d="{rail}" stroke="{c["line"]}" stroke-width="2"/>')
    out.append(f'<path d="{rail}" stroke="{c["accent"]}" stroke-width="2" stroke-dasharray="{length}" stroke-dashoffset="{length}">'
               f'{ramp("stroke-dashoffset", str(length), "0", 0.6, 0.6 + (len(events) - 1) * 1.1, T)}</path>')
    for i, (at, who, actor, action, detail) in enumerate(events):
        s = 0.6 + i * 1.1
        y = top + i * gap
        elapsed = clock(at) - start
        mins, secs = divmod(int(elapsed.total_seconds()), 60)
        stamp = clock(at).strftime("%H:%M:%S")
        since = "" if i == 0 else f"+{mins}m {secs:02d}s"
        person = who == "person"
        colour = c["blue"] if person else c["accent"]
        node = (f'<circle cx="{rail_x}" cy="{y}" r="7" fill="{c["bg"]}" stroke="{colour}" stroke-width="2"/>' if person else
                f'<circle cx="{rail_x}" cy="{y}" r="7" fill="{colour}"/><circle cx="{rail_x}" cy="{y}" r="11" fill="none" '
                f'stroke="{colour}" stroke-opacity="0.35"/>')
        out.append(f'<g opacity="0">{shown(s, T - 0.6, T)}'
                   f'<text x="30" y="{y - 2}" font-family="{MONO}" font-size="12" fill="{c["text"]}">{stamp}</text>'
                   f'<text x="30" y="{y + 14}" font-family="{MONO}" font-size="11" fill="{c["muted"]}">{since}</text>'
                   f'{node}'
                   f'<text x="160" y="{y - 2}" font-size="14" fill="{c["text"]}"><tspan font-weight="600" fill="{colour}">{esc(actor)}</tspan> {esc(action)}</text>'
                   f'<text x="160" y="{y + 16}" font-family="{MONO}" font-size="12" fill="{c["muted"]}">{esc(detail)}</text></g>')
    total = clock(events[-1][0]) - start
    people = sum(1 for e in events if e[1] == "person")
    done = 0.6 + len(events) * 1.1
    y = H - 34
    out.append(f'<path d="M20 {H - 60}H{W - 20}" stroke="{c["line"]}"/>')
    out.append(f'<g opacity="0">{shown(done, T - 0.6, T)}'
               f'<rect x="32" y="{y - 20}" width="236" height="30" rx="15" fill="#8250DF" fill-opacity="0.18" stroke="#A371F7"/>'
               f'<text x="150" y="{y}" text-anchor="middle" font-size="13" font-weight="600" fill="#A371F7">'
               f'✓ issue to merge in {int(total.total_seconds()) // 60} minutes</text>'
               f'<text x="{W - 32}" y="{y}" text-anchor="end" font-family="{MONO}" font-size="12" fill="{c["muted"]}">'
               f'{people} decisions by a person · every merge gated by CI</text></g>')
    out.append('</g>')
    label = (f"Invariant's first issue, #{run['issue']['number']}, from opened to merged in "
             f"{int(total.total_seconds()) // 60} minutes: " + " ".join(f"{e[2]} {e[3]} ({e[4]})." for e in events))
    return svg_doc(W, H, "\n".join(out), label)



# ---------------------------------------------------------------- 7. the crash test

def crash_card(run):
    """The crash test: the watcher stopped just before each of its effects,
    and a fresh one finishing the issue every time."""
    c, W = DARK, 860
    effects = run["effects"]
    steps_seen = []
    for e in effects:
        if e["step"] not in steps_seen:
            steps_seen.append(e["step"])
    RH, GH, TOP = 22, 28, 96
    H = TOP + len(steps_seen) * GH + len(effects) * RH + 104
    T = 13.0
    xs, xo = W - 214, W - 84
    stops = sum(1 for e in effects for k in ("same_machine", "another_machine") if e[k])
    recovered = sum(1 for e in effects for k in ("same_machine", "another_machine") if e[k] == "pass")
    out = [card(W, H, c, "Stop the watcher anywhere", f"go test -run {run['test']}"), f'<g font-family="{SANS}">']
    out.append(f'<text x="44" y="74" font-size="12" fill="{c["muted"]}" letter-spacing="0.6">STOPPED JUST BEFORE</text>')
    for x, label in ((xs, "SAME MACHINE"), (xo, "ANOTHER MACHINE")):
        out.append(f'<text x="{x}" y="74" text-anchor="middle" font-size="12" fill="{c["muted"]}" letter-spacing="0.6">{label}</text>')
    y, i, step = TOP - 10, 0, None
    last = 0.0
    for e in effects:
        if e["step"] != step:
            step = e["step"]
            y += GH
            out.append(f'<text x="30" y="{y}" font-size="13" font-weight="700" fill="{c["blue"]}">{esc(step)}</text>')
            out.append(f'<path d="M30 {y + 7}H{W - 30}" stroke="{c["line"]}"/>')
        y += RH
        s = 0.7 + i * (T - 4.2) / len(effects)
        last = s
        out.append(f'<text x="30" y="{y}" font-family="{MONO}" font-size="12" fill="{c["arrow"]}">{e["n"]}</text>'
                   f'<text x="56" y="{y}" font-family="{MONO}" font-size="12.5" fill="{c["text"]}">{esc(clip(e["stopped_before"], 58))}</text>')
        for x, key in ((xs, "same_machine"), (xo, "another_machine")):
            ok = e[key] == "pass"
            cy = y - 4
            # Stopped: a red mark, while the watcher is down.
            out.append(f'<g opacity="0">{shown(s, s + 0.55, T)}'
                       f'<circle cx="{x}" cy="{cy}" r="8" fill="{c["red"]}" fill-opacity="0.2" stroke="{c["red"]}"/>'
                       f'<path d="M{x - 3.5},{cy - 3.5}l7,7M{x + 3.5},{cy - 3.5}l-7,7" stroke="{c["red"]}" stroke-width="1.8" stroke-linecap="round"/></g>')
            # Recovered: a fresh watcher finished the issue, every effect once.
            color = c["green"] if ok else c["red"]
            mark = (f'<path d="M{x - 4},{cy}l3,3l5.5-6" fill="none" stroke="{color}" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/>'
                    if ok else f'<path d="M{x - 3.5},{cy - 3.5}l7,7M{x + 3.5},{cy - 3.5}l-7,7" stroke="{color}" stroke-width="1.8" stroke-linecap="round"/>')
            out.append(f'<g opacity="0">{shown(s + 0.55, T - 0.4, T)}'
                       f'<circle cx="{x}" cy="{cy}" r="8" fill="{color}" fill-opacity="0.16" stroke="{color}"/>{mark}</g>')
        i += 1
    done = last + 0.9
    fy = y + 26
    out.append(f'<path d="M20 {fy}H{W - 20}" stroke="{c["line"]}"/>')
    before = run.get("before")
    out.append(f'<g opacity="0">{shown(done, T - 0.4, T)}'
               f'<rect x="30" y="{fy + 20}" width="238" height="30" rx="15" fill="{c["green"]}" fill-opacity="0.16" stroke="{c["green"]}"/>'
               f'<text x="149" y="{fy + 40}" text-anchor="middle" font-size="13" font-weight="600" fill="{c["green"]}">'
               f'{stops} stops · {recovered} recoveries</text>'
               f'<text x="{W - 30}" y="{fy + 32}" text-anchor="end" font-family="{MONO}" font-size="12" fill="{c["muted"]}">'
               f'every effect once, and the issue merges</text>'
               + (f'<text x="{W - 30}" y="{fy + 50}" text-anchor="end" font-family="{MONO}" font-size="12" fill="{c["muted"]}">'
                  f'before #26: {before["broke"]} of {before["stops"]} stops broke the flow</text>' if before else "")
               + '</g>')
    out.append('</g>')
    return svg_doc(W, H, "\n".join(out),
                   f"The crash test: the watcher is stopped just before each of its {len(effects)} effects on one issue, "
                   f"and a fresh watcher, on the same machine or another, finishes it. {recovered} of {stops} stops recover, "
                   "with one post per command, one agent run per command or build, one pull request and one merge."
                   + (f" Before #26, {before['broke']} of {before['stops']} stops broke the flow." if before else ""))


def main():
    receipt = json.load(open(sys.argv[1] if len(sys.argv) > 1 else os.path.join(RUN, "receipt.json")))
    trace = json.load(open(sys.argv[2] if len(sys.argv) > 2 else os.path.join(RUN, "traces", "early-commit.json")))
    receipt.setdefault("bugs", receipt.get("mutants", []))
    if not receipt["passed"]:
        sys.exit("the receipt is failing; fix the gate before regenerating the README graphics")
    write(os.path.join(ROOT, "docs", "brand", "logo-animated.svg"), logo(LIGHT))
    write(os.path.join(ROOT, "docs", "brand", "logo-animated-dark.svg"), logo(DARK))
    # Still frames: the merge, the violation, the first matched pair, the
    # finished receipt.
    write(os.path.join(HERE, "how-it-works.svg"), poster(pipeline(LIGHT), 10.6))
    write(os.path.join(HERE, "how-it-works-dark.svg"), poster(pipeline(DARK), 10.6))
    write(os.path.join(HERE, "counterexample.svg"), poster(trace_card(trace, receipt), 15.0))
    write(os.path.join(HERE, "model-and-code.svg"), poster(dual_card(receipt), 9.4))
    write(os.path.join(HERE, "receipt.svg"), poster(receipt_card(receipt), 8.0))
    run = json.load(open(os.path.join(HERE, "factory-run.json")))
    write(os.path.join(HERE, "factory-run.svg"), poster(factory_card(run), 12.0))
    crashes = json.load(open(os.path.join(HERE, "crash-run.json")))
    write(os.path.join(HERE, "crash-anywhere.svg"), poster(crash_card(crashes), 12.5))


if __name__ == "__main__":
    main()
