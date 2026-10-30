package receipt_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/verify"
)

// An explicit receipt names every component and its proof coverage, including
// unchecked neighbors. Legacy package-wide results cannot widen those claims;
// reports without explicit components retain their existing receipt bytes.
func TestComponentProofReceiptsShowCoverageAndPreserveLegacy(t *testing.T) {
	component := func(symbol, source, sourceHash, contractHash string) project.Component {
		return project.Component{
			ID:       "go:example.test/mixed/core#" + symbol + "@" + source,
			Language: "go", Package: "example.test/mixed/core", Symbol: symbol, Source: source,
			SourceSHA256: "sha256:" + strings.Repeat(sourceHash, 64), ContractSHA256: contractHash,
		}
	}
	step := component("Step", "core/step.go", "1", "sha256:"+strings.Repeat("a", 64))
	contract := component("Contract", "core/contract.go", "2", "sha256:"+strings.Repeat("b", 64))
	neighbor := component("Neighbor", "core/neighbor.go", "3", "")
	inv := project.ComponentInventory{
		Mode: "explicit", Scope: []string{"core"},
		Files: []project.ComponentFile{
			{Path: contract.Source, SHA256: contract.SourceSHA256},
			{Path: neighbor.Source, SHA256: neighbor.SourceSHA256},
			{Path: step.Source, SHA256: step.SourceSHA256},
		},
		Components: []project.Component{contract, neighbor, step},
		Checks: []project.ResolvedComponentCheck{{Target: step.ID,
			Proof: &project.ResolvedComponentProof{Contracts: []string{contract.ID}}}},
	}
	proof := verify.ComponentProofResult{
		Target: step.ID, Verifier: "Gobra", Passed: true,
		Inputs: []project.Component{contract, step}, Verified: []string{contract.ID, step.ID},
		Contracts: []string{contract.ID},
	}
	failed := proof
	failed.Passed = false
	failed.Errors = []string{"component-proof-failure: postcondition might not hold"}
	for _, tc := range []struct {
		name    string
		results []verify.ComponentProofResult
		proved  int
		passed  bool
	}{
		{"partial proof", []verify.ComponentProofResult{proof}, 1, true},
		{"proof survives unrelated gate failure", []verify.ComponentProofResult{proof}, 1, false},
		{"failed proof", []verify.ComponentProofResult{failed}, 0, false},
		{"missing proof", nil, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Deliberately retain successful legacy Code and Larger results.
			// They must not establish a blanket claim for explicit components.
			r := receiptAssuranceFixtures()[0]
			r.Passed = tc.passed
			if err := r.AttributeComponentProofs(&inv, tc.results); err != nil {
				t.Fatal(err)
			}
			if r.Assurance != "component evidence" {
				t.Fatalf("attribution recorded assurance %q", r.Assurance)
			}
			encoded, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			var wire struct {
				Components *struct {
					Inventory project.ComponentInventory `json:"inventory"`
					Proofs    []struct {
						Target string `json:"target"`
						Status string `json:"status"`
					} `json:"proofs"`
				} `json:"components"`
			}
			if err := json.Unmarshal(encoded, &wire); err != nil {
				t.Fatal(err)
			}
			if wire.Components == nil || wire.Components.Inventory.Mode != "explicit" || len(wire.Components.Proofs) != len(inv.Components) {
				t.Fatalf("component JSON omits explicit inventory or proof coverage: %s", encoded)
			}
			coverage := make(map[string]string)
			for _, p := range wire.Components.Proofs {
				coverage[p.Target] = p.Status
			}
			for _, c := range inv.Components {
				want := "unchecked"
				if c.ID == step.ID && tc.proved == 1 {
					want = "proved"
				}
				if coverage[c.ID] != want {
					t.Errorf("component %s coverage = %q, want %q", c.ID, coverage[c.ID], want)
				}
			}
			var decoded verify.Report
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			stale := decoded
			stale.Assurance = "proved"
			stale.Fingerprint = verify.Fingerprint(stale)
			for _, report := range []*verify.Report{&r, &decoded, &stale} {
				if report.Passed != tc.passed ||
					report.Claim() != "component evidence" || report.CodeAssurance() != "component evidence" || report.EverySize() {
					t.Fatalf("explicit receipt acquired legacy claims or changed gate outcome: %#v", report)
				}
				if report.Fingerprint == "" || report.Fingerprint != verify.Fingerprint(*report) {
					t.Fatal("component receipt has no current evidence fingerprint")
				}
				md := receipt.Markdown(report)
				header := fmt.Sprintf("Component proofs: **%d proved; %d unchecked**.", tc.proved, len(inv.Components)-tc.proved)
				if !strings.Contains(md, header) {
					t.Errorf("receipt lacks scoped summary %q:\n%s", header, md)
				}
				for _, c := range inv.Components {
					want, other := "unchecked", "proved"
					if c.ID == step.ID && tc.proved == 1 {
						want, other = other, want
					}
					found := false
					for _, line := range strings.Split(md, "\n") {
						// The first canonical ID is the row's subject; later IDs
						// may be its selected contracts rather than new subjects.
						first := strings.Index(line, "go:")
						if first < 0 || !strings.HasPrefix(line[first:], c.ID) {
							continue
						}
						statuses := map[string]bool{}
						for _, word := range strings.FieldsFunc(strings.ToLower(line), func(r rune) bool { return r < 'a' || r > 'z' }) {
							statuses[word] = true
						}
						if statuses[want] && !statuses[other] {
							found = true
						}
					}
					if !found {
						t.Errorf("component %s has no own Markdown entry marked %s:\n%s", c.ID, want, md)
					}
					for _, want := range []string{c.ID, c.SourceSHA256[:19]} {
						if !strings.Contains(md, want) {
							t.Errorf("receipt omits component identity %q:\n%s", want, md)
						}
					}
					if c.ContractSHA256 != "" && !strings.Contains(md, c.ContractSHA256[:19]) {
						t.Errorf("receipt omits contract identity for %s:\n%s", c.ID, md)
					}
				}
				if len(tc.results) > 0 && !strings.Contains(md, "Gobra") {
					t.Errorf("receipt omits proof verifier:\n%s", md)
				}
				for _, result := range tc.results {
					for _, failure := range result.Errors {
						if !strings.Contains(md, failure) {
							t.Errorf("receipt omits failed proof diagnostic %q:\n%s", failure, md)
						}
					}
				}
				for _, overclaim := range []string{"The code is **proved", "at every size", "| Code ·", "proved: 2 of 2 functions verified"} {
					if strings.Contains(md, overclaim) {
						t.Errorf("legacy proof widened explicit coverage with %q:\n%s", overclaim, md)
					}
				}
			}
			if receipt.Markdown(&r) != receipt.Markdown(&decoded) {
				t.Fatal("component Markdown changed after a JSON round trip")
			}
		})
	}
	t.Run("empty explicit inventory", func(t *testing.T) {
		r := receiptAssuranceFixtures()[0]
		empty := project.ComponentInventory{Mode: "explicit", Scope: []string{"core"},
			Files: []project.ComponentFile{{Path: "core/data.go", SHA256: "sha256:" + strings.Repeat("4", 64)}}}
		if err := r.AttributeComponentProofs(&empty, nil); err != nil {
			t.Fatal(err)
		}
		md := receipt.Markdown(&r)
		if !strings.Contains(md, "Component proofs: **0 proved; 0 unchecked**.") ||
			strings.Contains(md, "The code is **proved") || strings.Contains(md, "at every size") {
			t.Fatalf("empty inventory inherited legacy proof coverage:\n%s", md)
		}
	})
	t.Run("legacy receipts", func(t *testing.T) {
		goldens := []struct{ fingerprint, markdown string }{
			{"sha256:12362a3a9c164fea85ae6ea6ff4bf623d16ed3136adfbbdb211ea8161007453a", "fa2c20aa584f9ba051a6e8f89b97843086750f307ac6e09d0c1b646273381e15"},
			{"sha256:8d0e0ac4ecf82433ffb6f0971b45a14bafc96fa10407766f3bcae0b76dc50fda", "dd24cf0587a3a16d7f372d38f562402c2f3a6738b4587ee064184dd192d91728"},
			{"sha256:295f04994adf44460b659ba6ded42792ceab506707fae3516ef90e7fab2d475e", "040b3e9231b957ca2bd8e2ff20585fde0798b6b392d6ab9e750b51c5908e447d"},
		}
		for i, r := range receiptAssuranceFixtures() {
			r.Fingerprint = verify.Fingerprint(r)
			md := receipt.Markdown(&r)
			if r.Fingerprint != goldens[i].fingerprint || fmt.Sprintf("%x", sha256.Sum256([]byte(md))) != goldens[i].markdown {
				t.Errorf("legacy %s changed its fingerprint or Markdown:\n%s", r.Project, md)
			}
			encoded, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &fields); err != nil {
				t.Fatal(err)
			}
			if _, ok := fields["components"]; ok {
				t.Errorf("legacy %s acquired explicit component JSON", r.Project)
			}
		}
	})
}
