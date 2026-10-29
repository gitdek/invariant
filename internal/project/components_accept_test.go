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

// This slice introduces project.ResolveComponents(*project.Project). Its
// result is a serializable inventory, not a verification report. The JSON
// shape below is the contract for later evidence adapters; configuration
// and this inventory alone must not acquire assurance or evidence fields.
//
// Manifest.components has scope (a list of Go package directories, relative
// to the project, each inventoried non-recursively) and checks. A check has
// target:{source,symbol}, optional proof:{contracts:[{source,symbol}]}, tests
// and conformance, the latter two also lists of {source,symbol}. Paths must
// be clean and project-relative. Associations may name files outside scope,
// but selecting a proof target or its contract outside scope is an error.
//
// Go symbols are Func, Type.Method or (*Type).Method. Initializers are
// init[1], init[2], ... in declaration order within each source file, even
// when there is only one, so repeated init functions cannot disappear.
// The package is the
// import path from the enclosing go.mod and the source's directory, never
// just the package clause. IDs are go:<package>#<symbol>@<source>. Source
// hashes cover the whole source file. Contract hashes cover the exact
// attached doc-comment block (including its final newline) when it contains
// Gobra annotations; no contract means an empty contract hash. All hashes
// use sha256:<lowercase hex>. Non-test files without functions still belong
// in files. All source files are inventoried, independent of build tags.
//
// ResolveComponents returns mode:"legacy" and no inferred inventory when
// components is omitted. Explicit results have mode:"explicit", sorted
// scope, files sorted by path, components sorted by ID, and checks sorted by
// target ID, with sorted association IDs. project.Load validates explicit
// configuration against the filesystem as well as validating the lock.

type acceptSymbol struct {
	Source string `json:"source"`
	Symbol string `json:"symbol"`
}

type acceptProof struct {
	Contracts []acceptSymbol `json:"contracts"`
}

type acceptCheck struct {
	Target      acceptSymbol   `json:"target"`
	Proof       *acceptProof   `json:"proof,omitempty"`
	Tests       []acceptSymbol `json:"tests,omitempty"`
	Conformance []acceptSymbol `json:"conformance,omitempty"`
}

type acceptConfig struct {
	Scope  []string      `json:"scope"`
	Checks []acceptCheck `json:"checks,omitempty"`
}

type acceptIdentity struct {
	ID             string          `json:"id"`
	Language       string          `json:"language"`
	Package        string          `json:"package"`
	Symbol         string          `json:"symbol"`
	Source         string          `json:"source"`
	SourceSHA256   string          `json:"source_sha256"`
	ContractSHA256 string          `json:"contract_sha256"`
	Assurance      json.RawMessage `json:"assurance"`
	Evidence       json.RawMessage `json:"evidence"`
}

type acceptInventory struct {
	Mode  string   `json:"mode"`
	Scope []string `json:"scope"`
	Files []struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	} `json:"files"`
	Components []acceptIdentity `json:"components"`
	Checks     []struct {
		Target string `json:"target"`
		Proof  *struct {
			Contracts []string `json:"contracts"`
		} `json:"proof"`
		Tests       []string `json:"tests"`
		Conformance []string `json:"conformance"`
	} `json:"checks"`
	Assurance json.RawMessage `json:"assurance"`
	Evidence  json.RawMessage `json:"evidence"`
}

const acceptContract = "//@ requires n >= 0\n//@ ensures result >= 0\n"

func acceptID(pkg, symbol, source string) string {
	return "go:" + pkg + "#" + symbol + "@" + source
}

