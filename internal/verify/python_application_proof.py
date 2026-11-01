"""Read Python for in-place proof without importing or running it.

The request on stdin is JSON. With a "symbol", it names a source, as base64,
and one of its top-level functions: the reply gives the function's byte
spans in the original source, the names it reads from its module and how it
reads them, its exception handlers and what their bodies call, and how the
module binds each name at its top level, so the gate can build Nagini's
input around the original bytes. It serves a selected function and a trusted
contract's declaration alike. With "standin" too, Invariant's runtime
stand-in for nagini_contracts.contracts, it gives where the function's
contract couldn't run as Nagini reads it. With "writes", it names a module
and the application's sources: the reply gives every place a source writes
one of that module's attributes, or what the stand-in or the builtins bind,
or reaches one of them in a way the scan can't follow.
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


# The statements Nagini reads as a function's contract, or as facts it checks
# where they stand. Python runs each one as a call where it stands, and
# evaluates its arguments then, whatever Nagini reads lazily: an Implies'
# consequent only where its antecedent holds, and a postcondition at the
# function's exit, which Python evaluates on entry (#359).
CONTRACT_STATEMENTS = frozenset(("Requires", "Ensures", "Exsures", "Decreases", "Assert", "Invariant"))

# The builtins a contract may call, by how many arguments each takes: the
# ones the pinned Nagini models whose call can't raise on an argument of the
# type each takes. Its translator reads len through the argument's __len__
# and isinstance as a type check, and its builtins define abs on ints. min
# and max are left out: their one-argument form fails on an empty list where
# Nagini only requires one that isn't.
CONTRACT_BUILTINS = {"len": 1, "abs": 1, "isinstance": 2}

# What a contract's argument may hold besides names, literals, tuples of
# them, the stand-in's calls and those builtins' calls. Nagini reads none of
# these lazily, and none fails on operands of the builtin types the gate can
# tell they have, Result()'s inert value included. // and % take only a
# positive integer literal, a plain int constant and not a bool, which the
# pinned Nagini divides by as Python does; it divides by a negative one as
# Viper's Euclidean division, which isn't Python's (#359).
CONTRACT_OPERATORS = (ast.Add, ast.Sub, ast.Mult)
CONTRACT_DIVISIONS = (ast.FloorDiv, ast.Mod)
CONTRACT_FORMS = (ast.BoolOp, ast.UnaryOp, ast.Compare, ast.IfExp)
ORDERINGS = (ast.Lt, ast.LtE, ast.Gt, ast.GtE)

# The builtin types the gate can tell a value has: by an annotation, a
# literal, a display or the builtin that makes it, and for a local by every
# binding of it. "inert" is what Result() stands for where Python evaluates
# a contract, which answers every operator. "optional" is an Optional or a
# Union, whose names a contract may read only in is, is not and isinstance
# tests, since Python would apply anything else to None or another type.
INTEGERS = frozenset(("bool", "int"))
SEQUENCES = frozenset(("str", "bytes", "list", "tuple"))
SIZED = SEQUENCES | {"dict", "set", "range"}
HASHABLE = INTEGERS | {"str", "bytes", "none"}
TYPE_NAMES = frozenset(("int", "bool", "str", "bytes", "list", "tuple", "dict", "set", "range", "float", "complex"))
GENERICS = {"List": "list", "Dict": "dict", "Set": "set", "Tuple": "tuple", "list": "list", "dict": "dict", "set": "set", "tuple": "tuple"}
UNIONS = frozenset(("Optional", "Union"))
CONSTRUCTORS = {"len": "int", "int": "int", "bool": "bool", "str": "str", "bytes": "bytes", "list": "list", "tuple": "tuple",
                "dict": "dict", "set": "set", "range": "range", "sorted": "list", "isinstance": "bool", "float": "float"}

# The types isinstance may test in a contract: builtins it can't fail on.
ISINSTANCE_TYPES = frozenset(("int", "bool", "str", "bytes", "list", "tuple", "dict", "set", "object"))

# The largest count a sequence may be repeated by, which CPython can index.
REPEATS = 2 ** 63

# What the rest is called where a contract is refused for holding it.
CONTRACT_REFUSED = {
    ast.Subscript: "a subscript", ast.Attribute: "an attribute read", ast.Div: "a division",
    ast.FloorDiv: "a floor division by other than a positive integer literal",
    ast.Mod: "a modulo by other than a positive integer literal", ast.Pow: "a power", ast.LShift: "a shift",
    ast.RShift: "a shift", ast.BitAnd: "a bitwise operator", ast.BitOr: "a bitwise operator",
    ast.BitXor: "a bitwise operator", ast.MatMult: "a matrix product", ast.List: "a list", ast.Set: "a set",
    ast.Dict: "a dict", ast.JoinedStr: "an f-string", ast.Starred: "an unpacked argument",
    ast.NamedExpr: "an assignment expression", ast.Lambda: "a lambda",
    ast.ListComp: "a comprehension", ast.SetComp: "a comprehension", ast.DictComp: "a comprehension",
    ast.GeneratorExp: "a comprehension",
}


def standin_of(raw):
    """What Invariant's runtime stand-in for nagini_contracts exports, by its
    __all__, and how each one's parameters bind a call: the positional ones,
    whether it takes more, and whether it takes keywords beyond them."""
    tree = ast.parse(raw.decode("utf-8"))
    names, defs = (), {}
    for s in tree.body:
        if isinstance(s, ast.Assign) and any(isinstance(t, ast.Name) and t.id == "__all__" for t in s.targets):
            names = ast.literal_eval(s.value)
        elif isinstance(s, ast.FunctionDef):
            a = s.args
            defs[s.name] = ([p.arg for p in a.posonlyargs + a.args], a.vararg is not None, a.kwarg is not None)
    return {name: defs[name] for name in names}


def binds(call, signature):
    """Whether a call binds the stand-in's parameters as Python does."""
    params, more, keywords = signature
    if any(isinstance(a, ast.Starred) for a in call.args) or any(k.arg is None for k in call.keywords):
        return False
    if len(call.args) > len(params) and not more:
        return False
    filled = set(params[:len(call.args)])
    for k in call.keywords:
        if k.arg in params and k.arg not in filled:
            filled.add(k.arg)
        elif not keywords:
            return False
    return filled >= set(params)


