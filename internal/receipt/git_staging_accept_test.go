package receipt

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/verify"
)

func TestGitStageReceiptSelection(t *testing.T) {
	for _, mode := range []string{"git", "directory"} {
		for _, onlyLarger := range []bool{false, true} {
			var provenance verify.PythonApplicationProvenance
			if err := json.Unmarshal([]byte(`{"file_selection":"`+mode+`","files":[]}`), &provenance); err != nil {
				t.Fatal(err)
			}
			x := verify.PythonApplicationExecutionResult{Provenance: provenance}
			a := &verify.PythonApplicationReport{}
			if onlyLarger {
				a.Larger = []verify.PythonApplicationLarger{{Execution: &x}}
			} else {
				a.Executions = []verify.PythonApplicationExecutionResult{x}
			}
			r := &verify.Report{Project: "selection receipt", Application: a}
			raw, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(raw), `"file_selection":"`+mode+`"`) {
				t.Errorf("JSON lost %s selection (larger=%t)", mode, onlyLarger)
			}
			markdown := strings.ToLower(Markdown(r))
			want := "git ls-files"
			if mode == "directory" {
				want = "whole directory"
			}
			if !strings.Contains(markdown, want) {
				t.Errorf("receipt does not explain %s selection (larger=%t): %s", mode, onlyLarger, markdown)
			}
		}
	}
}
