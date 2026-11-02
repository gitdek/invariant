"""Sample selected calls without changing application source or functions.

A driver gets stateless handles. Every binding, read-back and continuity check
uses records only the collector holds, so nothing a driver is given can
substitute evidence. Code in the same process can still reach this module's
own objects, frames and closures; a driver that does is tampering with the
gate, which no in-process collector can rule out, and its bytes are bound to
every observation it makes.
"""

import ast
import asyncio
import _thread
import dis
import functools
import gc
import hashlib
import inspect
import json
import math
import os
import random
import re
import subprocess
import sys
import threading
import time
import types


_collector = None


def guarded(method):
    @functools.wraps(method)
    def call(*args, **kwargs):
        try:
            return method(*args, **kwargs)
        except BaseException:
            if _collector is not None:
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
    if type(a) in (tuple, list):
        return len(a) == len(b) and all(equal(x, y) for x, y in zip(a, b))
    return a == b


def copied(value, ancestors=()):
    if type(value) not in (dict, list):
        return scalar(value)
    if any(value is old for old in ancestors):
        raise ValueError("cyclic read-back")
    ancestors = ancestors + (value,)
    if type(value) is list:
        return [copied(v, ancestors) for v in value]
    if any(type(k) is not str for k in value):
        raise ValueError("read-back dictionary keys must be strings")
    return {k: copied(v, ancestors) for k, v in value.items()}


def path_segments(path):
    name = r"[A-Za-z_][A-Za-z0-9_]*"
    if re.fullmatch(name + r"(?:\." + name + r"|\[(?:[0-9]+|\*)\])*", path) is None:
        raise ValueError("invalid module read-back path")
    return re.findall(name + r"|\[[0-9]+\]|\[\*\]", path)


def read_path(value, segments, ancestors=()):
    if not segments:
        return copied(value, ancestors)
    if any(value is old for old in ancestors):
        raise ValueError("cyclic read-back path")
    ancestors = ancestors + (value,)
    head, tail = segments[0], segments[1:]
    if head == "[*]":
        if type(value) not in (dict, list):
            raise ValueError("projection requires an exact dict or list")
        return [read_path(v, tail, ancestors) for v in (value.values() if type(value) is dict else value)]
    if head.startswith("["):
        if type(value) is not list:
            raise ValueError("index requires an exact list")
        return read_path(value[int(head[1:-1])], tail, ancestors)
    if type(value) is not dict:
        raise ValueError("key requires an exact dict")
    if any(type(k) is not str for k in value):
        raise ValueError("dictionary lookup requires string keys")
    return read_path(value[head], tail, ancestors)


def expect(actual, expected, module=False):
    if expected is not None:
        if type(expected) is not dict or any(type(k) is not str for k in expected):
            raise ValueError("expected state must be a dictionary")
        if module:
            expected = copied(expected)
        else:
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


def coroutine_objects():
    return {value for value in gc.get_objects()
            if type(value) is types.CoroutineType
            or (type(value) is types.GeneratorType and value.gi_code.co_flags & inspect.CO_ITERABLE_COROUTINE)}


def return_kind(frame):
    code, offset = frame.f_code.co_code, frame.f_lasti
    # Before 3.10, YIELD_FROM backs f_lasti up one word on suspension.
    if sys.version_info < (3, 10) and offset + 2 < len(code) and dis.opname[code[offset + 2]] == "YIELD_FROM":
        offset += 2
    opcode = dis.opname[code[offset]]
    # 3.13 reports suspension at the following RESUME instruction.
    if opcode in ("YIELD_VALUE", "YIELD_FROM") or (opcode == "RESUME" and code[offset + 1] & 3):
        return "yield"
    if opcode in ("RETURN_VALUE", "RETURN_CONST"):
        return "return"
    return "unwind"


def await_completion(frame, arg):
    # Completion of an await is a stackless exception event, not an
    # application exception. Explicit raises always carry a traceback.
    return (arg[0] in (StopIteration, StopAsyncIteration) and arg[2] is None
            and frame.f_code.co_flags & (inspect.CO_COROUTINE | inspect.CO_ITERABLE_COROUTINE))


class Refusal:
    """An exact application exception allowed to refuse an explored step.

    Only the named, finite JSON scalar attributes are matched and recorded.
    Subclasses of the exception or of an attribute's scalar type do not match.
    """

    @guarded
    def __init__(self, exception_type, **attributes):
        # An Exception subclass can also inherit a control-flow class, such
        # as class X(Exception, KeyboardInterrupt); neither may refuse.
        if (not isinstance(exception_type, type) or not issubclass(exception_type, Exception)
                or issubclass(exception_type, (KeyboardInterrupt, SystemExit, GeneratorExit, asyncio.CancelledError))):
            raise ValueError("a refusal requires an Exception subclass")
        if (type(exception_type.__module__) is not str or not exception_type.__module__
                or type(exception_type.__qualname__) is not str or not exception_type.__qualname__):
            raise ValueError("a refusal requires a qualified exception type")
        self.exception_type = exception_type
        self.attributes = {key: scalar(value) for key, value in attributes.items()}
        self.type_name = exception_type.__module__ + "." + exception_type.__qualname__

    def descriptor(self):
        return dict(type=self.type_name, attributes=self.attributes.copy())

    def matches(self, exc):
        if type(exc) is not self.exception_type:
            return False
        # The application can give the class a control-flow base after it
        # was declared, by assigning __bases__.
        if issubclass(type(exc), (KeyboardInterrupt, SystemExit, GeneratorExit, asyncio.CancelledError)):
            return False
        if self.type_name != type(exc).__module__ + "." + type(exc).__qualname__:
            return False
        for name, value in self.attributes.items():
            try:
                actual = getattr(exc, name)
            except AttributeError:
                return False
            if type(actual) not in (str, bool, int, float, type(None)) or not equal(actual, value):
                return False
            # == takes -0.0 for 0.0, and the row records the declared value.
            if type(actual) is float and (not math.isfinite(actual) or math.copysign(1.0, actual) != math.copysign(1.0, value)):
                return False
        return True


