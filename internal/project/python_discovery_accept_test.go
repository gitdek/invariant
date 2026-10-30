package project_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
)

// Python identity contract: python:<module>#<symbol>@<source>. The module
// is the project-relative source without .py, with / replaced by ., including
// __init__. Functions nested in functions add .<locals>.; classes add a dot.
// In each lexical namespace, repeated declaration names (including containing
// classes) use [1], [2], ... in source order; unique names have no ordinal.
// Descendants inherit their parent's ordinal. Only named def/async def nodes
// are components, including declarations inside control-flow suites. Decorators
// never change identity and are never evaluated. Classes, aliases and lambdas
// are not selectable function declarations. Test files are test_*.py or *_test.py.
//
// Contract syntax is deliberately a syntactic snapshot, not name resolution or
// proof: direct standalone calls to unqualified Requires, Ensures, Exsures or
// Decreases in the function's own immediate suite. Concatenate their original
// expression spans (first name byte through closing parenthesis) in source
// order, without separators. Exclude surrounding indentation/newlines/comments;
// retain everything inside each span, including multiline indentation, internal
// comments, original encoding and CRLF. No matching expression means empty hash.
// Calls in nested suites/declarations and decorators are not this contract.

func py304Write(t *testing.T, dir, name, body string) {
	t.Helper()
	file := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func py304Hash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func py304ID(source, symbol string) string {
	return "python:" + strings.ReplaceAll(strings.TrimSuffix(source, ".py"), "/", ".") + "#" + symbol + "@" + source
}

func py304Ref(source, symbol string) project.ComponentRef {
	return project.ComponentRef{Source: source, Symbol: symbol}
}

const py304Core = `# +nagini
from nagini_contracts.contracts import *
import dependency_that_must_never_be_imported
raise RuntimeError("discovery must never execute this module")
# def comment_ghost(): pass
fake = """
class Fake:
    def string_ghost(self): pass
"""
class Alpha:
    @unknown_decorator("not executed")
    def step(self, n: int) -> int:
        """A real method, with a docstring before its contract."""
        Requires(n >= 0)
        Ensures(Result() >= 0)
        return n
class Beta:
    async def step(self, n):
        return n
@unknown_decorator
async def fetch(n):
    async def inner():
        return n
    return n

def outer():
    def inner():
        return 1
    class Local:
        def method(self): return 2
    return inner
class Outer:
    class Inner:
        @staticmethod
        def step(): return 3
class Properties:
    @property
    def value(self): return 1
    @value.setter
    def value(self, new): pass
if enabled:
    def conditional(): return 1
else:
    def conditional(): return 2
lambda_value = lambda: 3
alias = outer
`

func py304Fixture(t *testing.T) *project.Project {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"core/ops.py":              py304Core,
		"core/data.py":             "# a function-free file\nVALUE = 2\n",
		"core/__init__.py":         "def init(): pass\n",
		"other/ops.py":             "class Alpha:\n    def step(self, n): return n\n",
		"core/test_ignored.py":     "this is invalid Python and is not selected\n",
		"core/ignored_test.py":     "this is also invalid Python\n",
		"core/sub/ignored.py":      "this is invalid and below the scoped directory\n",
		"core/ignored.go":          "not Python\n",
		"tests/test_ops.py":        "class TestAlpha:\n    def test_step(self): pass\ndef test_other(): pass\n",
		"tests/ops_test.py":        "async def test_async(): pass\n",
		"tests/unselected.py":      "invalid and must not be inventoried\n",
		"drivers/explore.py":       "def drive(): pass\ndef unused(): pass\n",
		"drivers/again.py":         "async def drive(): pass\n",
		"notes/assumption.txt":     "the caller serializes calls\n",
		"data/input.json":          "{\"n\":1}\n",
		".invariant/ratified.lock": `{"bounds":{"N":"2"},"statements":[{"name":"Spec","kind":"spec"},{"name":"Safe","kind":"invariant"}]}`,
	} {
		py304Write(t, dir, name, body)
	}
	target := py304Ref("core/ops.py", "Alpha.step")
	test := py304Ref("tests/test_ops.py", "TestAlpha.test_step")
	driver := py304Ref("drivers/explore.py", "drive")
	return &project.Project{Dir: dir, Manifest: project.Manifest{
		Name: "Python discovery", Language: "python", Code: "core", Module: ".invariant/specs/Counter.tla",
		Conformance: "drivers/explore.py",
		Components: &project.ComponentsConfig{
			Scope: []string{"other", "core"},
			Checks: []project.ComponentCheck{{
				Target: target, Proof: &project.ComponentProof{Contracts: []project.ComponentRef{target}},
				Tests:       []project.ComponentRef{test, py304Ref("tests/ops_test.py", "test_async")},
				Conformance: []project.ComponentRef{driver, py304Ref("drivers/again.py", "drive")},
				Assumptions: []project.ComponentAssumption{{ID: "serialized", Statement: "Calls are serialized", Reason: "Caller lock", Source: "notes/assumption.txt"}},
				Invocations: []project.ComponentInvocation{
					{ID: "proof", Kind: "proof", Check: target, Command: "must-not-run", WorkingDir: ".", Inputs: []string{"core/ops.py"}},
					{ID: "test", Kind: "test", Check: test, Command: "must-not-run", WorkingDir: "tests", Args: []string{"b", "a"}, Env: map[string]string{"MODE": "test"}, Inputs: []string{"tests/test_ops.py"}},
					{ID: "drive", Kind: "conformance", Check: driver, Command: "must-not-run", WorkingDir: ".", Sampling: map[string]string{"seed": "7"}, Inputs: []string{"data/input.json", "drivers/explore.py"}},
				},
			}},
		},
	}}
}

