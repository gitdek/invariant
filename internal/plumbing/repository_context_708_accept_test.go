package plumbing

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/synth"
)

const ruleBody708 = "Captured rule708: keep widget names stable.\n"

type contextBuildFixture708 struct {
	root     string
	snapshot *repository.Snapshot
	plan     *Plan
	parts    []repository.Part
	rules    map[string]string
}

func buildFixture708(t *testing.T, frameworks []string, namedSpec bool) contextBuildFixture708 {
	t.Helper()
	var parts []repository.Part
	p := &Plan{Name: "repository context fixture", Summary: "Change the widget values and clarify the declared rule.",
		Files: []string{"policy/guide.md"}, Sources: map[string]string{}}
	for i, framework := range frameworks {
		dir := "app"
		if i > 0 {
			dir += "/" + framework
		}
		language, production, file, selector, source := "typescript", "main.ts", "context.test.ts", "widgets ready [one]", "import {test, expect} from 'vitest';\ntest('widgets ready [one]', () => { expect(1).toBe(2); });\n"
		suite, one := []string{"vitest", "run"}, []string{"vitest", "run", "-t", "{test}"}
		switch framework {
		case "go":
			language, production, file, selector, source = "go", "main.go", "context_accept_test.go", "TestContextWidget", "package fixture\nimport \"testing\"\nfunc TestContextWidget(t *testing.T) { t.Fatal(\"base\") }\n"
			suite, one = []string{"go", "test", "./..."}, []string{"go", "test", "./...", "-run", "{test}"}
		case "pytest":
			language, production, file, selector, source = "python", "main.py", "test_context.py", "test_context.py::test_ready", "def test_ready():\n    assert False\n"
			suite, one = []string{"python3", "-m", "pytest", "tests"}, []string{"python3", "-m", "pytest", "{test}"}
		case "jest":
			suite, one = []string{"jest", "--runInBand"}, []string{"jest", "--runInBand", "-t", "{test}"}
			source = "import {test, expect} from '@jest/globals';\ntest('widgets ready [one]', () => { expect(1).toBe(2); });\n"
		}
		part := part484(language, dir)
		part.Name, part.Inputs, part.Lockfiles = framework+"-part", []string{}, []string{}
		part.Commands["install"] = repository.Command{NotApplicable: "No dependencies in this context fixture."}
		part.Commands["format"] = repository.Command{Argv: []string{"format708", "--check", "two words"}}
		part.Commands["lint"] = repository.Command{Argv: []string{"lint708", "--strict"}}
		part.Commands["build"] = repository.Command{NotApplicable: "No build phase708 for this part."}
		part.Commands["test"], part.Commands["test_one"] = repository.Command{Argv: suite}, repository.Command{Argv: one}
		parts = append(parts, part)
		p.Files = append(p.Files, dir+"/"+production)
		p.Tests = append(p.Tests, Test{Name: "widget case", Part: part.Name, Framework: framework, File: dir + "/" + file, Selector: selector, Says: "Widget values change."})
		p.Sources[dir+"/"+file] = source
	}
	root, _, snapshot := fixture484(t, parts, nil)
	rules := map[string]string{"AGENTS.md": "Root rule708: preserve documented widgets.\n", "policy/guide.md": ruleBody708,
		"app/AGENTS.md": "Nested rule708: keep widgets in their owning part.\n"}
	for _, part := range parts {
		if part.Directory != "app" {
			rules[part.Directory+"/CLAUDE.md"] = "Part rule708 for " + part.Name + ": use the captured suite.\n"
		}
	}
	for file, body := range rules {
		write468(t, root, file, body)
	}
	for _, file := range p.Files[1:] {
		if strings.HasSuffix(file, ".py") {
			write468(t, root, file, "value = 1\n")
		}
	}
	doc := "notes/CURRENT.md"
	if namedSpec {
		doc = "SPEC.md"
		write468(t, root, "README.md", "The widget repository's front page.\n")
	}
	write468(t, root, doc, "The current widget behavior708.\n")
	c := snapshot.Config()
	c.AgentRules, c.CurrentState, c.TrustedPaths = []string{"policy/guide.md"}, []string{doc}, []string{"policy/"}
	c.SharedPaths = []repository.SharedPath{{Path: "shared/", Parts: []string{parts[0].Name}}}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	write468(t, root, repository.Path, string(data))
	git468(t, root, "add", "--all")
	git468(t, root, "commit", "-qm", "repository context")
	snapshot, err = repository.Load(t.Context(), root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	p = n503Read(t, p, snapshot)
	lock := Lock{Plan: *p, Ratified: Ratification{Issue: 708, Proposal: p.Hash()}}
	if err := lock.Write(root); err != nil {
		t.Fatal(err)
	}
	git468(t, root, "add", "--all")
	git468(t, root, "commit", "-qm", "ratified fixture")
	return contextBuildFixture708{root, snapshot, p, parts, rules}
}

// Only factory context artifacts explicitly named by the prompt supplement
// its text. Candidate documents are inspected separately, never treated as
// an authoritative context merely because a review needs to read their diff.
func buildText708(t *testing.T, job synth.Job, rules map[string]string) (string, string) {
	t.Helper()
	text, readable := job.Prompt, ""
	err := filepath.WalkDir(job.Workspace, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !entry.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(job.Workspace, file)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if ext := filepath.Ext(file); ext != ".md" && ext != ".json" && ext != ".txt" {
			return nil
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		readable += string(data) + "\n"
		_, isRule := rules[rel]
		isTree := strings.HasPrefix(rel, "repo/") || strings.HasPrefix(rel, "base/")
		if !isRule && !isTree && rel != repository.Path && rel != "plan.json" && rel != "tests.txt" && !strings.HasPrefix(rel, ".invariant/plans/") && strings.Contains(job.Prompt, rel) {
			text += "\n" + string(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return text, readable
}

type contextBuilder708 struct {
	t               *testing.T
	fixture         contextBuildFixture708
	builds, reviews int
	workspace       string
	lastTimeout     time.Duration
}

func (*contextBuilder708) Name() string { return "repository context builder" }
func (a *contextBuilder708) Run(_ context.Context, job synth.Job) (synth.Usage, error) {
	f := a.fixture
	text, readable := buildText708(a.t, job, f.rules)
	for _, want := range []string{repository.Path, f.snapshot.Config().CurrentState[0], "format708", "--check", "two words", "lint708", "--strict", "No build phase708 for this part.", "policy/"} {
		if !strings.Contains(text, want) {
			a.t.Errorf("agent context omits %q (review=%v)", want, job.ReadOnly)
		}
	}
	for file, body := range f.rules {
		if !strings.Contains(text, file) || !strings.Contains(text, strings.TrimSpace(body)) {
			a.t.Errorf("agent lacks captured rule %s (review=%v)", file, job.ReadOnly)
		}
		if !strings.Contains(readable, body) {
			a.t.Errorf("rule %s has no captured workspace copy (review=%v)", file, job.ReadOnly)
		}
	}
	for _, part := range f.parts {
		for _, want := range []string{part.Name, part.Directory, part.Language, part.Commands["test"].Argv[0]} {
			if !strings.Contains(text, want) {
				a.t.Errorf("agent context lacks part/check %q", want)
			}
		}
	}
	for _, test := range f.plan.Tests {
		for _, want := range []string{test.Part, test.Framework, test.File, test.Selector} {
			if !strings.Contains(text, want) {
				a.t.Errorf("agent context lacks acceptance identity %q", want)
			}
		}
	}
	for _, absent := range []string{"Invariant, a Go project", "SPEC.md and the README describe Invariant", "at most 400 lines", "decisions check", "anything under decisions/", "cannot write decisions/", "opt-in real Python and TLAPS", "POISON708", "gofmt, go vet"} {
		if strings.Contains(text, absent) {
			a.t.Errorf("configured agent inherited %q (review=%v)", absent, job.ReadOnly)
		}
	}
	if !strings.Contains(text, "regression") || !strings.Contains(strings.ToLower(text), "person") || !strings.Contains(strings.ToLower(text), "plan") {
		a.t.Error("repository guidance omitted the regression, plan or human-merge boundary")
	}
	if job.Timeout <= 0 || a.lastTimeout > 0 && job.Timeout > a.lastTimeout+time.Second {
		a.t.Errorf("role renewed execution allowance: %v after %v", job.Timeout, a.lastTimeout)
	}
	a.lastTimeout = job.Timeout
	if job.ReadOnly {
		a.reviews++
		if !strings.Contains(text, "Merger work") {
			a.t.Error("review lost unavailable-check handoff")
		}
		if read484(a.t, filepath.Join(job.Workspace, "base"), "policy/guide.md") != ruleBody708 {
			a.t.Error("review lost original rules")
		}
		if !strings.Contains(read484(a.t, filepath.Join(job.Workspace, "repo"), "policy/guide.md"), "POISON708") {
			a.t.Error("review cannot inspect the candidate's changed rule")
		}
		if a.reviews < 3 {
			return synth.Usage{Summary: "policy/guide.md:1: clarify the planned rule.\nVERDICT: changes needed"}, nil
		}
		return synth.Usage{Summary: "VERDICT: approve"}, nil
	}
	a.builds++
	if a.workspace == "" {
		a.workspace = job.Workspace
	}
	if a.workspace != job.Workspace {
		a.t.Error("fallback or repair lost the existing workspace")
	}
	if !slices.Contains(job.Tools, "test") {
		a.t.Error("builder lost test tool")
	}
	if a.builds > 2 && !strings.Contains(job.Prompt, "clarify the planned rule") {
		a.t.Error("repair did not receive review findings")
	}
	for _, file := range f.plan.Files {
		body := "// changed widget708\n"
		switch filepath.Ext(file) {
		case ".go":
			body = "package fixture\nconst Value = 7\n"
		case ".py":
			body = "value = 7\n"
		case ".ts":
			body = "export const value = 7;\n"
		case ".md":
			body = "POISON708: candidate rules are not current authority.\n"
		}
		write468(a.t, job.Workspace, file, body)
	}
	write468(a.t, job.Workspace, repository.Path, `{"candidate":"POISON708"}`)
	if a.builds == 1 {
		return synth.ReadStream(strings.NewReader(loopyReplies), job.Transcript)
	}
	return synth.Usage{Summary: "candidate written"}, nil
}

func TestBuildFallbackRepairsAndReviewsKeepCapturedContext708(t *testing.T) {
	for _, frameworks := range [][]string{{"go"}, {"pytest"}, {"vitest"}, {"jest"}, {"go", "pytest", "vitest", "jest"}} {
		t.Run(strings.Join(frameworks, "+"), func(t *testing.T) {
			f := buildFixture708(t, frameworks, frameworks[0] == "jest")
			runner := &runner515{runner484: newRunner484(t)}
			// Existing trusted adapters isolate context delivery from real tools.
			// The ordinary runner still validates captured command selection.
			runner.hook = func(_ context.Context, q DependencyRequest, argv []string) ([]byte, error) {
				for _, part := range f.parts {
					if part.Directory != q.Directory {
						continue
					}
					for _, phase := range []string{"format", "lint", "test"} {
						if slices.Equal(argv, part.Commands[phase].Argv) {
							return nil, nil
						}
					}
				}
				t.Errorf("check ran undeclared command %q in %s", argv, q.Directory)
				return nil, nil
			}
			sandbox := sandbox484(t, f.root, f.snapshot, runner.runner484)
			sandbox.Repository.Dependencies.Runner = runner
			sandbox.Repository.Adapters = map[string]RepositoryAcceptanceAdapter{}
			for _, framework := range frameworks {
				sandbox.Repository.Adapters[framework] = &adapter515{selectHook: func(_ context.Context, part repository.Part, test Test) (RepositoryAcceptanceSelection, error) {
					argv := slices.Clone(part.Commands["test_one"].Argv)
					for i := range argv {
						argv[i] = strings.ReplaceAll(argv[i], "{test}", test.Selector)
					}
					return RepositoryAcceptanceSelection{Package: "./" + part.Directory, Argv: argv}, nil
				}}
			}
			agent := &contextBuilder708{t: t, fixture: f}
			builder := Builder{Backend: agent, Reviewer: agent, Binary: "unused", TestRuns: 4, Timeout: time.Minute,
				Sandbox: sandbox, FallbackEffort: "xhigh"}
			hash := f.plan.Hash()
			result, err := builder.Build(t.Context(), f.root, 708, t.TempDir())
			if err != nil || result == nil || !result.Built() {
				t.Fatalf("context fixture build failed: %v %+v", err, result)
			}
			if agent.builds != 4 || agent.reviews != 3 || result.Fallback == nil {
				t.Fatalf("want initial/fallback/two repairs and three reviews, got builds=%d reviews=%d fallback=%+v", agent.builds, agent.reviews, result.Fallback)
			}
			lock, err := ReadLockFile(f.root, 708)
			if err != nil || lock.Plan.Hash() != hash {
				t.Fatalf("context delivery changed the ratified plan: %v", err)
			}
			for file, body := range f.plan.Sources {
				if read484(t, f.root, file) != body {
					t.Errorf("context delivery changed acceptance %s", file)
				}
			}
			if strings.Contains(read484(t, f.root, repository.Path), "POISON708") {
				t.Error("unplanned candidate configuration escaped the existing scope boundary")
			}
			closed484(t, runner.runner484)
		})
	}
}