class Binding:
    def __init__(self, collector, function, module=False, entry=False):
        self.receiver = None
        if type(function) is types.MethodType:
            if module:
                raise ValueError("module function required")
            self.receiver = function.__self__
            ordinary(self.receiver)
            function = function.__func__
        if type(function) is not types.FunctionType or function.__closure__ or hasattr(function, "__wrapped__"):
            raise ValueError("ordinary undecorated function or instance method required")
        code = function.__code__
        if code.co_flags & (inspect.CO_GENERATOR | inspect.CO_ASYNC_GENERATOR) or (not module and code.co_flags & inspect.CO_COROUTINE) or code.co_freevars:
            raise ValueError("async, generators and closures are unsupported")
        self.function, self.code = function, code
        self.component, self.digest, node = collector.lexical(code)
        if (not module and (type(node) is not ast.FunctionDef or node.decorator_list)) or ".<locals>." in self.component:
            raise ValueError("decorated and nested declarations are unsupported")
        self.symbol = self.component.split("#", 1)[1].split("@", 1)[0]
        self.source = self.component.rsplit("@", 1)[1]
        self.globals = function.__globals__
        if module and collector.original_functions.get((id(self.globals), code)) is not function:
            raise ValueError("replaced lexical module function")
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
        self.opaque_defaults = module and entry
        if self.opaque_defaults:
            if function not in collector.original_defaults:
                raise ValueError("entry defaults were not observed at definition")
            self.defaults, self.kwdefaults, self.keyword_values = collector.original_defaults[function]
        else:
            # Targets and legacy entries keep literal scalar defaults.
            try:
                defaults = tuple(scalar(ast.literal_eval(v)) for v in node.args.defaults)
                kwdefaults = {k.arg: scalar(ast.literal_eval(v)) for k, v in zip(node.args.kwonlyargs, node.args.kw_defaults) if v is not None}
            except (ValueError, TypeError):
                raise ValueError("nonliteral or mutable defaults are unsupported")
            self.defaults, self.kwdefaults = defaults, kwdefaults
        collector.receivers[self] = self.receiver
        self.check()

    def check(self):
        if self not in _collector.receivers or self.receiver is not _collector.receivers[self]:
            raise ValueError("replaced receiver binding")
        if self.opaque_defaults:
            valid = self.function.__defaults__ is self.defaults and self.function.__kwdefaults__ is self.kwdefaults
            actual = self.function.__kwdefaults__ or {}
            valid = valid and actual.keys() == self.keyword_values.keys() and all(actual[k] is v for k, v in self.keyword_values.items())
        else:
            valid = equal(self.function.__defaults__ or (), self.defaults) and equal(self.function.__kwdefaults__ or {}, self.kwdefaults)
        if vars(self.owner).get(self.code.co_name) is not self.function or self.function.__code__ is not self.code or not valid:
            raise ValueError("replaced callable binding")
        if self.receiver is not None and inspect.getattr_static(self.receiver, self.code.co_name) is not self.function:
            raise ValueError("replaced receiver method")

    def frame(self, frame):
        self.check()
        if frame.f_code is not self.code or frame.f_globals is not self.globals:
            raise ValueError("substituted callable frame")
        if self.receiver is not None and frame.f_locals.get(self.code.co_varnames[0]) is not self.receiver:
            raise ValueError("selected call used another receiver")

    def call(self, *args, **kwargs):
        self.check()
        if self.receiver is not None:
            return self.function(self.receiver, *args, **kwargs)
        return self.function(*args, **kwargs)


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
        self.original_defaults = {}
        self.original_functions = {}
        self.definitions = {}
        self.internal_threads = set()
        self.thread_traces = {}
        self.thread_start = threading.Thread.start
        # The first reason a handle was misused, kept for the diagnostic in
        # case the driver catches the error.
        self.misuse = None
        self.writers = (_Lifecycle.__enter__.__wrapped__.__code__, _Lifecycle.step.__wrapped__.__code__, _ModuleLifecycle.step.__wrapped__.__code__, _Exploration.try_step.__code__)
        # An explored invocation drives the application only through
        # explore(), which runs once (#342).
        self.exploring = request.get("mode") == "explored"
        self.exploration = None
        self.explored = False

    def lexical(self, code):
        filename = os.path.abspath(code.co_filename)
        root = self.request["root"]
        if not filename.startswith(root + os.sep) or not filename.endswith(".py"):
            raise ValueError("callable is not staged Python application code")
        if filename not in self.sources:
            with open(filename, "rb") as stream:
                raw = stream.read()
            tree = ast.parse(raw, filename)
            nodes = {(min([n.lineno] + [d.lineno for d in n.decorator_list]), n.name): n for n in ast.walk(tree) if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef))}
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
        # An exploration's rows all name the driver function that called
        # explore(), beneath the steps' own functions.
        exploring = self.exploration.driver["id"] if self.exploration is not None else None
        while frame is not None:
            if os.path.abspath(frame.f_code.co_filename) == os.path.join(self.request["root"], self.request["check"]["source"]):
                component, digest, _ = self.lexical(frame.f_code)
                selected = self.request["check"]
                if self.request.get("legacy"):
                    selected = next((d for d in self.request["drivers"] if d["id"] == component), {})
                if component == selected.get("id") and exploring in (None, component):
                    function = frame.f_globals.get(frame.f_code.co_name)
                    binding = Binding(self, function)
                    binding.frame(frame)
                    if digest != selected["source_sha256"]:
                        raise ValueError("driver source changed")
                    self.driver_codes.add(frame.f_code)
                    self.current_driver = selected
                    return
            frame = frame.f_back
        raise ValueError("configured driver is not executing")

    def write(self, life, entry, kind=None, **state):
        caller = sys._getframe(1)
        if not any(caller.f_code is code for code in self.writers) or caller.f_globals is not globals():
            self.failed = True
            raise ValueError("observation did not come from a lifecycle read-back")
        self.count += 1
        if life.form:
            state["form"] = life.form
        if kind is None:
            kind = "initial" if life.sequence == 0 else "transition"
        if self.exploration is not None:
            state.update(self.exploration.fields(life, kind))
        row = dict(type="observation", kind=kind,
                   run=life.run, instance=life.identity, sequence=life.sequence,
                   key=self.request["key"], target=life.target.component,
                   driver=self.current_driver["id"], source_sha256=life.target.digest,
                   driver_sha256=self.current_driver["source_sha256"],
                   symbol=life.target.symbol, readback=life.fields, **state)
        # An omitted step calls no application entry.
        if entry is not None:
            row.update(entry=entry.component, entry_sha256=entry.digest)
        self.emit(row)

    def declare(self, steps):
        # The harness's own record of the steps it tries, before any run.
        caller = sys._getframe(1)
        if caller.f_code is not explore.__wrapped__.__code__ or caller.f_globals is not globals():
            self.failed = True
            raise ValueError("exploration steps did not come from the harness")
        self.emit(dict(type="exploration", steps=steps))

    def trace(self, frame, event, arg):
        current = threading.current_thread()
        if current in self.internal_threads:
            return None
        if current not in self.thread_traces:
            self.thread_traces[current] = lambda frame, event, arg: self.trace_event(current, frame, event, arg)
        return self.trace_event(current, frame, event, arg)

    def trace_event(self, current, frame, event, arg):
        try:
            if threading.get_ident() != self.thread:
                if self.active is None or self.active.form != "module" or current not in self.active.threads:
                    raise ValueError("unrelated concurrent execution is unsupported")
            if frame.f_code.co_name == "<module>" and frame.f_code.co_filename.startswith(self.request["root"] + os.sep):
                self.capture_definition(frame, event)
            if event == "exception" and frame.f_code in self.driver_codes:
                self.failed = True
            active = self.active
            if active is not None:
                active.observe(frame, event, arg)
        except BaseException:
            self.failed = True
            raise
        return self.thread_traces[current]

    def capture_definition(self, frame, event):
        # Observe MAKE_FUNCTION before any decorator receives the object, even
        # through *args, a callable object or a C callable. The evaluation stack
        # keeps the new function alive and visible to GC at the next opcode.
        frame.f_trace_opcodes = True
        if event == "return":
            self.definitions.pop(frame, None)
        if event != "opcode":
            return
        previous, pending = self.definitions.get(frame, (None, ()))
        if previous == "MAKE_FUNCTION":
            pending = tuple(value for value in gc.get_objects()
                            if type(value) is types.FunctionType
                            and value.__globals__ is frame.f_globals
                            and value not in self.original_defaults)
        if previous in ("MAKE_FUNCTION", "SET_FUNCTION_ATTRIBUTE"):
            # Python 3.13+ attaches defaults with separate opcodes immediately
            # after creation. No application callback runs between them.
            for value in pending:
                self.original_defaults[value] = (value.__defaults__, value.__kwdefaults__, (value.__kwdefaults__ or {}).copy())
                self.original_functions.setdefault((id(value.__globals__), value.__code__), value)
        else:
            pending = ()
        self.definitions[frame] = (dis.opname[frame.f_code.co_code[frame.f_lasti]], pending)

    def profile(self, frame, event, arg):
        # Refuse thread creation even if the worker never calls the target.
        if event == "c_call" and (arg is _thread.start_new_thread or arg is getattr(_thread, "start_joinable_thread", None)):
            worker = frame.f_locals.get("self")
            if worker not in self.internal_threads and (self.active is None or self.active.form != "module" or worker not in self.active.threads):
                self.failed = True
                raise ValueError("unowned thread creation is unsupported")

    def start_thread(self, worker, *args, **kwargs):
        try:
            life = self.active
            if life is None or life.form != "module" or not life.owns(sys._getframe(1)):
                raise ValueError("thread must be started by a module entry or its workers")
            life.threads.add(worker)
            return self.thread_start(worker, *args, **kwargs)
        except BaseException:
            self.failed = True
            raise


