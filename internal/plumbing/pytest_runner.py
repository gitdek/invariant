"""Factory-owned pytest observations, separate from both diagnostic streams."""

import ast
import dis
import hashlib
import importlib
import inspect
import json
import os
from pathlib import Path
import stat
import sys
import types


def digest(data):
    return "sha256:" + hashlib.sha256(data).hexdigest()


def regular(path, root):
    path = Path(path).absolute()
    path.relative_to(root)
    for parent in (path, *path.parents):
        info = parent.lstat()
        if stat.S_ISLNK(info.st_mode):
            raise ValueError("linked source or configuration")
        if parent == root:
            break
    info = path.stat()
    if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1:
        raise ValueError("source or configuration is not a single regular file")
    return path.read_bytes()


def configuration(root, selected=None):
    files = {}
    for directory, dirs, names in os.walk(root, followlinks=False):
        dirs[:] = sorted(d for d in dirs if d not in (".git", "__pycache__", ".pytest_cache"))
        for name in sorted(names):
            if name in ("conftest.py", "pytest.ini", ".pytest.ini", "tox.ini", "setup.cfg", "pyproject.toml"):
                path = Path(directory) / name
                files[str(path.relative_to(root))] = digest(regular(path, root))
    if selected is not None:
        path = Path(selected).absolute()
        files[str(path.relative_to(root))] = digest(regular(path, root))
    return digest(json.dumps(files, sort_keys=True).encode())


def configuration_file(root, argv):
    for index, arg in enumerate(argv):
        if arg == "-c":
            return root / argv[index + 1]
        if arg.startswith("-c="):
            return root / arg[3:]
    return None


def definition(tree, selector):
    declaration = selector.split("::", 1)[1].split("[", 1)[0].split("::")
    body = tree.body
    for index, name in enumerate(declaration):
        matches = [node for node in body if isinstance(node, (ast.ClassDef, ast.FunctionDef, ast.AsyncFunctionDef)) and node.name == name]
        if len(matches) != 1:
            raise ValueError("selected declaration is absent or ambiguous")
        node = matches[0]
        if index < len(declaration) - 1:
            if not isinstance(node, ast.ClassDef) or node.bases or node.decorator_list:
                raise ValueError("unsupported selected test class")
            body = node.body
        elif not isinstance(node, ast.FunctionDef):
            raise ValueError("selection needs an ordinary test function or method")
    return node


def missing_api(error, source, tree, function, root, selector):
    # ImportError text is never attribution. Require the interpreter's missing
    # member exception at this exact import, a loaded local production module,
    # and an unshadowed direct call in the selected function's own body.
    if "[" in selector or function.decorator_list or error is None:
        return ""
    seen = set()
    while type(error) is not ImportError:
        if id(error) in seen or type(error).__name__ != "CollectError" or type(error).__module__ != "_pytest.nodes":
            return ""
        seen.add(id(error))
        error = error.__cause__ or error.__context__
        if error is None:
            return ""
    module = sys.modules.get(error.name)
    if module is None or not getattr(module, "__file__", None) or "__getattr__" in vars(module):
        return ""
    module_path = Path(module.__file__).absolute()
    if module_path.suffix != ".py" or module_path == source or module_path.name == "conftest.py" or module_path.name.startswith("test_") or module_path.name.endswith("_test.py"):
        return ""
    try:
        regular(module_path, root)
        if Path(error.path).absolute() != module_path or importlib.import_module(error.name) is not module:
            return ""
    except (TypeError, ValueError, OSError, ImportError):
        return ""
    trace = error.__traceback__
    locations = []
    while trace is not None:
        locations.append((Path(trace.tb_frame.f_code.co_filename).absolute(), trace.tb_lineno))
        trace = trace.tb_next
    if not locations or locations[-1][0] != source:
        return ""
    line = locations[-1][1]
    imports = [node for node in ast.walk(tree) if isinstance(node, ast.ImportFrom) and node.lineno == line]
    if len(imports) != 1:
        return ""
    node = imports[0]
    if node.module != error.name or node.level or len(node.names) != 1:
        return ""
    imported = node.names[0]
    name = imported.asname or imported.name
    if imported.name == "*" or imported.name in vars(module):
        return ""
    # Only a top-level import or a direct statement in this selected body is
    # admitted. Helpers, neighbors, nested scopes and conditional imports cannot
    # lend the selected test their failure.
    if node not in tree.body and node not in function.body:
        return ""
    for scope in (tree.body, function.body):
        for statement in scope:
            if statement is node:
                continue
            if isinstance(statement, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
                if statement.name == name:
                    return ""
                continue
            for child in ast.walk(statement):
                if isinstance(child, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)) and child.name == name:
                    return ""
                if isinstance(child, ast.Name) and child.id == name and isinstance(child.ctx, (ast.Store, ast.Del)):
                    return ""
                # Exception targets and pattern captures store bindings as
                # strings, rather than Name nodes with a Store context.
                if type(child).__name__ in ("ExceptHandler", "MatchAs", "MatchStar") and child.name == name:
                    return ""
                if type(child).__name__ == "MatchMapping" and child.rest == name:
                    return ""
                if isinstance(child, (ast.Import, ast.ImportFrom)):
                    if any((alias.asname or alias.name.split(".")[0]) == name for alias in child.names):
                        return ""
    arguments = function.args.posonlyargs + function.args.args + function.args.kwonlyargs
    arguments += [arg for arg in (function.args.vararg, function.args.kwarg) if arg is not None]
    if any(arg.arg == name for arg in arguments):
        return ""
    def direct_calls(nodes):
        for child in nodes:
            if isinstance(child, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef, ast.Lambda)):
                continue
            yield child
            yield from direct_calls(ast.iter_child_nodes(child))
    if not any(isinstance(child, ast.Call) and isinstance(child.func, ast.Name) and child.func.id == name for child in direct_calls(function.body)):
        return ""
    return "selected test directly uses missing local production API " + error.name + "." + imported.name


