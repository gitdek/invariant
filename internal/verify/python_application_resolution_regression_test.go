package verify

import (
	"fmt"
	"strings"
	"testing"
)

// Source matching keeps declarations with decorators, repeated names and
// nested scopes, while refusing compiled code without a matching declaration.
const resolutionSourceLibrary = ptcTinyver + `
import functools
import types


def wrapped(function):
    @functools.wraps(function)
    def wrapper(*args, **kwargs):
        return function(*args, **kwargs)
    return wrapper


class Declared:
    field: int

    def __new__(cls):
        return super().__new__(cls)

    @wrapped
    def fail(self):
        raise ValueError('declared method')

    @property
    def value(self):
        return self._value

    @value.setter
    def value(self, value):
        self._value = value

    @classmethod
    def from_class(cls):
        return cls.__name__

    @staticmethod
    def from_static():
        return 7

    if True:
        def conditional(self):
            return 7

    class Nested:
        def value(self):
            return 7


class AnnotationOnly:
    field: int
    # Like a compiler's annotation helper, this code is compiled in the
    # class's source but has no method declaration, even on Python 3.13.
    __annotate__ = lambda format: {'field': int}


class Replaced:
    def value(self):
        return 7


def replacement(self):
    return 8


# Matching a declaration's location and qualified name still isn't enough
# when the code itself was generated elsewhere.
original = Replaced.value.__code__
code = replacement.__code__.replace(co_name=original.co_name,
                                    co_qualname=original.co_qualname,
                                    co_firstlineno=original.co_firstlineno)
Replaced.value = types.FunctionType(code, globals(), 'value')
`

const resolutionSourceTests = ptcTestSource + `


class SourceTests(unittest.TestCase):
    def test_forms(self):
        value = tinyver.Declared()
        value.value = 7
        self.assertEqual(value.value, 7)
        self.assertEqual(value.from_class(), 'Declared')
        self.assertEqual(value.conditional(), 7)
        with self.assertRaises(ValueError):
            value.fail()
        self.assertEqual(value.from_static(), 7)

    def test_static(self):
        self.assertEqual(tinyver.Declared.from_static(), 7)

    def test_classmethod(self):
        self.assertEqual(tinyver.Declared.from_class(), 'Declared')

    def test_new(self):
        self.assertIs(type(tinyver.Declared()), tinyver.Declared)

    def test_nested(self):
        self.assertEqual(tinyver.Declared.Nested().value(), 7)

    def test_annotation(self):
        self.assertIs(tinyver.AnnotationOnly.__annotate__(1)['field'], int)

    def test_replaced(self):
        self.assertEqual(tinyver.Replaced().value(), 8)

    def test_replaced_method(self):
        self.assertEqual(tinyver.Replaced().value(), 8)
`

func TestPythonApplicationResolutionMatchesSourceDeclarations(t *testing.T) {
	t.Parallel()
	cases := []struct {
		symbol, test string
		calls        int64
		resolved     bool
	}{
		{"Declared", "forms", 5, true},
		{"Declared.from_static", "static", 1, true},
		{"Declared.from_class", "classmethod", 1, true},
		{"Declared.__new__", "new", 1, true},
		{"Declared.Nested", "nested", 1, true},
		{"AnnotationOnly", "annotation", 0, false},
		{"Replaced", "replaced", 0, false},
		{"Replaced.value", "replaced_method", 0, false},
	}
	root, p := ptcFixture(t)
	pa310Write(t, root, ptcTests, resolutionSourceTests)
	contracts := ptcContractSource
	p.Manifest.Components.TrustedContracts = nil
	for _, tc := range cases {
		contracts += fmt.Sprintf("\ndef %s(x: object) -> object:\n    Requires(True)\n    Exsures(ValueError, True)\n    ...\n", tc.test)
		p.Manifest.Components.TrustedContracts = append(p.Manifest.Components.TrustedContracts,
			ptcContract("tinyver."+tc.symbol, prvDep(tc.symbol), tc.test, "SourceTests.test_"+tc.test))
	}
	pa310Write(t, root, ptcContracts, contracts)
	pa310Save(t, p)
	r := ptcRun(t, ptcSession(t, p, prvLibrariesWith(t, resolutionSourceLibrary, false), "1.0"))
	for _, tc := range cases {
		x := ptcExecution(t, r, "SourceTests-test_"+tc.test)
		row := ptcSelected(t, x)
		if !x.Success || !x.Complete || !row.Exercised || row.Outcome != "passed" || len(row.Assertions) == 0 {
			t.Fatalf("%s: backing test failed: execution=%+v test=%+v", tc.symbol, x, row)
		}
		for _, assertion := range row.Assertions {
			if assertion.Outcome != "passed" {
				t.Errorf("%s: assertion failed: %+v", tc.symbol, assertion)
			}
		}
		if row.BoundaryCalls == nil || row.Calls() != tc.calls {
			t.Errorf("%s: got %d calls, want %d", tc.symbol, row.Calls(), tc.calls)
		}
		b := x.Boundary
		if b == nil || b.Resolved != tc.resolved {
			t.Fatalf("%s: implementation: %+v", tc.symbol, b)
		}
		want := "untested"
		if tc.resolved {
			want = "tested"
			if b.Callable != "python" || !b.Recorded || b.Distribution != "tinyver" || b.Version != "1.0" ||
				b.File.Path != "tinyver/__init__.py" || b.File.SHA256 != pa310Hash([]byte(resolutionSourceLibrary)) {
				t.Errorf("%s: source attribution: %+v", tc.symbol, b)
			}
		} else {
			if b.Callable != "" || b.File.Path != "" || b.Recorded || len(b.Diagnostics) == 0 {
				t.Errorf("%s: unresolved implementation gained evidence: %+v", tc.symbol, b)
			}
			if d := ptcDiagnostic(r, UnsupportedContract, "tinyver."+tc.symbol); !strings.Contains(d, "didn't resolve the operation's implementation") {
				t.Errorf("%s: refusal reason: %q", tc.symbol, d)
			}
		}
		if c := ptcStanding(t, r, "tinyver."+tc.symbol); c.Support != want {
			t.Errorf("%s: support %s, want %s; diagnostics %+v", tc.symbol, c.Support, want, r.Components.ContractDiagnostics)
		}
	}
}