class _Lifecycle:
    def __init__(self, target, initializer, argument, fields, expected, form=""):
        c = _collector
        c.driver()
        if c.active is not None:
            raise ValueError("nested lifecycles during a step are unsupported")
        if c.exploring and (c.exploration is None or not c.exploration.starting):
            raise ValueError("an explored invocation makes lifecycles only in explore()'s starts")
        self.form = form
        self.target = Binding(c, target, module=form == "module")
        c.targets[self] = self.target
        selected = c.request["target"]
        if c.request.get("legacy"):
            selected = next((t for t in c.request["targets"] if t["id"] == self.target.component), {})
        if self.target.component != selected.get("id") or self.target.digest != selected.get("source_sha256"):
            raise ValueError("target differs from selected staged declaration")
        self.functional = initializer is not None
        if not form and self.functional != (self.target.receiver is None):
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
        elif form != "module":
            if any(not v.isidentifier() for v in fields.values()):
                raise ValueError("read-back attributes must be simple names")
        self.expected = expected
        self.entered = self.closed = False
        self.sequence = 0
        # An exploration run whose one attempt the environment rules out
        # can end with no step.
        self.omitted = False
        if c.exploration is not None:
            c.exploration.made.append(self)

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

    def owns(self, frame):
        if threading.get_ident() != _collector.thread:
            return False
        while frame is not None:
            if frame is self.entry_frame:
                return True
            frame = frame.f_back
        return False

    def refusal_candidate(self, frame, exc):
        # Attribute getters must run only after escape, with tracing active;
        # a trace callback itself runs with tracing suspended.
        return (self.refusal is not None and self.calls == 0
                and not getattr(self, "threads", ())
                and threading.get_ident() == _collector.thread
                and self.owns(frame) and type(exc) is self.refusal.exception_type)

    def escaped_refusal(self, exc):
        # A driver exception, a target exception, or a collector error must
        # never become a refusal, even when it has the declared type.
        escaped = exc is self.entry_raised or exc is getattr(self, "await_raised", None)
        if (_collector.failed or not escaped or self.refusal is None or self.calls
                or getattr(self, "threads", ()) or not self.refusal.matches(exc)):
            raise ValueError("application call raised an undeclared or invalid refusal") from exc
        if _collector.failed or self.calls or getattr(self, "threads", ()):
            raise ValueError("refusal attributes started application work")
        self.outcome = "refused"

    @guarded
    def __enter__(self):
        c = _collector
        c.driver()
        if self.entered or self.closed or c.active is not None:
            raise ValueError("reused or nested lifecycle")
        if c.exploring and (c.exploration is None or c.exploration.entering is not self):
            raise ValueError("explore() enters an explored invocation's lifecycles")
        if len(c.lives) >= int(c.request["sampling"]["runs"]):
            raise ValueError("run sampling budget exceeded")
        self.entered = True
        self.run = len(c.lives) + 1
        c.lives.append(self)
        obj = self.target.owner if self.form == "module" else self if self.functional else self.target.receiver
        if not any(obj is old for old in c.instances):
            c.instances.append(obj)
        self.identity = next(i + 1 for i, old in enumerate(c.instances) if old is obj)
        self.check_target()
        if self.functional:
            self.state = {next(iter(self.fields)): scalar(self.initializer.call())}
        else:
            self.state = self.read()
        expect(self.state, self.expected, module=self.form == "module")
        c.write(self, self.initializer or self.target, state=self.state)
        return self

    @guarded
    def __exit__(self, exc_type, exc, tb):
        c = _collector
        c.driver()
        if not self.entered or self.closed or c.active is not None:
            raise ValueError("closed, unentered or nested lifecycle exit")
        if c.exploring and (c.exploration is None or c.exploration.leaving is not self):
            raise ValueError("explore() closes an explored invocation's lifecycles")
        self.closed = True
        if exc_type is not None or (self.sequence == 0 and not self.omitted):
            raise ValueError("exception or empty lifecycle")
        return False

    def observe(self, frame, event, arg):
        self.check_target()
        if event == 'call' and self.target_frame is not None:
            source = os.path.relpath(os.path.abspath(frame.f_code.co_filename), _collector.request['root']).replace(os.sep, '/')
            if source in _collector.request.get('delegates', []):
                parent = frame.f_back
                while parent is not None and parent is not self.target_frame:
                    parent = parent.f_back
                if parent is not None:
                    binding = Binding(_collector, frame.f_globals.get(frame.f_code.co_name))
                    binding.frame(frame)
                    self.delegate_called = True
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
            if frame is self.target_frame or not self.refusal_candidate(frame, arg[1]):
                raise ValueError("application call raised an exception")
            self.pending_exceptions[frame] = arg[1]
        if event == "return" and frame is self.target_frame:
            self.after = {next(iter(self.fields)): scalar(arg)} if self.functional else self.read()
        if event == "return" and frame is self.entry_frame:
            if return_kind(frame) == "unwind":
                self.entry_raised = self.pending_exceptions.pop(frame, None)
                if self.entry_raised is None:
                    raise ValueError("application entry failed")
                return
            self.pending_exceptions.pop(frame, None)
            actual = {next(iter(self.fields)): scalar(arg)} if self.functional else self.read()
            if self.after is None:
                # An explored step the application refuses before its
                # selected call must leave the state as it was (#342).
                if not (_collector.exploring and self.calls == 0 and equal(actual, self.before)):
                    raise ValueError("application entry did not return a selected call")
                self.after = actual
            elif not equal(actual, self.after):
                raise ValueError("application entry substituted selected result")

    @guarded
    def step(self, entry, *, expected_before=None, expected_after=None):
        c = _collector
        c.driver()
        if not self.entered or self.closed or c.active is not None:
            raise ValueError("closed, unentered or nested lifecycle step")
        if self.sequence >= int(c.request["sampling"]["steps"]):
            raise ValueError("step sampling budget exceeded")
        if c.exploring:
            c.exploration.stepping(self)
        self.refusal = c.exploration.refusal if c.exploring else None
        self.outcome = "accepted" if self.refusal is not None else None
        self.check_target()
        self.entry = self.application(entry)
        self.before = self.state.copy() if self.functional else self.read()
        if not equal(self.before, self.state):
            raise ValueError("state changed between transitions")
        expect(self.before, expected_before)
        self.calls, self.after = 0, None
        self.delegate_called = not bool(c.request.get('delegates'))
        self.entry_frame = self.target_frame = None
        self.entry_raised = None
        self.pending_exceptions = {}
        c.active = self
        try:
            args = [next(iter(self.state.values()))] if self.functional else []
            try:
                self.entry.call(*args)
            except BaseException as exc:
                self.escaped_refusal(exc)
                self.after = self.before.copy() if self.functional else self.read()
                if not equal(self.after, self.before):
                    raise ValueError("a refused step changed the state")
            refused = c.exploring and self.calls == 0 and self.after is not None
            if c.failed or (self.calls != 1 and not refused) or self.after is None or not (self.delegate_called or refused):
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


