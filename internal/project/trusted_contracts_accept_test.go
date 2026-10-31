package project_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
)

// Trusted contracts (#343, #294's step 3) are inventory: each binds a real
// operation to a contract declaration and backing tests, with exact
// snapshots, and none of it is evidence that a proof used it or a test ran.

const tcProject = "verification/tasks"

const tcContracts = `from nagini_contracts.contracts import *

def loads(text: str) -> object:
    Requires(len(text) > 0)
    ...

def version(text: str) -> object:
    Requires(len(text) > 0)
    ...

def save(task: object) -> object:
    Ensures(Result() is task)
    ...

def unannotated(text: str) -> object:
    ...
`

func tcFixture(t *testing.T) (string, *project.Project) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "application")
	for name, body := range map[string]string{
		"app/__init__.py":                         "",
		"app/tasks/__init__.py":                   "",
		"app/tasks/core.py":                       "import json\nfrom app.storage.store import save\n\ndef load(text):\n    return json.loads(text)\n\ndef finish(task):\n    return save(task)\n",
		"app/storage/__init__.py":                 "",
		"app/storage/store.py":                    "def save(task):\n    return task\n",
		"app/tasks/test_core.py":                  "import unittest\nclass CoreTests(unittest.TestCase):\n    def test_load(self):\n        pass\n",
		tcProject + "/contracts.py":               tcContracts,
		tcProject + "/test_contracts.py":          "import unittest\nclass LoadsTests(unittest.TestCase):\n    def test_parses(self):\n        pass\n    def test_rejects_empty(self):\n        pass\nclass VersionTests(unittest.TestCase):\n    def test_orders(self):\n        pass\nclass SaveTests(unittest.TestCase):\n    def test_returns(self):\n        pass\n",
		tcProject + "/fixture.json":               "{\"name\": \"task\"}\n",
		tcProject + "/driver.py":                  "from app.tasks.core import finish\ndef drive():\n    return finish({})\n",
		tcProject + "/.invariant/specs/Tasks.tla": "---- MODULE Tasks ----\nEXTENDS Naturals\nCONSTANT Limit\nVARIABLE n\nInit == n = 0\nNext == n' = n\nSpec == Init /\\ [][Next]_n\nSafe == n >= 0\n====\n",
	} {
		py304Write(t, root, name, body)
	}
	tests := tcProject + "/test_contracts.py"
	p := &project.Project{Dir: filepath.Join(root, filepath.FromSlash(tcProject)), Manifest: project.Manifest{
		Name: "tasks", Language: "python", Code: ".", Module: ".invariant/specs/Tasks.tla", Conformance: "driver.py",
		Existing: []string{"app/tasks"},
		Components: &project.ComponentsConfig{Scope: []string{"app/tasks"}, TrustedContracts: []project.ComponentContract{
			{
				ID:        "json.loads",
				Operation: project.ComponentOperation{Kind: "standard_library", Module: "json", Symbol: "loads"},
				Contract:  py304Ref(tcProject+"/contracts.py", "loads"),
				Tests:     []project.ComponentRef{py304Ref(tests, "LoadsTests.test_rejects_empty"), py304Ref(tests, "LoadsTests.test_parses")},
				Invocations: []project.ComponentInvocation{{ID: "loads", Kind: "test", Check: py304Ref(tests, "LoadsTests.test_parses"),
					Command: "python", Args: []string{"-m", "unittest", "verification.tasks.test_contracts"}, WorkingDir: ".", Inputs: []string{tcProject + "/fixture.json"}}},
			},
			{
				ID:        "packaging.Version",
				Operation: project.ComponentOperation{Kind: "dependency", Distribution: "Packaging", Module: "packaging.version", Symbol: "Version"},
				Contract:  py304Ref(tcProject+"/contracts.py", "version"),
				Tests:     []project.ComponentRef{py304Ref(tests, "VersionTests.test_orders")},
			},
			{
				ID:        "store.save",
				Operation: project.ComponentOperation{Kind: "application", Source: "app/storage/store.py", Symbol: "save"},
				Contract:  py304Ref(tcProject+"/contracts.py", "save"),
				Tests:     []project.ComponentRef{py304Ref(tests, "SaveTests.test_returns")},
			},
		}},
	}, Lock: project.Lock{Bounds: map[string]string{"Limit": "2"}, Statements: []project.Statement{{Name: "Spec", Kind: project.Spec}, {Name: "Safe", Kind: project.Invariant}}}}
	tcSave(t, p)
	return root, p
}

