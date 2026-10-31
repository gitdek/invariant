package verify_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

// Trusted contract evidence (#343, #294's step 3). These tests supply
// backing-test outcomes as fixtures to exercise attribution and reporting;
// none of them is executed integration evidence, and the API runs nothing.

const (
	ctProject   = "verification/tasks"
	ctContracts = ctProject + "/contracts.py"
	ctTests     = ctProject + "/test_contracts.py"
	ctRuntime   = "python@sha256:79e7a9b9ff1cbceff819f856fb374477792a5967759d94df266de7b7b4120e6f"
)

const ctContractSource = "from nagini_contracts.contracts import *\n\ndef loads(text: str) -> object:\n    Requires(len(text) > 0)\n    ...\n\ndef version(text: str) -> object:\n    Requires(len(text) > 0)\n    ...\n\ndef save(task: object) -> object:\n    Ensures(Result() is task)\n    ...\n"
const ctTestSource = "import unittest\nclass LoadsTests(unittest.TestCase):\n    def test_parses(self):\n        pass\n    def test_rejects_empty(self):\n        pass\nclass VersionTests(unittest.TestCase):\n    def test_orders(self):\n        pass\nclass SaveTests(unittest.TestCase):\n    def test_returns(self):\n        pass\n"

func ctWrite(t *testing.T, root, name, body string) {
	t.Helper()
	file := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func ctID(source, symbol string) string {
	return "python:" + strings.ReplaceAll(strings.TrimSuffix(source, ".py"), "/", ".") + "#" + symbol + "@" + source
}

func ctRef(source, symbol string) project.ComponentRef {
	return project.ComponentRef{Source: source, Symbol: symbol}
}

func ctHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func ctFixture(t *testing.T) (string, *project.Project) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "application")
	for name, body := range map[string]string{
		"app/__init__.py":                         "",
		"app/tasks/__init__.py":                   "",
		"app/tasks/core.py":                       "import json\n\ndef load(text):\n    Requires(len(text) > 0)\n    return json.loads(text)\n",
		"app/storage/__init__.py":                 "",
		"app/storage/store.py":                    "def save(task):\n    return task\n",
		ctContracts:                               ctContractSource,
		ctTests:                                   ctTestSource,
		ctProject + "/fixture.json":               "{\"name\": \"task\"}\n",
		ctProject + "/driver.py":                  "from app.tasks.core import load\ndef drive():\n    return load('{}')\n",
		ctProject + "/.invariant/specs/Tasks.tla": "---- MODULE Tasks ----\nEXTENDS Naturals\nCONSTANT Limit\nVARIABLE n\nInit == n = 0\nNext == n' = n\nSpec == Init /\\ [][Next]_n\nSafe == n >= 0\n====\n",
	} {
		ctWrite(t, root, name, body)
	}
	target := ctRef("app/tasks/core.py", "load")
	p := &project.Project{Dir: filepath.Join(root, filepath.FromSlash(ctProject)), Manifest: project.Manifest{
		Name: "tasks", Language: "python", Code: ".", Module: ".invariant/specs/Tasks.tla", Conformance: "driver.py",
		Existing: []string{"app/tasks"},
		Components: &project.ComponentsConfig{
			Scope:  []string{"app/tasks"},
			Checks: []project.ComponentCheck{{Target: target, Proof: &project.ComponentProof{Contracts: []project.ComponentRef{target}}}},
			TrustedContracts: []project.ComponentContract{
				{
					ID:        "json.loads",
					Operation: project.ComponentOperation{Kind: "standard_library", Module: "json", Symbol: "loads"},
					Contract:  ctRef(ctContracts, "loads"),
					Tests:     []project.ComponentRef{ctRef(ctTests, "LoadsTests.test_parses"), ctRef(ctTests, "LoadsTests.test_rejects_empty")},
					Invocations: []project.ComponentInvocation{{ID: "loads", Kind: "test", Check: ctRef(ctTests, "LoadsTests.test_parses"),
						Command: "python", Args: []string{"-m", "unittest", "verification.tasks.test_contracts"}, WorkingDir: ".", Inputs: []string{ctProject + "/fixture.json"}},
						{ID: "rejects", Kind: "test", Check: ctRef(ctTests, "LoadsTests.test_rejects_empty"),
							Command: "python", Args: []string{"-m", "unittest", "verification.tasks.test_contracts.LoadsTests.test_rejects_empty"}, WorkingDir: "."}},
				},
				{
					ID:        "packaging.Version",
					Operation: project.ComponentOperation{Kind: "dependency", Distribution: "packaging", Module: "packaging.version", Symbol: "Version"},
					Contract:  ctRef(ctContracts, "version"),
					Tests:     []project.ComponentRef{ctRef(ctTests, "VersionTests.test_orders")},
					Invocations: []project.ComponentInvocation{{ID: "version", Kind: "test", Check: ctRef(ctTests, "VersionTests.test_orders"),
						Command: "python", Args: []string{"-m", "unittest", "verification.tasks.test_contracts.VersionTests"}, WorkingDir: "."}},
				},
				{
					ID:        "store.save",
					Operation: project.ComponentOperation{Kind: "application", Source: "app/storage/store.py", Symbol: "save"},
					Contract:  ctRef(ctContracts, "save"),
					Tests:     []project.ComponentRef{ctRef(ctTests, "SaveTests.test_returns")},
					Invocations: []project.ComponentInvocation{{ID: "save", Kind: "test", Check: ctRef(ctTests, "SaveTests.test_returns"),
						Command: "python", Args: []string{"-m", "unittest", "verification.tasks.test_contracts.SaveTests"}, WorkingDir: "."}},
				},
			},
		},
	}, Lock: project.Lock{Bounds: map[string]string{"Limit": "2"}, Statements: []project.Statement{{Name: "Spec", Kind: project.Spec}, {Name: "Safe", Kind: project.Invariant}}}}
	ctSave(t, p)
	return root, p
}

func ctSave(t *testing.T, p *project.Project) {
	t.Helper()
	for name, value := range map[string]any{".invariant/invariant.json": p.Manifest, ".invariant/ratified.lock": p.Lock} {
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		ctWrite(t, p.Dir, name, string(b))
	}
}

func ctInventory(t *testing.T, dir string) *project.ComponentInventory {
	t.Helper()
	p, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	inv, err := project.ResolveComponents(p)
	if err != nil {
		t.Fatal(err)
	}
	return inv
}

func ctWheel() toolchain.PythonArtifact {
	return toolchain.PythonArtifact{Name: "packaging", Version: "24.2", Filename: "packaging-24.2-py3-none-any.whl", SHA256: ctHash("packaging 24.2 wheel")}
}

