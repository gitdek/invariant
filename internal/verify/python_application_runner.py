"""Controlled unittest startup and per-test observations for a prepared session."""

import ast
from collections import Counter
import hashlib
from importlib.machinery import PathFinder
import json
import os
import sys
import traceback
import types
import unittest


def lexical_codes(filename):
    """Map compiled lexical locations to the inventory's qualified symbols."""
    with open(filename, "rb") as source:
        raw = source.read()
    tree = ast.parse(raw, filename)
    locations = {}

    def declarations(node):
        for child in ast.iter_child_nodes(node):
            if isinstance(child, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
                yield child
            else:
                yield from declarations(child)

    def namespace(node, prefix):
        children = sorted(declarations(node), key=lambda d: (d.lineno, d.col_offset))
        counts = Counter(d.name for d in children)
        ordinals = Counter()
        for child in children:
            ordinals[child.name] += 1
            symbol = prefix + child.name
            if counts[child.name] > 1:
                symbol += "[" + str(ordinals[child.name]) + "]"
            if isinstance(child, ast.ClassDef):
                namespace(child, symbol + ".")
            else:
                first = min([child.lineno] + [d.lineno for d in child.decorator_list])
                locations[(first, child.name)] = symbol
                namespace(child, symbol + ".<locals>.")

    namespace(tree, "")
    codes = {}

    def collect(code):
        for value in code.co_consts:
            if isinstance(value, types.CodeType):
                symbol = locations.get((value.co_firstlineno, value.co_name))
                if symbol is not None:
                    codes[value] = symbol
                collect(value)

    collect(compile(tree, filename, "exec", dont_inherit=True))
    return "sha256:" + hashlib.sha256(raw).hexdigest(), codes


def main():
    with open(sys.argv[1], encoding="utf-8") as source:
        request = json.load(source)
    if sys.argv[2:4] != ["-m", "unittest"] or not sys.argv[4:]:
        raise ValueError("explicit unittest selections required")
    selections = sys.argv[4:]
    declaration = request["declaration"]
    filename = os.path.join("/src", declaration["source"])
    source_hash, codes = lexical_codes(filename)
    if source_hash != declaration["source_sha256"]:
        raise ValueError("stale selected source")

    trusted = list(sys.path) + ["/runtime"]
    application = ["/src", os.path.dirname(os.path.join("/src", request["driver"]))]
    sys.path[:] = list(dict.fromkeys(trusted + application + [request["library"]]))

    # A namespace in a higher-priority group must beat a regular package in
    # a lower group, including when resolving children of a namespace.
    class ApplicationFinder:
        @classmethod
        def find_spec(cls, fullname, path=None, target=None):
            groups = [[], [], []]
            for entry in sys.path if path is None else path:
                absolute = os.path.abspath(entry)
                if any(absolute == root or absolute.startswith(root + "/") for root in trusted):
                    groups[0].append(entry)
                elif any(absolute == root or absolute.startswith(root + "/") for root in application):
                    groups[1].append(entry)
                else:
                    groups[2].append(entry)
            for group in groups:
                spec = PathFinder.find_spec(fullname, group, target)
                if spec is not None:
                    return spec
            return None

    sys.meta_path[:] = [ApplicationFinder if finder is PathFinder else finder for finder in sys.meta_path]
    sys.argv = ["unittest"] + selections

    # This file is independent of both application stdout and unittest's
    # display stream. Flush each record so interruption retains earlier tests.
    with open("/output/events.jsonl", "x", encoding="utf-8") as events:
        count = 0

        def emit(record):
            events.write(json.dumps(record, ensure_ascii=True, separators=(",", ":")) + "\n")
            events.flush()

        emit({"type": "begin", "version": 1, "key": request["key"],
              "snapshot_sha256": request["snapshot_sha256"],
              "runner_sha256": request["runner_sha256"]})

        class Observations(unittest.TestResult):
            def __init__(self):
                super().__init__()
                self.current = None
                self.method = None

            def identity(self, test):
                method = getattr(test, getattr(test, "_testMethodName", ""), None)
                code = getattr(method, "__code__", None)
                check, digest = "", ""
                if code is not None and os.path.abspath(code.co_filename) == filename:
                    digest = source_hash
                    if codes.get(code) == declaration["symbol"]:
                        check = declaration["id"]
                elif code is not None:
                    location = os.path.abspath(code.co_filename)
                    if location.startswith("/src/") and os.path.isfile(location):
                        with open(location, "rb") as source:
                            digest = "sha256:" + hashlib.sha256(source.read()).hexdigest()
                return code, check, digest

            def row(self, test):
                code, check, digest = self.identity(test)
                return code, {"type": "test", "id": test.id(), "check": check,
                              "source_sha256": digest, "exercised": False,
                              "outcome": "not_run", "diagnostics": []}

            def startTest(self, test):
                super().startTest(test)
                self.method, self.current = self.row(test)

            def entered(self, frame, event, arg):
                if event == "call" and self.current is not None and frame.f_code is self.method:
                    self.current["exercised"] = True

            def record(self, test, outcome, diagnostics=()):
                nonlocal count
                if self.current is None:
                    _, row = self.row(test)
                    row["outcome"] = outcome
                    row["diagnostics"] = list(diagnostics)
                    emit(row)
                    count += 1
                else:
                    # A later teardown error must not be hidden by success or
                    # a subtest failure. Keep one final observation per test.
                    priorities = {"not_run": 0, "passed": 1, "skipped": 2,
                                  "expected_failure": 3, "unexpected_success": 4,
                                  "failed": 5, "error": 6}
                    if priorities[outcome] > priorities[self.current["outcome"]]:
                        self.current["outcome"] = outcome
                    self.current["diagnostics"].extend(diagnostics)

            def addSuccess(self, test):
                super().addSuccess(test)
                self.record(test, "passed")

            def addFailure(self, test, err):
                super().addFailure(test, err)
                self.record(test, "failed", traceback.format_exception(*err))

            def addError(self, test, err):
                super().addError(test, err)
                self.record(test, "error", traceback.format_exception(*err))

            def addSkip(self, test, reason):
                super().addSkip(test, reason)
                self.record(test, "skipped", [reason])

            def addExpectedFailure(self, test, err):
                super().addExpectedFailure(test, err)
                self.record(test, "expected_failure", traceback.format_exception(*err))

            def addUnexpectedSuccess(self, test):
                super().addUnexpectedSuccess(test)
                self.record(test, "unexpected_success", ["expected failure passed"])

            def addSubTest(self, test, subtest, err):
                super().addSubTest(test, subtest, err)
                if err is not None:
                    outcome = "failed" if issubclass(err[0], test.failureException) else "error"
                    self.record(test, outcome, traceback.format_exception(*err))

            def stopTest(self, test):
                nonlocal count
                emit(self.current)
                count += 1
                self.current, self.method = None, None
                super().stopTest(test)

        result = Observations()
        suite = unittest.TestLoader().loadTestsFromNames(selections)
        sys.setprofile(result.entered)
        try:
            suite.run(result)
        finally:
            sys.setprofile(None)
        emit({"type": "end", "count": count})
        return 0 if result.wasSuccessful() else 1


if __name__ == "__main__":
    sys.exit(main())
