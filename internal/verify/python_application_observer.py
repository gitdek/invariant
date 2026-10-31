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
import hashlib
import inspect
import math
import os
import random
import re
import signal
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


def expect(actual, expected, module=False):
    if expected is not None:
        if type(expected) is not dict or any(type(k) is not str for k in expected):
            raise ValueError("expected state must be a dictionary")
        for value in expected.values():
            copied(value) if module else scalar(value)
        if not equal(actual, expected):
            raise ValueError("expected state differs from application read-back")


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


def path_parts(path):
    # Identifiers deliberately use the portable ASCII subset shared with Go.
    if re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*|\[(?:[0-9]+|\*)\])*", path) is None:
        raise ValueError("invalid module read-back path")
    return re.findall(r"[A-Za-z_][A-Za-z0-9_]*|\[[0-9]+\]|\[\*\]", path)


def read_path(value, parts, ancestors=()):
    if not parts:
        return copied(value, ancestors)
    if any(value is old for old in ancestors):
        raise ValueError("cyclic read-back path")
    ancestors = ancestors + (value,)
    part, rest = parts[0], parts[1:]
    if part == "[*]":
        if type(value) not in (dict, list):
            raise ValueError("projection requires an exact dictionary or list")
        if type(value) is dict and any(type(k) is not str for k in value):
            raise ValueError("read-back dictionary keys must be strings")
        return [read_path(v, rest, ancestors) for v in (value.values() if type(value) is dict else value)]
    if part.startswith("["):
        if type(value) is not list:
            raise ValueError("index requires an exact list")
        return read_path(value[int(part[1:-1])], rest, ancestors)
    if type(value) is not dict:
        raise ValueError("key requires an exact dictionary")
    return read_path(value[part], rest, ancestors)


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
    def __init__(self, collector, function, module=False, entry=False):
        self.module = module
        self.receiver = None
        if type(function) is types.MethodType:
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
        if (type(node) not in (ast.FunctionDef, ast.AsyncFunctionDef) or (not module and (type(node) is ast.AsyncFunctionDef or node.decorator_list)) or ".<locals>." in self.component):
            raise ValueError("decorated and nested declarations are unsupported")
        self.symbol = self.component.split("#", 1)[1].split("@", 1)[0]
        if module and (self.receiver is not None or "." in self.symbol):
            raise ValueError("module form requires a module function")
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
        self.opaque_defaults = module and entry
        if self.opaque_defaults:
            if function not in collector.defaults:
                raise ValueError("entry defaults were not observed at declaration")
            self.defaults, self.kwdefaults, self.kwitems = collector.defaults[function]
        else:
            try:
                defaults = tuple(scalar(ast.literal_eval(v)) for v in node.args.defaults)
                kwdefaults = {k.arg: scalar(ast.literal_eval(v)) for k, v in zip(node.args.kwonlyargs, node.args.kw_defaults) if v is not None}
            except (ValueError, TypeError):
                raise ValueError("nonliteral or mutable defaults are unsupported")
            self.defaults, self.kwdefaults = defaults, kwdefaults
        collector.receivers[self] = self.receiver
        self.check()

    def check(self):
        if self.module and _collector.functions.get(self.code) is not self.function:
            raise ValueError("replaced lexical function identity")
        if self not in _collector.receivers or self.receiver is not _collector.receivers[self]:
            raise ValueError("replaced receiver binding")
        if self.opaque_defaults:
            valid = self.function.__defaults__ is self.defaults and self.function.__kwdefaults__ is self.kwdefaults
            current = self.function.__kwdefaults__ or {}
            valid = valid and current.keys() == self.kwitems.keys() and all(current[k] is v for k, v in self.kwitems.items())
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
        self.defaults = {}
        self.functions = {}
        # The first reason a handle was misused, kept for the diagnostic in
        # case the driver catches the error.
        self.misuse = None
        self.writers = (_Lifecycle.__enter__.__wrapped__.__code__, _Lifecycle.step.__wrapped__.__code__, _Module.step.__wrapped__.__code__)

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
        while frame is not None:
            if os.path.abspath(frame.f_code.co_filename) == os.path.join(self.request["root"], self.request["check"]["source"]):
                component, digest, _ = self.lexical(frame.f_code)
                selected = self.request["check"]
                if self.request.get("legacy"):
                    selected = next((d for d in self.request["drivers"] if d["id"] == component), {})
                if component == selected.get("id"):
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

    def write(self, life, entry, **state):
        caller = sys._getframe(1)
        if not any(caller.f_code is code for code in self.writers) or caller.f_globals is not globals():
            self.failed = True
            raise ValueError("observation did not come from a lifecycle read-back")
        self.count += 1
        if isinstance(life, _Module):
            state["form"] = "module"
        self.emit(dict(type="observation", kind="initial" if life.sequence == 0 else "transition",
                       run=life.run, instance=life.identity, sequence=life.sequence,
                       key=self.request["key"], target=life.target.component,
                       driver=self.current_driver["id"], source_sha256=life.target.digest,
                       driver_sha256=self.current_driver["source_sha256"],
                       symbol=life.target.symbol, entry=entry.component, entry_sha256=entry.digest,
                       readback=life.fields, **state))

    def trace(self, frame, event, arg):
        try:
            active = self.active
            if threading.get_ident() != self.thread and (not isinstance(active, _Module) or not active.owned(frame)):
                raise ValueError("concurrent execution is unsupported")
            if event == "call" or frame.f_code.co_filename.startswith(self.request["root"] + os.sep):
                values = ()
                if frame.f_code.co_name == "<module>" and event in ("line", "return"):
                    values = list(frame.f_globals.values())
                elif event == "call":
                    # Registration decorators receive the lexical function
                    # before its module name is bound. Remember that object,
                    # not a replacement a decorator might return.
                    values = list(frame.f_locals.values())
                for value in values:
                    if type(value) is types.FunctionType and value.__code__.co_filename.startswith(self.request["root"] + os.sep) and value not in self.defaults:
                        self.defaults[value] = (value.__defaults__, value.__kwdefaults__, (value.__kwdefaults__ or {}).copy())
                        self.functions.setdefault(value.__code__, value)
            if event == "exception" and frame.f_code in self.driver_codes:
                self.failed = True
            if active is not None:
                active.observe(frame, event, arg)
        except BaseException:
            self.failed = True
            raise
        return self.trace

    def profile(self, frame, event, arg):
        # Refuse thread creation even if the worker never calls the target.
        if event == "c_call" and (arg is _thread.start_new_thread or arg is getattr(_thread, "start_joinable_thread", None)):
            if isinstance(self.active, _Module) and self.active.starting(frame):
                return
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
        selected = c.request["target"]
        if c.request.get("legacy"):
            selected = next((t for t in c.request["targets"] if t["id"] == self.target.component), {})
        if self.target.component != selected.get("id") or self.target.digest != selected.get("source_sha256"):
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
        obj = self.target.owner if isinstance(self, _Module) else (self if self.functional else self.target.receiver)
        if not any(obj is old for old in c.instances):
            c.instances.append(obj)
        self.identity = next(i + 1 for i, old in enumerate(c.instances) if old is obj)
        self.check_target()
        if self.functional:
            self.state = {next(iter(self.fields)): scalar(self.initializer.call())}
        else:
            self.state = self.read()
        expect(self.state, self.expected, isinstance(self, _Module))
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
        self.delegate_called = not bool(c.request.get('delegates'))
        self.entry_frame = self.target_frame = None
        c.active = self
        try:
            args = [next(iter(self.state.values()))] if self.functional else []
            self.entry.call(*args)
            if c.failed or self.calls != 1 or self.after is None or not self.delegate_called:
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