class FunctionCalls:
    # A runtest wrapper can swallow or replace the function's exception. Trace
    # the pinned frame itself; a return event also fires on exception unwind,
    # so only a return instruction establishes a normal function completion.
    def __init__(self, code):
        self.code = code
        self.calls = 0
        self.errors = []
        self.pending = {}
        self.intact = False
        self.observer = self.trace

    def trace(self, frame, event, arg):
        if frame.f_code is not self.code:
            return None
        if event == "call":
            self.calls += 1
            self.pending[frame] = None
        elif event == "exception":
            self.pending[frame] = arg[1]
        elif event == "return":
            error = self.pending.pop(frame)
            if dis.opname[frame.f_code.co_code[frame.f_lasti]] in ("RETURN_VALUE", "RETURN_CONST"):
                error = None
            elif error is None:
                error = ValueError("function exit has no observed exception")
            self.errors.append(error)
        return self.observer

    def __enter__(self):
        self.previous = sys.gettrace()
        sys.settrace(self.observer)
        return self

    def __exit__(self, *exc):
        self.intact = sys.gettrace() is self.observer and not self.pending and len(self.errors) == self.calls
        sys.settrace(self.previous)


def main():
    with open(sys.argv[1], encoding="utf-8") as file:
        invocation = json.load(file)
    endpoint = os.environ.pop("INVARIANT_PYTEST_EVENTS")
    events = open(endpoint, "wb", buffering=0)
    # Neither subprocesses nor diagnostics inherit the framework endpoint.
    inherited = int(endpoint.rsplit("/", 1)[1])
    if inherited != events.fileno():
        os.close(inherited)
    os.set_inheritable(events.fileno(), False)
    binding = invocation["Binding"]
    sequence = 0
    def emit(event, **fields):
        nonlocal sequence
        sequence += 1
        events.write((json.dumps(dict(Binding=binding, Seq=sequence, Event=event, **fields), separators=(",", ":")) + "\n").encode())
    root = Path.cwd()
    source = root / invocation["File"]
    emit("begin")
    code = 2
    try:
        data = regular(source, root)
        if digest(data) != binding["SourceDigest"]:
            raise ValueError("acceptance source differs from pinned bytes")
        config_file = configuration_file(root, invocation["Argv"])
        initial_config = configuration(root, config_file)
        tree = ast.parse(data, filename=str(source))
        function = definition(tree, binding["Selector"])
        # Resolve pytest from the prepared image before admitting candidate
        # imports. No package installation or fallback is attempted here.
        import pytest
        from _pytest.assertion.rewrite import rewrite_asserts
        sys.path.insert(0, str(root))

        def outcome(error):
            if error is None:
                return "passed"
            if isinstance(error, pytest.skip.Exception):
                return "skipped"
            if isinstance(error, AssertionError):
                return "assertion"
            return "setup"

        class Observations:
            def __init__(self):
                self.raw = {}
                self.reports = {}
                self.failures = []
                self.attribution = []
                self.code = None
                self.config = None
                self.config_path = None

            def check_source(self):
                if digest(regular(source, root)) != binding["SourceDigest"]:
                    raise ValueError("acceptance source changed during pytest")

            def check_item(self, item):
                self.check_source()
                obj = item.obj
                line = min([function.lineno] + [node.lineno for node in function.decorator_list])
                code = getattr(obj, "__code__", None)
                if type(item) is not pytest.Function or item.nodeid != binding["Selector"] or Path(item.path).absolute() != source or code is None or code != self.code or Path(code.co_filename).absolute() != source or code.co_firstlineno != line or code.co_name != function.name or inspect.iscoroutinefunction(obj):
                    raise ValueError("collected item differs from the exact pinned function")
                declaration = binding["Selector"].split("::", 1)[1].split("[", 1)[0].split("::")
                parent = item.getparent(pytest.Class)
                if (len(declaration) == 2 and (parent is None or parent.name != declaration[0])) or (len(declaration) == 1 and parent is not None):
                    raise ValueError("collected class differs from selection")
                return code

            @pytest.hookimpl(wrapper=True, tryfirst=True)
            def pytest_sessionstart(self, session):
                result = yield
                emit("session_start")
                return result

            def pytest_configure(self, config):
                options = config.option
                if any(getattr(options, name, False) for name in ("collectonly", "pyargs", "keyword", "markexpr", "deselect", "numprocesses")):
                    raise pytest.UsageError("effective pytest configuration changes individual selection")
                if configuration(root, config.inipath) != initial_config:
                    raise ValueError("effective pytest configuration differs from initial files")
                self.config, self.config_path = config, config.inipath
                # Match actual executable code to a fresh compilation of the
                # pinned bytes under pytest's own assertion policy. Matching
                # filenames alone would admit substituted functions or stale
                # candidate bytecode caches with the same name and line.
                expected = ast.parse(data, filename=str(source))
                if config.getoption("assertmode") == "rewrite":
                    rewrite_asserts(expected, data, str(source), config)
                code = compile(expected, str(source), "exec", dont_inherit=True)
                declaration = binding["Selector"].split("::", 1)[1].split("[", 1)[0].split("::")
                for name in declaration:
                    matches = [value for value in code.co_consts if isinstance(value, types.CodeType) and value.co_name == name]
                    if len(matches) != 1:
                        raise ValueError("ambiguous compiled pinned declaration")
                    code = matches[0]
                self.code = code

            @pytest.hookimpl(wrapper=True, tryfirst=True)
            def pytest_exception_interact(self, node, call, report):
                result = yield
                if report.when == "collect" and Path(node.path).absolute() == source:
                    reason = missing_api(call.excinfo.value if call.excinfo else None, source, tree, function, root, binding["Selector"])
                    if reason:
                        self.attribution.append(reason)
                return result

            @pytest.hookimpl(wrapper=True, tryfirst=True)
            def pytest_collectreport(self, report):
                result = yield
                if not report.passed:
                    self.failures.append((report.nodeid, report.outcome))
                return result

            @pytest.hookimpl(wrapper=True, tryfirst=True)
            def pytest_collection_finish(self, session):
                result = yield
                self.check_source()
                for plugin in session.config.pluginmanager._conftest_plugins:
                    regular(plugin.__file__, root)
                nodes = [item.nodeid for item in session.items]
                kind, reason = "runtime", ""
                if self.failures:
                    kind, reason = "setup", "pytest import or collection failed without selected API attribution"
                    if self.failures == [(invocation["File"], "failed")] and len(self.attribution) == 1 and not nodes:
                        kind, reason = "compile", self.attribution[0]
                elif nodes == [binding["Selector"]]:
                    self.check_item(session.items[0])
                emit("collection", Nodes=nodes, Outcome=kind, Reason=reason)
                if kind == "runtime" and nodes != [binding["Selector"]]:
                    raise pytest.UsageError("collection must contain exactly the literal selected node")
                return result

            @pytest.hookimpl(wrapper=True, tryfirst=True)
            def pytest_runtest_setup(self, item):
                error = None
                try:
                    self.check_item(item)
                    result = yield
                    return result
                except BaseException as caught:
                    error = caught
                    raise
                finally:
                    self.raw["setup"] = outcome(error)
                    emit("phase", Node=item.nodeid, Phase="setup", Outcome=outcome(error), Xfail=bool(list(item.iter_markers("xfail"))))

            @pytest.hookimpl(wrapper=True, tryfirst=True)
            def pytest_runtest_call(self, item):
                code = self.check_item(item)
                calls = FunctionCalls(code)
                error = None
                try:
                    with calls:
                        result = yield
                    return result
                except BaseException as caught:
                    error = caught
                    raise
                finally:
                    reason = missing_api(error, source, tree, function, root, binding["Selector"])
                    actual = "compile" if reason else outcome(error)
                    if not calls.intact or len(calls.errors) != 1 or calls.errors[0] is not error:
                        actual, reason = "conflicting", "function outcome differs from pytest call hooks"
                    self.raw["call"] = actual
                    emit("phase", Node=item.nodeid, Phase="call", Outcome=actual, Reason=reason, Calls=calls.calls if calls.intact else -1, Xfail=bool(list(item.iter_markers("xfail"))))

            @pytest.hookimpl(wrapper=True, tryfirst=True)
            def pytest_runtest_teardown(self, item):
                error = None
                try:
                    result = yield
                    return result
                except BaseException as caught:
                    error = caught
                    raise
                finally:
                    self.raw["teardown"] = outcome(error)
                    emit("phase", Node=item.nodeid, Phase="teardown", Outcome=outcome(error))

            @pytest.hookimpl(wrapper=True, tryfirst=True)
            def pytest_runtest_makereport(self, item, call):
                report = yield
                actual = outcome(call.excinfo.value if call.excinfo else None)
                if call.when == "call" and missing_api(call.excinfo.value if call.excinfo else None, source, tree, function, root, binding["Selector"]):
                    actual = "compile"
                if actual != self.raw.get(call.when):
                    actual = "conflicting"
                self.reports[call.when] = actual
                emit("report", Node=report.nodeid, Phase=call.when, Outcome=actual, Report=report.outcome, Xfail=hasattr(report, "wasxfail"))
                return report

            @pytest.hookimpl(wrapper=True, tryfirst=True)
            def pytest_runtest_logreport(self, report):
                result = yield
                emit("logreport", Node=report.nodeid, Phase=report.when, Outcome=self.reports.get(report.when, "absent"), Report=report.outcome, Xfail=hasattr(report, "wasxfail"))
                return result

            @pytest.hookimpl(wrapper=True, tryfirst=True)
            def pytest_sessionfinish(self, session, exitstatus):
                result = yield
                self.check_source()
                emit("session_finish", ExitCode=int(session.exitstatus))
                return result

        observations = Observations()
        code = int(pytest.main(invocation["Argv"], plugins=[observations]))
        if observations.config is not None and observations.config.inipath != observations.config_path:
            raise ValueError("effective pytest configuration path changed during execution")
        emit("finish", ExitCode=code, SourceDigest=digest(regular(source, root)), ConfigDigest=configuration(root, observations.config_path or config_file), InitialConfigDigest=initial_config)
    except BaseException as error:
        emit("setup", Reason=type(error).__name__ + ": " + str(error))
    finally:
        events.close()
    return code


if __name__ == "__main__":
    if sys.argv[1] == "--configuration":
        # The host runs this code with -I -S before and after the workload,
        # without pytest, candidate imports or candidate-owned helper files.
        invocation = json.loads(sys.argv[2])
        root = Path.cwd()
        print(configuration(root, configuration_file(root, invocation["Argv"])))
    else:
        sys.exit(main())
