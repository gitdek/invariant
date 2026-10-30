package plumbing

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
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
	// guard stops its first run (D-0125). Empty never runs it again.
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
	// first run (D-0125). Usage includes every builder and reviewer run.
	Fallback *synth.Fallback `json:"fallback,omitempty"`
}

// Build implements issue n's ratified plan in the checkout at root, which
// holds the plan's record and its acceptance tests. The agent works on a
// copy outside the home directory. Only named files and new regression
// tests in the plan's packages come back into root. Its logs land in out.
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
	budget := synth.NewExecutionBudget(b.Timeout)
	job := synth.Job{
		Workspace: ws,
		Prompt:    BuildPrompt(plan, n, b.TestRuns),
		GateServer: []string{b.Binary, "mcp", "-plumbing", "-ratified", root, "-issue", fmt.Sprint(n), "-max-runs", fmt.Sprint(b.TestRuns),
			"-log", testLog, "-cache", b.Sandbox.Cache, ws},
		Tools:      []string{"test"},
		Transcript: transcript,
	}
	r := &BuildResult{Tests: len(plan.Tests)}
	var buildErr error
	// root stays unchanged until all rounds finish: added tests remain new
	// against that baseline, and every review sees the whole candidate.
	for round := 0; round <= 2; round++ {
		r.Passed, r.Approved = false, false
		if round > 0 {
			job.Prompt = BuildPrompt(plan, n, b.TestRuns) + fmt.Sprintf("\nRepair attempt %d of at most two. Continue in this workspace under the unchanged ratified plan. Address the latest review findings below, adding regression tests within the permitted packages where needed. If a finding needs a different plan, explain what a person must change; do not expand the scope. The factory will run fresh checks and review the whole candidate again.\n\n%s\n", round, r.Review)
		}
		usage, runErr := budget.Run(ctx, b.Backend, job)
		r.Usage.Add(usage)
		if round == 0 && b.FallbackEffort != "" && errors.Is(runErr, synth.ErrThinkingLoop) && budget.Err() == nil && ctx.Err() == nil {
			r.Fallback = &synth.Fallback{Effort: b.FallbackEffort, Why: runErr.Error()}
			fallbackJob := job
			fallbackJob.Effort = b.FallbackEffort
			usage, runErr = budget.Run(ctx, b.Backend, fallbackJob)
			r.Usage.Add(usage)
		}
		if ctx.Err() != nil {
			buildErr = ctx.Err()
		} else if budget.Err() != nil {
			buildErr = budget.Err()
		} else if errors.Is(runErr, context.DeadlineExceeded) || errors.Is(runErr, context.Canceled) {
			buildErr = runErr
		}
		if buildErr != nil {
			r.Summary = fmt.Sprintf("The build's execution time ended before fresh checks and approval: %v", buildErr)
			break
		}
		// The factory checks a fresh staging of what the builder left,
		// regardless of what the builder says about its own tests.
		staged, err := os.MkdirTemp("", "invariant-plumbing-staged-")
		if err != nil {
			buildErr = err
			r.Summary = "The factory couldn't stage the change: " + err.Error()
			break
		}
		defer os.RemoveAll(staged)
		checkCtx, cancel := budget.Context(ctx)
		r.Changes, err = Diff(checkCtx, root, ws, plan)
		if err == nil {
			err = Stage(checkCtx, root, ws, plan, staged)
		}
		r.Final = Result{}
		if err == nil {
			r.Final, err = b.Sandbox.Run(checkCtx, staged, plan)
		}
		if checkCtx.Err() != nil {
			err = checkCtx.Err()
		}
		cancel()
		r.Passed = err == nil && r.Final.Passed() && len(r.Changes.Written) > 0
		r.Summary = r.Final.Summary()
		if len(r.Changes.Written) == 0 {
			r.Summary = "The build changed none of the files it may write.\n" + r.Summary
		}
		if runErr != nil {
			r.Summary = fmt.Sprintf("The agent's run ended with an error: %v\n%s", runErr, r.Summary)
		}
		os.WriteFile(filepath.Join(out, fmt.Sprintf("final-%d.txt", round+1)), []byte(r.Final.Output), 0o644)
		os.WriteFile(filepath.Join(out, "final.txt"), []byte(r.Final.Output), 0o644)
		if err != nil {
			buildErr = err
			r.Summary += "\nThe factory's checks couldn't finish: " + err.Error()
			break
		}
		if !r.Passed {
			break
		}
		review, approved, err := b.review(ctx, root, staged, n, plan, r, out, budget)
		if ctx.Err() != nil {
			err = ctx.Err()
		} else if budget.Err() != nil {
			err = budget.Err()
		}
		os.WriteFile(filepath.Join(out, fmt.Sprintf("review-%d.txt", round+1)), []byte(review), 0o644)
		if err != nil {
			buildErr = err
			// An interrupted review cannot erase the findings that led to
			// this repair, or approve a candidate after its time expires.
			if review != "" {
				r.Review += "\n" + review
			}
			r.Review += "\nThe review couldn't finish: " + err.Error()
			break
		}
		r.Review, r.Approved = review, approved
		if r.Approved {
			break
		}
	}
	// Preserve a draft and its findings even when execution ran out of time.
	// This only collects files; no further agent or checks run here.
	r.Changes, err = Diff(context.WithoutCancel(ctx), root, ws, plan)
	if err == nil {
		err = apply(root, ws, plan)
	}
	if err != nil {
		r.Passed, r.Approved = false, false
		buildErr = errors.Join(buildErr, err)
		r.Summary += "\nThe candidate couldn't be copied: " + err.Error()
	}
	r.Usage.Backend = b.Backend.Name()
	r.Spend = r.Usage.CostUSD
	r.TestRuns, _ = readRuns(testLog)
	b2, _ := json.MarshalIndent(r, "", "  ")
	os.WriteFile(filepath.Join(out, "build.json"), append(b2, '\n'), 0o644)
	return r, buildErr
}