func ctToolchain(wheels ...toolchain.PythonArtifact) verify.Toolchain {
	return verify.Toolchain{PythonImage: ctRuntime, PythonDependencies: &toolchain.PythonDependencies{Base: ctRuntime, Platform: "linux/amd64", Artifacts: wheels}}
}

func ctReport(t *testing.T, inv *project.ComponentInventory, tc verify.Toolchain) *verify.Report {
	t.Helper()
	r := &verify.Report{Project: "tasks", Bounds: map[string]string{"Limit": "2"}, Toolchain: tc}
	if err := r.AttributeComponentProofs(inv, nil); err != nil {
		t.Fatalf("an inventory with trusted contracts must take evidence: %v", err)
	}
	return r
}

func ctJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ctKeys names the keys of a report's components, and of its proof results.
func ctKeys(t *testing.T, r *verify.Report) (components, results map[string]bool) {
	t.Helper()
	var decoded struct {
		Components map[string]json.RawMessage `json:"components"`
	}
	if err := json.Unmarshal([]byte(ctJSON(t, r)), &decoded); err != nil {
		t.Fatal(err)
	}
	components, results = map[string]bool{}, map[string]bool{}
	for key := range decoded.Components {
		components[key] = true
	}
	var proofs []struct {
		Result map[string]json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(decoded.Components["proofs"], &proofs); err != nil {
		t.Fatal(err)
	}
	for _, p := range proofs {
		for key := range p.Result {
			results[key] = true
		}
	}
	return components, results
}

func ctContract(t *testing.T, r *verify.Report, id string) verify.ComponentContractEvidence {
	t.Helper()
	for _, c := range r.Components.Contracts {
		if c.Contract == id {
			return c
		}
	}
	t.Fatalf("no standing for trusted contract %s: %+v", id, r.Components.Contracts)
	return verify.ComponentContractEvidence{}
}

func ctDeclaration(t *testing.T, inv *project.ComponentInventory, id string) project.Component {
	t.Helper()
	for _, c := range append(append([]project.Component(nil), inv.Components...), inv.Provenance.Declarations...) {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no declaration %s", id)
	return project.Component{}
}

func ctFile(t *testing.T, inv *project.ComponentInventory, name string) project.ComponentFile {
	t.Helper()
	for _, f := range append(append([]project.ComponentFile(nil), inv.Files...), inv.Provenance.Files...) {
		if f.Path == name {
			return f
		}
	}
	t.Fatalf("no file %s", name)
	return project.ComponentFile{}
}

func ctInvocation(t *testing.T, inv *project.ComponentInventory, contract string) *project.ResolvedComponentInvocation {
	t.Helper()
	for _, c := range inv.TrustedContracts {
		if c.ID == contract {
			v := c.Invocations[0]
			return &v
		}
	}
	t.Fatalf("no trusted contract %s", contract)
	return nil
}

// ctInvocationOf is the contract's declared invocation with this ID.
func ctInvocationOf(t *testing.T, inv *project.ComponentInventory, contract, id string) *project.ResolvedComponentInvocation {
	t.Helper()
	for _, c := range inv.TrustedContracts {
		for _, v := range c.Invocations {
			if c.ID == contract && v.ID == id {
				return &v
			}
		}
	}
	t.Fatalf("no invocation %s of %s", id, contract)
	return nil
}

// ctPassing is a complete supplied run of json.loads's backing tests.
func ctPassing(t *testing.T, inv *project.ComponentInventory, r *verify.Report) verify.ComponentContractTestResult {
	t.Helper()
	parses, rejects := ctID(ctTests, "LoadsTests.test_parses"), ctID(ctTests, "LoadsTests.test_rejects_empty")
	binding := ctContract(t, r, "json.loads").Binding
	return verify.ComponentContractTestResult{
		Contract: "json.loads", Check: parses, Invocation: ctInvocation(t, inv, "json.loads"),
		Runner:     &verify.ComponentProofTool{Name: "Python unittest", Version: ctRuntime},
		Binding:    binding,
		Inputs:     []project.Component{ctDeclaration(t, inv, ctID(ctContracts, "loads")), ctDeclaration(t, inv, parses), ctDeclaration(t, inv, rejects)},
		FileInputs: []project.ComponentFile{ctFile(t, inv, ctContracts), ctFile(t, inv, ctTests), ctFile(t, inv, ctProject+"/fixture.json")},
		Outcome:    "passed",
		Tests: []verify.ComponentRuntimeTestResult{
			{Check: parses, Exercised: true, Outcome: "passed"},
			{Check: rejects, Exercised: true, Outcome: "passed"},
		},
		Implementation: &verify.ComponentContractImplementation{Kind: "standard_library", Callable: "python",
			File: &project.ComponentFile{Path: "json/__init__.py", SHA256: ctHash("the runtime's json")}, Python: "3.13.15"},
		Exercise: []verify.ComponentContractExercise{{Check: parses, Calls: 1}, {Check: rejects, Calls: 2}},
	}
}

func TestTrustedContractsStartAsDeclarationsBoundToActualIdentities(t *testing.T) {
	_, p := ctFixture(t)
	inv := ctInventory(t, p.Dir)
	r := ctReport(t, inv, ctToolchain(ctWheel()))
	if len(r.Components.Contracts) != 3 || len(r.Components.ContractTests) != 0 || len(r.Components.ContractDiagnostics) != 0 {
		t.Fatalf("declared contracts = %+v, tests %+v, diagnostics %+v", r.Components.Contracts, r.Components.ContractTests, r.Components.ContractDiagnostics)
	}
	for _, c := range r.Components.Contracts {
		if c.Support != "untested" || c.Use != "declared" || len(c.Relying) != 0 || c.Binding == nil || c.Binding.Runtime != ctRuntime {
			t.Fatalf("a declaration acquired support, use or a mutable identity: %+v", c)
		}
	}
	if b := ctContract(t, r, "json.loads").Binding; b.Kind != "standard_library" || b.Wheel != nil || b.Declaration != nil {
		t.Fatalf("standard-library binding = %+v", b)
	}
	if b := ctContract(t, r, "packaging.Version").Binding; b.Kind != "dependency" || b.Wheel == nil || *b.Wheel != ctWheel() {
		t.Fatalf("dependency binding = %+v", b)
	}
	if b := ctContract(t, r, "store.save").Binding; b.Kind != "application" || b.Declaration == nil || *b.Declaration != ctDeclaration(t, inv, ctID("app/storage/store.py", "save")) {
		t.Fatalf("application binding = %+v", b)
	}
	for _, row := range r.Components.Proofs {
		if row.Status != "unchecked" {
			t.Fatalf("a declaration proved %s", row.Target)
		}
	}
	if keys, _ := ctKeys(t, r); !keys["contracts"] || keys["contract_tests"] || keys["contract_diagnostics"] {
		t.Fatalf("declared-only JSON: %s", ctJSON(t, r))
	}
}

func TestTrustedContractBindingsRefuseNamesAndMutableTags(t *testing.T) {
	_, p := ctFixture(t)
	inv := ctInventory(t, p.Dir)
	diagnosed := func(r *verify.Report) map[string]string {
		out := map[string]string{}
		for _, d := range r.Components.ContractDiagnostics {
			if d.Kind == verify.UnresolvedOperation {
				out[d.Contract] = d.Detail
			}
		}
		return out
	}
	tagged := ctToolchain(ctWheel())
	tagged.PythonDependencies.Base = "python:3.13"
	r := ctReport(t, inv, tagged)
	if got := diagnosed(r); len(got) != 3 || !strings.Contains(got["json.loads"], "pinned by digest") {
		t.Fatalf("a mutable runtime tag bound contracts: %+v", r.Components.Contracts)
	}
	for _, c := range r.Components.Contracts {
		if c.Binding != nil {
			t.Fatalf("bound without a pinned runtime: %+v", c)
		}
	}
	missing := ctReport(t, inv, ctToolchain())
	if got := diagnosed(missing); len(got) != 1 || !strings.Contains(got["packaging.Version"], `no resolved wheel provides distribution "packaging"`) {
		t.Fatalf("a missing wheel bound a dependency: %+v", missing.Components.ContractDiagnostics)
	}
	incomplete := ctWheel()
	incomplete.SHA256 = ""
	if got := diagnosed(ctReport(t, inv, ctToolchain(incomplete))); !strings.Contains(got["packaging.Version"], "lacks a version, filename or SHA-256") {
		t.Fatalf("a wheel without a hash bound a dependency: %+v", got)
	}
	twice := ctWheel()
	twice.Version = "24.1"
	if got := diagnosed(ctReport(t, inv, ctToolchain(ctWheel(), twice))); !strings.Contains(got["packaging.Version"], "2 resolved wheels") {
		t.Fatalf("an ambiguous wheel bound a dependency: %+v", got)
	}
	// Without dependency evidence, the Python image is the runtime.
	r = ctReport(t, inv, verify.Toolchain{PythonImage: ctRuntime})
	if b := ctContract(t, r, "json.loads").Binding; b == nil || b.Runtime != ctRuntime {
		t.Fatalf("standard library without dependencies: %+v", b)
	}
}

func TestTrustedContractTestsSupportOnlyFromCompleteRuns(t *testing.T) {
	_, p := ctFixture(t)
	inv := ctInventory(t, p.Dir)
	r := ctReport(t, inv, ctToolchain(ctWheel()))
	passing := ctPassing(t, inv, r)
	if err := r.AttributeComponentContractTests(inv, []verify.ComponentContractTestResult{passing}); err != nil {
		t.Fatal(err)
	}
	if row := r.Components.ContractTests[0]; row.Status != "tested" || row.Origin != "supplied" || len(row.Tests) != 2 || row.Tests[0].Status != "tested" || row.Tests[1].Status != "tested" {
		t.Fatalf("complete passing run: %+v", row)
	}
	c := ctContract(t, r, "json.loads")
	if c.Support != "tested" || c.Use != "declared" {
		t.Fatalf("tested support must leave an unused contract a declaration: %+v", c)
	}
	for _, other := range []string{"packaging.Version", "store.save"} {
		if ctContract(t, r, other).Support != "untested" {
			t.Fatalf("%s gained another contract's support", other)
		}
	}
	for _, c := range []struct {
		name   string
		mutate func(*verify.ComponentContractTestResult)
		row    string
	}{
		{"failed process", func(x *verify.ComponentContractTestResult) { x.Outcome = "failed" }, "unchecked"},
		{"interrupted process", func(x *verify.ComponentContractTestResult) { x.Outcome = "timed_out" }, "unchecked"},
		{"skipped test", func(x *verify.ComponentContractTestResult) { x.Tests[1].Outcome = "skipped" }, "tested"},
		{"failed test", func(x *verify.ComponentContractTestResult) { x.Tests[1].Outcome = "failed" }, "tested"},
		{"unexercised test", func(x *verify.ComponentContractTestResult) { x.Tests[1].Exercised = false }, "tested"},
		{"missing test", func(x *verify.ComponentContractTestResult) { x.Tests = x.Tests[:1] }, "tested"},
		{"no tests", func(x *verify.ComponentContractTestResult) { x.Tests = nil }, "unchecked"},
		{"no runner", func(x *verify.ComponentContractTestResult) { x.Runner = nil }, "unchecked"},
		{"no binding", func(x *verify.ComponentContractTestResult) { x.Binding = nil }, "unchecked"},
		{"no contract snapshot", func(x *verify.ComponentContractTestResult) { x.Inputs = x.Inputs[1:] }, "unchecked"},
		{"no contract file", func(x *verify.ComponentContractTestResult) { x.FileInputs = x.FileInputs[1:] }, "unchecked"},
		{"no invocation input", func(x *verify.ComponentContractTestResult) { x.FileInputs = x.FileInputs[:2] }, "unchecked"},
		{"no implementation", func(x *verify.ComponentContractTestResult) { x.Implementation = nil }, "unchecked"},
		{"another implementation", func(x *verify.ComponentContractTestResult) {
			x.Implementation.Distribution, x.Implementation.Version, x.Implementation.Recorded = "simplejson", "3.19", true
		}, "unchecked"},
		{"stub only", func(x *verify.ComponentContractTestResult) { x.Exercise[1].Calls = 0 }, "tested"},
		{"no exercise", func(x *verify.ComponentContractTestResult) { x.Exercise = nil }, "unchecked"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := ctReport(t, inv, ctToolchain(ctWheel()))
			x := ctPassing(t, inv, r)
			c.mutate(&x)
			if err := r.AttributeComponentContractTests(inv, []verify.ComponentContractTestResult{x}); err != nil {
				t.Fatal(err)
			}
			if row := r.Components.ContractTests[0]; row.Status != c.row {
				t.Fatalf("row status = %s, want %s: %+v", row.Status, c.row, row)
			}
			if got := ctContract(t, r, "json.loads"); got.Support != "untested" {
				t.Fatalf("incomplete backing tests supported the contract: %+v", got)
			}
		})
	}
	// An application operation's tests also need its declaration's snapshot.
	r = ctReport(t, inv, ctToolchain(ctWheel()))
	test := ctID(ctTests, "SaveTests.test_returns")
	store := ctFile(t, inv, "app/storage/store.py")
	save := verify.ComponentContractTestResult{Contract: "store.save", Check: test, Invocation: ctInvocation(t, inv, "store.save"),
		Runner: &verify.ComponentProofTool{Name: "Python unittest", Version: ctRuntime}, Binding: ctContract(t, r, "store.save").Binding,
		Inputs:     []project.Component{ctDeclaration(t, inv, ctID(ctContracts, "save")), ctDeclaration(t, inv, test)},
		FileInputs: []project.ComponentFile{ctFile(t, inv, ctContracts), ctFile(t, inv, ctTests), store},
		Outcome:    "passed", Tests: []verify.ComponentRuntimeTestResult{{Check: test, Exercised: true, Outcome: "passed"}},
		Implementation: &verify.ComponentContractImplementation{Kind: "application", Callable: "python", File: &store},
		Exercise:       []verify.ComponentContractExercise{{Check: test, Calls: 1}}}
	if err := r.AttributeComponentContractTests(inv, []verify.ComponentContractTestResult{save}); err != nil {
		t.Fatal(err)
	}
	if ctContract(t, r, "store.save").Support != "untested" {
		t.Fatal("an application operation was supported without its declaration's snapshot")
	}
	save.Inputs = append(save.Inputs, ctDeclaration(t, inv, ctID("app/storage/store.py", "save")))
	if err := r.AttributeComponentContractTests(inv, []verify.ComponentContractTestResult{save}); err != nil {
		t.Fatal(err)
	}
	if ctContract(t, r, "store.save").Support != "tested" {
		t.Fatalf("complete application support: %+v", r.Components.ContractTests)
	}
	// And the implementation its run resolved must be the declaration's own
	// source as the inventory holds it.
	other := store
	other.SHA256 = ctHash("an edited store")
	save.Implementation.File = &other
	if err := r.AttributeComponentContractTests(inv, []verify.ComponentContractTestResult{save}); err != nil {
		t.Fatal(err)
	}
	if ctContract(t, r, "store.save").Support != "untested" {
		t.Fatal("another source's implementation supported the application operation")
	}
}

func TestTrustedContractTestsRejectMismatchedAndStaleReferences(t *testing.T) {
	_, p := ctFixture(t)
	inv := ctInventory(t, p.Dir)
	for _, c := range []struct {
		name   string
		mutate func(*verify.ComponentContractTestResult)
		stale  bool
	}{
		{"unknown contract", func(x *verify.ComponentContractTestResult) { x.Contract = "json.dumps" }, false},
		{"not a backing test", func(x *verify.ComponentContractTestResult) { x.Check = ctID(ctTests, "SaveTests.test_returns") }, false},
		{"no invocation", func(x *verify.ComponentContractTestResult) { x.Invocation = nil }, false},
		{"undeclared invocation", func(x *verify.ComponentContractTestResult) { x.Invocation.ID = "nonexistent" }, false},
		{"changed invocation", func(x *verify.ComponentContractTestResult) { x.Invocation.Args = []string{"-m", "unittest"} }, true},
		{"another contract's invocation", func(x *verify.ComponentContractTestResult) { x.Invocation.ID = "save" }, false},
		{"unselected test result", func(x *verify.ComponentContractTestResult) {
			x.Tests = append(x.Tests, verify.ComponentRuntimeTestResult{Check: ctID(ctTests, "VersionTests.test_orders"), Exercised: true, Outcome: "passed"})
		}, false},
		{"invalid outcome", func(x *verify.ComponentContractTestResult) { x.Outcome = "green" }, false},
		{"unknown input", func(x *verify.ComponentContractTestResult) { x.Inputs[0].ID = ctID(ctContracts, "absent") }, false},
		{"unknown file", func(x *verify.ComponentContractTestResult) {
			x.FileInputs = append(x.FileInputs, project.ComponentFile{Path: "elsewhere.py", SHA256: ctHash("x")})
		}, false},
		{"stale input", func(x *verify.ComponentContractTestResult) { x.Inputs[0].ContractSHA256 = ctHash("Requires(True)") }, true},
		{"stale file", func(x *verify.ComponentContractTestResult) { x.FileInputs[0].SHA256 = ctHash("older bytes") }, true},
		{"stale wheel", func(x *verify.ComponentContractTestResult) {
			x.Contract, x.Check = "packaging.Version", ctID(ctTests, "VersionTests.test_orders")
			x.Invocation.ID, x.Invocation.Check, x.Invocation.Args, x.Invocation.Inputs = "version", x.Check, []string{"-m", "unittest", "verification.tasks.test_contracts.VersionTests"}, nil
			old := ctWheel()
			old.Version = "24.1"
			x.Binding = &verify.ComponentContractBinding{Kind: "dependency", Runtime: ctRuntime, Wheel: &old}
			x.Tests = []verify.ComponentRuntimeTestResult{{Check: x.Check, Exercised: true, Outcome: "passed"}}
		}, true},
		{"stale runtime", func(x *verify.ComponentContractTestResult) {
			b := *x.Binding
			b.Runtime = "python@sha256:" + strings.Repeat("0", 64)
			x.Binding = &b
		}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := ctReport(t, inv, ctToolchain(ctWheel()))
			if err := r.AttributeComponentContractTests(inv, []verify.ComponentContractTestResult{ctPassing(t, inv, r)}); err != nil {
				t.Fatal(err)
			}
			before, fingerprint := ctJSON(t, r), r.Fingerprint
			x := ctPassing(t, inv, r)
			c.mutate(&x)
			err := r.AttributeComponentContractTests(inv, []verify.ComponentContractTestResult{x})
			if err == nil {
				t.Fatal("a mismatched backing-test result was attributed")
			}
			if errors.Is(err, verify.ErrStaleComponentEvidence) != c.stale {
				t.Fatalf("stale = %t for %v", errors.Is(err, verify.ErrStaleComponentEvidence), err)
			}
			if ctJSON(t, r) != before || r.Fingerprint != fingerprint || verify.Fingerprint(*r) != fingerprint {
				t.Fatal("a rejected attribution changed the report")
			}
		})
	}
	// A binding supplied where the report binds nothing is stale too.
	r := ctReport(t, inv, ctToolchain())
	x := ctPassing(t, inv, ctReport(t, inv, ctToolchain(ctWheel())))
	x.Contract, x.Check = "packaging.Version", ctID(ctTests, "VersionTests.test_orders")
	*x.Invocation = *ctInvocation(t, inv, "packaging.Version")
	wheel := ctWheel()
	x.Binding = &verify.ComponentContractBinding{Kind: "dependency", Runtime: ctRuntime, Wheel: &wheel}
	x.Tests = []verify.ComponentRuntimeTestResult{{Check: x.Check, Exercised: true, Outcome: "passed"}}
	if err := r.AttributeComponentContractTests(inv, []verify.ComponentContractTestResult{x}); !errors.Is(err, verify.ErrStaleComponentEvidence) {
		t.Fatalf("a binding the report doesn't record: %v", err)
	}
	// Duplicates reject the batch.
	r = ctReport(t, inv, ctToolchain(ctWheel()))
	if err := r.AttributeComponentContractTests(inv, []verify.ComponentContractTestResult{ctPassing(t, inv, r), ctPassing(t, inv, r)}); err == nil {
		t.Fatal("a duplicate backing-test result was attributed")
	}
}

