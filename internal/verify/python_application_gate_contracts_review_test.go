package verify_test

import (
	"context"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

// #355's review at the gate: backing tests that exercise only a substitute or
// nothing, call without asserting, swallow a failed assertion or run twice
// fail the gate, on its route and the direct one alike, and leave the rest of
// its evidence standing.

const gtcSubstituteTests = `

import base64
import dataclasses
import enum
import html


class SubstituteTests(unittest.TestCase):
    def test_nothing(self):
        pass

    def test_substitute_counter(self):
        fake = lambda items: {'a': 2}
        self.assertCountEqual(fake('aa'), ['a'])

    def test_fake_status(self):
        class FakeStatus(enum.IntEnum):
            OK = 200
        self.assertEqual(FakeStatus(200), 200)

    def test_fake_profile(self):
        @dataclasses.dataclass
        class FakeProfile:
            total_tt: float
            func_profiles: dict
        self.assertEqual(FakeProfile(1.0, {}).total_tt, 1.0)

    def test_unasserted(self):
        json.dumps({'task': 'pending'})

    def test_swallowed(self):
        try:
            self.assertEqual(html.escape('<'), '<')
        except AssertionError:
            pass


class EncodeTests(unittest.TestCase):
    def test_encodes(self):
        self.assertEqual(base64.b64encode(b'a'), b'YQ==')


class MoreEncodeTests(EncodeTests):
    pass
`

const gtcSubstituteContracts = `
def attr(o: object, name: str) -> object:
    Requires(len(name) > 0)
    ...

def counter(items: object) -> object:
    Requires(True)
    ...

def status(code: int) -> object:
    Requires(code >= 100)
    ...

def profile(total: float, funcs: dict) -> object:
    Requires(total >= 0)
    ...

def dumps(value: object) -> str:
    Requires(True)
    ...

def escape(text: str) -> str:
    Requires(True)
    ...

def encode(data: bytes) -> bytes:
    Requires(True)
    ...
`

func TestPythonApplicationGateTrustedContractsRefuseSubstitutes(t *testing.T) {
	t.Run("local Python", func(t *testing.T) {
		tc, _ := pa327Fake(t)
		gtcSubstitutes(t, tc)
	})
	if os.Getenv("INVARIANT_PYTHON_APPLICATION_GATE_REAL") == "1" {
		t.Run("real Docker Python TLC", func(t *testing.T) {
			tc, err := toolchain.Ensure(context.Background())
			if err != nil {
				t.Fatalf("requested real prerequisites: %v", err)
			}
			gtcSubstitutes(t, tc)
		})
	}
}

func gtcSubstitutes(t *testing.T, tc toolchain.Toolchain) {
	t.Helper()
	f := gtcNew(t, false)
	pa327Write(t, f.root, gtcTests, gtcTestSource+gtcSubstituteTests)
	pa327Write(t, f.root, gtcContracts, gtcContractSource+gtcSubstituteContracts)
	std := func(module, symbol string) project.ComponentOperation {
		return project.ComponentOperation{Kind: "standard_library", Module: module, Symbol: symbol}
	}
	// One selection runs the inherited test twice.
	encode := gtcContract("base64.b64encode", std("base64", "b64encode"), "encode", "EncodeTests.test_encodes")
	encode.Invocations[0].Args = append(encode.Invocations[0].Args, "tests.test_contracts.MoreEncodeTests.test_encodes")
	f.p.Manifest.Components.TrustedContracts = append(f.p.Manifest.Components.TrustedContracts,
		gtcContract("builtins.getattr", std("builtins", "getattr"), "attr", "SubstituteTests.test_nothing"),
		gtcContract("collections.Counter", std("collections", "Counter"), "counter", "SubstituteTests.test_substitute_counter"),
		gtcContract("http.HTTPStatus", std("http", "HTTPStatus"), "status", "SubstituteTests.test_fake_status"),
		gtcContract("pstats.StatsProfile", std("pstats", "StatsProfile"), "profile", "SubstituteTests.test_fake_profile"),
		gtcContract("json.dumps", std("json", "dumps"), "dumps", "SubstituteTests.test_unasserted"),
		gtcContract("html.escape", std("html", "escape"), "escape", "SubstituteTests.test_swallowed"),
		encode,
	)
	pa327Save(t, f)
	pa327Admit(t, f)
	r := pa327Run(t, f, tc)
	if r.Passed {
		t.Fatalf("contracts no test supports passed the gate: %s", pa327JSON(t, r))
	}
	standing := gtcStanding(t, r)
	if standing["json.loads"].Support != "tested" || standing["tasks.next_state"].Support != "tested" {
		t.Fatalf("supported contracts lost their standing: %+v; diagnostics %+v", standing, r.Components.ContractDiagnostics)
	}
	md := receipt.Markdown(r)
	failed := strings.Split(md[strings.Index(md, "**What failed**"):], "\n")
	for id, reason := range map[string]string{
		"builtins.getattr":    "the test itself made no call into the bound implementation",
		"collections.Counter": "the test itself made no call into the bound implementation",
		"http.HTTPStatus":     "the test itself made no call into the bound implementation",
		"pstats.StatsProfile": "didn't resolve the operation's implementation",
		"json.dumps":          "it made no assertion that passed, or one failed",
		"html.escape":         "it made no assertion that passed, or one failed",
		"base64.b64encode":    "EncodeTests.test_encodes@tests/test_contracts.py: failed",
	} {
		if standing[id].Support != "untested" {
			t.Errorf("%s: %+v", id, standing[id])
		}
		n := slices.IndexFunc(failed, func(line string) bool { return strings.Contains(line, "Trusted contract <code>"+id+"</code>") })
		if n < 0 {
			t.Errorf("the receipt doesn't list %s as failed", id)
		} else if !strings.Contains(strings.ReplaceAll(failed[n], "&#95;", "_"), reason) {
			t.Errorf("the receipt doesn't say why %s failed, %q: %s", id, reason, failed[n])
		}
	}
	repeated := false
	for _, row := range r.Components.ContractTests {
		repeated = repeated || row.Contract == "base64.b64encode" &&
			slices.ContainsFunc(row.Result.Diagnostics, func(d string) bool { return strings.Contains(d, "ran 2 times in one invocation") })
	}
	if !repeated {
		t.Fatalf("no diagnostic for the repeated run: %+v", r.Components.ContractTests)
	}
	// Each backing test's step passes only where its contract stands, and the
	// direct route marks the same steps.
	steps := map[string]bool{}
	for _, s := range r.Build.Steps {
		if id, ok := strings.CutPrefix(s.Name, "trusted contract "); ok {
			steps[s.Name] = s.Passed
			if id, _, _ = strings.Cut(id, " · "); s.Passed != (standing[id].Support == "tested") {
				t.Errorf("%s: passed %v for a contract %s", s.Name, s.Passed, standing[id].Support)
			}
		}
	}
	b, _, e := (verify.Python{Image: toolchain.PythonImage}).Check(context.Background(), f.p)
	if e != nil || b.Passed {
		t.Fatalf("the direct route: %+v %v", b, e)
	}
	direct := map[string]bool{}
	for _, s := range b.Steps {
		if strings.HasPrefix(s.Name, "trusted contract ") {
			direct[s.Name] = s.Passed
		}
	}
	if len(steps) != 11 || !reflect.DeepEqual(steps, direct) {
		t.Fatalf("the routes disagree:\ngate   %v\ndirect %v", steps, direct)
	}
}
