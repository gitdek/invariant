package plumbing

import (
	"context"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
)

func nodePart633(framework, dir, image string) repository.Part {
	commands := map[string]repository.Command{}
	for _, phase := range []string{"install", "format", "lint", "build", "test"} {
		commands[phase] = repository.Command{NotApplicable: "Offline fixture has no command for this phase."}
	}
	argv := []string{"vitest", "run", "--testNamePattern", "{test}"}
	if framework == "jest" {
		argv = []string{"jest", "--runInBand", "--testNamePattern", "{test}"}
	}
	commands["test_one"] = repository.Command{Argv: argv}
	return repository.Part{Name: dir, Directory: dir, Language: "typescript", Image: image, Inputs: []string{}, Lockfiles: []string{}, Commands: commands}
}

func nodeAdapter633(t *testing.T, framework string) RepositoryAcceptanceAdapter {
	t.Helper()
	a := (Sandbox{Repository: &RepositoryChecks{}}).repositoryAdapter(framework)
	if a == nil {
		t.Fatalf("captured TypeScript has no default %s acceptance adapter", framework)
	}
	return a
}

// Every real-tool test calls this control before its optional runtime branch.
// Default tests exercise admission without requiring Node, Docker or a network.
func nodeControl633(t *testing.T) {
	t.Helper()
	for _, framework := range []string{"vitest", "jest"} {
		p := nodePart633(framework, "web", "node@"+hash468("633"))
		test := Test{Name: "label", Part: "web", Framework: framework, File: "web/nested/exact.test.ts", Selector: "outer ready [one].+ $(literal); | ^ $ \\"}
		if _, err := nodeAdapter633(t, framework).Select(t.Context(), p, test); err != nil {
			t.Fatalf("%s cannot select a literal full title: %v", framework, err)
		}
	}
}

