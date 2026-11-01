"""Read Python for in-place proof without importing or running it.

The request on stdin is JSON. With a "symbol", it names a source, as base64,
and one of its top-level functions: the reply gives the function's byte
spans in the original source, the names it reads from its module and how it
reads them, its exception handlers and what their bodies call, and how the
module binds each name at its top level, so the gate can build Nagini's
input around the original bytes. It serves a selected function and a trusted
contract's declaration alike. With "writes", it names a module and the
application's sources: the reply gives every place a source writes one of
that module's attributes, or reaches the module in a way the scan can't
follow.
"""

import ast
import base64
import io
import json
import re
import sys
import tokenize

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


# What a source may read of a module the scan for writes follows, besides
# the module's own functions and values.
PLAIN_DUNDERS = frozenset(("__name__", "__file__", "__doc__", "__path__", "__all__", "__version__"))

# Calls that reach a module, or what's in one, by a dotted name.
BY_NAME = frozenset(("importlib.import_module", "importlib.__import__", "builtins.__import__", "runpy.run_module",
                     "pkgutil.resolve_name", "pkgutil.get_loader", "pkgutil.find_loader", "pydoc.locate"))

# mock's patchers that take their target by a dotted name.
PATCHERS = frozenset(("unittest.mock.patch", "unittest.mock.patch.dict", "unittest.mock.patch.multiple",
                      "mock.patch", "mock.patch.dict", "mock.patch.multiple"))


def module_of(path):
    """The module a source is, by its path from the application's root."""
    name = path[:-3] if path.endswith(".py") else path
    if name == "__init__" or name.endswith("/__init__"):
        name = name[:-len("__init__")].rstrip("/")
    return name.replace("/", ".")


def imports_of(tree, package):
    """What each name a source's imports bind, at any scope, may name, as
    dotted paths; the modules it star-imports; and every module its imports
    may load."""
    names, stars, loads = {}, [], []
    for sub in ast.walk(tree):
        if isinstance(sub, ast.Import):
            for alias in sub.names:
                bound = alias.asname or alias.name.split(".")[0]
                names.setdefault(bound, set()).add(alias.name if alias.asname else bound)
                loads.append(alias.name)
        elif isinstance(sub, ast.ImportFrom):
            base = sub.module or ""
            if sub.level:
                parts = package.split(".") if package else []
                if sub.level - 1 > len(parts):
                    continue
                parts = parts[:len(parts) - (sub.level - 1)]
                base = ".".join(p for p in parts + ([sub.module] if sub.module else []) if p)
            loads.append(base)
            for alias in sub.names:
                if alias.name == "*":
                    stars.append(base)
                else:
                    names.setdefault(alias.asname or alias.name, set()).add((base + "." if base else "") + alias.name)
                    loads.append((base + "." if base else "") + alias.name)
    return names, stars, loads


