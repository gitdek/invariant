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
# distribution's code. A call counts only when the test method makes it,
# directly or through code it calls, and never when unittest or the runner
# does, except a callable the test hands an assertion, such as
# assertRaises(E, f, x). setUp and tearDown don't count. Assertions are the
# runner's own records of unittest's checks. Neither runs for other tests.
boundary = request.get("boundary")
boundary_python = {}
boundary_receivers = {}
boundary_equal = False
boundary_c = []
assertions = [None]
handed = [[], []]
runner_globals = globals()


def boundary_implementation(target):
    """The code a call into target runs, or the C callable it is, and its file:
    the file that code is in, or for a C callable its extension's, if any."""
    if inspect.isclass(target):
        functions = []
        for value in vars(target).values():
            # A staticmethod has no receiver to check at call time.
            if isinstance(value, staticmethod):
                continue
            if isinstance(value, classmethod):
                value = value.__func__
            candidates = [value.fget, value.fset, value.fdel] if isinstance(value, property) else [value]
            for candidate in candidates:
                candidate = inspect.unwrap(candidate) if candidate is not None else None
                # Only methods compiled in the class body, by the qualified
                # name their code was compiled under, which no assignment
                # changes: code installed from elsewhere, such as an Enum's
                # __new__, total_ordering's comparisons or a dataclass's
                # generated methods, isn't the class's own.
                if (inspect.isfunction(candidate) and candidate.__code__.co_argcount > 0
                        and candidate.__code__.co_qualname.rpartition(".")[0] == target.__qualname__
                        and candidate not in functions):
                    functions.append(candidate)
        if not functions:
            raise TypeError("the class has no Python method written in its own body whose calls the runner can observe; generated methods, such as a dataclass's, aren't its own, and a type written in C has none")
        files = {f.__code__.co_filename for f in functions}
        if len(files) != 1:
            raise TypeError("the class's methods are written in more than one file")
        return "python", [f.__code__ for f in functions], [], files.pop()
    if isinstance(target, (types.WrapperDescriptorType, types.MethodWrapperType)):
        raise TypeError("slot wrappers and method-wrappers are reached through syntax, whose calls the runner can't observe")
    function = inspect.unwrap(target)
    if inspect.ismethod(function):
        function = function.__func__
    if inspect.isfunction(function):
        return "python", [function.__code__], [], function.__code__.co_filename
    if isinstance(target, types.BuiltinFunctionType) and isinstance(target.__self__, type):
        # A C class method, which each access binds anew, is its descriptor.
        descriptor = inspect.getattr_static(target.__self__, target.__name__, None)
        if isinstance(descriptor, types.ClassMethodDescriptorType):
            target = descriptor
    if isinstance(target, (types.MethodDescriptorType, types.ClassMethodDescriptorType)):
        if target.__name__.startswith("__") and target.__name__.endswith("__"):
            raise TypeError("a C special method is reached through syntax, whose calls the runner can't observe")
        return "c", [], [target], c_origin(target.__objclass__)
    if isinstance(target, types.BuiltinFunctionType):
        owner = target.__self__
        if inspect.ismodule(owner) and c_module(owner):
            return "c", [], [target], getattr(owner, "__file__", None)
        # A C method bound to an instance, as random.random is, is its type's.
        descriptor = inspect.getattr_static(type(owner), target.__name__, None)
        if owner is not None and not inspect.ismodule(owner) and isinstance(descriptor, types.MethodDescriptorType):
            return "c", [], [target], c_origin(descriptor.__objclass__)
        raise TypeError("the runner can't find the C module that defines the operation")
    raise TypeError("the operation is neither a Python function or class nor a C function")


def c_module(module):
    """Whether a module is written in C: built into the interpreter, or an extension."""
    from importlib import machinery
    filename = getattr(module, "__file__", None)
    if not isinstance(filename, str):
        return getattr(module, "__name__", None) in sys.builtin_module_names
    return filename.endswith(tuple(machinery.EXTENSION_SUFFIXES))


def c_origin(cls):
    """The file of the C module that defines a type, or None when the
    interpreter has it built in."""
    module = sys.modules.get(cls.__module__)
    if module is None or not c_module(module):
        # A Python module's C type comes from a C module it imported, as
        # decimal's Decimal comes from _decimal: the one that holds it.
        holders = []
        for candidate in list(sys.modules.values()):
            try:
                if inspect.ismodule(candidate) and c_module(candidate) and getattr(candidate, cls.__name__, None) is cls:
                    holders.append(candidate)
            except Exception:
                continue
        if len({getattr(m, "__file__", None) for m in holders}) != 1:
            raise TypeError("the runner can't find the one C module that defines %s" % cls.__qualname__)
        module = holders[0]
    return getattr(module, "__file__", None)


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
    codes, callables, receivers = [], [], {}
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
            name = spec["module"] + "." + spec["symbol"]
            if spec["kind"] == "standard_library" and spec["module"].split(".")[0] not in sys.stdlib_module_names:
                raise LookupError("%s isn't in the runtime's standard library" % spec["module"])
            target = importlib.import_module(spec["module"])
            for part in spec["symbol"].split("."):
                target = getattr(target, part)
            form, codes, callables, origin = boundary_implementation(target)
            if inspect.isclass(target):
                receivers = {code: target for code in codes}
            record["callable"] = form
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
        record["callable"], codes, callables, receivers = "", [], [], {}
    return record, codes, callables, receivers


