"""Sample selected calls without changing application source or functions.

A driver gets stateless handles. Every binding, read-back and continuity check
uses records only the collector holds, so nothing a driver is given can
substitute evidence. Code in the same process can still reach this module's
own objects, frames and closures; a driver that does is tampering with the
gate, which no in-process collector can rule out, and its bytes are bound to
every observation it makes.
"""

import ast
import _thread
import functools
import hashlib
import inspect
import math
import os
import random
import sys
import threading
import types


_collector = None


def guarded(method):
    @functools.wraps(method)
    def call(*args, **kwargs):
        try:
            return method(*args, **kwargs)
        except BaseException:
            _collector.failed = True
            raise
    return call


def scalar(value):
    if type(value) not in (str, bool, int, float, type(None)):
        raise ValueError("read-back requires immutable JSON scalars")
    if type(value) is float and not math.isfinite(value):
        raise ValueError("read-back requires finite numbers")
    return value


def equal(a, b):
    if type(a) is not type(b):
        return False
    if type(a) is dict:
        return a.keys() == b.keys() and all(equal(a[k], b[k]) for k in a)
    if type(a) is tuple:
        return len(a) == len(b) and all(equal(x, y) for x, y in zip(a, b))
    return a == b


def expect(actual, expected):
    if expected is not None:
        if type(expected) is not dict or any(type(k) is not str for k in expected):
            raise ValueError("expected state must be a dictionary")
        for value in expected.values():
            scalar(value)
        if not equal(actual, expected):
            raise ValueError("expected state differs from application read-back")


def ordinary(obj):
    cls = type(obj)
    if type(cls) is not type:
        raise ValueError("custom metaclasses are unsupported")
    for base in cls.__mro__:
        if base is object:
            continue
        if any(k in vars(base) for k in ("__slots__", "__getattribute__", "__getattr__", "__setattr__", "__delattr__")):
            raise ValueError("slots and custom attribute access are unsupported")
        if "__dict__" in vars(base) and type(vars(base)["__dict__"]) is not types.GetSetDescriptorType:
            raise ValueError("custom instance dictionary is unsupported")
    data = object.__getattribute__(obj, "__dict__")
    if type(data) is not dict:
        raise ValueError("ordinary instance dictionary required")
    return data


class Binding:
    def __init__(self, collector, function):
        self.receiver = None
        if type(function) is types.MethodType:
            self.receiver = function.__self__
            ordinary(self.receiver)
            function = function.__func__
        if type(function) is not types.FunctionType or function.__closure__ or hasattr(function, "__wrapped__"):
            raise ValueError("ordinary undecorated function or instance method required")
        code = function.__code__
        if code.co_flags & (inspect.CO_COROUTINE | inspect.CO_GENERATOR | inspect.CO_ASYNC_GENERATOR) or code.co_freevars:
            raise ValueError("async, generators and closures are unsupported")
        self.function, self.code = function, code
        self.component, self.digest, node = collector.lexical(code)
        if type(node) is not ast.FunctionDef or node.decorator_list or ".<locals>." in self.component:
            raise ValueError("decorated and nested declarations are unsupported")
        self.symbol = self.component.split("#", 1)[1].split("@", 1)[0]
        self.source = self.component.rsplit("@", 1)[1]
        self.globals = function.__globals__
        module = sys.modules.get(self.globals.get("__name__"))
        if type(module) is not types.ModuleType or vars(module) is not self.globals or os.path.abspath(self.globals.get("__file__", "")) != os.path.abspath(code.co_filename):
            raise ValueError("callable globals do not belong to staged module")
        self.owner = module
        parts = self.symbol.split(".")
        for part in parts[:-1]:
            self.owner = vars(self.owner).get(part)
            if type(self.owner) is not type:
                raise ValueError("unsupported lexical owner")
        if self.receiver is None and len(parts) != 1:
            raise ValueError("unbound methods and descriptors are unsupported")
        if self.receiver is not None and self.owner not in type(self.receiver).__mro__:
            raise ValueError("receiver differs from lexical method owner")
        # Defaults must still be the values declared in the staged function.
        try:
            defaults = tuple(scalar(ast.literal_eval(v)) for v in node.args.defaults)
            kwdefaults = {k.arg: scalar(ast.literal_eval(v)) for k, v in zip(node.args.kwonlyargs, node.args.kw_defaults) if v is not None}
        except (ValueError, TypeError):
            raise ValueError("nonliteral or mutable defaults are unsupported")
        if not equal(function.__defaults__ or (), defaults) or not equal(function.__kwdefaults__ or {}, kwdefaults):
            raise ValueError("replaced function defaults")
        self.defaults, self.kwdefaults = defaults, kwdefaults
        collector.receivers[self] = self.receiver
        self.check()

    def check(self):
        if self not in _collector.receivers or self.receiver is not _collector.receivers[self]:
            raise ValueError("replaced receiver binding")
        if vars(self.owner).get(self.code.co_name) is not self.function or self.function.__code__ is not self.code or not equal(self.function.__defaults__ or (), self.defaults) or not equal(self.function.__kwdefaults__ or {}, self.kwdefaults):
            raise ValueError("replaced callable binding")
        if self.receiver is not None and inspect.getattr_static(self.receiver, self.code.co_name) is not self.function:
            raise ValueError("replaced receiver method")

    def frame(self, frame):
        self.check()
        if frame.f_code is not self.code or frame.f_globals is not self.globals:
            raise ValueError("substituted callable frame")
        if self.receiver is not None and frame.f_locals.get(self.code.co_varnames[0]) is not self.receiver:
            raise ValueError("selected call used another receiver")

    def call(self, *args):
        self.check()
        if self.receiver is not None:
            return self.function(self.receiver, *args)
        return self.function(*args)