func py304Load(t *testing.T, p *project.Project) (*project.Project, error) {
	t.Helper()
	b, err := json.Marshal(p.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	py304Write(t, p.Dir, ".invariant/invariant.json", string(b))
	return project.Load(p.Dir)
}

func py304Resolve(t *testing.T, p *project.Project) *project.ComponentInventory {
	t.Helper()
	_, err := py304Load(t, p)
	if err != nil {
		t.Fatalf("valid explicit Python manifest must load: %v", err)
	}
	inv, err := project.ResolveComponents(p)
	if err != nil {
		t.Fatalf("resolve Python components: %v", err)
	}
	return inv
}

func py304Component(t *testing.T, inv *project.ComponentInventory, source, symbol string) project.Component {
	t.Helper()
	id := py304ID(source, symbol)
	for _, c := range inv.Components {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("missing component %s", id)
	return project.Component{}
}

func py304NoEvidence(t *testing.T, value any) {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for key, item := range x {
				switch key {
				case "assurance", "evidence", "proved", "verified", "status":
					t.Errorf("inventory manufactured %q: %s", key, b)
				}
				walk(item)
			}
		case []any:
			for _, item := range x {
				walk(item)
			}
		}
	}
	walk(decoded)
}

func TestPythonDiscoveryInventoriesSyntaxWithoutExecution(t *testing.T) {
	p := py304Fixture(t)
	// Import hooks, module top-level code, default values, annotations, class
	// bases and decorators must all remain inert during syntax discovery.
	marker := filepath.Join(p.Dir, "executed")
	payload := "__import__('pathlib').Path(" + "'" + filepath.ToSlash(marker) + "'" + ").write_text('executed')"
	py304Write(t, p.Dir, "core/danger.py", "open('executed', 'w').write('bad')\n@("+payload+")\ndef danger(x="+payload+") -> ("+payload+"): pass\nclass Danger("+payload+"):\n    def method(self): pass\n")
	py304Write(t, p.Dir, "ast.py", payload+"\nraise RuntimeError('project ast shadowed stdlib')\n")
	py304Write(t, p.Dir, "sitecustomize.py", payload+"\n")
	t.Setenv("PYTHONPATH", p.Dir)
	inv := py304Resolve(t, p)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("discovery executed project code: %v", err)
	}
	want := map[string]bool{}
	for _, symbol := range []string{"Alpha.step", "Beta.step", "fetch", "fetch.<locals>.inner", "outer", "outer.<locals>.inner", "outer.<locals>.Local.method", "Outer.Inner.step", "Properties.value[1]", "Properties.value[2]", "conditional[1]", "conditional[2]"} {
		want[py304ID("core/ops.py", symbol)] = true
	}
	for source, symbols := range map[string][]string{"core/__init__.py": {"init"}, "other/ops.py": {"Alpha.step"}, "core/danger.py": {"danger", "Danger.method"}} {
		for _, symbol := range symbols {
			want[py304ID(source, symbol)] = true
		}
	}
	if inv.Mode != "explicit" || !reflect.DeepEqual(inv.Scope, []string{"core", "other"}) {
		t.Fatalf("wrong explicit scope: %#v", inv)
	}
	var ids []string
	for _, c := range inv.Components {
		if !want[c.ID] {
			t.Errorf("unexpected or duplicated component: %#v", c)
		}
		delete(want, c.ID)
		if c.ID != py304ID(c.Source, c.Symbol) || c.Language != "python" || c.Package != strings.ReplaceAll(strings.TrimSuffix(c.Source, ".py"), "/", ".") {
			t.Errorf("bad identity: %#v", c)
		}
		b, err := os.ReadFile(filepath.Join(p.Dir, c.Source))
		if err != nil || c.SourceSHA256 != py304Hash(string(b)) {
			t.Errorf("bad source snapshot: %#v, %v", c, err)
		}
		ids = append(ids, c.ID)
	}
	if len(want) != 0 || !sort.StringsAreSorted(ids) {
		t.Fatalf("missing declarations %v or unsorted inventory %v", want, ids)
	}
	var paths []string
	for _, f := range inv.Files {
		paths = append(paths, f.Path)
		b, err := os.ReadFile(filepath.Join(p.Dir, f.Path))
		if err != nil || f.SHA256 != py304Hash(string(b)) {
			t.Errorf("bad file snapshot: %#v, %v", f, err)
		}
	}
	if !reflect.DeepEqual(paths, []string{"core/__init__.py", "core/danger.py", "core/data.py", "core/ops.py", "other/ops.py"}) {
		t.Fatalf("scope must inventory exactly its immediate non-test .py files: %v", paths)
	}
	py304NoEvidence(t, inv)
	p.Manifest.Components.Checks = nil
	unselected := py304Resolve(t, p)
	if !reflect.DeepEqual(inv.Components, unselected.Components) || !reflect.DeepEqual(inv.Files, unselected.Files) || len(unselected.Checks) != 0 {
		t.Fatal("selection changed discovery or manufactured checks")
	}
}