func TestTrustedContractTestsAnUnsuccessfulRunVetoesSupport(t *testing.T) {
	_, p := ctFixture(t)
	inv := ctInventory(t, p.Dir)
	rejects := ctID(ctTests, "LoadsTests.test_rejects_empty")
	// The loads run reports both tests passing; a second run of
	// test_rejects_empty, through its own invocation, doesn't.
	second := func(r *verify.Report, process, outcome string, exercised bool) verify.ComponentContractTestResult {
		x := ctPassing(t, inv, r)
		x.Check, x.Invocation, x.Outcome = rejects, ctInvocationOf(t, inv, "json.loads", "rejects"), process
		x.Tests = []verify.ComponentRuntimeTestResult{{Check: rejects, Exercised: exercised, Outcome: outcome}}
		return x
	}
	for _, c := range []struct {
		name, process, outcome string
		exercised              bool
		want                   string
	}{
		{"failed", "failed", "failed", true, "failed"},
		{"skipped", "passed", "skipped", false, "skipped"},
		{"timed out", "timed_out", "timed_out", true, "timed out"},
		{"unexercised", "passed", "passed", false, "wasn't exercised"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := ctReport(t, inv, ctToolchain(ctWheel()))
			if err := r.AttributeComponentContractTests(inv, []verify.ComponentContractTestResult{ctPassing(t, inv, r), second(r, c.process, c.outcome, c.exercised)}); err != nil {
				t.Fatal(err)
			}
			if got := ctContract(t, r, "json.loads"); got.Support != "untested" {
				t.Fatalf("a passing run masked an unsuccessful one: %+v", got)
			}
			found := false
			for _, d := range r.Components.ContractDiagnostics {
				found = found || d.Kind == verify.ConflictingSupport && d.Contract == "json.loads" && strings.Contains(d.Detail, rejects+" passed in one run and "+c.want+" in another")
			}
			if !found {
				t.Fatalf("no diagnostic for the conflict: %+v", r.Components.ContractDiagnostics)
			}
		})
	}
	// Two passing runs support it, with nothing to diagnose.
	r := ctReport(t, inv, ctToolchain(ctWheel()))
	if err := r.AttributeComponentContractTests(inv, []verify.ComponentContractTestResult{ctPassing(t, inv, r), second(r, "passed", "passed", true)}); err != nil {
		t.Fatal(err)
	}
	if got := ctContract(t, r, "json.loads"); got.Support != "tested" || len(r.Components.ContractDiagnostics) != 0 {
		t.Fatalf("two passing runs: %+v, %+v", got, r.Components.ContractDiagnostics)
	}
}

