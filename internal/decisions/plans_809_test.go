package decisions

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// The fixture's plan IDs are put together here, so that none of this file's
// own lines names a plan: the graph reads a test's source as code, and a
// plan's ID in code is a claim to implement it.
var (
	p7     = PlanID(7)
	p8     = PlanID(8)
	p788   = PlanID(788)
	p790   = PlanID(790)
	p791   = PlanID(791)
	p12345 = PlanID(12345)
)

// plan809 writes an issue's plan file as a ratification commits it. An empty
// by leaves the ratification out, as a plan nobody ratified has none.
func plan809(t *testing.T, repo Repo, issue int, by, name, summary string) {
	t.Helper()
	plan := map[string]any{"plan": map[string]any{"name": name, "summary": summary, "files": []string{"a.go"}, "tests": []any{}, "sources": map[string]string{}}}
	if by != "" {
		plan["ratified"] = map[string]any{"by": by, "at": "2026-11-10T15:57:00Z", "issue": fmt.Sprint(issue),
			"comment":  fmt.Sprintf("https://github.com/o/r/issues/%d#issuecomment-1", issue),
			"proposal": "sha256:" + strings.Repeat(fmt.Sprint(issue%10), 64)}
	}
	b, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(repo.Dir, ".invariant", "plans", fmt.Sprintf("issue-%d.json", issue)), string(b))
}

// A ratified plan is a node of the graph, named for its issue, with its
// ratifier, date, proposal and words. What names it rests on it, by its
// file's path in prose and by its ID anywhere, and it cites what its own
// words name (#809).
func TestRatifiedPlansAreNodes809(t *testing.T) {
	s, repo := fixture(t)
	for _, d := range []NewDecision{
		{Text: "Refuse a test that can't run."},
		{Text: "Say why, under " + p788 + "."},
	} {
		d.Door, d.Status, d.Who = "two-way", "decided", "agent"
		if _, err := s.Decide(repo, d, "agent"); err != nil {
			t.Fatal(err)
		}
	}
	plan809(t, repo, 788, "gitdek", "Explain refusals.", "List every diagnostic, refining D-0001.")
	plan809(t, repo, 12345, "gitdek", "A later plan", "Builds on "+p788+".")
	plan809(t, repo, 7, "", "Nobody ratified this", "It records nothing yet.")
	plan809(t, repo, 8, "invariant-code-factory[bot]", "A bot can't ratify", "Only a person does.")
	write(t, filepath.Join(repo.Dir, ".invariant", "plans", "notes.json"), "{}")
	write(t, filepath.Join(repo.Dir, "SPEC.md"), "# Spec\n\n- Under [#788's ratified plan](.invariant/plans/issue-788.json), refusals say why.\n")
	write(t, filepath.Join(repo.Dir, "docs", "factory.md"), "See [the plan](../.invariant/plans/issue-788.json) and "+p12345+".\n")
	write(t, filepath.Join(repo.Dir, "runner", "runner.go"), "package runner\n\n// report lists diagnostics ("+p788+").\nfunc report() {}\n")
	// A plan's path in code is data, such as a fixture, and names nothing.
	write(t, filepath.Join(repo.Dir, "runner", "runner_test.go"), "package runner\n\nconst fixture = \".invariant/plans/issue-3.json\"\n")
	if err := s.Rebuild([]Repo{repo}); err != nil {
		t.Fatal(err)
	}

	n, err := s.Get("demo/" + p788)
	if err != nil {
		t.Fatal(err)
	}
	want := Node{ID: "demo/" + p788, Kind: "plan", Project: "demo", Date: "2026-11-10", Status: "ratified", Who: "@gitdek",
		Text: "#788's ratified plan: Explain refusals. List every diagnostic, refining D-0001.", Record: ".invariant/plans/issue-788.json",
		Proposal: "sha256:" + strings.Repeat("8", 64)}
	if n != want {
		t.Errorf("the plan's node = %+v\nwant %+v", n, want)
	}
	if plans, err := s.PlanCount("demo"); err != nil || plans != 2 {
		t.Errorf("ratified plans = %d, %v; want the two a person ratified", plans, err)
	}
	for _, id := range []string{"demo/" + p7, "demo/" + p8} {
		if _, err := s.Get(id); err == nil {
			t.Errorf("%s is a node, and nobody ratified it", id)
		}
	}
	if decisions, _, err := s.Count("demo"); err != nil || decisions != 2 {
		t.Errorf("decisions = %d, %v; a plan isn't counted as one", decisions, err)
	}

	deps, err := s.Dependents("demo/" + p788)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(ids(deps), ","), "demo/runner/runner.go:3,demo/D-0002,demo/docs/factory.md:1,demo/"+p12345+",demo/SPEC.md:3"; got != want {
		t.Errorf("dependents of the plan = %s\nwant %s", got, want)
	}
	code, _ := s.Implementers("demo/" + p788)
	if len(code) != 1 || code[0].ID != "demo/runner/runner.go:3" {
		t.Errorf("implementers of the plan = %v", ids(code))
	}
	grounds, _ := s.Grounds("demo/" + p12345)
	if got := strings.Join(ids(grounds), ","); got != "demo/"+p788 {
		t.Errorf("grounds of the later plan = %s", got)
	}
	grounds, _ = s.Grounds("demo/" + p788)
	if got := strings.Join(ids(grounds), ","); got != "demo/D-0001" {
		t.Errorf("grounds of the plan = %s", got)
	}
	found, _ := s.Search("every DIAGNOSTIC")
	if len(found) != 1 || found[0].ID != "demo/"+p788 {
		t.Errorf("search = %v", ids(found))
	}
	if all, _ := s.Decisions("demo"); len(all) != 2 {
		t.Errorf("the project's decisions = %v; plans aren't listed among them", ids(all))
	}
	if problems, err := s.Check([]Repo{repo}); err != nil || len(problems) != 0 {
		t.Errorf("the check: %v, %v", problems, err)
	}
}

