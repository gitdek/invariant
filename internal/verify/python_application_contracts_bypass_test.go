package verify

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
)

// #355's second review: method operations whose code other classes share,
// exceptions that escape calls the runner doesn't count, assertions that
// setUp, tearDown or a cleanup makes, callables handed to an assertion's own
// work, look-alikes compiled from the staged source and spoofed receivers
// support nothing, and what held still holds. Each case declares one
// contract, runs it alone on the real embedded runner on the host's Python,
// and checks its standing.

const pbtState = `import asyncio


def save(task):
    task['saved'] = True
    return task


class StoreError(Exception):
    pass


def lookup(d, k):
    return d[k]


def walk(n):
    for i in range(n):
        yield i


def walk_fail(n):
    yield n
    raise StoreError('walk failed')


async def fetch(x):
    await asyncio.sleep(0)
    if x < 0:
        raise StoreError('negative')
    return x


def helper_double(x):
    return x * 2


def compute(x):
    return helper_double(x) + 1
`

const pbtTests = ptcTestSource + `

import asyncio
import base64
import binascii
import collections
import enum
import fractions
import functools
import ipaddress
import operator
import re
import threading

from store import state


class SetUpAssertTests(unittest.TestCase):
    def setUp(self):
        self.assertTrue(True)

    def test_only_calls(self):
        json.dumps({'a': 1})


class TearDownAssertTests(unittest.TestCase):
    def tearDown(self):
        self.assertIsNotNone(self)

    def test_only_calls(self):
        json.dumps({'a': 1})


class CleanupAssertTests(unittest.TestCase):
    def test_only_calls(self):
        self.addCleanup(self.assertTrue, True)
        json.dumps({'a': 1})


class HandedMsgTests(unittest.TestCase):
    def test_len_via_msg(self):
        self.assertEqual([1, 2, 3], [1, 2, 3], msg=len)

    def test_counter_via_msg(self):
        self.assertCountEqual('aab', 'aba', msg=collections.Counter)

    def test_compile_via_msg(self):
        self.assertRegex('aaa', 'a+', msg=re.compile)


@functools.total_ordering
class FakeRank:
    def __init__(self, n):
        self.n = n

    def __eq__(self, other):
        return self.n == other.n

    def __lt__(self, other):
        return self.n < other.n


class SharedCodeTests(unittest.TestCase):
    def test_fake_rank_gt(self):
        self.assertTrue(FakeRank(2) > FakeRank(1))

    def test_fake_enum_new(self):
        class FakeStatus(enum.IntEnum):
            OK = 200
        self.assertEqual(FakeStatus(200), 200)

    def test_ipv6_lt(self):
        self.assertTrue(ipaddress.IPv6Address('::1') < ipaddress.IPv6Address('::2'))

    def test_fraction_mul(self):
        self.assertEqual(fractions.Fraction(1, 2) * 2, 1)

    def test_plain_dict_pop(self):
        self.assertEqual({'a': 1}.pop('a'), 1)


class ThroughCTests(unittest.TestCase):
    def test_partial(self):
        self.assertEqual(functools.partial(json.dumps, sort_keys=True)({'b': 1, 'a': 2}), '{"a": 2, "b": 1}')

    def test_map(self):
        self.assertEqual(list(map(json.dumps, [1])), ['1'])

    def test_sorted_key(self):
        self.assertEqual(sorted([2, 1], key=json.dumps), [1, 2])

    def test_sorted_key_raises(self):
        with self.assertRaises(TypeError):
            sorted([object()], key=json.dumps)

    def test_methodcaller_pop(self):
        self.assertEqual(operator.methodcaller('pop', 'a')({'a': 1}), 1)

    def test_map_pop(self):
        d = {'a': 1}
        self.assertEqual(list(map(d.pop, ['a'])), [1])


class GeneratorTests(unittest.TestCase):
    def test_walk(self):
        self.assertEqual(list(state.walk(3)), [0, 1, 2])

    def test_walk_fail_escapes(self):
        gen = state.walk_fail(1)
        self.assertEqual(next(gen), 1)
        with self.assertRaises(state.StoreError):
            next(gen)

    def test_walk_fail_unittest_iterates(self):
        self.assertEqual(next(state.walk_fail(1)), 1)
        self.assertRaises(state.StoreError, list, state.walk_fail(1))

    def test_walk_fail_next(self):
        gen = state.walk_fail(1)
        self.assertEqual(next(gen), 1)
        self.assertRaises(state.StoreError, next, gen)

    def test_fetch(self):
        self.assertEqual(asyncio.run(state.fetch(1)), 1)

    def test_fetch_fail(self):
        with self.assertRaises(state.StoreError):
            asyncio.run(state.fetch(-1))


class AsyncTests(unittest.IsolatedAsyncioTestCase):
    async def test_fetch(self):
        self.assertEqual(await state.fetch(1), 1)


class ThreadTests(unittest.TestCase):
    def test_thread_only(self):
        out = []
        t = threading.Thread(target=lambda: out.append(json.dumps({'a': 1})))
        t.start()
        t.join()
        self.assertEqual(out, ['{"a": 1}'])

    def test_undeclared_in_thread(self):
        self.assertEqual(base64.b64decode('YQ=='), b'a')
        errors = []

        def worker():
            try:
                base64.b64decode('a')
            except Exception as e:
                errors.append(e)
        t = threading.Thread(target=worker)
        t.start()
        t.join()
        self.assertIsInstance(errors[0], binascii.Error)


class SwallowTests(unittest.TestCase):
    def test_swallow(self):
        try:
            base64.b64decode('a')
        except Exception:
            pass
        self.assertTrue(True)


class SetUpRaiseTests(unittest.TestCase):
    def setUp(self):
        try:
            base64.b64decode('a')
        except binascii.Error as e:
            self.error = e

    def test_decodes(self):
        self.assertEqual(base64.b64decode('YQ=='), b'a')
        self.assertIsInstance(self.error, binascii.Error)


class AliasTests(unittest.TestCase):
    def test_alias_decode_error(self):
        with self.assertRaises(json.JSONDecodeError):
            json.loads('{')

    def test_ioerror_alias(self):
        with self.assertRaises(FileNotFoundError):
            open('/nonexistent/invariant-review')


class LazyExsuresTests(unittest.TestCase):
    def test_patched_exception_class(self):
        with mock.patch('store.state.StoreError', KeyError):
            with self.assertRaises(KeyError):
                state.lookup({}, 'x')
        self.assertEqual(state.lookup({'x': 1}, 'x'), 1)


class RecompileTests(unittest.TestCase):
    def test_recompiled_compute(self):
        with open(state.__file__) as f:
            src = f.read()
        ns = {}
        exec(compile(src, state.__file__, 'exec'), ns)
        ns['helper_double'] = lambda x: 0
        self.assertEqual(ns['compute'](5), 1)


class SpyTests(unittest.TestCase):
    def test_spy_hides_raise(self):
        self.assertEqual(state.lookup({'x': 1}, 'x'), 1)
        with mock.patch.object(state, 'lookup', wraps=state.lookup) as spy:
            self.assertRaises(KeyError, state.lookup, {}, 'missing')
        self.assertEqual(spy.call_count, 1)


class ImportTests(unittest.TestCase):
    def test_import_statement(self):
        import json as again
        self.assertIs(again, json)


class GeneratorCloseTests(unittest.TestCase):
    def test_unittest_iterates_then_del(self):
        g = state.walk(3)
        self.assertIn(0, g)
        del g

    def test_unittest_iterates(self):
        g = state.walk(3)
        self.assertIn(0, g)


class SpoofedVersion:
    __class__ = property(lambda self: tinyver.Version)
    parts = (1,)


class SpoofTests(unittest.TestCase):
    def test_spoofed_receiver(self):
        self.assertFalse(tinyver.Version.__lt__(SpoofedVersion(), SpoofedVersion()))
`

