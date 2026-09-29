package scope

import (
	"context"
	"testing"
)

// A factory pull request can't move an existing project to another coding
// agent (#173). The manifest's agent is a field of it like any other, and
// the factory may change none of them but its code's parameters (D-0086):
// adding the agent, changing it and taking it out are each out of scope,
// alone or beside a change to the parameters. A new project's manifest may
// name its agent.
func TestAPullRequestCantChangeAProjectsAgent(t *testing.T) {
	const problem = "it changes the project's manifest beyond its code's parameters"
	for _, c := range []struct {
		name   string
		before string
		after  string
	}{
		{"added", `{"name": "old", "language": "go"}`, `{"name": "old", "language": "go", "agent": "codex"}`},
		{"added with parameters", `{"name": "old", "language": "go"}`, `{"name": "old", "language": "go", "agent": "codex", "parameters": ["Capacity"]}`},
		{"changed", `{"name": "old", "language": "go", "agent": "claude-code"}`, `{"name": "old", "language": "go", "agent": "codex"}`},
		{"taken out", `{"name": "old", "language": "go", "agent": "codex"}`, `{"name": "old", "language": "go", "parameters": ["Capacity"]}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := repo(t)
			gitRun(t, dir, "checkout", "-q", "main")
			write(t, dir, map[string]string{"examples/01-old/.invariant/invariant.json": c.before})
			commit(t, dir, "the project as it is")
			gitRun(t, dir, "checkout", "-q", "-B", "invariant/issue-7-new")
			write(t, dir, map[string]string{
				"examples/01-old/.invariant/invariant.json": c.after,
				"examples/01-old/old/old.go":                "package old\n\n// Changed.\n",
			})
			commit(t, dir, "amend")
			r, err := Check(context.Background(), dir, "main", "HEAD", 7)
			if err != nil {
				t.Fatal(err)
			}
			if r.OK() || !contains(r.Problems, problem) {
				t.Errorf("the manifest %s, then %s: problems = %q; want %q", c.before, c.after, r.Problems, problem)
			}
		})
	}

	dir := repo(t)
	write(t, dir, newProject(map[string]string{"examples/02-new/.invariant/invariant.json": `{"name": "new", "agent": "codex"}`}))
	commit(t, dir, "new project")
	r, err := Check(context.Background(), dir, "main", "HEAD", 7)
	if err != nil {
		t.Fatal(err)
	}
	if !r.OK() || !r.New || r.Project != "examples/02-new" {
		t.Errorf("a new project whose manifest names its agent should be in scope: %+v", r)
	}
}
