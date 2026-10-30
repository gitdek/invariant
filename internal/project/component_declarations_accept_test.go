package project_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
)

// Issue #257's wire contract extends the existing selection API, without
// changing Component or any canonical target/test/conformance ID.
//
// checks[].assumptions contains {id,statement,reason,source}, where source is
// any project-relative regular file. IDs are nonblank, unique within a target,
// and stay unchanged when statement, reason or source contents change. The
// resolved source is a ComponentFile {path,sha256}. An assumption-only target
// is valid; a target with no proof, test, conformance or assumption is not.
//
// checks[].invocations contains {id,kind,check,command,args,working_dir,env,
// sampling,inputs}. id, kind, check, command and working_dir are required;
// kind is proof, test or conformance. check is a ComponentRef: for proof it
// must select this target with configured proof, otherwise it must belong to
// this target's corresponding selection list. IDs are unique within each
// (target,kind,check) namespace; two invocations of one check may have different
// IDs. command is opaque text, args an ordered string list, env and sampling
// maps of explicit string values with nonblank keys. working_dir names a clean
// project-relative directory (including "."). Optional inputs is a set of
// explicit regular-file paths; duplicates within an invocation are errors.
// In resolved invocations check is its canonical ID, inputs is a sorted list
// of ComponentFile snapshots, and the other values are retained exactly.
//
// Inventory adds provenance:{declarations,files,modules}. declarations is the
// unique selected test/conformance Component snapshots, sorted by ID. files
// is the union of their whole source files, assumption source files and all
// invocation inputs, sorted/deduplicated by path. modules retains every nearest
// go.mod used to resolve production and selected identities, likewise sorted
// and deduplicated. Explicitly selected module inputs may also appear in files.
// Existing Files and Components remain scoped production only. Collections are
// sets except ordered args; assumptions sort by ID and invocations by
// (kind,check,id). All returned nested values belong to the snapshot.
//
// Inputs may explicitly include models, includes, configuration, dependency
// manifests/lockfiles and ratified.lock: exact-byte hashes bind those inputs
// (including ratified bounds), without interpreting them or adding required
// model files to old manifests. Later attribution compares actual supplied
// file snapshots with these declared snapshots. None of this attests that a
// command ran or that an execution's complete inputs were declared.

type cdAssumption struct {
	ID        string `json:"id"`
	Statement string `json:"statement"`
	Reason    string `json:"reason"`
	Source    string `json:"source"`
}

type cdInvocation struct {
	ID         string               `json:"id"`
	Kind       string               `json:"kind"`
	Check      project.ComponentRef `json:"check"`
	Command    string               `json:"command"`
	Args       []string             `json:"args,omitempty"`
	WorkingDir string               `json:"working_dir"`
	Env        map[string]string    `json:"env,omitempty"`
	Sampling   map[string]string    `json:"sampling,omitempty"`
	Inputs     []string             `json:"inputs,omitempty"`
}

type cdCheck struct {
	Target      project.ComponentRef    `json:"target"`
	Proof       *project.ComponentProof `json:"proof,omitempty"`
	Tests       []project.ComponentRef  `json:"tests,omitempty"`
	Conformance []project.ComponentRef  `json:"conformance,omitempty"`
	Assumptions []cdAssumption          `json:"assumptions,omitempty"`
	Invocations []cdInvocation          `json:"invocations,omitempty"`
}

type cdConfig struct {
	Scope  []string  `json:"scope"`
	Checks []cdCheck `json:"checks"`
}

type cdResolvedAssumption struct {
	ID        string                `json:"id"`
	Statement string                `json:"statement"`
	Reason    string                `json:"reason"`
	Source    project.ComponentFile `json:"source"`
}

type cdResolvedInvocation struct {
	ID         string                  `json:"id"`
	Kind       string                  `json:"kind"`
	Check      string                  `json:"check"`
	Command    string                  `json:"command"`
	Args       []string                `json:"args"`
	WorkingDir string                  `json:"working_dir"`
	Env        map[string]string       `json:"env"`
	Sampling   map[string]string       `json:"sampling"`
	Inputs     []project.ComponentFile `json:"inputs"`
}

