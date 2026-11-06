package verify

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"reflect"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
)

func (s *readerSnapshot) comparison(data []byte, err error) {
	if os.IsNotExist(err) {
		return
	}
	var c statechart.Comparison
	if err == nil {
		err = json.Unmarshal(data, &c)
	}
	if err == nil {
		err = c.Validate()
	}
	if err == nil {
		err = validateComparisonSources(&c, s.reader.Inputs, s.lock)
	}
	if err == nil {
		_, err = c.SVG()
	}
	if err != nil {
		s.reader.ComparisonDiagnostic = &ReaderDiagnostic{Code: "comparison", Message: "Captured proposal comparison is unavailable: " + err.Error() + ". Restore .invariant/comparison.json from the checked proposal and verify again."}
		return
	}
	s.reader.Comparison = &c
}

func validateComparisonSources(c *statechart.Comparison, inputs ReaderInputs, lock project.Lock) error {
	if c.Candidate.Module != inputs.Module || c.CandidateIdentity.ViewSHA256 != inputs.View || !maps.Equal(c.CandidateIdentity.Includes, inputs.Includes) ||
		!maps.Equal(c.Candidate.Lock.Bounds, lock.Bounds) || !reflect.DeepEqual(c.Candidate.Lock.Statements, lock.Statements) {
		return fmt.Errorf("comparison candidate view, includes or locked rules differ from the checked project")
	}
	return nil
}

// ValidateComparison validates captured provenance independently of the final
// chart. The final model may differ, but the proposal's original inputs remain.
func (r *Reader) ValidateComparison(lock project.Lock) error {
	if r.Comparison == nil {
		return nil
	}
	if err := r.Comparison.Validate(); err != nil {
		return err
	}
	return validateComparisonSources(r.Comparison, r.Inputs, lock)
}

func (r *Reader) FinalSources() string {
	data, _ := json.Marshal(r.Inputs)
	text := "Final-model source identities: " + string(data)
	if d := r.Unavailable; d != nil {
		text += "\nFinal-model chart unavailable (" + d.Code + "): " + d.Message
	}
	if l := r.Limitation; l != nil {
		text += fmt.Sprintf("\nFinal-model chart not drawn (too_large): TLC reached %d states, more than the %d states charts are drawn for. Mappings and hiding were not evaluated.", l.Actual, l.Limit)
	}
	return text
}

// SVG uses the shared native renderer for both captured and final evidence.
func (r *Reader) SVG() (string, error) {
	if r.Comparison != nil {
		return r.Comparison.SVGWithFinal(r.Chart, r.FinalSources())
	}
	if r.Chart == nil {
		return "", nil
	}
	return r.Chart.SVG()
}
