package verify_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

// These wire types pin the additive JSON contract without making old callers
// depend on new Go type names. Declared inputs are intent: this slice does not
// extend ComponentProofResult or attest that an invocation actually occurred.
type provenance257Assumption struct {
	ID        string                `json:"id"`
	Statement string                `json:"statement"`
	Reason    string                `json:"reason"`
	Source    project.ComponentFile `json:"source"`
}

type provenance257Invocation struct {
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

type provenance257Check struct {
	Target      string                          `json:"target"`
	Proof       *project.ResolvedComponentProof `json:"proof,omitempty"`
	Tests       []string                        `json:"tests,omitempty"`
	Conformance []string                        `json:"conformance,omitempty"`
	Assumptions []provenance257Assumption       `json:"assumptions,omitempty"`
	Invocations []provenance257Invocation       `json:"invocations,omitempty"`
}

type provenance257Inventory struct {
	Mode       string                  `json:"mode"`
	Scope      []string                `json:"scope,omitempty"`
	Files      []project.ComponentFile `json:"files,omitempty"`
	Components []project.Component     `json:"components,omitempty"`
	Checks     []provenance257Check    `json:"checks,omitempty"`
	Provenance *struct {
		Declarations []project.Component     `json:"declarations"`
		Files        []project.ComponentFile `json:"files"`
		Modules      []project.ComponentFile `json:"modules"`
	} `json:"provenance"`
}

func provenance257JSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func provenance257Write(t *testing.T, root, name, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func provenance257Ref(source, symbol string) map[string]any {
	return map[string]any{"source": source, "symbol": symbol}
}

func provenance257ID(source, symbol string) string {
	pkg := "example.test/provenance/" + filepath.ToSlash(filepath.Dir(source))
	if strings.HasPrefix(source, "drivers/") {
		pkg = "example.test/driver"
	}
	return "go:" + pkg + "#" + symbol + "@" + source
}

func provenance257Fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range map[string]string{
		"go.mod":                       "module example.test/provenance\n\ngo 1.27.1\n",
		"go.sum":                       "example.test/dependency v1.0.0 h1:declared-input\n",
		"core/core.go":                 "package core\n//@ ensures result >= 0\nfunc Step() (result int) { return 1 }\nfunc Neighbor() {}\nfunc Assumed() {}\n",
		"core/scoped_driver.go":        "package core\nfunc DriveScoped() {}\n",
		"core/data.go":                 "package core\nconst Limit = 2\n",
		"core/tagged.go":               "//go:build never_enabled\n\npackage core\nfunc Tagged() {}\n",
		"core/child/ignored.go":        "package child\nfunc OutsideNonrecursiveScope() {}\n",
		"neighbors/extra.go":           "package neighbors\nfunc Unchecked() {}\n",
		"tests/step_test.go":           "package tests\nimport \"testing\"\nfunc TestStep(t *testing.T) {}\nfunc TestAgain(t *testing.T) {}\n",
		"drivers/go.mod":               "module example.test/driver\n\ngo 1.27.1\n",
		"drivers/drive.go":             "package drivers\nfunc Drive() {}\nfunc UnselectedDriverNeighbor() {}\n",
		"assumptions.md":               "The input clock is monotonic.\n",
		"policy.txt":                   "The environment supplies valid input.\n",
		"data/fixture.bin":             "\x00fixture\r\n\xff",
		"data/second.bin":              "second fixture\n",
		"config/check.json":            "{\"limit\":2}\n",
		".invariant/ratified.lock":     `{"bounds":{"Limit":"2"},"statements":[{"name":"Spec","kind":"spec"},{"name":"Safe","kind":"invariant"}]}`,
		".invariant/specs/Counter.tla": "---- MODULE Counter ----\nEXTENDS Naturals\nCONSTANT Limit\nVARIABLE n\nInit == n = 0\nNext == n' = n\nSpec == Init /\\ [][Next]_n\nSafe == n >= 0\n====\n",
		".invariant/specs/Include.tla": "---- MODULE Include ----\nHelper == 1\n====\n",
	} {
		provenance257Write(t, root, name, body)
	}
	step := provenance257Ref("core/core.go", "Step")
	test := provenance257Ref("tests/step_test.go", "TestStep")
	driver := provenance257Ref("drivers/drive.go", "Drive")
	assumption := func(id, statement, source string) map[string]any {
		return map[string]any{"id": id, "statement": statement, "reason": "Explicit environment policy.", "source": source}
	}
	invocation := func(kind string, check map[string]any, inputs ...string) map[string]any {
		return map[string]any{"id": "run", "kind": kind, "check": check,
			"command": "provenance-command", "args": []string{"z-first", "a-second"}, "working_dir": ".",
			"env":      map[string]string{"MODE": "strict", "OTHER": "kept"},
			"sampling": map[string]string{"seed": "17", "runs": "3"}, "inputs": inputs}
	}
	manifest := map[string]any{"name": "component provenance", "language": "go", "code": "core", "module": ".invariant/specs/Counter.tla",
		"components": map[string]any{"scope": []string{"neighbors", "core"}, "checks": []any{
			map[string]any{"target": step, "proof": map[string]any{"contracts": []any{step}},
				"tests":       []any{test, provenance257Ref("tests/step_test.go", "TestAgain")},
				"conformance": []any{driver, provenance257Ref("core/scoped_driver.go", "DriveScoped")},
				"assumptions": []any{assumption("clock", "Clock is monotonic.", "assumptions.md"), assumption("valid", "Input is valid.", "policy.txt")},
				"invocations": []any{
					invocation("proof", step, "go.sum", "go.mod", "config/check.json"),
					invocation("test", test, "data/fixture.bin", "config/check.json"),
					invocation("conformance", driver, ".invariant/specs/Counter.tla", ".invariant/specs/Include.tla", ".invariant/ratified.lock"),
				}},
			map[string]any{"target": provenance257Ref("core/core.go", "Assumed"), "tests": []any{test},
				"assumptions": []any{assumption("clock", "Clock is monotonic.", "assumptions.md")}},
		}}}
	provenance257Write(t, root, ".invariant/invariant.json", string(provenance257JSON(t, manifest)))
	return root
}

func provenance257View(t *testing.T, inv any) provenance257Inventory {
	t.Helper()
	var out provenance257Inventory
	if err := json.Unmarshal(provenance257JSON(t, inv), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func provenance257Require(t *testing.T, inv *project.ComponentInventory) provenance257Inventory {
	t.Helper()
	wire := provenance257View(t, inv)
	if wire.Provenance == nil || len(wire.Provenance.Declarations) != 4 || len(wire.Provenance.Modules) != 2 {
		t.Fatalf("selected declarations and nearest-module snapshots were discarded: %s", provenance257JSON(t, inv))
	}
	check := provenance257CheckFor(t, wire, "Step")
	if len(check.Assumptions) != 2 || len(check.Invocations) != 3 {
		t.Fatalf("resolved assumptions/invocations were discarded: %#v", check)
	}
	return wire
}

func provenance257Resolve(t *testing.T, root string) *project.ComponentInventory {
	t.Helper()
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	inv, err := project.ResolveComponents(p)
	if err != nil {
		t.Fatal(err)
	}
	provenance257Require(t, inv)
	return inv
}

func provenance257CheckFor(t *testing.T, inv provenance257Inventory, symbol string) provenance257Check {
	t.Helper()
	for _, check := range inv.Checks {
		if check.Target == provenance257ID("core/core.go", symbol) {
			return check
		}
	}
	t.Fatalf("missing target %s", symbol)
	return provenance257Check{}
}

func provenance257Report(t *testing.T, inv *project.ComponentInventory, results []verify.ComponentProofResult) verify.Report {
	t.Helper()
	r := verify.Report{Project: "component provenance", Passed: true, Assurance: "proved", Bounds: map[string]string{"Limit": "2"},
		Code:  &verify.Code{Verifier: "Gobra", Passed: true, Functions: []string{"Step"}, Unverified: []string{"Neighbor"}},
		Build: verify.Build{Passed: true}, Toolchain: verify.Toolchain{GobraImage: "gobra@sha256:" + strings.Repeat("a", 64)}}
	if err := r.AttributeComponentProofs(inv, results); err != nil {
		t.Fatal(err)
	}
	if r.Components == nil || !reflect.DeepEqual(provenance257View(t, &r.Components.Inventory), provenance257View(t, inv)) {
		t.Fatal("proof attribution lost or changed declaration/input provenance")
	}
	if r.Fingerprint != verify.Fingerprint(r) {
		t.Fatal("attribution did not bind its copied evidence")
	}
	return r
}

func provenance257Snapshot(t *testing.T, inv provenance257Inventory, file string) string {
	t.Helper()
	for _, files := range [][]project.ComponentFile{inv.Provenance.Files, inv.Provenance.Modules} {
		for _, f := range files {
			if f.Path == file {
				return f.SHA256
			}
		}
	}
	t.Fatalf("missing input snapshot for %s", file)
	return ""
}

func provenance257EditManifest(t *testing.T, root string, edit func(map[string]any)) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, ".invariant/invariant.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	edit(m)
	provenance257Write(t, root, ".invariant/invariant.json", string(provenance257JSON(t, m)))
}

func provenance257ConfigCheck(m map[string]any, symbol string) map[string]any {
	for _, v := range m["components"].(map[string]any)["checks"].([]any) {
		c := v.(map[string]any)
		if c["target"].(map[string]any)["symbol"] == symbol {
			return c
		}
	}
	panic("fixture target missing")
}

func TestComponentProvenance257BindsEveryDeclaredInputByte(t *testing.T) {
	root := provenance257Fixture(t)
	inv := provenance257Resolve(t, root)
	wire := provenance257Require(t, inv)
	want := provenance257Report(t, inv, nil).Fingerprint
	for _, f := range append(slices.Clone(wire.Provenance.Files), wire.Provenance.Modules...) {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f.Path)))
		if err != nil || f.SHA256 != fmt.Sprintf("sha256:%x", sha256.Sum256(b)) {
			t.Fatalf("input %s did not retain its exact bytes: %#v, %v", f.Path, f, err)
		}
	}
	for _, file := range []string{"drivers/drive.go", "tests/step_test.go", "data/fixture.bin", ".invariant/specs/Counter.tla",
		".invariant/specs/Include.tla", ".invariant/ratified.lock", "go.mod", "drivers/go.mod", "go.sum", "config/check.json", "assumptions.md"} {
		t.Run(file, func(t *testing.T) {
			path := filepath.Join(root, filepath.FromSlash(file))
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			changed := string(original) + "\n"
			if file == ".invariant/ratified.lock" {
				changed = strings.Replace(string(original), `"Limit":"2"`, `"Limit":"3"`, 1)
			}
			if strings.HasSuffix(file, "go.mod") {
				changed = strings.Replace(string(original), "go 1.27.1", "go 1.27.2", 1)
			}
			provenance257Write(t, root, file, changed)
			t.Cleanup(func() { provenance257Write(t, root, file, string(original)) })
			next := provenance257Resolve(t, root)
			nextWire := provenance257Require(t, next)
			wantHash := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(changed)))
			if provenance257Snapshot(t, nextWire, file) != wantHash {
				t.Fatal("changed input snapshot is not its complete byte hash")
			}
			for _, check := range nextWire.Checks {
				for _, assumption := range check.Assumptions {
					if assumption.Source.Path == file && assumption.Source.SHA256 != wantHash {
						t.Fatal("assumption kept a stale source snapshot")
					}
				}
				for _, invocation := range check.Invocations {
					for _, input := range invocation.Inputs {
						if input.Path == file && input.SHA256 != wantHash {
							t.Fatal("invocation kept a stale declared input snapshot")
						}
					}
				}
			}
			if provenance257Snapshot(t, nextWire, file) == provenance257Snapshot(t, wire, file) || provenance257Report(t, next, nil).Fingerprint == want {
				t.Fatal("changed declared input bytes did not change snapshot and fingerprint")
			}
			for i, c := range inv.Components {
				if next.Components[i].ID != c.ID {
					t.Fatal("content change renamed a production identity")
				}
			}
			for i, c := range wire.Provenance.Declarations {
				if nextWire.Provenance.Declarations[i].ID != c.ID {
					t.Fatal("content change renamed a selected identity")
				}
				if c.Source == file && nextWire.Provenance.Declarations[i].SourceSHA256 == c.SourceSHA256 {
					t.Fatal("selected declaration retained a stale source snapshot")
				}
			}
		})
	}
}