type cdInventory struct {
	Mode       string                  `json:"mode"`
	Scope      []string                `json:"scope"`
	Files      []project.ComponentFile `json:"files"`
	Components []project.Component     `json:"components"`
	Checks     []struct {
		Target      string                          `json:"target"`
		Proof       *project.ResolvedComponentProof `json:"proof"`
		Tests       []string                        `json:"tests"`
		Conformance []string                        `json:"conformance"`
		Assumptions []cdResolvedAssumption          `json:"assumptions"`
		Invocations []cdResolvedInvocation          `json:"invocations"`
	} `json:"checks"`
	Provenance *struct {
		Declarations []project.Component     `json:"declarations"`
		Files        []project.ComponentFile `json:"files"`
		Modules      []project.ComponentFile `json:"modules"`
	} `json:"provenance"`
}

func cdJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func cdWrite(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func cdHash(b []byte) string {
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}

func cdRef(source, symbol string) project.ComponentRef {
	return project.ComponentRef{Source: source, Symbol: symbol}
}

func cdID(pkg, source, symbol string) string { return "go:" + pkg + "#" + symbol + "@" + source }

func cdFixture(t *testing.T) (string, cdConfig) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"go.mod":                       "module example.test/inputs\n\ngo 1.27.1\n",
		"drivers/go.mod":               "module example.test/drivers\n\ngo 1.27.1\n",
		"core/core.go":                 "package core\ntype Counter struct{}\n//@ ensures result >= 0\nfunc (c *Counter) Step() (result int) { return 1 }\nfunc Neighbor() {}\nfunc init() {}\nfunc init() {}\nfunc _() {}\n",
		"core/data.go":                 "package core\nconst Limit = 2\n",
		"core/tagged.go":               "//go:build never\n\npackage core\nfunc Tagged() {}\n",
		"core/sub/unselected.go":       "not even valid Go: scope must stay nonrecursive\n",
		"outside_test.go":              "package inputs\nimport \"testing\"\n//@ requires true\nfunc TestStep(t *testing.T) {}\nfunc TestOther(t *testing.T) {}\n",
		"drivers/driver.go":            "package drivers\ntype Walker struct{}\n//@ requires true\nfunc (w *Walker) Drive() {}\nfunc Drive() {}\nfunc init() {}\nfunc init() {}\nfunc _() {}\n",
		"drivers/unselected.go":        "must not parse unselected neighboring driver source\n",
		"notes/trust.txt":              "The caller serializes writes.\r\n",
		"data/fixture.bin":             "\x00\x01\xff\r\n",
		"go.sum":                       "example.test/dependency v1.0.0 h1:fixed\n",
		"config.json":                  "{\"count\":3}\n",
		".invariant/ratified.lock":     `{"bounds":{"Limit":"2"},"statements":[{"name":"Spec","kind":"spec"},{"name":"Safe","kind":"invariant"}]}`,
		".invariant/specs/Counter.tla": "---- MODULE Counter ----\nEXTENDS Naturals, Shared\nCONSTANT Limit\nVARIABLE n\nInit == n = 0\nNext == n' = n\nSpec == Init /\\ [][Next]_n\nSafe == n >= 0\n====\n",
		".invariant/specs/Shared.tla":  "---- MODULE Shared ----\nValue == 1\n====\n",
	} {
		cdWrite(t, dir, name, body)
	}
	target := cdRef("core/core.go", "(*Counter).Step")
	test := cdRef("outside_test.go", "TestStep")
	driver := cdRef("drivers/driver.go", "(*Walker).Drive")
	cfg := cdConfig{Scope: []string{"core"}, Checks: []cdCheck{{
		Target: target, Proof: &project.ComponentProof{Contracts: []project.ComponentRef{target}},
		Tests:       []project.ComponentRef{test, cdRef("outside_test.go", "TestOther")},
		Conformance: []project.ComponentRef{driver, cdRef("drivers/driver.go", "init[2]")},
		Assumptions: []cdAssumption{{ID: "serialized", Statement: "Calls are serialized", Reason: "Caller protocol", Source: "notes/trust.txt"}},
		Invocations: []cdInvocation{{ID: "seeded", Kind: "conformance", Check: driver, Command: "go", Args: []string{"run", "./drivers", "--seed", "7"}, WorkingDir: ".", Env: map[string]string{"MODE": "test", "EMPTY": ""}, Sampling: map[string]string{"seed": "7", "count": "3"}, Inputs: []string{"go.sum", "config.json", "data/fixture.bin", ".invariant/specs/Shared.tla", ".invariant/specs/Counter.tla", ".invariant/ratified.lock"}}},
	}}}
	cdManifest(t, dir, &cfg)
	return dir, cfg
}