def writes(request):
    """Every place the application's production sources could change what
    the module's names hold, or reach the module in a way a parse can't
    follow. Test sources, which the request marks, are scanned only where
    production code imports them; the proof covers the module as production
    code leaves it. A reference to the module, to a package above it, or to
    an application module whose imports bind one of those, through an
    import, a relative import, a module that imports it or a star import,
    may only read attributes, and writing one is refused where it's the
    module's own or rebinds a module on the way to it. So are reaching one
    of them by name, through sys.modules, importlib, __import__, runpy,
    pkgutil, pydoc or mock's patchers, reading __globals__ or f_globals,
    reading the namespace of a source that holds one, and a source the
    host's Python can't read."""
    target = request["module"]
    out, sources, unread = [], [], {}

    def report(path, line, what):
        out.append({"path": path, "line": line, "what": what})

    for f in request["files"]:
        path = f["path"]
        try:
            raw = base64.b64decode(f["source"])
            encoding, _ = tokenize.detect_encoding(io.BytesIO(raw).readline)
            tree = ast.parse(raw.decode(encoding))
        except (SyntaxError, UnicodeError, ValueError, LookupError, RecursionError) as error:
            unread[module_of(path)] = (path, getattr(error, "lineno", None) or 0, error.msg if isinstance(error, SyntaxError) else str(error), f.get("tests", False))
            continue
        package = path.rsplit("/", 1)[0].replace("/", ".") if "/" in path else ""
        names, stars, loads = imports_of(tree, package)
        sources.append((path, tree, names, stars, loads, f.get("tests", False)))
    exported = {module_of(s[0]): s[2] for s in sources}
    starred = {module_of(s[0]): s[3] for s in sources}

    # Production sources, and what they import, which runs in production
    # too, whether or not it's a test helper, with each package on its way.
    by_module = {module_of(s[0]): s for s in sources}
    scanned = [s for s in sources if not s[5]]
    included = {s[0] for s in scanned}
    for s in scanned:
        for loaded in s[4]:
            parts = loaded.split(".")
            for i in range(1, len(parts) + 1):
                prefix = ".".join(parts[:i])
                other = by_module.get(prefix)
                if other is not None and other[0] not in included:
                    included.add(other[0])
                    scanned.append(other)
                if prefix in unread:
                    unread[prefix] = unread[prefix][:3] + (False,)
    for path, line, why, tests in unread.values():
        if not tests:
            report(path, line, "can't be read by the host's Python (%s), so the scan for writes can't see it" % why)

    def resolve(dotted, seen=()):
        """The dotted paths a path may name, the path itself among them,
        following what the staged modules' imports bind."""
        found = {dotted}
        if dotted in seen or len(seen) > 32:
            return found
        parts = dotted.split(".")
        for i in range(len(parts) - 1, 0, -1):
            owner = ".".join(parts[:i])
            if owner in exported:
                bound = exported[owner].get(parts[i]) or {(s + "." if s else "") + parts[i] for s in starred[owner]}
                for b in bound:
                    found |= resolve(".".join([b] + parts[i + 1:]), seen + (dotted,))
                break
        return found

    # The module, the packages on its path, and every application module
    # whose imports bind one of them, so it's reached by attribute too.
    segments = target.split(".")
    above = {".".join(segments[:i]) for i in range(1, len(segments))}
    reach = above | {target}
    changed = True
    while changed:
        changed = False
        for module in exported:
            bound = [b for bs in exported[module].values() for b in bs] + starred[module]
            if module not in reach and any(reach & resolve(b) for b in bound):
                reach.add(module)
                changed = True

    def reaches(name):
        """Whether a dotted name, as a by-name call takes it, names one of
        those modules or something in the module."""
        return any(r in reach or r.startswith(target + ".") for r in resolve(name.replace(":", ".")))

    for path, tree, names, stars, _, _ in scanned:
        parents = {child: node for node in ast.walk(tree) for child in ast.iter_child_nodes(node)}
        holds = any(reach & resolve(b) for bs in names.values() for b in bs) or any(reach & resolve(s) for s in stars)

        def candidates(expr):
            attrs = []
            while isinstance(expr, ast.Attribute):
                attrs.append(expr.attr)
                expr = expr.value
            if not isinstance(expr, ast.Name):
                return set()
            heads = set(names.get(expr.id, ())) | {(s + "." if s else "") + expr.id for s in stars}
            if expr.id not in names:
                heads.add("builtins." + expr.id)
            found = set()
            for h in heads:
                found |= resolve(".".join([h] + attrs[::-1]))
            return found

        def shown(refs):
            return target if target in refs else max(sorted(refs), key=len)

        def use(node, refs, line):
            parent = parents.get(node)
            if isinstance(parent, ast.Attribute) and parent.value is node:
                if isinstance(parent.ctx, ast.Load):
                    if reach & candidates(parent):
                        return
                    if parent.attr.startswith("__") and parent.attr.endswith("__") and parent.attr not in PLAIN_DUNDERS:
                        report(path, line, "reads %s.%s, which reaches the module's names" % (shown(refs), parent.attr))
                    return
                if target in refs:
                    report(path, line, "writes %s.%s, which no binding in the module shows" % (target, parent.attr))
                    return
                for r in sorted(refs & above):
                    if (above | {target}) & resolve(r + "." + parent.attr):
                        report(path, line, "rebinds %s's attribute %s, through which code reaches %s" % (r, parent.attr, target))
                        return
                return
            call = parent if isinstance(parent, ast.Call) and node is not parent.func else None
            if isinstance(parent, (ast.keyword, ast.Starred)) and isinstance(parents.get(parent), ast.Call):
                call = parents[parent]
            if call is not None and target not in refs and node in call.args[:1] and len(call.args) > 1:
                # getattr and its kin may take a package or module on the
                # way to the module, by a constant naming something else in it.
                name = call.args[1]
                if (candidates(call.func) & {"builtins.getattr", "builtins.hasattr", "builtins.setattr", "builtins.delattr"}
                        and isinstance(name, ast.Constant) and isinstance(name.value, str)
                        and not any(reach & resolve(r + "." + name.value) for r in refs)):
                    return
            if call is not None:
                what = "passes %s to a call"
            elif isinstance(parent, (ast.Assign, ast.AnnAssign, ast.AugAssign, ast.NamedExpr)):
                what = "binds %s to a name other than by an import"
            else:
                what = "uses %s other than to read its attributes"
            report(path, line, (what % shown(refs)) + ", and the scan for writes can't follow it")

        for node in ast.walk(tree):
            line = getattr(node, "lineno", 0)
            if isinstance(node, (ast.Name, ast.Attribute)) and isinstance(node.ctx, ast.Load):
                found = candidates(node)
                if reach & found:
                    use(node, reach & found, line)
                if "sys.modules" in found:
                    parent, key = parents.get(node), None
                    if isinstance(parent, ast.Subscript) and parent.value is node:
                        key = parent.slice
                    elif (isinstance(parent, ast.Attribute) and parent.attr == "get" and isinstance(parents.get(parent), ast.Call)
                          and parents[parent].func is parent and parents[parent].args):
                        key = parents[parent].args[0]
                    elif isinstance(parent, ast.Compare) and node in parent.comparators and all(isinstance(op, (ast.In, ast.NotIn)) for op in parent.ops):
                        continue
                    else:
                        report(path, line, "uses sys.modules other than to look a module up by its name, and the scan for writes can't follow it")
                        continue
                    if not (isinstance(key, ast.Constant) and isinstance(key.value, str)):
                        report(path, line, "looks a module up in sys.modules by a name it computes, and the scan for writes can't follow it")
                    elif reaches(key.value):
                        report(path, line, "looks %s up in sys.modules, and the scan for writes can't follow it" % key.value)
            if isinstance(node, ast.Attribute) and node.attr in ("__globals__", "f_globals"):
                report(path, line, "reads %s, which holds a module's names" % node.attr)
            if not isinstance(node, ast.Call):
                continue
            # A string names a module only where it's handed to something
            # that reaches modules by name; a logger's name is just a name.
            funcs, arg = candidates(node.func), node.args[0] if node.args else None
            constant = isinstance(arg, ast.Constant) and isinstance(arg.value, str)
            if holds and (funcs & {"builtins.globals", "builtins.locals"} or "builtins.vars" in funcs and not node.args and not node.keywords):
                report(path, line, "reads its own namespace, which holds a module on the way to %s" % target)
            if funcs & BY_NAME:
                if not constant or arg.value.startswith("."):
                    report(path, line, "imports a module by a name it computes, and the scan for writes can't follow it")
                elif reaches(arg.value) or funcs & {"builtins.__import__", "importlib.__import__"} and arg.value.split(".")[0] in reach:
                    report(path, line, "imports %s by name, and the scan for writes can't follow it" % arg.value)
            if funcs & PATCHERS:
                # patch.dict also takes the dict itself, which the rules
                # above follow.
                if constant and (arg.value == "sys.modules" or reaches(arg.value)):
                    report(path, line, "patches %s by name, and the scan for writes can't follow it" % arg.value)
                elif not constant and not funcs & {"unittest.mock.patch.dict", "mock.patch.dict"}:
                    report(path, line, "patches a target it computes, and the scan for writes can't follow it")
    return {"writes": sorted(out, key=lambda r: (r["path"], r["line"], r["what"]))}


def main():
    request = json.loads(sys.stdin.buffer.read())
    reply = writes(request) if "writes" in request else declaration(request)
    print(json.dumps(reply, sort_keys=True))


try:
    main()
except (SyntaxError, UnicodeError, LookupError, ValueError, KeyError, RecursionError) as error:
    sys.exit(str(error))
