"""Read one top-level Python function for in-place proof without importing or running it.

The request on stdin is JSON naming a source, as base64, and a function. The
reply gives the function's byte spans in the original source, the names it
reads from its module and how it reads them, and how the module binds each
name at its top level, so the gate can build Nagini's input around the
original bytes. It serves a selected function and a trusted contract's
declaration alike.
"""

import ast
import base64
import json
import re
import sys


class Names(ast.NodeVisitor):
    """The names a function reads, and the ones it binds itself."""

    def __init__(self):
        self.loads = []
        self.bound = set()
        self.attributes = {}
        self.deep = set()
        self.unsupported = set()

    def visit_Name(self, node):
        if isinstance(node.ctx, ast.Load):
            self.loads.append(node.id)
        else:
            self.bound.add(node.id)

    def visit_Attribute(self, node):
        root, depth = node, 0
        while isinstance(root, ast.Attribute):
            root, depth = root.value, depth + 1
        if isinstance(root, ast.Name):
            if depth == 1:
                self.attributes.setdefault(root.id, set()).add(node.attr)
            else:
                self.deep.add(root.id)
        self.generic_visit(node)

    def visit_arg(self, node):
        self.bound.add(node.arg)
        if node.annotation is not None:
            self.visit(node.annotation)

    def visit_ExceptHandler(self, node):
        if node.name:
            self.bound.add(node.name)
        self.generic_visit(node)

    def refuse(self, what):
        def visit(node):
            self.unsupported.add(what)
            self.generic_visit(node)
        return visit

    def __getattr__(self, name):
        refused = {
            "visit_Global": "a global statement", "visit_Nonlocal": "a nonlocal statement",
            "visit_Import": "an import inside the function", "visit_ImportFrom": "an import inside the function",
            "visit_FunctionDef": "a nested function", "visit_AsyncFunctionDef": "a nested function",
            "visit_ClassDef": "a nested class", "visit_Lambda": "a lambda",
            "visit_Await": "await", "visit_Yield": "yield", "visit_YieldFrom": "yield",
        }
        if name in refused:
            return self.refuse(refused[name])
        raise AttributeError(name)


def free_names(node):
    """Each name the function reads that it doesn't bind, with its uses."""
    names = Names()
    args = node.args
    for default in args.defaults + [d for d in args.kw_defaults if d is not None]:
        names.visit(default)
    for a in args.posonlyargs + args.args + args.kwonlyargs:
        names.visit(a)
    for a in (args.vararg, args.kwarg):
        if a is not None:
            names.unsupported.add("*args or **kwargs")
            names.visit(a)
    if node.returns is not None:
        names.visit(node.returns)
    for decorator in node.decorator_list:
        names.visit(decorator)
    for statement in node.body:
        names.visit(statement)
    bases = set()
    for sub in ast.walk(node):
        if isinstance(sub, ast.Attribute):
            base = sub
            while isinstance(base, ast.Attribute):
                base = base.value
            bases.add(id(base))
    free = {}
    for name in names.loads:
        if name not in names.bound and name not in free:
            free[name] = {"attributes": sorted(names.attributes.get(name, ())), "bare": False, "deep": name in names.deep}
    for sub in ast.walk(node):
        if isinstance(sub, ast.Name) and isinstance(sub.ctx, ast.Load) and sub.id in free and id(sub) not in bases:
            free[sub.id]["bare"] = True
    return free, sorted(names.unsupported)


def immutable(value):
    if isinstance(value, tuple):
        return all(immutable(v) for v in value)
    return value is None or isinstance(value, (bool, int, float, complex, str, bytes))