func TestComponentProvenance257BindsDeclarationsAndOrderedInvocationData(t *testing.T) {
	root := provenance257Fixture(t)
	before := provenance257Resolve(t, root)
	want := provenance257Report(t, before, nil).Fingerprint
	original, err := os.ReadFile(filepath.Join(root, ".invariant/invariant.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
	}{
		{"assumption identity", func(c map[string]any) { c["assumptions"].([]any)[0].(map[string]any)["id"] = "different-clock" }},
		{"assumption statement", func(c map[string]any) {
			c["assumptions"].([]any)[0].(map[string]any)["statement"] = "Clock is strictly monotonic."
		}},
		{"assumption reason", func(c map[string]any) {
			c["assumptions"].([]any)[0].(map[string]any)["reason"] = "Ratified environment constraint."
		}},
		{"assumption source", func(c map[string]any) { c["assumptions"].([]any)[0].(map[string]any)["source"] = "policy.txt" }},
		{"invocation identity", func(c map[string]any) { c["invocations"].([]any)[0].(map[string]any)["id"] = "another-run" }},
		{"command", func(c map[string]any) { c["invocations"].([]any)[0].(map[string]any)["command"] = "another-command" }},
		{"argument order", func(c map[string]any) { slices.Reverse(c["invocations"].([]any)[0].(map[string]any)["args"].([]any)) }},
		{"working directory", func(c map[string]any) { c["invocations"].([]any)[0].(map[string]any)["working_dir"] = "core" }},
		{"explicit environment", func(c map[string]any) {
			c["invocations"].([]any)[0].(map[string]any)["env"].(map[string]any)["MODE"] = "relaxed"
		}},
		{"sampling", func(c map[string]any) {
			c["invocations"].([]any)[0].(map[string]any)["sampling"].(map[string]any)["seed"] = "19"
		}},
		{"input association", func(c map[string]any) {
			c["invocations"].([]any)[1].(map[string]any)["inputs"] = []string{"data/second.bin", "config/check.json"}
		}},
		{"input reassignment with unchanged file set", func(c map[string]any) {
			proof := c["invocations"].([]any)[0].(map[string]any)
			test := c["invocations"].([]any)[1].(map[string]any)
			proof["inputs"], test["inputs"] = test["inputs"], proof["inputs"]
		}},
		{"selected check association", func(c map[string]any) {
			c["invocations"].([]any)[1].(map[string]any)["check"] = provenance257Ref("tests/step_test.go", "TestAgain")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provenance257Write(t, root, ".invariant/invariant.json", string(original))
			provenance257EditManifest(t, root, func(m map[string]any) { tc.edit(provenance257ConfigCheck(m, "Step")) })
			if provenance257Report(t, provenance257Resolve(t, root), nil).Fingerprint == want {
				t.Fatal("declarative invocation or assumption change was omitted from fingerprint")
			}
		})
	}
}

func TestComponentProvenance257CanonicalOrderRelocationAndVolatileExclusions(t *testing.T) {
	root, relocated := provenance257Fixture(t), provenance257Fixture(t)
	inv := provenance257Resolve(t, root)
	r := provenance257Report(t, inv, nil)
	provenance257EditManifest(t, relocated, func(m map[string]any) {
		cfg := m["components"].(map[string]any)
		slices.Reverse(cfg["scope"].([]any))
		slices.Reverse(cfg["checks"].([]any))
		for _, value := range cfg["checks"].([]any) {
			check := value.(map[string]any)
			for _, key := range []string{"tests", "conformance", "assumptions", "invocations"} {
				if values, ok := check[key].([]any); ok {
					slices.Reverse(values)
				}
			}
			if calls, ok := check["invocations"].([]any); ok {
				for _, call := range calls {
					slices.Reverse(call.(map[string]any)["inputs"].([]any))
				}
			}
		}
	})
	other := provenance257Resolve(t, relocated)
	if !reflect.DeepEqual(inv, other) || provenance257Report(t, other, nil).Fingerprint != r.Fingerprint {
		t.Fatal("declaration order or checkout location changed canonical snapshots/fingerprint")
	}
	wire := provenance257Require(t, inv)
	slices.Reverse(wire.Scope)
	slices.Reverse(wire.Files)
	slices.Reverse(wire.Components)
	slices.Reverse(wire.Checks)
	slices.Reverse(wire.Provenance.Declarations)
	slices.Reverse(wire.Provenance.Files)
	slices.Reverse(wire.Provenance.Modules)
	for i := range wire.Checks {
		c := &wire.Checks[i]
		slices.Reverse(c.Tests)
		slices.Reverse(c.Conformance)
		slices.Reverse(c.Assumptions)
		slices.Reverse(c.Invocations)
		for j := range c.Invocations {
			slices.Reverse(c.Invocations[j].Inputs)
		}
	}
	var permuted project.ComponentInventory
	if err := json.Unmarshal(provenance257JSON(t, wire), &permuted); err != nil {
		t.Fatal(err)
	}
	saved := string(provenance257JSON(t, permuted))
	copyReport := r
	copyEvidence := *r.Components
	copyEvidence.Inventory = permuted
	copyReport.Components = &copyEvidence
	if verify.Fingerprint(copyReport) != r.Fingerprint {
		t.Fatal("Fingerprint failed to canonicalize caller-supplied provenance collections")
	}
	var attached verify.Report
	attached = verify.Report{Project: r.Project, Passed: r.Passed, Bounds: r.Bounds, Code: r.Code, Build: r.Build, Toolchain: r.Toolchain}
	if err := attached.AttributeComponentProofs(&permuted, nil); err != nil {
		t.Fatal(err)
	}
	if attached.Fingerprint != r.Fingerprint || string(provenance257JSON(t, permuted)) != saved {
		t.Fatal("attribution failed to canonicalize provenance without mutating its caller")
	}
	copyReport.GeneratedAt, copyReport.Agent, copyReport.Build.Output, copyReport.Toolchain.Go = "tomorrow", "another agent", "new timings", "go1.27.99"
	copyReport.Bugs = []verify.Bug{{Name: "Bug", Trace: "/first/trace"}}
	copyReport.Properties = []verify.Property{{Name: "Live", Trace: "/first/property"}}
	first := verify.Fingerprint(copyReport)
	copyReport.GeneratedAt, copyReport.Agent, copyReport.Build.Output, copyReport.Toolchain.Go = "later", "different agent", "different timings", "go1.27.100"
	copyReport.Bugs[0].Trace, copyReport.Properties[0].Trace = "/relocated/trace", "/relocated/property"
	if verify.Fingerprint(copyReport) != first {
		t.Fatal("volatile metadata entered the component provenance fingerprint")
	}
}

// Mutate all strings recursively, including values in newly introduced maps
// and nested slices. Reflection lets this black-box test use additive fields
// while compiling against the old API on which it must fail behaviorally.
func provenance257Mutate(v reflect.Value) {
	if !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			provenance257Mutate(v.Elem())
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			provenance257Mutate(v.Field(i))
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			provenance257Mutate(v.Index(i))
		}
	case reflect.Map:
		if !v.IsNil() && v.Type().Key().Kind() == reflect.String && v.Type().Elem().Kind() == reflect.String {
			for _, key := range v.MapKeys() {
				v.SetMapIndex(key, reflect.ValueOf("changed").Convert(v.Type().Elem()))
			}
		}
	case reflect.String:
		if v.CanSet() {
			v.SetString("changed")
		}
	}
}

