package synth

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tla"
)

const example = "../../examples/02-twophase-commit"

func TestReadStream(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"system","subtype":"init","model":"claude-opus-5","mcp_servers":[{"name":"invariant","status":"connected"}]}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Writing the model."},{"type":"tool_use","name":"Write"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"mcp__invariant__gate"}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result"}]}}`,
		`{"type":"result","subtype":"success","result":"The gate passed.","total_cost_usd":1.25,"num_turns":14}`,
	}, "\n")
	var transcript bytes.Buffer
	u, err := ReadStream(strings.NewReader(stream), &transcript)
	if err != nil {
		t.Fatal(err)
	}
	if u.Model != "claude-opus-5" || u.GateTool != "connected" || u.Outcome != "success" || u.Turns != 14 || u.CostUSD != 1.25 {
		t.Errorf("usage = %+v", u)
	}
	if u.ToolCalls["mcp__invariant__gate"] != 1 || u.ToolCalls["Write"] != 1 {
		t.Errorf("tool calls = %v", u.ToolCalls)
	}
	if strings.Count(transcript.String(), "\n") != 5 {
		t.Errorf("the transcript should hold every event")
	}
}

func TestPrepareAssembleAndTampered(t *testing.T) {
	p, err := project.Load(example)
	if err != nil {
		t.Fatal(err)
	}
	skeleton, err := Skeleton(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(skeleton, "TMCommit ==") || !strings.Contains(skeleton, "EarlyCommit ==") {
		t.Fatalf("the skeleton should keep the pinned bug and drop the model:\n%s", skeleton)
	}
	ws := t.TempDir()
	if err := Prepare(p, skeleton, ws); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(filepath.Join(ws, "twophase")); len(entries) != 0 {
		t.Fatal("the agent should start with an empty package")
	}
	// The agent writes code and, against the rules, weakens the lock and go.mod.
	os.WriteFile(filepath.Join(ws, "twophase", "twophase.go"), []byte("package twophase\n"), 0o644)
	os.WriteFile(filepath.Join(ws, ".invariant", "ratified.lock"), []byte(`{"statements": []}`), 0o644)
	os.WriteFile(filepath.Join(ws, "twophase", "go.mod"), []byte("module sneaky\n"), 0o644)

	tampered, err := Tampered(p, ws)
	if err != nil || len(tampered) != 1 || tampered[0] != ".invariant/ratified.lock" {
		t.Fatalf("Tampered = %v, %v; want the lock", tampered, err)
	}
	dst := filepath.Join(t.TempDir(), "result")
	if err := Assemble(p, ws, dst); err != nil {
		t.Fatal(err)
	}
	lock, _ := os.ReadFile(filepath.Join(dst, ".invariant", "ratified.lock"))
	original, _ := os.ReadFile(filepath.Join(example, ".invariant", "ratified.lock"))
	if !bytes.Equal(lock, original) {
		t.Error("the assembled project must carry the original lock")
	}
	if _, err := os.Stat(filepath.Join(dst, "twophase", "go.mod")); !os.IsNotExist(err) {
		t.Error("a go.mod inside the package must not be assembled")
	}
	if _, err := os.Stat(filepath.Join(dst, "twophase", "twophase.go")); err != nil {
		t.Error("the agent's code must be assembled")
	}
	// Every pin still matches in the skeleton the agent started from.
	src, _ := os.ReadFile(filepath.Join(ws, p.Manifest.Module))
	for _, s := range p.Lock.Statements {
		if h, err := tla.PinHash(string(src), s.Name, project.Model); err != nil || h != s.SHA256 {
			t.Errorf("the skeleton changed %s's pin", s.Name)
		}
	}
}

func TestPrompt(t *testing.T) {
	p, err := project.Load(example)
	if err != nil {
		t.Fatal(err)
	}
	skeleton, _ := Skeleton(p)
	request, _ := p.Request()
	got := Prompt(p, skeleton, request, 4, false)
	for _, want := range []string{"Implement two-phase commit", "`TCConsistent`", "`EarlyCommit`", "It must violate TCConsistent.",
		"`RM = {r1, r2, r3}`", "Go package `twophase`", "You have 4 gate runs", "func Successors(s State) []State",
		tla.ModelMarker, "// +gobra", "The skeleton of"} {
		if !strings.Contains(got, want) {
			t.Errorf("the prompt lacks %q", want)
		}
	}
	if strings.Contains(got, "RMRcvCommitMsg(s State") {
		t.Error("the prompt must not contain the hand-built answer")
	}
	if draft := Prompt(p, skeleton, request, 4, true); !strings.Contains(draft, "It's already drafted") || strings.Contains(draft, "The skeleton of") {
		t.Error("with a draft model, the prompt should say the model is drafted")
	}
}
