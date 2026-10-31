package verify

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/toolchain"
)

// Backing tests for trusted contracts (#350, #294's step 4) run through the
// session's real embedded runner on the host's Python, against a distribution
// installed into a local libraries directory with a real RECORD. Docker is a
// double here; the gate's real suite runs them in the pinned image.

const ptcTests = "tests/test_contracts.py"
const ptcContracts = pa315Project + "/contracts.py"

const ptcTestSource = `import json
import unittest
from unittest import mock

import tinyver
from store.state import save


class LoadsTests(unittest.TestCase):
    def test_parses_objects(self):
        self.assertEqual(json.loads('{"a": [1, 2]}'), {"a": [1, 2]})

    def test_rejects_malformed(self):
        with self.assertRaises(json.JSONDecodeError):
            json.loads('{')

    def test_stub_only(self):
        with mock.patch('json.loads', return_value={}):
            self.assertEqual(json.loads('anything'), {})

    @unittest.skip('the contract must not count this')
    def test_skipped(self):
        json.loads('1')

    def test_wrong(self):
        self.assertEqual(json.loads('[1]'), [2])


class ParseTests(unittest.TestCase):
    def test_orders(self):
        self.assertLess(tinyver.parse('1.9'), tinyver.parse('1.10'))

    def test_rejects_empty(self):
        with self.assertRaises(ValueError):
            tinyver.parse('')


class SaveTests(unittest.TestCase):
    def test_marks_saved(self):
        task = {}
        self.assertIs(save(task), task)
        self.assertTrue(task['saved'])
`

const ptcContractSource = `from nagini_contracts.contracts import *

def loads(text: str) -> object:
    Requires(len(text) > 0)
    ...

def parse(text: str) -> object:
    Requires(len(text) > 0)
    ...

def save(task: dict) -> dict:
    Ensures(Result() is task)
    ...
`

const ptcTinyver = "def parse(text):\n    if not text:\n        raise ValueError('empty version')\n    return tuple(int(part) for part in text.split('.'))\n"

func ptcRecordHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return "sha256=" + base64.RawURLEncoding.EncodeToString(sum[:])
}

// ptcLibraries installs tinyver 1.0 as pip --target would: its package and a
// dist-info whose RECORD lists each file's hash.
func ptcLibraries(t *testing.T, module string) string {
	t.Helper()
	dir := t.TempDir()
	metadata := "Metadata-Version: 2.1\nName: tinyver\nVersion: 1.0\n"
	pa310Write(t, dir, "tinyver/__init__.py", module)
	pa310Write(t, dir, "tinyver-1.0.dist-info/METADATA", metadata)
	record := fmt.Sprintf("tinyver/__init__.py,%s,%d\ntinyver-1.0.dist-info/METADATA,%s,%d\ntinyver-1.0.dist-info/RECORD,,\n",
		ptcRecordHash(ptcTinyver), len(ptcTinyver), ptcRecordHash(metadata), len(metadata))
	pa310Write(t, dir, "tinyver-1.0.dist-info/RECORD", record)
	return dir
}

func ptcContract(id string, operation project.ComponentOperation, contract string, tests ...string) project.ComponentContract {
	c := project.ComponentContract{ID: id, Operation: operation, Contract: pa310Ref(ptcContracts, contract)}
	for _, test := range tests {
		ref := pa310Ref(ptcTests, test)
		c.Tests = append(c.Tests, ref)
		c.Invocations = append(c.Invocations, project.ComponentInvocation{ID: strings.ReplaceAll(test, ".", "-"), Kind: "test", Check: ref,
			Command: "python", Args: []string{"-m", "unittest", "tests.test_contracts." + test}, WorkingDir: "."})
	}
	return c
}