func TestTrustedContractStaleSnapshotsAlwaysSayStale(t *testing.T) {
	root, p := ctFixture(t)
	inv := ctInventory(t, p.Dir)
	r := ctReport(t, inv, ctToolchain(ctWheel()))
	if err := r.AttributeComponentContractTests(inv, []verify.ComponentContractTestResult{ctPassing(t, inv, r)}); err != nil {
		t.Fatal(err)
	}
	stale := func(name string, attribute func() error) {
		t.Helper()
		before := ctJSON(t, r)
		if err := attribute(); !errors.Is(err, verify.ErrStaleComponentEvidence) {
			t.Fatalf("%s: %v isn't stale", name, err)
		}
		if ctJSON(t, r) != before {
			t.Fatalf("%s: a rejected attribution changed the report", name)
		}
	}
	// A backing test's invocation input or arguments changed.
	ctWrite(t, root, ctProject+"/fixture.json", "{\"name\": \"other\"}\n")
	changed := ctInventory(t, p.Dir)
	stale("an invocation input's bytes", func() error { return r.AttributeComponentProofs(changed, nil) })
	fresh := ctReport(t, changed, ctToolchain(ctWheel()))
	before := ctJSON(t, fresh)
	if err := fresh.AttributeComponentContractTests(changed, []verify.ComponentContractTestResult{ctPassing(t, inv, r)}); !errors.Is(err, verify.ErrStaleComponentEvidence) || ctJSON(t, fresh) != before {
		t.Fatalf("a new result with the old invocation snapshot: %v", err)
	}
	ctWrite(t, root, ctProject+"/fixture.json", "{\"name\": \"task\"}\n")
	p.Manifest.Components.TrustedContracts[0].Invocations[0].Args = []string{"-m", "unittest", "verification.tasks.test_contracts.LoadsTests"}
	ctSave(t, p)
	changed = ctInventory(t, p.Dir)
	stale("an invocation's arguments", func() error { return r.AttributeComponentRuntime(changed, nil) })
	// A retained proof that assumed a contract whose declaration changed.
	if err := r.AttributeComponentContractTests(inv, nil); err != nil {
		t.Fatal(err)
	}
	target := ctID("app/tasks/core.py", "load")
	loads := ctDeclaration(t, inv, ctID(ctContracts, "loads"))
	if err := r.AttributeComponentProofs(inv, []verify.ComponentProofResult{{Target: target, Verifier: "nagini", Passed: true,
		Inputs: []project.Component{ctDeclaration(t, inv, target), loads}, Verified: []string{target}, Contracts: []string{target},
		Assumed: []verify.ComponentContractUse{{Contract: "json.loads", Declaration: loads}}}}); err != nil {
		t.Fatal(err)
	}
	p.Manifest.Components.TrustedContracts[0].Invocations[0].Args = []string{"-m", "unittest", "verification.tasks.test_contracts"}
	ctSave(t, p)
	ctWrite(t, root, ctContracts, strings.Replace(ctContractSource, "Requires(len(text) > 0)", "Requires(len(text) > 3)", 1))
	changed = ctInventory(t, p.Dir)
	stale("a retained proof's assumed contract", func() error { return r.AttributeComponentRuntime(changed, nil) })
}

