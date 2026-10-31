"""Controlled startup and per-test observations for a prepared application."""

import ast
from collections import Counter
import hashlib
import hmac
import inspect
import json
import os
import sys
import traceback
import types
import unittest
from importlib.machinery import PathFinder


with open(sys.argv[1], encoding="utf-8") as stream:
    request = json.load(stream)
root = request["root"]
trusted = list(sys.path) + [os.path.dirname(os.path.abspath(__file__))]
application = [root, os.path.dirname(os.path.join(root, request["driver"]))]
libraries = [request["libraries"]]
sys.path[:] = list(dict.fromkeys(trusted + application + libraries))


# Search trust groups independently so regular dependency packages cannot
# replace application namespaces, including at nested package boundaries.
class ApplicationFinder:
    @classmethod
    def find_spec(cls, fullname, path=None, target=None):
        groups = [[], [], []]
        for entry in sys.path if path is None else path:
            absolute = os.path.abspath(entry)
            if any(absolute == base or absolute.startswith(base + "/") for base in trusted):
                groups[0].append(entry)
            elif any(absolute == base or absolute.startswith(base + "/") for base in application):
                groups[1].append(entry)
            else:
                groups[2].append(entry)
        for group in groups:
            spec = PathFinder.find_spec(fullname, group, target)
            if spec is not None:
                return spec
        return None


sys.meta_path[:] = [ApplicationFinder if finder is PathFinder else finder
                    for finder in sys.meta_path]
events = open(request["output"], "w", encoding="utf-8", buffering=1)
count = 0


def emit(record):
    events.write(json.dumps(record, ensure_ascii=True, sort_keys=True) + "\n")
    events.flush()


