package verify

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
)

// #355's review: backing tests that reach only a stub, a substitute or
// nothing must not support their contracts, whoever makes the calls the
// runner sees, and real use must still count. Each case runs the real
// embedded runner on the host's Python.

const prvTests = ptcTestSource + `

import collections
import dataclasses
import decimal
import enum
import functools
import re


class CounterTests(unittest.TestCase):
    def test_substitute_counter(self):
        fake = lambda items: {'a': 2}
        self.assertCountEqual(fake('aa'), ['a'])

    def test_real_counter(self):
        self.assertEqual(collections.Counter('aab')['a'], 2)


class LenTests(unittest.TestCase):
    def test_substitute_len(self):
        fake_len = lambda items: 3
        self.assertEqual(fake_len([1, 2, 3]), 3)

    def test_real_len(self):
        self.assertEqual(len([1, 2, 3]), 3)


class AttrTests(unittest.TestCase):
    def test_nothing(self):
        pass


class MapTests(unittest.TestCase):
    def test_ordered_pop(self):
        d = collections.OrderedDict(a=1)
        self.assertEqual(d.pop('a'), 1)

    def test_real_pop(self):
        d = {'a': 1}
        self.assertEqual(d.pop('a'), 1)
        self.assertEqual(d, {})

    def test_fake_get(self):
        class FakeMap:
            def get(self, key, default=None):
                return 1
        self.assertEqual(FakeMap().get('a'), 1)


class RegexTests(unittest.TestCase):
    def test_substitute_regex(self):
        self.assertRegex('aaa', 'a+')

    def test_handed_compile(self):
        self.assertRaises(re.error, re.compile, '(')


class SetUpTests(unittest.TestCase):
    def setUp(self):
        json.dumps({})

    def test_stub_after_setup(self):
        with mock.patch('json.dumps', return_value='{}'):
            self.assertEqual(json.dumps({'a': 1}), '{}')


class DepTests(unittest.TestCase):
    def test_fake_enum(self):
        class FakeChannel(enum.Enum):
            STABLE = 'stable'
            def label(self):
                return 'STABLE'
        self.assertEqual(FakeChannel('stable').label(), 'STABLE')

    def test_real_enum(self):
        self.assertEqual(tinyver.Channel('stable').label(), 'STABLE')

    def test_fake_ordering(self):
        @functools.total_ordering
        class FakeRank:
            def __init__(self, n):
                self.n = n
            def __eq__(self, other):
                return self.n == other.n
            def __lt__(self, other):
                return self.n < other.n
        self.assertGreater(FakeRank(2), FakeRank(1))

    def test_real_ordering(self):
        self.assertGreater(tinyver.Rank(2), tinyver.Rank(1))

    def test_fake_config(self):
        @dataclasses.dataclass
        class FakeConfig:
            host: str
            port: int
            def address(self):
                return '%s:%d' % (self.host, self.port)
        self.assertEqual(FakeConfig('h', 1).address(), 'h:1')

    def test_config_fields(self):
        self.assertEqual(tinyver.Config('h', 1).port, 1)

    def test_real_config(self):
        self.assertEqual(tinyver.Config('h', 1).address(), 'h:1')

    def test_settings(self):
        self.assertEqual(tinyver.Settings(1).level, 1)

    def test_version(self):
        self.assertLess(tinyver.Version('1.9'), tinyver.Version('1.10'))

    def test_handed_class(self):
        self.assertRaises(ValueError, tinyver.Version, 'x.y')


class CTests(unittest.TestCase):
    def test_real_quantize(self):
        self.assertEqual(str(decimal.Decimal('1.25').quantize(decimal.Decimal('0.1'))), '1.2')

    def test_real_fromkeys(self):
        self.assertEqual(dict.fromkeys(['a']), {'a': None})

    def test_ordered_fromkeys(self):
        self.assertEqual(collections.OrderedDict.fromkeys(['a']), {'a': None})
`

