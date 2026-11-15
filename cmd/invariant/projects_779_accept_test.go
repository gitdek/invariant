package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/setup"
)

func exclusionGuidance779(t *testing.T, printed, directory string) {
	t.Helper()
	lower := strings.ToLower(printed)
	for _, word := range []string{directory, directory + "/**", "exclude", "eslint", "ignore", ".ts", "gate"} {
		if !strings.Contains(lower, strings.ToLower(word)) {
			t.Errorf("init lacks %q in its projects guidance:\n%s", word, printed)
		}
	}
}

func TestInitExplainsProjectExclusions779(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"package.json":      `{"name":"app","private":true,"scripts":{"build":"tsc --noEmit","lint":"eslint ."}}`,
		"tsconfig.json":     "{\n  // a person's configuration\n  \"include\": [\"**/*.ts\"]\n}\n",
		"eslint.config.mjs": "throw new Error('init must never execute this configuration');\n",
		"web/package.json":  `{"name":"other-part","private":true}`,
	}
	for file, text := range files {
		initWrite727(t, root, file, text)
	}
	args := []string{"-repo", "acme/app", "-invariant", initPin, root}
	code, printed := initPrints(t, args...)
	if code != 0 {
		t.Fatalf("init: %d\n%s", code, printed)
	}
	exclusionGuidance779(t, printed, "invariant")
	config := string(initRead727(t, root, repository.Path))
	// Advice must also reach a person re-running init, including -force,
	// even though their repository contract must remain byte-for-byte intact.
	code, printed = initPrints(t, "-force", "-repo", "acme/app", "-invariant", initPin, root)
	if code != 0 {
		t.Fatalf("repeat init: %d\n%s", code, printed)
	}
	exclusionGuidance779(t, printed, "invariant")
	if got := string(initRead727(t, root, repository.Path)); got != config {
		t.Fatal("repeat init rewrote the contract")
	}
	for name, want := range files {
		if got := string(initRead727(t, root, name)); got != want {
			t.Errorf("init rewrote %s", name)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "invariant")); !os.IsNotExist(err) {
		t.Fatalf("guidance should work before any modeled project exists: %v", err)
	}
}

// A subprocess makes unsupported CLI flags an ordinary assertion failure in
// the parent test, since init's existing flag set uses ExitOnError.
func TestInitProjectsFlag779(t *testing.T) {
	if os.Getenv("INVARIANT_INIT_CHILD_779") == "1" {
		i := slices.Index(os.Args, "--")
		if i < 0 {
			os.Exit(99)
		}
		os.Exit(initCmd(os.Args[i+1:]))
	}
	run := func(args ...string) (int, string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), os.Args[0], append([]string{"-test.run=^TestInitProjectsFlag779$", "--"}, args...)...)
		cmd.Env = append(os.Environ(), "INVARIANT_INIT_CHILD_779=1")
		out, err := cmd.CombinedOutput()
		if err == nil {
			return 0, string(out)
		}
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode(), string(out)
		}
		t.Fatal(err)
		return -1, ""
	}
	root := t.TempDir()
	for _, dir := range []string{".", "web", "other", "web/model checks/one"} {
		initWrite727(t, root, filepath.ToSlash(filepath.Join(dir, "package.json")), `{"name":"fixture","private":true}`)
	}
	code, printed := run("-projects", "web/model checks", "-repo", "acme/app", "-invariant", initPin, root)
	if code != 0 {
		t.Fatalf("init must support a custom projects directory: %d\n%s", code, printed)
	}
	var draft struct {
		Projects string            `json:"projects_directory"`
		Parts    []repository.Part `json:"parts"`
	}
	before := initRead727(t, root, repository.Path)
	if err := json.Unmarshal(before, &draft); err != nil {
		t.Fatal(err)
	}
	if draft.Projects != "web/model checks" {
		t.Fatalf("wrong drafted directory: %q", draft.Projects)
	}
	if len(draft.Parts) != 3 {
		t.Fatalf("project became an application part: %+v", draft.Parts)
	}
	exclusionGuidance779(t, printed, "web/model checks")
	watch := ""
	for _, line := range strings.Split(printed, "\n") {
		if strings.Contains(line, "invariant watch -repo acme/app") {
			watch = line
		}
	}
	if !strings.Contains(watch, "-projects 'web/model checks'") && !strings.Contains(watch, "-projects \"web/model checks\"") && !strings.Contains(watch, `-projects web/model\ checks`) {
		t.Fatalf("watch instructions lost the selected projects directory:\n%s", printed)
	}
	// Re-running with the default flag must use the retained declaration,
	// rather than advise a different directory or overwrite the contract.
	code, printed = run("-repo", "acme/app", "-invariant", initPin, root)
	if code != 0 {
		t.Fatalf("kept custom contract: %d\n%s", code, printed)
	}
	exclusionGuidance779(t, printed, "web/model checks")
	if string(initRead727(t, root, repository.Path)) != string(before) {
		t.Fatal("custom declaration was rewritten")
	}
	for _, bad := range []string{".", "../outside", "/absolute", ".invariant", ".github", "models/**"} {
		dir := t.TempDir()
		initWrite727(t, dir, "package.json", `{"name":"fixture","private":true}`)
		code, printed := run("-projects", bad, "-repo", "acme/app", "-invariant", initPin, dir)
		if code == 0 || !strings.Contains(strings.ToLower(printed), "project") {
			t.Errorf("unsafe projects path %q accepted: %d\n%s", bad, code, printed)
		}
		for _, destination := range []string{repository.Path, setup.WorkflowPath} {
			if _, err := os.Lstat(filepath.Join(dir, destination)); !os.IsNotExist(err) {
				t.Errorf("invalid projects path wrote %s: %v", destination, err)
			}
		}
	}
}