class Collector:
    def __init__(self, request, emit, locations, codes):
        self.request, self.emit = request, emit
        self.locations, self.codes = locations, codes
        self.sources = {}
        self.failed = False
        self.lives, self.instances = [], []
        self.receivers = {}
        self.targets = {}
        self.active = None
        self.count = 0
        self.thread = threading.get_ident()
        self.driver_codes = set()
        self.handles = {}
        # The first reason a handle was misused, kept for the diagnostic in
        # case the driver catches the error.
        self.misuse = None
        self.writers = (_Lifecycle.__enter__.__wrapped__.__code__, _Lifecycle.step.__wrapped__.__code__)

    def lexical(self, code):
        filename = os.path.abspath(code.co_filename)
        root = self.request["root"]
        if not filename.startswith(root + os.sep) or not filename.endswith(".py"):
            raise ValueError("callable is not staged Python application code")
        if filename not in self.sources:
            with open(filename, "rb") as stream:
                raw = stream.read()
            tree = ast.parse(raw, filename)
            nodes = {(n.lineno, n.name): n for n in ast.walk(tree) if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef))}
            self.sources[filename] = ("sha256:" + hashlib.sha256(raw).hexdigest(), self.locations(tree), list(self.codes(compile(raw, filename, "exec"))), nodes)
        digest, locations, compiled, nodes = self.sources[filename]
        symbol = locations.get((code.co_firstlineno, code.co_name))
        if symbol is None or not any(code == original for original in compiled):
            raise ValueError("callable code differs from staged lexical declaration")
        source = os.path.relpath(filename, root).replace(os.sep, "/")
        component = "python:" + source[:-3].replace("/", ".") + "#" + symbol + "@" + source
        node = nodes.get((code.co_firstlineno, code.co_name))
        if node is None:
            raise ValueError("decorated declaration is unsupported")
        return component, digest, node

    def driver(self):
        if self.failed or threading.get_ident() != self.thread or sys.gettrace() != self.trace:
            raise ValueError("observation execution is poisoned or tracing changed" + (": " + self.misuse if self.misuse else ""))
        frame = sys._getframe(1)
        while frame is not None:
            if os.path.abspath(frame.f_code.co_filename) == os.path.join(self.request["root"], self.request["check"]["source"]):
                component, digest, _ = self.lexical(frame.f_code)
                if component == self.request["key"]["check"]:
                    function = frame.f_globals.get(frame.f_code.co_name)
                    binding = Binding(self, function)
                    binding.frame(frame)
                    if digest != self.request["check"]["source_sha256"]:
                        raise ValueError("driver source changed")
                    self.driver_codes.add(frame.f_code)
                    return
            frame = frame.f_back
        raise ValueError("configured driver is not executing")

    def write(self, life, entry, **state):
        caller = sys._getframe(1)
        if not any(caller.f_code is code for code in self.writers) or caller.f_globals is not globals():
            self.failed = True
            raise ValueError("observation did not come from a lifecycle read-back")
        self.count += 1
        self.emit(dict(type="observation", kind="initial" if life.sequence == 0 else "transition",
                       run=life.run, instance=life.identity, sequence=life.sequence,
                       key=self.request["key"], target=life.target.component,
                       driver=self.request["key"]["check"], source_sha256=life.target.digest,
                       driver_sha256=self.request["check"]["source_sha256"],
                       symbol=life.target.symbol, entry=entry.component, entry_sha256=entry.digest,
                       readback=life.fields, **state))

    def trace(self, frame, event, arg):
        try:
            if threading.get_ident() != self.thread:
                raise ValueError("concurrent execution is unsupported")
            if event == "exception" and frame.f_code in self.driver_codes:
                self.failed = True
            active = self.active
            if active is not None:
                active.observe(frame, event, arg)
        except BaseException:
            self.failed = True
            raise
        return self.trace

    def profile(self, frame, event, arg):
        # Refuse thread creation even if the worker never calls the target.
        if event == "c_call" and (arg is _thread.start_new_thread or arg is getattr(_thread, "start_joinable_thread", None)):
            self.failed = True
            raise ValueError("concurrent execution is unsupported")


