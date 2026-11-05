"""Factory-owned pytest integration. Diagnostics never enter the event pipe."""
import ast
import atexit
import hashlib
import json
import os
from pathlib import Path
import stat
import sys


def digest(data):
    return "sha256:" + hashlib.sha256(data).hexdigest()


def regular_bytes(file):
    file = Path(file).absolute()
    for parent in (file, *file.parents):
        info = parent.lstat()
        if stat.S_ISLNK(info.st_mode):
            raise ValueError("linked source or configuration")
    info = file.stat()
    if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1:
        raise ValueError("source or configuration is not a regular file")
    return file.read_bytes()


def configuration(root, argv):
    names = {"conftest.py", "pytest.ini", ".pytest.ini", "pyproject.toml", "tox.ini", "setup.cfg"}
    files = {}
    for directory, dirs, leaves in os.walk(root):
        dirs[:] = sorted(d for d in dirs if d not in {".git", "__pycache__", ".pytest_cache", ".venv", "venv"})
        for name in sorted(names.intersection(leaves)):
            file = Path(directory) / name
            files[str(file)] = digest(regular_bytes(file))
    # Pytest can discover configuration and conftest in ancestors of the part.
    for parent in Path(root).parents:
        for name in names:
            file = parent / name
            if file.exists() or file.is_symlink():
                files[str(file)] = digest(regular_bytes(file))
    for index, arg in enumerate(argv):
        if arg == "-c":
            file = root / argv[index + 1]
        elif arg.startswith("-c="):
            file = root / arg[3:]
        else:
            continue
        files[str(file)] = digest(regular_bytes(file))
    return digest(json.dumps(files, sort_keys=True).encode())


def declaration(tree, selector):
    names = selector.split("::")[1:]
    names[-1] = names[-1].split("[", 1)[0]
    body = tree.body
    for index, name in enumerate(names):
        matches = [n for n in body if isinstance(n, (ast.FunctionDef, ast.ClassDef)) and n.name == name]
        if len(matches) != 1:
            return None
        node = matches[0]
        if index < len(names) - 1 and not isinstance(node, ast.ClassDef):
            return None
        body = node.body
    return node if isinstance(node, ast.FunctionDef) else None


def unconditional_read(function, name):
    """Prove a read from entry, stopping at every uncertain evaluation.

    The result of an expression is (reads the name, safe to continue). Even
    a straight-line call before the read could raise or never return. Branches
    and delayed expressions never establish reachability by syntax alone.
    """
    def expression(node):
        if isinstance(node, ast.Name):
            return isinstance(node.ctx, ast.Load) and node.id == name, False
        if isinstance(node, ast.Constant):
            return False, True
        if isinstance(node, (ast.BoolOp, ast.IfExp, ast.Lambda, ast.ListComp,
                             ast.SetComp, ast.DictComp, ast.GeneratorExp,
                             ast.Await, ast.Yield, ast.YieldFrom, ast.NamedExpr)):
            return False, False
        if isinstance(node, ast.Call):
            # The callable is evaluated before its arguments, but an unrelated
            # lookup or call cannot establish unconditional use of this API.
            return expression(node.func)[0], False
        if isinstance(node, (ast.Attribute, ast.Subscript)):
            return expression(node.value)[0], False
        if isinstance(node, ast.Compare):
            return expression(node.left)[0], False
        if isinstance(node, (ast.BinOp, ast.UnaryOp)):
            first = node.left if isinstance(node, ast.BinOp) else node.operand
            return expression(first)[0], False
        if isinstance(node, (ast.List, ast.Tuple, ast.Set)):
            for element in node.elts:
                read, safe = expression(element)
                if read or not safe:
                    return read, False
            return False, True
        return False, False

    # A local binding anywhere in the body makes the imported name ambiguous.
    for node in ast.walk(function):
        if isinstance(node, ast.Name) and node.id == name and not isinstance(node.ctx, ast.Load):
            return False
        if isinstance(node, (ast.Global, ast.Nonlocal)) and name in node.names:
            return False
        if isinstance(node, ast.arg) and node.arg == name:
            return False
    for statement in function.body:
        if isinstance(statement, ast.Expr):
            value = statement.value
        elif isinstance(statement, ast.Assert):
            # A failing assertion before the use is also a control-flow barrier.
            return expression(statement.test)[0]
        elif isinstance(statement, ast.Assign):
            if not all(isinstance(n, ast.Name) and n.id != name for n in statement.targets):
                return False
            value = statement.value
        else:
            return False
        read, safe = expression(value)
        if read:
            return True
        if not safe:
            return False
    return False