func acceptHash(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func acceptWrite(t *testing.T, dir, name, body string) {
	t.Helper()
	file := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func acceptConfigForFixture() acceptConfig {
	target := acceptSymbol{"core/ops.go", "(*Alpha).Step"}
	return acceptConfig{
		Scope: []string{"other", "core"},
		Checks: []acceptCheck{{
			Target:      target,
			Proof:       &acceptProof{Contracts: []acceptSymbol{target}},
			Tests:       []acceptSymbol{{"core/ops_test.go", "TestAlpha"}},
			Conformance: []acceptSymbol{{"drivers/explore.go", "Drive"}},
		}},
	}
}

func acceptManifest(t *testing.T, dir, language string, cfg *acceptConfig) {
	t.Helper()
	m := map[string]any{
		"name": "component fixture", "language": language,
		"module": ".invariant/specs/Counter.tla", "code": "core",
	}
	if cfg != nil {
		m["components"] = cfg
	}
	if language == "python" || language == "typescript" {
		m["conformance"] = "conformance.driver"
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	acceptWrite(t, dir, ".invariant/invariant.json", string(b)+"\n")
}

func acceptFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":                       "module example.test/app\n\ngo 1.27.1\n",
		".invariant/ratified.lock":     `{"bounds":{"Limit":"2"},"statements":[{"name":"Spec","kind":"spec"},{"name":"Safe","kind":"invariant"}]}`,
		".invariant/specs/Counter.tla": "---- MODULE Counter ----\nEXTENDS Naturals\nCONSTANT Limit\nVARIABLE n\nInit == n = 0\nNext == n' = n\nSpec == Init /\\ [][Next]_n\nSafe == n >= 0\n====\n",
		"core/ops.go": "// +gobra\npackage core\ntype Alpha struct{}\ntype Beta struct{}\n" + acceptContract +
			"func (a *Alpha) Step(n int) (result int) { return n }\n" +
			"func (b Beta) Step(n int) int { return n }\n" +
			"func Helper() int { return 7 }\n",
		"core/data.go":       "package core\nconst Limit = 2\n",
		"core/init.go":       "package core\nfunc init() {}\nfunc init() {}\n",
		"core/ops_test.go":   "package core\nimport \"testing\"\nfunc TestAlpha(t *testing.T) {}\n",
		"core/sub/hidden.go": "package sub\nfunc OutsideScope() {}\n",
		"other/ops.go":       "package other\ntype Alpha struct{}\nfunc (a *Alpha) Step(n int) int { return n }\n",
		"drivers/explore.go": "package drivers\nfunc Drive() {}\n",
		"outside.go":         "package app\nfunc NotDeclared() {}\n",
	}
	for name, body := range files {
		acceptWrite(t, dir, name, body)
	}
	cfg := acceptConfigForFixture()
	acceptManifest(t, dir, "go", &cfg)
	return dir
}

func acceptResolve(t *testing.T, dir string) acceptInventory {
	t.Helper()
	p, err := project.Load(dir)
	if err != nil {
		t.Fatalf("load a valid component project: %v", err)
	}
	result, err := project.ResolveComponents(p)
	if err != nil {
		t.Fatalf("resolve components: %v", err)
	}
	b, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var inv acceptInventory
	if err := json.Unmarshal(b, &inv); err != nil {
		t.Fatal(err)
	}
	if len(inv.Assurance) != 0 || len(inv.Evidence) != 0 {
		t.Fatalf("configuration was presented as evidence: %s", b)
	}
	for _, c := range inv.Components {
		if len(c.Assurance) != 0 || len(c.Evidence) != 0 {
			t.Fatalf("configuration gave %s assurance or evidence: %s", c.ID, b)
		}
	}
	return inv
}

func TestComponentScopeKeepsQualifiedNeighbors(t *testing.T) {
	dir := acceptFixture(t)
	inv := acceptResolve(t, dir)
	if inv.Mode != "explicit" || !reflect.DeepEqual(inv.Scope, []string{"core", "other"}) {
		t.Fatalf("scope was lost: %#v", inv)
	}
	want := map[string]acceptIdentity{}
	for _, c := range []acceptIdentity{
		{Package: "example.test/app/core", Source: "core/ops.go", Symbol: "(*Alpha).Step"},
		{Package: "example.test/app/core", Source: "core/ops.go", Symbol: "Beta.Step"},
		{Package: "example.test/app/core", Source: "core/ops.go", Symbol: "Helper"},
		{Package: "example.test/app/core", Source: "core/init.go", Symbol: "init[1]"},
		{Package: "example.test/app/core", Source: "core/init.go", Symbol: "init[2]"},
		{Package: "example.test/app/other", Source: "other/ops.go", Symbol: "(*Alpha).Step"},
	} {
		c.ID = acceptID(c.Package, c.Symbol, c.Source)
		want[c.ID] = c
	}
	if len(inv.Components) != len(want) {
		t.Fatalf("got %d components, want all six scoped functions, including unselected ones and both initializers: %#v", len(inv.Components), inv.Components)
	}
	var ids []string
	for _, c := range inv.Components {
		expected, ok := want[c.ID]
		if !ok || c.Language != "go" || c.Package != expected.Package || c.Source != expected.Source || c.Symbol != expected.Symbol {
			t.Fatalf("wrong or duplicate qualified component: %#v", c)
		}
		delete(want, c.ID)
		ids = append(ids, c.ID)
		src, err := os.ReadFile(filepath.Join(dir, c.Source))
		if err != nil {
			t.Fatal(err)
		}
		if c.SourceSHA256 != acceptHash(src) {
			t.Errorf("%s doesn't identify its source bytes", c.ID)
		}
		contract := ""
		if c.Source == "core/ops.go" && c.Symbol == "(*Alpha).Step" {
			contract = acceptHash([]byte(acceptContract))
		}
		if c.ContractSHA256 != contract {
			t.Errorf("%s contract hash = %q, want %q", c.ID, c.ContractSHA256, contract)
		}
	}
	if !sort.StringsAreSorted(ids) {
		t.Errorf("component order is not canonical: %v", ids)
	}
	var paths []string
	for _, f := range inv.Files {
		paths = append(paths, f.Path)
		b, err := os.ReadFile(filepath.Join(dir, f.Path))
		if err != nil || f.SHA256 != acceptHash(b) {
			t.Fatalf("source inventory lost file identity: %#v, %v", f, err)
		}
	}
	if !reflect.DeepEqual(paths, []string{"core/data.go", "core/init.go", "core/ops.go", "other/ops.go"}) {
		t.Errorf("scope files = %v; want all non-test files in exactly the declared directories", paths)
	}
	target := acceptID("example.test/app/core", "(*Alpha).Step", "core/ops.go")
	if len(inv.Checks) != 1 || inv.Checks[0].Target != target || inv.Checks[0].Proof == nil {
		t.Fatalf("lost selected check: %#v", inv.Checks)
	}
	check := inv.Checks[0]
	if !reflect.DeepEqual(check.Proof.Contracts, []string{target}) ||
		!reflect.DeepEqual(check.Tests, []string{acceptID("example.test/app/core", "TestAlpha", "core/ops_test.go")}) ||
		!reflect.DeepEqual(check.Conformance, []string{acceptID("example.test/app/drivers", "Drive", "drivers/explore.go")}) {
		t.Fatalf("associations did not resolve to their qualified identities: %#v", check)
	}
	// Scope is independent of selection: an explicitly unselected package
	// must still be visible, with no manufactured proof or other evidence.
	cfg := acceptConfigForFixture()
	cfg.Checks = nil
	acceptManifest(t, dir, "go", &cfg)
	unselected := acceptResolve(t, dir)
	if !reflect.DeepEqual(inv.Components, unselected.Components) || len(unselected.Checks) != 0 {
		t.Fatal("removing intended checks removed source components or created evidence")
	}
}

func TestComponentIdentitiesAndHashesSurviveRelocation(t *testing.T) {
	a, b := acceptFixture(t), acceptFixture(t)
	for _, dir := range []string{a, b} {
		acceptWrite(t, dir, "core/ops_test.go", "package core\nimport \"testing\"\nfunc TestAlpha(t *testing.T) {}\nfunc TestHelper(t *testing.T) {}\n")
		acceptWrite(t, dir, "drivers/more.go", "package drivers\nfunc DriveAgain() {}\n")
	}
	cfg := acceptConfigForFixture()
	cfg.Checks[0].Tests = append(cfg.Checks[0].Tests, acceptSymbol{"core/ops_test.go", "TestHelper"})
	cfg.Checks[0].Conformance = append(cfg.Checks[0].Conformance, acceptSymbol{"drivers/more.go", "DriveAgain"})
	cfg.Checks = append(cfg.Checks, acceptCheck{
		Target: acceptSymbol{"core/ops.go", "Helper"},
		Tests:  []acceptSymbol{{"core/ops_test.go", "TestHelper"}},
	})
	acceptManifest(t, a, "go", &cfg)
	cfg.Scope = []string{"core", "other"}
	cfg.Checks[0].Tests[0], cfg.Checks[0].Tests[1] = cfg.Checks[0].Tests[1], cfg.Checks[0].Tests[0]
	cfg.Checks[0].Conformance[0], cfg.Checks[0].Conformance[1] = cfg.Checks[0].Conformance[1], cfg.Checks[0].Conformance[0]
	cfg.Checks[0], cfg.Checks[1] = cfg.Checks[1], cfg.Checks[0]
	acceptManifest(t, b, "go", &cfg)
	first, relocated := acceptResolve(t, a), acceptResolve(t, b)
	if !reflect.DeepEqual(first, relocated) {
		t.Fatalf("inventory depends on checkout location or scope, check or association order:\n%#v\n%#v", first, relocated)
	}
	before, err := os.ReadFile(filepath.Join(b, "core/ops.go"))
	if err != nil {
		t.Fatal(err)
	}
	acceptWrite(t, b, "core/ops.go", strings.Replace(string(before), "return 7", "return 8", 1))
	afterBody := acceptResolve(t, b)
	for i, c := range first.Components {
		next := afterBody.Components[i]
		if c.ID != next.ID || c.ContractSHA256 != next.ContractSHA256 {
			t.Fatalf("a body edit renamed a component or changed its contract identity: %#v -> %#v", c, next)
		}
		if (c.SourceSHA256 != next.SourceSHA256) != (c.Source == "core/ops.go") {
			t.Fatalf("source identity failed to track the edited file: %#v -> %#v", c, next)
		}
	}
	changedContract := strings.Replace(string(before), "requires n >= 0", "requires n >= 1", 1)
	acceptWrite(t, b, "core/ops.go", changedContract)
	afterContract := acceptResolve(t, b)
	for i, c := range first.Components {
		next := afterContract.Components[i]
		if c.ID != next.ID {
			t.Fatal("a contract edit renamed its component")
		}
		wantChange := c.Source == "core/ops.go" && c.Symbol == "(*Alpha).Step"
		if (c.ContractSHA256 != next.ContractSHA256) != wantChange {
			t.Fatalf("contract identity did not track exactly its owner: %#v -> %#v", c, next)
		}
	}
	// Module import paths are part of identity, even with identical package
	// clauses, relative filenames and source bytes.
	acceptWrite(t, b, "go.mod", "module example.test/another\n\ngo 1.27.1\n")
	renamed := acceptResolve(t, b)
	for _, c := range renamed.Components {
		if !strings.HasPrefix(c.Package, "example.test/another/") || !strings.Contains(c.ID, c.Package) {
			t.Fatalf("component uses a package clause or old module identity: %#v", c)
		}
	}
	acceptWrite(t, b, "other/go.mod", "module example.test/nested\n\ngo 1.27.1\n")
	nested := acceptResolve(t, b)
	for _, c := range nested.Components {
		if c.Source == "other/ops.go" && c.Package != "example.test/nested" {
			t.Fatalf("component ignored its nearest module declaration: %#v", c)
		}
	}
}

func TestComponentSelectionsMustResolveExactlyOnce(t *testing.T) {
	tests := []struct {
		name, diagnostic string
		mutate           func(*acceptConfig)
	}{
		{"empty scope", "scope", func(c *acceptConfig) { c.Scope = nil }},
		{"duplicate scope", "core", func(c *acceptConfig) { c.Scope = append(c.Scope, "core") }},
		{"missing scope", "missing", func(c *acceptConfig) { c.Scope = []string{"missing"} }},
		{"scope is a file", "ops.go", func(c *acceptConfig) { c.Scope = []string{"core/ops.go"} }},
		{"duplicate target", "Step", func(c *acceptConfig) { c.Checks = append(c.Checks, c.Checks[0]) }},
		{"missing target", "NoSuchMethod", func(c *acceptConfig) { c.Checks[0].Target.Symbol = "(*Alpha).NoSuchMethod" }},
		{"bare method", "Step", func(c *acceptConfig) { c.Checks[0].Target.Symbol = "Step" }},
		{"missing target source", "missing.go", func(c *acceptConfig) { c.Checks[0].Target.Source = "core/missing.go" }},
		{"target outside scope", "Drive", func(c *acceptConfig) { c.Checks[0].Target = acceptSymbol{"drivers/explore.go", "Drive"} }},
		{"missing contract selection", "contract", func(c *acceptConfig) { c.Checks[0].Proof.Contracts = nil }},
		{"unresolved contract", "AbsentContract", func(c *acceptConfig) { c.Checks[0].Proof.Contracts[0].Symbol = "AbsentContract" }},
		{"function without contract", "Helper", func(c *acceptConfig) { c.Checks[0].Proof.Contracts[0].Symbol = "Helper" }},
		{"duplicate contract", "Step", func(c *acceptConfig) {
			c.Checks[0].Proof.Contracts = append(c.Checks[0].Proof.Contracts, c.Checks[0].Proof.Contracts[0])
		}},
		{"missing test", "TestMissing", func(c *acceptConfig) { c.Checks[0].Tests[0].Symbol = "TestMissing" }},
		{"test in production file", "Helper", func(c *acceptConfig) { c.Checks[0].Tests[0] = acceptSymbol{"core/ops.go", "Helper"} }},
		{"duplicate test", "TestAlpha", func(c *acceptConfig) { c.Checks[0].Tests = append(c.Checks[0].Tests, c.Checks[0].Tests[0]) }},
		{"missing driver source", "missing.go", func(c *acceptConfig) { c.Checks[0].Conformance[0].Source = "drivers/missing.go" }},
		{"missing driver function", "NotADriver", func(c *acceptConfig) { c.Checks[0].Conformance[0].Symbol = "NotADriver" }},
		{"duplicate driver", "Drive", func(c *acceptConfig) {
			c.Checks[0].Conformance = append(c.Checks[0].Conformance, c.Checks[0].Conformance[0])
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := acceptFixture(t)
			cfg := acceptConfigForFixture()
			tc.mutate(&cfg)
			acceptManifest(t, dir, "go", &cfg)
			_, err := project.Load(dir)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.diagnostic)) {
				t.Fatalf("invalid selection was discarded or poorly diagnosed: %v; want error naming %q", err, tc.diagnostic)
			}
		})
	}
	for _, language := range []string{"python", "typescript"} {
		t.Run("explicit "+language, func(t *testing.T) {
			dir := acceptFixture(t)
			cfg := acceptConfigForFixture()
			acceptManifest(t, dir, language, &cfg)
			_, err := project.Load(dir)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), language) {
				t.Fatalf("unsupported component language entered the legacy path: %v", err)
			}
		})
	}
	t.Run("malformed source", func(t *testing.T) {
		dir := acceptFixture(t)
		acceptWrite(t, dir, "core/broken.go", "package core\nfunc Broken(\n")
		_, err := project.Load(dir)
		if err == nil || !strings.Contains(err.Error(), "broken.go") {
			t.Fatalf("malformed scoped source silently disappeared: %v", err)
		}
	})
}