func TestPythonDiscoveryResolvesSelectionsAndProvenance(t *testing.T) {
	p := py304Fixture(t)
	inv := py304Resolve(t, p)
	target := py304ID("core/ops.py", "Alpha.step")
	if len(inv.Checks) != 1 {
		t.Fatalf("checks: %#v", inv.Checks)
	}
	c := inv.Checks[0]
	if c.Target != target || c.Proof == nil || !reflect.DeepEqual(c.Proof.Contracts, []string{target}) {
		t.Fatalf("wrong proof selection: %#v", c)
	}
	wantTests := []string{py304ID("tests/ops_test.py", "test_async"), py304ID("tests/test_ops.py", "TestAlpha.test_step")}
	wantDrivers := []string{py304ID("drivers/again.py", "drive"), py304ID("drivers/explore.py", "drive")}
	sort.Strings(wantTests)
	sort.Strings(wantDrivers)
	if !reflect.DeepEqual(c.Tests, wantTests) || !reflect.DeepEqual(c.Conformance, wantDrivers) {
		t.Fatalf("wrong associations: %#v", c)
	}
	if inv.Provenance == nil || len(inv.Provenance.Modules) != 0 {
		t.Fatalf("Python modules must be derived from paths, without Go modules: %#v", inv.Provenance)
	}
	var declarations []string
	for _, d := range inv.Provenance.Declarations {
		declarations = append(declarations, d.ID)
		b, err := os.ReadFile(filepath.Join(p.Dir, d.Source))
		if err != nil || d.SourceSHA256 != py304Hash(string(b)) || d.Language != "python" || d.ID != py304ID(d.Source, d.Symbol) {
			t.Fatalf("wrong provenance declaration: %#v, %v", d, err)
		}
	}
	wantDeclarations := append(append([]string{}, wantTests...), wantDrivers...)
	sort.Strings(wantDeclarations)
	if !reflect.DeepEqual(declarations, wantDeclarations) {
		t.Fatalf("unselected neighbors leaked into provenance: %v", declarations)
	}
	var files []string
	for _, f := range inv.Provenance.Files {
		files = append(files, f.Path)
		b, err := os.ReadFile(filepath.Join(p.Dir, f.Path))
		if err != nil || f.SHA256 != py304Hash(string(b)) {
			t.Fatalf("wrong provenance file: %#v, %v", f, err)
		}
	}
	if !reflect.DeepEqual(files, []string{"core/ops.py", "data/input.json", "drivers/again.py", "drivers/explore.py", "notes/assumption.txt", "tests/ops_test.py", "tests/test_ops.py"}) {
		t.Fatalf("wrong provenance files: %v", files)
	}
	if len(c.Assumptions) != 1 || c.Assumptions[0].Source.SHA256 != py304Hash("the caller serializes calls\n") {
		t.Fatalf("lost assumptions: %#v", c.Assumptions)
	}
	if len(c.Invocations) != 3 || c.Invocations[0].Kind != "conformance" || c.Invocations[0].Check != py304ID("drivers/explore.py", "drive") || c.Invocations[1].Check != target || c.Invocations[2].Check != py304ID("tests/test_ops.py", "TestAlpha.test_step") {
		t.Fatalf("invocations not resolved and sorted: %#v", c.Invocations)
	}
	if !reflect.DeepEqual(c.Invocations[2].Args, []string{"b", "a"}) || c.Invocations[2].Command != "must-not-run" {
		t.Fatal("invocation changed literal command or argument order")
	}
	py304NoEvidence(t, inv)
	before, _ := json.Marshal(inv)
	p.Manifest.Components.Scope[0] = "changed"
	p.Manifest.Components.Checks[0].Invocations[1].Args[0] = "changed"
	p.Manifest.Components.Checks[0].Invocations[1].Env["MODE"] = "changed"
	py304Write(t, p.Dir, "tests/test_ops.py", "def changed(): pass\n")
	after, _ := json.Marshal(inv)
	if string(before) != string(after) {
		t.Fatal("resolved snapshot aliases later configuration or filesystem edits")
	}
}