func ptcFixture(t *testing.T) (string, *project.Project) {
	t.Helper()
	root, p := pa315Fixture(t, false)
	pa310Write(t, root, "store/__init__.py", "")
	pa310Write(t, root, "store/state.py", "def save(task):\n    task['saved'] = True\n    return task\n")
	pa310Write(t, root, ptcTests, ptcTestSource)
	pa310Write(t, root, ptcContracts, ptcContractSource)
	pa310Write(t, root, "requirements.txt", "tinyver==1.0\n")
	p.Manifest.Components.TrustedContracts = []project.ComponentContract{
		ptcContract("json.loads", project.ComponentOperation{Kind: "standard_library", Module: "json", Symbol: "loads"}, "loads", "LoadsTests.test_parses_objects", "LoadsTests.test_rejects_malformed"),
		ptcContract("tinyver.parse", project.ComponentOperation{Kind: "dependency", Distribution: "tinyver", Module: "tinyver", Symbol: "parse"}, "parse", "ParseTests.test_orders", "ParseTests.test_rejects_empty"),
		ptcContract("store.save", project.ComponentOperation{Kind: "application", Source: "store/state.py", Symbol: "save"}, "save", "SaveTests.test_marks_saved"),
	}
	pa310Save(t, p)
	return root, p
}

// ptcDependencies is evidence for the resolved tinyver wheel, as the
// dependency image builder records it.
func ptcDependencies(version string) func(context.Context, *PythonApplicationWorkspace) (string, *toolchain.PythonDependencies, error) {
	return func(_ context.Context, w *PythonApplicationWorkspace) (string, *toolchain.PythonDependencies, error) {
		hash := ""
		for _, f := range w.Files {
			if f.Path == "requirements.txt" {
				hash = f.SHA256
			}
		}
		digest := func(c string) string { return "sha256:" + strings.Repeat(c, 64) }
		return pa315Image, &toolchain.PythonDependencies{Image: pa315Image, Base: toolchain.PythonImage, Platform: "linux/amd64", Recipe: digest("c"),
			Inputs:          []toolchain.PythonInput{{Path: "requirements.txt", SHA256: hash}},
			CanonicalInputs: []toolchain.PythonInput{{Path: "constraints.txt", SHA256: digest("d")}, {Path: "requirements.txt", SHA256: digest("e")}},
			Artifacts:       []toolchain.PythonArtifact{{Name: "tinyver", Version: version, Filename: "tinyver-" + version + "-py3-none-any.whl", SHA256: digest("f")}},
		}, nil
	}
}

func ptcSession(t *testing.T, p *project.Project, libraries, version string) *PythonApplicationSession {
	t.Helper()
	return pa315Prepare(t, p, PythonApplicationSessionOptions{SelectImage: ptcDependencies(version), Execute: pa315LocalPython(t, libraries)})
}

// ptcRun runs every backing-test invocation and returns a report holding
// them, attributed as the gate does.
func ptcRun(t *testing.T, s *PythonApplicationSession) *Report {
	t.Helper()
	r := &Report{Project: "task lifecycle", Bounds: map[string]string{"Limit": "2"}, Application: &PythonApplicationReport{},
		Toolchain: Toolchain{PythonImage: toolchain.PythonImage, PythonDependencies: s.Provenance().Dependencies}}
	contracts := pythonApplicationContracts(s.inventory)
	for _, v := range s.Invocations() {
		if _, ok := contracts[v.Key.Target]; !ok {
			continue
		}
		x, err := s.Run(context.Background(), v.Key)
		if x == nil {
			t.Fatalf("%s: %v", v.Key.ID, err)
		}
		r.Application.Executions = append(r.Application.Executions, *x)
	}
	if err := r.attributePythonApplication(s.inventory); err != nil {
		t.Fatal(err)
	}
	return r
}

func ptcStanding(t *testing.T, r *Report, id string) ComponentContractEvidence {
	t.Helper()
	for _, c := range r.Components.Contracts {
		if c.Contract == id {
			return c
		}
	}
	t.Fatalf("no standing for %s", id)
	return ComponentContractEvidence{}
}

func ptcExecution(t *testing.T, r *Report, invocation string) PythonApplicationExecutionResult {
	t.Helper()
	for _, x := range r.Application.Executions {
		if x.Key.ID == invocation {
			return x
		}
	}
	t.Fatalf("no execution %s", invocation)
	return PythonApplicationExecutionResult{}
}

func ptcSelected(t *testing.T, x PythonApplicationExecutionResult) PythonApplicationTest {
	t.Helper()
	for _, row := range x.Tests {
		if row.Check == x.Key.Check {
			return row
		}
	}
	t.Fatalf("%s: selected test missing: %+v", x.Key.ID, x.Tests)
	return PythonApplicationTest{}
}