def missing_api(error, source, file, selector, root):
    """Attribute only Python's direct missing from-import from a loaded module."""
    try:
        tree = ast.parse(source, filename=str(file))
        function = declaration(tree, selector)
        if function is None or function.decorator_list or "[" in selector:
            return None
        arguments = function.args
        args = arguments.posonlyargs + arguments.args + arguments.kwonlyargs
        if arguments.vararg or arguments.kwarg or arguments.defaults or arguments.kw_defaults:
            return None
        if [arg.arg for arg in args] not in ([], ["self"]):
            return None
        seen = set()
        while type(error) is not ImportError:
            if id(error) in seen:
                return None
            seen.add(id(error))
            error = error.__cause__ or error.__context__
            if error is None:
                return None
        frames = []
        tb = error.__traceback__
        while tb is not None:
            if Path(tb.tb_frame.f_code.co_filename).absolute() == file:
                frames.append((tb.tb_lineno, tb.tb_frame.f_code.co_name))
            tb = tb.tb_next
        if len(frames) != 1 or frames[0][1] != "<module>":
            return None
        imports = [node for node in tree.body if isinstance(node, ast.ImportFrom)
                   and node.lineno == frames[0][0] and not node.level and len(node.names) == 1]
        if len(imports) != 1:
            return None
        imp = imports[0]
        alias = imp.names[0]
        name = alias.asname or alias.name
        if alias.name == "*" or error.name != imp.module or not unconditional_read(function, name):
            return None
        for definition in ast.walk(tree):
            if isinstance(definition, (ast.FunctionDef, ast.AsyncFunctionDef)):
                for default in definition.args.defaults + [v for v in definition.args.kw_defaults if v is not None]:
                    ast.literal_eval(default)
                annotations = [a.annotation for a in definition.args.posonlyargs + definition.args.args + definition.args.kwonlyargs]
                annotations.append(definition.returns)
                if any(a is not None and not isinstance(a, (ast.Name, ast.Constant)) for a in annotations):
                    return None
        # Importing the missing name must supply the binding actually used by
        # the test. Rebinding it later could let future=None pass unchanged.
        for node in tree.body:
            if isinstance(node, (ast.Import, ast.ImportFrom)):
                if node is not imp:
                    for other in node.names:
                        bound = other.asname or other.name.split(".")[0]
                        if bound == name or other.name == "*":
                            return None
            elif isinstance(node, (ast.FunctionDef, ast.ClassDef)):
                if node.name == name or node.decorator_list:
                    return None
                if isinstance(node, ast.ClassDef):
                    if node.bases or node.keywords or any(not isinstance(n, (ast.FunctionDef, ast.Pass)) and not (isinstance(n, ast.Expr) and isinstance(n.value, ast.Constant)) for n in node.body):
                        return None
                    if any(isinstance(n, ast.FunctionDef) and n.decorator_list for n in node.body):
                        return None
            elif isinstance(node, ast.Assign):
                if not all(isinstance(n, ast.Name) and n.id != name and n.id != "__test__" for n in node.targets):
                    return None
                ast.literal_eval(node.value)
            elif not (isinstance(node, ast.Expr) and isinstance(node.value, ast.Constant)):
                return None
        module = sys.modules.get(imp.module)
        if module is None or alias.name in vars(module) or "__getattr__" in vars(module):
            return None
        origin = Path(vars(module).get("__file__", "")).absolute()
        expected = root.joinpath(*imp.module.split(".")).with_suffix(".py")
        if origin != expected or error.path != str(origin) or origin == file:
            return None
        spec = vars(module).get("__spec__")
        if spec is None or spec.name != imp.module or spec.origin != str(origin):
            return None
        if origin.name.startswith("test_") or origin.name.endswith("_test.py") or origin.name == "conftest.py":
            return None
        regular_bytes(origin)
        # A module that itself raised never completed its import and is absent
        # from sys.modules. Also exclude an exception originating in its body.
        tb = error.__traceback__
        while tb is not None:
            if Path(tb.tb_frame.f_code.co_filename).absolute() == origin:
                return None
            tb = tb.tb_next
        return {"Module": imp.module, "Name": alias.name, "File": str(origin.relative_to(root)), "Line": imp.lineno}
    except (AttributeError, TypeError, ValueError, OSError, SyntaxError):
        return None