func TestComponentProvenance257OwnsAllMutableDeclarationsAndSnapshots(t *testing.T) {
	root := provenance257Fixture(t)
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	inv, err := project.ResolveComponents(p)
	if err != nil {
		t.Fatal(err)
	}
	provenance257Require(t, inv)
	original := string(provenance257JSON(t, inv))
	provenance257Mutate(reflect.ValueOf(p.Manifest.Components))
	if string(provenance257JSON(t, inv)) != original {
		t.Fatal("resolved declarations retained caller-owned configuration maps or slices")
	}
	r := provenance257Report(t, inv, nil)
	saved := string(provenance257JSON(t, r))
	provenance257Mutate(reflect.ValueOf(inv))
	if string(provenance257JSON(t, r)) != saved || verify.Fingerprint(r) != r.Fingerprint {
		t.Fatal("proof evidence retained mutable caller-owned declarations, invocations or snapshots")
	}
}

func TestComponentProvenance257AssumptionsAndDriversNeverInheritProof(t *testing.T) {
	root := provenance257Fixture(t)
	initial := provenance257Resolve(t, root)
	provenance257EditManifest(t, root, func(m map[string]any) { delete(provenance257ConfigCheck(m, "Assumed"), "tests") })
	inv := provenance257Resolve(t, root)
	wire := provenance257Require(t, inv)
	if !reflect.DeepEqual(initial.Files, inv.Files) || !reflect.DeepEqual(initial.Components, inv.Components) {
		t.Fatal("assumption-only configuration changed the production inventory")
	}
	assumed := provenance257CheckFor(t, wire, "Assumed")
	if assumed.Proof != nil || len(assumed.Tests) != 0 || len(assumed.Conformance) != 0 || len(assumed.Assumptions) != 1 {
		t.Fatalf("assumption-only target acquired a check: %#v", assumed)
	}
	step := provenance257ID("core/core.go", "Step")
	var input project.Component
	for _, c := range inv.Components {
		if c.ID == step {
			input = c
		}
		if strings.HasPrefix(c.Source, "drivers/") || strings.HasPrefix(c.Source, "tests/") || strings.Contains(c.Source, "/child/") {
			t.Fatalf("selected declaration escaped its separate provenance into production coverage: %#v", c)
		}
	}
	for _, f := range inv.Files {
		if strings.HasPrefix(f.Path, "drivers/") || strings.HasPrefix(f.Path, "tests/") {
			t.Fatalf("selected file inflated production scope: %#v", f)
		}
	}
	result := verify.ComponentProofResult{Target: step, Verifier: "supplied verifier result", Passed: true,
		Inputs: []project.Component{input}, Verified: []string{step}, Contracts: []string{step}}
	r := provenance257Report(t, inv, []verify.ComponentProofResult{result})
	if len(r.Components.Proofs) != len(inv.Components) || len(inv.Components) != 6 || !r.Passed || r.CodeAssurance() != "component evidence" || r.EverySize() {
		t.Fatal("declarations changed the gate or inflated proof coverage")
	}
	for _, proof := range r.Components.Proofs {
		want := "unchecked"
		if proof.Target == step {
			want = "proved"
		}
		if proof.Status != want || proof.Target != step && proof.Result != nil {
			t.Fatalf("an assumption, selection or neighboring driver inherited proof: %#v", proof)
		}
	}
	for _, c := range wire.Provenance.Declarations {
		if c.ID == provenance257ID("core/scoped_driver.go", "DriveScoped") && !slices.ContainsFunc(inv.Components, func(p project.Component) bool { return p == c }) {
			t.Fatal("independently scoped driver lost its ordinary production snapshot")
		}
	}
	if !reflect.DeepEqual(r.Code.Unverified, []string{"Neighbor"}) {
		t.Fatal("provenance rewrote legacy unverified-function data")
	}
}