def declarations(node):
    for child in ast.iter_child_nodes(node):
        if isinstance(child, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
            yield child
        else:
            yield from declarations(child)


def lexical_locations(tree):
    locations = {}

    def namespace(node, prefix):
        children = sorted(declarations(node), key=lambda d: (d.lineno, d.col_offset))
        totals = Counter(child.name for child in children)
        ordinals = Counter()
        for child in children:
            ordinals[child.name] += 1
            symbol = prefix + child.name
            if totals[child.name] > 1:
                symbol += "[" + str(ordinals[child.name]) + "]"
            if isinstance(child, ast.ClassDef):
                namespace(child, symbol + ".")
            else:
                first = min([child.lineno] + [d.lineno for d in child.decorator_list])
                locations[(first, child.name)] = symbol
                namespace(child, symbol + ".<locals>.")

    namespace(tree, "")
    return locations


def compiled_methods(code):
    yield code
    for constant in code.co_consts:
        if isinstance(constant, types.CodeType):
            yield from compiled_methods(constant)


sources = {}


def identity(test):
    method = getattr(test, getattr(test, "_testMethodName", ""), None)
    method = inspect.unwrap(method)
    code = getattr(method, "__code__", None)
    if code is None:
        return "", "", None
    filename = os.path.abspath(code.co_filename)
    if not filename.startswith(root + "/"):
        return "", "", code
    if filename not in sources:
        with open(filename, "rb") as stream:
            raw = stream.read()
        tree = ast.parse(raw, filename)
        sources[filename] = ("sha256:" + hashlib.sha256(raw).hexdigest(),
                             lexical_locations(tree),
                             list(compiled_methods(compile(raw, filename, "exec"))))
    digest, locations, compiled = sources[filename]
    symbol = locations.get((code.co_firstlineno, code.co_name))
    if symbol is None or not any(code == original for original in compiled):
        return "", digest, code
    source = os.path.relpath(filename, root).replace(os.sep, "/")
    component = "python:" + source[:-3].replace("/", ".") + "#" + symbol + "@" + source
    check = component if request.get("legacy") or component == request["check"]["id"] else ""
    return check, digest, code


# A trusted contract's backing tests (#294) support it only by reaching its
# real implementation, which the runner resolves before any test loads: the
# application's own declaration, or the standard library's or an installed
# distribution's code. Calls are counted per test, and assertions are the
# runner's own records of unittest's checks. Neither runs for other tests.
boundary = request.get("boundary")
boundary_python = {}
boundary_c = []
assertions = [None]


def boundary_implementation(target):
    """The code a call into target runs, or the C callable it is, and its file."""
    if inspect.isclass(target):
        codes = []
        for value in vars(target).values():
            if isinstance(value, (staticmethod, classmethod)):
                value = value.__func__
            candidates = [value.fget, value.fset, value.fdel] if isinstance(value, property) else [value]
            for candidate in candidates:
                if candidate is not None and inspect.isfunction(inspect.unwrap(candidate)):
                    codes.append(inspect.unwrap(candidate).__code__)
        if not codes:
            raise TypeError("the class defines no Python code whose calls the runner can observe")
        return "python", codes, [], getattr(sys.modules.get(target.__module__), "__file__", None)
    function = inspect.unwrap(target)
    if inspect.ismethod(function):
        function = function.__func__
    if inspect.isfunction(function):
        return "python", [function.__code__], [], function.__code__.co_filename
    if isinstance(target, (types.BuiltinFunctionType, types.MethodDescriptorType, types.WrapperDescriptorType,
                           types.ClassMethodDescriptorType, types.MethodWrapperType)):
        owner = getattr(target, "__objclass__", None)
        module = getattr(owner if owner is not None else target, "__module__", None)
        if module is None:
            module = getattr(getattr(target, "__self__", None), "__name__", None)
        return "c", [], [target], getattr(sys.modules.get(module), "__file__", None)
    raise TypeError("the operation is neither a Python function or class nor a C function")


def boundary_digest(filename):
    with open(filename, "rb") as stream:
        return "sha256:" + hashlib.sha256(stream.read()).hexdigest()


def boundary_within(filename, base):
    return os.path.realpath(filename).startswith(os.path.realpath(base) + os.sep)


def boundary_recorded(distribution, filename):
    """Whether the installed distribution's RECORD lists this file's bytes."""
    import base64
    real = os.path.realpath(filename)
    for entry in distribution.files or []:
        if os.path.realpath(distribution.locate_file(entry)) == real:
            if entry.hash is None or entry.hash.mode != "sha256":
                return False
            with open(real, "rb") as stream:
                digest = base64.urlsafe_b64encode(hashlib.sha256(stream.read()).digest()).rstrip(b"=").decode()
            return digest == entry.hash.value
    return False


def resolve_boundary(spec):
    record = dict(type="boundary", contract=spec["contract"], kind=spec["kind"], resolved=False, callable="",
                  file=dict(path="", sha256=""), distribution="", version="", recorded=False,
                  python="%d.%d.%d" % sys.version_info[:3], diagnostics=[])
    codes, callables = [], []
    try:
        if spec["kind"] == "application":
            declaration = spec["declaration"]
            filename = os.path.join(root, declaration["source"])
            with open(filename, "rb") as stream:
                raw = stream.read()
            places = [place for place, symbol in lexical_locations(ast.parse(raw, filename)).items() if symbol == declaration["symbol"]]
            if len(places) == 1:
                codes = [code for code in compiled_methods(compile(raw, filename, "exec"))
                         if (code.co_firstlineno, code.co_name) == places[0]]
            if len(codes) != 1:
                raise LookupError("the operation's declaration isn't one function in its source")
            record.update(callable="python", file=dict(path=declaration["source"], sha256="sha256:" + hashlib.sha256(raw).hexdigest()))
        else:
            import importlib
            target = importlib.import_module(spec["module"])
            for part in spec["symbol"].split("."):
                target = getattr(target, part)
            form, codes, callables, origin = boundary_implementation(target)
            record["callable"] = form
            name = spec["module"] + "." + spec["symbol"]
            frozen = origin is None or origin.startswith("<frozen ")
            if spec["kind"] == "standard_library":
                import sysconfig
                bases = [sysconfig.get_paths()[key] for key in ("stdlib", "platstdlib")]
                if not frozen:
                    base = next((base for base in bases if boundary_within(origin, base)), None)
                    if base is None:
                        raise LookupError("%s resolves outside the runtime's standard library" % name)
                    record["file"] = dict(path=os.path.relpath(os.path.realpath(origin), os.path.realpath(base)).replace(os.sep, "/"), sha256=boundary_digest(origin))
            else:
                import re
                from importlib import metadata
                libraries = request["libraries"]
                if frozen or not boundary_within(origin, libraries):
                    raise LookupError("%s resolves outside the installed dependencies" % name)
                normalize = lambda value: re.sub(r"[-_.]+", "-", value).lower()
                # The application finder stands in for PathFinder, so ask the
                # metadata finder for the libraries directory alone.
                installed = metadata.MetadataPathFinder.find_distributions(metadata.DistributionFinder.Context(path=[libraries]))
                found = [d for d in installed if normalize(d.metadata["Name"] or "") == spec["distribution"]]
                if len(found) != 1:
                    raise LookupError("%d installed distributions are named %s" % (len(found), spec["distribution"]))
                record.update(distribution=spec["distribution"], version=found[0].version or "", recorded=boundary_recorded(found[0], origin),
                              file=dict(path=os.path.relpath(os.path.realpath(origin), os.path.realpath(libraries)).replace(os.sep, "/"), sha256=boundary_digest(origin)))
                if not record["recorded"]:
                    record["diagnostics"].append("%s's file doesn't match %s's RECORD" % (name, spec["distribution"]))
        record["resolved"] = True
    except Exception as error:
        record["diagnostics"].append("".join(traceback.format_exception_only(type(error), error)).strip())
        record["callable"], codes, callables = "", [], []
    return record, codes, callables


def boundary_called(function):
    for target in boundary_c:
        if function is target:
            return True
        if getattr(function, "__name__", None) != getattr(target, "__name__", None):
            continue
        owner, bound = getattr(target, "__objclass__", None), getattr(function, "__self__", None)
        # A method descriptor arrives bound to its instance, a module function
        # to its module.
        if owner is not None and isinstance(bound, owner) or owner is None and bound is not None and bound is getattr(target, "__self__", None):
            return True
    return False


def watch_assertions():
    """Wrap unittest's assertions so each outermost one records its outcome."""
    import functools
    contexts = tuple(c for c in (getattr(unittest.case, name, None) for name in ("_AssertRaisesBaseContext", "_AssertLogsContext")) if isinstance(c, type))
    depth = [0]

    def wrap(name, function, exit=False):
        @functools.wraps(function)
        def checked(*args, **kwargs):
            records = assertions[0]
            if records is None:
                return function(*args, **kwargs)
            depth[0] += 1
            try:
                value = function(*args, **kwargs)
            except BaseException as error:
                depth[0] -= 1
                if depth[0] == 0:
                    records.append(dict(id="%s:%d" % (name, len(records) + 1), outcome="failed" if isinstance(error, AssertionError) else "error"))
                raise
            depth[0] -= 1
            if depth[0] == 0:
                if exit:
                    # A with-block's exception the context didn't accept means its
                    # expectation failed.
                    records.append(dict(id="%s:%d" % (name, len(records) + 1), outcome="failed" if len(args) > 1 and args[1] is not None and not value else "passed"))
                elif not isinstance(value, contexts):
                    records.append(dict(id="%s:%d" % (name, len(records) + 1), outcome="passed"))
            return value
        return checked

    for name in dir(unittest.TestCase):
        value = getattr(unittest.TestCase, name)
        if (name.startswith("assert") or name == "fail") and inspect.isfunction(value):
            setattr(unittest.TestCase, name, wrap(name, value))
    for name, context in (("assertRaises", "_AssertRaisesContext"), ("assertWarns", "_AssertWarnsContext"), ("assertLogs", "_AssertLogsContext")):
        context = getattr(unittest.case, context, None)
        if isinstance(context, type) and inspect.isfunction(context.__dict__.get("__exit__")):
            context.__exit__ = wrap(name, context.__dict__["__exit__"], exit=True)


class Observations(unittest.TestResult):
    def __init__(self):
        super().__init__()
        self.current = None
        self.method_code = None
        self.pending = None

    def row(self, test):
        check, digest, code = identity(test)
        # Runtime IDs are descriptive only; lexical identity is independent.
        row = dict(type="test", id=str(test.id()), check=check,
                   source_sha256=digest, exercised=False,
                   outcome="not_run", diagnostics=[])
        if boundary is not None:
            row.update(boundary_calls=0, assertions=[])
        return row, code

    def profile(self, frame, event, arg):
        if self.pending is None:
            return
        if event == "call":
            code = frame.f_code
            if code is self.method_code:
                self.pending["exercised"] = True
            if boundary_python:
                known = boundary_python.get((code.co_filename, code.co_firstlineno, code.co_name))
                if known is not None and (known is code or known == code):
                    self.pending["boundary_calls"] += 1
        elif event == "c_call" and boundary_c and boundary_called(arg):
            self.pending["boundary_calls"] += 1

    def startTest(self, test):
        super().startTest(test)
        self.current = test
        self.pending, self.method_code = self.row(test)
        if boundary is not None:
            assertions[0] = self.pending["assertions"]
        sys.setprofile(self.profile)

    def record(self, test, outcome, diagnostic=None):
        if test is self.current:
            row = self.pending
        else:
            row, _ = self.row(test)
        # Fixture and subtest failures take precedence over later success.
        if row["outcome"] not in {"failed", "error"} or outcome == "error":
            row["outcome"] = outcome
        if diagnostic is not None:
            row["diagnostics"].append(diagnostic)
        if outcome == "skipped":
            row["exercised"] = False
        if test is not self.current:
            self.write(row)

    def write(self, row):
        global count
        emit(row)
        count += 1

    def stopTest(self, test):
        sys.setprofile(None)
        assertions[0] = None
        self.write(self.pending)
        self.current = self.pending = self.method_code = None
        super().stopTest(test)

    def diagnostic(self, error):
        return "".join(traceback.format_exception(*error))

    def addSuccess(self, test):
        super().addSuccess(test)
        self.record(test, "passed")

    def addFailure(self, test, error):
        super().addFailure(test, error)
        self.record(test, "failed", self.diagnostic(error))

    def addError(self, test, error):
        super().addError(test, error)
        self.record(test, "error", self.diagnostic(error))

    def addSkip(self, test, reason):
        super().addSkip(test, reason)
        self.record(test, "skipped", str(reason))

    def addExpectedFailure(self, test, error):
        super().addExpectedFailure(test, error)
        self.record(test, "expected_failure", self.diagnostic(error))

    def addUnexpectedSuccess(self, test):
        super().addUnexpectedSuccess(test)
        self.record(test, "unexpected_success", "unexpected success")

    def addSubTest(self, test, subtest, error):
        super().addSubTest(test, subtest, error)
        if error is not None:
            outcome = "failed" if issubclass(error[0], test.failureException) else "error"
            self.record(test, outcome, self.diagnostic(error))


def conformance():
    import invariant_observe
    secret = os.path.join(os.path.dirname(request["output"]), "authentication.key")
    with open(secret, "rb") as stream:
        authentication = hmac.new(stream.read(), digestmod="sha256")
    os.unlink(secret)
    # Authenticate bytes emitted by the collector, never bytes read back from
    # the application-writable file. Only a completed collector can seal them.
    encode = json.dumps
    write, flush = events.write, events.flush
    callers = (invariant_observe.Collector.write.__code__, invariant_observe.run.__code__)
    frame = sys._getframe

    def collect(record):
        caller = frame(1)
        if not any(caller.f_code is code for code in callers) or caller.f_globals is not vars(invariant_observe):
            raise ValueError("observation output did not come from the collector")
        line = encode(record, ensure_ascii=True, sort_keys=True) + "\n"
        authentication.update(line.encode("utf-8"))
        write(line)
        flush()

    begin = encode(dict(type="begin", version=2, key=request["key"],
                        snapshot_sha256=request["snapshot_sha256"], runner_sha256=request["runner_sha256"],
                        execution=request["execution"]), ensure_ascii=True, sort_keys=True) + "\n"
    authentication.update(begin.encode("utf-8"))
    write(begin)
    flush()
    invariant_observe.run(request, collect, lexical_locations, compiled_methods, sys.argv[2:])
    write(encode(dict(type="authentication", hmac_sha256=authentication.hexdigest()), sort_keys=True) + "\n")
    flush()
    events.close()


if request["key"]["kind"] == "conformance":
    conformance()
    sys.exit(0)

emit(dict(type="begin", version=1, key=request["key"],
          snapshot_sha256=request["snapshot_sha256"], runner_sha256=request["runner_sha256"]))
if sys.argv[2:4] != ["-m", "unittest"] or len(sys.argv) < 5:
    raise ValueError("explicit unittest selections required")
selections = sys.argv[4:]
sys.argv = ["unittest"] + selections
if boundary is not None:
    resolved, codes, callables = resolve_boundary(boundary)
    boundary_python.update(((code.co_filename, code.co_firstlineno, code.co_name), code) for code in codes)
    boundary_c.extend(callables)
    emit(resolved)
    watch_assertions()
if request.get("legacy"):
    suite = unittest.defaultTestLoader.discover(root, pattern="test_*.py", top_level_dir=root)
else:
    suite = unittest.defaultTestLoader.loadTestsFromNames(selections)
result = Observations()
suite.run(result)
emit(dict(type="end", count=count))
events.close()
sys.exit(0 if result.wasSuccessful() else 1)
