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
	if os.IsNotExist(err) {
		return
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
		r.ComparisonDiagnostic = &ReaderDiagnostic{Code: "comparison_evidence", Message: "Proposal comparison unavailable: " + printable(err.Error())}
		return
	}
	r.Comparison = &c
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
		return []string{r.ComparisonDiagnostic.Message}
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
	if r.Comparison == nil && r.ComparisonDiagnostic == nil {
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
		svg, err := statechart.TextSVG([]string{fmt.Sprintf("Final model too_large: %d states, capacity %d.", r.Limitation.Actual, r.Limitation.Limit)})
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
