package synth

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
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
	got := Prompt(p, skeleton, request, 4, false, false)
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
	if draft := Prompt(p, skeleton, request, 4, true, false); !strings.Contains(draft, "It's already drafted") || strings.Contains(draft, "The skeleton of") {
		t.Error("with a draft model, the prompt should say the model is drafted")
	}
}

// The agent's sandbox: the file tools and Invariant's tool, and nothing in
// the home directory.
func TestClaudeCodeSandbox(t *testing.T) {
	args, err := ClaudeCode{Model: "opus", BudgetUSD: 5, MaxTurns: 80}.args(Job{Prompt: "p", GateServer: []string{"invariant", "mcp"}, Tools: []string{"check"}})
	if err != nil {
		t.Fatal(err)
	}
	flags := map[string]string{}
	for i := 0; i+1 < len(args); i++ {
		if strings.HasPrefix(args[i], "--") {
			flags[args[i]] = args[i+1]
		}
	}
	if flags["--tools"] != "Read,Write,Edit,Glob,Grep" {
		t.Errorf("--tools = %q; the agent must get only the file tools", flags["--tools"])
	}
	if flags["--allowedTools"] != "Read,Write,Edit,Glob,Grep,mcp__invariant__check" {
		t.Errorf("--allowedTools = %q", flags["--allowedTools"])
	}
	for _, deny := range []string{"Bash", "WebFetch", "Read(~/**)", "Grep(~/**)", "Glob(~/**)", "Write(~/**)", "Edit(~/**)"} {
		if !strings.Contains(","+flags["--disallowedTools"]+",", ","+deny+",") {
			t.Errorf("--disallowedTools lacks %s", deny)
		}
	}
	if flags["--mcp-config"] == "" || !contains(args, "--strict-mcp-config") {
		t.Error("the agent must get Invariant's MCP server and no other")
	}
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func TestOwnedByLanguage(t *testing.T) {
	ts := project.Manifest{Module: ".invariant/specs/Buf.tla", Code: "src", Language: "typescript", Conformance: "conformance.ts"}
	py := project.Manifest{Module: ".invariant/specs/Buf.tla", Code: "buffer", Language: "python", Conformance: "conformance.py"}
	gm := project.Manifest{Module: ".invariant/specs/Buf.tla", Code: "buffer", Language: "go"}
	for _, tc := range []struct {
		m    project.Manifest
		file string
		want bool
	}{
		{ts, ".invariant/specs/Buf.tla", true}, {ts, "src/machine.ts", true}, {ts, "src/machine.test.ts", true},
		{ts, "conformance.ts", true}, {ts, "package.json", false}, {ts, "src/package.json", false},
		{ts, "README.md", false}, {ts, ".invariant/ratified.lock", false},
		{py, "buffer/core.py", true}, {py, "test_buffer.py", true}, {py, "conformance.py", true},
		{py, "requirements.txt", false}, {py, "setup.py", false}, {py, "docs/test_x.py", false},
		{gm, "buffer/buffer.go", true}, {gm, "buffer/go.mod", false}, {gm, "go.mod", false}, {gm, "test_buffer.py", false},
	} {
		if got := owned(tc.m, tc.file); got != tc.want {
			t.Errorf("%s: owned(%s) = %v", tc.m.Language, tc.file, got)
		}
	}
}

// Assemble keeps the people's files and takes only what the agent owns.
func TestAssembleTypeScript(t *testing.T) {
	src, ws, dst := t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "out")
	m := project.Manifest{Name: "buf", Module: ".invariant/specs/Buf.tla", Code: "src", Language: "typescript", Conformance: "conformance.ts", Exhaustive: true}
	write := func(dir string, files map[string]string) {
		for name, text := range files {
			if err := writeFile(filepath.Join(dir, name), text); err != nil {
				t.Fatal(err)
			}
		}
	}
	write(src, map[string]string{".invariant/ratified.lock": "lock", ".invariant/request.md": "req", "package.json": `{"name": "buf"}`, ".invariant/specs/Buf.tla": "draft"})
	write(ws, map[string]string{".invariant/ratified.lock": "tampered", ".invariant/request.md": "req", "package.json": `{"dependencies": {"left-pad": "1"}}`,
		".invariant/specs/Buf.tla": "model", "src/machine.ts": "code", "src/machine.test.ts": "test", "conformance.ts": "driver", "NOTES.md": "stray"})
	p := &project.Project{Dir: src, Manifest: m}
	if err := Assemble(p, ws, dst); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{".invariant/ratified.lock": "lock", "package.json": `{"name": "buf"}`, ".invariant/specs/Buf.tla": "model",
		"src/machine.ts": "code", "src/machine.test.ts": "test", "conformance.ts": "driver"} {
		if b, err := os.ReadFile(filepath.Join(dst, name)); err != nil || string(b) != want {
			t.Errorf("%s = %q, %v; want %q", name, b, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "NOTES.md")); err == nil {
		t.Error("a file the agent doesn't own must not reach the project")
	}
	if got, _ := Tampered(p, ws); !reflect.DeepEqual(got, []string{".invariant/ratified.lock", "package.json"}) {
		t.Errorf("tampered = %v", got)
	}
}

func TestPromptByLanguage(t *testing.T) {
	lock := project.Lock{Bounds: map[string]string{"Cap": "2"}, Statements: []project.Statement{{Name: "Spec", Kind: project.Spec, Says: "s"}}}
	for lang, wants := range map[string][]string{
		"typescript": {"TypeScript code", "Node 24 runs", "`src/machine.ts`", "`conformance.ts`", `{"$set": [...]}`, "every state TLC finds", "and package.json are protected", "node --test"},
		"python":     {"Python code", "`buffer/core.py`", "`# +nagini`", "`test_buffer.py`", "`conformance.py`", "# Nagini, briefly", "Acc(list_pred(", `{"$mv": "p1"}`},
	} {
		m := project.Manifest{Module: ".invariant/specs/Buf.tla", Code: map[string]string{"typescript": "src", "python": "buffer"}[lang], Language: lang,
			Conformance: map[string]string{"typescript": "conformance.ts", "python": "conformance.py"}[lang], Exhaustive: true}
		got := Prompt(&project.Project{Manifest: m, Lock: lock}, "---- MODULE Buf ----\n====\n", "# Add a buffer", 4, true, false)
		for _, want := range wants {
			if !strings.Contains(got, want) {
				t.Errorf("%s prompt lacks %q", lang, want)
			}
		}
		for _, unwanted := range []string{"Gobra", "go.mod", "Successors(s State)"} {
			if strings.Contains(got, unwanted) {
				t.Errorf("%s prompt mentions %q", lang, unwanted)
			}
		}
	}
}

// A project that checks existing code owns its model, its driver and the
// driver's helpers. The copies of the code it reads, and dependencies, never
// come back from the agent (D-0054).
func TestOwnedExisting(t *testing.T) {
	m := project.Manifest{Module: ".invariant/specs/Leases.tla", Code: ".", Language: "typescript", Conformance: "conformance.ts", Existing: []string{"src/lib"}}
	for rel, want := range map[string]bool{
		".invariant/specs/Leases.tla": true,
		"conformance.ts":              true,
		"harness.ts":                  true,
		"lib/steps.ts":                true,
		".invariant/ratified.lock":    false,
		".invariant/invariant.json":   false,
		"existing/src/lib/store.ts":   false,
		"package.json":                false,
		"node_modules/zod/index.js":   false,
		"lib/node_modules/x/index.js": false,
	} {
		if got := Owned(m, rel); got != want {
			t.Errorf("Owned(%s) = %v, want %v", rel, got, want)
		}
	}
}

// Every gate run stages the package around a project that checks existing
// code: the package's files and the named code come from the package, the
// project sits at its place in it, and the agent's copy of the code is
// ignored.
func TestStageExisting(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"package.json":      `{"name": "app"}`,
		"package-lock.json": `{"lockfileVersion": 3}`,
		"src/lib/store.ts":  "export class Store {}",
		"src/app/page.tsx":  "not named, not staged",
		"invariant/leases/.invariant/invariant.json":   `{"name": "leases", "module": ".invariant/specs/Leases.tla", "code": ".", "language": "typescript", "conformance": "conformance.ts", "existing": ["src/lib"]}`,
		"invariant/leases/.invariant/ratified.lock":    `{"decision": "test", "bounds": {"N": "1"}, "statements": [{"name": "Spec", "kind": "spec", "says": "s", "sha256": ""}, {"name": "TypeOK", "kind": "invariant", "says": "t", "sha256": ""}]}`,
		"invariant/leases/.invariant/specs/Leases.tla": "---- MODULE Leases ----\n====\n",
	}
	for name, text := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(text), 0o644)
	}
	p, err := project.Load(filepath.Join(root, "invariant", "leases"))
	if err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	for name, text := range map[string]string{
		".invariant/specs/Leases.tla": "---- MODULE Leases ----\n\\* the agent's model\n====\n",
		"conformance.ts":              "import { Store } from \"../../src/lib/store\";\n",
		"existing/src/lib/store.ts":   "export class Store { hacked = true }",
		"package.json":                `{"dependencies": {"left-pad": "1"}}`,
	} {
		p := filepath.Join(ws, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(text), 0o644)
	}
	dir := t.TempDir()
	proj, err := Stage(p, ws, dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "invariant", "leases"); proj != want {
		t.Fatalf("staged at %s, want %s", proj, want)
	}
	read := func(rel string) string {
		b, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		return string(b)
	}
	if got := read("src/lib/store.ts"); got != "export class Store {}" {
		t.Errorf("the staged code is %q; it must come from the package", got)
	}
	if read("src/app/page.tsx") != "" {
		t.Error("staged code the project doesn't name")
	}
	if got := read("invariant/leases/conformance.ts"); !strings.Contains(got, "src/lib/store") {
		t.Errorf("the driver is %q", got)
	}
	if !strings.Contains(read("invariant/leases/.invariant/specs/Leases.tla"), "the agent's model") {
		t.Error("the agent's model didn't reach the staged project")
	}
	if read("invariant/leases/existing/src/lib/store.ts") != "" || read("invariant/leases/package.json") != "" {
		t.Error("the agent's copy of the code, or its package.json, reached the gate")
	}
	if got := read("package.json"); got != `{"name": "app"}` {
		t.Errorf("the package's manifest is %q", got)
	}
}