func tcSave(t *testing.T, p *project.Project) {
	t.Helper()
	for name, value := range map[string]any{".invariant/invariant.json": p.Manifest, ".invariant/ratified.lock": p.Lock} {
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		py304Write(t, p.Dir, name, string(b))
	}
}

func tcResolve(t *testing.T, dir string) *project.ComponentInventory {
	t.Helper()
	p, err := project.Load(dir)
	if err != nil {
		t.Fatalf("a manifest with trusted contracts must load: %v", err)
	}
	inv, err := project.ResolveComponents(p)
	if err != nil {
		t.Fatal(err)
	}
	return inv
}

func tcRead(t *testing.T, root, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTrustedContractsResolveOperationsContractsAndTests(t *testing.T) {
	root, p := tcFixture(t)
	inv := tcResolve(t, p.Dir)
	contracts := tcProject + "/contracts.py"
	tests := tcProject + "/test_contracts.py"
	want := []project.ResolvedComponentContract{
		{
			ID:        "json.loads",
			Operation: project.ResolvedComponentOperation{Kind: "standard_library", Module: "json", Symbol: "loads"},
			Contract:  py304ID(contracts, "loads"),
			Tests:     []string{py304ID(tests, "LoadsTests.test_parses"), py304ID(tests, "LoadsTests.test_rejects_empty")},
			Invocations: []project.ResolvedComponentInvocation{{ID: "loads", Kind: "test", Check: py304ID(tests, "LoadsTests.test_parses"),
				Command: "python", Args: []string{"-m", "unittest", "verification.tasks.test_contracts"}, WorkingDir: ".",
				Inputs: []project.ComponentFile{{Path: tcProject + "/fixture.json", SHA256: py304Hash(tcRead(t, root, tcProject+"/fixture.json"))}}}},
		},
		{
			ID:        "packaging.Version",
			Operation: project.ResolvedComponentOperation{Kind: "dependency", Distribution: "packaging", Module: "packaging.version", Symbol: "Version"},
			Contract:  py304ID(contracts, "version"),
			Tests:     []string{py304ID(tests, "VersionTests.test_orders")},
		},
		{
			ID:        "store.save",
			Operation: project.ResolvedComponentOperation{Kind: "application", Declaration: py304ID("app/storage/store.py", "save")},
			Contract:  py304ID(contracts, "save"),
			Tests:     []string{py304ID(tests, "SaveTests.test_returns")},
		},
	}
	if !reflect.DeepEqual(inv.TrustedContracts, want) {
		t.Fatalf("trusted contracts =\n%+v\nwant\n%+v", inv.TrustedContracts, want)
	}
	// Production stays the scope alone: the contract file, tests and the
	// application operation outside scope are provenance, never coverage.
	for _, c := range inv.Components {
		if !strings.HasPrefix(c.Source, "app/tasks/") {
			t.Fatalf("a trusted contract added production coverage: %+v", c)
		}
	}
	declarations := map[string]project.Component{}
	for _, c := range inv.Provenance.Declarations {
		declarations[c.ID] = c
	}
	contractsHash := py304Hash(tcContracts)
	for _, id := range []string{py304ID(contracts, "loads"), py304ID(contracts, "version"), py304ID(contracts, "save"),
		py304ID(tests, "LoadsTests.test_parses"), py304ID(tests, "SaveTests.test_returns"), py304ID("app/storage/store.py", "save")} {
		if _, ok := declarations[id]; !ok {
			t.Fatalf("provenance lacks %s: %+v", id, inv.Provenance.Declarations)
		}
	}
	if c := declarations[py304ID(contracts, "loads")]; c.SourceSHA256 != contractsHash || c.ContractSHA256 != py304Hash("Requires(len(text) > 0)") {
		t.Fatalf("contract declaration snapshot = %+v", c)
	}
	files := map[string]string{}
	for _, f := range inv.Provenance.Files {
		files[f.Path] = f.SHA256
	}
	for _, name := range []string{contracts, tests, "app/storage/store.py", tcProject + "/fixture.json"} {
		if files[name] != py304Hash(tcRead(t, root, name)) {
			t.Fatalf("provenance file %s = %q", name, files[name])
		}
	}
	b, err := json.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	for _, word := range []string{`"trusted_contracts"`, `"standard_library"`, `"distribution":"packaging"`} {
		if !strings.Contains(string(b), word) {
			t.Fatalf("inventory JSON lacks %s: %s", word, b)
		}
	}
	for _, word := range []string{"tested", "proved", "assumed", "used"} {
		if strings.Contains(strings.ToLower(string(b)), `"`+word+`"`) {
			t.Fatalf("a declaration carried evidence %q: %s", word, b)
		}
	}
}

func TestTrustedContractIdentitiesAreStableAndTrackContractBytes(t *testing.T) {
	root, p := tcFixture(t)
	first, err := json.Marshal(tcResolve(t, p.Dir))
	if err != nil {
		t.Fatal(err)
	}
	// Relocation keeps every identity and snapshot.
	moved := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.CopyFS(moved, os.DirFS(root)); err != nil {
		t.Fatal(err)
	}
	again, err := json.Marshal(tcResolve(t, filepath.Join(moved, filepath.FromSlash(tcProject))))
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(first) {
		t.Fatalf("relocation changed trusted contract identities:\n%s\n%s", first, again)
	}
	// Reordering the manifest's contracts and tests changes nothing.
	cfg := p.Manifest.Components
	cfg.TrustedContracts[0], cfg.TrustedContracts[2] = cfg.TrustedContracts[2], cfg.TrustedContracts[0]
	tcSave(t, p)
	reordered, err := json.Marshal(tcResolve(t, p.Dir))
	if err != nil {
		t.Fatal(err)
	}
	if string(reordered) != string(first) {
		t.Fatal("manifest order changed the canonical inventory")
	}
	contract := func(inv *project.ComponentInventory) project.Component {
		for _, c := range inv.Provenance.Declarations {
			if c.ID == py304ID(tcProject+"/contracts.py", "loads") {
				return c
			}
		}
		t.Fatal("missing contract declaration")
		return project.Component{}
	}
	before := contract(tcResolve(t, p.Dir))
	// A changed contract keeps its identity and changes its contract bytes.
	py304Write(t, root, tcProject+"/contracts.py", strings.Replace(tcContracts, "Requires(len(text) > 0)\n    ...\n\ndef version", "Requires(len(text) > 1)\n    ...\n\ndef version", 1))
	changed := contract(tcResolve(t, p.Dir))
	if changed.ID != before.ID || changed.ContractSHA256 == before.ContractSHA256 || changed.SourceSHA256 == before.SourceSHA256 {
		t.Fatalf("changed contract bytes: before %+v, after %+v", before, changed)
	}
	// An edit outside the contract's calls changes the file, not the contract.
	py304Write(t, root, tcProject+"/contracts.py", tcContracts+"\n# a trailing comment\n")
	edited := contract(tcResolve(t, p.Dir))
	if edited.ContractSHA256 != before.ContractSHA256 || edited.SourceSHA256 == before.SourceSHA256 {
		t.Fatalf("file edit: before %+v, after %+v", before, edited)
	}
}

func TestTrustedContractsRejectInvalidAndAmbiguousBindings(t *testing.T) {
	tests := tcProject + "/test_contracts.py"
	for _, c := range []struct {
		name   string
		mutate func(cfg *project.ComponentsConfig)
		want   string
	}{
		{"blank id", func(cfg *project.ComponentsConfig) { cfg.TrustedContracts[0].ID = "" }, "must be nonblank"},
		{"spaced id", func(cfg *project.ComponentsConfig) { cfg.TrustedContracts[0].ID = "json loads" }, "without spaces"},
		{"duplicate id", func(cfg *project.ComponentsConfig) { cfg.TrustedContracts[1].ID = "json.loads" }, "duplicate trusted contract"},
		{"unknown kind", func(cfg *project.ComponentsConfig) { cfg.TrustedContracts[0].Operation.Kind = "network" }, "unknown operation kind"},
		{"standard library distribution", func(cfg *project.ComponentsConfig) { cfg.TrustedContracts[0].Operation.Distribution = "json" }, "belongs to the Python runtime"},
		{"standard library source", func(cfg *project.ComponentsConfig) {
			cfg.TrustedContracts[0].Operation.Source = "app/storage/store.py"
		}, "not a source"},
		{"undotted module", func(cfg *project.ComponentsConfig) { cfg.TrustedContracts[0].Operation.Module = "json/decoder" }, "dotted Python module"},
		{"missing symbol", func(cfg *project.ComponentsConfig) { cfg.TrustedContracts[0].Operation.Symbol = "" }, "dotted Python module"},
		{"bad distribution", func(cfg *project.ComponentsConfig) { cfg.TrustedContracts[1].Operation.Distribution = "-packaging" }, "distribution name"},
		{"missing distribution", func(cfg *project.ComponentsConfig) { cfg.TrustedContracts[1].Operation.Distribution = "" }, "distribution name"},
		{"application module", func(cfg *project.ComponentsConfig) { cfg.TrustedContracts[2].Operation.Module = "app.storage.store" }, "not a module"},
		{"application missing", func(cfg *project.ComponentsConfig) { cfg.TrustedContracts[2].Operation.Symbol = "absent" }, "exactly one declaration"},
		{"application test", func(cfg *project.ComponentsConfig) {
			cfg.TrustedContracts[2].Operation.Source, cfg.TrustedContracts[2].Operation.Symbol = "app/tasks/test_core.py", "CoreTests.test_load"
		}, "outside a test file"},
		{"same operation", func(cfg *project.ComponentsConfig) {
			cfg.TrustedContracts[1].Operation = project.ComponentOperation{Kind: "standard_library", Module: "json", Symbol: "loads"}
		}, "already has trusted contract"},
		{"same distribution normalized", func(cfg *project.ComponentsConfig) {
			cfg.TrustedContracts[0].Operation = project.ComponentOperation{Kind: "dependency", Distribution: "PACKAGING", Module: "packaging.version", Symbol: "Version"}
		}, "already has trusted contract"},
		{"same dotted name split differently", func(cfg *project.ComponentsConfig) {
			cfg.TrustedContracts[0].Operation = project.ComponentOperation{Kind: "standard_library", Module: "os.path", Symbol: "join"}
			cfg.TrustedContracts[1].Operation = project.ComponentOperation{Kind: "standard_library", Module: "os", Symbol: "path.join"}
		}, "already has trusted contract"},
		{"same dotted name in another kind", func(cfg *project.ComponentsConfig) {
			cfg.TrustedContracts[1].Operation = project.ComponentOperation{Kind: "dependency", Distribution: "packaging", Module: "json", Symbol: "loads"}
		}, "already has trusted contract"},
		{"operation is another contract's abstraction", func(cfg *project.ComponentsConfig) {
			cfg.TrustedContracts[1].Operation = project.ComponentOperation{Kind: "application", Source: tcProject + "/contracts.py", Symbol: "loads"}
		}, `is trusted contract "json.loads"'s abstraction`},
		{"abstraction is another contract's operation", func(cfg *project.ComponentsConfig) {
			cfg.TrustedContracts[0].Operation = project.ComponentOperation{Kind: "application", Source: tcProject + "/contracts.py", Symbol: "version"}
		}, `is trusted contract "json.loads"'s operation`},
		{"misspelled contract source", func(cfg *project.ComponentsConfig) {
			cfg.TrustedContracts[0].Contract.Source = "Verification/tasks/contracts.py"
		}, ""},
		{"misspelled scoped contract", func(cfg *project.ComponentsConfig) {
			cfg.TrustedContracts[0].Contract = py304Ref("App/Tasks/contracted.py", "load")
		}, ""},
		{"misspelled application operation", func(cfg *project.ComponentsConfig) {
			cfg.TrustedContracts[2].Operation.Source = "APP/storage/store.py"
		}, ""},
		{"missing contract", func(cfg *project.ComponentsConfig) { cfg.TrustedContracts[0].Contract.Symbol = "absent" }, "exactly one declaration"},
		{"unannotated contract", func(cfg *project.ComponentsConfig) { cfg.TrustedContracts[0].Contract.Symbol = "unannotated" }, "no supported inline Nagini contract"},
		{"contract in a test file", func(cfg *project.ComponentsConfig) {
			cfg.TrustedContracts[0].Contract = py304Ref(tests, "LoadsTests.test_parses")
		}, "outside a test file"},
		{"contract in scope", func(cfg *project.ComponentsConfig) {
			cfg.TrustedContracts[0].Contract = py304Ref("app/tasks/contracted.py", "load")
		}, "production code in scope"},
		{"contract is the operation", func(cfg *project.ComponentsConfig) {
			cfg.TrustedContracts[2].Operation = project.ComponentOperation{Kind: "application", Source: tcProject + "/contracts.py", Symbol: "save"}
		}, "is the operation it describes"},
		{"shared contract", func(cfg *project.ComponentsConfig) { cfg.TrustedContracts[1].Contract.Symbol = "loads" }, "already holds trusted contract"},
		{"no tests", func(cfg *project.ComponentsConfig) { cfg.TrustedContracts[1].Tests = nil }, "at least one backing test"},
		{"test outside a test file", func(cfg *project.ComponentsConfig) {
			cfg.TrustedContracts[1].Tests = []project.ComponentRef{py304Ref("app/storage/store.py", "save")}
		}, "must name a test source file"},
		{"duplicate test", func(cfg *project.ComponentsConfig) {
			cfg.TrustedContracts[1].Tests = append(cfg.TrustedContracts[1].Tests, cfg.TrustedContracts[1].Tests[0])
		}, "duplicate test selection"},
		{"conformance invocation", func(cfg *project.ComponentsConfig) { cfg.TrustedContracts[0].Invocations[0].Kind = "conformance" }, "kind must be test"},
		{"proof invocation", func(cfg *project.ComponentsConfig) { cfg.TrustedContracts[0].Invocations[0].Kind = "proof" }, "kind must be test"},
		{"unselected invocation", func(cfg *project.ComponentsConfig) {
			cfg.TrustedContracts[0].Invocations[0].Check = py304Ref(tests, "SaveTests.test_returns")
		}, "is not a selected test check"},
		{"missing working directory", func(cfg *project.ComponentsConfig) { cfg.TrustedContracts[0].Invocations[0].WorkingDir = "absent" }, "working directory"},
		{"missing input", func(cfg *project.ComponentsConfig) {
			cfg.TrustedContracts[0].Invocations[0].Inputs = []string{tcProject + "/absent.json"}
		}, "absent.json"},
		{"escaping contract", func(cfg *project.ComponentsConfig) { cfg.TrustedContracts[0].Contract.Source = "../contracts.py" }, "clean contained slash path"},
		{"excluded contract", func(cfg *project.ComponentsConfig) { cfg.TrustedContracts[0].Contract.Source = ".venv/contracts.py" }, "excluded directory"},
		{"escaping test", func(cfg *project.ComponentsConfig) {
			cfg.TrustedContracts[1].Tests = []project.ComponentRef{py304Ref("../test_contracts.py", "VersionTests.test_orders")}
		}, "clean contained slash path"},
	} {
		t.Run(c.name, func(t *testing.T) {
			root, p := tcFixture(t)
			py304Write(t, root, "app/tasks/contracted.py", "def load(text):\n    Requires(len(text) > 0)\n    return text\n")
			c.mutate(p.Manifest.Components)
			tcSave(t, p)
			if _, err := project.Load(p.Dir); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Load error = %v; want one containing %q", err, c.want)
			}
		})
	}
}

