package receipt_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"html"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

// Trusted contract receipts (#343). Backing-test outcomes here are supplied
// fixtures for rendering, not executed integration evidence.

const (
	rcProject   = "verification/tasks"
	rcContracts = rcProject + "/contracts.py"
	rcTests     = rcProject + "/test_contracts.py"
	rcRuntime   = "python@sha256:79e7a9b9ff1cbceff819f856fb374477792a5967759d94df266de7b7b4120e6f"
)

func rcHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func rcID(source, symbol string) string {
	return "python:" + strings.ReplaceAll(strings.TrimSuffix(source, ".py"), "/", ".") + "#" + symbol + "@" + source
}

func rcRef(source, symbol string) project.ComponentRef {
	return project.ComponentRef{Source: source, Symbol: symbol}
}

// rcReport builds an application with a proved-capable target and two trusted
// contracts, one named loads, and returns its declared-only report.
func rcReport(t *testing.T, loads string) (*project.ComponentInventory, *verify.Report) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "application")
	for name, body := range map[string]string{
		"app/tasks/__init__.py":  "",
		"app/tasks/core.py":      "import json\n\ndef load(text):\n    Requires(len(text) > 0)\n    return json.loads(text)\n",
		rcContracts:              "def loads(text: str) -> object:\n    Requires(len(text) > 0)\n    ...\n\ndef version(text: str) -> object:\n    Requires(len(text) > 0)\n    ...\n",
		rcTests:                  "import unittest\nclass LoadsTests(unittest.TestCase):\n    def test_parses(self):\n        pass\nclass VersionTests(unittest.TestCase):\n    def test_orders(self):\n        pass\n",
		rcProject + "/driver.py": "def drive():\n    pass\n",
		rcProject + "/.invariant/specs/Tasks.tla": "---- MODULE Tasks ----\nVARIABLE n\nInit == n = 0\nNext == n' = n\nSpec == Init /\\ [][Next]_n\nSafe == n >= 0\n====\n",
	} {
		file := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	target := rcRef("app/tasks/core.py", "load")
	p := &project.Project{Dir: filepath.Join(root, filepath.FromSlash(rcProject)), Manifest: project.Manifest{
		Name: "tasks", Language: "python", Code: ".", Module: ".invariant/specs/Tasks.tla", Conformance: "driver.py", Existing: []string{"app/tasks"},
		Components: &project.ComponentsConfig{Scope: []string{"app/tasks"},
			Checks: []project.ComponentCheck{{Target: target, Proof: &project.ComponentProof{Contracts: []project.ComponentRef{target}}}},
			TrustedContracts: []project.ComponentContract{
				{ID: loads, Operation: project.ComponentOperation{Kind: "standard_library", Module: "json", Symbol: "loads"},
					Contract: rcRef(rcContracts, "loads"), Tests: []project.ComponentRef{rcRef(rcTests, "LoadsTests.test_parses")},
					Invocations: []project.ComponentInvocation{{ID: "loads", Kind: "test", Check: rcRef(rcTests, "LoadsTests.test_parses"), Command: "python", Args: []string{"-m", "unittest"}, WorkingDir: "."}}},
				{ID: "packaging.Version", Operation: project.ComponentOperation{Kind: "dependency", Distribution: "packaging", Module: "packaging.version", Symbol: "Version"},
					Contract: rcRef(rcContracts, "version"), Tests: []project.ComponentRef{rcRef(rcTests, "VersionTests.test_orders")}},
			}},
	}, Lock: project.Lock{Bounds: map[string]string{"Limit": "2"}, Statements: []project.Statement{{Name: "Spec", Kind: project.Spec}, {Name: "Safe", Kind: project.Invariant}}}}
	inv, err := project.ResolveComponents(p)
	if err != nil {
		t.Fatal(err)
	}
	wheel := toolchain.PythonArtifact{Name: "packaging", Version: "24.2", Filename: "packaging-24.2-py3-none-any.whl", SHA256: rcHash("wheel")}
	r := &verify.Report{Project: "tasks", Bounds: map[string]string{"Limit": "2"},
		Toolchain: verify.Toolchain{PythonImage: rcRuntime, PythonDependencies: &toolchain.PythonDependencies{Base: rcRuntime, Artifacts: []toolchain.PythonArtifact{wheel}}}}
	if err := r.AttributeComponentProofs(inv, nil); err != nil {
		t.Fatal(err)
	}
	return inv, r
}