def value_kind(value):
    """The builtin type a literal value has."""
    if isinstance(value, bool):
        return "bool"
    for t, k in ((int, "int"), (float, "float"), (complex, "complex"), (str, "str"), (bytes, "bytes"), (tuple, "tuple")):
        if type(value) is t:
            return k
    return "none" if value is None else None


class Kinds:
    """What builtin type the gate can tell each expression of a function has
    (#359): one of TYPE_NAMES' kinds, "inert", "optional", "none", or None
    where it can't tell. Parameters take their annotations', the module's
    literal constants their values', and a local the one every binding of it
    gives, or "optional" where the others are None."""

    def __init__(self, node, tree, bindings, names):
        args = node.args
        self.params = {a.arg: a for a in args.posonlyargs + args.args + args.kwonlyargs}
        bound = Bound()
        for statement in node.body:
            bound.visit(statement)
        self.bound, self.bindings, self.names = bound.names, bindings, names
        self.typing = {n: rows[0].get("name") for n, rows in bindings.items()
                       if len(rows) == 1 and rows[0]["kind"] == "from" and rows[0]["module"] == "typing" and rows[0]["level"] == 0}
        self.returns = {s.name: s.returns for s in tree.body if isinstance(s, (ast.FunctionDef, ast.AsyncFunctionDef)) and s.returns is not None}
        self.constants = {}
        for s in tree.body:
            targets = s.targets if isinstance(s, ast.Assign) else [s.target] if isinstance(s, ast.AnnAssign) else []
            if len(targets) == 1 and isinstance(targets[0], ast.Name) and s.value is not None:
                try:
                    self.constants[targets[0].id] = value_kind(ast.literal_eval(s.value))
                except (ValueError, TypeError, SyntaxError, MemoryError, RecursionError):
                    pass
        self.env, self.elements = {}, {}
        for name, a in self.params.items():
            self.env[name], self.elements[name] = self.annotation(a.annotation)
        for name, k in self.constants.items():
            if name not in self.bound and name not in self.params and len(bindings.get(name, ())) == 1:
                self.env[name] = k
        # Every binding of each local, and its annotation where it has one.
        parents = {child: n for n in ast.walk(node) for child in ast.iter_child_nodes(n)}
        binders, declared = {}, {}
        for sub in ast.walk(node):
            if not (isinstance(sub, ast.Name) and isinstance(sub.ctx, ast.Store)) or sub.id in self.params:
                continue
            parent = parents.get(sub)
            if isinstance(parent, ast.AnnAssign) and parent.target is sub:
                declared[sub.id] = self.annotation(parent.annotation)
                continue
            if isinstance(parent, ast.Assign) and sub in parent.targets:
                b = ("value", parent.value)
            elif isinstance(parent, ast.AugAssign):
                b = ("value", ast.BinOp(left=ast.Name(id=sub.id, ctx=ast.Load()), op=parent.op, right=parent.value))
            elif isinstance(parent, (ast.For, ast.AsyncFor)) and parent.target is sub:
                b = ("element", parent.iter)
            else:
                b = None
            binders.setdefault(sub.id, []).append(b)
        for name, (k, element) in declared.items():
            self.env[name], self.elements[name] = k, element
        pending = {n: bs for n, bs in binders.items() if n not in declared}
        for n in pending:
            self.env[n] = "?"
        for _ in range(len(pending) + 2):
            changed = False
            for n, bs in sorted(pending.items()):
                new = self.join({None if b is None else self.kind(b[1]) if b[0] == "value" else self.element(b[1]) for b in bs})
                if new != self.env[n]:
                    self.env[n], changed = new, True
            if not changed:
                break
        for n in pending:
            if self.env[n] == "?":
                self.env[n] = None

    def builtin(self, name):
        """Whether a name the function reads is the builtin of that name."""
        return name not in self.bound and name not in self.params and name not in self.bindings

    @staticmethod
    def join(kinds):
        kinds = kinds - {"?"}
        if not kinds:
            return "?"
        if None in kinds:
            return None
        if len(kinds) == 1:
            return next(iter(kinds))
        return "optional" if len(kinds - {"none", "optional"}) <= 1 else None

    def annotation(self, a):
        """The kind an annotation names, and its elements' kind."""
        if isinstance(a, ast.Constant) and isinstance(a.value, str):
            try:
                a = ast.parse(a.value, mode="eval").body
            except SyntaxError:
                return None, None
        if a is None:
            return None, None
        if isinstance(a, ast.Constant) and a.value is None:
            return "none", None
        if isinstance(a, ast.BinOp) and isinstance(a.op, ast.BitOr):
            return "optional", None
        base, args = (a.value, a.slice.elts if isinstance(a.slice, ast.Tuple) else [a.slice]) if isinstance(a, ast.Subscript) else (a, [])
        if not isinstance(base, ast.Name):
            return None, None
        t = base.id if base.id in TYPE_NAMES and self.builtin(base.id) else self.typing.get(base.id)
        if t in UNIONS:
            return "optional", None
        if t in GENERICS:
            k = GENERICS[t]
            if not args:
                return k, None
            if k == "dict":
                return k, self.annotation(args[-1])[0] if len(args) == 2 else None
            if k == "tuple":
                ellipsis = len(args) == 2 and isinstance(args[1], ast.Constant) and args[1].value is Ellipsis
                return k, self.annotation(args[0])[0] if ellipsis else None
            return k, self.annotation(args[0])[0]
        if t in TYPE_NAMES and not args:
            return t, {"str": "str", "bytes": "int", "range": "int"}.get(t)
        return None, None

    def element(self, e):
        """The kind of what iterating over e gives."""
        if isinstance(e, ast.Call) and isinstance(e.func, ast.Name) and e.func.id == "range" and self.builtin("range"):
            return "int"
        if isinstance(e, ast.Name) and self.elements.get(e.id):
            return self.elements[e.id]
        if isinstance(e, (ast.List, ast.Tuple, ast.Set)) and e.elts:
            return self.join({self.kind(x) for x in e.elts})
        return {"str": "str", "bytes": "int", "range": "int"}.get(self.kind(e))

    def kind(self, e):
        """The builtin type the gate can tell e has, or None."""
        if isinstance(e, ast.Constant):
            return value_kind(e.value)
        if isinstance(e, ast.Name):
            return self.env.get(e.id)
        if isinstance(e, (ast.Tuple, ast.List, ast.Set, ast.Dict)):
            return {ast.Tuple: "tuple", ast.List: "list", ast.Set: "set", ast.Dict: "dict"}[type(e)]
        if isinstance(e, (ast.ListComp, ast.SetComp, ast.DictComp)):
            return {ast.ListComp: "list", ast.SetComp: "set", ast.DictComp: "dict"}[type(e)]
        if isinstance(e, ast.JoinedStr):
            return "str"
        if isinstance(e, ast.Compare):
            return "bool"
        if isinstance(e, ast.UnaryOp):
            if isinstance(e.op, ast.Not):
                return "bool"
            k = self.kind(e.operand)
            return "int" if k in INTEGERS else k if k in ("?", "inert") else None
        if isinstance(e, ast.BoolOp):
            return self.join({self.kind(v) for v in e.values})
        if isinstance(e, ast.IfExp):
            return self.join({self.kind(e.body), self.kind(e.orelse)})
        if isinstance(e, ast.BinOp):
            left, right = self.kind(e.left), self.kind(e.right)
            if "inert" in (left, right):
                return "inert"
            if left == "?" or right == "?":
                left = right if left == "?" else left
                right = left if right == "?" else right
                if left == "?":
                    return "?"
            if left in INTEGERS and right in INTEGERS and isinstance(e.op, (ast.Add, ast.Sub, ast.Mult, ast.FloorDiv, ast.Mod, ast.Pow, ast.LShift, ast.RShift)):
                return "int"
            if isinstance(e.op, ast.Add) and left == right and left in SEQUENCES:
                return left
            if isinstance(e.op, ast.Mult) and {left, right} & SEQUENCES and {left, right} & INTEGERS:
                return left if left in SEQUENCES else right
            if isinstance(e.op, ast.Sub) and left == right == "set":
                return "set"
            return None
        if isinstance(e, ast.Subscript):
            base = self.kind(e.value)
            if isinstance(e.slice, ast.Slice):
                return base if base in SEQUENCES else None
            if base in ("str", "bytes"):
                return "str" if base == "str" else "int"
            if isinstance(e.value, ast.Name):
                return self.elements.get(e.value.id)
            return None
        if isinstance(e, ast.Call) and isinstance(e.func, ast.Name):
            f = e.func.id
            if f in self.names:
                if f == "Result":
                    return "inert"
                return self.kind(e.args[0]) if f == "Old" and len(e.args) == 1 else None
            if f in CONSTRUCTORS and self.builtin(f):
                return CONSTRUCTORS[f]
            if f in ("abs", "min", "max") and self.builtin(f) and e.args:
                k = self.join({self.kind(a) for a in e.args})
                return "int" if k in INTEGERS else k if k == "?" else None
            rows = self.bindings.get(f, ())
            if f in self.returns and len(rows) == 1 and rows[0]["kind"] == "function" and f not in self.bound and f not in self.params:
                return self.annotation(self.returns[f])[0]
        return None