func TestPythonDiscoveryIDsAndHashesAreIndependent(t *testing.T) {
	a, b := py304Fixture(t), py304Fixture(t)
	for _, p := range []*project.Project{a, b} {
		// Module identity distinguishes a module from the package initializer.
		py304Write(t, p.Dir, "core.py", "def init(): pass\n")
		py304Write(t, p.Dir, "__init__.py", "def init(): pass\n")
		p.Manifest.Components.Scope = append(p.Manifest.Components.Scope, ".")
		p.Manifest.Components.Checks = append(p.Manifest.Components.Checks, project.ComponentCheck{
			Target: py304Ref("core/ops.py", "fetch.<locals>.inner"), Tests: []project.ComponentRef{py304Ref("tests/ops_test.py", "test_async")},
		})
	}
	first := py304Resolve(t, a)
	if py304Component(t, first, "core.py", "init").Package != "core" || py304Component(t, first, "core/__init__.py", "init").Package != "core.__init__" || py304Component(t, first, "__init__.py", "init").Package != "__init__" {
		t.Fatal("initializer module identity collapsed")
	}
	cfg := b.Manifest.Components
	cfg.Scope[0], cfg.Scope[2] = cfg.Scope[2], cfg.Scope[0]
	cfg.Checks[0].Tests[0], cfg.Checks[0].Tests[1] = cfg.Checks[0].Tests[1], cfg.Checks[0].Tests[0]
	cfg.Checks[0].Conformance[0], cfg.Checks[0].Conformance[1] = cfg.Checks[0].Conformance[1], cfg.Checks[0].Conformance[0]
	cfg.Checks[0].Invocations[0], cfg.Checks[0].Invocations[2] = cfg.Checks[0].Invocations[2], cfg.Checks[0].Invocations[0]
	cfg.Checks[0].Invocations[0].Inputs[0], cfg.Checks[0].Invocations[0].Inputs[1] = cfg.Checks[0].Invocations[0].Inputs[1], cfg.Checks[0].Invocations[0].Inputs[0]
	cfg.Checks[0], cfg.Checks[1] = cfg.Checks[1], cfg.Checks[0]
	t.Setenv("PYTHONPATH", filepath.Join(b.Dir, "other"))
	second := py304Resolve(t, b)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("inventory depends on relocation, PYTHONPATH or set ordering")
	}
	py304Write(t, b.Dir, "core/ops.py", "# moved down\n\n"+strings.Replace(py304Core, "return n", "return n + 1", 1))
	body := py304Resolve(t, b)
	if len(body.Components) != len(first.Components) {
		t.Fatal("body edit changed declaration count")
	}
	for i, old := range first.Components {
		next := body.Components[i]
		if old.ID != next.ID || old.ContractSHA256 != next.ContractSHA256 {
			t.Fatalf("body or line edit changed stable identity: %#v -> %#v", old, next)
		}
		if (old.SourceSHA256 != next.SourceSHA256) != (old.Source == "core/ops.py") {
			t.Fatalf("source hash did not track edited file: %s", old.ID)
		}
	}
	py304Write(t, b.Dir, "core/ops.py", strings.Replace(py304Core, "Requires(n >= 0)", "Requires(n >= 1)", 1))
	contract := py304Resolve(t, b)
	for i, old := range first.Components {
		next := contract.Components[i]
		if old.ID != next.ID {
			t.Fatalf("contract edit renamed %s", old.ID)
		}
		changed := old.Source == "core/ops.py" && old.Symbol == "Alpha.step"
		if (old.ContractSHA256 != next.ContractSHA256) != changed {
			t.Fatalf("contract change affected the wrong owner: %s", old.ID)
		}
	}
	// The old snapshots must not track disk edits performed after resolution.
	if py304Component(t, first, "core/ops.py", "Alpha.step").SourceSHA256 != py304Hash(py304Core) {
		t.Fatal("old snapshot changed")
	}
}

func TestPythonDiscoveryNumbersRepeatedLexicalDeclarations(t *testing.T) {
	p := py304Fixture(t)
	source := "core/repeated.py"
	body := `def repeated():
    Requires(True)
    def inner(): return 1
    return inner

def repeated():
    Requires(False)
    def inner(): return 2
    return inner

class Same:
    def step(self): return 1
class Same:
    def step(self): return 2

class Methods:
    def step(self): return 1
    async def step(self): return 2

def nesting():
    def local(): return 1
    def local(): return 2
    class Local:
        def step(self): return 1
    class Local:
        def step(self): return 2

def _(): pass
`
	py304Write(t, p.Dir, source, body)
	symbols := []string{"repeated[1]", "repeated[1].<locals>.inner", "repeated[2]", "repeated[2].<locals>.inner", "Same[1].step", "Same[2].step", "Methods.step[1]", "Methods.step[2]", "nesting", "nesting.<locals>.local[1]", "nesting.<locals>.local[2]", "nesting.<locals>.Local[1].step", "nesting.<locals>.Local[2].step", "_"}
	for _, symbol := range symbols {
		p.Manifest.Components.Checks = append(p.Manifest.Components.Checks, project.ComponentCheck{Target: py304Ref(source, symbol), Tests: []project.ComponentRef{py304Ref("tests/ops_test.py", "test_async")}})
	}
	inv := py304Resolve(t, p)
	found := map[string]bool{}
	for _, c := range inv.Components {
		if c.Source == source {
			found[c.Symbol] = true
		}
	}
	if len(found) != len(symbols) {
		t.Fatalf("lost or invented repeated declarations: %v", found)
	}
	for _, symbol := range symbols {
		if !found[symbol] {
			t.Errorf("missing %s", symbol)
		}
	}
	if len(inv.Checks) != 1+len(symbols) {
		t.Fatal("ordinal references did not select each declaration once")
	}
	for _, symbol := range []string{"repeated", "repeated.<locals>.inner", "Same.step", "Methods.step", "nesting.<locals>.local", "nesting.<locals>.Local.step", "repeated[3]"} {
		bad := *p.Manifest.Components
		bad.Checks = []project.ComponentCheck{{Target: py304Ref(source, symbol), Tests: []project.ComponentRef{py304Ref("tests/ops_test.py", "test_async")}}}
		q := *p
		q.Manifest.Components = &bad
		if _, err := project.ResolveComponents(&q); err == nil || !strings.Contains(err.Error(), symbol) {
			t.Errorf("ambiguous or absent ordinal %q accepted or poorly diagnosed: %v", symbol, err)
		}
	}
	// Moving ordinary lines preserves ordinals; swapping repeated definitions
	// can change which source contract an ordinal denotes, just like Go init.
	first := py304Component(t, inv, source, "repeated[1]")
	second := py304Component(t, inv, source, "repeated[2]")
	blocks := strings.SplitN(body, "\n\n", 3)
	changed := blocks[1] + "\n\n" + blocks[0] + "\n\n" + blocks[2]
	py304Write(t, p.Dir, source, "\n\n"+changed)
	after := py304Resolve(t, p)
	if py304Component(t, after, source, "repeated[1]").ContractSHA256 != second.ContractSHA256 || py304Component(t, after, source, "repeated[2]").ContractSHA256 != first.ContractSHA256 {
		t.Fatal("ordinals did not follow declaration order and source contracts")
	}
}

