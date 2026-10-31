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
        return row, code

    def profile(self, frame, event, arg):
        if event == "call" and frame.f_code is self.method_code and self.pending is not None:
            self.pending["exercised"] = True

    def startTest(self, test):
        super().startTest(test)
        self.current = test
        self.pending, self.method_code = self.row(test)
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
    callers = (invariant_observe.Collector.write.__code__, invariant_observe.Collector.declare.__code__, invariant_observe.run.__code__)
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
if request.get("legacy"):
    suite = unittest.defaultTestLoader.discover(root, pattern="test_*.py", top_level_dir=root)
else:
    suite = unittest.defaultTestLoader.loadTestsFromNames(selections)
result = Observations()
suite.run(result)
emit(dict(type="end", count=count))
events.close()
sys.exit(0 if result.wasSuccessful() else 1)