def contracts(node, names, kinds):
    """Each place the function uses one of Nagini's names other than in a
    contract statement, where Invariant's runtime stand-in gives back nothing
    Nagini's does, and each contract argument Python could fail evaluating
    where Nagini reads it lazily. A contract may read the function's
    parameters, the module's names, and its locals only where every path to
    it binds them first: Nagini checks a local is bound only where it reads
    it, and an Implies' consequent only where its antecedent holds. It may
    order, do arithmetic on, and take the len or abs of only what the gate
    can tell has one builtin type that operation can't fail on, and read an
    Optional or a Union only in is, is not and isinstance tests."""
    params = set(kinds.params)
    deleted = {n.id for n in ast.walk(node) if isinstance(n, ast.Name) and isinstance(n.ctx, ast.Del)}
    local = (kinds.bound - params) | (params & deleted)
    out = []

    def problem(sub, what):
        out.append({"line": sub.lineno, "what": what})

    def refuse(e, what, statement):
        problem(e, "%s holds %s, which Python evaluates when the statement runs, even where Nagini reads it lazily, so a contract may hold only names, literals, Result(), Old(), Nagini's other names Invariant's runtime stand-in provides, comparisons, and, or, not, conditional expressions, +, -, *, // and %% by a positive integer literal, unary operators and calls of len, abs and isinstance" % (statement, what))

    def optional(x):
        return isinstance(x, ast.Name) and kinds.kind(x) == "optional"

    def involves(x):
        """Whether x reads an Optional or a Union, which the name rule refuses."""
        return any(optional(n) for n in ast.walk(x))

    def sure_after(statement):
        """What a simple statement binds on every path past it."""
        if isinstance(statement, ast.Assign):
            targets = statement.targets
        elif isinstance(statement, (ast.AnnAssign, ast.AugAssign)) and statement.value is not None:
            targets = [statement.target]
        else:
            return set()
        return {n.id for t in targets for n in ast.walk(t) if isinstance(n, ast.Name) and isinstance(n.ctx, ast.Store)} - deleted

    def block(statements, sure):
        sure = set(sure)
        for statement in statements:
            visit(statement, sure)
            sure |= sure_after(statement)

    def visit(n, sure):
        if (isinstance(n, ast.Expr) and isinstance(n.value, ast.Call) and isinstance(n.value.func, ast.Name)
                and n.value.func.id in CONTRACT_STATEMENTS and n.value.func.id in names):
            call(n.value, n.value.func.id, sure)
            return
        if isinstance(n, ast.Name) and n.id in names:
            problem(n, "reads Nagini's %s outside a contract statement, where Invariant's runtime stand-in for it gives back nothing Nagini's does" % n.id)
        for field, value in ast.iter_fields(n):
            if isinstance(value, list) and value and isinstance(value[0], ast.stmt):
                loop = set()
                if isinstance(n, (ast.For, ast.AsyncFor)) and field == "body":
                    loop = {t.id for t in ast.walk(n.target) if isinstance(t, ast.Name)} - deleted
                block(value, sure | loop)
            elif isinstance(value, list):
                for v in value:
                    if isinstance(v, ast.AST):
                        visit(v, sure)
            elif isinstance(value, ast.AST):
                visit(value, sure)

    def call(c, statement, sure):
        if not binds(c, names[c.func.id]):
            problem(c, "%s calls %s with arguments Invariant's runtime stand-in doesn't take" % (statement, c.func.id))
        for a in c.args + [k.value for k in c.keywords]:
            argument(a, statement, sure)

    def results(e):
        return any(isinstance(s, ast.Call) and isinstance(s.func, ast.Name) and s.func.id == "Result" and "Result" in names for s in ast.walk(e))

    def literal(x):
        return isinstance(x, ast.Constant) and type(x.value) is int and abs(x.value) < REPEATS

    def argument(e, statement, sure, may_be_optional=False):
        if isinstance(e, ast.Constant):
            return
        if isinstance(e, ast.Name):
            if e.id in local and e.id not in sure:
                problem(e, "%s reads %s, which the function may not have bound when Python evaluates the contract" % (statement, e.id))
            if optional(e) and not may_be_optional:
                problem(e, "%s reads %s, an Optional or a Union, other than in an is, is not or isinstance test, and Python would apply the rest to None or another type when it evaluates the contract" % (statement, e.id))
            return
        if isinstance(e, ast.Tuple) and not any(isinstance(x, ast.Starred) for x in e.elts):
            for x in e.elts:
                argument(x, statement, sure)
            return
        if isinstance(e, ast.BinOp) and isinstance(e.op, CONTRACT_DIVISIONS):
            divisor = e.right
            if not (isinstance(divisor, ast.Constant) and type(divisor.value) is int and divisor.value > 0):
                refuse(e, CONTRACT_REFUSED[type(e.op)], statement)
            elif not involves(e.left) and kinds.kind(e.left) not in INTEGERS | {"inert"}:
                if isinstance(e.op, ast.Mod):
                    problem(e, "%s holds a modulo of what the gate can't tell is an int, and %% formats a string, failing where Nagini's model of it doesn't, so its left side must be an int the gate can see: a parameter or local of an int's type or bound only to ints, a literal constant, len(), abs(), Old() or Result(), or arithmetic on them" % statement)
                else:
                    problem(e, "%s holds a floor division of what the gate can't tell is an int, and Python can fail dividing anything else, None among it, when it evaluates the contract" % statement)
            argument(e.left, statement, sure)
            return
        if isinstance(e, ast.BinOp) and isinstance(e.op, CONTRACT_OPERATORS):
            left, right = kinds.kind(e.left), kinds.kind(e.right)
            if "inert" not in (left, right) and not involves(e):
                result = kinds.kind(e)
                if result in (None, "?"):
                    what = {ast.Add: "an addition", ast.Sub: "a subtraction", ast.Mult: "a multiplication"}[type(e.op)]
                    problem(e, "%s holds %s of what the gate can't tell are builtin values Python can apply it to, and it can fail on others, None among them, when Python evaluates the contract" % (statement, what))
                elif isinstance(e.op, ast.Mult) and result in SEQUENCES and not (literal(e.left) or literal(e.right)):
                    problem(e, "%s repeats a sequence by other than an int literal, which Python fails on past what it can index, where Nagini's model doesn't" % statement)
            argument(e.left, statement, sure)
            argument(e.right, statement, sure)
            return
        if isinstance(e, ast.UnaryOp):
            if isinstance(e.op, ast.Invert):
                refuse(e, "a bitwise operator", statement)
                return
            if isinstance(e.op, (ast.USub, ast.UAdd)) and not involves(e.operand) and kinds.kind(e.operand) not in INTEGERS | {"inert"}:
                problem(e, "%s negates what the gate can't tell is an int, and Python can fail negating anything else, None among it, when it evaluates the contract" % statement)
            argument(e.operand, statement, sure)
            return
        if isinstance(e, (ast.BoolOp, ast.IfExp)):
            for child in ast.iter_child_nodes(e):
                if isinstance(child, ast.expr):
                    argument(child, statement, sure)
            return
        if isinstance(e, ast.Compare):
            identity = all(isinstance(op, (ast.Is, ast.IsNot)) for op in e.ops)
            for op, left, right in zip(e.ops, [e.left] + e.comparators, e.comparators):
                kl, kr = kinds.kind(left), kinds.kind(right)
                if (isinstance(op, ORDERINGS) and "inert" not in (kl, kr) and not involves(left) and not involves(right)
                        and not (kl in INTEGERS and kr in INTEGERS or kl == kr and kl in ("str", "bytes", "set"))):
                    problem(e, "%s orders what the gate can't tell are both ints, both strings or both sets, and Python can fail ordering anything else, None among it, when it evaluates the contract" % statement)
                if isinstance(op, (ast.In, ast.NotIn)) and not isinstance(right, ast.Tuple):
                    if results(left):
                        problem(e, "%s tests Result() for membership in something other than a tuple it lists, and in a string or bytes that fails on the value Result() stands for when Python evaluates the contract" % statement)
                    elif kr != "inert" and not involves(left) and not involves(right) and not (
                            kr in ("list", "tuple", "range") or kr in ("str", "bytes") and kl == kr or kr in ("set", "dict") and kl in HASHABLE):
                        problem(e, "%s tests membership in what the gate can't tell is a builtin container that can hold what it tests, and Python can fail testing anything else, None among it, when it evaluates the contract" % statement)
            for child in [e.left] + e.comparators:
                argument(child, statement, sure, identity)
            return
        if isinstance(e, ast.Call) and isinstance(e.func, ast.Name):
            f = e.func.id
            if f in names:
                call(e, statement, sure)
                return
            if f in CONTRACT_BUILTINS and kinds.builtin(f):
                if len(e.args) != CONTRACT_BUILTINS[f] or e.keywords or any(isinstance(a, ast.Starred) for a in e.args):
                    problem(e, "%s calls %s with other than its %d argument%s" % (statement, f, CONTRACT_BUILTINS[f], "s" if CONTRACT_BUILTINS[f] > 1 else ""))
                elif f == "isinstance" and not (isinstance(e.args[1], ast.Name) and e.args[1].id in ISINSTANCE_TYPES and kinds.builtin(e.args[1].id)):
                    problem(e, "%s's isinstance tests other than int, bool, str, bytes, list, tuple, dict, set or object" % statement)
                elif f == "len" and not involves(e.args[0]) and kinds.kind(e.args[0]) not in SIZED | {"inert"}:
                    problem(e, "%s takes the len of what the gate can't tell is a sized builtin, and len fails on anything else, None among it, when Python evaluates the contract" % statement)
                elif f == "abs" and not involves(e.args[0]) and kinds.kind(e.args[0]) not in INTEGERS | {"inert"}:
                    problem(e, "%s takes the abs of what the gate can't tell is an int, and abs fails on anything else, None among it, when Python evaluates the contract" % statement)
                for i, a in enumerate(e.args):
                    argument(a, statement, sure, f == "isinstance" and i == 0)
                return
            problem(e, "%s calls %s, and a contract may call only Nagini's names Invariant's runtime stand-in provides, len, abs and isinstance" % (statement, f))
            return
        if isinstance(e, ast.Call):
            what = "a method call" if isinstance(e.func, ast.Attribute) else "a call"
        else:
            what = CONTRACT_REFUSED.get(type(e.op) if isinstance(e, ast.BinOp) else type(e), "an expression of its own kind")
        refuse(e, what, statement)

    # A default is one of a parameter's values, so it must be of the type its
    # annotation names, which is the type the rule above reads the parameter
    # as: limit: int = None would have a contract order None.
    args = node.args
    positional = args.posonlyargs + args.args
    defaults = list(zip(positional[len(positional) - len(args.defaults):], args.defaults))
    defaults += [(a, d) for a, d in zip(args.kwonlyargs, args.kw_defaults) if d is not None]
    for a, d in defaults:
        declared = kinds.env.get(a.arg)
        try:
            given = value_kind(ast.literal_eval(d))
        except (ValueError, TypeError, SyntaxError, MemoryError, RecursionError):
            given = kinds.constants.get(d.id) if isinstance(d, ast.Name) and d.id not in kinds.bound and d.id not in kinds.params else None
        if declared in (None, "?", "optional") or given is None or given == declared or given == "bool" and declared == "int":
            continue
        shown = ast.unparse(d) if hasattr(ast, "unparse") else given
        problem(d, "defaults %s to %s, which isn't the %s its annotation names, and the gate reads a parameter's type from its annotation" % (a.arg, shown, declared))

    block(node.body, params - deleted)
    return sorted({(r["line"], r["what"]): r for r in out}.values(), key=lambda r: (r["line"], r["what"]))