func TestPythonDiscoveryHashesOnlySupportedContractBytes(t *testing.T) {
	p := py304Fixture(t)
	contract1 := "Requires(\n        x >= 0  # within the expression\n    )"
	contract2 := "Ensures(Result() >= 0)"
	contract3 := "Exsures(ValueError, x < 0)"
	contract4 := "Decreases(x)"
	source := `# +nagini
from nagini_contracts.contracts import *
@unknown_decorator
def complete(x):
    """Requires(False) is only documentation."""
    ` + contract1 + `  # outside the expression
    ` + contract2 + `
    ` + contract3 + `
    ` + contract4 + `
    return x

def marker_only():
    # Requires(False)
    """Ensures(False)"""
    return 1

def nested():
    def inner():
        Requires(True)
        return 1
    if enabled:
        Ensures(False)
    return inner

def qualified():
    contracts.Requires(True)
    return 1

def alias_contract():
    Req(True)
    return 1

@Pure
def decorator_only(): return 1

def assigned():
    x = Requires(True)
    return x

def inline(): Requires(True); return 1
`
	py304Write(t, p.Dir, "core/contracts.py", source)
	inv := py304Resolve(t, p)
	expected := py304Hash(contract1 + contract2 + contract3 + contract4)
	if py304Component(t, inv, "core/contracts.py", "complete").ContractSHA256 != expected {
		t.Fatal("contract hash must cover exact standalone expression spans in source order")
	}
	for _, symbol := range []string{"marker_only", "nested", "qualified", "alias_contract", "decorator_only", "assigned"} {
		if c := py304Component(t, inv, "core/contracts.py", symbol); c.ContractSHA256 != "" {
			t.Errorf("%s got a contract from unsupported or nested syntax", symbol)
		}
	}
	for _, symbol := range []string{"nested.<locals>.inner", "inline"} {
		if c := py304Component(t, inv, "core/contracts.py", symbol); c.ContractSHA256 != py304Hash("Requires(True)") {
			t.Errorf("wrong direct contract hash for %s: %s", symbol, c.ContractSHA256)
		}
	}
	for _, symbol := range []string{"complete", "nested.<locals>.inner", "inline"} {
		ref := py304Ref("core/contracts.py", symbol)
		p.Manifest.Components.Checks = append(p.Manifest.Components.Checks, project.ComponentCheck{Target: ref, Proof: &project.ComponentProof{Contracts: []project.ComponentRef{ref}}})
	}
	py304Resolve(t, p)
	crlf := strings.ReplaceAll(source, "\n", "\r\n")
	py304Write(t, p.Dir, "core/contracts.py", crlf)
	windows := py304Resolve(t, p)
	c := py304Component(t, windows, "core/contracts.py", "complete")
	if c.ContractSHA256 != py304Hash(strings.ReplaceAll(contract1+contract2+contract3+contract4, "\n", "\r\n")) || c.SourceSHA256 != py304Hash(crlf) {
		t.Fatal("parser normalized original CRLF contract or source bytes")
	}
	if py304Component(t, windows, "core/contracts.py", "inline").ContractSHA256 != py304Hash("Requires(True)") {
		t.Fatal("contract included an outside newline")
	}
	// UTF-8 offsets count bytes, not Unicode codepoints. Python encoding
	// declarations also must not silently turn a complete-file snapshot into
	// a decoded-text hash, nor normalize the contract bytes.
	encoded := "# coding: latin-1\nlabel = 'caf\xe9'\ndef caf\xe9():\n    Requires('caf\xe9' != '')\n    return 1\n"
	py304Write(t, p.Dir, "core/encoded.py", encoded)
	py304Write(t, p.Dir, "core/unicode.py", "label = '世界'; other = 1\ndef café():\n    Requires('世界' != '')\n    return 1\n")
	unicode := py304Resolve(t, p)
	if c := py304Component(t, unicode, "core/encoded.py", "café"); c.SourceSHA256 != py304Hash(encoded) || c.ContractSHA256 != py304Hash("Requires('caf\xe9' != '')") {
		t.Fatalf("wrong encoded source or contract snapshot: %#v", c)
	}
	if c := py304Component(t, unicode, "core/unicode.py", "café"); c.ContractSHA256 != py304Hash("Requires('世界' != '')") {
		t.Fatalf("wrong UTF-8 span: %#v", c)
	}
}

