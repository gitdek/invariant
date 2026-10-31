"""Read Python for in-place proof without importing or running it.

The request on stdin is JSON. With a "symbol", it names a source, as base64,
and one of its top-level functions: the reply gives the function's byte
spans in the original source, the names it reads from its module and how it
reads them, its exception handlers and what their bodies call, and how the
module binds each name at its top level, so the gate can build Nagini's
input around the original bytes. It serves a selected function and a trusted
contract's declaration alike. With "writes", it names a module and the
application's sources: the reply gives every place a source writes one of
that module's attributes.
"""

import ast
import base64
import json
import re
import sys

COMPREHENSIONS = (ast.ListComp, ast.SetComp, ast.DictComp, ast.GeneratorExp)
TRY = tuple(t for t in (ast.Try, getattr(ast, "TryStar", None)) if t is not None)

# Builtins and names that write a module's namespace in ways no binding shows.
DYNAMIC = ("globals", "vars", "setattr", "delattr", "exec", "eval", "__import__")

REFUSED = {
    ast.Global: "a global statement", ast.Nonlocal: "a nonlocal statement",
    ast.Import: "an import inside the function", ast.ImportFrom: "an import inside the function",
    ast.FunctionDef: "a nested function", ast.AsyncFunctionDef: "a nested function",
    ast.ClassDef: "a nested class", ast.Lambda: "a lambda",
    ast.Await: "await", ast.Yield: "yield", ast.YieldFrom: "yield",
}


def comprehension_targets(node):
    names = set()
    for generator in node.generators:
        for sub in ast.walk(generator.target):
            if isinstance(sub, ast.Name):
                names.add(sub.id)
    return names


class Bound(ast.NodeVisitor):
    """The names a function binds in its own scope. A comprehension's
    variables are its own, but an assignment expression in one binds in the
    function."""

    def __init__(self):
        self.names = set()

    def visit_Name(self, node):
        if not isinstance(node.ctx, ast.Load):
            self.names.add(node.id)

    def visit_arg(self, node):
        self.names.add(node.arg)

    def visit_ExceptHandler(self, node):
        if node.name:
            self.names.add(node.name)
        self.generic_visit(node)

    def visit_comprehension_node(self, node):
        self.visit(node.generators[0].iter)
        for sub in ast.walk(node):
            if isinstance(sub, getattr(ast, "NamedExpr", ())) and isinstance(sub.target, ast.Name):
                self.names.add(sub.target.id)

    visit_ListComp = visit_SetComp = visit_DictComp = visit_GeneratorExp = visit_comprehension_node


class Reads(ast.NodeVisitor):
    """Each name a function reads from beyond its own scope, how it reads it,
    and, for each exception handler, what its try body reads."""

    def __init__(self, bound):
        self.bound = bound
        self.scopes = []
        self.free = {}
        self.unsupported = set()
        self.bases = set()
        self.tries = []
        self.active = []

    def local(self, name):
        return name in self.bound or any(name in scope for scope in self.scopes)

    def read(self, name, node, attribute=None, deep=False):
        if self.local(name):
            return
        row = self.free.setdefault(name, {"attributes": [], "bare": False, "deep": False})
        if attribute is None and not deep:
            row["bare"] = True
        elif deep:
            row["deep"] = True
        elif attribute not in row["attributes"]:
            row["attributes"].append(attribute)
        for t in self.active:
            use = t["reads"].setdefault(name, {"attributes": [], "bare": False, "deep": False})
            if attribute is None and not deep:
                use["bare"] = True
            elif deep:
                use["deep"] = True
            elif attribute not in use["attributes"]:
                use["attributes"].append(attribute)

    def generic_visit(self, node):
        what = REFUSED.get(type(node))
        if what:
            self.unsupported.add(what)
        super().generic_visit(node)

    def visit_Name(self, node):
        if isinstance(node.ctx, ast.Load) and id(node) not in self.bases:
            self.read(node.id, node)

    def visit_Attribute(self, node):
        root, depth = node, 0
        while isinstance(root, ast.Attribute):
            root, depth = root.value, depth + 1
        if isinstance(root, ast.Name):
            self.bases.add(id(root))
            if isinstance(root.ctx, ast.Load):
                self.read(root.id, root, node.attr if depth == 1 else None, depth > 1)
        self.generic_visit(node)

    def visit_arg(self, node):
        if node.annotation is not None:
            self.visit(node.annotation)

    def visit_comprehension_node(self, node):
        first = node.generators[0]
        self.visit(first.iter)
        self.scopes.append(comprehension_targets(node))
        for i, generator in enumerate(node.generators):
            if i > 0:
                self.visit(generator.iter)
            for condition in generator.ifs:
                self.visit(condition)
        for part in ("elt", "key", "value"):
            if getattr(node, part, None) is not None:
                self.visit(getattr(node, part))
        self.scopes.pop()

    visit_ListComp = visit_SetComp = visit_DictComp = visit_GeneratorExp = visit_comprehension_node

    def visit_Try(self, node):
        handlers = []
        for h in node.handlers:
            if h.type is None:
                handlers.append("*")
            elif isinstance(h.type, ast.Name):
                handlers.append(h.type.id)
            elif isinstance(h.type, ast.Tuple) and all(isinstance(e, ast.Name) for e in h.type.elts):
                handlers.extend(e.id for e in h.type.elts)
            else:
                handlers.append("?")
        record = {"line": node.lineno, "handlers": handlers, "reads": {}}
        self.tries.append(record)
        self.active.append(record)
        for statement in node.body:
            self.visit(statement)
        self.active.pop()
        for part in (node.handlers, node.orelse, node.finalbody):
            for statement in part:
                self.visit(statement)

    visit_TryStar = visit_Try