func ptcDiagnostic(r *Report, kind, contract string) string {
	for _, d := range r.Components.ContractDiagnostics {
		if d.Kind == kind && d.Contract == contract {
			return d.Detail
		}
	}
	return ""
}

func TestPythonApplicationContractTestsReachRealImplementations(t *testing.T) {
	_, p := ptcFixture(t)
	s := ptcSession(t, p, ptcLibraries(t, ptcTinyver), "1.0")
	r := ptcRun(t, s)
	for _, id := range []string{"json.loads", "tinyver.parse", "store.save"} {
		if c := ptcStanding(t, r, id); c.Support != "tested" || c.Use != "declared" {
			t.Fatalf("%s: %+v; diagnostics %+v", id, c, r.Components.ContractDiagnostics)
		}
	}
	if len(r.Components.ContractDiagnostics) != 0 {
		t.Fatalf("diagnostics for supported contracts: %+v", r.Components.ContractDiagnostics)
	}
	for _, row := range r.Components.ContractTests {
		if row.Origin != "gate" || row.Status != "tested" {
			t.Fatalf("a backing-test row: %+v", row)
		}
	}
	// Each run resolved the operation's real implementation first.
	loads := ptcExecution(t, r, "LoadsTests-test_parses_objects")
	if b := loads.Boundary; b == nil || !b.Resolved || b.Kind != "standard_library" || b.Callable != "python" || b.File.Path != "json/__init__.py" || b.Distribution != "" {
		t.Fatalf("standard-library boundary: %+v", loads.Boundary)
	}
	parse := ptcExecution(t, r, "ParseTests-test_orders")
	if b := parse.Boundary; b == nil || !b.Resolved || b.Distribution != "tinyver" || b.Version != "1.0" || !b.Recorded || b.File.Path != "tinyver/__init__.py" || b.File.SHA256 != pa310Hash([]byte(ptcTinyver)) {
		t.Fatalf("dependency boundary: %+v", parse.Boundary)
	}
	save := ptcExecution(t, r, "SaveTests-test_marks_saved")
	if b := save.Boundary; b == nil || !b.Resolved || b.File.Path != "store/state.py" || b.Callable != "python" {
		t.Fatalf("application boundary: %+v", save.Boundary)
	}
	// Calls are counted per test, and assertions record results, mutations
	// and error outcomes.
	if row := ptcSelected(t, parse); row.Calls() != 2 || len(row.Assertions) != 1 || row.Assertions[0] != (PythonApplicationAssertion{ID: "assertLess:1", Outcome: "passed"}) {
		t.Fatalf("dependency test: %+v", row)
	}
	if row := ptcSelected(t, ptcExecution(t, r, "LoadsTests-test_rejects_malformed")); row.Calls() != 1 || len(row.Assertions) != 1 || row.Assertions[0].ID != "assertRaises:1" || row.Assertions[0].Outcome != "passed" {
		t.Fatalf("error outcome test: %+v", row)
	}
	if row := ptcSelected(t, save); row.Calls() != 1 || len(row.Assertions) != 2 || row.Assertions[1] != (PythonApplicationAssertion{ID: "assertTrue:2", Outcome: "passed"}) {
		t.Fatalf("mutation test: %+v", row)
	}
	// Supported without a proof, every contract stays a declaration.
	target := pa310ID(pa315Target, "advance")
	if err := r.AttributeComponentProofs(s.inventory, nil); err != nil {
		t.Fatal(err)
	}
	for _, c := range r.Components.Contracts {
		if c.Use != "declared" || len(c.Relying) != 0 {
			t.Fatalf("a tested contract no proof used became an assumption: %+v (%s)", c, target)
		}
	}
	// A backing test's record always carries its calls and assertions; an
	// ordinary test's carries neither.
	for _, x := range r.Application.Executions {
		for _, row := range x.Tests {
			if row.BoundaryCalls == nil || !strings.Contains(pa310JSON(t, row), `"assertions":[`) {
				t.Fatalf("a backing test's record lacks its calls or assertions: %s", pa310JSON(t, row))
			}
		}
	}
	unit, err := s.Run(context.Background(), pa315Unit(t, s).Key)
	if unit == nil {
		t.Fatal(err)
	}
	for _, row := range unit.Tests {
		if raw := pa310JSON(t, row); row.BoundaryCalls != nil || strings.Contains(raw, "boundary_calls") || strings.Contains(raw, "assertions") {
			t.Fatalf("an ordinary test's record carries contract evidence: %s", raw)
		}
	}
}