func TestPythonDiscoveryRejectsInvalidSourceAndSelections(t *testing.T) {
	// A positive load is essential: rejecting Python altogether does not meet
	// this test's contract, even though it also rejects the invalid cases.
	py304Resolve(t, py304Fixture(t))
	cases := []struct {
		name, diagnostic string
		change           func(*testing.T, *project.Project)
	}{
		{"empty scope", "scope", func(t *testing.T, p *project.Project) { p.Manifest.Components.Scope = nil }},
		{"duplicate scope", "core", func(t *testing.T, p *project.Project) {
			p.Manifest.Components.Scope = append(p.Manifest.Components.Scope, "core")
		}},
		{"missing scope", "missing", func(t *testing.T, p *project.Project) { p.Manifest.Components.Scope = []string{"missing"} }},
		{"file scope", "ops.py", func(t *testing.T, p *project.Project) { p.Manifest.Components.Scope = []string{"core/ops.py"} }},
		{"no production Python", "scope", func(t *testing.T, p *project.Project) {
			p.Manifest.Components.Scope = []string{"tests"}
			if err := os.Remove(filepath.Join(p.Dir, "tests/unselected.py")); err != nil {
				t.Fatal(err)
			}
		}},
		{"duplicate target", "Alpha.step", func(t *testing.T, p *project.Project) {
			c := p.Manifest.Components
			c.Checks = append(c.Checks, c.Checks[0])
		}},
		{"missing target", "absent", func(t *testing.T, p *project.Project) { p.Manifest.Components.Checks[0].Target.Symbol = "absent" }},
		{"bare method", "step", func(t *testing.T, p *project.Project) { p.Manifest.Components.Checks[0].Target.Symbol = "step" }},
		{"ambiguous target", "conditional", func(t *testing.T, p *project.Project) { p.Manifest.Components.Checks[0].Target.Symbol = "conditional" }},
		{"class selection", "Alpha", func(t *testing.T, p *project.Project) { p.Manifest.Components.Checks[0].Target.Symbol = "Alpha" }},
		{"lambda selection", "lambda_value", func(t *testing.T, p *project.Project) { p.Manifest.Components.Checks[0].Target.Symbol = "lambda_value" }},
		{"alias selection", "alias", func(t *testing.T, p *project.Project) { p.Manifest.Components.Checks[0].Target.Symbol = "alias" }},
		{"missing source", "missing.py", func(t *testing.T, p *project.Project) {
			p.Manifest.Components.Checks[0].Target.Source = "core/missing.py"
		}},
		{"target outside scope", "drive", func(t *testing.T, p *project.Project) {
			p.Manifest.Components.Checks[0].Target = py304Ref("drivers/explore.py", "drive")
		}},
		{"target is test", "test_async", func(t *testing.T, p *project.Project) {
			p.Manifest.Components.Checks[0].Target = py304Ref("tests/ops_test.py", "test_async")
		}},
		{"no intended check", "check", func(t *testing.T, p *project.Project) {
			c := p.Manifest.Components
			c.Checks[0] = project.ComponentCheck{Target: c.Checks[0].Target}
		}},
		{"empty proof", "contract", func(t *testing.T, p *project.Project) { p.Manifest.Components.Checks[0].Proof.Contracts = nil }},
		{"missing contract", "missing_contract", func(t *testing.T, p *project.Project) {
			p.Manifest.Components.Checks[0].Proof.Contracts[0].Symbol = "missing_contract"
		}},
		{"contract outside scope", "drive", func(t *testing.T, p *project.Project) {
			py304Write(t, p.Dir, "drivers/explore.py", "def drive():\n    Requires(True)\n    pass\n")
			p.Manifest.Components.Checks[0].Proof.Contracts[0] = py304Ref("drivers/explore.py", "drive")
		}},
		{"no inline contract", "Beta.step", func(t *testing.T, p *project.Project) {
			p.Manifest.Components.Checks[0].Proof.Contracts[0].Symbol = "Beta.step"
		}},
		{"marker and fake contract", "fake_contract", func(t *testing.T, p *project.Project) {
			py304Write(t, p.Dir, "core/fake.py", "# +nagini\ndef fake_contract():\n    # Requires(True)\n    'Ensures(True)'\n    pass\n")
			p.Manifest.Components.Checks[0].Proof.Contracts[0] = py304Ref("core/fake.py", "fake_contract")
		}},
		{"unsupported contract", "qualified", func(t *testing.T, p *project.Project) {
			py304Write(t, p.Dir, "core/fake.py", "def qualified():\n    contracts.Requires(True)\n    pass\n")
			p.Manifest.Components.Checks[0].Proof.Contracts[0] = py304Ref("core/fake.py", "qualified")
		}},
		{"duplicate contract", "Alpha.step", func(t *testing.T, p *project.Project) {
			c := &p.Manifest.Components.Checks[0]
			c.Proof.Contracts = append(c.Proof.Contracts, c.Proof.Contracts[0])
		}},
		{"missing test", "missing_test", func(t *testing.T, p *project.Project) {
			p.Manifest.Components.Checks[0].Tests[0].Symbol = "missing_test"
		}},
		{"production file as test", "test", func(t *testing.T, p *project.Project) {
			p.Manifest.Components.Checks[0].Tests[0] = py304Ref("core/ops.py", "Alpha.step")
		}},
		{"Go file as test", "test", func(t *testing.T, p *project.Project) {
			py304Write(t, p.Dir, "tests/check_test.go", "package tests\nfunc TestStep() {}\n")
			p.Manifest.Components.Checks[0].Tests[0] = py304Ref("tests/check_test.go", "TestStep")
		}},
		{"duplicate test", "TestAlpha.test_step", func(t *testing.T, p *project.Project) {
			c := &p.Manifest.Components.Checks[0]
			c.Tests = append(c.Tests, c.Tests[0])
		}},
		{"ambiguous test", "test_async", func(t *testing.T, p *project.Project) {
			py304Write(t, p.Dir, "tests/ops_test.py", "def test_async(): pass\ndef test_async(): pass\n")
		}},
		{"missing driver", "absent", func(t *testing.T, p *project.Project) {
			p.Manifest.Components.Checks[0].Conformance[0].Symbol = "absent"
		}},
		{"duplicate driver", "drive", func(t *testing.T, p *project.Project) {
			c := &p.Manifest.Components.Checks[0]
			c.Conformance = append(c.Conformance, c.Conformance[0])
		}},
		{"ambiguous driver", "drive", func(t *testing.T, p *project.Project) {
			py304Write(t, p.Dir, "drivers/explore.py", "def drive(): pass\ndef drive(): pass\n")
		}},
		{"malformed unselected source", "broken.py", func(t *testing.T, p *project.Project) {
			py304Write(t, p.Dir, "core/broken.py", "def broken(:\n    pass\n")
		}},
		{"malformed function-free source", "data.py", func(t *testing.T, p *project.Project) { py304Write(t, p.Dir, "core/data.py", "value = (\n") }},
		{"bad indentation", "bad_indent.py", func(t *testing.T, p *project.Project) {
			py304Write(t, p.Dir, "core/bad_indent.py", "def broken():\nreturn 1\n")
		}},
		{"malformed selected test", "test_ops.py", func(t *testing.T, p *project.Project) {
			py304Write(t, p.Dir, "tests/test_ops.py", "def test_step(: pass\n")
		}},
		{"malformed selected driver", "explore.py", func(t *testing.T, p *project.Project) {
			py304Write(t, p.Dir, "drivers/explore.py", "def drive(: pass\n")
		}},
		{"unselected invocation", "invocation", func(t *testing.T, p *project.Project) {
			p.Manifest.Components.Checks[0].Invocations[1].Check = py304Ref("tests/test_ops.py", "test_other")
		}},
		{"duplicate invocation input", "input", func(t *testing.T, p *project.Project) {
			v := &p.Manifest.Components.Checks[0].Invocations[0]
			v.Inputs = append(v.Inputs, v.Inputs[0])
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := py304Fixture(t)
			tc.change(t, p)
			_, err := py304Load(t, p)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.diagnostic)) {
				t.Fatalf("invalid Python manifest accepted or poorly diagnosed: %v; want %q", err, tc.diagnostic)
			}
			if _, err := project.ResolveComponents(p); err == nil {
				t.Fatal("direct resolution accepted invalid selections")
			}
		})
	}
	// A scope can contain only function-free files and still be an explicit,
	// nonempty source inventory. It does not need an invented declaration.
	p := py304Fixture(t)
	py304Write(t, p.Dir, "empty/constants.py", "VALUE = 1\n")
	p.Manifest.Components = &project.ComponentsConfig{Scope: []string{"empty"}}
	inv := py304Resolve(t, p)
	if len(inv.Components) != 0 || len(inv.Files) != 1 || inv.Files[0].Path != "empty/constants.py" {
		t.Fatalf("function-free scope was lost: %#v", inv)
	}
}