class _Module(_Lifecycle):
    def __init__(self, target, fields, expected, timeout):
        c = _collector
        c.driver()
        if c.active is not None:
            raise ValueError("nested lifecycles during a step are unsupported")
        if type(timeout) not in (int, float) or not math.isfinite(timeout) or not 0 < timeout <= 60:
            raise ValueError("module timeout must be positive, finite and at most 60 seconds")
        self.timeout = timeout
        self.target = Binding(c, target, module=True)
        c.targets[self] = self.target
        selected = c.request["target"]
        if c.request.get("legacy"):
            selected = next((t for t in c.request["targets"] if t["id"] == self.target.component), {})
        if self.target.component != selected.get("id") or self.target.digest != selected.get("source_sha256"):
            raise ValueError("target differs from selected staged declaration")
        if type(fields) is not dict or not fields or any(type(k) is not str or not k or type(v) is not str for k, v in fields.items()):
            raise ValueError("read-back requires a nonempty declarative field mapping")
        self.fields = fields.copy()
        self.paths = {k: path_parts(v) for k, v in fields.items()}
        self.functional, self.initializer = False, None
        self.expected = expected
        self.entered = self.closed = False
        self.sequence = 0

    def application(self, function):
        binding = Binding(_collector, function, module=True, entry=True)
        if binding.source == _collector.request["check"]["source"] or binding.component == self.target.component:
            raise ValueError("distinct surrounding application entry required")
        return binding

    def read(self):
        self.check_target()
        return {key: read_path(self.target.globals, parts) for key, parts in self.paths.items()}

    def owned(self, frame):
        # Bootstrap frames retain the actual Thread object even before its
        # registration in threading._active and after its removal there.
        while frame is not None:
            if frame.f_globals is vars(threading) and frame.f_code in (threading.Thread._bootstrap.__code__, threading.Thread._bootstrap_inner.__code__):
                return any(frame.f_locals.get("self") is worker for worker in self.workers)
            frame = frame.f_back
        return False

    def beneath_entry(self, frame):
        while frame is not None:
            if frame is self.entry_frame:
                return True
            frame = frame.f_back
        return False

    def starting(self, frame):
        caller = frame
        while frame is not None:
            if frame.f_code is threading.Thread.start.__code__ and frame.f_globals is vars(threading):
                worker = frame.f_locals.get("self")
                if type(worker) is not threading.Thread:
                    return False
                if not (self.owned(caller) or self.beneath_entry(caller) or (threading.get_ident() == _collector.thread and self.awaiting)):
                    return False
                if any(worker is old for old in self.workers):
                    return False
                self.workers.append(worker)
                return True
            frame = frame.f_back
        return False

    def observe(self, frame, event, arg):
        self.check_target()
        if event == "call" and frame.f_code is self.entry.code:
            self.entry.frame(frame)
            if self.entry_frame is not None and frame is not self.entry_frame:
                raise ValueError("reentrant application entry")
            self.entry_frame = frame
        if event == "call" and frame.f_code is self.target.code:
            self.target.frame(frame)
            if frame is not self.target_frame:
                if self.target_frame is not None or not (self.owned(frame) or self.beneath_entry(frame) or self.awaiting):
                    raise ValueError("step requires exactly one nonnested selected call")
                self.target_frame = frame
        if event == "exception" and (frame is self.entry_frame or frame is self.target_frame):
            if not issubclass(arg[0], (StopIteration, StopAsyncIteration)):
                raise ValueError("application call raised an exception")
        if event == "return" and (frame is self.entry_frame or frame is self.target_frame):
            # Coroutine suspension also emits return/call trace events. Only
            # a return opcode completes a selected invocation.
            complete = dis.opname[frame.f_code.co_code[frame.f_lasti]] in ("RETURN_VALUE", "RETURN_CONST")
            if frame is self.target_frame and complete:
                self.completed = True

    @guarded
    def step(self, entry, *args, expected_before=None, expected_after=None, **kwargs):
        c = _collector
        c.driver()
        if not self.entered or self.closed or c.active is not None:
            raise ValueError("closed, unentered or nested lifecycle step")
        if self.sequence >= int(c.request["sampling"]["steps"]):
            raise ValueError("step sampling budget exceeded")
        self.entry = self.application(entry)
        self.before = self.read()
        if not equal(self.before, self.state):
            raise ValueError("state changed between transitions")
        expect(self.before, expected_before, True)
        self.entry_frame = self.target_frame = None
        self.completed = self.awaiting = False
        self.workers = []
        loop = None

        def expired(signum, frame):
            # Python cannot safely kill a worker, and cancellation can be
            # caught. End this isolated execution without sealing evidence.
            os.write(2, b"module observation timeout\n")
            os._exit(1)

        old_handler = signal.signal(signal.SIGALRM, expired)
        signal.setitimer(signal.ITIMER_REAL, self.timeout)
        deadline = time.monotonic() + self.timeout
        c.active = self
        try:
            result = self.entry.call(*args, **kwargs)
            if inspect.isawaitable(result):
                loop = asyncio.new_event_loop()
                asyncio.set_event_loop(loop)
                self.awaiting = True
                loop.run_until_complete(result)
                self.awaiting = False
                if asyncio.all_tasks(loop):
                    raise ValueError("unfinished application coroutine")
                loop.run_until_complete(loop.shutdown_asyncgens())
                if asyncio.all_tasks(loop):
                    raise ValueError("unfinished application coroutine during cleanup")
                loop.close()
                asyncio.set_event_loop(None)
                loop = None
            index = 0
            while index < len(self.workers):
                self.workers[index].join(max(0, deadline - time.monotonic()))
                if self.workers[index].is_alive() or time.monotonic() >= deadline:
                    expired(None, None)
                index += 1
            if c.failed or not self.completed:
                raise ValueError("incomplete selected call")
            self.entry.check()
            self.after = self.read()
            expect(self.after, expected_after, True)
            self.sequence += 1
            self.state = self.after
            c.write(self, self.entry, before=self.before, after=self.after)
        finally:
            # Cleanup is inside the same deadline. A canceled coroutine that
            # refuses to finish cannot hang the process or earn evidence.
            try:
                if loop is not None:
                    pending = asyncio.all_tasks(loop)
                    for task in pending:
                        task.cancel()
                    if pending:
                        loop.run_until_complete(asyncio.gather(*pending, return_exceptions=True))
                    loop.run_until_complete(loop.shutdown_asyncgens())
                    loop.close()
                    asyncio.set_event_loop(None)
                if any(worker.is_alive() for worker in self.workers):
                    os.write(2, b"incomplete module workers\n")
                    os._exit(1)
            finally:
                c.active = None
                self.entry_frame = self.target_frame = None
                signal.setitimer(signal.ITIMER_REAL, 0)
                signal.signal(signal.SIGALRM, old_handler)


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
    return _handle(_Module(target, fields, expected_initial, timeout))


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
    threading.setprofile(c.profile)
    old_excepthook = threading.excepthook

    def thread_error(args):
        c.failed = True
        old_excepthook(args)

    threading.excepthook = thread_error
    try:
        exec(code, vars(module))
        if c.failed or not c.lives or any(not life.closed or life.sequence == 0 for life in c.lives) or sys.gettrace() != c.trace or sys.getprofile() != c.profile:
            raise ValueError("driver did not complete bound lifecycles")
    finally:
        sys.settrace(None)
        sys.setprofile(None)
        threading.settrace(None)
        threading.setprofile(None)
        threading.excepthook = old_excepthook
    emit(dict(type="end", count=c.count))