func TestPythonApplicationContractTestsFailWithoutRealSupport(t *testing.T) {
	for _, c := range []struct {
		name   string
		test   string
		args   []string
		reason string
	}{
		{"stub only", "LoadsTests.test_stub_only", nil, "the test itself made no call into the bound implementation"},
		{"skipped", "LoadsTests.test_skipped", nil, "LoadsTests.test_skipped@" + ptcTests + ": skipped"},
		{"failing contract", "LoadsTests.test_wrong", nil, "LoadsTests.test_wrong@" + ptcTests + ": failed"},
		{"passes without running it", "LoadsTests.test_skipped", []string{"-m", "unittest", "tests.test_contracts.LoadsTests.test_parses_objects"}, "didn't run"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, p := ptcFixture(t)
			loads := &p.Manifest.Components.TrustedContracts[0]
			*loads = ptcContract("json.loads", loads.Operation, "loads", "LoadsTests.test_parses_objects", c.test)
			if c.args != nil {
				loads.Invocations[1].Args = c.args
			}
			pa310Save(t, p)
			r := ptcRun(t, ptcSession(t, p, ptcLibraries(t, ptcTinyver), "1.0"))
			if got := ptcStanding(t, r, "json.loads"); got.Support != "untested" {
				t.Fatalf("unsupported contract was tested: %+v", got)
			}
			if detail := ptcDiagnostic(r, UnsupportedContract, "json.loads"); !strings.Contains(detail, c.reason) {
				t.Fatalf("diagnostic = %q, want %q; all: %+v", detail, c.reason, r.Components.ContractDiagnostics)
			}
			// Failed and partial evidence stays: the other backing test's row
			// and the other contracts keep their support.
			if ptcStanding(t, r, "tinyver.parse").Support != "tested" || ptcStanding(t, r, "store.save").Support != "tested" {
				t.Fatal("one contract's failure took another's support")
			}
			tested := 0
			for _, row := range r.Components.ContractTests {
				if row.Contract == "json.loads" && row.Status == "tested" {
					tested++
				}
			}
			if tested != 1 {
				t.Fatalf("the passing backing test lost its evidence: %+v", r.Components.ContractTests)
			}
			if r.pythonApplicationContractsTested() {
				t.Fatal("the gate would pass an unsupported contract")
			}
		})
	}
}

func TestPythonApplicationContractTestsBindTheInstalledDistribution(t *testing.T) {
	for _, c := range []struct {
		name      string
		libraries func(t *testing.T) string
		version   string
		shadow    bool
		reason    string
	}{
		{"evidence names another version", func(t *testing.T) string { return ptcLibraries(t, ptcTinyver) }, "1.1", false, "resolved an implementation the report doesn't bind"},
		{"file the RECORD doesn't list", func(t *testing.T) string {
			dir := ptcLibraries(t, ptcTinyver)
			pa310Write(t, dir, "tinyver/__init__.py", ptcTinyver+"# edited after installation\n")
			return dir
		}, "1.0", false, "resolved an implementation the report doesn't bind"},
		{"application shadows the distribution", func(t *testing.T) string { return ptcLibraries(t, ptcTinyver) }, "1.0", true, "didn't resolve the operation's implementation"},
	} {
		t.Run(c.name, func(t *testing.T) {
			root, p := ptcFixture(t)
			if c.shadow {
				pa310Write(t, root, "tinyver/__init__.py", ptcTinyver)
			}
			r := ptcRun(t, ptcSession(t, p, c.libraries(t), c.version))
			if got := ptcStanding(t, r, "tinyver.parse"); got.Support != "untested" {
				t.Fatalf("an unbound implementation supported the contract: %+v", got)
			}
			if detail := ptcDiagnostic(r, UnsupportedContract, "tinyver.parse"); !strings.Contains(detail, c.reason) {
				t.Fatalf("diagnostic = %q; all %+v", detail, r.Components.ContractDiagnostics)
			}
			if c.shadow {
				x := ptcExecution(t, r, "ParseTests-test_orders")
				if x.Boundary == nil || x.Boundary.Resolved || !strings.Contains(strings.Join(x.Boundary.Diagnostics, " "), "outside the installed dependencies") {
					t.Fatalf("a shadowing application module resolved as the dependency: %+v", x.Boundary)
				}
			}
		})
	}
}