// apply copies the named files and new regression tests into root,
// removing named files the build removed.
func apply(root, ws string, plan *Plan) error {
	files, err := buildFiles(root, ws, plan)
	if err != nil {
		return err
	}
	for _, f := range files {
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
func (b Builder) review(ctx context.Context, root, candidate string, n int, plan *Plan, r *BuildResult, out string, budget *synth.ExecutionBudget) (string, bool, error) {
	ws, err := os.MkdirTemp("", "invariant-plumbing-review-")
	if err != nil {
		return "", false, err
	}
	defer os.RemoveAll(ws)
	prepCtx, cancel := budget.Context(ctx)
	defer cancel()
	if err := CopyCheckout(prepCtx, candidate, filepath.Join(ws, "repo")); err != nil {
		return "", false, err
	}
	if err := CopyCheckout(prepCtx, root, filepath.Join(ws, "original")); err != nil {
		return "", false, err
	}
	// Both trees are copies without .git. This includes new files and
	// deletions, compared with the original checkout in every round.
	diff, err := exec.CommandContext(prepCtx, "git", "-C", ws, "diff", "--no-index", "--no-color", "--", "original", "repo").Output()
	var exit *exec.ExitError
	if err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 1) {
		return "", false, fmt.Errorf("diffing the change: %w", err)
	}
	var news strings.Builder
	for _, f := range r.Changes.Written {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(f))); os.IsNotExist(err) {
			fmt.Fprintln(&news, f)
		}
	}
	planJSON, _ := json.MarshalIndent(plan, "", "  ")
	for name, text := range map[string]string{"plan.json": string(planJSON) + "\n", "change.diff": string(diff), "new-files.txt": news.String(), "tests.txt": r.Summary + "\n" + r.Final.Tail(6000)} {
		if err := os.WriteFile(filepath.Join(ws, name), []byte(text), 0o644); err != nil {
			return "", false, err
		}
	}
	if err := prepCtx.Err(); err != nil {
		return "", false, err
	}
	cancel()
	transcript, err := os.OpenFile(filepath.Join(out, "review-transcript.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return "", false, err
	}
	defer transcript.Close()
	usage, err := budget.Run(ctx, b.Reviewer, synth.Job{Workspace: ws, Prompt: ReviewPrompt(plan, n), ReadOnly: true, Transcript: transcript})
	r.Usage.Add(usage)
	text := strings.TrimSpace(usage.Summary)
	if err != nil {
		return text, false, err
	}
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