def execute(request, emit):
    binding = request["Binding"]
    root = Path.cwd()
    file = root / request["File"]
    state = {"Collected": [], "Calls": 0, "Phases": [], "Session": 0,
             "ExitCode": -1, "Invalid": [], "Kind": "test", "Reason": "",
             "Import": None, "SourceDigest": "", "ConfigBefore": "", "ConfigAfter": ""}
    emit({"Event": "start", "Binding": binding})
    try:
        source = regular_bytes(file)
        state["SourceDigest"] = digest(source)
        if state["SourceDigest"] != binding["SourceDigest"]:
            raise ValueError("pinned pytest source changed before collection")
        state["ConfigBefore"] = configuration(root, request["Argv"])
        import pytest
        # Import the installed framework before admitting the part's imports.
        sys.path.insert(0, str(root))

        class Observer:
            code = None
            collection_errors = 0
            exceptions = ()

            @pytest.hookimpl(hookwrapper=True, tryfirst=True)
            def pytest_collection_finish(self, session):
                yield
                state["Collected"] = [item.nodeid for item in session.items]
                if state["Collected"] != [binding["Selector"]]:
                    state["Invalid"].append("collection must contain exactly the literal selected node ID")
                    return
                item = session.items[0]
                function = declaration(ast.parse(source), binding["Selector"])
                obj = getattr(item, "obj", None)
                code = getattr(obj, "__code__", None)
                if function is None or code is None or Path(code.co_filename).absolute() != file or code.co_name != function.name:
                    state["Invalid"].append("collected callable is not the pinned declaration")
                    return
                first = min([function.lineno] + [n.lineno for n in function.decorator_list])
                if code.co_firstlineno != first or Path(item.path).absolute() != file:
                    state["Invalid"].append("collected callable has a different source location")
                    return
                self.code = code
                if digest(regular_bytes(file)) != binding["SourceDigest"]:
                    state["Invalid"].append("pinned source changed during collection")

            @pytest.hookimpl(hookwrapper=True, tryfirst=True)
            def pytest_runtest_call(self, item):
                previous = sys.getprofile()
                previous_trace = sys.gettrace()
                exceptions = []
                self.exceptions = exceptions

                def profile(frame, event, arg):
                    if event == "call" and frame.f_code is self.code:
                        state["Calls"] += 1

                def trace(frame, event, arg):
                    if frame.f_code is self.code:
                        if event == "exception":
                            exceptions.append(arg[1])
                        return trace
                    return None

                sys.setprofile(profile)
                sys.settrace(trace)
                try:
                    yield
                finally:
                    if sys.getprofile() is not profile:
                        state["Invalid"].append("call observation was replaced")
                    if sys.gettrace() is not trace:
                        state["Invalid"].append("exception observation was replaced")
                    sys.setprofile(previous)
                    sys.settrace(previous_trace)

            @pytest.hookimpl(hookwrapper=True, tryfirst=True)
            def pytest_runtest_makereport(self, item, call):
                raw = "passed" if call.excinfo is None else "failed"
                exception = "" if call.excinfo is None else call.excinfo.type.__module__ + "." + call.excinfo.type.__qualname__
                if call.when == "call" and call.excinfo is not None and not any(call.excinfo.value is error for error in self.exceptions):
                    state["Invalid"].append("call report has no exception from the selected body")
                outcome = yield
                report = outcome.get_result()
                state["Phases"].append({"Node": item.nodeid, "When": call.when,
                                        "Raw": raw, "Outcome": report.outcome,
                                        "Exception": exception, "Xfail": hasattr(report, "wasxfail")})
                if report.nodeid != item.nodeid or report.when != call.when:
                    state["Invalid"].append("report identity changed")

            @pytest.hookimpl(tryfirst=True)
            def pytest_exception_interact(self, node, call, report):
                if report.when == "collect":
                    self.collection_errors += 1
                    attributed = missing_api(call.excinfo.value, source, file, binding["Selector"], root)
                    if self.collection_errors == 1 and attributed is not None and Path(node.path).absolute() == file:
                        state["Import"] = attributed
                    else:
                        state["Import"] = None
                    state["Kind"] = "setup"
                    state["Reason"] = "pytest import or collection failed"

            @pytest.hookimpl(tryfirst=True)
            def pytest_deselected(self, items):
                state["Invalid"].append("pytest deselected items")

            @pytest.hookimpl(hookwrapper=True, tryfirst=True)
            def pytest_sessionfinish(self, session, exitstatus):
                yield
                state["Session"] += 1

        observer = Observer()
        state["ExitCode"] = int(pytest.main(request["Argv"], plugins=[observer]))
        if observer.collection_errors == 1 and state["Import"] is not None:
            state["Kind"] = "compile"
            state["Reason"] = "selected test unconditionally uses missing local API " + state["Import"]["Module"] + "." + state["Import"]["Name"]
            # No callable can be collected until this particular import works.
            state["Invalid"] = [v for v in state["Invalid"] if v != "collection must contain exactly the literal selected node ID"]
        elif state["Session"] != 1 or state["ExitCode"] in (3, 4):
            state["Kind"] = "setup"
            state["Reason"] = "pytest or its configuration could not complete a session"
        state["ConfigAfter"] = configuration(root, request["Argv"])
        if digest(regular_bytes(file)) != binding["SourceDigest"]:
            state["Invalid"].append("pinned source changed during pytest")
        if state["ConfigBefore"] != state["ConfigAfter"]:
            state["Invalid"].append("pytest configuration changed during execution")
    except BaseException as error:
        state["Kind"] = "setup"
        state["Reason"] = type(error).__name__ + ": " + str(error)
        state["ExitCode"] = 4
    emit({"Event": "finish", "Binding": binding, "State": state})
    return state["ExitCode"]


def main():
    request = json.loads(sys.argv[1])
    endpoint = os.environ.pop("INVARIANT_PYTEST_EVENTS")
    stream = open(endpoint, "wb", buffering=0)
    pending = []
    root = Path.cwd()

    def write(event):
        data = json.dumps(event, separators=(",", ":")).encode() + b"\n"
        while data:
            data = data[stream.write(data):]

    def emit(event):
        if event["Event"] == "finish":
            pending.append(event)
        else:
            write(event)

    def finish():
        try:
            if len(pending) != 1:
                return
            event = pending[0]
            state = event["State"]
            try:
                state["ConfigAfter"] = configuration(root, request["Argv"])
                if state["ConfigBefore"] != state["ConfigAfter"] or digest(regular_bytes(root / request["File"])) != request["Binding"]["SourceDigest"]:
                    state["Invalid"].append("source or configuration changed at process shutdown")
            except BaseException as error:
                state["Invalid"].append("cannot recheck source or configuration: " + str(error))
            write(event)
        finally:
            stream.close()

    # Registered before framework or workload imports, so their exit handlers
    # run first. An abrupt exit cannot leave apparently finished evidence.
    atexit.register(finish)
    return execute(request, emit)


if __name__ == "__main__":
    sys.exit(main())
