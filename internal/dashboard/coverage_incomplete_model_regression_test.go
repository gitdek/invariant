package dashboard

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/conformance"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/verify"
)

func TestIncompleteModelCoverageDisplaysUnknown802(t *testing.T) {
	for _, source := range []string{"conformance", "python_application"} {
		t.Run(source, func(t *testing.T) {
			r := verify.Report{Project: "incomplete model", Bounds: map[string]string{"Limit": "4"},
				Design: verify.Design{Outcome: "violated", Violated: "Safe", DistinctStates: 3}}
			result := conformance.Result{Passed: true, Runs: 1, Steps: 5, States: 3, ModelStates: 3}
			prefix := "| Code · conformance |"
			if source == "conformance" {
				r.Conformance = &result
			} else {
				r.Application = &verify.PythonApplicationReport{Conformance: []verify.PythonApplicationConformance{{
					Key:    verify.PythonApplicationInvocationKey{Target: "app", Check: "drive", ID: "one"},
					Bounds: r.Bounds, Result: result,
				}}}
				prefix = "| <code>app</code> · <code>drive</code> · <code>one</code> |"
			}
			r.Fingerprint = verify.Fingerprint(r)
			before, err := r.EvidenceJSON()
			if err != nil {
				t.Fatal(err)
			}
			md := receipt.Markdown(&r)
			if !strings.Contains(md, "**❌ Fail.") {
				t.Fatal("model failure lost the gate failure")
			}
			found := false
			for _, line := range strings.Split(md, "\n") {
				if strings.HasPrefix(line, prefix) {
					found = true
					if !strings.Contains(line, "✅ tested (sampled)") || !strings.Contains(line, "coverage unknown") || strings.Contains(line, "%") || strings.Contains(line, "every reachable state") {
						t.Errorf("conformance row misstates incomplete exploration: %s", line)
					}
				}
			}
			if !found {
				t.Fatalf("missing conformance row: %s", md)
			}
			p := projectOf(projectSource{}, &r)
			if p.Passed || p.Assurance != "tested (sampled)" || p.Fingerprint != r.Fingerprint {
				t.Fatalf("dashboard changed outcome or identity: %+v", p)
			}
			cs := dashboardCoverageJSON802(t, p)
			if len(cs) != 1 || cs[0].Status != "unknown" || !cs[0].Checked || !cs[0].Sampled || cs[0].Fraction != "" || cs[0].Percent != "" {
				t.Fatalf("dashboard supplied incomplete model coverage: %+v", cs)
			}
			page := makeReader(readerEvidence{Report: &r})
			// The page's own stylesheet holds percentages, and isn't text
			// a reader sees.
			visible := dashboardText802(regexp.MustCompile(`(?s)<style>.*?</style>`).ReplaceAll(page.html, nil))
			if !strings.Contains(visible, "coverage unknown") || !strings.Contains(visible, "tested (sampled)") || strings.Contains(visible, "100%") || strings.Contains(visible, "3/3") {
				t.Fatalf("evidence page misstates coverage: %s", visible)
			}
			after, err := r.EvidenceJSON()
			if err != nil || !bytes.Equal(before, after) || verify.Fingerprint(r) != r.Fingerprint {
				t.Fatal("rendering changed evidence or its fingerprint")
			}
		})
	}
}