class _ModuleLifecycle(_Lifecycle):
    def __init__(self, target, fields, expected, timeout):
        if type(timeout) not in (int, float) or not math.isfinite(timeout) or not 0 < timeout <= 60:
            raise ValueError("module timeout must be positive and finite, at most 60 seconds")
        super().__init__(target, None, None, fields, expected, form="module")
        self.paths = {key: path_segments(path) for key, path in self.fields.items()}
        self.timeout = timeout

    def application(self, function):
        binding = Binding(_collector, function, module=True, entry=True)
        if binding.source == _collector.request["check"]["source"] or binding.component == self.target.component:
            raise ValueError("distinct surrounding application entry required")
        return binding

    def read(self):
        self.check_target()
        return {key: read_path(self.target.globals, path) for key, path in self.paths.items()}

    def owns(self, frame):
        if threading.current_thread() in self.threads:
            return True
        if threading.get_ident() != _collector.thread:
            return False
        while frame is not None:
            if frame is self.entry_frame or frame is self.await_frame:
                return True
            frame = frame.f_back
        return False

    def observe(self, frame, event, arg):
        self.check_target()
        coroutine = frame.f_code.co_flags & (inspect.CO_COROUTINE | inspect.CO_ITERABLE_COROUTINE)
        if coroutine:
            if event == "call":
                self.coroutines.add(frame)
            elif event == "return":
                kind = return_kind(frame)
                if kind != "yield":
                    self.coroutines.discard(frame)
                    exc = self.pending_exceptions.get(frame)
                    if kind == "unwind" and (exc is None or not self.refusal_candidate(frame, exc)):
                        raise ValueError("module coroutine failed or was cancelled")
        if event == "call" and frame.f_code is self.entry.code:
            self.entry.frame(frame)
            if self.entry_frame is not None and frame is not self.entry_frame:
                raise ValueError("reentrant application entry")
            self.entry_frame = frame
        if event == "call" and frame.f_code is self.target.code:
            self.target.frame(frame)
            if frame is not self.await_frame and not self.owns(frame.f_back):
                raise ValueError("selected call is not owned by the module entry")
            if frame is not self.target_frame:
                if self.calls:
                    raise ValueError("step requires exactly one nonrecursive selected call")
                self.calls += 1
                self.target_frame = frame
        selected = frame is self.entry_frame or frame is self.target_frame
        if (selected or coroutine) and event == "exception" and not await_completion(frame, arg):
            if frame is self.target_frame:
                raise ValueError("application call raised an exception")
            if self.refusal_candidate(frame, arg[1]):
                self.pending_exceptions[frame] = arg[1]
            else:
                self.pending_exceptions.pop(frame, None)
                if selected:
                    raise ValueError("application call raised an exception")
        if (selected or coroutine) and event == "return":
            # Python reports a return for every coroutine suspension as well
            # as completion. Retain frame identity until the actual return.
            kind = return_kind(frame)
            if kind == "return":
                self.pending_exceptions.pop(frame, None)
                if frame is self.target_frame:
                    self.completed = True
                if frame is self.entry_frame:
                    self.entry_completed = True
            elif kind == "unwind":
                exc = self.pending_exceptions.pop(frame, None)
                if frame is self.entry_frame:
                    if exc is None:
                        raise ValueError("application entry failed")
                    self.entry_raised = exc
                if frame is self.await_frame:
                    self.await_raised = exc

    def expire(self):
        # A Python thread cannot safely be killed or forcibly joined. End this
        # disposable runner without a seal, including for non-daemon workers
        # and code that catches cancellation. Go accepts none of its rows.
        _collector.failed = True
        os.write(2, b"module observation timed out or left unfinished work\n")
        os._exit(1)

    async def await_result(self, result):
        return await result

    def task(self, loop, coroutine, **kwargs):
        task = asyncio.Task(coroutine, loop=loop, **kwargs)
        self.tasks.add(task)
        if getattr(coroutine, "cr_frame", None) is self.await_frame:
            self.await_task = task
        return task

    def async_error(self, loop, context):
        _collector.failed = True
        _collector.misuse = "module event loop reported failed work"
        loop.default_exception_handler(context)

    def watchdog(self, deadline):
        # A separate interpreter enforces the deadline even while application
        # C code holds this interpreter's GIL. EOF cancels it on normal cleanup
        # or parent death; no application thread can catch SIGKILL.
        program = """import os, select, signal, sys, time
parent, deadline = int(sys.argv[1]), float(sys.argv[2])
os.write(1, b'1')
if not select.select([0], [], [], max(0, deadline - time.monotonic()))[0]:
    if os.getppid() == parent:
        os.write(2, b'module observation timed out or left unfinished work\\n')
        os.kill(parent, signal.SIGKILL)
"""
        process = subprocess.Popen([sys.executable, "-I", "-S", "-c", program, str(os.getpid()), str(deadline)],
                                   stdin=subprocess.PIPE, stdout=subprocess.PIPE)
        if process.stdout.read(1) != b"1":
            process.stdin.close()
            process.stdout.close()
            process.wait()
            raise ValueError("module deadline watchdog failed to start")
        process.stdout.close()
        return process

    @guarded
    def step(self, entry, *args, expected_before=None, expected_after=None, **kwargs):
        c = _collector
        c.driver()
        if not self.entered or self.closed or c.active is not None:
            raise ValueError("closed, unentered or nested lifecycle step")
        if self.sequence >= int(c.request["sampling"]["steps"]):
            raise ValueError("step sampling budget exceeded")
        if c.exploring:
            c.exploration.stepping(self)
        self.refusal = c.exploration.refusal if c.exploring else None
        self.outcome = "accepted" if self.refusal is not None else None
        self.entry = self.application(entry)
        self.before = self.read()
        if not equal(self.before, self.state):
            raise ValueError("state changed between transitions")
        expect(self.before, expected_before, module=True)
        self.calls = 0
        self.completed = self.entry_completed = False
        self.entry_frame = self.target_frame = self.await_frame = None
        self.entry_raised = self.await_raised = self.await_task = None
        self.pending_exceptions = {}
        self.threads = set()
        self.coroutines, self.tasks = set(), set()
        existing_coroutines = coroutine_objects()
        deadline = time.monotonic() + self.timeout
        watchdog = self.watchdog(deadline)
        loop = None
        c.active = self
        try:
            escaped = None
            try:
                result = self.entry.call(*args, **kwargs)
                if inspect.isawaitable(result):
                    loop = asyncio.new_event_loop()
                    asyncio.set_event_loop(loop)
                    loop.set_task_factory(self.task)
                    loop.set_exception_handler(self.async_error)
                    awaited = self.await_result(result)
                    self.await_frame = awaited.cr_frame
                    loop.run_until_complete(awaited)
            except BaseException as exc:
                self.escaped_refusal(exc)
                escaped = exc
            # Descendants can start more threads while their parents drain.
            joined = set()
            while self.threads - joined:
                for worker in list(self.threads - joined):
                    worker.join(max(0, deadline - time.monotonic()))
                    if worker.is_alive() or time.monotonic() >= deadline:
                        self.expire()
                    joined.add(worker)
            unfinished = any((value.cr_frame if type(value) is types.CoroutineType else value.gi_frame) is not None
                             for value in coroutine_objects() - existing_coroutines)
            if self.coroutines or unfinished:
                raise ValueError("module entry left unfinished coroutines")
            if any(not task.done() or task.cancelled()
                   or (task.exception() is not None and not (task is self.await_task and task.exception() is escaped))
                   for task in self.tasks):
                raise ValueError("module entry left unfinished or failed tasks")
            # An explored step the application refuses before its selected
            # call completes the entry and leaves the state as it was (#342).
            refused = c.exploring and self.calls == 0
            if (c.failed or (self.calls != 1 and not refused) or not (self.completed or refused)
                    or not (self.entry_completed or escaped is not None)):
                raise ValueError("incomplete selected call")
            self.entry.check()
            self.after = self.read()
            if refused and not equal(self.after, self.before):
                raise ValueError("a refused step changed the state without a selected call")
            expect(self.after, expected_after, module=True)
            if time.monotonic() >= deadline:
                self.expire()
            self.sequence += 1
            self.state = copied(self.after)
            c.write(self, self.entry, before=self.before, after=self.after)
        finally:
            if loop is not None:
                try:
                    pending = asyncio.all_tasks(loop)
                    for task in pending:
                        task.cancel()
                    if pending:
                        loop.run_until_complete(asyncio.gather(*pending, return_exceptions=True))
                finally:
                    loop.close()
                    asyncio.set_event_loop(None)
            if any(worker.is_alive() for worker in self.threads):
                self.expire()
            watchdog.stdin.close()
            if watchdog.wait() != 0:
                c.failed = True
            c.active = None
            self.entry_frame = self.target_frame = self.await_frame = None


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

    @guarded
    def step(self, entry, *args, expected_before=None, expected_after=None, **kwargs):
        _record(self).step(entry, *args, expected_before=expected_before, expected_after=expected_after, **kwargs)


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


