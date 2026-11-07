package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/synth"
)

func tool701Context(t *testing.T, ws, view string) formalize.CheckContext {
	t.Helper()
	p, err := formalize.Read(ws)
	if err != nil {
		t.Fatal(err)
	}
	return (formalize.Request{Current: &formalize.Current{Dir: "examples/reader-701", Manifest: p.Manifest(),
		ModuleText: p.ModuleText + "\\* tool baseline 701\n", Includes: p.Includes, View: view,
		Lock: project.Lock{Ratified: &project.Ratification{Issue: 571, By: "owner"}, Bounds: p.Bounds, Statements: p.Statements}}}).CheckContext()
}

func tool701Work(t *testing.T, log, model string) [3]int {
	t.Helper()
	var work [3]int
	for _, c := range reader603Calls(t, log) {
		if c["main"] != model {
			continue
		}
		switch {
		case c["dump"] == "":
			work[0]++
		case c["module"] == "Reader603":
			work[1]++
		default:
			work[2]++
		}
	}
	return work
}

func TestComparison701MCPReusesItsBaseline(t *testing.T) {
	tc, log := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	authority := tool701Context(t, ws, reader603View)
	runLog := filepath.Join(t.TempDir(), "checks.jsonl")
	tool := checkTool(ws, tc, 3, runLog, 0, authority)
	for i := 0; i < 3; i++ {
		if i == 1 {
			reader603Write(t, filepath.Join(ws, "view.json"), strings.ReplaceAll(reader603View, "Working", "Candidate changed"))
			for _, name := range []string{"baseline/Reader603.tla", "baseline/Helper603.tla", "baseline/view.json", "baseline/ratified.lock", "check-context.json", "reader.json", "comparison.json"} {
				reader603Write(t, filepath.Join(ws, name), `{}`)
			}
		}
		text, bad := tool.Call(t.Context(), json.RawMessage(`{}`))
		if bad {
			t.Fatalf("MCP check %d: %s", i+1, text)
		}
	}
	if got := tool701Work(t, log, authority.Baseline.Model); got != [3]int{1, 1, 1} {
		t.Errorf("MCP repeated baseline gate/graph/replay: %v, want [1 1 1]", got)
	}
	if got := tool701Work(t, log, reader603Model); got[0] != 3 || got[1] < 3 || got[2] < 3 {
		t.Errorf("MCP skipped candidate gate/graph/replay: %v", got)
	}
	before := len(reader603Calls(t, log))
	text, bad := tool.Call(t.Context(), json.RawMessage(`{}`))
	if !bad || !strings.Contains(text, "No checks left") || len(reader603Calls(t, log)) != before {
		t.Fatal("baseline reuse changed the check allowance")
	}
	data, err := os.ReadFile(runLog)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("check log has %d records", len(lines))
	}
	for i, line := range lines {
		var run synth.GateRun
		if err := json.Unmarshal([]byte(line), &run); err != nil {
			t.Fatal(err)
		}
		if !run.Passed || run.Run != i+1 {
			t.Fatalf("wrong reused-baseline check record: %+v", run)
		}
	}
	// A separate checker in the same process starts a fresh session.
	fresh := checkTool(ws, tc, 1, "", 0, authority)
	if text, bad := fresh.Call(t.Context(), json.RawMessage(`{}`)); bad {
		t.Fatal(text)
	}
	if got := tool701Work(t, log, authority.Baseline.Model); got != [3]int{2, 2, 2} {
		t.Errorf("new checker inherited baseline evidence: %v, want [2 2 2]", got)
	}
}

func TestComparison701MCPExplainsFixedView(t *testing.T) {
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	view := strings.Replace(reader603View, `"label":"Continue"`, `"label":"Continue","hide":true`, 1)
	authority := tool701Context(t, ws, view)
	reader603Write(t, filepath.Join(ws, "baseline/view.json"), reader603View)
	tool := checkTool(ws, tc, 2, "", 0, authority)
	for _, want := range []string{".invariant/view.json", "new amendment issue"} {
		if !strings.Contains(tool.Description, want) {
			t.Errorf("tool description omits fixed-view remedy %q: %s", want, tool.Description)
		}
	}
	text, bad := tool.Call(t.Context(), json.RawMessage(`{}`))
	if !bad {
		t.Fatal("MCP accepted staged replacement of fixed hiding-invalid view")
	}
	for _, want := range []string{"unreachable_chart", "advance", "Advance", "examples/reader-701/.invariant/view.json", "separate change", "new amendment issue", "revis"} {
		if !strings.Contains(text, want) {
			t.Errorf("fixed-view tool feedback omits %q: %s", want, text)
		}
	}
	if strings.Contains(text, "repair baseline/view.json") {
		t.Fatal("tool suggests editing a fixed capture")
	}
	legacy := tool701Context(t, ws, "")
	if err := os.Remove(filepath.Join(ws, "baseline/view.json")); err != nil {
		t.Fatal(err)
	}
	missing := checkTool(ws, tc, 2, "", 0, legacy)
	text, bad = missing.Call(t.Context(), json.RawMessage(`{}`))
	if !bad || !strings.Contains(text, "baseline/view.json") || !strings.Contains(text, "original bounds") {
		t.Fatalf("missing legacy view lost repair path: %s", text)
	}
	reader603Write(t, filepath.Join(ws, "baseline/view.json"), reader603View)
	if text, bad := missing.Call(t.Context(), json.RawMessage(`{}`)); bad {
		t.Fatalf("legacy repair failed: %s", text)
	}
}
