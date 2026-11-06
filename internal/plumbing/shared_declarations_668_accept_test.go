package plumbing

// The reusable internal API is analyzeGoDeclarations(file, source), returning
// an analysis and error; analysis.require(name, policy) checks one top-level
// name under goDeclarationLegacy or goDeclarationNative. Both declares and
// acceptanceDeclarations consume it. Analysis is structural: it neither type
// checks the source nor discovers runtime cases. In particular, legacy policy
// preserves the original permissive walker even for code a compiler rejects.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSharedGoDeclarationPolicies668(t *testing.T) {
	const file = "app/ready_accept_test.go"
	for _, tc := range []struct {
		name, source   string
		legacy, native bool
	}{
		{"ordinary", "import \"testing\"\nfunc TestReady(t *testing.T) {}", true, true},
		{"alias", "import check \"testing\"\nfunc TestReady(t *check.T) {}", true, true},
		{"dot import", "import . \"testing\"\nfunc TestReady(t *T) {}", false, true},
		{"unnamed parameter", "import \"testing\"\nfunc TestReady(*testing.T) {}", true, true},
		{"unbound qualifier", "func TestReady(t *other.T) {}", true, false},
		{"wrong import", "import other \"example.invalid/other\"\nfunc TestReady(t *other.T) {}", true, false},
		{"wrong alias", "import check \"testing\"\nfunc TestReady(t *testing.T) {}", true, false},
		{"grouped parameters", "import \"testing\"\nfunc TestReady(a, b *testing.T) {}", true, false},
		{"separate parameters", "import \"testing\"\nfunc TestReady(a *testing.T, b int) {}", false, false},
		{"results", "import \"testing\"\nfunc TestReady(t *testing.T) int { return 0 }", true, false},
		{"type parameters", "import \"testing\"\nfunc TestReady[X any](t *testing.T) {}", true, false},
		{"method only", "import \"testing\"\ntype R int\nfunc (R) TestReady(t *testing.T) {}", false, false},
		{"method and function", "import \"testing\"\ntype R int\nfunc (R) TestReady(t *testing.T) {}\nfunc TestReady(t *testing.T) {}", true, true},
		{"duplicate functions", "import \"testing\"\nfunc TestReady(t *testing.T) {}\nfunc TestReady(t *testing.T) {}", true, false},
		{"invalid duplicate", "import \"testing\"\nfunc TestReady(t *testing.T) {}\nfunc TestReady() {}", true, false},
		{"missing pointer", "import \"testing\"\nfunc TestReady(t testing.T) {}", false, false},
		{"no parameter", "func TestReady() {}", false, false},
		{"wrong testing type", "import \"testing\"\nfunc TestReady(t *testing.B) {}", false, false},
		{"function variable", "import \"testing\"\nvar TestReady = func(t *testing.T) {}", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := "package app\n" + tc.source + "\n"
			analysis, err := analyzeGoDeclarations(file, source)
			if err != nil {
				t.Fatal(err)
			}
			for _, native := range []bool{false, true} {
				policy, want := goDeclarationLegacy, tc.legacy
				test := Test{Name: "TestReady", File: file, Says: "Ready."}
				if native {
					policy, want = goDeclarationNative, tc.native
					test.Name, test.Part, test.Framework, test.Selector = "display label is not a declaration", "app", "go", "TestReady/a.b+[one]/case two"
				}
				if err := analysis.require("TestReady", policy); (err == nil) != want {
					t.Errorf("analysis native=%v: %v, admitted=%v", native, err, want)
				}
				if err := analysis.require("TestMissing", policy); err == nil {
					t.Fatal("missing function admitted")
				}
				p := &Plan{Name: "declarations", Summary: "Preserve declaration policy.", Files: []string{"app/main.go"}, Tests: []Test{test}, Sources: map[string]string{file: source}}
				if err := p.Validate(); (err == nil) != want {
					t.Errorf("Plan.Validate native=%v: %v, admitted=%v", native, err, want)
				}
				if err := acceptanceDeclarations(file, source, []Test{test}); (err == nil) != want {
					t.Errorf("declaration dispatch native=%v: %v", native, err)
				}
				if !native {
					if err := declares(file, source, []Test{test}); (err == nil) != want {
						t.Errorf("legacy caller: %v", err)
					}
				}
			}
		})
	}
	for _, source := range []string{"not Go", "package app\nfunc TestReady("} {
		if _, err := analyzeGoDeclarations(file, source); err == nil {
			t.Fatal("unparseable source admitted")
		}
		for _, native := range []bool{false, true} {
			test := Test{Name: "TestReady", File: file, Says: "Ready."}
			if native {
				test.Part, test.Framework, test.Selector = "app", "go", "TestReady"
			}
			if err := acceptanceDeclarations(file, source, []Test{test}); err == nil {
				t.Fatal("caller bypassed parse error")
			}
		}
	}
	// Shared analysis cannot replace package-wide ambiguity and identity checks.
	source := "package app\nimport \"testing\"\nfunc TestReady(t *testing.T) {}\n"
	p := &Plan{Name: "native", Summary: "Keep identities separate.", Files: []string{"app/main.go"}, Sources: map[string]string{file: source}, Tests: []Test{{Name: "label", File: file, Part: "app", Framework: "go", Selector: "TestReady/one", Says: "One."}}}
	second := p.Tests[0]
	second.Selector = "TestReady/two"
	p.Tests = append(p.Tests, second)
	if err := p.Validate(); err != nil {
		t.Fatal("two cases in one function must remain valid:", err)
	}
	p.Tests[1].File = "app/other_accept_test.go"
	p.Sources[p.Tests[1].File] = source
	if err := p.Validate(); err == nil {
		t.Fatal("same package's two declarations admitted")
	}
	delete(p.Sources, p.Tests[1].File)
	p.Tests[1] = p.Tests[0]
	p.Tests[1].Name = "different display label"
	if err := p.Validate(); err == nil {
		t.Fatal("duplicate full identity admitted")
	}
	p.Tests = p.Tests[:1]
	p.Tests[0].Name, p.Tests[0].Selector = "TestReady", "TestMissing"
	if err := p.Validate(); err == nil {
		t.Fatal("display label substituted for native selector")
	}

	// Pin actual pre-native canonical bytes and verify real committed locks.
	legacy := &Plan{Name: "irrelevant display name", Summary: "Keep the old hash.", Files: []string{"app/main.go"}, Tests: []Test{{Name: "TestReady", File: file, Says: "The behavior holds."}}, Sources: map[string]string{file: strings.Replace(source, "package app", "package fixture", 1)}}
	if err := legacy.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := legacy.Hash(); got != "sha256:38f5e36ab3f3bec9291b281ee1b870b92a389f6070140a657cbf090fac5ce2b2" {
		t.Fatalf("legacy hash changed: %s", got)
	}
	for _, name := range []string{"issue-100.json", "issue-103.json", "issue-484.json"} {
		data, err := os.ReadFile(filepath.Join("..", "..", LockDir, name))
		if err != nil {
			t.Fatal(err)
		}
		lock, err := ReadLock(data)
		if err != nil {
			t.Fatal(err)
		}
		if lock.Plan.Hash() != lock.Ratified.Proposal {
			t.Fatal("committed lock changed")
		}
		encoded, err := json.Marshal(lock)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ReadLock(encoded); err != nil {
			t.Fatal(err)
		}
	}
}