// pbtContracts declares each contract, with an optional Exsures expression,
// in a module that imports what those expressions name.
func pbtContracts(name, exsures string) string {
	b := strings.Builder{}
	b.WriteString(ptcContractSource)
	b.WriteString("\nfrom json import JSONDecodeError as Bad\nimport json as j\nfrom store.state import StoreError\n")
	line := ""
	if exsures != "" {
		line = "    Exsures(" + exsures + ", True)\n"
	}
	fmt.Fprintf(&b, "\ndef %s(x: object) -> object:\n    Requires(True)\n%s    ...\n", name, line)
	return b.String()
}

func pbtApp(symbol string) project.ComponentOperation {
	return project.ComponentOperation{Kind: "application", Source: "store/state.py", Symbol: symbol}
}

func TestPythonApplicationContractTestsRefuseTheSecondReviewsBypasses(t *testing.T) {
	const (
		noAssertion = "it made no assertion that passed, or one failed"
		noCall      = "the test itself made no call into the bound implementation"
		unresolved  = "its run didn't resolve the operation's implementation"
		undeclared  = "which its contract's Exsures doesn't declare"
	)
	for _, c := range []struct {
		name      string
		operation project.ComponentOperation
		test      string
		exsures   string
		reason    string // empty where the contract stays tested
	}{
		// Assertions setUp, tearDown or a cleanup makes aren't the test's.
		{"setUp's assertion", prvStd("json", "dumps"), "SetUpAssertTests.test_only_calls", "", noAssertion},
		{"tearDown's assertion", prvStd("json", "dumps"), "TearDownAssertTests.test_only_calls", "", noAssertion},
		{"a cleanup's assertion", prvStd("json", "dumps"), "CleanupAssertTests.test_only_calls", "", noAssertion},
		// Only the callable forms of assertRaises and assertWarns call what
		// they're handed.
		{"len as an assertion's msg", prvStd("builtins", "len"), "HandedMsgTests.test_len_via_msg", "", noCall},
		{"Counter as an assertion's msg", prvStd("collections", "Counter"), "HandedMsgTests.test_counter_via_msg", "", noCall},
		{"compile as an assertion's msg", prvStd("re", "compile"), "HandedMsgTests.test_compile_via_msg", "", noCall},
		// A method named on its class must be the class's own.
		{"total_ordering's __gt__", prvStd("ipaddress", "IPv4Address.__gt__"), "SharedCodeTests.test_fake_rank_gt", "", unresolved},
		{"Enum's __new__", prvStd("http", "HTTPStatus.__new__"), "SharedCodeTests.test_fake_enum_new", "", unresolved},
		{"an inherited __lt__", prvStd("ipaddress", "IPv4Address.__lt__"), "SharedCodeTests.test_ipv6_lt", "", unresolved},
		{"a factory's __add__", prvStd("fractions", "Fraction.__add__"), "SharedCodeTests.test_fraction_mul", "", unresolved},
		{"dict's pop named on Counter", prvStd("collections", "Counter.pop"), "SharedCodeTests.test_plain_dict_pop", "", unresolved},
		{"the import statement's __import__", prvStd("builtins", "__import__"), "ImportTests.test_import_statement", "", unresolved},
		// What escapes any call into the implementation is checked: through
		// C, through unittest, from setUp, through a spy and in a thread.
		{"sorted's key raising", prvStd("json", "dumps"), "ThroughCTests.test_sorted_key_raises", "", "json.dumps raised builtins.TypeError, " + undeclared},
		{"a generator raising", pbtApp("walk_fail"), "GeneratorTests.test_walk_fail_escapes", "", "raised store.state.StoreError, " + undeclared},
		{"a generator unittest lists", pbtApp("walk_fail"), "GeneratorTests.test_walk_fail_unittest_iterates", "", "raised store.state.StoreError, " + undeclared},
		{"a generator handed to next", pbtApp("walk_fail"), "GeneratorTests.test_walk_fail_next", "", "raised store.state.StoreError, " + undeclared},
		{"a coroutine raising", pbtApp("fetch"), "GeneratorTests.test_fetch_fail", "", "raised store.state.StoreError, " + undeclared},
		{"a worker thread's raise", prvStd("base64", "b64decode"), "ThreadTests.test_undeclared_in_thread", "", "base64.b64decode raised binascii.Error, " + undeclared},
		{"a swallowed raise", prvStd("base64", "b64decode"), "SwallowTests.test_swallow", "", "base64.b64decode raised binascii.Error, " + undeclared},
		{"setUp's raise", prvStd("base64", "b64decode"), "SetUpRaiseTests.test_decodes", "", "base64.b64decode raised binascii.Error, " + undeclared},
		{"a spy's raise", pbtApp("lookup"), "SpyTests.test_spy_hides_raise", "", "raised builtins.KeyError, " + undeclared},
		// A declared class is pinned before tests run, so patching its name
		// changes nothing.
		{"a patched exception class", pbtApp("lookup"), "LazyExsuresTests.test_patched_exception_class", "StoreError", "raised builtins.KeyError, " + undeclared},
		// C called from C, and calls only a thread makes, count nothing.
		{"methodcaller's pop", prvStd("builtins", "dict.pop"), "ThroughCTests.test_methodcaller_pop", "", noCall},
		{"map's pop", prvStd("builtins", "dict.pop"), "ThroughCTests.test_map_pop", "", noCall},
		{"a thread's only call", prvStd("json", "dumps"), "ThreadTests.test_thread_only", "", noCall},
		// Look-alikes: the staged source compiled again, a generator's close
		// and a receiver whose __class__ lies.
		{"the staged source compiled again", pbtApp("compute"), "RecompileTests.test_recompiled_compute", "", noCall},
		{"a generator closed after unittest iterated it", pbtApp("walk"), "GeneratorCloseTests.test_unittest_iterates_then_del", "", noCall},
		{"a generator only unittest iterated", pbtApp("walk"), "GeneratorCloseTests.test_unittest_iterates", "", noCall},
		{"a spoofed receiver", prvDep("Version"), "SpoofTests.test_spoofed_receiver", "", noCall},
		// What held still holds.
		{"through partial", prvStd("json", "dumps"), "ThroughCTests.test_partial", "", ""},
		{"through map", prvStd("json", "dumps"), "ThroughCTests.test_map", "", ""},
		{"through sorted's key", prvStd("json", "dumps"), "ThroughCTests.test_sorted_key", "", ""},
		{"a generator", pbtApp("walk"), "GeneratorTests.test_walk", "", ""},
		{"a coroutine", pbtApp("fetch"), "GeneratorTests.test_fetch", "", ""},
		{"an async test", pbtApp("fetch"), "AsyncTests.test_fetch", "", ""},
		{"an Exsures alias", prvStd("json", "loads"), "AliasTests.test_alias_decode_error", "Bad", ""},
		{"an Exsures module alias", prvStd("json", "loads"), "AliasTests.test_alias_decode_error", "j.decoder.JSONDecodeError", ""},
		{"a builtin Exsures alias", prvStd("builtins", "open"), "AliasTests.test_ioerror_alias", "IOError", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			root, p := ptcFixture(t)
			pa310Write(t, root, "store/state.py", pbtState)
			pa310Write(t, root, ptcTests, pbtTests)
			pa310Write(t, root, ptcContracts, pbtContracts("contract", c.exsures))
			p.Manifest.Components.TrustedContracts = []project.ComponentContract{ptcContract("probe", c.operation, "contract", c.test)}
			pa310Save(t, p)
			r := ptcRun(t, ptcSession(t, p, prvLibraries(t, false), "1.0"))
			standing, why := ptcStanding(t, r, "probe"), ptcDiagnostic(r, UnsupportedContract, "probe")
			if c.reason == "" && standing.Support != "tested" {
				t.Fatalf("real use lost its support: %q; executions %+v", why, r.Application.Executions)
			}
			if c.reason != "" && (standing.Support != "untested" || !strings.Contains(why, c.reason)) {
				t.Fatalf("support %s, %q; want untested, %q", standing.Support, why, c.reason)
			}
		})
	}
}