def boundary_called(function):
    """Whether a C call is the bound callable: itself, or a method descriptor
    the receiver's own type resolves to it, never a same-named override."""
    for target in boundary_c:
        if function is target:
            return True
        if not isinstance(target, (types.MethodDescriptorType, types.ClassMethodDescriptorType)):
            continue
        bound = getattr(function, "__self__", None)
        if bound is None or getattr(function, "__name__", None) != target.__name__:
            continue
        owner = bound if isinstance(target, types.ClassMethodDescriptorType) else type(bound)
        if isinstance(owner, type) and inspect.getattr_static(owner, target.__name__, None) is target:
            return True
    return False


def boundary_receiver(frame, target):
    """Whether a class's method was called on the class, a subclass or an
    instance: methods that share code with other classes count only there."""
    code = frame.f_code
    value = frame.f_locals.get(code.co_varnames[0]) if code.co_argcount > 0 else None
    return value is target or isinstance(value, target) or isinstance(value, type) and issubclass(value, target)


def unittest_frame(frame):
    """Whether a frame runs unittest's code or the runner's own."""
    if frame.f_globals is runner_globals:
        return True
    name = frame.f_globals.get("__name__") or ""
    return name == "unittest" or name.startswith("unittest.")


def handed_to(callee):
    """Whether the test handed this callable to an assertion to call."""
    if isinstance(callee, types.CodeType):
        return any(callee is code for code in handed[0])
    return any(callee is value for value in handed[1])


def handed_codes(value):
    """The Python code that runs first when unittest calls a handed callable:
    a function's own, a class's constructor, an instance's __call__, or the
    function a wrapper written in C calls."""
    import functools
    if inspect.isclass(value):
        found = [inspect.getattr_static(value, "__new__", None), inspect.getattr_static(value, "__init__", None),
                 inspect.getattr_static(type(value), "__call__", None)]
    elif inspect.isfunction(value) or inspect.ismethod(value):
        found = [value]
    else:
        found = [inspect.getattr_static(type(value), "__call__", None), inspect.getattr_static(value, "__wrapped__", None),
                 value.func if isinstance(value, functools.partial) else None]
    codes = []
    for function in found:
        if isinstance(function, (staticmethod, classmethod)):
            function = function.__func__
        if inspect.ismethod(function):
            function = function.__func__
        if inspect.isfunction(function):
            codes.append(function.__code__)
    return codes


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
            if depth[0] == 0:
                # A callable the test hands an assertion, as to
                # assertRaises(E, f, x), is the test's own call when
                # unittest makes it.
                for value in list(args[1:]) + list(kwargs.values()):
                    if callable(value):
                        handed[0].extend(handed_codes(value))
                        handed[1].append(value)
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
                # Only the application's own source compiles anew; any other
                # implementation is the very code object resolved.
                if (known is not None and (known is code or boundary_equal and known == code)
                        and (known not in boundary_receivers or boundary_receiver(frame, boundary_receivers[known]))
                        and self.by_test(frame.f_back, code)):
                    self.pending["boundary_calls"] += 1
        elif event == "c_call" and boundary_c and boundary_called(arg) and self.by_test(frame, arg):
            self.pending["boundary_calls"] += 1

    def by_test(self, caller, callee):
        """Whether the test method made this call, directly or through code it
        called: a chain to its frame through unittest's or the runner's code
        counts only where unittest calls a callable the test handed it."""
        allowed, below, f = False, callee, caller
        while f is not None:
            if f.f_code is self.method_code:
                return True
            if unittest_frame(f):
                if not allowed:
                    if not handed_to(below):
                        return False
                    allowed = True
            else:
                allowed = False
            below, f = f.f_code, f.f_back
        return False

    def startTest(self, test):
        super().startTest(test)
        self.current = test
        self.pending, self.method_code = self.row(test)
        if boundary is not None:
            assertions[0] = self.pending["assertions"]
            handed[0], handed[1] = [], []
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
    resolved, codes, callables, receivers = resolve_boundary(boundary)
    boundary_python.update(((code.co_filename, code.co_firstlineno, code.co_name), code) for code in codes)
    boundary_receivers.update(receivers)
    boundary_equal = boundary["kind"] == "application"
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