func TestComponentScopeCannotFollowAliasesOutsideItsProject(t *testing.T) {
	for _, bad := range []string{"../escape", "core/../other", "core//sub", "/absolute", `core\ops.go`} {
		t.Run(bad, func(t *testing.T) {
			dir := acceptFixture(t)
			cfg := acceptConfigForFixture()
			cfg.Scope = []string{bad}
			acceptManifest(t, dir, "go", &cfg)
			if _, err := project.Load(dir); err == nil {
				t.Fatalf("accepted non-clean scope %q", bad)
			}
		})
	}
	t.Run("association escape", func(t *testing.T) {
		dir := acceptFixture(t)
		cfg := acceptConfigForFixture()
		cfg.Checks[0].Tests[0].Source = "../outside_test.go"
		acceptManifest(t, dir, "go", &cfg)
		if _, err := project.Load(dir); err == nil {
			t.Fatal("accepted a test association outside the project")
		}
	})
	for _, kind := range []string{"scope directory", "scope source", "test association"} {
		t.Run(kind, func(t *testing.T) {
			dir, outside := acceptFixture(t), t.TempDir()
			acceptWrite(t, outside, "outsider.go", "package outsider\nfunc Outside() {}\n")
			acceptWrite(t, outside, "outsider_test.go", "package outsider\nimport \"testing\"\nfunc TestOutside(t *testing.T) {}\n")
			cfg := acceptConfigForFixture()
			link, target := "alias", outside
			switch kind {
			case "scope directory":
				cfg.Scope = append(cfg.Scope, "alias")
			case "scope source":
				link, target = "core/alias.go", filepath.Join(outside, "outsider.go")
			case "test association":
				link, target = "core/alias_test.go", filepath.Join(outside, "outsider_test.go")
				cfg.Checks[0].Tests[0] = acceptSymbol{link, "TestOutside"}
			}
			if err := os.Symlink(target, filepath.Join(dir, link)); err != nil {
				t.Fatal(err)
			}
			acceptManifest(t, dir, "go", &cfg)
			if _, err := project.Load(dir); err == nil || !strings.Contains(err.Error(), "alias") {
				t.Fatalf("followed or silently omitted source alias %q: %v", link, err)
			}
		})
	}
}