func TestNodeExactSelection633(t *testing.T) {
	for _, framework := range []string{"vitest", "jest"} {
		t.Run(framework, func(t *testing.T) {
			a := nodeAdapter633(t, framework)
			p := nodePart633(framework, "web", "node@"+hash468("633"))
			test := Test{Name: "display only", Part: "web", Framework: framework, File: "web/nested/exact.spec.tsx", Selector: "outer ready [one].+ $(literal); | ^ $ \\"}
			original := slices.Clone(p.Commands["test_one"].Argv)
			got, err := a.Select(t.Context(), p, test)
			if err != nil || len(got.Argv) == 0 || got.Package != "" {
				t.Fatalf("exact selection: %v %+v", err, got)
			}
			// Both CLIs implement title filters as ECMAScript regexes. No regexp
			// metacharacter in the ratified title may broaden that selection.
			pattern := "^" + regexp.QuoteMeta(test.Selector) + "$"
			if !slices.Contains(got.Argv, pattern) && !slices.Contains(got.Argv, "--testNamePattern="+pattern) {
				t.Fatalf("title was not escaped and anchored: %q", got.Argv)
			}
			if !slices.Contains(got.Argv, "nested/exact.spec.tsx") && !slices.Contains(got.Argv, "./nested/exact.spec.tsx") {
				t.Fatalf("selection omitted its independent file: %q", got.Argv)
			}
			if framework == "vitest" && !slices.Contains(got.Argv, "run") || framework == "jest" && !slices.Contains(got.Argv, "--runTestsByPath") {
				t.Fatalf("selection is not a one-shot file invocation: %q", got.Argv)
			}
			got.Argv[0] = "changed"
			if !reflect.DeepEqual(p.Commands["test_one"].Argv, original) {
				t.Fatal("selection mutated captured command policy")
			}
			for _, ext := range []string{"ts", "tsx", "js", "jsx", "mts", "cts", "mjs", "cjs"} {
				copy := test
				copy.File = "web/regular.test." + ext
				if _, err := a.Select(t.Context(), p, copy); err != nil {
					t.Errorf("structurally valid %s file refused before framework discovery: %v", ext, err)
				}
			}
			// Supported configuration and timing policy must survive narrowing.
			config := framework + ".config.cjs"
			p.Commands["test_one"] = repository.Command{Argv: append(slices.Clone(original), "--config", config, "--testTimeout", "5000")}
			got, err = a.Select(t.Context(), p, test)
			if err != nil || !slices.Contains(got.Argv, config) || !slices.Contains(got.Argv, "5000") {
				t.Fatalf("supported command policy lost: %v %q", err, got.Argv)
			}
			for _, bad := range [][]string{
				{"sh", "-c", framework + " -t {test}"},
				{"npx", framework, "-t", "{test}"},
				{"npm", "test", "--", "-t", "{test}"},
				{framework, "--watch", "-t", "{test}"},
				{framework, "--watchAll", "-t", "{test}"},
				{framework, "--listTests", "-t", "{test}"},
				{framework, "--reporter", "./forged.cjs", "-t", "{test}"},
				{framework, "--reporters", "./forged.cjs", "-t", "{test}"},
				{framework, "--outputFile", "results.json", "-t", "{test}"},
				{framework, "--testNamePattern", "prefix{test}"},
				{framework, "-t", "{test}", "-t", "neighbor"},
				{framework, "-t", "{test}", "other.test.ts"},
				{framework, "--config", "../outside.cjs", "-t", "{test}"},
				{framework, "--unknown", "-t", "{test}"},
				{framework, "-t", "{test}", "{test}"},
			} {
				p.Commands["test_one"] = repository.Command{Argv: bad}
				if _, err := a.Select(t.Context(), p, test); err == nil {
					t.Errorf("unsafe or opaque template admitted: %q", bad)
				}
			}
			p.Commands["test_one"] = repository.Command{Argv: original}
			for _, mode := range []string{"owner", "language", "framework", "file", "empty"} {
				part, selected := p, test
				switch mode {
				case "owner":
					selected.Part = "other"
				case "language":
					part.Language = "python"
				case "framework":
					selected.Framework = "pytest"
				case "file":
					selected.File = "other/exact.spec.tsx"
				case "empty":
					selected.Selector = ""
				}
				if _, err := a.Select(t.Context(), part, selected); err == nil {
					t.Errorf("mismatched %s accepted", mode)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := a.Select(ctx, p, test); err == nil {
				t.Fatal("canceled selection succeeded")
			}
			override := &adapter515{}
			s := Sandbox{Repository: &RepositoryChecks{Adapters: map[string]RepositoryAcceptanceAdapter{framework: override}}}
			if s.repositoryAdapter(framework) != override {
				t.Fatal("trusted override replaced")
			}
		})
	}
}

func TestNodePreparedCapability633(t *testing.T) {
	_, logs := docker484(t)
	e, err := (&DockerDependencyRunner{}).OpenChecks(t.Context(), request484(t, "typescript"))
	if err != nil {
		t.Fatal(err)
	}
	defer close484(t, e)
	if _, ok := e.(RepositoryProcessTransport); !ok {
		t.Fatal("prepared Node environment lacks process transport")
	}
	if _, ok := e.(RepositoryAcceptanceEnvironment); !ok {
		t.Fatal("prepared Node environment lacks acceptance capability")
	}
	if out, err := e.Run(t.Context(), []string{"fixture-check", "literal;$(title)"}); err != nil || !strings.Contains(string(out), "container diagnostic") {
		t.Fatalf("ordinary command behavior changed: %v %s", err, out)
	}
	for _, args := range invocations484(t, logs) {
		for _, arg := range args {
			if strings.Contains(arg, "/invariant/process-") || arg == "python3" || arg == "pip" || arg == "npm" || arg == "npx" {
				t.Fatalf("ordinary Node command installed dependencies or staged a helper: %q", args)
			}
		}
	}
}

func TestNodeConsoleCannotQualify633(t *testing.T) {
	nodeControl633(t)
	for _, framework := range []string{"vitest", "jest"} {
		p := nodePart633(framework, "web", "node@"+hash468("633"))
		root, candidate, snapshot := fixture484(t, []repository.Part{p}, nil)
		plan := n503Plan(t, framework, "web", "web/ready.test.ts", "outer ready", "test('outer ready', () => { throw new Error('pending') });\n")
		plan.Files = []string{"web/main.ts"}
		plan = n503Read(t, plan, snapshot)
		runner := newRunner484(t)
		runner.hook = func(context.Context, DependencyRequest, []string) ([]byte, error) {
			return []byte("PASS outer ready\nFAIL outer ready\n{\"status\":\"failed\"}\n@@invariant accept=0\n"), nil
		}
		s := sandbox484(t, root, snapshot, runner)
		c, err := Check(t.Context(), n503Draft(t, plan), root, s)
		if err != nil || c == nil || c.Problem == "" || len(runner.opens) == 0 {
			t.Fatalf("supported template must prepare, then refuse absent evidence: %v %+v", err, c)
		}
		r, _ := s.Run(t.Context(), candidate, plan)
		if r.Passed() || len(runner.closed) != len(runner.opens) {
			t.Fatal("console-only environment qualified or leaked")
		}
		for _, record := range r.TestResults {
			if record.Phase == "accept" && (record.Status == "passed" || record.Status == "failed") {
				t.Fatal("console manufactured a terminal verdict")
			}
		}
	}
}