func TestTrustedContractsRejectLinkedContractSources(t *testing.T) {
	root, p := tcFixture(t)
	real := filepath.Join(root, filepath.FromSlash(tcProject), "contracts.py")
	link := filepath.Join(root, filepath.FromSlash(tcProject), "linked.py")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	p.Manifest.Components.TrustedContracts[0].Contract.Source = tcProject + "/linked.py"
	tcSave(t, p)
	if _, err := project.Load(p.Dir); err == nil || !strings.Contains(err.Error(), "symbolic links") {
		t.Fatalf("Load error = %v; want a refused symlink", err)
	}
}

func TestTrustedContractsNeedAnExistingPythonApplication(t *testing.T) {
	_, p := tcFixture(t)
	contracts := p.Manifest.Components.TrustedContracts
	// A Go project resolves its components and still can't declare one.
	dir := acceptFixture(t)
	goProject, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	goProject.Manifest.Components.TrustedContracts = contracts
	if _, err := project.ResolveComponents(goProject); err == nil || !strings.Contains(err.Error(), "need an existing Python application") {
		t.Fatalf("Go project with trusted contracts: %v", err)
	}
	// Python components without existing code can't either.
	py := py304Fixture(t)
	py.Manifest.Components.TrustedContracts = contracts
	if _, err := project.ResolveComponents(py); err == nil || !strings.Contains(err.Error(), "need an existing Python application") {
		t.Fatalf("Python project without existing code: %v", err)
	}
}

func TestTrustedContractsLeaveOldManifestsUnchanged(t *testing.T) {
	_, p := tcFixture(t)
	p.Manifest.Components.TrustedContracts = nil
	tcSave(t, p)
	raw, err := os.ReadFile(filepath.Join(p.Dir, ".invariant", "invariant.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "trusted_contracts") {
		t.Fatalf("a manifest without contracts names them: %s", raw)
	}
	inv := tcResolve(t, p.Dir)
	b, err := json.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "trusted_contracts") || inv.TrustedContracts != nil {
		t.Fatalf("an inventory without contracts names them: %s", b)
	}
}

func TestTrustedContractsOwnTheirResolvedValues(t *testing.T) {
	_, p := tcFixture(t)
	loaded, err := project.Load(p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	inv, err := project.ResolveComponents(loaded)
	if err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	cdMutate(reflect.ValueOf(loaded.Manifest.Components))
	got, err := json.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatal("a resolved trusted contract retained the manifest's slices or maps")
	}
}