func TestPythonDiscoveryContainsEveryInputPath(t *testing.T) {
	py304Resolve(t, py304Fixture(t))
	cases := []struct {
		name   string
		change func(*testing.T, *project.Project)
	}{
		{"scope parent", func(t *testing.T, p *project.Project) { p.Manifest.Components.Scope = []string{"../outside"} }},
		{"scope alias", func(t *testing.T, p *project.Project) { p.Manifest.Components.Scope = []string{"core/../core"} }},
		{"scope absolute", func(t *testing.T, p *project.Project) {
			p.Manifest.Components.Scope = []string{filepath.Join(p.Dir, "core")}
		}},
		{"scope separator", func(t *testing.T, p *project.Project) { p.Manifest.Components.Scope = []string{`core\sub`} }},
		{"target alias", func(t *testing.T, p *project.Project) {
			p.Manifest.Components.Checks[0].Target.Source = "core/./ops.py"
		}},
		{"contract alias", func(t *testing.T, p *project.Project) {
			p.Manifest.Components.Checks[0].Proof.Contracts[0].Source = "core/../core/ops.py"
		}},
		{"test alias", func(t *testing.T, p *project.Project) {
			p.Manifest.Components.Checks[0].Tests[0].Source = "tests/./test_ops.py"
		}},
		{"test absolute", func(t *testing.T, p *project.Project) {
			p.Manifest.Components.Checks[0].Tests[0].Source = filepath.Join(p.Dir, "tests/test_ops.py")
		}},
		{"driver escape", func(t *testing.T, p *project.Project) {
			p.Manifest.Components.Checks[0].Conformance[0].Source = "../outside.py"
		}},
		{"assumption escape", func(t *testing.T, p *project.Project) {
			p.Manifest.Components.Checks[0].Assumptions[0].Source = "../outside.txt"
		}},
		{"input alias", func(t *testing.T, p *project.Project) {
			p.Manifest.Components.Checks[0].Invocations[0].Inputs = []string{"core/./ops.py"}
		}},
		{"working directory alias", func(t *testing.T, p *project.Project) {
			p.Manifest.Components.Checks[0].Invocations[0].WorkingDir = "core/.."
		}},
		{"scope symlink inside", func(t *testing.T, p *project.Project) {
			py304Link(t, filepath.Join(p.Dir, "core"), filepath.Join(p.Dir, "linked"))
			p.Manifest.Components.Scope = []string{"linked"}
		}},
		{"source symlink inside", func(t *testing.T, p *project.Project) {
			py304Link(t, filepath.Join(p.Dir, "core/ops.py"), filepath.Join(p.Dir, "core/alias.py"))
		}},
		{"source symlink outside", func(t *testing.T, p *project.Project) {
			out := t.TempDir()
			py304Write(t, out, "external.py", "def external(): pass\n")
			py304Link(t, filepath.Join(out, "external.py"), filepath.Join(p.Dir, "core/external.py"))
		}},
		{"test directory symlink outside", func(t *testing.T, p *project.Project) {
			out := t.TempDir()
			py304Write(t, out, "test_out.py", "def test_out(): pass\n")
			py304Link(t, out, filepath.Join(p.Dir, "linked"))
			p.Manifest.Components.Checks[0].Tests[0] = py304Ref("linked/test_out.py", "test_out")
		}},
		{"driver symlink inside", func(t *testing.T, p *project.Project) {
			py304Link(t, filepath.Join(p.Dir, "drivers/explore.py"), filepath.Join(p.Dir, "drivers/link.py"))
			p.Manifest.Components.Checks[0].Conformance[0].Source = "drivers/link.py"
		}},
		{"assumption symlink", func(t *testing.T, p *project.Project) {
			py304Link(t, filepath.Join(p.Dir, "notes/assumption.txt"), filepath.Join(p.Dir, "notes/link.txt"))
			p.Manifest.Components.Checks[0].Assumptions[0].Source = "notes/link.txt"
		}},
		{"input symlink", func(t *testing.T, p *project.Project) {
			py304Link(t, filepath.Join(p.Dir, "data/input.json"), filepath.Join(p.Dir, "data/link.json"))
			p.Manifest.Components.Checks[0].Invocations[0].Inputs = []string{"data/link.json"}
		}},
		{"working directory symlink", func(t *testing.T, p *project.Project) {
			py304Link(t, filepath.Join(p.Dir, "core"), filepath.Join(p.Dir, "linked"))
			p.Manifest.Components.Checks[0].Invocations[0].WorkingDir = "linked"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := py304Fixture(t)
			tc.change(t, p)
			if _, err := py304Load(t, p); err == nil {
				t.Fatal("load accepted an unclean path or symlink")
			}
			if _, err := project.ResolveComponents(p); err == nil {
				t.Fatal("resolution accepted an unclean path or symlink")
			}
		})
	}
}