@guarded
def module(target, *, fields, expected_initial=None, timeout=5.0):
    return _handle(_ModuleLifecycle(target, fields, expected_initial, timeout))


class Step:
    """A step the model's Next names, such as Advance or Submit(t) (#342).

    name is the step's name, exactly as Next names it. args is every argument
    list to try it with, as Next passes them, in the spec's encoding:
    [[{"$mv": "t1"}], [{"$mv": "t2"}]]; leave it out for a step with no
    arguments. take(life, state, *args) tries the step: it calls life.step()
    once, with the application entry that takes it, or returns without
    calling it only where the environment's bounds rule the step out. state
    is a copy of what the gate read back, so the driver can apply those
    bounds; changing it changes nothing. refusal=Refusal(ExceptionType, ...)
    declares an exact exception that may escape the entry before a selected
    call or any application thread, with unchanged read-back. Declared steps
    distinguish that refusal from every normal return, including read-only
    acceptance. Without a declaration, the existing outcome rule applies.
    """

    def __init__(self, name, take, args=None, *, refusal=None):
        self.name = name
        self.take = take
        self.args = args if args is not None else [[]]
        self.refusal = refusal


_step_name = re.compile(r"[A-Za-z_][A-Za-z0-9_]*")


class _Exploration:
    """The harness's own records of one exploration: every state read back,
    and the path of attempts that first reached it."""

    def __init__(self, collector, starts, steps):
        self.c = collector
        self.driver = collector.current_driver
        self.starts, self.steps = starts, steps
        self.instances = [(s, a) for s in range(len(steps)) for a in range(len(steps[s][2]))]
        self.starting = False
        self.made = []
        self.entering = self.leaving = None
        self.record = self.attempt = None
        self.refusal = None
        self.stepped = False
        self.nodes, self.index = [], {}
        self.roots = [None] * len(starts)
        self.start_index = None

    def fields(self, life, kind):
        if kind == "initial":
            return dict(start=self.start_index)
        if kind == "omitted":
            return dict(step=self.attempt["step"], args=self.attempt["args"])
        fields = dict(step=self.attempt["step"], args=self.attempt["args"],
                      replay=self.attempt["replay"], called=life.calls == 1)
        if self.refusal is not None:
            fields["outcome"] = life.outcome
            if life.outcome == "refused":
                fields["refusal"] = self.refusal.descriptor()
        return fields

    def stepping(self, life):
        if life is not self.record or self.attempt is None or self.stepped:
            raise ValueError("a step's take calls life.step() at most once, on the lifecycle it was given")
        self.stepped = True

    def key(self, state):
        return json.dumps(state, sort_keys=True)

    def add(self, state, start, edges, trail, level):
        n = len(self.nodes)
        key = self.key(state)
        self.index[key] = n
        self.nodes.append(dict(key=key, state=copied(state), start=start, edges=edges, trail=trail + [n], level=level, expanded=False))
        return n

    def make(self, r):
        self.made, self.starting = [], True
        try:
            handle = self.starts[r]()
        finally:
            self.starting = False
        if type(handle) is not Lifecycle or len(self.made) != 1 or self.c.handles.get(handle) is not self.made[0]:
            raise ValueError("a start returns the one new lifecycle it made")
        return handle, self.made[0]

    def take(self, handle, record, si, ai, replay):
        name, take, args, self.refusal = self.steps[si]
        self.record, self.stepped = record, False
        self.attempt = dict(step=name, args=copied(args[ai]), replay=replay)
        try:
            take(handle, copied(record.state), *copied(args[ai]))
        finally:
            self.record = None

    def try_step(self, r, node, si, ai):
        handle, record = self.make(r)
        self.entering, self.start_index = record, r
        try:
            record.__enter__()
        finally:
            self.entering = None
        key = self.key(record.state)
        if self.roots[r] is None:
            n = self.index.get(key)
            if n is None:
                n = self.add(record.state, r, [], [], 1)
            elif n in self.roots or self.nodes[n]["expanded"]:
                raise ValueError("two starts reached the same initial state")
            else:
                # An earlier start reached this start's initial state, and
                # hasn't tried a step there: this start tries them, directly.
                self.nodes[n].update(start=r, edges=[], trail=[n], level=1)
            self.roots[r] = n
        elif self.nodes[self.roots[r]]["key"] != key:
            raise ValueError("a start did not reproduce its initial state")
        if node is None:
            node = self.nodes[self.roots[r]]
        # Replay the path that first reached the state, then try one step.
        for j, (psi, pai, outcome) in enumerate(node["edges"]):
            self.take(handle, record, psi, pai, True)
            if not self.stepped:
                raise ValueError("a replay did not take the step it took before")
            if self.key(record.state) != self.nodes[node["trail"][j + 1]]["key"]:
                raise ValueError("the application is not deterministic: a replay reached another state")
            if record.outcome != outcome:
                raise ValueError("the application is not deterministic: a replay returned another outcome")
        self.take(handle, record, si, ai, False)
        if self.stepped:
            if self.key(record.state) not in self.index:
                self.add(record.state, r, node["edges"] + [(si, ai, record.outcome)], node["trail"], node["level"] + 1)
        else:
            record.omitted = True
            self.c.write(record, None, kind="omitted", state=copied(record.state))
        self.attempt = None
        self.leaving = record
        try:
            record.__exit__(None, None, None)
        finally:
            self.leaving = None

    def run(self):
        # Each start's initial state is tried from the start itself, and
        # every other state from the path that first reached it, once.
        for r in range(len(self.starts)):
            for si, ai in self.instances:
                self.try_step(r, None, si, ai)
            self.nodes[self.roots[r]]["expanded"] = True
        n = 0
        while n < len(self.nodes):
            node = self.nodes[n]
            if not node["expanded"]:
                for si, ai in self.instances:
                    self.try_step(node["start"], node, si, ai)
                node["expanded"] = True
            n += 1


