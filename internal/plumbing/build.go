package plumbing

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gitdek/invariant/internal/synth"
)

// Builder builds a ratified plan with a coding agent, then has a second
// agent review what it built against the plan (D-0105).
type Builder struct {
	Backend  synth.Backend // writes the code
	Reviewer synth.Backend // reviews it, with tools that only read
	Binary   string        // this invariant binary, which serves the test tool
	TestRuns int           // the most test runs the agent gets
	Timeout  time.Duration
	Sandbox  Sandbox
	// FallbackEffort is the effort the agent runs at once more when the loop
	// guard stops its first run (D-0125). Empty never runs it again. The
	// review never runs again.
	FallbackEffort string
}

// BuildResult is how a build went.
type BuildResult struct {
	Changes  Changes         `json:"changes"`
	Final    Result          `json:"-"`
	Passed   bool            `json:"passed"`
	Tests    int             `json:"tests"`    // the plan's acceptance tests
	Summary  string          `json:"summary"`  // what the factory's own run found
	Review   string          `json:"review"`   // the second agent's review
	Approved bool            `json:"approved"` // whether the review approves the change
	Usage    synth.Usage     `json:"usage"`
	Spend    float64         `json:"spend"` // both agents' estimated cost
	TestRuns []synth.GateRun `json:"test_runs,omitempty"`
	// Fallback says the agent ran once more, when the loop guard stopped its
	// first run (D-0125). Usage is the second run's.
	Fallback *synth.Fallback `json:"fallback,omitempty"`
}

// Build implements issue n's ratified plan in the checkout at root, which
// holds the plan's record and its acceptance tests. The agent works on a
// copy outside the home directory, and only the files the plan names come
// back into root. Its transcript lands in out.
func (b Builder) Build(ctx context.Context, root string, n int, out string) (*BuildResult, error) {
	lock, err := ReadLockFile(root, n)
	if err != nil {
		return nil, err
	}
	plan := &lock.Plan
	if err := os.MkdirAll(out, 0o755); err != nil {
		return nil, err
	}
	ws, err := os.MkdirTemp("", "invariant-plumbing-build-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(ws)
	if err := CopyCheckout(ctx, root, ws); err != nil {
		return nil, err
	}
	transcript, err := os.Create(filepath.Join(out, "transcript.jsonl"))
	if err != nil {
		return nil, err
	}
	defer transcript.Close()
	testLog := filepath.Join(out, "test-runs.jsonl")
	// One build cache serves the agent's test runs and the factory's own, so
	// only the first compiles everything.
	b.Sandbox.Cache = filepath.Join(out, "gocache")
	usage, fallback, runErr := synth.RunAgent(ctx, b.Backend, synth.Job{
		Workspace: ws,
		Prompt:    BuildPrompt(plan, n, b.TestRuns),
		GateServer: []string{b.Binary, "mcp", "-plumbing", "-ratified", root, "-issue", fmt.Sprint(n), "-max-runs", fmt.Sprint(b.TestRuns),
			"-log", testLog, "-cache", b.Sandbox.Cache, ws},
		Tools:      []string{"test"},
		Transcript: transcript,
	}, b.Timeout, b.FallbackEffort)
	usage.Backend = b.Backend.Name()
	r := &BuildResult{Usage: usage, Spend: usage.CostUSD, Tests: len(plan.Tests), Fallback: fallback}
	r.TestRuns, _ = readRuns(testLog)
	if r.Changes, err = Diff(ctx, root, ws, plan); err != nil {
		return nil, err
	}
	// The factory runs the tests itself on what the build made, whatever
	// the agent says.
	staged, err := os.MkdirTemp("", "invariant-plumbing-staged-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staged)
	if err := Stage(ctx, root, ws, plan, staged); err != nil {
		return nil, err
	}
	final, err := b.Sandbox.Run(ctx, staged, plan)
	if err != nil {
		return nil, err
	}
	r.Final, r.Passed, r.Summary = final, final.Passed() && len(r.Changes.Written) > 0, final.Summary()
	if len(r.Changes.Written) == 0 {
		r.Summary = "The build changed none of the files the plan names.\n" + r.Summary
	}
	if runErr != nil {
		r.Summary = fmt.Sprintf("The agent's run ended with an error: %v\n%s", runErr, r.Summary)
	}
	os.WriteFile(filepath.Join(out, "final.txt"), []byte(final.Output), 0o644)
	// Only the files the plan names come back.
	if err := apply(root, ws, plan); err != nil {
		return nil, err
	}
	if r.Passed {
		r.Review, r.Approved, err = b.review(ctx, root, n, plan, r, out)
		if err != nil {
			r.Review = "The review didn't run: " + err.Error()
		}
	}
	b2, _ := json.MarshalIndent(r, "", "  ")
	os.WriteFile(filepath.Join(out, "build.json"), append(b2, '\n'), 0o644)
	return r, nil
}

// apply copies each file the plan names from the workspace into root,
// removing the ones the build removed.
func apply(root, ws string, plan *Plan) error {
	for _, f := range plan.Files {
		if err := take(ws, f, filepath.Join(root, filepath.FromSlash(f))); err != nil {
			return err
		}
	}
	return nil
}

// ReadLockFile reads issue n's ratified plan from the checkout at root.
func ReadLockFile(root string, n int) (*Lock, error) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(LockPath(n))))
	if err != nil {
		return nil, fmt.Errorf("the ratified plan's record: %w", err)
	}
	return ReadLock(b)
}

