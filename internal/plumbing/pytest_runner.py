"""Factory-owned pytest observations, separate from workload diagnostics."""
import ast
import fnmatch
import functools
import hashlib
import json
import os
from pathlib import Path
import stat
import sys


def regular_bytes(file):
    file = Path(file).absolute()
    for parent in (file, *file.parents):
        info = parent.lstat()
        if stat.S_ISLNK(info.st_mode):
            raise ValueError("source path contains a symlink")
    info = file.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1:
        raise ValueError("source is not a private regular file")
    return file.read_bytes()


def inert(expr):
    # Only literal evaluation can be assumed to finish before the API call.
    return isinstance(expr, ast.Constant) or (
        isinstance(expr, (ast.Tuple, ast.List)) and all(inert(x) for x in expr.elts))


def direct_call(expr, name):
    if isinstance(expr, ast.Call):
        return (isinstance(expr.func, ast.Name) and expr.func.id == name
                and all(inert(x) for x in expr.args)
                and all(x.arg is not None and inert(x.value) for x in expr.keywords))
    if isinstance(expr, ast.Compare):
        return direct_call(expr.left, name) or (
            inert(expr.left) and len(expr.comparators) == 1
            and direct_call(expr.comparators[0], name))
    if isinstance(expr, ast.BinOp):
        return direct_call(expr.left, name) or (inert(expr.left) and direct_call(expr.right, name))
    if isinstance(expr, ast.UnaryOp):
        return direct_call(expr.operand, name)
    # No branches, short circuiting, lazy bodies, comprehensions or callbacks.
    return False


def unconditional_use(function, name):
    if function.decorator_list or isinstance(function, ast.AsyncFunctionDef):
        return False
    for node in ast.walk(function):
        if isinstance(node, ast.Name) and isinstance(node.ctx, ast.Store) and node.id == name:
            return False
        if isinstance(node, ast.arg) and node.arg == name:
            return False
        if isinstance(node, (ast.Global, ast.Nonlocal)):
            return False
    for statement in function.body:
        if isinstance(statement, ast.Pass):
            continue
        if isinstance(statement, (ast.Expr, ast.Assert)):
            value = statement.value if isinstance(statement, ast.Expr) else statement.test
        elif isinstance(statement, ast.Assign) and all(isinstance(t, ast.Name) for t in statement.targets):
            value = statement.value
        elif isinstance(statement, ast.Return):
            return direct_call(statement.value, name)
        else:
            return False
        if direct_call(value, name):
            return True
        if not inert(value) or isinstance(statement, ast.Assert):
            return False
    return False


def inert_declarations(nodes, imported, selected):
    # Import failure stops module execution. Everything following that import
    # must therefore be inert enough to establish the selected declaration;
    # another import, initializer, decorator or rebinding is ambiguous setup.
    for node in nodes:
        if isinstance(node, ast.Pass) or isinstance(node, ast.Expr) and isinstance(node.value, ast.Constant):
            continue
        if isinstance(node, ast.Assign):
            if (not inert(node.value) or any(not isinstance(t, ast.Name) for t in node.targets)
                    or any(t.id in (imported, selected, "__test__") for t in node.targets)):
                return False
        elif isinstance(node, ast.FunctionDef):
            arguments = node.args
            if (node.name in (imported, "__init__", "__new__") or node.decorator_list
                    or node.returns is not None
                    or any(a.annotation is not None for a in (*arguments.posonlyargs, *arguments.args, *arguments.kwonlyargs))
                    or any(not inert(x) for x in (*arguments.defaults, *arguments.kw_defaults) if x is not None)):
                return False
        elif isinstance(node, ast.ClassDef):
            if (node.name == imported or node.bases or node.keywords or node.decorator_list
                    or not inert_declarations(node.body, imported, selected)):
                return False
        else:
            return False
    return True