func TestComponentProvenance257InvocationMetadataRemainsInert(t *testing.T) {
	root := provenance257Fixture(t)
	provenance257Resolve(t, root)
	bin := t.TempDir()
	log := filepath.Join(bin, "called")
	t.Setenv("PROVENANCE_257_TOOL_LOG", log)
	for _, name := range []string{"go", "docker", "provenance-command"} {
		provenance257Write(t, bin, name, "#!/bin/sh\necho called >> \"$PROVENANCE_257_TOOL_LOG\"\nexit 73\n")
		if err := os.Chmod(filepath.Join(bin, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	provenance257EditManifest(t, root, func(m map[string]any) {
		for _, value := range provenance257ConfigCheck(m, "Step")["invocations"].([]any) {
			if value.(map[string]any)["kind"] == "proof" {
				value.(map[string]any)["command"] = "provenance-command; provenance-command"
			}
			value.(map[string]any)["args"] = []string{"$(provenance-command)", "; provenance-command", "`provenance-command`"}
			value.(map[string]any)["env"] = map[string]string{"EXPANSION": "$(provenance-command)"}
		}
	})
	inv := provenance257Resolve(t, root)
	for _, invocation := range provenance257CheckFor(t, provenance257Require(t, inv), "Step").Invocations {
		wantCommand := "provenance-command"
		if invocation.Kind == "proof" {
			wantCommand = "provenance-command; provenance-command"
		}
		if invocation.Command != wantCommand ||
			!slices.Equal(invocation.Args, []string{"$(provenance-command)", "; provenance-command", "`provenance-command`"}) ||
			invocation.Env["EXPANSION"] != "$(provenance-command)" {
			t.Fatal("opaque invocation metadata was interpreted or rewritten")
		}
	}
	provenance257Report(t, inv, nil)
	if err := verify.Check(root); !errors.Is(err, verify.ErrComponents) {
		t.Fatalf("explicit preflight no longer rejects execution: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if r, err := verify.Run(ctx, root, "", toolchain.Toolchain{}); r != nil || !errors.Is(err, verify.ErrComponents) {
		t.Fatalf("explicit invocation metadata entered an executor: %#v, %v", r, err)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("loading, resolving, attribution or rejected verification executed a command")
	}
	model, err := verify.RunModel(ctx, root, toolchain.Toolchain{})
	if err != nil || model == nil || !model.ModelOnly {
		t.Fatalf("declarations disabled model-only checking: %#v, %v", model, err)
	}
	if _, err := os.Stat(log); err != nil {
		t.Fatalf("stub model tool was not reached: %v", err)
	}
	for _, language := range []string{"python", "typescript"} {
		provenance257EditManifest(t, root, func(m map[string]any) { m["language"] = language })
		if _, err := project.Load(root); err == nil || !strings.Contains(strings.ToLower(err.Error()), language) {
			t.Fatalf("unsupported explicit %s entered a legacy path: %v", language, err)
		}
	}
}