// A driver that skips a step the model rules out hides the bug the check
// exists to find, as a driver did on copythis-ad#33 (D-0059). The prompt
// says so.
func TestExistingPromptForbidsSkippingSteps(t *testing.T) {
	root := t.TempDir()
	for name, text := range map[string]string{
		"package.json":      `{"name": "app"}`,
		"package-lock.json": `{"lockfileVersion": 3}`,
		"invariant/leases/.invariant/invariant.json": `{"name": "leases", "module": ".invariant/specs/Leases.tla", "code": ".", "language": "typescript", "conformance": "conformance.ts", "existing": ["src/lib"]}`,
		"invariant/leases/.invariant/ratified.lock":  `{"decision": "test", "bounds": {"N": "1"}, "statements": [{"name": "Spec", "kind": "spec", "says": "s", "sha256": ""}, {"name": "TypeOK", "kind": "invariant", "says": "t", "sha256": ""}]}`,
	} {
		p := filepath.Join(root, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(text), 0o644)
	}
	p, err := project.Load(filepath.Join(root, "invariant", "leases"))
	if err != nil {
		t.Fatal(err)
	}
	pr := Prompt(p, "", "request", 4, true, false)
	for _, want := range []string{"Never skip an operation because of the state the code is in", "`../../src/lib/...`", "The bounds are the only reason to skip"} {
		if !strings.Contains(pr, want) {
			t.Errorf("the existing-code prompt lacks %q", want)
		}
	}
}