// #355's final pass: a wrapped method bound at module level counts only on
// its own object, an application method only on its class, a transplanted
// __new__ only for its class, the staged source only under the module name
// its path gives, and a worker thread inside an assertion hides none the
// test method makes.

const pbfTinyver = `
import functools


def logged(f):
    @functools.wraps(f)
    def wrapper(*args, **kwargs):
        return f(*args, **kwargs)
    return wrapper


class Parser:
    def __init__(self, strict):
        self.strict = strict

    @logged
    def parse(self, text):
        return text.upper()

    def plain(self, text):
        return text.lower()


_default = Parser(strict=True)
parse_strict = _default.parse
plain_strict = _default.plain


class Token:
    def __new__(cls, text):
        obj = object.__new__(cls)
        obj.text = text
        return obj
`

const pbfTests = pbtTests + `

from store import kv


class BoundTests(unittest.TestCase):
    def test_wrapped_own_object(self):
        self.assertEqual(tinyver.parse_strict('abc'), 'ABC')

    def test_wrapped_other_instance(self):
        self.assertEqual(tinyver.Parser(strict=False).parse('abc'), 'ABC')

    def test_plain_other_instance(self):
        self.assertEqual(tinyver.Parser(strict=False).plain('ABC'), 'abc')


class NewTests(unittest.TestCase):
    def test_real(self):
        self.assertEqual(tinyver.Token('t').text, 't')

    def test_transplanted(self):
        class Fake:
            __new__ = tinyver.Token.__new__
        self.assertEqual(Fake('t').text, 't')


class AppMethodTests(unittest.TestCase):
    def test_real(self):
        self.assertEqual(kv.Store().save('a', 1), 1)

    def test_transplanted(self):
        class Fake:
            items = {}
            save = kv.Store.save
        self.assertEqual(Fake().save('a', 1), 1)


class SecondModuleTests(unittest.TestCase):
    def test_second_module(self):
        import importlib.util
        import sys as _sys
        spec = importlib.util.spec_from_file_location('store_copy', state.__file__)
        mod = importlib.util.module_from_spec(spec)
        _sys.modules['store_copy'] = mod
        spec.loader.exec_module(mod)
        mod.helper_double = lambda x: 0
        self.assertEqual(mod.compute(5), 1)


class ThreadedAssertionTests(unittest.TestCase):
    def test_worker_inside_an_assertion(self):
        self.assertTrue(json.dumps({'a': 1}))
        release = threading.Event()
        inside = threading.Event()

        def blocker():
            inside.set()
            release.wait(5)
            raise ValueError('done')

        def worker():
            self.assertRaises(ValueError, blocker)
        t = threading.Thread(target=worker)
        t.start()
        inside.wait(5)
        try:
            self.assertEqual(1, 2)
        except AssertionError:
            pass
        release.set()
        t.join()
`

