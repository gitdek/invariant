package verify_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

// #350, #294's step 4: the gate runs every declared trusted contract's backing
// tests in the application's session and passes only when each contract is
// tested. Default runs execute the real embedded runner on the host's Python
// with #327's Docker and TLC doubles. With INVARIANT_PYTHON_APPLICATION_GATE_REAL=1
// the same cases also run verify.Run with real Docker, Python and TLC, and add
// a contract on the installed packaging wheel:
//
//	INVARIANT_PYTHON_APPLICATION_GATE_REAL=1 go test ./internal/verify -run '^TestPythonApplicationGateTrustedContracts' -count=1 -timeout 45m

const gtcContracts = pa327Project + "/contracts.py"
const gtcTests = "tests/test_contracts.py"

const gtcContractSource = `from nagini_contracts.contracts import *

def loads(text: str) -> object:
    Requires(len(text) > 0)
    ...

def next_state(state: str) -> str:
    Requires(state in ('pending', 'running', 'done'))
    Ensures(Result() in ('running', 'done'))
    ...

def version(text: str) -> object:
    Requires(len(text) > 0)
    ...
`

const gtcTestSource = `import json
import unittest
from unittest import mock

from tasks.helper import next_state


class LoadsTests(unittest.TestCase):
    def test_parses(self):
        self.assertEqual(json.loads('{"task": "pending"}'), {"task": "pending"})

    def test_rejects_malformed(self):
        with self.assertRaises(json.JSONDecodeError):
            json.loads('{')

    def test_stub_only(self):
        with mock.patch('json.loads', return_value={}):
            self.assertEqual(json.loads('anything'), {})

    @unittest.skip('a skipped backing test supports nothing')
    def test_skipped(self):
        json.loads('1')

    def test_wrong(self):
        self.assertEqual(json.loads('[1]'), [2])


class NextStateTests(unittest.TestCase):
    def test_advances_pending(self):
        self.assertEqual(next_state('pending'), 'running')

    def test_finishes_running(self):
        self.assertEqual(next_state('running'), 'done')


class VersionTests(unittest.TestCase):
    def test_orders(self):
        from packaging.version import Version
        self.assertLess(Version('1.9'), Version('1.10'))

    def test_rejects_invalid(self):
        from packaging.version import InvalidVersion, Version
        with self.assertRaises(InvalidVersion):
            Version('not a version')
`

func gtcContract(id string, operation project.ComponentOperation, contract string, tests ...string) project.ComponentContract {
	c := project.ComponentContract{ID: id, Operation: operation, Contract: pa327Ref(gtcContracts, contract)}
	for _, test := range tests {
		ref := pa327Ref(gtcTests, test)
		c.Tests = append(c.Tests, ref)
		c.Invocations = append(c.Invocations, project.ComponentInvocation{ID: strings.ReplaceAll(test, ".", "-"), Kind: "test", Check: ref,
			Command: "python", Args: []string{"-m", "unittest", "tests.test_contracts." + test}, WorkingDir: "."})
	}
	return c
}

// gtcNew is #327's explicit fixture with trusted contracts on json.loads and
// the application's own next_state, and with the wheel, on packaging's Version.
func gtcNew(t *testing.T, wheel bool) pa327Fixture {
	t.Helper()
	f := pa327New(t, true, wheel)
	pa327Write(t, f.root, gtcContracts, gtcContractSource)
	pa327Write(t, f.root, gtcTests, gtcTestSource)
	contracts := []project.ComponentContract{
		gtcContract("json.loads", project.ComponentOperation{Kind: "standard_library", Module: "json", Symbol: "loads"}, "loads", "LoadsTests.test_parses", "LoadsTests.test_rejects_malformed"),
		gtcContract("tasks.next_state", project.ComponentOperation{Kind: "application", Source: "tasks/helper.py", Symbol: "next_state"}, "next_state", "NextStateTests.test_advances_pending", "NextStateTests.test_finishes_running"),
	}
	if wheel {
		contracts = append(contracts, gtcContract("packaging.Version", project.ComponentOperation{Kind: "dependency", Distribution: "packaging", Module: "packaging.version", Symbol: "Version"},
			"version", "VersionTests.test_orders", "VersionTests.test_rejects_invalid"))
	}
	f.p.Manifest.Components.TrustedContracts = contracts
	pa327Save(t, f)
	return f
}

func gtcStanding(t *testing.T, r *verify.Report) map[string]verify.ComponentContractEvidence {
	t.Helper()
	if r.Components == nil {
		t.Fatal("no component evidence")
	}
	out := map[string]verify.ComponentContractEvidence{}
	for _, c := range r.Components.Contracts {
		out[c.Contract] = c
	}
	return out
}

func TestPythonApplicationGateTrustedContracts(t *testing.T) {
	t.Run("local Python", func(t *testing.T) {
		tc, _ := pa327Fake(t)
		gtcPasses(t, tc, false)
		gtcFailures(t, tc, false)
	})
	if os.Getenv("INVARIANT_PYTHON_APPLICATION_GATE_REAL") == "1" {
		t.Run("real Docker Python TLC and wheel", func(t *testing.T) {
			tc, err := toolchain.Ensure(context.Background())
			if err != nil {
				t.Fatalf("requested real prerequisites: %v", err)
			}
			gtcPasses(t, tc, true)
			gtcFailures(t, tc, true)
		})
	}
}

