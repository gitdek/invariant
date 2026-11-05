package verify

import (
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
)

// #479: run just these contracts through the real embedded runner and normal
// session/report attribution. The local adapter replaces Docker transport,
// not Python execution. No full package run or second interpreter is needed.
//
// Python releases can give generated helpers a class-qualified code name.
// This installed module reproduces that property on 3.13 as well: its generated
// function has real code, globals and a RECORD-listed file, but no declaration
// in the owning class's body. It is installed before boundary resolution; the
// backing tests neither change the runner nor patch the resolved operation.
const resolution479Library = ptcTinyver + `
import dataclasses
import types


class Generated:
    pass


@dataclasses.dataclass
class Settings:
    level: int


@dataclasses.dataclass
class NativeSettings:
    level: int


class Mixed:
    def value(self):
        return 7


class Authored:
    def generated(self):
        return 7


def _factory_body(self):
    return 7


def _attach_generated(cls):
    code = _factory_body.__code__.replace(
        co_name='generated', co_qualname=cls.__qualname__ + '.generated')
    cls.generated = types.FunctionType(code, globals(), 'generated')


_attach_generated(Generated)
_attach_generated(Settings)
_attach_generated(Mixed)
`

const resolution479Tests = ptcTestSource + `
import dataclasses


class ResolutionTests(unittest.TestCase):
    def test_generated_class(self):
        self.assertEqual(tinyver.Generated().generated(), 7)

    def test_generated_method(self):
        self.assertEqual(tinyver.Generated().generated(), 7)

    def test_generated_dataclass(self):
        self.assertEqual(tinyver.Settings(1).level, 1)

    def test_native_dataclass(self):
        self.assertEqual(tinyver.NativeSettings(1).level, 1)

    def test_substitute_profile(self):
        @dataclasses.dataclass
        class FakeProfile:
            total_tt: float
            func_profiles: dict
        self.assertEqual(FakeProfile(1.0, {}).total_tt, 1.0)

    def test_mixed_generated(self):
        self.assertEqual(tinyver.Mixed().generated(), 7)

    def test_mixed_real(self):
        self.assertEqual(tinyver.Mixed().value(), 7)

    def test_authored(self):
        self.assertEqual(tinyver.Authored().generated(), 7)
`

const resolution479Contracts = ptcContractSource + `

def generated_class(x: object) -> object:
    Requires(True)
    ...

def generated_method(x: object) -> object:
    Requires(True)
    ...

def generated_dataclass(x: object) -> object:
    Requires(True)
    ...

def native_dataclass(x: object) -> object:
    Requires(True)
    ...

def profile(x: object) -> object:
    Requires(True)
    ...

def mixed(x: object) -> object:
    Requires(True)
    ...

def real_method(x: object) -> object:
    Requires(True)
    ...

def authored(x: object) -> object:
    Requires(True)
    ...
`

func resolution479Run(t *testing.T, contracts ...project.ComponentContract) *Report {
	t.Helper()
	root, p := ptcFixture(t)
	pa310Write(t, root, ptcTests, resolution479Tests)
	pa310Write(t, root, ptcContracts, resolution479Contracts)
	p.Manifest.Components.TrustedContracts = contracts
	pa310Save(t, p)
	libraries := prvLibrariesWith(t, resolution479Library, false)
	return ptcRun(t, ptcSession(t, p, libraries, "1.0"))
}

func resolution479PassedTest(t *testing.T, r *Report, method string) PythonApplicationExecutionResult {
	t.Helper()
	x := ptcExecution(t, r, "ResolutionTests-"+method)
	row := ptcSelected(t, x)
	if !x.Success || !x.Complete || !row.Exercised || row.Outcome != "passed" || len(row.Assertions) == 0 {
		t.Fatalf("the refusal must concern contract support, not a broken backing test: execution=%+v test=%+v", x, row)
	}
	for _, assertion := range row.Assertions {
		if assertion.Outcome != "passed" {
			t.Fatalf("backing assertion failed: %+v", assertion)
		}
	}
	if x.Boundary == nil {
		t.Fatal("execution omitted its implementation boundary")
	}
	return x
}