func TestTrustedContractFingerprintsBindContractDependencyAndTestInputs(t *testing.T) {
	root, p := ctFixture(t)
	inv := ctInventory(t, p.Dir)
	base := ctReport(t, inv, ctToolchain(ctWheel())).Fingerprint
	changed := func(name string, fingerprint string) {
		t.Helper()
		if fingerprint == base {
			t.Fatalf("%s left the fingerprint unchanged", name)
		}
	}
	newer := ctWheel()
	newer.Version, newer.Filename = "24.3", "packaging-24.3-py3-none-any.whl"
	changed("a dependency version", ctReport(t, inv, ctToolchain(newer)).Fingerprint)
	rebuilt := ctWheel()
	rebuilt.SHA256 = ctHash("another packaging 24.2 artifact")
	changed("a dependency artifact", ctReport(t, inv, ctToolchain(rebuilt)).Fingerprint)
	ctWrite(t, root, ctContracts, strings.Replace(ctContractSource, "Requires(len(text) > 0)", "Requires(len(text) > 1)", 1))
	changed("contract bytes", ctReport(t, ctInventory(t, p.Dir), ctToolchain(ctWheel())).Fingerprint)
	ctWrite(t, root, ctContracts, ctContractSource)
	if ctReport(t, ctInventory(t, p.Dir), ctToolchain(ctWheel())).Fingerprint != base {
		t.Fatal("restoring the contract didn't restore the fingerprint")
	}
	ctWrite(t, root, ctTests, ctTestSource+"\n# another assertion later\n")
	changed("backing-test source", ctReport(t, ctInventory(t, p.Dir), ctToolchain(ctWheel())).Fingerprint)
	ctWrite(t, root, ctTests, ctTestSource)
	ctWrite(t, root, ctProject+"/fixture.json", "{\"name\": \"other\"}\n")
	changed("a backing test's invocation input", ctReport(t, ctInventory(t, p.Dir), ctToolchain(ctWheel())).Fingerprint)
	ctWrite(t, root, ctProject+"/fixture.json", "{\"name\": \"task\"}\n")

	// Supplied order, nested order and a JSON round trip change nothing.
	inv = ctInventory(t, p.Dir)
	r := ctReport(t, inv, ctToolchain(ctWheel()))
	x := ctPassing(t, inv, r)
	if err := r.AttributeComponentContractTests(inv, []verify.ComponentContractTestResult{x}); err != nil {
		t.Fatal(err)
	}
	reordered := ctReport(t, inv, ctToolchain(ctWheel()))
	y := ctPassing(t, inv, reordered)
	y.Inputs[0], y.Inputs[2] = y.Inputs[2], y.Inputs[0]
	y.FileInputs[0], y.FileInputs[2] = y.FileInputs[2], y.FileInputs[0]
	y.Tests[0], y.Tests[1] = y.Tests[1], y.Tests[0]
	if err := reordered.AttributeComponentContractTests(inv, []verify.ComponentContractTestResult{y}); err != nil {
		t.Fatal(err)
	}
	if reordered.Fingerprint != r.Fingerprint {
		t.Fatal("reordered supplied values changed the fingerprint")
	}
	var decoded verify.Report
	if err := json.Unmarshal([]byte(ctJSON(t, r)), &decoded); err != nil {
		t.Fatal(err)
	}
	if verify.Fingerprint(decoded) != r.Fingerprint {
		t.Fatal("a JSON round trip changed the fingerprint")
	}
	// The supplied outcome is bound too.
	failed := ctReport(t, inv, ctToolchain(ctWheel()))
	z := ctPassing(t, inv, failed)
	z.Tests[1].Outcome = "failed"
	if err := failed.AttributeComponentContractTests(inv, []verify.ComponentContractTestResult{z}); err != nil {
		t.Fatal(err)
	}
	if failed.Fingerprint == r.Fingerprint {
		t.Fatal("a backing test's outcome left the fingerprint unchanged")
	}
}

