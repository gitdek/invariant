"""Expands the contract macros in steps.tmpl into steps.go.

Run from factory/journal-merges:

    python3 tools/gen.py tools/steps.tmpl merges/steps.go

Each macro is a comment line of its own, indented as the line it stands for:

    {{main pre|post|inv F Q P}}   main's journal: fields at F, Len at Q, lines at P
    {{branch pre|post|inv F Q P}} the branch's, shaped as main's
    {{lens pre|post|inv F Q R}}   only main's and the branch's Len, at Q and R

A post or inv also says the fields and lengths kept their old values. Edit
steps.tmpl, never steps.go, and run this again.
"""
import re
import sys


def main(kind, F, Q, P):
    w = {"pre": "requires", "post": "ensures", "inv": "invariant"}[kind]
    out = [
        f"// @ {w} acc(&s.Lines, {F}) && acc(&s.Len, {F}) && acc(&s.Room, {F}) && 0 <= s.Room && s.Room <= MaxSize",
        f"// @ {w} len(s.Len) == len(s.Lines) && len(s.Lines) <= MaxSize",
    ]
    if kind != "pre":
        out.append(f"// @ {w} s.Room == old(s.Room) && len(s.Lines) == old(len(s.Lines)) && len(s.Len) == old(len(s.Len))")
    out += [
        f"// @ {w} forall g int :: {{ &s.Len[g] }} 0 <= g && g < len(s.Len) ==> acc(&s.Len[g], {Q}) && 0 <= s.Len[g] && s.Len[g] <= s.Room",
        f"// @ {w} forall g int :: {{ &s.Lines[g] }} 0 <= g && g < len(s.Lines) ==> acc(&s.Lines[g], {F}) && len(s.Lines[g]) == s.Room",
        f"// @ {w} forall g, k int :: {{ &s.Lines[g][k] }} 0 <= g && g < len(s.Lines) && 0 <= k && k < s.Room ==> acc(&s.Lines[g][k], {P})",
    ]
    return out


def branch(kind, F, Q, P):
    w = {"pre": "requires", "post": "ensures", "inv": "invariant"}[kind]
    out = [f"// @ {w} acc(&b.Lines, {F}) && acc(&b.Len, {F}) && len(b.Lines) == len(s.Lines) && len(b.Len) == len(s.Lines)"]
    if kind != "pre":
        out.append(f"// @ {w} len(b.Lines) == old(len(b.Lines)) && len(b.Len) == old(len(b.Len))")
    out += [
        f"// @ {w} forall g int :: {{ &b.Len[g] }} 0 <= g && g < len(b.Len) ==> acc(&b.Len[g], {Q}) && 0 <= b.Len[g] && b.Len[g] <= s.Room",
        f"// @ {w} forall g int :: {{ &b.Lines[g] }} 0 <= g && g < len(b.Lines) ==> acc(&b.Lines[g], {F}) && len(b.Lines[g]) == s.Room",
        f"// @ {w} forall g, k int :: {{ &b.Lines[g][k] }} 0 <= g && g < len(b.Lines) && 0 <= k && k < s.Room ==> acc(&b.Lines[g][k], {P})",
    ]
    return out


def lens(kind, F, Q, R):
    w = {"pre": "requires", "post": "ensures", "inv": "invariant"}[kind]
    out = [
        f"// @ {w} acc(&s.Len, {F}) && acc(&s.Room, {F}) && acc(&b.Len, {F}) && 0 <= s.Room && s.Room <= MaxSize",
        f"// @ {w} len(b.Len) == len(s.Len) && len(s.Len) <= MaxSize",
    ]
    if kind != "pre":
        out.append(f"// @ {w} s.Room == old(s.Room) && len(s.Len) == old(len(s.Len)) && len(b.Len) == old(len(b.Len))")
    out += [
        f"// @ {w} forall g int :: {{ &s.Len[g] }} 0 <= g && g < len(s.Len) ==> acc(&s.Len[g], {Q}) && 0 <= s.Len[g] && s.Len[g] <= s.Room",
        f"// @ {w} forall g int :: {{ &b.Len[g] }} 0 <= g && g < len(b.Len) ==> acc(&b.Len[g], {R}) && 0 <= b.Len[g] && b.Len[g] <= s.Room",
    ]
    return out


def expand(m):
    indent = m.group(1)
    name, kind, *args = m.group(2).split()
    lines = {"main": main, "branch": branch, "lens": lens}[name](kind, *args)
    return "\n".join(indent + l for l in lines)


src = open(sys.argv[1]).read()
out = re.sub(r"^(\t*)// \{\{(.+?)\}\}$", expand, src, flags=re.M)
open(sys.argv[2], "w").write(out)