// A SPEC bullet that names only a ratified plan is traced. A line that names
// a plan the repository doesn't hold is reported, and so is a plan whose
// file doesn't parse.
func TestTheCheckTracesToPlans809(t *testing.T) {
	if got := Untraced("# A\n\n- Under [#788's ratified plan](.invariant/plans/issue-788.json), it says why.\n  - under it\n- By `" + p788 + "` too.\n- alone\n"); len(got) != 1 || got[0] != "- alone" {
		t.Errorf("untraced = %v", got)
	}
	s, repo := fixture(t)
	plan809(t, repo, 788, "gitdek", "Explain refusals", "")
	write(t, filepath.Join(repo.Dir, "SPEC.md"), "# Spec\n\n- Under [#788's ratified plan](.invariant/plans/issue-788.json).\n- Under [#790's ratified plan](.invariant/plans/issue-790.json).\n")
	write(t, filepath.Join(repo.Dir, "docs", "notes.md"), "As "+p791+" says.\n")
	if err := s.Rebuild([]Repo{repo}); err != nil {
		t.Fatal(err)
	}
	problems, err := s.Check([]Repo{repo})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(problems, "\n")
	for _, want := range []string{"demo/SPEC.md:4 cites demo/" + p790 + ", which isn't a ratified plan the repository holds",
		"demo/docs/notes.md:1 cites demo/" + p791 + ", which isn't a ratified plan the repository holds"} {
		if !strings.Contains(got, want) {
			t.Errorf("the check missed %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, p788) || len(problems) != 2 {
		t.Errorf("the check's problems:\n%s", got)
	}
	write(t, filepath.Join(repo.Dir, ".invariant", "plans", "issue-790.json"), "{not json")
	if err := s.Rebuild([]Repo{repo}); err == nil || !strings.Contains(err.Error(), ".invariant/plans/issue-790.json") {
		t.Errorf("a plan that doesn't parse: %v", err)
	}
}

// A decision can refine or cite a ratified plan, and only one the checkout
// holds. A plan's own record is its file: no journal line ratifies,
// supersedes or links from it, and none is written for it.
func TestDecisionsRestOnPlans809(t *testing.T) {
	s, repo := fixture(t)
	plan809(t, repo, 788, "gitdek", "Explain refusals", "")
	id, err := s.Decide(repo, NewDecision{Door: "two-way", Status: "decided", Who: "agent", Text: "Clip nothing.",
		Edges: []Edge{{Refines, p788}}}, "agent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decide(repo, NewDecision{Door: "two-way", Status: "decided", Who: "agent", Text: "Rests on nothing.",
		Edges: []Edge{{Refines, p790}}}, "agent"); err == nil || !strings.Contains(err.Error(), "no ratified plan "+p790) {
		t.Errorf("a decision refining a plan the checkout doesn't hold: %v", err)
	}
	if err := s.Rebuild([]Repo{repo}); err != nil {
		t.Fatal(err)
	}
	deps, _ := s.Dependents("demo/" + p788)
	if got := strings.Join(ids(deps), ","); got != "demo/"+id {
		t.Errorf("dependents of the plan = %s", got)
	}
	for name, change := range map[string]func() error{
		"ratify":    func() error { return s.Ratify(repo, p788, "@gitdek") },
		"supersede": func() error { return s.Supersede(repo, p788, id, "agent") },
		"link":      func() error { return s.Link(repo, p788, Edge{Cites, id}, "agent") },
	} {
		if err := change(); err == nil || !strings.Contains(err.Error(), "is a ratified plan") {
			t.Errorf("%s of a plan: %v", name, err)
		}
	}
	if _, ids, err := Journals(repo); err != nil || len(ids) != 1 {
		t.Errorf("the journal holds %v, %v; a plan has no journal file", ids, err)
	}
	// A journal file named for a plan is refused, as any misnamed one is.
	write(t, filepath.Join(repo.JournalDir(), p788+".jsonl"), "")
	if _, _, err := Journals(repo); err == nil {
		t.Error("a journal file named for a plan was read as a decision's")
	}
}

// The MCP tools show a plan as they show a decision.
func TestToolsShowPlans809(t *testing.T) {
	s, repo := fixture(t)
	plan809(t, repo, 788, "gitdek", "Explain refusals", "List every diagnostic.")
	write(t, filepath.Join(repo.Dir, "SPEC.md"), "# Spec\n\n- Under [#788's ratified plan](.invariant/plans/issue-788.json).\n")
	if err := s.Rebuild([]Repo{repo}); err != nil {
		t.Fatal(err)
	}
	session, err := NewSession(s, repo)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	call := func(name, args string) string {
		t.Helper()
		for _, tool := range session.Tools(false) {
			if tool.Name == name {
				out, failed := tool.Call(context.Background(), json.RawMessage(args))
				if failed {
					t.Fatalf("%s failed: %s", name, out)
				}
				return out
			}
		}
		t.Fatalf("no tool %s", name)
		return ""
	}
	shown := call("decision", `{"id":"`+p788+`"}`)
	for _, want := range []string{"demo/" + p788 + " · plan · ratified · 2026-11-10 · @gitdek · proposal sha256:" + strings.Repeat("8", 64),
		"#788's ratified plan: Explain refusals. List every diagnostic.", "Its record: .invariant/plans/issue-788.json", "cites demo/SPEC.md:3"} {
		if !strings.Contains(shown, want) {
			t.Errorf("the plan's view lacks %q:\n%s", want, shown)
		}
	}
	if found := call("search_decisions", `{"words":"diagnostic"}`); !strings.Contains(found, "demo/"+p788+" [plan, ratified]") {
		t.Errorf("search:\n%s", found)
	}
	if deps := call("dependents", `{"id":"`+p788+`"}`); !strings.Contains(deps, "demo/SPEC.md:3 [spec]") {
		t.Errorf("dependents:\n%s", deps)
	}
}
