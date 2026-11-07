package verify

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/statechart"
)

func TestCapturedComparisonDistinguishesAbsenceAndCompleteDiagnostics(t *testing.T) {
	a, b := comparisonRenderSnapshot(t, 1), comparisonRenderSnapshot(t, 2)
	c, err := statechart.Compare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, cause string
		data        []byte
		err         error
	}{
		{name: "absent", err: os.ErrNotExist},
		{name: "empty", cause: "EOF", data: []byte{}},
		{name: "malformed", cause: "unexpected EOF", data: []byte(`{"version":`)},
		{name: "trailing", cause: "extra comparison JSON data", data: append(append([]byte{}, data...), []byte(" {}")...)},
		{name: "valid", data: data},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, diagnostic := CapturedComparison(tc.data, tc.err, b.Lock)
			r := &Reader{Comparison: c, ComparisonDiagnostic: &ReaderDiagnostic{Code: "obsolete"}}
			r.captureComparison(tc.data, tc.err, b.Lock)
			want, _ := json.Marshal([]any{got, diagnostic})
			captured, _ := json.Marshal([]any{r.Comparison, r.ComparisonDiagnostic})
			if string(want) != string(captured) {
				t.Fatal("receipt capture differs from shared validation")
			}
			if tc.cause != "" {
				if got != nil || diagnostic == nil || diagnostic.Code != "comparison_evidence" || diagnostic.Source != nil ||
					diagnostic.Message != "Proposal comparison unavailable: "+tc.cause {
					t.Fatalf("incomplete diagnostic: %+v", diagnostic)
				}
				if !strings.Contains(strings.Join(r.ComparisonNotes(), " "), "comparison_evidence") {
					t.Fatal("diagnostic code is not visible")
				}
			} else if diagnostic != nil || (got != nil) != (tc.name == "valid") {
				t.Fatalf("unexpected captured evidence: %+v %+v", got, diagnostic)
			}
		})
	}
}