def main():
    request = json.loads(sys.stdin.buffer.read())
    raw = base64.b64decode(request["source"])
    if raw.startswith(b"\xef\xbb\xbf"):
        raise ValueError("the source starts with a byte-order mark")
    text = raw.decode("utf-8")
    for line in text.split("\n", 2)[:2]:
        m = re.match(r"^[ \t\f]*#.*?coding[:=][ \t]*([-\w.]+)", line)
        if m and m.group(1).lower().replace("_", "-") not in ("utf-8", "utf8"):
            raise ValueError("the source declares the %s encoding, not UTF-8" % m.group(1))
    tree = ast.parse(text)

    # AST columns count UTF-8 bytes, so a line's start plus a column is a byte offset.
    starts = [0] + [m.end() for m in re.finditer(rb"\r\n|\r|\n", raw)]

    def offset(line, column):
        return starts[line - 1] + column

    def line_end(line):
        return starts[line] if line < len(starts) else len(raw)

    found = [n for n in tree.body if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)) and n.name == request["symbol"]]
    if len(found) != 1:
        raise LookupError("%s must name exactly one top-level declaration" % request["symbol"])
    node = found[0]
    if isinstance(node, ast.ClassDef):
        raise LookupError("%s is a class, and in-place proof selects a function" % node.name)
    first = node.decorator_list[0].lineno if node.decorator_list else node.lineno

    body = []
    for statement in node.body:
        kind = "other"
        if isinstance(statement, ast.Expr):
            value = statement.value
            if isinstance(value, ast.Call) and isinstance(value.func, ast.Name) and value.func.id in ("Requires", "Ensures", "Exsures", "Decreases"):
                kind = "contract"
            elif isinstance(value, ast.Constant) and value.value is Ellipsis:
                kind = "ellipsis"
            elif isinstance(value, ast.Constant) and isinstance(value.value, str):
                kind = "docstring"
        elif isinstance(statement, ast.Pass):
            kind = "pass"
        body.append({"kind": kind, "start": offset(statement.lineno, 0), "end": line_end(statement.end_lineno),
                     "line": statement.lineno, "end_line": statement.end_lineno})

    defline = raw[offset(node.lineno, 0):line_end(node.lineno)]
    m = re.match(rb"(async[ \t]+)?def[ \t]+" + re.escape(node.name.encode()) + rb"\b", defline)
    if not m:
        raise LookupError("%s's name isn't on its def line" % node.name)
    name_start = offset(node.lineno, 0) + m.end() - len(node.name.encode())

    decorators = []
    for d in node.decorator_list:
        decorators.append(raw[offset(d.lineno, d.col_offset):offset(d.end_lineno, d.end_col_offset)].decode("utf-8"))

    bindings, star = {}, []

    def bind(name, row):
        bindings.setdefault(name, []).append(row)

    # The gate copies whole lines, so a statement that shares a line with
    # another can't be copied alone.
    lines = {}
    for statement in tree.body:
        for line in range(statement.lineno, statement.end_lineno + 1):
            lines[line] = lines.get(line, 0) + 1

    def shared(statement):
        return any(lines[line] > 1 for line in range(statement.lineno, statement.end_lineno + 1))

    for statement in tree.body:
        where = {"start": offset(statement.lineno, 0), "end": line_end(statement.end_lineno), "line": statement.lineno,
                 "shared": shared(statement)}
        if isinstance(statement, ast.Import):
            for alias in statement.names:
                bind(alias.asname or alias.name.split(".")[0], dict(where, kind="import", module=alias.name, asname=alias.asname or ""))
        elif isinstance(statement, ast.ImportFrom):
            for alias in statement.names:
                if alias.name == "*":
                    star.append(dict(where, module=statement.module or "", level=statement.level))
                else:
                    bind(alias.asname or alias.name, dict(where, kind="from", module=statement.module or "", level=statement.level,
                                                          name=alias.name, asname=alias.asname or ""))
        elif isinstance(statement, (ast.Assign, ast.AnnAssign)):
            targets = statement.targets if isinstance(statement, ast.Assign) else [statement.target]
            # Only an immutable literal keeps its value: nothing can change
            # it in place, and a rebinding shows as a second binding.
            literal = False
            if statement.value is not None:
                try:
                    literal = immutable(ast.literal_eval(statement.value))
                except (ValueError, TypeError, SyntaxError, MemoryError, RecursionError):
                    literal = False
            simple = len(targets) == 1 and isinstance(targets[0], ast.Name)
            for target in targets:
                for sub in ast.walk(target):
                    if isinstance(sub, ast.Name):
                        bind(sub.id, dict(where, kind="assign" if simple else "other", literal=literal))
        elif isinstance(statement, (ast.FunctionDef, ast.AsyncFunctionDef)):
            bind(statement.name, dict(where, kind="function"))
        elif isinstance(statement, ast.ClassDef):
            bind(statement.name, dict(where, kind="class"))
        else:
            # Anything bound inside a compound statement, or rebound some
            # other way, binds conditionally: the gate can't preserve it.
            for sub in ast.walk(statement):
                if isinstance(sub, ast.Name) and not isinstance(sub.ctx, ast.Load):
                    bind(sub.id, dict(where, kind="conditional"))
                elif isinstance(sub, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
                    bind(sub.name, dict(where, kind="conditional"))
                elif isinstance(sub, (ast.Import, ast.ImportFrom)):
                    for alias in sub.names:
                        bind(alias.asname or alias.name.split(".")[0], dict(where, kind="conditional"))

    # Names any function in the module rebinds with a global statement, and
    # the module's __future__ imports, which change how it compiles.
    rebound = sorted({name for sub in ast.walk(tree) if isinstance(sub, ast.Global) for name in sub.names})
    future = [dict(start=offset(s.lineno, 0), end=line_end(s.end_lineno), line=s.lineno, shared=shared(s))
              for s in tree.body if isinstance(s, ast.ImportFrom) and s.module == "__future__"]

    free, unsupported = free_names(node)
    if body and body[0]["line"] == node.lineno:
        unsupported.append("a body on the def line")
    if node.col_offset != 0:
        unsupported.append("a declaration that isn't at the module's top level")
    print(json.dumps({
        "globals": rebound, "future": future,
        "start": offset(first, 0), "end": line_end(node.end_lineno), "line": first, "end_line": node.end_lineno,
        "def_line": node.lineno, "async": isinstance(node, ast.AsyncFunctionDef), "decorators": decorators,
        "header_end": body[0]["start"] if body else line_end(node.end_lineno),
        "name_start": name_start, "name_end": name_start + len(node.name.encode()),
        "body": body, "free": free, "unsupported": unsupported, "bindings": bindings, "star": star,
        "declarations": sorted(n.name for n in tree.body if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef))),
    }, sort_keys=True))


try:
    main()
except (SyntaxError, UnicodeError, LookupError, ValueError, KeyError, RecursionError) as error:
    sys.exit(str(error))