func py304Link(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func TestPythonDiscoveryPreservesGoAndLegacyIdentities(t *testing.T) {
	p := py304Fixture(t)
	py304Resolve(t, p)
	manifestBefore, err := os.ReadFile(filepath.Join(p.Dir, ".invariant/invariant.json"))
	if err != nil {
		t.Fatal(err)
	}
	lockBefore, err := os.ReadFile(filepath.Join(p.Dir, ".invariant/ratified.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := project.Load(p.Dir); err != nil {
		t.Fatal(err)
	}
	if _, err := project.ResolveComponents(p); err != nil {
		t.Fatal(err)
	}
	manifestAfter, _ := os.ReadFile(filepath.Join(p.Dir, ".invariant/invariant.json"))
	lockAfter, _ := os.ReadFile(filepath.Join(p.Dir, ".invariant/ratified.lock"))
	if string(manifestBefore) != string(manifestAfter) || string(lockBefore) != string(lockAfter) {
		t.Fatal("discovery rewrote a manifest or ratified lock")
	}
	py304Write(t, p.Dir, "go.mod", "module example.test/stable\n\ngo 1.27.1\n")
	goSource := "package core\ntype Alpha struct{}\n//@ requires true\nfunc (a *Alpha) Step() {}\nfunc init() {}\nfunc init() {}\nfunc _() {}\nfunc _() {}\nfunc Plain() {}\n"
	py304Write(t, p.Dir, "gocore/ops.go", goSource)
	p.Manifest.Language = "go"
	p.Manifest.Components = &project.ComponentsConfig{Scope: []string{"gocore"}}
	goInv := py304Resolve(t, p)
	want := []string{"(*Alpha).Step", "Plain", "_[1]", "_[2]", "init[1]", "init[2]"}
	if len(goInv.Components) != len(want) {
		t.Fatal("Go declaration inventory changed")
	}
	for i, symbol := range want {
		c := goInv.Components[i]
		contract := ""
		if symbol == "(*Alpha).Step" {
			contract = py304Hash("//@ requires true\n")
		}
		if c.ID != "go:example.test/stable/gocore#"+symbol+"@gocore/ops.go" || c.Language != "go" || c.SourceSHA256 != py304Hash(goSource) || c.ContractSHA256 != contract {
			t.Fatalf("Go identity or hashing changed: %#v", c)
		}
	}
	for _, language := range []string{"", "go", "python", "typescript"} {
		p.Manifest.Language = language
		p.Manifest.Components = nil
		p.Manifest.Code = "does-not-exist"
		inv := py304Resolve(t, p)
		if inv.Mode != "legacy" || len(inv.Files) != 0 || len(inv.Components) != 0 || len(inv.Checks) != 0 || inv.Provenance != nil {
			t.Fatalf("legacy %s inferred an inventory: %#v", language, inv)
		}
	}
	p.Manifest.Language = "typescript"
	p.Manifest.Components = &project.ComponentsConfig{Scope: []string{"core"}}
	if _, err := py304Load(t, p); err == nil || !strings.Contains(strings.ToLower(err.Error()), "typescript") {
		t.Fatalf("explicit TypeScript discovery must remain unsupported: %v", err)
	}
}