func TestTrustedContractRetentionRejectsStaleAssociationsAtomically(t *testing.T) {
	root, p := ctFixture(t)
	inv := ctInventory(t, p.Dir)
	r := ctReport(t, inv, ctToolchain(ctWheel()))
	version := ctID(ctTests, "VersionTests.test_orders")
	supported := verify.ComponentContractTestResult{Contract: "packaging.Version", Check: version, Invocation: ctInvocation(t, inv, "packaging.Version"),
		Runner: &verify.ComponentProofTool{Name: "Python unittest", Version: ctRuntime}, Binding: ctContract(t, r, "packaging.Version").Binding,
		Inputs:     []project.Component{ctDeclaration(t, inv, ctID(ctContracts, "version")), ctDeclaration(t, inv, version)},
		FileInputs: []project.ComponentFile{ctFile(t, inv, ctContracts), ctFile(t, inv, ctTests)},
		Outcome:    "passed", Tests: []verify.ComponentRuntimeTestResult{{Check: version, Exercised: true, Outcome: "passed"}},
		Implementation: &verify.ComponentContractImplementation{Kind: "dependency", Callable: "python", Distribution: "packaging", Version: "24.2", Recorded: true,
			File: &project.ComponentFile{Path: "packaging/version.py", SHA256: ctHash("packaging/version.py")}},
		Exercise: []verify.ComponentContractExercise{{Check: version, Calls: 3}}}
	if err := r.AttributeComponentContractTests(inv, []verify.ComponentContractTestResult{supported, ctPassing(t, inv, r)}); err != nil {
		t.Fatal(err)
	}
	if ctContract(t, r, "packaging.Version").Support != "tested" || ctContract(t, r, "json.loads").Support != "tested" {
		t.Fatalf("support: %+v", r.Components.Contracts)
	}
	rejects := func(name string, attribute func() error) {
		t.Helper()
		before, fingerprint := ctJSON(t, r), r.Fingerprint
		if err := attribute(); !errors.Is(err, verify.ErrStaleComponentEvidence) {
			t.Fatalf("%s: retained support survived as %v", name, err)
		}
		if ctJSON(t, r) != before || r.Fingerprint != fingerprint {
			t.Fatalf("%s: a rejected attribution changed the report", name)
		}
	}
	// A newer resolved wheel invalidates support tested against the old one,
	// whichever kind is attributed next.
	newer := ctWheel()
	newer.Version = "24.3"
	r.Toolchain = ctToolchain(newer)
	rejects("proofs after a dependency change", func() error { return r.AttributeComponentProofs(inv, nil) })
	rejects("runtime after a dependency change", func() error { return r.AttributeComponentRuntime(inv, nil) })
	r.Toolchain = ctToolchain(ctWheel())
	// So does a changed contract or backing test, in a re-resolved inventory.
	ctWrite(t, root, ctContracts, strings.Replace(ctContractSource, "Requires(len(text) > 0)\n    ...\n\ndef save", "Requires(len(text) > 2)\n    ...\n\ndef save", 1))
	changed := ctInventory(t, p.Dir)
	rejects("conformance after a contract change", func() error { return r.AttributeComponentConformance(changed, nil) })
	ctWrite(t, root, ctContracts, ctContractSource)
	ctWrite(t, root, ctTests, ctTestSource+"\n# edited\n")
	changed = ctInventory(t, p.Dir)
	rejects("assumptions after a test change", func() error { return r.AttributeComponentAssumptions(changed, nil) })
	ctWrite(t, root, ctTests, ctTestSource)
	// A changed operation does too, even with the same contract declaration.
	p.Manifest.Components.TrustedContracts[1].Operation.Symbol = "parse"
	ctSave(t, p)
	changed = ctInventory(t, p.Dir)
	rejects("proofs after an operation change", func() error { return r.AttributeComponentProofs(changed, nil) })
	p.Manifest.Components.TrustedContracts[1].Operation.Symbol = "Version"
	// And so does a test that no longer backs its contract.
	tests, invocations := p.Manifest.Components.TrustedContracts[0].Tests, p.Manifest.Components.TrustedContracts[0].Invocations
	p.Manifest.Components.TrustedContracts[0].Tests, p.Manifest.Components.TrustedContracts[0].Invocations = tests[:1], invocations[:1]
	ctSave(t, p)
	changed = ctInventory(t, p.Dir)
	rejects("proofs after a backing test is dropped", func() error { return r.AttributeComponentProofs(changed, nil) })
	p.Manifest.Components.TrustedContracts[0].Tests, p.Manifest.Components.TrustedContracts[0].Invocations = tests, invocations
	ctSave(t, p)
	// Clearing the support recovers a report whose support alone went stale.
	// Evidence of another kind that rests on the same changed input, such as
	// a proof that assumed the contract, goes stale with it, and then only a
	// new report records the changed inputs.
	if err := r.AttributeComponentContractTests(inv, nil); err != nil {
		t.Fatal(err)
	}
	r.Toolchain = ctToolchain(newer)
	if err := r.AttributeComponentProofs(inv, nil); err != nil {
		t.Fatalf("with no retained support, new identities attribute: %v", err)
	}
	if ctContract(t, r, "packaging.Version").Support != "untested" || ctContract(t, r, "packaging.Version").Binding.Wheel.Version != "24.3" {
		t.Fatalf("after clearing: %+v", ctContract(t, r, "packaging.Version"))
	}
}

