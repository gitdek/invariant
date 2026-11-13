package plumbing

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
)

const bracketRoute877 = "app/[slug]/page.tsx"
const bracketNeighbor877 = "app/s/page.tsx"

func TestNativePlanDigestKeepsBracketPathsLiteral877(t *testing.T) {
	root, _, _ := fixture484(t, []repository.Part{n503Part("typescript", "app")}, nil)
	write468(t, root, bracketRoute877, "route base\n")
	write468(t, root, bracketNeighbor877, "neighbor base\n")
	git468(t, root, "add", "--all")
	git468(t, root, "commit", "-qm", "route fixtures")
	snapshot, err := repository.Load(t.Context(), root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	const source = "import {test, expect} from 'vitest';\ntest('route is ready', () => { expect(1).toBe(2); });\n"
	p := n503Plan(t, "vitest", "app", "app/route_accept.test.ts", "route is ready", source)
	p.Files = []string{bracketRoute877}
	bound, err := ReadRepositoryDraft(t.Context(), n503Draft(t, p), snapshot)
	if err != nil {
		t.Fatalf("a native plan must bind an ordinary bracketed production path: %v", err)
	}
	if bound.Execution == nil || bound.Execution.Digest == "" || !slices.Equal(bound.Files, []string{bracketRoute877}) {
		t.Fatalf("binding lost the literal path or execution identity: %+v", bound)
	}
	if err := bound.ValidateRepository(t.Context(), snapshot); err != nil {
		t.Fatal(err)
	}
	other := n503Clone(t, p)
	other.Files = []string{bracketNeighbor877}
	neighbor := n503Read(t, other, snapshot)
	if bound.Execution.Digest == neighbor.Execution.Digest || bound.Hash() == neighbor.Hash() {
		t.Fatal("the bracketed file and its glob neighbor share a plan identity")
	}
	tampered := n503Clone(t, bound)
	tampered.Files = []string{bracketNeighbor877}
	if err := tampered.ValidateRepository(t.Context(), snapshot); err == nil {
		t.Fatal("replacing the route with its glob neighbor retained authority")
	}
	// Native acceptance files still reach pattern-based runner includes.
	// This applies to either bracket separately as well as balanced pairs.
	for _, file := range []string{"app/[slug]/route.test.ts", "app/open[/route.test.ts", "app/close]/route.test.ts"} {
		bad := n503Clone(t, p)
		bad.Tests[0].File = file
		bad.Sources = map[string]string{file: source}
		if _, err := ReadRepositoryDraft(t.Context(), n503Draft(t, bad), snapshot); err == nil {
			t.Errorf("native acceptance admitted bracketed filename %q", file)
		}
	}
}

func TestBracketPlanStagesDiffsAndRunsOnlyItsOwner877(t *testing.T) {
	root, candidate, _ := fixture484(t, []repository.Part{part484("typescript", "app"), part484("typescript", "app/s")}, nil)
	for _, dir := range []string{root, candidate} {
		write468(t, dir, bracketRoute877, "route base\n")
		write468(t, dir, bracketNeighbor877, "neighbor base\n")
	}
	git468(t, root, "add", "--all")
	git468(t, root, "commit", "-qm", "route fixtures")
	snapshot, err := repository.Load(t.Context(), root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	plan := &Plan{Files: []string{bracketRoute877}}
	write468(t, candidate, bracketRoute877, "route candidate\n")
	write468(t, candidate, bracketNeighbor877, "unplanned neighbor edit\n")
	changes, err := Diff(t.Context(), root, candidate, plan)
	if err != nil || !slices.Equal(changes.Written, []string{bracketRoute877}) || !slices.Equal(changes.Outside, []string{bracketNeighbor877}) {
		t.Fatalf("diff expanded the plan path: %+v, %v", changes, err)
	}
	staged := t.TempDir()
	if err := Stage(t.Context(), root, candidate, plan, staged); err != nil {
		t.Fatal(err)
	}
	runner := newRunner484(t)
	runner.hook = func(_ context.Context, q DependencyRequest, _ []string) ([]byte, error) {
		if q.Directory != "app" {
			return nil, fmt.Errorf("ran neighbor's checks in %s", q.Directory)
		}
		for file, want := range map[string]string{bracketRoute877: "route candidate\n", bracketNeighbor877: "neighbor base\n"} {
			got, err := os.ReadFile(filepath.Join(q.SourceDir, filepath.FromSlash(file)))
			if err != nil || string(got) != want {
				return nil, fmt.Errorf("staged %s = %q, %v; want %q", file, got, err, want)
			}
		}
		return nil, nil
	}
	result, err := sandbox484(t, root, snapshot, runner).Run(t.Context(), staged, plan)
	if err != nil || !result.Passed() {
		t.Fatalf("sandbox refused the literal route: %v\n%s\n%s", err, result.Summary(), result.Output)
	}
	if len(runner.opens) != 1 || len(runner.calls) != 4 {
		t.Fatalf("want four checks of only the route owner: opens=%d calls=%d", len(runner.opens), len(runner.calls))
	}
	closed484(t, runner)
	for file, want := range map[string]string{bracketRoute877: "route base\n", bracketNeighbor877: "neighbor base\n"} {
		if got := read484(t, root, file); got != want {
			t.Errorf("sandbox changed the original %s: %q", file, got)
		}
	}
}

func TestSandboxFileListUsesLiteralBracketDirectories877(t *testing.T) {
	root := t.TempDir()
	for file, body := range map[string]string{
		"app/[slug]/page.go":       "package route\n",
		"app/[slug]/route_test.go": "package route\n",
		"app/s/page.go":            "package neighbor\n",
		"app/s/neighbor_test.go":   "package neighbor\n",
	} {
		write468(t, root, file, body)
	}
	plan := &Plan{Files: []string{"app/[slug]/page.go"}}
	got, err := goFiles(t.Context(), "", root, plan)
	want := []string{"app/[slug]/page.go", "app/[slug]/route_test.go"}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("sandbox Go file list = %v, %v; want %v", got, err, want)
	}
}