@guarded
def explore(start, steps):
    """Explore every state the application reaches, trying every step there.

    start is a callable, or a list of them, each making a new lifecycle with
    functional(), instance() or module() and returning it unentered, in one
    initial state. For every state reached and every step and argument list,
    the harness makes a new lifecycle, replays through the application the
    path that first reached the state, and has the step's take try it. The
    gate reads every state back from the application, and the attempts it
    records are checked as a driver's are (D-0082, D-0085). A start that
    doesn't reproduce its state, or a replay that doesn't, fails the run.
    """
    c = _collector
    c.driver()
    if not c.exploring:
        raise ValueError("explore() needs an exhaustive manifest")
    if c.exploration is not None or c.explored or c.lives or c.active is not None:
        raise ValueError("explore() runs once, before any other lifecycle")
    starts = list(start) if type(start) in (list, tuple) else [start]
    if not starts or any(not callable(s) for s in starts):
        raise ValueError("explore() needs one or more start callables")
    if type(steps) not in (list, tuple) or not steps:
        raise ValueError("explore() needs a nonempty list of steps")
    records, names = [], set()
    for s in steps:
        if type(s) is not Step:
            raise ValueError("each step must be a Step")
        name, take, args = s.name, s.take, s.args
        if type(name) is not str or _step_name.fullmatch(name) is None or name in names:
            raise ValueError("step names must be distinct TLA+ identifiers")
        if not callable(take):
            raise ValueError("a step's take must be callable")
        if type(args) not in (list, tuple) or not args or any(type(a) not in (list, tuple) for a in args):
            raise ValueError("a step's args must be a nonempty list of argument lists")
        refusal = s.refusal
        if refusal is not None:
            if type(refusal) is not Refusal or type(refusal.attributes) is not dict:
                raise ValueError("a step's refusal must be a Refusal declaration")
            # Keep the validated declaration independently of driver objects.
            refusal = Refusal(refusal.exception_type, **refusal.attributes)
        names.add(name)
        records.append((name, take, [copied(list(a)) for a in args], refusal))
    declarations = []
    for name, _, args, refusal in records:
        declaration = dict(name=name, args=args)
        if refusal is not None:
            declaration["refusal"] = refusal.descriptor()
        declarations.append(declaration)
    c.declare(declarations)
    c.exploration = _Exploration(c, starts, records)
    try:
        c.exploration.run()
    finally:
        c.exploration = None
    c.explored = True