def function(node):
    """Read a function's own names: its signature's and its body's."""
    bound = Bound()
    args = node.args
    for a in args.posonlyargs + args.args + args.kwonlyargs:
        bound.visit(a)
    for statement in node.body:
        bound.visit(statement)
    reads = Reads(bound.names)
    for default in args.defaults + [d for d in args.kw_defaults if d is not None]:
        reads.visit(default)
    for a in args.posonlyargs + args.args + args.kwonlyargs:
        reads.visit(a)
    for a in (args.vararg, args.kwarg):
        if a is not None:
            reads.unsupported.add("*args or **kwargs")
            reads.visit(a)
            bound.names.add(a.arg)
    if node.returns is not None:
        reads.visit(node.returns)
    for decorator in node.decorator_list:
        reads.visit(decorator)
    for statement in node.body:
        reads.visit(statement)
    for name in reads.free:
        reads.free[name]["attributes"].sort()
    return reads.free, sorted(reads.unsupported), sorted(bound.names), reads.tries


def immutable(value):
    if isinstance(value, tuple):
        return all(immutable(v) for v in value)
    return value is None or isinstance(value, (bool, int, float, complex, str, bytes))


def lines_of(raw):
    starts = [0] + [m.end() for m in re.finditer(rb"\r\n|\r|\n", raw)]

    def offset(line, column):
        return starts[line - 1] + column

    def line_end(line):
        return starts[line] if line < len(starts) else len(raw)

    return offset, line_end


def decode(raw):
    if raw.startswith(b"\xef\xbb\xbf"):
        raise ValueError("the source starts with a byte-order mark")
    text = raw.decode("utf-8")
    for line in text.split("\n", 2)[:2]:
        m = re.match(r"^[ \t\f]*#.*?coding[:=][ \t]*([-\w.]+)", line)
        if m and m.group(1).lower().replace("_", "-") not in ("utf-8", "utf8"):
            raise ValueError("the source declares the %s encoding, not UTF-8" % m.group(1))
    return text


def dynamic(tree):
    """Each place the module writes its namespace in a way no binding shows."""
    out = []
    for sub in ast.walk(tree):
        if isinstance(sub, ast.Name) and sub.id in DYNAMIC:
            out.append({"line": sub.lineno, "what": sub.id})
        elif isinstance(sub, ast.Attribute) and sub.attr == "modules" and isinstance(sub.value, ast.Name) and sub.value.id == "sys":
            out.append({"line": sub.lineno, "what": "sys.modules"})
        elif isinstance(sub, (ast.Name, ast.Attribute)) and getattr(sub, "id", getattr(sub, "attr", "")) == "__builtins__" and not isinstance(sub.ctx, ast.Load):
            out.append({"line": sub.lineno, "what": "__builtins__"})
    return sorted(out, key=lambda r: (r["line"], r["what"]))


