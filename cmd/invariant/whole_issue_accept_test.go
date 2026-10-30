package main

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/synth"
)

func TestPlumbingCheckValidatesIssuePlansWithinItsBudget(t *testing.T) {
	ws := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "checks.jsonl")
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(ws, "issues.json"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	check := planTool(ws, plumbing.Sandbox{}, 2, logPath)
	write(`{"name":"all the work","summary":"Implement both parts.","issues":[]}`)
	feedback, _ := check.Call(context.Background(), json.RawMessage(`{}`))
	if !strings.Contains(feedback, "no issues") {
		t.Errorf("the plumbing checker must validate the issue plan: %s", feedback)
	}
	write(`{"name":"all the work","summary":"Implement both parts.","issues":[
		{"title":"Serve the graph","body":"Serve every decision.\n\nKind: plumbing"},
		{"title":"Draw the graph","body":"Draw the graph served by the first step.\n\nKind: plumbing"}
	]}`)
	feedback, failed := check.Call(context.Background(), json.RawMessage(`{}`))
	if failed || !strings.Contains(feedback, "holds together") {
		t.Errorf("a valid issue plan must check without plumbing acceptance tests: failed=%v, %s", failed, feedback)
	}
	feedback, failed = check.Call(context.Background(), json.RawMessage(`{}`))
	if !failed || !strings.Contains(feedback, "No checks left") {
		t.Errorf("issue-plan checks must consume the plumbing check budget: failed=%v, %s", failed, feedback)
	}
	f, err := os.Open(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var runs []synth.GateRun
	scanner := bufio.NewScanner(f)
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
	if len(runs) != 2 || runs[0].Run != 1 || runs[0].Passed || len(runs[0].Failed) == 0 || runs[1].Run != 2 || !runs[1].Passed {
		t.Fatalf("expected one failed and one successful check, numbered in the same budget: %+v", runs)
	}
}