def missing_api(exception, source, file, selector):
    """Attribute only an actual import of an absent member of a loaded module."""
    chain, seen = [], set()
    while exception is not None and id(exception) not in seen:
        seen.add(id(exception))
        chain.append(exception)
        exception = exception.__cause__ or exception.__context__
    imports = [e for e in chain if type(e) is ImportError]
    if len(imports) != 1 or any(isinstance(e, ModuleNotFoundError) for e in chain):
        return ""
    error = imports[0]
    module = sys.modules.get(error.name)
    if module is None or "__getattr__" in vars(module):
        return ""
    origin = vars(module).get("__file__")
    if not origin or not error.path or Path(origin).absolute() != Path(error.path).absolute():
        return ""
    origin = Path(origin).absolute()
    try:
        local = origin.relative_to(Path.cwd())
        regular_bytes(origin)
    except (ValueError, OSError):
        return ""
    if (origin == file or origin.suffix != ".py" or local.name == "conftest.py"
            or local.name.startswith("test_") or local.name.endswith("_test.py")):
        return ""
    traceback = error.__traceback__
    if traceback is None:
        return ""
    while traceback.tb_next is not None:
        traceback = traceback.tb_next
    if Path(traceback.tb_frame.f_code.co_filename).absolute() != file:
        return ""
    try:
        tree = ast.parse(source, filename=str(file))
    except SyntaxError:
        return ""
    declarations = [n for n in tree.body if isinstance(n, ast.ImportFrom)
                    and n.module == error.name and n.level == 0]
    if len(declarations) != 1:
        return ""
    declaration = declarations[0]
    if declaration.lineno != traceback.tb_lineno or len(declaration.names) != 1:
        return ""
    alias = declaration.names[0]
    if alias.name == "*" or alias.name in vars(module):
        return ""
    # Parameter groups cannot be established from a failed collection.
    names = selector.split("::")[1:]
    if any("[" in n for n in names) or len(names) not in (1, 2):
        return ""
    imported = alias.asname or alias.name
    if not inert_declarations(tree.body[tree.body.index(declaration)+1:], imported, names[-1]):
        return ""
    body = tree.body
    if len(names) == 2:
        classes = [n for n in body if isinstance(n, ast.ClassDef) and n.name == names[0]]
        if len(classes) != 1 or classes[0].bases or classes[0].decorator_list or classes[0].keywords:
            return ""
        body = classes[0].body
    functions = [n for n in body if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef)) and n.name == names[-1]]
    if len(functions) != 1:
        return ""
    arguments = functions[0].args
    args = arguments.posonlyargs + arguments.args
    if (arguments.defaults or arguments.kwonlyargs or arguments.vararg or arguments.kwarg
            or [a.arg for a in args] != (["self"] if len(names) == 2 else [])
            or not unconditional_use(functions[0], imported)):
        return ""
    return "selected test unconditionally calls missing local production API " + error.name + "." + alias.name


