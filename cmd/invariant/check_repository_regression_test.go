package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
)

func TestRepositoryCIStagesCommittedCandidate(t *testing.T) {
	f := gateFixtureFor736(t, "pytest")
	f.native([]string{"worker"})
	r := f.runner()
	gateWrite736(t, f.root, "worker/main.py", "raise RuntimeError('dirty checkout')\n")
	gateWrite736(t, f.root, f.lock.Plan.Tests[0].File, "def test_ready(): pass\n")
	gateWrite736(t, f.root, repository.Path, "not configuration\n")
	gateWrite736(t, f.root, "worker/test_uncommitted.py", "raise RuntimeError('not committed')\n")
	if code, out := f.run(t.Context(), r, f.factoryArgs()...); code != 0 || len(r.accepts) != 1 {
		t.Fatalf("mutable checkout supplied CI inputs: %d\n%s", code, out)
	}
}

func TestRepositoryCIRejectsInvalidArguments(t *testing.T) {
	for _, args := range [][]string{{"missing-head"}, {"HEAD", "HEAD"}, {"-issue", "0"}, {"-issue", "-1"}, {"-base", ""}, {"--", "--help"}} {
		f := gateFixtureFor736(t, "go")
		r := f.runner()
		if code, out := f.run(t.Context(), r, args...); code == 0 || len(r.events) != 0 || strings.TrimSpace(out) == "" {
			t.Fatalf("invalid args %v reached execution: %d %v\n%s", args, code, r.events, out)
		}
	}
}

func TestRepositoryCIRefusesNonregularStaging(t *testing.T) {
	f := gateFixtureFor736(t, "go")
	if err := os.Symlink("api/main.go", filepath.Join(f.root, "linked.go")); err != nil {
		t.Fatal(err)
	}
	gateCommit736(t, f.root)
	r := f.runner()
	if code, out := f.run(t.Context(), r, "-base", f.base); code == 0 || len(r.events) != 0 || !strings.Contains(out, "regular") {
		t.Fatalf("nonregular fixture was omitted: %d %v\n%s", code, r.events, out)
	}
}

func TestRepositoryCIOrdinaryPolicyEditKeepsBaseChecks(t *testing.T) {
	f := gateFixtureFor736(t, "go", "pytest")
	gateWrite736(t, f.root, "README.md", "unowned changes require all parts\n")
	gateWrite736(t, f.root, "worker/test_extra.py", "def test_extra(): assert True\n")
	gateWrite736(t, f.root, repository.Path, "candidate configuration supplies no authority\n")
	gateCommit736(t, f.root)
	r := f.runner()
	if code, out := f.run(t.Context(), r, "-base", f.base); code != 0 || len(r.commands) != 8 {
		t.Fatalf("ordinary policy edit did not run captured commands: %d %v\n%s", code, r.commands, out)
	}
}

func TestRepositoryCIScopeRejectsGitRedirection(t *testing.T) {
	if err := repositoryScopeEnvironment(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", t.TempDir())
	if err := repositoryScopeEnvironment(); err == nil || !strings.Contains(err.Error(), "GIT_DIR") {
		t.Fatalf("redirected scope reader was allowed: %v", err)
	}
}

type repositoryPreflightGitHub struct {
	gateSetupGitHub736
	ref string
}

func (g *repositoryPreflightGitHub) Workflows(ctx context.Context, ref string) (map[string]string, error) {
	g.ref = ref
	return g.gateSetupGitHub736.Workflows(ctx, ref)
}

func TestRepositoryPreflightPinsWorkflowAuthority(t *testing.T) {
	f := gateFixtureFor736(t, "go")
	gh := &repositoryPreflightGitHub{}
	err := checkRepositorySetup(t.Context(), gh, gateRepo736, f.root, "main")
	if err == nil || !strings.Contains(err.Error(), "main") || gh.ref != f.base {
		t.Fatalf("preflight must read the resolved commit and name the upgrade branch: ref=%s err=%v", gh.ref, err)
	}
}

// A developer's shell often names an editor or pager. Neither runs for
// scope's reads, so neither stops factory checks; redirection still does.
func TestRepositoryCIScopeAllowsEditorsAndPagers(t *testing.T) {
	for _, name := range []string{"GIT_EDITOR", "GIT_SEQUENCE_EDITOR", "GIT_PAGER"} {
		t.Setenv(name, "vi")
	}
	if err := repositoryScopeEnvironment(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_OBJECT_DIRECTORY", t.TempDir())
	if err := repositoryScopeEnvironment(); err == nil || !strings.Contains(err.Error(), "GIT_OBJECT_DIRECTORY") {
		t.Fatalf("redirected objects were allowed beside an editor: %v", err)
	}
}

// A committed tree laid out as a Git repository would hand its own config to
// the plain git the sandbox runs in the staged copy, outside Docker, so
// staging refuses it, at any depth, before any check runs.
func TestRepositoryCIRefusesStagedGitRepositoryLayout(t *testing.T) {
	for _, dir := range []string{".", "api/vendor/tool"} {
		t.Run(dir, func(t *testing.T) {
			f := gateFixtureFor736(t, "go", "pytest")
			for name, body := range map[string]string{"HEAD": "ref: refs/heads/main\n", "config": "[core]\n\tbare = true\n", "objects/info/packs": "\n", "refs/heads/README": "\n"} {
				gateWrite736(t, f.root, filepath.Join(dir, name), body)
			}
			gateCommit736(t, f.root)
			r := f.runner()
			code, out := f.run(t.Context(), r, "-base", f.base)
			if code == 0 || len(r.events) != 0 || !strings.Contains(out, "lays out a Git repository") {
				t.Fatalf("a staged Git repository layout reached the checks: %d %v\n%s", code, r.events, out)
			}
		})
	}
}