type projectsGateRunner779 struct{ *gateRunner736 }

func (r *projectsGateRunner779) OpenChecks(ctx context.Context, q plumbing.DependencyRequest) (plumbing.RepositoryCheckEnvironment, error) {
	if _, err := os.Lstat(filepath.Join(q.SourceDir, "invariant")); !os.IsNotExist(err) {
		return nil, fmt.Errorf("CI application checks can still see the modeled-project tree: %v", err)
	}
	return r.gateRunner736.OpenChecks(ctx, q)
}

func TestRepositoryCILeavesProjectsToGate779(t *testing.T) {
	for _, mode := range []string{"push", "ordinary PR", "modeled factory PR"} {
		t.Run(mode, func(t *testing.T) {
			f := gateFixtureFor736(t, "vitest")
			// Move the web part to the repository root, as in the reported
			// recursive tsc failure. Preserve all captured fake command argv.
			p := &f.config.Parts[0]
			p.Directory = "."
			for _, names := range [][]string{p.Inputs, p.Lockfiles} {
				for i, old := range names {
					b, err := os.ReadFile(filepath.Join(f.root, old))
					if err != nil {
						t.Fatal(err)
					}
					names[i] = strings.TrimPrefix(old, "web/")
					gateWrite736(t, f.root, names[i], string(b))
				}
			}
			gateWrite736(t, f.root, "main.ts", "export const value = 0;\n")
			gateWrite736(t, f.root, repository.Path, gateJSON736(t, f.config))
			if err := os.RemoveAll(filepath.Join(f.root, "web")); err != nil {
				t.Fatal(err)
			}
			f.base = gateCommit736(t, f.root)
			driver := "import {value} from '../../main.ts';\nvoid value;\n"
			gateWrite736(t, f.root, "invariant/01-compile-once/conformance.ts", driver)
			gateWrite736(t, f.root, "invariant/01-compile-once/.invariant/invariant.json", `{"name":"compile once","module":"spec.tla","language":"typescript","code":"conformance.ts"}`)
			gateWrite736(t, f.root, "invariant/01-compile-once/.invariant/ratified.lock", `{"bounds":{},"statements":[],"ratified":{"by":"writer","issue":736}}`)
			gateCommit736(t, f.root)
			args := []string{}
			if mode != "push" {
				args = append(args, "-base", f.base)
			}
			if mode == "modeled factory PR" {
				args = append(args, "-issue", "736")
			}
			r := &projectsGateRunner779{f.runner()}
			code, out := runGate740(t, f, r, t.TempDir(), args...)
			if code != 0 {
				t.Fatalf("%s failed on gate-owned project files: %d\n%s", mode, code, out)
			}
			if !slices.Equal(r.commands, []string{"./format", "./lint", "./build", "./test"}) || r.open != r.closed {
				t.Fatalf("CI skipped application checks or cleanup: %v, %d/%d", r.commands, r.open, r.closed)
			}
			b, err := os.ReadFile(filepath.Join(f.root, "invariant/01-compile-once/conformance.ts"))
			if err != nil || string(b) != driver {
				t.Fatalf("gate lost the original driver and its .ts imports: %q, %v", b, err)
			}
			if f.comments.calls != 0 {
				t.Fatal("ordinary or modeled CI acquired plumbing ratification")
			}
		})
	}
}
