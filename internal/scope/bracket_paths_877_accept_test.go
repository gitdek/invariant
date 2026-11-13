package scope

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/repository"
)

func TestScopeKeepsBracketPathsAndTrustExact877(t *testing.T) {
	for _, mode := range []string{"edit", "delete", "neighbor edit", "trusted rule edit", "trusted rule deletion"} {
		t.Run(mode, func(t *testing.T) {
			root := repo(t)
			commands := map[string]repository.Command{}
			for _, phase := range []string{"install", "format", "lint", "build"} {
				commands[phase] = repository.Command{NotApplicable: "Scope fixture does not run commands."}
			}
			commands["test"] = repository.Command{Argv: []string{"vitest", "run"}}
			commands["test_one"] = repository.Command{Argv: []string{"vitest", "run", "-t", "{test}"}}
			config := repository.Config{Version: 1,
				Parts:      []repository.Part{{Name: "app", Directory: "app", Language: "typescript", Image: "node@sha256:" + strings.Repeat("a", 64), Inputs: []string{}, Lockfiles: []string{}, Commands: commands}},
				AgentRules: []string{}, CurrentState: []string{}, TrustedPaths: []string{}, SharedPaths: []repository.SharedPath{}}
			data, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			const route = "app/[slug]/page.tsx"
			const neighbor = "app/s/page.tsx"
			const rule = "app/[slug]/AGENTS.md"
			write(t, root, map[string]string{repository.Path: string(data), route: "route base\n", neighbor: "neighbor base\n", rule: "captured rule\n"})
			commit(t, root, "configured route base")
			gitRun(t, root, "branch", "bracket-base")
			snapshot, err := repository.Load(t.Context(), root, "bracket-base")
			if err != nil {
				t.Fatal(err)
			}
			files := []string{route}
			if strings.HasPrefix(mode, "trusted") {
				files = append(files, rule)
			}
			const accept = "app/route_accept.test.ts"
			draft := map[string]any{"name": "change the route", "summary": "Change the literal route.", "files": files,
				"tests": []map[string]string{{"name": "route ready", "part": "app", "framework": "vitest", "file": accept, "selector": "route ready", "says": "The route is ready."}}}
			data, err = json.Marshal(draft)
			if err != nil {
				t.Fatal(err)
			}
			ws := t.TempDir()
			write(t, ws, map[string]string{"plan.json": string(data), "tests/" + accept: "import {test, expect} from 'vitest';\ntest('route ready', () => { expect(1).toBe(2); });\n"})
			plan, err := plumbing.ReadRepositoryDraft(t.Context(), ws, snapshot)
			if err != nil {
				t.Fatalf("cannot ratify a plan for the literal bracketed route: %v", err)
			}
			lock := plumbing.Lock{Plan: *plan, Ratified: plumbing.Ratification{Issue: 7, Proposal: plan.Hash()}}
			if err := lock.Write(root); err != nil {
				t.Fatal(err)
			}
			write(t, root, map[string]string{route: "route candidate\n"})
			switch mode {
			case "delete":
				if err := os.Remove(filepath.Join(root, route)); err != nil {
					t.Fatal(err)
				}
			case "neighbor edit":
				write(t, root, map[string]string{neighbor: "outside the plan\n"})
			case "trusted rule edit":
				write(t, root, map[string]string{rule: "changed rule\n"})
			case "trusted rule deletion":
				if err := os.Remove(filepath.Join(root, rule)); err != nil {
					t.Fatal(err)
				}
			}
			commit(t, root, "candidate")
			result, err := Check(t.Context(), root, "bracket-base", "HEAD", 7)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "neighbor edit" {
				if result.OK() || !strings.Contains(strings.Join(result.Problems, "\n"), neighbor) {
					t.Fatalf("scope admitted or misidentified the unplanned neighbor: %+v", result)
				}
			} else if !result.OK() {
				t.Fatalf("scope refused the literal bracketed path: %+v", result)
			}
			if slices.Contains(result.Files, neighbor) != (mode == "neighbor edit") || !slices.Contains(result.Files, route) {
				t.Errorf("scope's diff lost literal file identity: %v", result.Files)
			}
			if strings.HasPrefix(mode, "trusted") {
				if !slices.Equal(result.Trusted, []string{rule}) {
					t.Errorf("nested bracketed rule lost the captured trust boundary: %v", result.Trusted)
				}
			} else if len(result.Trusted) != 0 {
				t.Errorf("ordinary route acquired trust: %v", result.Trusted)
			}
		})
	}
}