class _Lifecycle:
    def __init__(self, target, initializer, argument, fields, expected):
        c = _collector
        c.driver()
        if c.active is not None:
            raise ValueError("nested lifecycles during a step are unsupported")
        self.target = Binding(c, target)
        c.targets[self] = self.target
        if self.target.component != c.request["key"]["target"] or self.target.digest != c.request["target"]["source_sha256"]:
            raise ValueError("target differs from selected staged declaration")
        self.functional = initializer is not None
        if self.functional != (self.target.receiver is None):
            raise ValueError("unsupported target binding form")
        if type(fields) is not dict or not fields or any(type(k) is not str or not k or type(v) is not str or not v for k, v in fields.items()):
            raise ValueError("read-back requires a nonempty declarative field mapping")
        self.fields = fields.copy()
        self.argument = argument
        self.initializer = None
        if self.functional:
            self.initializer = self.application(initializer)
            args = inspect.signature(self.target.function).parameters
            if type(argument) is not str or argument not in args or args[argument].kind not in (inspect.Parameter.POSITIONAL_ONLY, inspect.Parameter.POSITIONAL_OR_KEYWORD, inspect.Parameter.KEYWORD_ONLY):
                raise ValueError("named scalar state argument required")
        else:
            if any(not v.isidentifier() for v in fields.values()):
                raise ValueError("read-back attributes must be simple names")
        self.expected = expected
        self.entered = self.closed = False
        self.sequence = 0

    def application(self, function):
        binding = Binding(_collector, function)
        if binding.source == _collector.request["check"]["source"] or binding.component == self.target.component:
            raise ValueError("distinct surrounding application entry required")
        return binding

    def read(self):
        self.check_target()
        data = ordinary(self.target.receiver)
        for attr in self.fields.values():
            for cls in type(self.target.receiver).__mro__:
                value = vars(cls).get(attr)
                if any(hasattr(type(value), name) for name in ("__get__", "__set__", "__delete__")):
                    raise ValueError("properties and descriptors cannot read state")
        return {key: scalar(data[attr]) for key, attr in self.fields.items()}

    def check_target(self):
        if self.target is not _collector.targets[self]:
            raise ValueError("replaced lifecycle target")
        self.target.check()

    @guarded
    def __enter__(self):
        c = _collector
        c.driver()
        if self.entered or self.closed or c.active is not None:
            raise ValueError("reused or nested lifecycle")
        if len(c.lives) >= int(c.request["sampling"]["runs"]):
            raise ValueError("run sampling budget exceeded")
        self.entered = True
        self.run = len(c.lives) + 1
        c.lives.append(self)
        obj = self if self.functional else self.target.receiver
        if not any(obj is old for old in c.instances):
            c.instances.append(obj)
        self.identity = next(i + 1 for i, old in enumerate(c.instances) if old is obj)
        self.check_target()
        if self.functional:
            self.state = {next(iter(self.fields)): scalar(self.initializer.call())}
        else:
            self.state = self.read()
        expect(self.state, self.expected)
        c.write(self, self.initializer or self.target, state=self.state)
        return self

    @guarded
    def __exit__(self, exc_type, exc, tb):
        _collector.driver()
        if not self.entered or self.closed or _collector.active is not None:
            raise ValueError("closed, unentered or nested lifecycle exit")
        self.closed = True
        if exc_type is not None or self.sequence == 0:
            raise ValueError("exception or empty lifecycle")
        return False

    def observe(self, frame, event, arg):
        self.check_target()
        if event == "call" and frame.f_code is self.entry.code:
            self.entry.frame(frame)
            if self.entry_frame is not None:
                raise ValueError("reentrant application entry")
            self.entry_frame = frame
        if event == "call" and frame.f_code is self.target.code:
            self.target.frame(frame)
            _collector.driver()
            parent = frame.f_back
            while parent is not None and parent is not self.entry_frame:
                parent = parent.f_back
            if parent is None or self.calls != 0:
                raise ValueError("step requires exactly one nonnested selected call")
            self.calls += 1
            self.target_frame = frame
            if self.functional:
                before = {next(iter(self.fields)): scalar(frame.f_locals[self.argument])}
            else:
                before = self.read()
            if not equal(before, self.before):
                raise ValueError("selected call pre-state differs from lifecycle")
        if event == "exception" and (frame is self.entry_frame or frame is self.target_frame):
            raise ValueError("application call raised an exception")
        if event == "return" and frame is self.target_frame:
            self.after = {next(iter(self.fields)): scalar(arg)} if self.functional else self.read()
        if event == "return" and frame is self.entry_frame:
            if self.after is None:
                raise ValueError("application entry did not return a selected call")
            actual = {next(iter(self.fields)): scalar(arg)} if self.functional else self.read()
            if not equal(actual, self.after):
                raise ValueError("application entry substituted selected result")

    @guarded
    def step(self, entry, *, expected_before=None, expected_after=None):
        c = _collector
        c.driver()
        if not self.entered or self.closed or c.active is not None:
            raise ValueError("closed, unentered or nested lifecycle step")
        if self.sequence >= int(c.request["sampling"]["steps"]):
            raise ValueError("step sampling budget exceeded")
        self.check_target()
        self.entry = self.application(entry)
        self.before = self.state.copy() if self.functional else self.read()
        if not equal(self.before, self.state):
            raise ValueError("state changed between transitions")
        expect(self.before, expected_before)
        self.calls, self.after = 0, None
        self.entry_frame = self.target_frame = None
        c.active = self
        try:
            args = [next(iter(self.state.values()))] if self.functional else []
            self.entry.call(*args)
            if c.failed or self.calls != 1 or self.after is None:
                raise ValueError("incomplete selected call")
            self.check_target()
            self.entry.check()
            expect(self.after, expected_after)
            self.sequence += 1
            self.state = self.after.copy()
            c.write(self, self.entry, before=self.before, after=self.after)
        finally:
            c.active = None
            self.entry_frame = self.target_frame = None