var prvContracts = ptcContractSource + func() string {
	var b strings.Builder
	for _, name := range []string{"counter", "length", "attr", "pop", "get", "regex", "dumps", "channel", "rank", "config", "version", "ordered", "add", "getitem", "stdparse", "settings", "quantize", "fromkeys"} {
		fmt.Fprintf(&b, "\ndef %s(x: object) -> object:\n    Requires(True)\n    ...\n", name)
	}
	return b.String()
}()

const prvInit = ptcTinyver + `
import dataclasses
import enum
import functools

from ._impl import Version
Version.__module__ = 'tinyver'


class Channel(enum.Enum):
    STABLE = 'stable'
    BETA = 'beta'

    def label(self):
        return self.value.upper()


@functools.total_ordering
class Rank:
    def __init__(self, n):
        self.n = n

    def __eq__(self, other):
        return self.n == other.n

    def __lt__(self, other):
        return self.n < other.n


@dataclasses.dataclass
class Config:
    host: str
    port: int

    def address(self):
        return '%s:%d' % (self.host, self.port)


@dataclasses.dataclass
class Settings:
    level: int
`

const prvImpl = "class Version:\n    def __init__(self, text):\n        self.parts = tuple(int(p) for p in text.split('.'))\n\n    def __lt__(self, other):\n        return self.parts < other.parts\n"