func TestPythonApplicationContractTestsNeedAnInvocationEach(t *testing.T) {
	_, p := ptcFixture(t)
	p.Manifest.Components.TrustedContracts[2].Invocations = nil
	pa310Save(t, p)
	_, err := PreparePythonApplicationSession(context.Background(), p, filepath.Join(t.TempDir(), "stage"), PythonApplicationSessionOptions{SelectImage: ptcDependencies("1.0")})
	if err == nil || !strings.Contains(err.Error(), `trusted contract "store.save"'s backing test`) {
		t.Fatalf("a missing backing-test invocation was admitted: %v", err)
	}
	for _, args := range [][]string{{"-m", "unittest", "discover"}, {"-m", "pytest", "tests"}, {"tests/test_contracts.py"}} {
		_, p := ptcFixture(t)
		p.Manifest.Components.TrustedContracts[2].Invocations[0].Args = args
		pa310Save(t, p)
		if _, err := PreparePythonApplicationSession(context.Background(), p, filepath.Join(t.TempDir(), "stage"), PythonApplicationSessionOptions{SelectImage: ptcDependencies("1.0")}); err == nil {
			t.Fatalf("backing-test invocation %q was admitted", args)
		}
	}
}

func TestPythonApplicationContractSupportGoesStaleWithItsInputs(t *testing.T) {
	root, p := ptcFixture(t)
	s := ptcSession(t, p, ptcLibraries(t, ptcTinyver), "1.0")
	r := ptcRun(t, s)
	if !r.pythonApplicationContractsTested() {
		t.Fatalf("baseline: %+v", r.Components.ContractDiagnostics)
	}
	rejects := func(name string, inv *project.ComponentInventory) {
		t.Helper()
		before := pa310JSON(t, r)
		if err := r.AttributeComponentProofs(inv, nil); !errors.Is(err, ErrStaleComponentEvidence) {
			t.Fatalf("%s: support from the earlier run survived: %v", name, err)
		}
		if pa310JSON(t, r) != before {
			t.Fatalf("%s: a rejected attribution changed the report", name)
		}
	}
	resolve := func() *project.ComponentInventory {
		w, err := StagePythonApplication(p, filepath.Join(t.TempDir(), "stage"))
		if err != nil {
			t.Fatal(err)
		}
		return w.Inventory
	}
	pa310Write(t, root, ptcContracts, strings.Replace(ptcContractSource, "Requires(len(text) > 0)", "Requires(len(text) > 1)", 1))
	rejects("changed contract bytes", resolve())
	pa310Write(t, root, ptcContracts, ptcContractSource)
	pa310Write(t, root, ptcTests, ptcTestSource+"\n# changed\n")
	rejects("changed backing-test bytes", resolve())
	pa310Write(t, root, ptcTests, ptcTestSource)
	// A newer resolved wheel invalidates support tested against the old one.
	newer := s.Provenance().Dependencies
	newer.Artifacts = slices.Clone(newer.Artifacts)
	newer.Artifacts[0].Version, newer.Artifacts[0].Filename = "1.1", "tinyver-1.1-py3-none-any.whl"
	saved := r.Toolchain.PythonDependencies
	r.Toolchain.PythonDependencies = newer
	rejects("changed dependency resolution", s.inventory)
	r.Toolchain.PythonDependencies = saved
	if err := r.AttributeComponentProofs(s.inventory, nil); err != nil {
		t.Fatalf("unchanged inputs keep their support: %v", err)
	}
}