def declaration(request):
    raw = base64.b64decode(request["source"])
    tree = ast.parse(decode(raw))
    offset, line_end = lines_of(raw)

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

    # The gate copies whole lines, so a statement that shares a line with
    # another can't be copied alone.
    lines = {}
    for statement in tree.body:
        for line in range(statement.lineno, statement.end_lineno + 1):
            lines[line] = lines.get(line, 0) + 1

    def shared(statement):
        return any(lines[line] > 1 for line in range(statement.lineno, statement.end_lineno + 1))

    bindings, star = {}, []

    def bind(name, row):
        bindings.setdefault(name, []).append(row)

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
            annotation = []
            if isinstance(statement, ast.AnnAssign):
                annotation = sorted({sub.id for sub in ast.walk(statement.annotation) if isinstance(sub, ast.Name)})
                if any(isinstance(sub, ast.Attribute) for sub in ast.walk(statement.annotation)):
                    annotation.append(".")
            simple = len(targets) == 1 and isinstance(targets[0], ast.Name)
            for target in targets:
                for sub in ast.walk(target):
                    if isinstance(sub, ast.Name):
                        bind(sub.id, dict(where, kind="assign" if simple else "other", literal=literal, annotation=annotation))
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

    free, unsupported, bound, tries = function(node)
    if body and body[0]["line"] == node.lineno:
        unsupported.append("a body on the def line")
    if node.col_offset != 0:
        unsupported.append("a declaration that isn't at the module's top level")
    return {
        "globals": rebound, "future": future, "dynamic": dynamic(tree), "bound": bound, "tries": tries,
        "start": offset(first, 0), "end": line_end(node.end_lineno), "line": first, "end_line": node.end_lineno,
        "def_line": node.lineno, "async": isinstance(node, ast.AsyncFunctionDef), "decorators": decorators,
        "header_end": body[0]["start"] if body else line_end(node.end_lineno),
        "name_start": name_start, "name_end": name_start + len(node.name.encode()),
        "body": body, "free": free, "unsupported": unsupported, "bindings": bindings, "star": star,
        "declarations": sorted(n.name for n in tree.body if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef))),
    }


def writes(request):
    """Every place the application's sources write one of a module's
    attributes: by assignment, augmented assignment or del through any name
    bound to it, or through setattr, delattr or vars."""
    target = request["module"]
    out = []
    for f in request["files"]:
        path = f["path"]
        try:
            tree = ast.parse(decode(base64.b64decode(f["source"])))
        except (SyntaxError, UnicodeError, ValueError, RecursionError):
            continue
        package = path.rsplit("/", 1)[0].replace("/", ".") if "/" in path else ""
        modules = {}
        for sub in ast.walk(tree):
            if isinstance(sub, ast.Import):
                for alias in sub.names:
                    if alias.asname:
                        modules[alias.asname] = alias.name
                    else:
                        modules[alias.name.split(".")[0]] = alias.name.split(".")[0]
            elif isinstance(sub, ast.ImportFrom):
                base = sub.module or ""
                if sub.level:
                    parts = package.split(".") if package else []
                    parts = parts[:len(parts) - (sub.level - 1)] if sub.level - 1 <= len(parts) else None
                    if parts is None:
                        continue
                    base = ".".join(p for p in parts + ([sub.module] if sub.module else []) if p)
                for alias in sub.names:
                    if alias.name != "*":
                        modules[alias.asname or alias.name] = (base + "." if base else "") + alias.name

        def dotted(expr):
            names = []
            while isinstance(expr, ast.Attribute):
                names.append(expr.attr)
                expr = expr.value
            if not isinstance(expr, ast.Name) or expr.id not in modules:
                return None
            return ".".join([modules[expr.id]] + list(reversed(names)))

        def written(expr, line):
            if isinstance(expr, ast.Attribute) and dotted(expr.value) == target:
                out.append({"path": path, "line": line, "what": target + "." + expr.attr})
            elif isinstance(expr, ast.Subscript) and isinstance(expr.value, ast.Attribute) and expr.value.attr == "__dict__" and dotted(expr.value.value) == target:
                out.append({"path": path, "line": line, "what": target + ".__dict__"})

        for sub in ast.walk(tree):
            if isinstance(sub, (ast.Assign, ast.AugAssign, ast.AnnAssign, ast.Delete)):
                targets = sub.targets if isinstance(sub, (ast.Assign, ast.Delete)) else [sub.target]
                for t in targets:
                    for part in ast.walk(t):
                        written(part, sub.lineno)
            elif isinstance(sub, ast.Call) and isinstance(sub.func, ast.Name) and sub.func.id in ("setattr", "delattr", "vars") and sub.args and dotted(sub.args[0]) == target:
                out.append({"path": path, "line": sub.lineno, "what": sub.func.id + "(" + target + ")"})
    return {"writes": sorted(out, key=lambda r: (r["path"], r["line"], r["what"]))}


def main():
    request = json.loads(sys.stdin.buffer.read())
    reply = writes(request) if "writes" in request else declaration(request)
    print(json.dumps(reply, sort_keys=True))


try:
    main()
except (SyntaxError, UnicodeError, LookupError, ValueError, KeyError, RecursionError) as error:
    sys.exit(str(error))
