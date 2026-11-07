package plumbing

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/synth"
)

func TestRepositoryContextBoundsDocumentsAndDeclarations(t *testing.T) {
	large := strings.Repeat("Captured text with Unicode λ and literal ``` fences.\n", 8192)
	for _, tc := range []struct {
		name string
		text string
		body string
	}{
		{"document", "Captured declarations.\n", large},
		{"declarations", large, "Captured document.\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			captured := &RepositoryContext{text: tc.text, files: []repository.ContextFile{{Path: "notes/CURRENT.md", Text: tc.body}}}
			ws := t.TempDir()
			prompt, release, err := captured.Stage(ws)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			if len(prompt) > 16*1024 {
				t.Fatalf("context grew the command-line argument to %d bytes", len(prompt))
			}
			files, err := filepath.Glob(filepath.Join(ws, "repository-context-*", "context.md"))
			if err != nil || len(files) != 1 {
				t.Fatalf("missing captured context: %v, %v", files, err)
			}
			rel, err := filepath.Rel(ws, files[0])
			if err != nil || !strings.Contains(prompt, filepath.ToSlash(rel)) {
				t.Fatalf("prompt does not reference the full context: %v, %s", err, prompt)
			}
			data, err := os.ReadFile(files[0])
			if err != nil || !strings.Contains(string(data), tc.text) || !strings.Contains(string(data), tc.body) {
				t.Fatalf("context artifact truncated captured contents: %v", err)
			}
			data, err = os.ReadFile(filepath.Join(filepath.Dir(files[0]), "source-0.txt"))
			if err != nil || string(data) != tc.body {
				t.Fatalf("source copy lost exact bytes: %v", err)
			}
			release()
			if entries, err := os.ReadDir(ws); err != nil || len(entries) != 0 {
				t.Fatalf("context artifacts leaked into candidate changes: %v, %v", entries, err)
			}
		})
	}
}

type largeContextBuilder708 struct {
	contextBuilder708
	document string
	seen     map[string]bool
}

func (a *largeContextBuilder708) Run(ctx context.Context, job synth.Job) (synth.Usage, error) {
	a.t.Helper()
	if len(job.Prompt) >= 32*1024 {
		a.t.Fatalf("large captured document reached the launch argument: %d bytes", len(job.Prompt))
	}
	files, err := filepath.Glob(filepath.Join(job.Workspace, "repository-context-*", "context.md"))
	if err != nil || len(files) != 1 {
		a.t.Fatalf("expected one fresh context artifact: %v, %v", files, err)
	}
	if a.seen[files[0]] {
		a.t.Fatal("a later invocation reused editable context artifacts")
	}
	a.seen[files[0]] = true
	text, _ := buildText708(a.t, job, a.fixture.rules)
	if !strings.Contains(text, a.document) || strings.Contains(text, "REPLACEMENT708") {
		a.t.Fatal("request lost the full captured document or trusted a replacement")
	}
	copies, err := filepath.Glob(filepath.Join(filepath.Dir(files[0]), "source-*.txt"))
	if err != nil {
		a.t.Fatal(err)
	}
	found := false
	for _, file := range copies {
		data, err := os.ReadFile(file)
		if err != nil {
			a.t.Fatal(err)
		}
		found = found || string(data) == a.document
	}
	if !found {
		a.t.Fatal("request has no exact readable copy of the large document")
	}
	usage, runErr := a.contextBuilder708.Run(ctx, job)
	if !job.ReadOnly {
		// The next fallback or repair must restore guidance from the retained
		// capture even when every earlier workspace artifact was overwritten.
		for _, file := range append(copies, files...) {
			if err := os.WriteFile(file, []byte("ARTIFACT_REPLACEMENT708"), 0o644); err != nil {
				a.t.Fatal(err)
			}
		}
	}
	return usage, runErr
}

func TestLargeCapturedContextSurvivesFallbackRepairsAndReviews(t *testing.T) {
	f := buildFixture708(t, []string{"go"}, false)
	document := strings.Repeat("Full current-state document λ with ``` fences.\n", 8192) + "Last captured document line.\n"
	f.rules["app/AGENTS.md"] += strings.Repeat("Keep the captured nested guidance.\n", 4096)
	write468(t, f.root, "notes/CURRENT.md", document)
	write468(t, f.root, "app/AGENTS.md", f.rules["app/AGENTS.md"])
	git468(t, f.root, "add", "--all")
	git468(t, f.root, "commit", "-qm", "large captured context")
	var err error
	f.snapshot, err = repository.Load(t.Context(), f.root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	write468(t, f.root, "notes/CURRENT.md", "WORKTREE_REPLACEMENT708")
	write468(t, f.root, "app/AGENTS.md", "WORKTREE_REPLACEMENT708")
	runner := &runner515{runner484: newRunner484(t)}
	runner.hook = func(_ context.Context, q DependencyRequest, argv []string) ([]byte, error) {
		for _, phase := range []string{"format", "lint", "test"} {
			if q.Directory == f.parts[0].Directory && slices.Equal(argv, f.parts[0].Commands[phase].Argv) {
				return nil, nil
			}
		}
		t.Errorf("check ran undeclared command %q in %s", argv, q.Directory)
		return nil, nil
	}
	sandbox := sandbox484(t, f.root, f.snapshot, runner.runner484)
	sandbox.Repository.Dependencies.Runner = runner
	sandbox.Repository.Adapters = map[string]RepositoryAcceptanceAdapter{"go": &adapter515{selectHook: func(_ context.Context, part repository.Part, test Test) (RepositoryAcceptanceSelection, error) {
		argv := slices.Clone(part.Commands["test_one"].Argv)
		for i := range argv {
			argv[i] = strings.ReplaceAll(argv[i], "{test}", test.Selector)
		}
		return RepositoryAcceptanceSelection{Package: "./" + part.Directory, Argv: argv}, nil
	}}}
	agent := &largeContextBuilder708{contextBuilder708: contextBuilder708{t: t, fixture: f}, document: document, seen: map[string]bool{}}
	builder := Builder{Backend: agent, Reviewer: agent, Binary: "unused", TestRuns: 4, Timeout: time.Minute,
		Sandbox: sandbox, FallbackEffort: "xhigh"}
	result, err := builder.Build(t.Context(), f.root, 708, t.TempDir())
	if err != nil || result == nil || !result.Built() {
		t.Fatalf("large context build failed: %v %+v", err, result)
	}
	if agent.builds != 4 || agent.reviews != 3 || result.Fallback == nil || len(agent.seen) != 7 {
		t.Fatalf("incomplete lifecycle: builds=%d reviews=%d fallback=%+v artifacts=%d", agent.builds, agent.reviews, result.Fallback, len(agent.seen))
	}
	lock, err := ReadLockFile(f.root, 708)
	if err != nil || lock.Plan.Hash() != f.plan.Hash() {
		t.Fatalf("context delivery changed the ratified plan: %v", err)
	}
	for file, body := range f.plan.Sources {
		if read484(t, f.root, file) != body {
			t.Errorf("context delivery changed acceptance %s", file)
		}
	}
	closed484(t, runner.runner484)
}
