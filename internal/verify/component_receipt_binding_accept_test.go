package verify_test

import (
	"encoding/json"
	"html"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/verify"
)

// Earlier acceptance fixtures describe real selected declarations and input
// snapshots. Convert their wire builders to the shipped public result types;
// these tests exercise only the supplied-data APIs, never a runner.
func cb296Typed[T any](t *testing.T, value any) T {
	t.Helper()
	var result T
	if err := json.Unmarshal(cpJSON(t, value), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func cb296All(t *testing.T, f ce274FixtureData, reverse bool) verify.Report {
	t.Helper()
	r := ce274Seed()
	r.Passed = false // Successful supplied evidence must preserve a failing gate.
	proof := cb296Typed[verify.ComponentProofResult](t, ce274ProofFor(t, f, f.Target, true))
	assumptions := cb296Typed[[]verify.ComponentAssumptionRecord](t, ce274Records(t, f))
	conformance := cb296Typed[verify.ComponentConformanceResult](t, cc279Good(t, f, f.Target, "", ""))
	conformance.Diagnostics = []string{"receipt binding conformance detail"}
	conformance.Mode = "bounded-exhaustive"
	conformance.Attempts = cb296Typed[*verify.ComponentAttemptResult](t, cc279AttemptGood())
	runtime := cb296Typed[verify.ComponentRuntimeResult](t, cri287Rich(t, f))
	runtime.Tests[0].Assertions[0].ID = "receipt-binding-assertion"
	if reverse {
		slices.Reverse(proof.Inputs)
		slices.Reverse(proof.Verified)
		slices.Reverse(proof.Contracts)
		slices.Reverse(proof.FileInputs)
		slices.Reverse(proof.Invocation.Inputs)
		slices.Reverse(assumptions)
		slices.Reverse(conformance.Inputs)
		slices.Reverse(conformance.FileInputs)
		slices.Reverse(conformance.Checked)
		slices.Reverse(conformance.Invocation.Inputs)
		slices.Reverse(conformance.Model.Inputs)
		slices.Reverse(conformance.Bounds.Sources)
		slices.Reverse(runtime.Inputs)
		slices.Reverse(runtime.FileInputs)
		slices.Reverse(runtime.Invocation.Inputs)
		slices.Reverse(runtime.Model.Inputs)
		slices.Reverse(runtime.Bounds.Sources)
		slices.Reverse(runtime.Tests)
		for i := range runtime.Tests {
			slices.Reverse(runtime.Tests[i].Assertions)
		}
	}
	if err := r.AttributeComponentProofs(f.Inv, []verify.ComponentProofResult{proof}); err != nil {
		t.Fatal(err)
	}
	if err := r.AttributeComponentAssumptions(f.Inv, assumptions); err != nil {
		t.Fatal(err)
	}
	if err := r.AttributeComponentConformance(f.Inv, []verify.ComponentConformanceResult{conformance}); err != nil {
		t.Fatal(err)
	}
	if err := r.AttributeComponentRuntime(f.Inv, []verify.ComponentRuntimeResult{runtime}); err != nil {
		t.Fatal(err)
	}
	return r
}

func cb296Render(t *testing.T, r *verify.Report) string {
	t.Helper()
	before, fingerprint, passed := string(cpJSON(t, r)), r.Fingerprint, r.Passed
	markdown := receipt.Markdown(r)
	if string(cpJSON(t, r)) != before || r.Fingerprint != fingerprint || verify.Fingerprint(*r) != fingerprint || r.Passed != passed {
		t.Fatal("rendering changed supplied report values, fingerprint or gate result")
	}
	decoded := cb296Typed[verify.Report](t, r)
	if string(cpJSON(t, decoded)) != before || receipt.Markdown(&decoded) != markdown || verify.Fingerprint(decoded) != fingerprint {
		t.Fatal("JSON round trip changed evidence, fingerprint or Markdown")
	}
	decoded.Assurance = "proved"
	if decoded.CodeAssurance() != "component evidence" || decoded.Claim() != "component evidence" || decoded.EverySize() || verify.Fingerprint(decoded) != fingerprint {
		t.Fatal("stale aggregate assurance label inflated explicit component claims")
	}
	return markdown
}

func cb296Visible(t *testing.T, markdown string, values ...string) {
	t.Helper()
	// Escaped underscores in source paths must be compared as visible text.
	visible := html.UnescapeString(markdown)
	for _, value := range values {
		if !strings.Contains(visible, value) {
			t.Errorf("receipt omits supplied evidence detail %q", value)
		}
	}
}

func cb296Short(hash string) string {
	if digest, ok := strings.CutPrefix(hash, "sha256:"); ok && len(digest) > 12 {
		return "sha256:" + digest[:12]
	}
	return hash
}

func TestComponentReceiptBindsInputsAcrossEditsOrderAndRelocation(t *testing.T) {
	f := cr287Fixture(t)
	r := cb296All(t, f, false)
	markdown := cb296Render(t, &r)
	cb296Visible(t, markdown, "Clock is monotonic.", "receipt binding conformance detail", "receipt-binding-assertion")
	files := []string{"drivers/drive.go", "tests/step_test.go", "data/fixture.bin", "data/second.bin", "config/check.json",
		".invariant/specs/Counter.tla", ".invariant/specs/Include.tla", ".invariant/ratified.lock", "go.mod", "drivers/go.mod", "go.sum", "assumptions.md"}
	for _, path := range files {
		file := ce274File(t, f.Inv, path)
		cb296Visible(t, markdown, path, cb296Short(file.SHA256))
	}
	for _, path := range files {
		t.Run(path, func(t *testing.T) {
			full := filepath.Join(f.Root, filepath.FromSlash(path))
			original, err := os.ReadFile(full)
			if err != nil {
				t.Fatal(err)
			}
			// Whitespace changes complete input bytes while preserving identities,
			// valid manifest/model/lock syntax and ratified bounds.
			provenance257Write(t, f.Root, path, string(original)+"\n")
			defer provenance257Write(t, f.Root, path, string(original))
			next := f
			next.Inv = cpResolve(t, f.Root)
			changed := cb296All(t, next, false)
			if changed.Fingerprint == r.Fingerprint || ce274File(t, next.Inv, path).SHA256 == ce274File(t, f.Inv, path).SHA256 {
				t.Fatal("changed execution input bytes did not change snapshot and fingerprint")
			}
			if path == "drivers/drive.go" && (string(cpJSON(t, next.Inv.Components)) != string(cpJSON(t, f.Inv.Components)) || string(cpJSON(t, next.Inv.Files)) != string(cpJSON(t, f.Inv.Files))) {
				t.Fatal("outside-scope driver edit changed production inventory")
			}
			cb296Visible(t, cb296Render(t, &changed), path, cb296Short(ce274File(t, next.Inv, path).SHA256), "receipt-binding-assertion")
		})
	}
	relocated := cr287Fixture(t)
	if relocated.Root == f.Root {
		t.Fatal("fixture was not relocated")
	}
	ce274ReverseInventory(relocated.Inv)
	permuted := cb296All(t, relocated, true)
	if string(cpJSON(t, permuted)) != string(cpJSON(t, r)) || permuted.Fingerprint != r.Fingerprint || cb296Render(t, &permuted) != markdown {
		t.Fatal("equivalent set ordering or checkout relocation changed report, fingerprint or receipt")
	}
}

func TestComponentReceiptPreservesIndependentUpdatesAndRejectsStaleKinds(t *testing.T) {
	f := cr287Fixture(t)
	base := cb296All(t, f, false)
	cb296Visible(t, cb296Render(t, &base), "receipt binding conformance detail", "receipt-binding-assertion", "Clock is monotonic.")
	kinds := []string{"proofs", "assumptions", "conformance", "runtime"}
	for _, kind := range kinds {
		for _, clear := range []bool{false, true} {
			name := kind + "/replace"
			if clear {
				name = kind + "/clear"
			}
			t.Run(name, func(t *testing.T) {
				r := cpClone(t, base)
				var err error
				switch kind {
				case "proofs":
					var results []verify.ComponentProofResult
					if !clear {
						results = []verify.ComponentProofResult{cb296Typed[verify.ComponentProofResult](t, ce274ProofFor(t, f, f.Other, true))}
					}
					err = r.AttributeComponentProofs(f.Inv, results)
				case "assumptions":
					var records []verify.ComponentAssumptionRecord
					if !clear {
						records = []verify.ComponentAssumptionRecord{base.Components.Assumptions[0].Record}
					}
					err = r.AttributeComponentAssumptions(f.Inv, records)
				case "conformance":
					var results []verify.ComponentConformanceResult
					if !clear {
						result := cpClone(t, base.Components.Conformance[0].Result)
						result.Diagnostics = []string{"replacement conformance detail"}
						results = []verify.ComponentConformanceResult{result}
					}
					err = r.AttributeComponentConformance(f.Inv, results)
				case "runtime":
					var results []verify.ComponentRuntimeResult
					if !clear {
						result := cpClone(t, base.Components.Runtime[0].Result)
						result.Diagnostics = []string{"replacement runtime detail"}
						results = []verify.ComponentRuntimeResult{result}
					}
					err = r.AttributeComponentRuntime(f.Inv, results)
				}
				if err != nil {
					t.Fatal(err)
				}
				for _, other := range kinds {
					if other != kind && cct279KindJSON(t, r, other) != cct279KindJSON(t, base, other) {
						t.Fatalf("%s changed retained %s", kind, other)
					}
				}
				if ce274Legacy(t, r) != ce274Legacy(t, base) || r.Passed {
					t.Fatal("component update changed legacy checks, ratification or failing gate")
				}
				markdown := cb296Render(t, &r)
				if kind != "runtime" {
					cb296Visible(t, markdown, "receipt-binding-assertion")
				}
				if kind != "conformance" {
					cb296Visible(t, markdown, "receipt binding conformance detail")
				}
				if !clear && kind == "conformance" {
					cb296Visible(t, markdown, "replacement conformance detail")
				}
				if !clear && kind == "runtime" {
					cb296Visible(t, markdown, "replacement runtime detail")
				}
			})
		}
	}
	// Re-resolve a real outside-scope edit. Every API must reject a stale
	// retained kind even when its own requested operation is only a clear.
	path := "drivers/drive.go"
	provenance257Write(t, f.Root, path, "package drivers\nfunc Drive() {}\nfunc UnselectedDriverNeighbor() {}\n// changed driver bytes\n")
	next := cpResolve(t, f.Root)
	if string(cpJSON(t, next.Components)) != string(cpJSON(t, f.Inv.Components)) || string(cpJSON(t, next.Files)) != string(cpJSON(t, f.Inv.Files)) {
		t.Fatal("outside-scope edit changed production coverage inventory")
	}
	for _, kind := range kinds {
		t.Run("stale/"+kind, func(t *testing.T) {
			r := cpClone(t, base)
			before := cb296Render(t, &r)
			apply := func(report *verify.Report) error {
				switch kind {
				case "proofs":
					return report.AttributeComponentProofs(next, nil)
				case "assumptions":
					return report.AttributeComponentAssumptions(next, nil)
				case "conformance":
					return report.AttributeComponentConformance(next, nil)
				default:
					return report.AttributeComponentRuntime(next, nil)
				}
			}
			probe := ce274Seed()
			if err := apply(&probe); err != nil {
				t.Fatalf("replacement inventory is independently invalid: %v", err)
			}
			ce274Unchanged(t, &r, func() error { return apply(&r) })
			if cb296Render(t, &r) != before {
				t.Fatal("rejected stale cross-kind update changed the rendered evidence")
			}
			cb296Visible(t, cb296Render(t, &r), "receipt binding conformance detail", "receipt-binding-assertion", "Clock is monotonic.")
		})
	}
}