var verdictLine = regexp.MustCompile(`(?im)^\s*\**VERDICT:\**\s*(approve|changes needed)\b`)

// review has a second agent, which can only read, check the change against
// the plan: that it does what the plan says, that its tests show it, and
// that it does nothing else.
func (b Builder) review(ctx context.Context, root string, n int, plan *Plan, r *BuildResult, out string) (string, bool, error) {
	ws, err := os.MkdirTemp("", "invariant-plumbing-review-")
	if err != nil {
		return "", false, err
	}
	defer os.RemoveAll(ws)
	if err := CopyCheckout(ctx, root, filepath.Join(ws, "repo")); err != nil {
		return "", false, err
	}
	diff, err := exec.CommandContext(ctx, "git", "-C", root, "diff", "--no-color", "HEAD", "--", ".").Output()
	if err != nil {
		return "", false, fmt.Errorf("diffing the change: %w", err)
	}
	// New files don't show in git diff until they're added.
	news, _ := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "--others", "--exclude-standard").Output()
	planJSON, _ := json.MarshalIndent(plan, "", "  ")
	for name, text := range map[string]string{"plan.json": string(planJSON) + "\n", "change.diff": string(diff), "new-files.txt": string(news), "tests.txt": r.Summary + "\n" + r.Final.Tail(6000)} {
		if err := os.WriteFile(filepath.Join(ws, name), []byte(text), 0o644); err != nil {
			return "", false, err
		}
	}
	transcript, err := os.Create(filepath.Join(out, "review-transcript.jsonl"))
	if err != nil {
		return "", false, err
	}
	defer transcript.Close()
	runCtx, cancel := context.WithTimeout(ctx, b.Timeout)
	defer cancel()
	usage, err := b.Reviewer.Run(runCtx, synth.Job{Workspace: ws, Prompt: ReviewPrompt(plan, n), ReadOnly: true, Transcript: transcript})
	r.Spend += usage.CostUSD
	if err != nil {
		return "", false, err
	}
	text := strings.TrimSpace(usage.Summary)
	m := verdictLine.FindStringSubmatch(text)
	if m == nil {
		return text, false, nil
	}
	return text, strings.EqualFold(m[1], "approve"), nil
}

func readRuns(path string) ([]synth.GateRun, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var runs []synth.GateRun
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var g synth.GateRun
		if json.Unmarshal(scanner.Bytes(), &g) == nil {
			runs = append(runs, g)
		}
	}
	return runs, scanner.Err()
}