func TestPythonApplicationResolutionRefusesGeneratedOnlyClasses(t *testing.T) {
	t.Parallel()
	cases := []struct {
		id, symbol, contract, method string
	}{
		{"tinyver.Generated", "Generated", "generated_class", "test_generated_class"},
		{"tinyver.Generated.generated", "Generated.generated", "generated_method", "test_generated_method"},
		{"tinyver.Settings", "Settings", "generated_dataclass", "test_generated_dataclass"},
		{"tinyver.NativeSettings", "NativeSettings", "native_dataclass", "test_native_dataclass"},
		{"pstats.StatsProfile", "StatsProfile", "profile", "test_substitute_profile"},
	}
	var contracts []project.ComponentContract
	for _, tc := range cases {
		op := prvDep(tc.symbol)
		if tc.id == "pstats.StatsProfile" {
			op = prvStd("pstats", tc.symbol)
		}
		contracts = append(contracts, ptcContract(tc.id, op, tc.contract, "ResolutionTests."+tc.method))
	}
	r := resolution479Run(t, contracts...)
	for _, tc := range cases {
		x := resolution479PassedTest(t, r, tc.method)
		b := x.Boundary
		if b.Resolved || b.Callable != "" || b.File.Path != "" || b.Recorded || len(b.Diagnostics) == 0 {
			t.Errorf("%s: generated code gained an implementation boundary: %+v", tc.id, b)
		}
		if row := ptcSelected(t, x); row.BoundaryCalls == nil || row.Calls() != 0 {
			t.Errorf("%s: unresolved implementation counted calls: %+v", tc.id, row)
		}
		if c := ptcStanding(t, r, tc.id); c.Support != "untested" {
			t.Errorf("%s: generated code supported its contract: %+v", tc.id, c)
		}
		if d := ptcDiagnostic(r, UnsupportedContract, tc.id); !strings.Contains(d, "didn't resolve the operation's implementation") {
			t.Errorf("%s: report must explain the unresolved implementation, got %q", tc.id, d)
		}
	}
	if r.pythonApplicationContractsTested() {
		t.Error("the gate would accept contracts with no declared implementation")
	}
}

func TestPythonApplicationResolutionCountsOnlyDeclaredMethods(t *testing.T) {
	t.Parallel()
	r := resolution479Run(t,
		ptcContract("tinyver.Mixed", prvDep("Mixed"), "mixed", "ResolutionTests.test_mixed_generated"),
		ptcContract("tinyver.Mixed.value", prvDep("Mixed.value"), "real_method", "ResolutionTests.test_mixed_real"),
		ptcContract("tinyver.Authored", prvDep("Authored"), "authored", "ResolutionTests.test_authored"),
	)
	for _, tc := range []struct {
		id, method string
		calls      int64
		support    string
	}{
		{"tinyver.Mixed", "test_mixed_generated", 0, "untested"},
		{"tinyver.Mixed.value", "test_mixed_real", 1, "tested"},
		{"tinyver.Authored", "test_authored", 1, "tested"},
	} {
		x := resolution479PassedTest(t, r, tc.method)
		b := x.Boundary
		if !b.Resolved || b.Callable != "python" || b.Distribution != "tinyver" || b.Version != "1.0" || !b.Recorded ||
			b.File.Path != "tinyver/__init__.py" || b.File.SHA256 != pa310Hash([]byte(resolution479Library)) {
			t.Errorf("%s: declared implementation lost its bound source: %+v", tc.id, b)
		}
		if row := ptcSelected(t, x); row.BoundaryCalls == nil || row.Calls() != tc.calls {
			t.Errorf("%s: got %d implementation calls, want %d", tc.id, row.Calls(), tc.calls)
		}
		if c := ptcStanding(t, r, tc.id); c.Support != tc.support {
			t.Errorf("%s: support %s, want %s; diagnostics %+v", tc.id, c.Support, tc.support, r.Components.ContractDiagnostics)
		}
	}
	if d := ptcDiagnostic(r, UnsupportedContract, "tinyver.Mixed"); !strings.Contains(d, "the test itself made no call into the bound implementation") {
		t.Errorf("a resolved class called only through a generated helper needs the missing-call reason, got %q", d)
	}
	if r.pythonApplicationContractsTested() {
		t.Error("the gate would accept a test that called only a generated helper")
	}
}