def _poison(message):
    _collector.failed = True
    _collector.misuse = _collector.misuse or message
    raise AttributeError(message)


class Lifecycle:
    """A driver's handle on one lifecycle: a context manager with step().

    It has no state of its own. Reading, writing or deleting any other
    attribute poisons the execution, even if the driver catches the error.
    """

    __slots__ = ()

    def __getattr__(self, name):
        _poison("lifecycle handles expose no state; drive the application through step()")

    def __setattr__(self, name, value):
        _poison("lifecycle handles are read-only")

    def __delattr__(self, name):
        _poison("lifecycle handles are read-only")

    def __enter__(self):
        _record(self).__enter__()
        return self

    def __exit__(self, exc_type, exc, tb):
        return _record(self).__exit__(exc_type, exc, tb)

    def step(self, entry, *, expected_before=None, expected_after=None):
        _record(self).step(entry, expected_before=expected_before, expected_after=expected_after)


def _handle(record):
    handle = object.__new__(Lifecycle)
    _collector.handles[handle] = record
    return handle


def _record(handle):
    record = _collector.handles.get(handle)
    if type(handle) is not Lifecycle or record is None:
        _collector.failed = True
        _collector.misuse = _collector.misuse or "unknown lifecycle handle"
        raise ValueError("unknown lifecycle handle")
    return record


@guarded
def functional(target, initializer, *, argument, field, expected_initial=None):
    if type(field) is not str or not field:
        raise ValueError("functional read-back requires a state field")
    return _handle(_Lifecycle(target, initializer, argument, {field: "$return"}, expected_initial))


@guarded
def instance(target, *, fields, expected_initial=None):
    return _handle(_Lifecycle(target, None, None, fields, expected_initial))


def run(request, emit, locations, codes, argv):
    global _collector
    c = _collector = Collector(request, emit, locations, codes)
    random.seed(int(request["sampling"]["seed"]))
    filename = os.path.join(request["root"], request["driver"])
    with open(filename, "rb") as stream:
        code = compile(stream.read(), filename, "exec")
    module = types.ModuleType("__main__")
    module.__file__, module.__package__ = filename, None
    sys.modules["__main__"] = module
    sys.argv = list(argv)
    sys.settrace(c.trace)
    sys.setprofile(c.profile)
    threading.settrace(c.trace)
    try:
        exec(code, vars(module))
        if c.failed or not c.lives or any(not life.closed or life.sequence == 0 for life in c.lives) or sys.gettrace() != c.trace or sys.getprofile() != c.profile:
            raise ValueError("driver did not complete bound lifecycles")
    finally:
        sys.settrace(None)
        sys.setprofile(None)
        threading.settrace(None)
    emit(dict(type="end", count=c.count))
