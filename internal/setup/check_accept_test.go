package setup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pinnedCommit is a full commit of Invariant for the gate workflow to pin.
const pinnedCommit = "61eb0495cd8a5ff84be5bbdb4efc3bb2b7c1ae23"

// testsOnly is a CI workflow with no invariant/gate job.
const testsOnly = `name: ci

on: [push, pull_request]

jobs:
  test:
    name: test
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - run: go test ./...
`

// namedForTheGate is a workflow named invariant/gate whose one job isn't. A
// check run takes its job's name, not its workflow's.
const namedForTheGate = `name: invariant/gate

on: [pull_request]

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - run: make
`

// onGitHub is a repository as GitHub shows it: the workflows on each of its
// branches, by path, and whether it allows merge commits.
type onGitHub struct {
	workflows    map[string]map[string]string
	mergeCommits bool
}

func (g *onGitHub) Workflows(_ context.Context, ref string) (map[string]string, error) {
	return g.workflows[ref], nil
}

func (g *onGitHub) MergeCommits(context.Context) (bool, error) { return g.mergeCommits, nil }

// writtenByInit is the gate workflow invariant init writes.
func writtenByInit(t *testing.T) string {
	t.Helper()
	w, err := Workflow(pinnedCommit)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// The factory merges only once invariant/gate passes, so without a job by
// that name on the branch its pull requests merge into, they would wait
// forever. The check refuses, and says to run invariant init.
func TestCheckRefusesARepositoryWithNoGateJob(t *testing.T) {
	gate := writtenByInit(t)
	for _, c := range []struct {
		name      string
		workflows map[string]string
	}{
		{"no workflows at all", nil},
		{"a workflow whose jobs are others", map[string]string{".github/workflows/ci.yml": testsOnly}},
		{"a workflow named invariant/gate, whose job isn't", map[string]string{".github/workflows/gate.yml": namedForTheGate}},
	} {
		// The gate is on another branch, but not on main, where pull requests
		// merge.
		g := &onGitHub{workflows: map[string]map[string]string{"main": c.workflows, "wip": {WorkflowPath: gate}}, mergeCommits: true}
		err := Check(context.Background(), g, "acme/widgets", "main")
		if err == nil {
			t.Errorf("%s: the check accepted a repository whose base branch runs no invariant/gate job", c.name)
			continue
		}
		if !strings.Contains(err.Error(), "invariant init") {
			t.Errorf("%s: the refusal doesn't say to run invariant init: %v", c.name, err)
		}
	}
}

// The factory merges with a merge commit, which GitHub refuses where only
// squash or rebase merges are allowed. The check refuses, and gives the
// command that allows merge commits.
func TestCheckRefusesARepositoryWithoutMergeCommits(t *testing.T) {
	gate := writtenByInit(t)
	g := &onGitHub{workflows: map[string]map[string]string{"main": {WorkflowPath: gate}}}
	err := Check(context.Background(), g, "acme/widgets", "main")
	if err == nil {
		t.Fatal("the check accepted a repository that doesn't allow merge commits")
	}
	for _, want := range []string{"gh api", "repos/acme/widgets", "allow_merge_commit=true"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal lacks %q, from the command that allows merge commits: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "invariant init") {
		t.Errorf("the base branch has the gate, but the refusal asks for it: %v", err)
	}

	// Missing both, the refusal names both fixes, so a person doesn't have to
	// start the factory twice to learn the second.
	g.workflows = map[string]map[string]string{"main": {".github/workflows/ci.yml": testsOnly}}
	err = Check(context.Background(), g, "acme/widgets", "main")
	if err == nil || !strings.Contains(err.Error(), "invariant init") || !strings.Contains(err.Error(), "allow_merge_commit=true") {
		t.Errorf("with neither the gate nor merge commits, the refusal should name both fixes: %v", err)
	}
}

// With a job named invariant/gate on the base branch, in whichever workflow
// file holds it, and merge commits allowed, the check lets the factory start.
// Invariant's own workflow passes too, so the factory still starts here.
func TestCheckAcceptsARepositoryWithTheGateAndMergeCommits(t *testing.T) {
	gate := writtenByInit(t)
	own, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "gate.yml"))
	if err != nil {
		t.Fatal(err)
	}
	quoted := strings.Replace(testsOnly, "name: test", "name: 'invariant/gate'", 1)
	for _, c := range []struct {
		name      string
		workflows map[string]string
	}{
		{"the workflow init writes", map[string]string{WorkflowPath: gate}},
		{"Invariant's own workflow", map[string]string{".github/workflows/gate.yml": string(own), ".github/workflows/other.yml": testsOnly}},
		{"a quoted job name in a .yaml file", map[string]string{".github/workflows/ci.yml": testsOnly, ".github/workflows/checks.yaml": quoted}},
	} {
		g := &onGitHub{workflows: map[string]map[string]string{"trunk": c.workflows}, mergeCommits: true}
		if err := Check(context.Background(), g, "acme/widgets", "trunk"); err != nil {
			t.Errorf("%s: the check refused a repository with the gate and merge commits: %v", c.name, err)
		}
	}
}