func TestTrustedContractUseComesOnlyFromProofAttempts(t *testing.T) {
	_, p := ctFixture(t)
	inv := ctInventory(t, p.Dir)
	target := ctID("app/tasks/core.py", "load")
	loads := ctDeclaration(t, inv, ctID(ctContracts, "loads"))
	proof := func() verify.ComponentProofResult {
		return verify.ComponentProofResult{Target: target, Verifier: "nagini", Passed: true,
			Inputs: []project.Component{ctDeclaration(t, inv, target), loads}, Verified: []string{target}, Contracts: []string{target},
			Assumed: []verify.ComponentContractUse{{Contract: "json.loads", Declaration: loads}}}
	}
	r := ctReport(t, inv, ctToolchain(ctWheel()))
	if err := r.AttributeComponentProofs(inv, []verify.ComponentProofResult{proof()}); err != nil {
		t.Fatal(err)
	}
	if r.Components.Proofs[0].Status != "proved" {
		t.Fatalf("a complete account of the boundary: %+v", r.Components.Proofs[0])
	}
	c := ctContract(t, r, "json.loads")
	if c.Use != "assumed" || len(c.Relying) != 1 || c.Relying[0] != target || c.Support != "untested" {
		t.Fatalf("assumed contract: %+v", c)
	}
	for _, other := range []string{"packaging.Version", "store.save"} {
		if ctContract(t, r, other).Use != "declared" {
			t.Fatalf("%s became used without a proof using it", other)
		}
	}
	if d := r.Components.ContractDiagnostics; len(d) != 1 || d[0].Kind != verify.UntestedAssumption || d[0].Contract != "json.loads" || d[0].Target != target {
		t.Fatalf("an untested assumption must be diagnosed: %+v", d)
	}
	// Support attributed later clears the diagnostic and leaves the proof as it was.
	if err := r.AttributeComponentContractTests(inv, []verify.ComponentContractTestResult{ctPassing(t, inv, r)}); err != nil {
		t.Fatalf("support must attribute beside a retained proof: %v", err)
	}
	if len(r.Components.ContractDiagnostics) != 0 || r.Components.Proofs[0].Status != "proved" || ctContract(t, r, "json.loads").Support != "tested" {
		t.Fatalf("after support: %+v %+v", r.Components.ContractDiagnostics, r.Components.Proofs[0])
	}
	// An unaccounted boundary leaves the target unproved and is diagnosed.
	missing := proof()
	missing.Unaccounted = []verify.ComponentUnaccounted{{Kind: verify.MissingContract, Operation: &project.ResolvedComponentOperation{Kind: "standard_library", Module: "json", Symbol: "dumps"}, Detail: "no trusted contract covers it"},
		{Kind: verify.UnaccountedAssumption, Detail: "builtins.len"}}
	if err := r.AttributeComponentProofs(inv, []verify.ComponentProofResult{missing}); err != nil {
		t.Fatal(err)
	}
	if r.Components.Proofs[0].Status != "unchecked" {
		t.Fatal("a proof with an unaccounted boundary was proved")
	}
	kinds := map[string]string{}
	for _, d := range r.Components.ContractDiagnostics {
		kinds[d.Kind] = d.Target + " " + d.Detail
	}
	if kinds[verify.MissingContract] != target+" json.dumps: no trusted contract covers it" || kinds[verify.UnaccountedAssumption] != target+" builtins.len" {
		t.Fatalf("unaccounted diagnostics: %+v", r.Components.ContractDiagnostics)
	}
	for _, c := range []struct {
		name   string
		mutate func(*verify.ComponentProofResult)
		stale  bool
	}{
		{"unknown contract", func(x *verify.ComponentProofResult) { x.Assumed[0].Contract = "json.dumps" }, false},
		{"duplicate contract", func(x *verify.ComponentProofResult) { x.Assumed = append(x.Assumed, x.Assumed[0]) }, false},
		{"stale contract", func(x *verify.ComponentProofResult) {
			x.Assumed[0].Declaration.ContractSHA256 = ctHash("Requires(True)")
		}, true},
		{"another declaration", func(x *verify.ComponentProofResult) {
			x.Assumed[0].Declaration = ctDeclaration(t, inv, ctID(ctContracts, "version"))
		}, true},
		{"invalid unaccounted kind", func(x *verify.ComponentProofResult) {
			x.Unaccounted = []verify.ComponentUnaccounted{{Kind: "stale", Detail: "json.dumps"}}
		}, false},
		{"blank unaccounted operation", func(x *verify.ComponentProofResult) {
			x.Unaccounted = []verify.ComponentUnaccounted{{Kind: verify.MissingContract}}
		}, false},
		{"missing a declared contract", func(x *verify.ComponentProofResult) {
			x.Unaccounted = []verify.ComponentUnaccounted{{Kind: verify.MissingContract, Operation: &project.ResolvedComponentOperation{Kind: "standard_library", Module: "json", Symbol: "loads"}}}
		}, false},
		{"unaccounted on a declared contract's operation", func(x *verify.ComponentProofResult) {
			x.Unaccounted = []verify.ComponentUnaccounted{{Kind: verify.UnaccountedAssumption, Detail: "assumed outright",
				Operation: &project.ResolvedComponentOperation{Kind: "application", Declaration: ctID("app/storage/store.py", "save")}}}
		}, false},
		{"invalid unaccounted operation", func(x *verify.ComponentProofResult) {
			x.Unaccounted = []verify.ComponentUnaccounted{{Kind: verify.MissingContract, Operation: &project.ResolvedComponentOperation{Kind: "standard_library", Module: "json/loads"}}}
		}, false},
		{"blank unaccounted assumption", func(x *verify.ComponentProofResult) {
			x.Unaccounted = []verify.ComponentUnaccounted{{Kind: verify.UnaccountedAssumption}}
		}, false},
		{"assumed without its declaration among the inputs", func(x *verify.ComponentProofResult) { x.Inputs = x.Inputs[:1] }, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := ctReport(t, inv, ctToolchain(ctWheel()))
			before := ctJSON(t, r)
			x := proof()
			c.mutate(&x)
			err := r.AttributeComponentProofs(inv, []verify.ComponentProofResult{x})
			if err == nil || errors.Is(err, verify.ErrStaleComponentEvidence) != c.stale {
				t.Fatalf("error = %v, stale want %t", err, c.stale)
			}
			if ctJSON(t, r) != before {
				t.Fatal("a rejected proof changed the report")
			}
		})
	}
}

