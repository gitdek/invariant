package plumbing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const graphTest = `package dashboard

import "testing"

func TestGraphShowsEveryDecision(t *testing.T) {
	if got := graph(); got != 3 {
		t.Fatalf("graph() = %d", got)
	}
}
`

func plan() *Plan {
	return &Plan{
		Name:    "the decision graph",
		Summary: "Draw the decision graph on the dashboard.",
		Files:   []string{"internal/dashboard/web/app.js", "./internal/dashboard/graph.go"},
		Tests:   []Test{{Name: "TestGraphShowsEveryDecision", File: "internal/dashboard/graph_accept_test.go", Says: "Every decision is a node."}},
		Sources: map[string]string{"internal/dashboard/graph_accept_test.go": graphTest},
	}
}

// A plan that holds together validates, with its files cleaned and sorted,
// and its hash doesn't depend on the order it lists them in.
func TestAPlanValidatesAndHashesTheSameInAnyOrder(t *testing.T) {
	p := plan()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(p.Files, ",") != "internal/dashboard/graph.go,internal/dashboard/web/app.js" || len(p.Trusted) != 0 {
		t.Errorf("files %v, trusted %v", p.Files, p.Trusted)
	}
	q := plan()
	q.Files[0], q.Files[1] = q.Files[1], q.Files[0]
	if err := q.Validate(); err != nil || q.Hash() != p.Hash() {
		t.Errorf("the order changed the hash: %s, %s, %v", p.Hash(), q.Hash(), err)
	}
	r := plan()
	r.Sources["internal/dashboard/graph_accept_test.go"] = strings.Replace(graphTest, "!= 3", "!= 4", 1)
	if r.Validate() != nil || r.Hash() == p.Hash() {
		t.Error("changing an acceptance test must change the plan's hash")
	}
}

// A plan can't reach where it mustn't, can't let the build change its tests,
// and its tests must be real test functions in the files it gives.
func TestAPlanThatBreaksTheRulesIsRefused(t *testing.T) {
	for name, change := range map[string]func(*Plan){
		"outside":        func(p *Plan) { p.Files = append(p.Files, "../elsewhere.go") },
		"ci":             func(p *Plan) { p.Files = append(p.Files, ".github/workflows/gate.yml") },
		"plan records":   func(p *Plan) { p.Files = append(p.Files, ".invariant/plans/issue-3.json") },
		"tests writable": func(p *Plan) { p.Files = append(p.Files, "internal/dashboard/graph_accept_test.go") },
		"no tests":       func(p *Plan) { p.Tests = nil },
		"bad name":       func(p *Plan) { p.Tests[0].Name = "graphTest" },
		"not a test file": func(p *Plan) {
			p.Tests[0].File = "internal/dashboard/graph_accept.go"
			p.Sources = map[string]string{"internal/dashboard/graph_accept.go": graphTest}
		},
		"undeclared":  func(p *Plan) { p.Tests[0].Name = "TestSomethingElse" },
		"no text":     func(p *Plan) { p.Sources = map[string]string{} },
		"extra text":  func(p *Plan) { p.Sources["internal/other_test.go"] = graphTest },
		"says":        func(p *Plan) { p.Tests[0].Says = " " },
		"unparseable": func(p *Plan) { p.Sources["internal/dashboard/graph_accept_test.go"] = "package dashboard\nfunc (" },
	} {
		p := plan()
		change(p)
		if err := p.Validate(); err == nil {
			t.Errorf("%s: the plan validated", name)
		}
	}
}

// A plan that touches the trusted base says so, so a person merges it.
func TestAPlanNamesWhatItTouchesInTheTrustedBase(t *testing.T) {
	p := plan()
	p.Files = append(p.Files, "internal/dashboard/act.go", "internal/factory/factory.go", "docs/PRD.md")
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(p.Trusted, ",") != "internal/dashboard/act.go,internal/factory/factory.go" {
		t.Errorf("trusted = %v", p.Trusted)
	}
	for f, want := range map[string]bool{"go.mod": true, "cmd/invariant/main.go": true, "internal/dashboard/graph.go": false, "README.md": false, ".github/x": true,
		".mcp.json": true, ".codex/config.toml": true, ".claude/settings.json": true, ".mcp.json.example": false} {
		if Trusted(f) != want {
			t.Errorf("Trusted(%s) = %v", f, !want)
		}
	}
}

// An agent's draft is read from its workspace, and a ratified plan's record
// round-trips, refusing a plan that isn't the one ratified.
func TestADraftIsReadAndARecordRoundTrips(t *testing.T) {
	ws := t.TempDir()
	write := func(p, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(ws, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ws, p), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("plan.json", `{"name":"the graph","summary":"Draw the graph.","files":["internal/dashboard/graph.go"],"tests":[{"name":"TestGraphShowsEveryDecision","file":"internal/dashboard/graph_accept_test.go","says":"Every decision is a node."}]}`)
	write("tests/internal/dashboard/graph_accept_test.go", graphTest)
	p, err := ReadDraft(ws)
	if err != nil {
		t.Fatal(err)
	}
	if p.Sources["internal/dashboard/graph_accept_test.go"] != graphTest {
		t.Errorf("sources = %v", p.Sources)
	}
	write("plan.json", `{"name":"x","summary":"x","files":["a.go"],"tests":[],"trusted":["a.go"]}`)
	if _, err := ReadDraft(ws); err == nil {
		t.Error("a draft that sets trusted itself was read")
	}

	root := t.TempDir()
	l := Lock{Ratified: Ratification{By: "gitdek", Issue: 92, Comment: "https://github.com/gitdek/invariant/issues/92#issuecomment-1", Proposal: p.Hash()}, Plan: *p}
	if err := l.Write(root); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, LockPath(92)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLock(b); err != nil {
		t.Errorf("the record didn't read back: %v", err)
	}
	if src, _ := os.ReadFile(filepath.Join(root, "internal/dashboard/graph_accept_test.go")); string(src) != graphTest {
		t.Error("the acceptance test wasn't written where the plan puts it")
	}
	if LockIssue(LockPath(92)) != 92 || LockIssue("internal/x.json") != 0 {
		t.Error("LockIssue")
	}
	tampered := strings.Replace(string(b), "Draw the graph.", "Draw a different graph.", 1)
	if _, err := ReadLock([]byte(tampered)); err == nil {
		t.Error("a record whose plan isn't the one ratified was read")
	}
}