func cdManifest(t *testing.T, dir string, cfg *cdConfig) {
	t.Helper()
	m := map[string]any{"name": "inputs", "module": ".invariant/specs/Counter.tla", "language": "go", "code": "core"}
	if cfg != nil {
		m["components"] = cfg
	}
	cdWrite(t, dir, ".invariant/invariant.json", string(cdJSON(t, m)))
}

func cdProject(t *testing.T, dir string, cfg cdConfig) *project.Project {
	t.Helper()
	var m project.Manifest
	b := cdJSON(t, map[string]any{"name": "inputs", "module": ".invariant/specs/Counter.tla", "language": "go", "code": "core", "components": cfg})
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return &project.Project{Dir: dir, Manifest: m}
}

func cdResolve(t *testing.T, dir string) (*project.ComponentInventory, cdInventory) {
	t.Helper()
	p, err := project.Load(dir)
	if err != nil {
		t.Fatalf("load valid declarations: %v", err)
	}
	inv, err := project.ResolveComponents(p)
	if err != nil {
		t.Fatal(err)
	}
	return inv, cdDecode(t, inv)
}

func cdDecode(t *testing.T, inv *project.ComponentInventory) cdInventory {
	t.Helper()
	var out cdInventory
	if err := json.Unmarshal(cdJSON(t, inv), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func cdFiles(t *testing.T, dir string, got []project.ComponentFile, want []string) {
	t.Helper()
	want = slices.Clone(want)
	slices.Sort(want)
	if len(got) != len(want) {
		t.Fatalf("file snapshots = %#v; want paths %v", got, want)
	}
	for i, name := range want {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		if got[i].Path != name || got[i].SHA256 != cdHash(b) {
			t.Fatalf("snapshot %d = %#v; want full byte hash of %s", i, got[i], name)
		}
	}
}

func TestComponentProvenanceSeparatesSelectedDeclarations(t *testing.T) {
	dir, cfg := cdFixture(t)
	// The old manifest shape must gain selected provenance without requiring
	// any new invocation, assumption, model or arbitrary execution inputs.
	cfg.Checks[0].Assumptions, cfg.Checks[0].Invocations = nil, nil
	cdManifest(t, dir, &cfg)
	_, inv := cdResolve(t, dir)
	if inv.Provenance == nil {
		t.Fatal("selected declaration snapshots were discarded")
	}
	for _, check := range inv.Checks {
		if len(check.Assumptions) != 0 || len(check.Invocations) != 0 {
			t.Fatal("undeclared assumptions or invocations were inferred from source")
		}
	}
	cdFiles(t, dir, inv.Files, []string{"core/core.go", "core/data.go", "core/tagged.go"})
	cdFiles(t, dir, inv.Provenance.Files, []string{"outside_test.go", "drivers/driver.go"})
	cdFiles(t, dir, inv.Provenance.Modules, []string{"go.mod", "drivers/go.mod"})
	want := map[string]project.ComponentRef{
		cdID("example.test/inputs", "outside_test.go", "TestStep"):           cdRef("outside_test.go", "TestStep"),
		cdID("example.test/inputs", "outside_test.go", "TestOther"):          cdRef("outside_test.go", "TestOther"),
		cdID("example.test/drivers", "drivers/driver.go", "(*Walker).Drive"): cdRef("drivers/driver.go", "(*Walker).Drive"),
		cdID("example.test/drivers", "drivers/driver.go", "init[2]"):         cdRef("drivers/driver.go", "init[2]"),
	}
	if len(inv.Provenance.Declarations) != len(want) {
		t.Fatalf("selected declarations: %#v", inv.Provenance.Declarations)
	}
	last := ""
	for _, c := range inv.Provenance.Declarations {
		ref, ok := want[c.ID]
		if !ok || c.ID <= last || c.Source != ref.Source || c.Symbol != ref.Symbol || c.Language != "go" {
			t.Fatalf("lost or noncanonical declaration: %#v", c)
		}
		pkg := "example.test/inputs"
		if c.Source == "drivers/driver.go" {
			pkg = "example.test/drivers"
		}
		if c.Package != pkg {
			t.Fatalf("nearest module identity lost: %#v", c)
		}
		cdFiles(t, dir, []project.ComponentFile{{Path: c.Source, SHA256: c.SourceSHA256}}, []string{c.Source})
		contract := ""
		if c.Symbol == "TestStep" || c.Symbol == "(*Walker).Drive" {
			contract = cdHash([]byte("//@ requires true\n"))
		}
		if c.ContractSHA256 != contract {
			t.Fatalf("selected declaration contract snapshot lost: %#v", c)
		}
		last = c.ID
	}
	if len(inv.Components) != 6 {
		t.Fatalf("production inventory changed: %#v", inv.Components)
	}
	for _, c := range inv.Components {
		if _, ok := want[c.ID]; ok {
			t.Fatalf("selected input became production: %s", c.ID)
		}
	}
	if !slices.Equal(inv.Checks[0].Tests, []string{cdID("example.test/inputs", "outside_test.go", "TestOther"), cdID("example.test/inputs", "outside_test.go", "TestStep")}) {
		t.Fatal("existing selected test IDs changed")
	}
	// Omitted components still require no discovery, even if every source
	// and module needed by explicit discovery has gone away.
	cdManifest(t, dir, nil)
	for _, name := range []string{"core", "drivers", "go.mod", "outside_test.go", ".invariant/specs"} {
		if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	legacy, _ := cdResolve(t, dir)
	if string(cdJSON(t, legacy)) != `{"mode":"legacy"}` {
		t.Fatalf("legacy discovery was added: %s", cdJSON(t, legacy))
	}
}

func TestComponentDeclarationsAllowAssumptionOnlyTargets(t *testing.T) {
	dir, cfg := cdFixture(t)
	check := &cfg.Checks[0]
	check.Proof, check.Tests, check.Conformance, check.Invocations = nil, nil, nil, nil
	cdManifest(t, dir, &cfg)
	raw, inv := cdResolve(t, dir)
	if len(inv.Checks) != 1 || len(inv.Checks[0].Assumptions) != 1 {
		t.Fatal("assumption-only target declaration disappeared")
	}
	a := inv.Checks[0].Assumptions[0]
	if inv.Checks[0].Target != cdID("example.test/inputs/core", "core/core.go", "(*Counter).Step") || a.ID != "serialized" || a.Statement != "Calls are serialized" || a.Reason != "Caller protocol" {
		t.Fatalf("lost assumption identity or explanation: %#v", a)
	}
	cdFiles(t, dir, []project.ComponentFile{a.Source}, []string{"notes/trust.txt"})
	if inv.Provenance == nil {
		t.Fatal("assumption source provenance missing")
	}
	cdFiles(t, dir, inv.Provenance.Files, []string{"notes/trust.txt"})
	if inv.Checks[0].Proof != nil || len(inv.Checks[0].Tests) != 0 || len(inv.Checks[0].Conformance) != 0 {
		t.Fatal("assumption invented an executable selection")
	}
	encoded := string(cdJSON(t, raw))
	for _, key := range []string{`"assurance":`, `"evidence":`, `"passed":`, `"status":`} {
		if strings.Contains(encoded, key) {
			t.Fatalf("configuration acquired result field %s", key)
		}
	}
	check.Assumptions[0].Statement, check.Assumptions[0].Reason = "Different statement", "Different origin"
	cdWrite(t, dir, "notes/trust.txt", "changed source\n")
	cdManifest(t, dir, &cfg)
	_, changed := cdResolve(t, dir)
	b := changed.Checks[0].Assumptions[0]
	if b.ID != a.ID || changed.Checks[0].Target != inv.Checks[0].Target || b.Source.SHA256 == a.Source.SHA256 || b.Statement == a.Statement || b.Reason == a.Reason {
		t.Fatal("assumption identity must stay stable while content snapshots change")
	}
}

func TestComponentDeclarationsRejectInvalidMetadata(t *testing.T) {
	type edit func(*cdConfig)
	cases := []struct {
		name   string
		edit   edit
		source string
	}{
		{"missing assumption id", func(c *cdConfig) { c.Checks[0].Assumptions[0].ID = "" }, ""},
		{"blank assumption id", func(c *cdConfig) { c.Checks[0].Assumptions[0].ID = "  " }, ""},
		{"missing assumption statement", func(c *cdConfig) { c.Checks[0].Assumptions[0].Statement = "" }, ""},
		{"blank assumption statement", func(c *cdConfig) { c.Checks[0].Assumptions[0].Statement = "\n " }, ""},
		{"missing assumption reason", func(c *cdConfig) { c.Checks[0].Assumptions[0].Reason = "" }, ""},
		{"blank assumption reason", func(c *cdConfig) { c.Checks[0].Assumptions[0].Reason = " \n" }, ""},
		{"missing assumption source", func(c *cdConfig) { c.Checks[0].Assumptions[0].Source = "" }, ""},
		{"duplicate assumption id", func(c *cdConfig) {
			a := c.Checks[0].Assumptions[0]
			a.Statement = "other"
			c.Checks[0].Assumptions = append(c.Checks[0].Assumptions, a)
		}, ""},
		{"missing invocation id", func(c *cdConfig) { c.Checks[0].Invocations[0].ID = "" }, ""},
		{"blank invocation id", func(c *cdConfig) { c.Checks[0].Invocations[0].ID = " \t" }, ""},
		{"duplicate invocation id", func(c *cdConfig) {
			v := c.Checks[0].Invocations[0]
			v.Args = []string{"different"}
			c.Checks[0].Invocations = append(c.Checks[0].Invocations, v)
		}, ""},
		{"unknown invocation kind", func(c *cdConfig) { c.Checks[0].Invocations[0].Kind = "runtime-guess" }, ""},
		{"missing invocation kind", func(c *cdConfig) { c.Checks[0].Invocations[0].Kind = "" }, ""},
		{"missing invocation check", func(c *cdConfig) { c.Checks[0].Invocations[0].Check = project.ComponentRef{} }, ""},
		{"unknown invocation check", func(c *cdConfig) { c.Checks[0].Invocations[0].Check.Symbol = "Missing" }, ""},
		{"unselected declaration", func(c *cdConfig) { c.Checks[0].Invocations[0].Check.Symbol = "Drive" }, ""},
		{"wrong check kind", func(c *cdConfig) { c.Checks[0].Invocations[0].Kind = "test" }, ""},
		{"unselected proof", func(c *cdConfig) {
			c.Checks[0].Proof = nil
			c.Checks[0].Invocations[0].Kind = "proof"
			c.Checks[0].Invocations[0].Check = c.Checks[0].Target
		}, ""},
		{"proof invocation for another target", func(c *cdConfig) {
			c.Checks[0].Invocations[0].Kind = "proof"
			c.Checks[0].Invocations[0].Check = cdRef("core/core.go", "Neighbor")
		}, ""},
		{"missing command", func(c *cdConfig) { c.Checks[0].Invocations[0].Command = "" }, ""},
		{"blank command", func(c *cdConfig) { c.Checks[0].Invocations[0].Command = " \t" }, ""},
		{"missing working directory", func(c *cdConfig) { c.Checks[0].Invocations[0].WorkingDir = "" }, ""},
		{"empty environment name", func(c *cdConfig) { c.Checks[0].Invocations[0].Env[""] = "value" }, ""},
		{"blank environment name", func(c *cdConfig) { c.Checks[0].Invocations[0].Env[" \t"] = "value" }, ""},
		{"empty sampling name", func(c *cdConfig) { c.Checks[0].Invocations[0].Sampling[""] = "7" }, ""},
		{"blank sampling name", func(c *cdConfig) { c.Checks[0].Invocations[0].Sampling[" \t"] = "7" }, ""},
		{"duplicate input", func(c *cdConfig) { v := &c.Checks[0].Invocations[0]; v.Inputs = append(v.Inputs, v.Inputs[0]) }, ""},
		{"unknown test", func(c *cdConfig) { c.Checks[0].Tests[0].Symbol = "Missing" }, ""},
		{"unknown conformance", func(c *cdConfig) { c.Checks[0].Conformance[0].Symbol = "Missing" }, ""},
		{"duplicate test", func(c *cdConfig) { c.Checks[0].Tests = append(c.Checks[0].Tests, c.Checks[0].Tests[0]) }, ""},
		{"duplicate conformance", func(c *cdConfig) {
			c.Checks[0].Conformance = append(c.Checks[0].Conformance, c.Checks[0].Conformance[0])
		}, ""},
		{"duplicate target", func(c *cdConfig) { c.Checks = append(c.Checks, c.Checks[0]) }, ""},
		{"empty target declarations", func(c *cdConfig) { c.Checks[0] = cdCheck{Target: c.Checks[0].Target} }, ""},
		{"ambiguous driver", func(c *cdConfig) {}, "package drivers\ntype Walker struct{}\nfunc (w *Walker) Drive() {}\nfunc (w *Walker) Drive() {}\nfunc init() {}\nfunc init() {}\n"},
	}
	for _, tc := range cases {
		dir, cfg := cdFixture(t)
		tc.edit(&cfg)
		if tc.source != "" {
			cdWrite(t, dir, "drivers/driver.go", tc.source)
		}
		cdManifest(t, dir, &cfg)
		if _, err := project.Load(dir); err == nil {
			t.Errorf("%s: project.Load accepted invalid declarations", tc.name)
		}
		if _, err := project.ResolveComponents(cdProject(t, dir, cfg)); err == nil {
			t.Errorf("%s: ResolveComponents accepted invalid declarations", tc.name)
		}
	}
}

func TestComponentDeclarationPathsStayInsideProject(t *testing.T) {
	for _, field := range []string{"assumption", "input", "working_dir"} {
		for _, name := range []string{"", "missing", "absolute", "../escape", "x/../../escape", "./notes/trust.txt", "notes//trust.txt", "notes/../notes/trust.txt", `notes\trust.txt`, "wrong-kind", "symlink-file", "symlink-dir/trust.txt", "inside-file-link", "inside-link", "inside-link/trust.txt"} {
			t.Run(field+"/"+name, func(t *testing.T) {
				dir, cfg := cdFixture(t)
				if err := os.MkdirAll(filepath.Join(dir, "notes/sub"), 0o755); err != nil {
					t.Fatal(err)
				}
				outside := t.TempDir()
				cdWrite(t, outside, "trust.txt", "outside")
				if err := os.Symlink(filepath.Join(outside, "trust.txt"), filepath.Join(dir, "symlink-file")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(dir, "symlink-dir")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(dir, "notes"), filepath.Join(dir, "inside-link")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(dir, "notes/trust.txt"), filepath.Join(dir, "inside-file-link")); err != nil {
					t.Fatal(err)
				}
				value := name
				if name == "absolute" {
					value = filepath.Join(dir, "notes/trust.txt")
				}
				if field == "working_dir" {
					switch name {
					case "absolute":
						value = filepath.Join(dir, "notes")
					case "./notes/trust.txt":
						value = "./notes"
					case "notes//trust.txt":
						value = "notes//sub"
					case "notes/../notes/trust.txt":
						value = "notes/../notes"
					case "symlink-dir/trust.txt":
						value = "symlink-dir"
					case "inside-link/trust.txt":
						value = "inside-link/sub"
					}
				}
				if name == "wrong-kind" {
					value = "notes"
					if field == "working_dir" {
						value = "notes/trust.txt"
					}
				}
				switch field {
				case "assumption":
					cfg.Checks[0].Assumptions[0].Source = value
				case "input":
					cfg.Checks[0].Invocations[0].Inputs = []string{value}
				case "working_dir":
					cfg.Checks[0].Invocations[0].WorkingDir = value
				}
				cdManifest(t, dir, &cfg)
				if _, err := project.Load(dir); err == nil {
					t.Error("load allowed an unsafe path")
				}
				if _, err := project.ResolveComponents(cdProject(t, dir, cfg)); err == nil {
					t.Error("resolution allowed an unsafe path")
				}
			})
		}
	}
}

func TestComponentDeclarationsAreCanonicalAndDetached(t *testing.T) {
	dir, cfg := cdFixture(t)
	// Reusing IDs under other targets/checks is valid, but declaration sets
	// and their file snapshots remain unique. Arguments retain their order.
	first := &cfg.Checks[0]
	first.Assumptions = append(first.Assumptions, cdAssumption{ID: "trusted", Statement: "Dependency follows protocol", Reason: "Owner review", Source: "notes/trust.txt"})
	secondInvocation := first.Invocations[0]
	secondInvocation.ID = "another"
	first.Invocations = append(first.Invocations, secondInvocation)
	for _, kind := range []string{"test", "proof"} {
		v := first.Invocations[0]
		v.Kind = kind
		v.Check = first.Target
		if kind == "test" {
			v.Check = first.Tests[0]
		}
		first.Invocations = append(first.Invocations, v)
	}
	second := cdCheck{Target: cdRef("core/core.go", "Neighbor"), Tests: slices.Clone(first.Tests), Conformance: slices.Clone(first.Conformance), Assumptions: slices.Clone(first.Assumptions), Invocations: []cdInvocation{first.Invocations[0]}}
	cfg.Checks = append(cfg.Checks, second)
	cdManifest(t, dir, &cfg)
	p, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	beforeConfig := string(cdJSON(t, p.Manifest))
	resolved, err := project.ResolveComponents(p)
	if err != nil {
		t.Fatal(err)
	}
	inv := cdDecode(t, resolved)
	if inv.Provenance == nil || len(inv.Checks[0].Invocations) == 0 {
		t.Fatal("new declaration data was discarded")
	}
	if string(cdJSON(t, p.Manifest)) != beforeConfig {
		t.Fatal("resolution reordered caller-owned configuration")
	}
	cdFiles(t, dir, inv.Provenance.Files, []string{"outside_test.go", "drivers/driver.go", "notes/trust.txt", "go.sum", "config.json", "data/fixture.bin", ".invariant/specs/Shared.tla", ".invariant/specs/Counter.tla", ".invariant/ratified.lock"})
	for _, check := range inv.Checks {
		if len(check.Assumptions) != 2 || check.Assumptions[0].ID != "serialized" || check.Assumptions[1].ID != "trusted" {
			t.Fatal("assumptions not canonical")
		}
		previous := ""
		for _, v := range check.Invocations {
			key := v.Kind + "\x00" + v.Check + "\x00" + v.ID
			if key <= previous {
				t.Fatal("invocations not canonical")
			}
			previous = key
			if !slices.Equal(v.Args, []string{"run", "./drivers", "--seed", "7"}) || v.Command != "go" || v.WorkingDir != "." || !reflect.DeepEqual(v.Env, map[string]string{"MODE": "test", "EMPTY": ""}) || !reflect.DeepEqual(v.Sampling, map[string]string{"seed": "7", "count": "3"}) {
				t.Fatalf("invocation changed: %#v", v)
			}
			cdFiles(t, dir, v.Inputs, first.Invocations[0].Inputs)
		}
	}
	want := string(cdJSON(t, resolved))
	slices.Reverse(cfg.Checks)
	for i := range cfg.Checks {
		c := &cfg.Checks[i]
		slices.Reverse(c.Tests)
		slices.Reverse(c.Conformance)
		slices.Reverse(c.Assumptions)
		slices.Reverse(c.Invocations)
		for j := range c.Invocations {
			slices.Reverse(c.Invocations[j].Inputs)
		}
	}
	cdManifest(t, dir, &cfg)
	reordered, _ := cdResolve(t, dir)
	if string(cdJSON(t, reordered)) != want {
		t.Fatal("set order changed resolved declarations")
	}
	relocated, _ := cdFixture(t)
	cdManifest(t, relocated, &cfg)
	moved, _ := cdResolve(t, relocated)
	if string(cdJSON(t, moved)) != want {
		t.Fatal("checkout location entered snapshots")
	}
	// Walk the actual newly added fields without depending on their Go type
	// names: mutating every mutable nested value must leave resolved intact.
	cdMutate(reflect.ValueOf(p.Manifest.Components))
	if string(cdJSON(t, p.Manifest)) == beforeConfig {
		t.Fatal("mutation probe did not change caller configuration")
	}
	if string(cdJSON(t, resolved)) != want {
		t.Fatal("snapshot retained caller-owned slices or maps")
	}
	cdWrite(t, dir, "data/fixture.bin", "edited after resolution")
	if string(cdJSON(t, resolved)) != want {
		t.Fatal("snapshot changed when its source file was edited")
	}
}

func cdMutate(v reflect.Value) {
	if !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			cdMutate(v.Elem())
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			cdMutate(v.Field(i))
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			cdMutate(v.Index(i))
		}
	case reflect.Map:
		if v.Type().Key().Kind() == reflect.String && v.Type().Elem().Kind() == reflect.String {
			for _, key := range v.MapKeys() {
				v.SetMapIndex(key, reflect.ValueOf("mutated").Convert(v.Type().Elem()))
			}
		}
	case reflect.String:
		if v.CanSet() {
			v.SetString("mutated")
		}
	}
}