def run(request, emit):
    file = Path(request["File"]).absolute()
    binding = request["Binding"]
    selector = binding["Selector"]

    def recheck():
        source = regular_bytes(file)
        if "sha256:" + hashlib.sha256(source).hexdigest() != binding["SourceDigest"]:
            raise ValueError("pinned pytest source changed")
        return source

    source = recheck()
    # Resolve the prepared framework before exposing candidate imports. A local
    # pytest.py must not replace the framework that owns these observations.
    import pytest

    # Match the owning part's import context, independent of the private helper.
    sys.path.insert(0, str(Path.cwd()))

    def actual(call):
        if call.excinfo is None:
            return "passed"
        error = call.excinfo.value
        if isinstance(error, pytest.skip.Exception):
            return "skipped"
        if isinstance(error, pytest.xfail.Exception):
            return "xfail"
        if isinstance(error, AssertionError):
            return "assertion"
        return "error"

    class Observer:
        def __init__(self):
            self.item = None
            self.obj = None

        def pytest_sessionstart(self, session):
            emit("session_start")
            config = session.config
            # Configuration may add selection flags that the captured template
            # itself did not contain. They cannot widen or suppress this check.
            options = config.option
            if (options.keyword or options.markexpr or options.collectonly or options.pyargs
                    or getattr(options, "deselect", None) or getattr(options, "numprocesses", None)):
                emit("refused", Reason="pytest configuration changes exact selection")
            names = selector.split("[", 1)[0].split("::")[1:]
            if (not any(names[-1].startswith(p) or fnmatch.fnmatchcase(names[-1], p) for p in config.getini("python_functions"))
                    or len(names) == 2 and not any(names[0].startswith(p) or fnmatch.fnmatchcase(names[0], p) for p in config.getini("python_classes"))):
                emit("refused", Reason="pytest configuration excludes the selected declaration")

        def pytest_deselected(self, items):
            emit("refused", Reason="pytest deselected items")

        def pytest_collection_finish(self, session):
            recheck()
            ids = [i.nodeid for i in session.items]
            emit("collection", IDs=ids)
            if ids != [selector]:
                return
            item = session.items[0]
            obj = getattr(item, "obj", None)
            code = getattr(obj, "__code__", None)
            expected = selector.split("[", 1)[0].split("::")[-1]
            if (not isinstance(item, pytest.Function) or Path(item.path).absolute() != file
                    or code is None or Path(code.co_filename).absolute() != file
                    or code.co_name != expected or item.originalname != expected):
                emit("refused", Reason="collected item is not the pinned function or method")
                return
            self.item, self.obj = item, obj
            if item.get_closest_marker("xfail") is not None:
                emit("refused", Reason="xfail and xpass cannot supply acceptance")

        @pytest.hookimpl(hookwrapper=True, tryfirst=True)
        def pytest_runtest_protocol(self, item, nextitem):
            if item is not self.item or item.nodeid != selector or item.obj is not self.obj:
                emit("refused", Reason="runtime item differs from the exact collected function")
            emit("protocol_start", Node=item.nodeid)
            yield
            emit("protocol_end", Node=item.nodeid)

        @pytest.hookimpl(hookwrapper=True, tryfirst=True)
        def pytest_pyfunc_call(self, pyfuncitem):
            original = pyfuncitem.obj
            if pyfuncitem is not self.item or original is not self.obj:
                emit("refused", Reason="pytest changed the selected callable")

            @functools.wraps(original)
            def observed(*args, **kwargs):
                emit("body_start", Node=pyfuncitem.nodeid)
                outcome = "passed"
                try:
                    return original(*args, **kwargs)
                except BaseException as error:
                    outcome = ("skipped" if isinstance(error, pytest.skip.Exception) else
                               "xfail" if isinstance(error, pytest.xfail.Exception) else
                               "assertion" if isinstance(error, AssertionError) else "error")
                    raise
                finally:
                    emit("body_end", Node=pyfuncitem.nodeid, Actual=outcome)

            pyfuncitem.obj = observed
            try:
                yield
            finally:
                pyfuncitem.obj = original

        @pytest.hookimpl(hookwrapper=True, tryfirst=True)
        def pytest_runtest_makereport(self, item, call):
            observed = actual(call)
            reason = "" if call.excinfo is None else type(call.excinfo.value).__name__ + ": " + str(call.excinfo.value)
            outcome = yield
            report = outcome.get_result()
            emit("phase", Node=item.nodeid, When=call.when, Actual=observed,
                 Outcome=report.outcome, Xfail=hasattr(report, "wasxfail"), Reason=reason[:4096])

        def pytest_exception_interact(self, node, call, report):
            if call.when == "collect":
                reason = missing_api(call.excinfo.value, source, file, selector)
                emit("collect_error", Attributed=bool(reason), Reason=reason or "unattributed collection exception")

        @pytest.hookimpl(hookwrapper=True, tryfirst=True)
        def pytest_sessionfinish(self, session, exitstatus):
            yield
            recheck()
            emit("session_end", Code=int(session.exitstatus))

    code = int(pytest.main(request["Args"], plugins=[Observer()]))
    recheck()
    emit("finish", Code=code)
    return code


def main():
    request = json.loads(sys.argv[1])
    endpoint = os.environ.pop("INVARIANT_PYTEST_EVENTS")
    fd = int(endpoint.rsplit("/", 1)[-1])
    os.set_inheritable(fd, False)
    stream = os.fdopen(fd, "w", encoding="utf-8", buffering=1)
    sequence = 0

    def emit(event, **fields):
        nonlocal sequence
        sequence += 1
        stream.write(json.dumps(dict(Binding=request["Binding"], Seq=sequence,
                                     Event=event, **fields), ensure_ascii=True) + "\n")

    emit("begin")
    try:
        code = run(request, emit)
    except BaseException as error:
        emit("setup", Reason=type(error).__name__ + ": " + str(error)[:4096])
        code = 3
    stream.close()
    return code


if __name__ == "__main__":
    sys.exit(main())