# Why arithmetic the pinned Nagini models apart from Python is refused, in a
# selected function's body and contract and in a trusted contract's
# abstraction alike (#359).
FLOATS = ("deals in floats, which the pinned Nagini leaves uninterpreted, while Python's arithmetic fails on an int too large "
          "for a float where Nagini's model doesn't")
DIVISION = "divides with /, which makes a float and fails on an int too large for one where Nagini's model doesn't"
BITWISE = "holds a bitwise operator, which the pinned Nagini models apart from Python on negative ints"
DIVIDES = ("holds a %s by other than a positive integer literal, which the pinned Nagini divides as Viper does, apart from "
           "Python for a negative divisor")
REPEATED = ("multiplies what the gate can't tell are both ints by other than an int literal, so it may repeat a sequence "
            "more often than Python can index, which fails where Nagini's model doesn't")


def arithmetic(node, kinds):
    """Each place a function, its signature, body and contract alike, holds
    arithmetic the pinned Nagini models apart from Python: floats and what
    makes them, which it leaves uninterpreted while Python fails on an int
    too large for one, the bitwise operators, which it models apart on
    negative ints, // and % by other than a positive integer literal, which
    it divides as Viper does, and multiplication by other than an int
    literal unless both sides are ints, which could repeat a sequence past
    what Python can index. Shifts and ** stay: Nagini's preconditions on
    them are where Python raises."""
    args = node.args
    annotations = [a.annotation for a in args.posonlyargs + args.args + args.kwonlyargs + [args.vararg, args.kwarg]
                   if a is not None and a.annotation is not None]
    annotations += [node.returns] if node.returns is not None else []
    annotations += [s.annotation for s in ast.walk(node) if isinstance(s, ast.AnnAssign)]
    inside = {id(x) for a in annotations for x in ast.walk(a)}
    floats = {n for n, k in kinds.constants.items() if k in ("float", "complex")}
    out = []

    def problem(sub, what):
        out.append({"line": sub.lineno, "what": what})

    def literal(x):
        return isinstance(x, ast.Constant) and type(x.value) is int and abs(x.value) < REPEATS

    for n in ast.walk(node):
        if isinstance(n, ast.Constant) and type(n.value) in (float, complex):
            problem(n, FLOATS)
        elif isinstance(n, ast.Name) and isinstance(n.ctx, ast.Load) and (
                n.id in ("float", "complex") and kinds.builtin(n.id) or n.id in floats and n.id not in kinds.bound and n.id not in kinds.params):
            problem(n, FLOATS)
        if id(n) in inside:
            continue
        if isinstance(n, (ast.BinOp, ast.AugAssign)):
            left, right = (n.left, n.right) if isinstance(n, ast.BinOp) else (n.target, n.value)
            if isinstance(n.op, ast.Div):
                problem(n, DIVISION)
            elif isinstance(n.op, (ast.BitAnd, ast.BitOr, ast.BitXor)):
                problem(n, BITWISE)
            elif isinstance(n.op, CONTRACT_DIVISIONS) and not (isinstance(right, ast.Constant) and type(right.value) is int and right.value > 0):
                problem(n, DIVIDES % ("floor division" if isinstance(n.op, ast.FloorDiv) else "modulo"))
            elif isinstance(n.op, ast.Mult) and not (literal(left) or literal(right) or kinds.kind(left) in INTEGERS and kinds.kind(right) in INTEGERS):
                problem(n, REPEATED)
        elif isinstance(n, ast.UnaryOp) and isinstance(n.op, ast.Invert):
            problem(n, BITWISE)
    return sorted({(r["line"], r["what"]): r for r in out}.values(), key=lambda r: (r["line"], r["what"]))


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
    # A selected function's contract, which runs where the application runs,
    # and the arithmetic Nagini models apart from Python, in any function.
    eager, sums = [], []
    if "standin" in request:
        standin = standin_of(base64.b64decode(request["standin"]))
        names = {n: standin[n] for n in standin if n not in bound}
        kinds = Kinds(node, tree, bindings, names)
        eager, sums = contracts(node, names, kinds), arithmetic(node, kinds)
    return {
        "globals": rebound, "future": future, "dynamic": dynamic(tree), "bound": bound, "tries": tries, "contracts": eager, "arithmetic": sums,
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

# Calls that run code from a path or a code object, which no import shows.
BY_PATH = frozenset(("runpy.run_path", "runpy.run_code", "importlib.util.spec_from_file_location",
                     "importlib.machinery.SourceFileLoader", "importlib.machinery.SourcelessFileLoader"))

# Calls that run or compile code from a string, which no parse shows.
FROM_STRING = frozenset(("builtins.exec", "builtins.eval", "builtins.compile"))

# Calls that hand back a module, or every object, from an object, which no
# reference to the module shows.
REFLECTION = frozenset(("inspect.getmodule", "gc.get_objects", "gc.get_referrers", "gc.get_referents"))

# What holds a module's names, or the builtins', reached through a function,
# a frame or a module's own namespace, and a builtin function's __self__,
# which is the builtins module itself.
NAMESPACES = frozenset(("__globals__", "f_globals", "f_builtins", "__builtins__", "__self__"))

# What takes an attribute by a name it's given, and a namespace to look a
# name up in, where a string can reach those.
GETATTRS = frozenset(("builtins.getattr", "builtins.setattr", "builtins.delattr", "builtins.hasattr", "operator.attrgetter"))
DUNDER_ACCESS = frozenset(("__getattribute__", "__getattr__", "__setattr__", "__delattr__"))
NAMESPACE_LOOKUPS = frozenset(("builtins.vars", "builtins.globals", "builtins.locals"))

# What decides what an import loads, which can put another module in place of
# one the proof takes as it ships.
IMPORT_STATE = frozenset(("sys.meta_path", "sys.path_hooks", "sys.path_importer_cache"))

# What a function runs, which writing changes whatever binds the function.
FUNCTION_PARTS = frozenset(("__code__", "__defaults__", "__kwdefaults__"))

# What the proof takes as it is besides the module: Invariant's runtime
# stand-in for nagini_contracts, whose calls the module states its contract
# with, and the builtins, which the function and its contract read. The scan
# protects both as it does the module (#359).
SHARED = ("nagini_contracts.contracts", "nagini_contracts", "builtins")


def scope_bindings(body, params=()):
    """The names a scope binds, from its statements, other than by an import,
    which the scan for writes reads itself: assignments, loops, handlers,
    patterns, and nested functions' and classes' own names, but not what
    they bind, with what the scope declares global left out."""
    names, declared, stack = set(params), set(), list(body)
    while stack:
        n = stack.pop()
        if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
            names.add(n.name)
            stack.extend(n.decorator_list)
            stack.extend(n.bases + [k.value for k in n.keywords] if isinstance(n, ast.ClassDef)
                         else n.args.defaults + [d for d in n.args.kw_defaults if d is not None])
            continue
        if isinstance(n, ast.Lambda):
            stack.extend(n.args.defaults + [d for d in n.args.kw_defaults if d is not None])
            continue
        if isinstance(n, COMPREHENSIONS):
            stack.append(n.generators[0].iter)
            names |= {s.target.id for s in ast.walk(n) if isinstance(s, ast.NamedExpr) and isinstance(s.target, ast.Name)}
            continue
        if isinstance(n, (ast.Global, ast.Nonlocal)):
            declared |= set(n.names)
        elif isinstance(n, ast.Name) and not isinstance(n.ctx, ast.Load):
            names.add(n.id)
        elif isinstance(n, ast.ExceptHandler) and n.name:
            names.add(n.name)
        elif getattr(ast, "MatchAs", None) and isinstance(n, (ast.MatchAs, ast.MatchStar)) and n.name:
            names.add(n.name)
        elif getattr(ast, "MatchMapping", None) and isinstance(n, ast.MatchMapping) and n.rest:
            names.add(n.rest)
        stack.extend(ast.iter_child_nodes(n))
    return names - declared


def builtin_reader(tree, parents):
    """A test of whether a bare name in tree reads the builtin of that name:
    whether no scope around it, its module's top level included, binds the
    name other than by an import."""
    cache = {}

    def bindings(scope):
        if scope not in cache:
            if isinstance(scope, ast.Module):
                cache[scope] = scope_bindings(scope.body)
            elif isinstance(scope, ast.Lambda):
                args = scope.args
                cache[scope] = scope_bindings([scope.body], [a.arg for a in args.posonlyargs + args.args + args.kwonlyargs]
                                              + [a.arg for a in (args.vararg, args.kwarg) if a is not None])
            elif isinstance(scope, (ast.FunctionDef, ast.AsyncFunctionDef)):
                args = scope.args
                cache[scope] = scope_bindings(scope.body, [a.arg for a in args.posonlyargs + args.args + args.kwonlyargs]
                                              + [a.arg for a in (args.vararg, args.kwarg) if a is not None])
            elif isinstance(scope, ast.ClassDef):
                cache[scope] = scope_bindings(scope.body)
            else:
                cache[scope] = comprehension_targets(scope)
        return cache[scope]

    def reads_builtin(name):
        child, scope, in_function = name, parents.get(name), False
        while scope is not None:
            if isinstance(scope, (ast.FunctionDef, ast.AsyncFunctionDef)):
                # Defaults, annotations and decorators run in the scope around.
                if child in scope.body:
                    if name.id in bindings(scope):
                        return False
                    in_function = True
            elif isinstance(scope, ast.Lambda):
                if child is scope.body:
                    if name.id in bindings(scope):
                        return False
                    in_function = True
            elif isinstance(scope, ast.ClassDef):
                # A class's body is no scope for the functions in it.
                if child in scope.body and not in_function and name.id in bindings(scope):
                    return False
            elif isinstance(scope, COMPREHENSIONS):
                if child is not scope.generators[0] and name.id in bindings(scope):
                    return False
            elif isinstance(scope, ast.Module):
                return name.id not in bindings(scope)
            child, scope = scope, parents.get(scope)
        return True

    return reads_builtin


def compiled_module_of(path):
    """The module a compiled file is, by its path from the application's
    root: an extension's name runs to its first dot."""
    folder, _, base = path.rpartition("/")
    name = base.split(".", 1)[0]
    if name == "__init__":
        return folder.replace("/", ".")
    return (folder + "/" + name if folder else name).replace("/", ".")


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
    the module's names hold, or what the stand-in for nagini_contracts or
    the builtins bind, or reach one of them in a way a parse can't follow.
    Test sources, which the request marks, are scanned only where production
    code imports them; the proof covers the module as production code leaves
    it. A reference to the module, to nagini_contracts or its contracts, to
    builtins, to a package above the module, or to an application module
    whose imports bind one of those, through an import, a relative import, a
    module that imports it or a star import, may only read attributes, and
    writing one is refused where it's one of theirs or rebinds a module on
    the way to them. So are reaching one of them by name, through
    sys.modules, importlib, __import__, runpy, pkgutil, pydoc or mock's
    patchers, reading the namespace of a source that holds one, and a source
    the host's Python can't read, a compiled module among them, that
    production code loads. A test source production code loads, by an import
    or by a constant name, is production code. The refusals that don't rest
    on reaching one of those modules by name apply where production code
    runs beside the module: in it, the packages on its way, every production
    source that loads one of them and all those load. There, running code
    from a path, a string or a code object, reaching modules through objects
    with inspect or gc, reading __globals__, f_globals, f_builtins,
    __builtins__ or __self__, or taking one by name, writing what a function
    runs, changing what an import loads, and reaching a module by a name the
    source computes are refused too (#359)."""
    target = request["module"]
    protected = (target,) + SHARED
    out, sources, unread = [], [], {}

    def report(path, line, what):
        out.append({"path": path, "line": line, "what": what})

    for f in request["files"]:
        path = f["path"]
        if f.get("compiled"):
            # Reported only if production code loads it, or it stands in
            # for the module or a package on its way.
            unread[compiled_module_of(path)] = (path, 0, "it's compiled", True)
            continue
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

    def candidates_of(names, stars, expr):
        """The dotted paths a source's expression may name."""
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

    def by_name(source):
        """What a source loads by a constant name: through importlib and its
        kin, or as a patcher's target, which mock imports."""
        _, tree, names, stars = source[:4]
        return [node.args[0].value.replace(":", ".") for node in ast.walk(tree)
                if isinstance(node, ast.Call) and node.args and isinstance(node.args[0], ast.Constant) and isinstance(node.args[0].value, str)
                and candidates_of(names, stars, node.func) & (BY_NAME | PATCHERS)]

    # Production sources, and what they load, by an import or by a constant
    # name, which runs in production too, whether or not it's a test helper,
    # with each package on its way.
    by_module = {module_of(s[0]): s for s in sources}
    scanned = [s for s in sources if not s[5]]
    included = {s[0] for s in scanned}
    segments = target.split(".")
    above = {".".join(segments[:i]) for i in range(1, len(segments))} - set(protected)
    loads = {}
    for s in scanned:
        mods = set()
        for loaded in s[4] + by_name(s):
            parts = loaded.split(".")
            for i in range(1, len(parts) + 1):
                prefix = ".".join(parts[:i])
                mods.add(prefix)
                other = by_module.get(prefix)
                if other is not None and other[0] not in included:
                    included.add(other[0])
                    scanned.append(other)
                if prefix in unread:
                    unread[prefix] = unread[prefix][:3] + (False,)
        loads[module_of(s[0])] = mods

    # What runs beside the module: it, the packages on its way, every
    # production source that loads one of those, directly or through others,
    # what the gate's own processes load, through the selected tests,
    # conformance and backing tests the request names and any source they
    # load, and everything all those load.
    running = above | {target}
    changed = True
    while changed:
        changed = False
        for module, mods in loads.items():
            if module not in running and mods & running:
                running.add(module)
                changed = True
    every = {}
    for s in sources:
        every[module_of(s[0])] = {".".join(l.split(".")[:i]) for l in s[4] + by_name(s) for i in range(1, len(l.split(".")) + 1)}
    stack = [module_of(r) for r in request.get("roots", []) if module_of(r) in every]
    seen = set(stack)
    while stack:
        for loaded in every.get(stack.pop(), ()):
            if loaded in unread:
                unread[loaded] = unread[loaded][:3] + (False,)
            if loaded in every and loaded not in seen:
                seen.add(loaded)
                stack.append(loaded)
                running.add(loaded)
    changed = True
    while changed:
        changed = False
        for module in list(running):
            for loaded in loads.get(module, ()):
                if loaded in loads and loaded not in running:
                    running.add(loaded)
                    changed = True
    # A compiled module where the module or a package on its way would be
    # is what runs in its place.
    for prefix in above | {target}:
        if prefix in unread:
            unread[prefix] = unread[prefix][:3] + (False,)
    for path, line, why, tests in unread.values():
        if not tests:
            report(path, line, "can't be read by the host's Python (%s), so the scan for writes can't see it" % why)

    # The module and the rest of what the scan protects, the packages on the
    # module's path, and every application module whose imports bind one of
    # them, so it's reached by attribute too.
    reach = above | set(protected)
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
        those modules or something in one the scan protects."""
        inside = tuple(p + "." for p in protected)
        return any(r in reach or r.startswith(inside) for r in resolve(name.replace(":", ".")))

    def shown(refs):
        return next((p for p in protected if p in refs), None) or max(sorted(refs), key=len)

    def changes(module):
        """Why writing one of a protected module's attributes is refused."""
        if module == target:
            return "which no binding in the module shows"
        if module == "builtins":
            return "which changes what every module's builtins bind"
        return "which changes Invariant's runtime stand-in for nagini_contracts"

    for path, tree, names, stars, _, _ in scanned:
        parents = {child: node for node in ast.walk(tree) for child in ast.iter_child_nodes(node)}
        held = {r for bs in names.values() for b in bs for r in reach & resolve(b)} | {r for s in stars for r in reach & resolve(s)}
        # Whether the refusals that rest on no reference apply here.
        wide = module_of(path) in running
        reads_builtin = builtin_reader(tree, parents)

        def candidates(expr, names=names, stars=stars, reads_builtin=reads_builtin):
            # A bare name a scope around it binds is that binding, not the
            # builtin of its name.
            found = candidates_of(names, stars, expr)
            root = expr
            while isinstance(root, ast.Attribute):
                root = root.value
            if isinstance(root, ast.Name) and root.id not in names and not reads_builtin(root):
                found = {f for f in found if not f.startswith("builtins." + root.id)}
            return found

        def literal_store(node):
            """Whether an attribute store writes a literal string or number,
            which no function runs."""
            parent = parents.get(node)
            if isinstance(parent, ast.Assign) and node in parent.targets or isinstance(parent, ast.AnnAssign) and parent.target is node:
                return isinstance(parent.value, ast.Constant) and parent.value.value is not None
            return False

        def use(node, refs, line):
            parent = parents.get(node)
            if isinstance(parent, ast.Attribute) and parent.value is node:
                if isinstance(parent.ctx, ast.Load):
                    if reach & candidates(parent):
                        return
                    if parent.attr.startswith("__") and parent.attr.endswith("__") and parent.attr not in PLAIN_DUNDERS:
                        report(path, line, "reads %s.%s, which reaches the module's names" % (shown(refs), parent.attr))
                    return
                if refs & set(protected):
                    module = shown(refs & set(protected))
                    report(path, line, "writes %s.%s, %s" % (module, parent.attr, changes(module)))
                    return
                for r in sorted(refs & above):
                    reached = (above | set(protected)) & resolve(r + "." + parent.attr)
                    if reached:
                        report(path, line, "rebinds %s's attribute %s, through which code reaches %s" % (
                            r, parent.attr, shown(reached & set(protected)) if reached & set(protected) else target))
                        return
                return
            call = parent if isinstance(parent, ast.Call) and node is not parent.func else None
            if isinstance(parent, (ast.keyword, ast.Starred)) and isinstance(parents.get(parent), ast.Call):
                call = parents[parent]
            if call is not None and not refs & set(protected) and node in call.args[:1] and len(call.args) > 1:
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
                        if wide:
                            report(path, line, "uses sys.modules other than to look a module up by its name, and the scan for writes can't follow it")
                        continue
                    if not (isinstance(key, ast.Constant) and isinstance(key.value, str)):
                        if wide:
                            report(path, line, "looks a module up in sys.modules by a name it computes, and the scan for writes can't follow it")
                    elif reaches(key.value):
                        report(path, line, "looks %s up in sys.modules, and the scan for writes can't follow it" % key.value)
            if not wide:
                # Elsewhere only what reaches a protected module by name is
                # refused, which the rules above and the calls below follow.
                if isinstance(node, ast.Call):
                    funcs, arg = candidates(node.func), node.args[0] if node.args else None
                    constant = isinstance(arg, ast.Constant) and isinstance(arg.value, str)
                    if held and (funcs & {"builtins.globals", "builtins.locals"} or "builtins.vars" in funcs and not node.args and not node.keywords):
                        report(path, line, "reads its own namespace, which holds a module on the way to %s" % (
                            shown(held & set(protected)) if held & set(protected) else target))
                    if funcs & BY_NAME and constant and not arg.value.startswith(".") and (
                            reaches(arg.value) or funcs & {"builtins.__import__", "importlib.__import__"} and arg.value.split(".")[0] in reach):
                        report(path, line, "imports %s by name, and the scan for writes can't follow it" % arg.value)
                    if funcs & PATCHERS and constant and (arg.value == "sys.modules" or reaches(arg.value)):
                        report(path, line, "patches %s by name, and the scan for writes can't follow it" % arg.value)
                continue
            if isinstance(node, ast.Attribute) and node.attr in NAMESPACES or isinstance(node, ast.Name) and node.id == "__builtins__":
                what = node.attr if isinstance(node, ast.Attribute) else node.id
                report(path, line, "%s %s, which holds a module's names" % ("reads" if isinstance(node.ctx, ast.Load) else "writes", what))
            if isinstance(node, ast.Attribute) and node.attr in FUNCTION_PARTS and not isinstance(node.ctx, ast.Load) and not literal_store(node):
                report(path, line, "writes %s, which changes what a function runs, and the scan for writes can't follow it" % node.attr)
            # A namespace looked up by a constant name, as getattr and a
            # namespace's subscript take one.
            if (isinstance(node, ast.Subscript) and isinstance(node.slice, ast.Constant) and node.slice.value in NAMESPACES
                    and (isinstance(node.value, ast.Call) and candidates(node.value.func) & NAMESPACE_LOOKUPS
                         or isinstance(node.value, ast.Attribute) and node.value.attr == "__dict__")):
                report(path, line, "looks %s up in a namespace, which holds a module's names, and the scan for writes can't follow it" % node.slice.value)
            # Running code from a string, reaching modules through objects and
            # changing what an import loads are refused wherever the function
            # or the state is reached, not only called.
            if isinstance(node, (ast.Name, ast.Attribute)) and isinstance(node.ctx, ast.Load) and not (
                    isinstance(parents.get(node), ast.Attribute) and parents[node].value is node):
                reached = candidates(node)
                if reached & FROM_STRING:
                    report(path, line, "runs or compiles code from a string, and the scan for writes can't follow it")
                if reached & REFLECTION:
                    report(path, line, "reaches modules through objects, and the scan for writes can't follow it")
            # The import state is used through its own methods, so its name is
            # refused where it's an attribute's base too.
            if isinstance(node, (ast.Name, ast.Attribute)) and isinstance(node.ctx, ast.Load) and candidates(node) & IMPORT_STATE:
                report(path, line, "uses %s, which can load another module in place of one the proof takes as it ships, and the scan for writes can't follow it" % sorted(candidates(node) & IMPORT_STATE)[0])
            if not isinstance(node, ast.Call):
                continue
            # A string names a module only where it's handed to something
            # that reaches modules by name; a logger's name is just a name.
            funcs, arg = candidates(node.func), node.args[0] if node.args else None
            constant = isinstance(arg, ast.Constant) and isinstance(arg.value, str)
            if funcs & GETATTRS or isinstance(node.func, ast.Attribute) and node.func.attr in DUNDER_ACCESS:
                for a in node.args:
                    if isinstance(a, ast.Constant) and a.value in NAMESPACES:
                        report(path, line, "takes %s by name, which holds a module's names, and the scan for writes can't follow it" % a.value)
            if held and (funcs & {"builtins.globals", "builtins.locals"} or "builtins.vars" in funcs and not node.args and not node.keywords):
                report(path, line, "reads its own namespace, which holds a module on the way to %s" % (
                    shown(held & set(protected)) if held & set(protected) else target))
            if (funcs & {"builtins.setattr", "builtins.delattr"} and len(node.args) > 1 and isinstance(node.args[1], ast.Constant)
                    and node.args[1].value in FUNCTION_PARTS
                    and not (funcs & {"builtins.setattr"} and len(node.args) > 2 and isinstance(node.args[2], ast.Constant) and node.args[2].value is not None)):
                report(path, line, "writes %s, which changes what a function runs, and the scan for writes can't follow it" % node.args[1].value)
            if funcs & BY_PATH:
                report(path, line, "runs code from a path or a code object, and the scan for writes can't follow it")
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