func TestPythonApplicationContractTestsRefuseTheFinalPassBypasses(t *testing.T) {
	const (
		noAssertion = "it made no assertion that passed, or one failed"
		noCall      = "the test itself made no call into the bound implementation"
	)
	method := func(symbol string) project.ComponentOperation {
		return project.ComponentOperation{Kind: "application", Source: "store/kv.py", Symbol: symbol}
	}
	for _, c := range []struct {
		name      string
		operation project.ComponentOperation
		test      string
		reason    string // empty where the contract stays tested
	}{
		{"a wrapped method on its own object", prvDep("parse_strict"), "BoundTests.test_wrapped_own_object", ""},
		{"a wrapped method bound to another instance", prvDep("parse_strict"), "BoundTests.test_wrapped_other_instance", noCall},
		{"a method bound to another instance", prvDep("plain_strict"), "BoundTests.test_plain_other_instance", noCall},
		{"its own __new__", prvDep("Token.__new__"), "NewTests.test_real", ""},
		{"a transplanted __new__", prvDep("Token.__new__"), "NewTests.test_transplanted", noCall},
		{"an application method", method("Store.save"), "AppMethodTests.test_real", ""},
		{"an application method on another class", method("Store.save"), "AppMethodTests.test_transplanted", noCall},
		{"the staged source loaded as a second module", pbtApp("compute"), "SecondModuleTests.test_second_module", noCall},
		{"a worker thread inside an assertion", prvStd("json", "dumps"), "ThreadedAssertionTests.test_worker_inside_an_assertion", noAssertion},
	} {
		t.Run(c.name, func(t *testing.T) {
			root, p := ptcFixture(t)
			pa310Write(t, root, "store/state.py", pbtState)
			pa310Write(t, root, "store/kv.py", "class Store:\n    def __init__(self):\n        self.items = {}\n\n    def save(self, key, value):\n        self.items[key] = value\n        return value\n")
			pa310Write(t, root, ptcTests, pbfTests)
			pa310Write(t, root, ptcContracts, pbtContracts("contract", ""))
			p.Manifest.Components.TrustedContracts = []project.ComponentContract{ptcContract("probe", c.operation, "contract", c.test)}
			pa310Save(t, p)
			r := ptcRun(t, ptcSession(t, p, prvLibrariesWith(t, prvInit+pbfTinyver, false), "1.0"))
			standing, why := ptcStanding(t, r, "probe"), ptcDiagnostic(r, UnsupportedContract, "probe")
			if c.reason == "" && standing.Support != "tested" {
				t.Fatalf("real use lost its support: %q; executions %+v", why, r.Application.Executions)
			}
			if c.reason != "" && (standing.Support != "untested" || !strings.Contains(why, c.reason)) {
				t.Fatalf("support %s, %q; want untested, %q", standing.Support, why, c.reason)
			}
		})
	}
}
