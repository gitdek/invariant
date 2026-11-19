package verify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
)

func (r *Reader) captureComparison(data []byte, err error, lock project.Lock) {
	r.Comparison, r.ComparisonDiagnostic = CapturedComparison(data, err, lock)
}

// FinalDigest binds the final evidence independently of optional comparison
// companions. It never replaces the original reader or verification identity.
func (r *Reader) FinalDigest() string {
	copy := *r
	copy.Comparison, copy.ComparisonRef, copy.ComparisonDiagnostic = nil, nil, nil
	copy.FinalSHA256 = ""
	b, _ := json.Marshal(copy)
	return readerHash(b)
}

func (r *Reader) ResolveComparison(read func(string) ([]byte, error)) error {
	ref := r.ComparisonRef
	if ref == nil {
		return nil
	}
	if r.Comparison != nil || r.FinalSHA256 == "" || r.FinalSHA256 != r.FinalDigest() {
		return fmt.Errorf("final reader evidence differs from its comparison reference")
	}
	var data []byte
	err := fmt.Errorf("missing reader-comparison.json evidence companion")
	if read != nil {
		data, err = read("reader-comparison.json")
	}
	if err == nil {
		err = ref.Validate("reader-comparison.json", data)
	}
	var c *statechart.Comparison
	if err == nil {
		c, err = statechart.ResolveComparison(data, read)
	}
	if err == nil && c.Fingerprint != ref.Fingerprint {
		err = fmt.Errorf("comparison companion fingerprint differs")
	}
	if err == nil {
		chart := r.Chart
		if chart == nil {
			chart = c.Candidate.Chart
			if chart == nil {
				chart = c.Baseline.Chart
			}
		}
		err = ref.ValidateChart(chart)
	}
	if err != nil {
		r.ComparisonDiagnostic = &ReaderDiagnostic{Code: "comparison_evidence", Message: "Proposal comparison unavailable: " + printable(err.Error())}
		return nil
	}
	r.Comparison = c
	r.ComparisonRef, r.FinalSHA256 = nil, ""
	return nil
}

// CapturedComparison validates the optional captured file for both verification
// and receipt readers. Only a missing file means absence; empty bytes are damage.
func CapturedComparison(data []byte, err error, lock project.Lock, readers ...func(string) ([]byte, error)) (*statechart.Comparison, *ReaderDiagnostic) {
	if os.IsNotExist(err) {
		return nil, nil
	}
	var c statechart.Comparison
	if err == nil {
		d := json.NewDecoder(bytes.NewReader(data))
		d.UseNumber()
		err = d.Decode(&c)
		if err == nil {
			var extra any
			if d.Decode(&extra) != io.EOF {
				err = fmt.Errorf("extra comparison JSON data")
			}
		}
	}
	if err == nil {
		if c.HasCompanions() && len(readers) == 1 {
			var resolved *statechart.Comparison
			resolved, err = statechart.ResolveComparison(data, readers[0])
			if err == nil {
				c = *resolved
			}
		}
	}
	if err == nil {
		err = c.Validate()
	}
	if err == nil && c.CandidateIdentity.ProposalHash != project.ProposalHash(lock.Bounds, lock.Statements) {
		err = fmt.Errorf("comparison candidate rules or bounds differ from the captured project lock")
	}
	// Evidence that can't be drawn is damaged too, and never reaches the
	// renderers that draw the final chart beside it.
	if err == nil {
		if _, renderErr := c.SVG(); renderErr != nil {
			err = fmt.Errorf("comparison cannot be drawn: %w", renderErr)
		}
	}
	if err != nil {
		return nil, &ReaderDiagnostic{Code: "comparison_evidence", Message: "Proposal comparison unavailable: " + printable(err.Error())}
	}
	return &c, nil
}

func captureComparisonFiles(dir string) map[string][]byte {
	files := map[string][]byte{}
	for _, name := range []string{"reader-baseline.json", "reader-candidate.json"} {
		if b, err := readReaderFile(dir, ".invariant/"+name); err == nil {
			files[name] = b
		}
	}
	return files
}

// printable keeps a diagnostic drawable whatever damaged evidence it quotes.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsGraphic(r) {
			return r
		}
		return '\uFFFD'
	}, s)
}

// ComparisonNotes explicitly distinguish the retained proposal's sources from
// the independently checked final model, without changing either identity.
func (r *Reader) ComparisonNotes() []string {
	if r == nil {
		return nil
	}
	if r.ComparisonDiagnostic != nil {
		return []string{r.ComparisonDiagnostic.Code + ": " + r.ComparisonDiagnostic.Message}
	}
	if r.ComparisonRef != nil {
		return []string{"Retained proposal comparison in reader-comparison.json; final-model evidence is independently checked.", r.ComparisonRef.Limitation.Description()}
	}
	if r.Comparison == nil {
		return nil
	}
	out := []string{"Retained proposal comparison; final-model evidence is computed independently.",
		"Final model: " + r.Inputs.Model, "Final view: " + r.Inputs.View}
	if r.Inputs.Model != r.Comparison.CandidateIdentity.ModelSHA256 || r.Inputs.View != r.Comparison.CandidateIdentity.ViewSHA256 || !sameComparisonIncludes(r.Inputs.Includes, r.Comparison.CandidateIdentity.Includes) {
		out = append(out, "Final source identities differ from the retained proposal comparison. Its candidate chart describes the proposed source, not the synthesized final model.")
	}
	return append(out, r.Comparison.Qualifications()...)
}

func sameComparisonIncludes(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// SVG shares the same comparison and ordinary renderer with all publishers.
func (r *Reader) SVG() (string, error) {
	if r == nil {
		return "", nil
	}
	if r.Comparison == nil && r.ComparisonDiagnostic == nil && r.ComparisonRef == nil {
		if r.Chart == nil {
			return "", nil
		}
		return r.Chart.SVG()
	}
	notes, err := statechart.TextSVG(r.ComparisonNotes())
	if err != nil {
		return "", err
	}
	parts := []string{notes}
	if r.Comparison != nil {
		svg, err := r.Comparison.SVG()
		if err != nil {
			return "", err
		}
		parts = append(parts, svg)
	}
	if r.Chart != nil {
		title, err := statechart.TextSVG([]string{"Independently checked final-model chart", "Final model: " + r.Inputs.Model, "Final view: " + r.Inputs.View})
		if err != nil {
			return "", err
		}
		svg, err := r.Chart.SVG()
		if err != nil {
			return "", err
		}
		parts = append(parts, title, svg)
	} else if r.Limitation != nil {
		text := fmt.Sprintf("Final model too_large: %d states, capacity %d.", r.Limitation.Actual, r.Limitation.Limit)
		if r.Limitation.Kind != "" {
			text = "Final model: " + r.Limitation.Description()
		}
		svg, err := statechart.TextSVG([]string{text})
		if err != nil {
			return "", err
		}
		parts = append(parts, svg)
	} else if r.Unavailable != nil {
		svg, err := statechart.TextSVG([]string{"Final model chart unavailable: " + r.Unavailable.Message})
		if err != nil {
			return "", err
		}
		parts = append(parts, svg)
	}
	return statechart.StackSVG(parts...)
}
