package main

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/mcp"
	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/synth"
	"github.com/gitdek/invariant/internal/toolchain"
)

const issues560 = `{"name":"the whole change","summary":"Store and display the data.","issues":[{"title":"Store the data","body":"Persist the data.\n\nKind: plumbing"},{"title":"Show the data","body":"Display the stored data.\n\nKind: plumbing"}]}`

// The last argument is the caller's parent issue, supplied by the new
// -parent-plan flag; zero keeps the existing unrestricted tool behavior.
func TestStepCheckToolsRefuseIssuePlans560(t *testing.T) {
	for _, mode := range []string{"plumbing", "modeled", "issues"} {
		t.Run(mode, func(t *testing.T) {
			ws, log := t.TempDir(), filepath.Join(t.TempDir(), "checks.jsonl")
			put560(t, ws, "issues.json", issues560)
			var check mcp.Tool
			switch mode {
			case "plumbing":
				check = planTool(ws, plumbing.Sandbox{}, 2, log, 10)
			case "modeled":
				check = checkTool(ws, toolchain.Toolchain{}, 2, log, 10)
			case "issues":
				check = issuesTool(ws, 2, log, 10)
			}
			for i := 0; i < 2; i++ {
				if i == 1 {
					// Another draft cannot hide a forbidden issues.json. The
					// rejection precedes any Go sandbox or model checker.
					put560(t, ws, "plan.json", `{}`)
					put560(t, ws, "proposal.json", `{"forks":[{"id":"F1","question":"Build as one change?","options":[{"id":"A","says":"Yes."},{"id":"B","says":"Stop the parent and replan."}]}]}`)
				}
				feedback, _ := check.Call(context.Background(), json.RawMessage(`{}`))
				if !strings.Contains(feedback, "issues.json") || !strings.Contains(feedback, "#10") || strings.Contains(feedback, "holds together") || strings.Contains(feedback, "nothing to check") {
					t.Errorf("step check must refuse the issue plan and identify the parent: %s", feedback)
				}
			}
			feedback, failed := check.Call(context.Background(), json.RawMessage(`{}`))
			if !failed || !strings.Contains(feedback, "No checks left") {
				t.Errorf("refused splits must consume the ordinary allowance: %s", feedback)
			}
			file, err := os.Open(log)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			scanner := bufio.NewScanner(file)
			var runs []synth.GateRun
			for scanner.Scan() {
				var run synth.GateRun
				if err := json.Unmarshal(scanner.Bytes(), &run); err != nil {
					t.Fatal(err)
				}
				runs = append(runs, run)
			}
			if err := scanner.Err(); err != nil {
				t.Fatal(err)
			}
			if len(runs) != 2 {
				t.Fatalf("want two recorded refusals, got %+v", runs)
			}
			for i, run := range runs {
				if run.Run != i+1 || run.Passed || len(run.Failed) == 0 {
					t.Errorf("refusal must be recorded as a numbered failure: %+v", run)
				}
			}
		})
	}
	for _, mode := range []string{"plumbing", "issues"} {
		t.Run("ordinary-"+mode, func(t *testing.T) {
			ws := t.TempDir()
			put560(t, ws, "issues.json", issues560)
			check := planTool(ws, plumbing.Sandbox{}, 1, "", 0)
			if mode == "issues" {
				check = issuesTool(ws, 1, "", 0)
			}
			feedback, failed := check.Call(context.Background(), json.RawMessage(`{}`))
			if failed || !strings.Contains(feedback, "holds together") {
				t.Errorf("ordinary issue must still pass issue-plan validation: %s", feedback)
			}
		})
	}
}

func put560(t *testing.T, dir, name, source string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}