def run(request, emit, locations, codes, argv):
    # Docker starts the runner as namespace init, which cannot be killed by
    # a child in that namespace. Keep init as a supervisor and execute the
    # application in its child so the independent deadline can terminate it.
    # Exiting init also removes every descendant on container cancellation.
    if os.getpid() == 1:
        child = os.fork()
        if child:
            _, status = os.waitpid(child, 0)
            os._exit(os.WEXITSTATUS(status) if os.WIFEXITED(status) else 1)
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
    # Python 3.12 delivers opcode events only if some frame asked for them
    # before sys.settrace, so ask on this untraced frame first. Later
    # versions instrument a frame's code when capture_definition asks.
    if sys.version_info < (3, 13):
        sys._getframe().f_trace_opcodes = True
    sys.settrace(c.trace)
    sys.setprofile(c.profile)
    threading.settrace(c.trace)
    threading.setprofile(c.profile)
    previous_excepthook = threading.excepthook
    def thread_error(args):
        c.failed = True
        previous_excepthook(args)
    def start(worker, *args, **kwargs):
        return c.start_thread(worker, *args, **kwargs)
    threading.Thread.start = start
    threading.excepthook = thread_error
    try:
        exec(code, vars(module))
        if c.failed or not c.lives or any(not life.closed or (life.sequence == 0 and not life.omitted) for life in c.lives) or sys.gettrace() != c.trace or sys.getprofile() != c.profile:
            raise ValueError("driver did not complete bound lifecycles")
        if c.exploring and not c.explored:
            raise ValueError("an explored invocation's driver did not complete explore()")
    finally:
        sys.settrace(None)
        sys.setprofile(None)
        threading.settrace(None)
        threading.setprofile(None)
        threading.Thread.start = c.thread_start
        threading.excepthook = previous_excepthook
    emit(dict(type="end", count=c.count))