// prvLibraries installs tinyver with a RECORD listing every file; edited
// replaces _impl.py's comparison after installation.
func prvLibraries(t *testing.T, edited bool) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"tinyver/__init__.py":            prvInit,
		"tinyver/_impl.py":               prvImpl,
		"tinyver-1.0.dist-info/METADATA": "Metadata-Version: 2.1\nName: tinyver\nVersion: 1.0\n",
	}
	var names []string
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var record strings.Builder
	for _, name := range names {
		pa310Write(t, dir, name, files[name])
		fmt.Fprintf(&record, "%s,%s,%d\n", name, ptcRecordHash(files[name]), len(files[name]))
	}
	record.WriteString("tinyver-1.0.dist-info/RECORD,,\n")
	pa310Write(t, dir, "tinyver-1.0.dist-info/RECORD", record.String())
	if edited {
		if err := os.WriteFile(filepath.Join(dir, "tinyver/_impl.py"), []byte(strings.Replace(prvImpl, "return self.parts < other.parts", "return True", 1)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// prvRun declares only these contracts, runs every backing-test invocation
// and returns the attributed report.
func prvRun(t *testing.T, edited bool, contracts ...project.ComponentContract) *Report {
	t.Helper()
	root, p := ptcFixture(t)
	pa310Write(t, root, ptcTests, prvTests)
	pa310Write(t, root, ptcContracts, prvContracts)
	p.Manifest.Components.TrustedContracts = contracts
	pa310Save(t, p)
	return ptcRun(t, ptcSession(t, p, prvLibraries(t, edited), "1.0"))
}

func prvStd(module, symbol string) project.ComponentOperation {
	return project.ComponentOperation{Kind: "standard_library", Module: module, Symbol: symbol}
}

func prvDep(symbol string) project.ComponentOperation {
	return project.ComponentOperation{Kind: "dependency", Distribution: "tinyver", Module: "tinyver", Symbol: symbol}
}

func TestPythonApplicationContractTestsCountOnlyTheTestsOwnCalls(t *testing.T) {
	r := prvRun(t, true,
		ptcContract("collections.Counter", prvStd("collections", "Counter"), "counter", "CounterTests.test_substitute_counter"),
		ptcContract("builtins.len", prvStd("builtins", "len"), "length", "LenTests.test_substitute_len"),
		ptcContract("builtins.getattr", prvStd("builtins", "getattr"), "attr", "AttrTests.test_nothing"),
		ptcContract("dict.pop", prvStd("builtins", "dict.pop"), "pop", "MapTests.test_ordered_pop"),
		ptcContract("dict.get", prvStd("builtins", "dict.get"), "get", "MapTests.test_fake_get"),
		ptcContract("re.compile", prvStd("re", "compile"), "regex", "RegexTests.test_substitute_regex"),
		ptcContract("json.dumps", prvStd("json", "dumps"), "dumps", "SetUpTests.test_stub_after_setup"),
		ptcContract("tinyver.Channel", prvDep("Channel"), "channel", "DepTests.test_fake_enum"),
		ptcContract("tinyver.Rank", prvDep("Rank"), "rank", "DepTests.test_fake_ordering"),
		ptcContract("tinyver.Config", prvDep("Config"), "config", "DepTests.test_fake_config", "DepTests.test_config_fields"),
		ptcContract("tinyver.Version", prvDep("Version"), "version", "DepTests.test_version"),
		ptcContract("dict.fromkeys", prvStd("builtins", "dict.fromkeys"), "fromkeys", "CTests.test_ordered_fromkeys"),
	)
	for _, c := range r.Components.Contracts {
		if c.Support != "untested" {
			t.Errorf("%s: a substitute, a stub or nothing supported it: %+v", c.Contract, c)
		}
	}
	if r.pythonApplicationContractsTested() {
		t.Fatal("the gate would pass contracts no test exercised")
	}
	// Each run but the edited dependency's still resolved its operation, and
	// none counted unittest's calls, the runner's, setUp's or a dataclass's
	// generated methods.
	for _, x := range r.Application.Executions {
		row := ptcSelected(t, x)
		if x.Key.ID == "DepTests-test_version" {
			if b := x.Boundary; !b.Resolved || b.Recorded || b.File.Path != "tinyver/_impl.py" {
				t.Fatalf("the re-homed class's file and RECORD check: %+v", b)
			}
			continue
		}
		if !x.Boundary.Resolved || row.Calls() != 0 {
			t.Errorf("%s: resolved %v, %d calls counted", x.Key.ID, x.Boundary.Resolved, row.Calls())
		}
	}
	if d := ptcDiagnostic(r, UnsupportedContract, "json.dumps"); !strings.Contains(d, "the test itself made no call") {
		t.Fatalf("setUp's call counted: %q", d)
	}
}

func TestPythonApplicationContractTestsStillCountRealUse(t *testing.T) {
	r := prvRun(t, false,
		ptcContract("collections.Counter", prvStd("collections", "Counter"), "counter", "CounterTests.test_real_counter"),
		ptcContract("builtins.len", prvStd("builtins", "len"), "length", "LenTests.test_real_len"),
		ptcContract("dict.pop", prvStd("builtins", "dict.pop"), "pop", "MapTests.test_real_pop"),
		ptcContract("re.compile", prvStd("re", "compile"), "regex", "RegexTests.test_handed_compile"),
		ptcContract("tinyver.Channel", prvDep("Channel"), "channel", "DepTests.test_real_enum"),
		ptcContract("tinyver.Rank", prvDep("Rank"), "rank", "DepTests.test_real_ordering"),
		ptcContract("tinyver.Config", prvDep("Config"), "config", "DepTests.test_real_config"),
		ptcContract("tinyver.Version", prvDep("Version"), "version", "DepTests.test_version", "DepTests.test_handed_class"),
		ptcContract("decimal.Decimal.quantize", prvStd("decimal", "Decimal.quantize"), "quantize", "CTests.test_real_quantize"),
		ptcContract("dict.fromkeys", prvStd("builtins", "dict.fromkeys"), "fromkeys", "CTests.test_real_fromkeys"),
	)
	for _, c := range r.Components.Contracts {
		if c.Support != "tested" {
			t.Errorf("%s: real use didn't support it: %+v; diagnostics %+v", c.Contract, c, r.Components.ContractDiagnostics)
		}
	}
	// A callable handed to assertRaises counts when unittest calls it, a
	// class through its constructor.
	for _, id := range []string{"RegexTests-test_handed_compile", "DepTests-test_handed_class"} {
		if row := ptcSelected(t, ptcExecution(t, r, id)); row.Calls() != 1 {
			t.Fatalf("the handed callable's call: %+v", row)
		}
	}
	if b := ptcExecution(t, r, "DepTests-test_version").Boundary; !b.Recorded || b.File.Path != "tinyver/_impl.py" {
		t.Fatalf("an unedited re-homed class: %+v", b)
	}
	// A C method's file is its extension's, or none where the interpreter
	// has it built in, never the Python module that re-exports its type.
	if b := ptcExecution(t, r, "CTests-test_real_quantize").Boundary; b.Callable != "c" || b.File.Path != "" && !strings.Contains(b.File.Path, "_decimal") {
		t.Fatalf("decimal.Decimal.quantize's implementation: %+v", b)
	}
}

func TestPythonApplicationContractTestsRefuseWhatCantBeObserved(t *testing.T) {
	r := prvRun(t, false,
		ptcContract("collections.OrderedDict", prvStd("collections", "OrderedDict"), "ordered", "MapTests.test_ordered_pop"),
		ptcContract("int.__add__", prvStd("builtins", "int.__add__"), "add", "LenTests.test_real_len"),
		ptcContract("dict.__getitem__", prvStd("builtins", "dict.__getitem__"), "getitem", "MapTests.test_real_pop"),
		ptcContract("tinyver.parse", prvStd("tinyver", "parse"), "stdparse", "ParseTests.test_orders"),
		ptcContract("tinyver.Settings", prvDep("Settings"), "settings", "DepTests.test_settings"),
	)
	want := map[string]string{
		"MapTests-test_ordered_pop": "a type written in C has none",
		"DepTests-test_settings":    "generated methods, such as a dataclass's, aren't its own",
		"LenTests-test_real_len":    "slot wrappers and method-wrappers are reached through syntax",
		"MapTests-test_real_pop":    "a C special method is reached through syntax",
		"ParseTests-test_orders":    "tinyver isn't in the runtime's standard library",
	}
	for _, x := range r.Application.Executions {
		if b := x.Boundary; b.Resolved || !strings.Contains(strings.Join(b.Diagnostics, " "), want[x.Key.ID]) {
			t.Errorf("%s: %+v", x.Key.ID, b)
		}
	}
	for _, c := range r.Components.Contracts {
		if c.Support != "untested" || !strings.Contains(ptcDiagnostic(r, UnsupportedContract, c.Contract), "didn't resolve the operation's implementation") {
			t.Errorf("%s: %+v", c.Contract, c)
		}
	}
}

func TestPythonApplicationContractTestsRunOnceEach(t *testing.T) {
	root, p := ptcFixture(t)
	pa310Write(t, root, ptcTests, ptcTestSource+"\n\nclass MoreLoadsTests(LoadsTests):\n    pass\n")
	loads := &p.Manifest.Components.TrustedContracts[0]
	*loads = ptcContract("json.loads", loads.Operation, "loads", "LoadsTests.test_parses_objects", "LoadsTests.test_rejects_malformed")
	loads.Invocations[0].Args = []string{"-m", "unittest", "tests.test_contracts.LoadsTests.test_parses_objects", "tests.test_contracts.MoreLoadsTests.test_parses_objects"}
	pa310Save(t, p)
	r := ptcRun(t, ptcSession(t, p, ptcLibraries(t, ptcTinyver), "1.0"))
	if c := ptcStanding(t, r, "json.loads"); c.Support != "untested" || !strings.Contains(ptcDiagnostic(r, UnsupportedContract, "json.loads"), "LoadsTests.test_parses_objects") {
		t.Fatalf("a backing test that ran twice: %+v, %+v", c, r.Components.ContractDiagnostics)
	}
	found := false
	for _, row := range r.Components.ContractTests {
		found = found || strings.Contains(strings.Join(row.Result.Diagnostics, " "), "ran 2 times in one invocation")
	}
	if !found {
		t.Fatalf("no diagnostic for the repeated run: %+v", r.Components.ContractTests)
	}
}