func rcDeclaration(t *testing.T, inv *project.ComponentInventory, id string) project.Component {
	t.Helper()
	for _, c := range append(append([]project.Component(nil), inv.Components...), inv.Provenance.Declarations...) {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no declaration %s", id)
	return project.Component{}
}

func rcFile(t *testing.T, inv *project.ComponentInventory, name string) project.ComponentFile {
	t.Helper()
	for _, f := range inv.Provenance.Files {
		if f.Path == name {
			return f
		}
	}
	t.Fatalf("no file %s", name)
	return project.ComponentFile{}
}

func rcPassing(t *testing.T, inv *project.ComponentInventory, r *verify.Report, loads string) verify.ComponentContractTestResult {
	t.Helper()
	test := rcID(rcTests, "LoadsTests.test_parses")
	v := inv.TrustedContracts[0].Invocations[0]
	for _, c := range inv.TrustedContracts {
		if c.ID == loads {
			v = c.Invocations[0]
		}
	}
	var binding *verify.ComponentContractBinding
	for _, c := range r.Components.Contracts {
		if c.Contract == loads {
			binding = c.Binding
		}
	}
	return verify.ComponentContractTestResult{Contract: loads, Check: test, Invocation: &v,
		Runner: &verify.ComponentProofTool{Name: "Python unittest", Version: rcRuntime}, Binding: binding,
		Inputs:     []project.Component{rcDeclaration(t, inv, rcID(rcContracts, "loads")), rcDeclaration(t, inv, test)},
		FileInputs: []project.ComponentFile{rcFile(t, inv, rcContracts), rcFile(t, inv, rcTests)},
		Outcome:    "passed", Tests: []verify.ComponentRuntimeTestResult{{Check: test, Exercised: true, Outcome: "passed"}},
		Implementation: &verify.ComponentContractImplementation{Kind: "standard_library", Callable: "python",
			File: &project.ComponentFile{Path: "json/__init__.py", SHA256: rcHash("the runtime's json")}, Python: "3.13.15"},
		Exercise: []verify.ComponentContractExercise{{Check: test, Calls: 1}}}
}

// rcRow finds the table row whose first cell holds text, and its cells.
func rcRow(t *testing.T, md, first string) []string {
	t.Helper()
	for _, line := range componentMDLines(md) {
		if cells := componentMDCells(line); len(cells) > 0 && html.UnescapeString(strings.TrimSuffix(strings.TrimPrefix(cells[0], "<code>"), "</code>")) == first {
			return cells
		}
	}
	t.Fatalf("no row for %q in:\n%s", first, md)
	return nil
}

func TestTrustedContractReceiptShowsDeclarationsAsUntestedAndUnused(t *testing.T) {
	_, r := rcReport(t, "json.loads")
	md := receipt.Markdown(r)
	for _, want := range []string{"### Trusted contracts", "nothing here proves the operation itself", "A declaration alone shows no call, no test run and no proof that used it",
		"### Trusted contract tests", "no supplied results", "### Trusted boundary diagnostics\n\nnone\n"} {
		if !strings.Contains(md, want) {
			t.Fatalf("receipt lacks %q:\n%s", want, md)
		}
	}
	loads := rcRow(t, md, "json.loads")
	if len(loads) != 5 || loads[2] != "trusted · untested" || loads[3] != "declaration only: no proof reported using it" ||
		!strings.Contains(loads[1], "standard library") || !strings.Contains(loads[4], "runtime <code>python@sha256:79e7a9b9ff1c</code>") {
		t.Fatalf("declared standard-library contract row: %q", loads)
	}
	version := rcRow(t, md, "packaging.Version")
	if version[2] != "trusted · untested" || !strings.Contains(version[4], "wheel <code>packaging</code> <code>24.2</code> · <code>packaging-24.2-py3-none-any.whl</code>") {
		t.Fatalf("declared dependency contract row: %q", version)
	}
	for _, row := range [][]string{loads, version} {
		if strings.Contains(strings.Join(row, " "), "proved") {
			t.Fatalf("a trusted contract was described as proved: %q", row)
		}
	}
}

func TestTrustedContractReceiptQualifiesSuppliedResultsAndProofUse(t *testing.T) {
	inv, r := rcReport(t, "json.loads")
	target := rcID("app/tasks/core.py", "load")
	loads := rcDeclaration(t, inv, rcID(rcContracts, "loads"))
	proof := verify.ComponentProofResult{Target: target, Verifier: "nagini", Passed: true,
		Inputs: []project.Component{rcDeclaration(t, inv, target), loads}, Verified: []string{target}, Contracts: []string{target},
		Assumed: []verify.ComponentContractUse{{Contract: "json.loads", Declaration: loads}}}
	if err := r.AttributeComponentProofs(inv, []verify.ComponentProofResult{proof}); err != nil {
		t.Fatal(err)
	}
	md := receipt.Markdown(r)
	if row := rcRow(t, md, "untested_assumption"); !strings.Contains(row[1], "json.loads") || !strings.Contains(row[2], "app.tasks.core") {
		t.Fatalf("untested assumption diagnostic: %q", row)
	}
	if err := r.AttributeComponentContractTests(inv, []verify.ComponentContractTestResult{rcPassing(t, inv, r, "json.loads")}); err != nil {
		t.Fatal(err)
	}
	md = receipt.Markdown(r)
	row := rcRow(t, md, "json.loads")
	if row[2] != "trusted · tested" || !strings.HasPrefix(row[3], "assumed by <code>") || !strings.Contains(row[3], "app.tasks.core#load") {
		t.Fatalf("tested, assumed contract row: %q", row)
	}
	if other := rcRow(t, md, "packaging.Version"); other[2] != "trusted · untested" || other[3] != "declaration only: no proof reported using it" {
		t.Fatalf("an unused contract must stay a declaration: %q", other)
	}
	for _, want := range []string{"supplied results describe runs the component API didn't make", "Origin: <code>supplied</code>", "calls into it from", "assumed trusted contracts: <code>json.loads</code>",
		"### Trusted boundary diagnostics\n\nnone\n"} {
		if !strings.Contains(md, want) {
			t.Fatalf("receipt lacks %q:\n%s", want, md)
		}
	}
	var tests []string
	for _, line := range componentMDLines(md) {
		if cells := componentMDCells(line); len(cells) == 4 && strings.Contains(cells[1], "LoadsTests") {
			tests = cells
		}
	}
	if len(tests) != 4 || tests[2] != "tested" {
		t.Fatalf("supplied backing-test row: %q", tests)
	}
	// An unaccounted boundary is diagnosed, and the proof row says why.
	proof.Unaccounted = []verify.ComponentUnaccounted{{Kind: verify.MissingContract, Operation: &project.ResolvedComponentOperation{Kind: "standard_library", Module: "json", Symbol: "dumps"}, Detail: "no contract"}}
	if err := r.AttributeComponentProofs(inv, []verify.ComponentProofResult{proof}); err != nil {
		t.Fatal(err)
	}
	md = receipt.Markdown(r)
	if row := rcRow(t, md, "missing_contract"); !strings.Contains(row[3], "json.dumps: no contract") {
		t.Fatalf("missing contract diagnostic: %q", row)
	}
	if !strings.Contains(md, "unaccounted boundary: <code>missing&#95;contract</code> standard library: <code>json.dumps</code>: no contract") {
		t.Fatalf("proof row lacks its unaccounted boundary:\n%s", md)
	}
}

func TestTrustedContractReceiptDistrustsForgedDecodedStanding(t *testing.T) {
	inv, r := rcReport(t, "json.loads")
	x := rcPassing(t, inv, r, "json.loads")
	x.Runner = nil
	if err := r.AttributeComponentContractTests(inv, []verify.ComponentContractTestResult{x}); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var forged verify.Report
	if err := json.Unmarshal(b, &forged); err != nil {
		t.Fatal(err)
	}
	for i := range forged.Components.Contracts {
		forged.Components.Contracts[i].Support, forged.Components.Contracts[i].Use = "tested", "assumed"
		forged.Components.Contracts[i].Relying = []string{"python:forged#x@forged.py"}
	}
	forged.Components.ContractTests[0].Status = "tested"
	forged.Components.ContractTests[0].Tests[0].Status = "tested"
	md := receipt.Markdown(&forged)
	if row := rcRow(t, md, "json.loads"); row[2] != "trusted · untested" || row[3] != "declaration only: no proof reported using it" {
		t.Fatalf("forged decoded standing was rendered: %q", row)
	}
	if strings.Contains(md, "forged") {
		t.Fatal("a forged relying component was rendered")
	}
	for _, line := range componentMDLines(md) {
		if cells := componentMDCells(line); len(cells) == 4 && strings.Contains(cells[1], "LoadsTests") && cells[2] != "unchecked" {
			t.Fatalf("a forged backing-test status was rendered: %q", cells)
		}
	}
}

func TestTrustedContractReceiptEscapesUntrustedText(t *testing.T) {
	id := "loads|proved|<b>*x*</b>"
	_, r := rcReport(t, id)
	md := receipt.Markdown(r)
	if strings.Contains(md, "<b>") || strings.Contains(md, "|proved|") {
		t.Fatalf("untrusted contract text escaped its cell:\n%s", md)
	}
	if row := rcRow(t, md, id); len(row) != 5 || row[2] != "trusted · untested" {
		t.Fatalf("escaped contract row: %q", row)
	}
}

func TestTrustedContractReceiptRederivesProofStatusAndDiagnostics(t *testing.T) {
	inv, r := rcReport(t, "json.loads")
	inv.TrustedContracts = nil
	target := rcID("app/tasks/core.py", "load")
	plain := &verify.Report{Project: "tasks", Bounds: r.Bounds, Toolchain: r.Toolchain}
	if err := plain.AttributeComponentProofs(inv, []verify.ComponentProofResult{{Target: target, Verifier: "nagini", Passed: true,
		Inputs: []project.Component{rcDeclaration(t, inv, target)}, Verified: []string{target}, Contracts: []string{target},
		Unaccounted: []verify.ComponentUnaccounted{{Kind: verify.MissingContract, Operation: &project.ResolvedComponentOperation{Kind: "standard_library", Module: "json", Symbol: "dumps"}}}}}); err != nil {
		t.Fatal(err)
	}
	// Without declared contracts, a proof's unaccounted boundary still shows.
	md := receipt.Markdown(plain)
	if !strings.Contains(md, "### Trusted boundary diagnostics") || rcRow(t, md, "missing_contract") == nil || strings.Contains(md, "### Trusted contracts") {
		t.Fatalf("diagnostics without contracts:\n%s", md)
	}
	// A decoded proof edited to proved stays unchecked: its result doesn't qualify.
	b, err := json.Marshal(plain)
	if err != nil {
		t.Fatal(err)
	}
	var forged verify.Report
	if err := json.Unmarshal(b, &forged); err != nil {
		t.Fatal(err)
	}
	for i := range forged.Components.Proofs {
		forged.Components.Proofs[i].Status = "proved"
	}
	md = receipt.Markdown(&forged)
	if !strings.Contains(md, "Component proofs: **0 proved;") {
		t.Fatalf("a forged proof status was rendered:\n%s", md)
	}
	if row := rcRow(t, md, target); row[1] != "unchecked" {
		t.Fatalf("forged proof row: %q", row)
	}
}

func TestReceiptsWithoutTrustedContractsHaveNoContractViews(t *testing.T) {
	inv, r := rcReport(t, "json.loads")
	inv.TrustedContracts = nil
	plain := &verify.Report{Project: "tasks", Bounds: r.Bounds, Toolchain: r.Toolchain}
	if err := plain.AttributeComponentProofs(inv, nil); err != nil {
		t.Fatal(err)
	}
	if md := receipt.Markdown(plain); strings.Contains(md, "Trusted contract") || strings.Contains(md, "Trusted boundary") {
		t.Fatalf("a receipt without trusted contracts shows their views:\n%s", md)
	}
}