func gtcPasses(t *testing.T, tc toolchain.Toolchain, wheel bool) {
	t.Helper()
	f := gtcNew(t, wheel)
	before := pa327Tree(t, f.root)
	pa327Admit(t, f)
	r := pa327Run(t, f, tc)
	if !r.Passed {
		t.Fatalf("supported contracts failed the gate: %s", pa327JSON(t, r))
	}
	standing := gtcStanding(t, r)
	want := []string{"json.loads", "tasks.next_state"}
	if wheel {
		want = append(want, "packaging.Version")
	}
	for _, id := range want {
		if c := standing[id]; c.Support != "tested" || c.Use != "declared" || c.Binding == nil {
			t.Fatalf("%s: %+v; diagnostics %+v", id, c, r.Components.ContractDiagnostics)
		}
	}
	if len(standing) != len(want) || len(r.Components.ContractDiagnostics) != 0 {
		t.Fatalf("contracts %+v, diagnostics %+v", standing, r.Components.ContractDiagnostics)
	}
	for _, row := range r.Components.ContractTests {
		if row.Origin != "gate" || row.Status != "tested" || row.Result.Implementation == nil || len(row.Result.Exercise) != 1 || row.Result.Exercise[0].Calls == 0 {
			t.Fatalf("a gate backing-test row: %+v", row)
		}
		if len(row.Result.Tests) != 1 || len(row.Result.Tests[0].Assertions) == 0 {
			t.Fatalf("a backing test's assertions weren't recorded: %+v", row.Result.Tests)
		}
	}
	if wheel {
		b := standing["packaging.Version"].Binding
		if b.Wheel == nil || b.Wheel.Name != "packaging" || b.Wheel.Version != "24.2" {
			t.Fatalf("packaging's binding: %+v", b)
		}
		for _, row := range r.Components.ContractTests {
			if row.Contract == "packaging.Version" {
				if i := row.Result.Implementation; i.Distribution != "packaging" || i.Version != "24.2" || !i.Recorded || i.File == nil || i.File.Path != "packaging/version.py" {
					t.Fatalf("the installed implementation: %+v", i)
				}
			}
		}
	}
	// A backing test's build step is named for its contract.
	steps := 0
	for _, s := range r.Build.Steps {
		if strings.HasPrefix(s.Name, "trusted contract ") {
			steps++
		}
	}
	if steps != 2*len(want) {
		t.Fatalf("build steps: %+v", r.Build.Steps)
	}
	md := receipt.Markdown(r)
	for _, w := range []string{"trusted · tested", "declaration only: no proof reported using it", "Origin: <code>gate</code>", "calls into it from"} {
		if !strings.Contains(md, w) {
			t.Fatalf("receipt lacks %q:\n%s", w, md)
		}
	}
	if strings.Contains(md, "What failed") {
		t.Fatalf("a passing receipt lists failures:\n%s", md)
	}
	var decoded verify.Report
	if err := json.Unmarshal(pa327JSON(t, r), &decoded); err != nil {
		t.Fatal(err)
	}
	if verify.Fingerprint(decoded) != r.Fingerprint {
		t.Fatal("a JSON round trip changed the fingerprint")
	}
	if !reflect.DeepEqual(before, pa327Tree(t, f.root)) {
		t.Fatal("verification modified the original application")
	}
	// The inputs' support is bound to them: a changed contract, backing test
	// or dependency resolution can't keep it, and runs again differently.
	pa327Write(t, f.root, gtcContracts, strings.Replace(gtcContractSource, "Requires(len(text) > 0)", "Requires(len(text) > 1)", 1))
	changed, err := project.Load(f.p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	// The gate's inventory is the staged application's, every file included.
	w, err := verify.StagePythonApplication(changed, t.TempDir()+"/stage")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.AttributeComponentProofs(w.Inventory, nil); !errors.Is(err, verify.ErrStaleComponentEvidence) {
		t.Fatalf("support survived a changed contract: %v", err)
	}
	again := pa327Run(t, f, tc)
	if !again.Passed || again.Fingerprint == r.Fingerprint {
		t.Fatalf("a changed contract: passed %v, same fingerprint %v", again.Passed, again.Fingerprint == r.Fingerprint)
	}
}

func gtcFailures(t *testing.T, tc toolchain.Toolchain, wheel bool) {
	t.Helper()
	for _, c := range []struct {
		name, test string
		args       []string
		reason     string
	}{
		{"stub only", "LoadsTests.test_stub_only", nil, "the test itself made no call into the bound implementation"},
		{"skipped", "LoadsTests.test_skipped", nil, "skipped"},
		{"failing contract", "LoadsTests.test_wrong", nil, "failed"},
		{"passes without running it", "LoadsTests.test_skipped", []string{"-m", "unittest", "tests.test_contracts.LoadsTests.test_parses"}, "didn't run"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := gtcNew(t, wheel)
			loads := &f.p.Manifest.Components.TrustedContracts[0]
			*loads = gtcContract("json.loads", loads.Operation, "loads", "LoadsTests.test_parses", c.test)
			if c.args != nil {
				loads.Invocations[1].Args = c.args
			}
			pa327Save(t, f)
			pa327Admit(t, f)
			r := pa327Run(t, f, tc)
			if r.Passed {
				t.Fatalf("an unsupported contract passed the gate: %s", pa327JSON(t, r))
			}
			standing := gtcStanding(t, r)
			if standing["json.loads"].Support != "untested" || standing["tasks.next_state"].Support != "tested" {
				t.Fatalf("standing: %+v", standing)
			}
			md := receipt.Markdown(r)
			failed := md[strings.Index(md, "**What failed**"):]
			if !strings.Contains(failed, "Trusted contract <code>json.loads</code>") || !strings.Contains(failed, c.reason) {
				t.Fatalf("the receipt's failures don't say why:\n%s", failed)
			}
			// The rest of the gate's evidence stays: the application's checks
			// and the other contract passed.
			for _, x := range r.Application.Executions {
				if x.Key.Kind == "conformance" && !x.Success {
					t.Fatalf("an unrelated check lost its evidence: %+v", x)
				}
			}
		})
	}
}