func TestLegacyComponentModePreservesExistingManifests(t *testing.T) {
	for _, name := range []string{"02-twophase-commit", "02-twophase-commit-py", "02-twophase-commit-py-proved", "02-twophase-commit-ts"} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join("..", "..", "examples", name)
			before := map[string][]byte{}
			for _, f := range []string{"invariant.json", "ratified.lock"} {
				b, err := os.ReadFile(filepath.Join(dir, ".invariant", f))
				if err != nil {
					t.Fatal(err)
				}
				before[f] = b
			}
			inv := acceptResolve(t, dir)
			if inv.Mode != "legacy" || len(inv.Scope) != 0 || len(inv.Files) != 0 || len(inv.Components) != 0 || len(inv.Checks) != 0 {
				t.Fatalf("legacy manifest acquired explicit component semantics: %#v", inv)
			}
			for f, want := range before {
				got, err := os.ReadFile(filepath.Join(dir, ".invariant", f))
				if err != nil || string(got) != string(want) {
					t.Fatalf("loading or resolving rewrote %s: %v", f, err)
				}
			}
		})
	}
	// Legacy Load has never needed code or a Go module to be present. Keep
	// that property for callers constructing and checking project metadata.
	for _, language := range []string{"", "go", "python", "typescript"} {
		t.Run("metadata only "+language, func(t *testing.T) {
			dir := t.TempDir()
			acceptWrite(t, dir, ".invariant/ratified.lock", `{"bounds":{"N":"1"},"statements":[{"name":"Spec","kind":"spec"},{"name":"Safe","kind":"invariant"}]}`)
			acceptManifest(t, dir, language, nil)
			inv := acceptResolve(t, dir)
			if inv.Mode != "legacy" || len(inv.Components) != 0 {
				t.Fatalf("omitted configuration lost its legacy path: %#v", inv)
			}
		})
	}
}
