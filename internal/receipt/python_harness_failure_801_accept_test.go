package receipt

import (
	"encoding/json"
	"html"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/verify"
)

func TestPythonHarnessFailureLiteralReceipt801(t *testing.T) {
	const hostile = "<script>error801</script> [click](https://invalid.example) | `tick`\n<!-- extra row --> &lt;literal&gt;"
	const cause = "hold requires a selected call on an owned worker: " + hostile
	key := verify.PythonApplicationInvocationKey{
		Target: "target801 " + hostile,
		Check:  "driver801 " + hostile,
		ID:     "invocation801 " + hostile,
		Kind:   "conformance",
	}
	for _, explicit := range []bool{false, true} {
		for _, larger := range []bool{false, true} {
			// Use the JSON API so this pins behavior without prescribing the Go
			// struct representation. Older receipts may omit harness_error.
			wire := map[string]any{
				"key": key, "complete": false, "success": false, "exit_code": 1,
				"harness_error": cause,
				"diagnostics":   []string{cause, strings.Repeat("traceback context retained\n", 30), "later poisoned lifecycle"},
			}
			b, err := json.Marshal(wire)
			if err != nil {
				t.Fatal(err)
			}
			var x verify.PythonApplicationExecutionResult
			if err := json.Unmarshal(b, &x); err != nil {
				t.Fatal(err)
			}
			r := &verify.Report{Project: "first collector cause", Application: &verify.PythonApplicationReport{}}
			if explicit {
				r.Components = &verify.ComponentEvidence{Inventory: project.ComponentInventory{Mode: "explicit"}}
			}
			if larger {
				r.Application.Larger = []verify.PythonApplicationLarger{{Key: key, Execution: &x, Result: verify.Larger{Required: true}}}
			} else {
				r.Application.Executions = []verify.PythonApplicationExecutionResult{x}
			}
			dir := t.TempDir()
			if err := Write(dir, r); err != nil {
				t.Fatal(err)
			}
			b, err = os.ReadFile(filepath.Join(dir, "receipt.json"))
			if err != nil {
				t.Fatal(err)
			}
			var data struct {
				Application struct {
					Executions []struct {
						HarnessError string `json:"harness_error"`
					} `json:"executions"`
					Larger []struct {
						Execution struct {
							HarnessError string `json:"harness_error"`
						} `json:"execution"`
					} `json:"larger"`
				} `json:"application"`
			}
			if err := json.Unmarshal(b, &data); err != nil {
				t.Fatal(err)
			}
			got := ""
			if larger && len(data.Application.Larger) == 1 {
				got = data.Application.Larger[0].Execution.HarnessError
			} else if !larger && len(data.Application.Executions) == 1 {
				got = data.Application.Executions[0].HarnessError
			}
			if got != cause {
				t.Errorf("first diagnostic lost on JSON round trip (explicit=%t larger=%t): %q", explicit, larger, got)
			}
			b, err = os.ReadFile(filepath.Join(dir, "receipt.md"))
			if err != nil {
				t.Fatal(err)
			}
			md := string(b)
			_, failed, found := strings.Cut(md, "**What failed**")
			if !found {
				t.Error("receipt has no failure section")
			}
			failed, _, _ = strings.Cut(failed, "\n<details>")
			plain := html.UnescapeString(failed)
			for _, want := range []string{cause, key.Target, key.Check, key.ID} {
				if !strings.Contains(plain, want) {
					t.Errorf("failure summary lost literal %q (explicit=%t larger=%t): %s", want, explicit, larger, failed)
				}
			}
			for _, active := range []string{"<script>error801</script>", "[click](https://invalid.example)", "<!-- extra row -->", "| `tick`"} {
				if strings.Contains(md, active) {
					t.Errorf("application text became markup: %q", active)
				}
			}
			if strings.Contains(failed, "traceback context retained") {
				t.Error("failure summary substituted the full traceback for the first cause")
			}
			if !strings.Contains(md, "traceback context retained") || !strings.Contains(md, "later poisoned lifecycle") {
				t.Error("receipt discarded full diagnostic context")
			}
		}
	}
}
