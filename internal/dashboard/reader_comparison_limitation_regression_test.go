package dashboard

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/verify"
)

// Omitting the receipt comparison after export leaves its original SVG beside
// a valid final limitation. Check that final panel before retaining the outcome.
func TestReaderOmittedComparisonKeepsArchivedFinalLimitation(t *testing.T) {
	for _, outcome := range []string{"too_large", "missing_view", "reader_computation"} {
		t.Run(outcome, func(t *testing.T) {
			f := receipt723Fixture(t)
			if outcome == "too_large" {
				large := reader631FixtureFor(t, 501, "Large final model")
				c, err := statechart.Compare(f.Report.Reader.Comparison.Baseline, receipt723Snapshot(large, "Large proposal"))
				if err != nil {
					t.Fatal(err)
				}
				f = large
				f.Report.Reader.Comparison = c
				f.Files[".invariant/comparison.json"] = reader631JSON(t, c)
			} else {
				f.Report.Reader.Chart = nil
				message := "Invariant replay failed <literal> & preserved."
				if outcome == "missing_view" {
					delete(f.Files, ".invariant/view.json")
					message = "No reader chart is available. Supply .invariant/view.json and verify again."
				}
				f.Report.Reader.Unavailable = &verify.ReaderDiagnostic{Code: outcome, Message: message}
			}
			f.bind(t)
			e := receipt723Evidence(t, f)
			status, message, svg := e.checked()
			if status != "chart" || svg == "" || strings.Contains(message, "comparison_evidence") || !bytes.Equal(e.Artifacts.SVG, []byte(svg)) {
				t.Fatalf("invalid comparison and final limitation fixture: %s %s", status, message)
			}
			for _, damage := range []string{"none", "final text", "final wrapper", "unrelated SVG"} {
				t.Run(damage, func(t *testing.T) {
					s, _, p := receipt723Load(t, f, func(entries map[string][]byte) {
						receipt723EditRecord(t, f.Lock, entries, func(r *verify.Reader) { r.Comparison = nil })
						if !bytes.Equal(entries["reader/reader.svg"], e.Artifacts.SVG) {
							t.Fatal("comparison omission changed the original archived SVG")
						}
						raw := string(entries["reader/reader.svg"])
						switch damage {
						case "final text":
							raw = strings.Replace(raw, "Final model ", "Substituted final outcome ", 1)
						case "final wrapper":
							at := strings.LastIndex(raw, `<g transform="translate(0 `)
							if at < 0 {
								t.Fatal("missing final panel wrapper")
							}
							raw = raw[:at] + strings.Replace(raw[at:], `<g transform=`, `<g opacity="0" transform=`, 1)
						case "unrelated SVG":
							raw = `<svg xmlns="http://www.w3.org/2000/svg"><text>Unrelated evidence</text></svg>`
						}
						if damage != "none" && raw == string(e.Artifacts.SVG) {
							t.Fatal("ineffective final panel damage")
						}
						entries["reader/reader.svg"] = []byte(raw)
					})
					want := outcome
					if damage != "none" {
						want = "artifact_mismatch"
					}
					for _, server := range []*Server{s, receipt723Restart(t, s, p, f)} {
						reader631Diagnostic(t, server, p, want)
						if damage == "none" {
							if !strings.Contains(p.Message, "comparison_evidence") || !strings.Contains(p.Message, "receipt") {
								t.Fatalf("final limitation lost the comparison omission diagnostic: %+v", p)
							}
							switch outcome {
							case "too_large":
								if !strings.Contains(p.Message, "501") || !strings.Contains(p.Message, "500") {
									t.Fatal("final limitation lost its counts")
								}
							case "missing_view":
								if !strings.Contains(p.Message, "No saved reader view") {
									t.Fatal("missing view lost its specific explanation")
								}
							case "reader_computation":
								if !strings.Contains(p.Message, f.Report.Reader.Unavailable.Message) {
									t.Fatal("final unavailable outcome lost its literal cause")
								}
							}
						}
						if project := readerRecoveryProject(t, server.assemble(time.Now()), receipt723RepoName, f.Dir); project.Poster != nil {
							t.Fatal("a final limitation supplied a poster")
						}
					}
				})
			}
		})
	}
}