func TestTrustedContractEvidenceLeavesOldReportsUnchanged(t *testing.T) {
	_, p := ctFixture(t)
	p.Manifest.Components.TrustedContracts = nil
	ctSave(t, p)
	inv := ctInventory(t, p.Dir)
	target := ctID("app/tasks/core.py", "load")
	r := &verify.Report{Project: "tasks", Bounds: map[string]string{"Limit": "2"}}
	if err := r.AttributeComponentProofs(inv, []verify.ComponentProofResult{{Target: target, Verifier: "nagini", Passed: true,
		Inputs: []project.Component{ctDeclaration(t, inv, target)}, Verified: []string{target}, Contracts: []string{target}}}); err != nil {
		t.Fatal(err)
	}
	raw := ctJSON(t, r)
	components, results := ctKeys(t, r)
	for _, key := range []string{"contracts", "contract_tests", "contract_diagnostics"} {
		if components[key] {
			t.Fatalf("a report without trusted contracts names %s: %s", key, raw)
		}
	}
	if results["assumed"] || results["unaccounted"] || strings.Contains(raw, "trusted_contracts") {
		t.Fatalf("an ordinary proof names a trusted boundary: %s", raw)
	}
	if r.Components.Proofs[0].Status != "proved" {
		t.Fatal("an ordinary proof lost its coverage")
	}
	// Supplied backing tests need declared contracts.
	if err := r.AttributeComponentContractTests(inv, []verify.ComponentContractTestResult{{Contract: "json.loads"}}); err == nil {
		t.Fatal("backing tests attributed to an inventory without contracts")
	}
	if err := r.AttributeComponentContractTests(inv, nil); err != nil {
		t.Fatalf("clearing an empty kind: %v", err)
	}
	if ctJSON(t, r) != raw {
		t.Fatal("clearing nothing changed the report")
	}
}

func TestTrustedContractEvidenceOwnsItsValues(t *testing.T) {
	_, p := ctFixture(t)
	inv := ctInventory(t, p.Dir)
	r := ctReport(t, inv, ctToolchain(ctWheel()))
	x := ctPassing(t, inv, r)
	target := ctID("app/tasks/core.py", "load")
	loads := ctDeclaration(t, inv, ctID(ctContracts, "loads"))
	proof := verify.ComponentProofResult{Target: target, Verifier: "nagini", Passed: true,
		Inputs: []project.Component{ctDeclaration(t, inv, target), loads}, Verified: []string{target}, Contracts: []string{target},
		Assumed:     []verify.ComponentContractUse{{Contract: "json.loads", Declaration: loads}},
		Unaccounted: []verify.ComponentUnaccounted{{Kind: verify.MissingContract, Operation: &project.ResolvedComponentOperation{Kind: "standard_library", Module: "json", Symbol: "dumps"}}}}
	if err := r.AttributeComponentProofs(inv, []verify.ComponentProofResult{proof}); err != nil {
		t.Fatal(err)
	}
	if err := r.AttributeComponentContractTests(inv, []verify.ComponentContractTestResult{x}); err != nil {
		t.Fatal(err)
	}
	want, fingerprint := ctJSON(t, r), r.Fingerprint
	provenance257Mutate(reflect.ValueOf(&x))
	provenance257Mutate(reflect.ValueOf(&proof))
	provenance257Mutate(reflect.ValueOf(inv))
	if ctJSON(t, r) != want || verify.Fingerprint(*r) != fingerprint {
		t.Fatal("attribution kept the caller's slices, maps or pointers")
	}
}

func TestGateRefusesABackingTestWithNoInvocationBeforeTools(t *testing.T) {
	_, p := ctFixture(t)
	// Leave json.loads's second backing test with nothing to run it.
	loads := &p.Manifest.Components.TrustedContracts[0]
	loads.Invocations = loads.Invocations[:1]
	p.Manifest.Components.Checks = nil
	ctSave(t, p)
	if err := verify.Check(p.Dir); err == nil || !strings.Contains(err.Error(), `trusted contract "json.loads"'s backing test`) {
		t.Fatalf("the gate admitted a backing test nothing runs: %v", err)
	}
}
