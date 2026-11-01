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
	// guard stops its first run (D-0125). Empty never runs it again. A review
	// never falls back.
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
	// first run (D-0125). Usage includes both runs and every review and repair.
	Fallback *synth.Fallback `json:"fallback,omitempty"`
	// ProviderError says the build didn't finish: its agent's provider, or
	// its review's, stayed unavailable (#261). Nothing it left reached the
	// checkout, and a retry builds it again, review included.
	ProviderError string `json:"provider_error,omitempty"`
}

// Build implements issue n's ratified plan in the checkout at root, which
// holds the plan's record and its acceptance tests. The agent works on a
// copy outside the home directory. The named files and new regression tests
// in the plan's packages come back into root after the last round. Its
// transcripts and checks land in out.
func (b Builder) Build(ctx context.Context, root string, n int, out string) (_ *BuildResult, buildErr error) {
	lock, err := ReadLockFile(root, n)
	if err != nil {
		return nil, err
	}
	plan := &lock.Plan
	ctx, cancel := synth.WithExecutionBudget(ctx, b.Timeout)
	defer cancel()
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
	// only the first compiles everything. Both check formatting against the
	// original checkout, so a test it already held, which the build may not
	// edit, never blocks the build.
	b.Sandbox.Cache = filepath.Join(out, "gocache")
	b.Sandbox.Original = root
	// The cache is the build's own, so another build can't read what this
	// one wrote. Every check, repair, fallback and review that uses it has
	// ended by the time Build returns, whatever its outcome, and the run's
	// records stay (#366). Deferred before the build's record is written,
	// it's removed after it.
	defer RemoveCache(b.Sandbox.Cache)
	job := synth.Job{
		Workspace: ws,
		Prompt:    BuildPrompt(plan, n, b.TestRuns),
		GateServer: []string{b.Binary, "mcp", "-plumbing", "-ratified", root, "-issue", fmt.Sprint(n), "-max-runs", fmt.Sprint(b.TestRuns),
			"-log", testLog, "-cache", b.Sandbox.Cache, ws},
		Tools:      []string{"test"},
		Transcript: transcript,
	}
	r := &BuildResult{Tests: len(plan.Tests)}
	defer func() {
		r.TestRuns, _ = readRuns(testLog)
		r.Usage.Backend = b.Backend.Name()
		r.Spend = r.Usage.CostUSD
		if r.ProviderError != "" {
			// A build whose provider never let its agent or review finish
			// is recorded as failed, with nothing it left in the checkout,
			// whatever its time allowance did (#261).
			b2, _ := json.MarshalIndent(r, "", "  ")
			os.WriteFile(filepath.Join(out, "build.json"), append(b2, '\n'), 0o644)
			return
		}
		// Keep the original checkout fixed through every round, including the
		// test tool's baseline. Saving a timed-out candidate is not more work
		// for the agent: it leaves a draft and the findings for a person.
		changes, err := Diff(context.WithoutCancel(ctx), root, ws, plan)
		if err == nil {
			r.Changes = changes
			err = apply(root, ws, plan, changes.Written...)
		}
		buildErr = errors.Join(buildErr, err, ctx.Err())
		if buildErr != nil {
			r.Passed, r.Approved = false, false
			r.Summary += fmt.Sprintf("\nThe build stopped: %v\n", buildErr)
		}
		b2, _ := json.MarshalIndent(r, "", "  ")
		os.WriteFile(filepath.Join(out, "build.json"), append(b2, '\n'), 0o644)
	}()
	for round := 0; round <= 2; round++ {
		if err := ctx.Err(); err != nil {
			return r, err
		}
		r.Passed, r.Approved = false, false
		fallback := ""
		if round == 0 {
			fallback = b.FallbackEffort
		} else {
			job.Prompt = BuildPrompt(plan, n, b.TestRuns) + fmt.Sprintf("\nRepair attempt %d of 2. Continue in this workspace under the unchanged ratified plan. Address the latest review findings below, adding or correcting regression tests within the permitted packages. If a finding requires a different plan, explain what remains for a person.\n\n%s\n", round, r.Review)
		}
		usage, fb, runErr := synth.RunAgent(ctx, b.Backend, job, b.Timeout, fallback)
		r.Usage = synth.AddUsage(r.Usage, usage)
		if fb != nil {
			r.Fallback = fb
		}
		r.Final = Result{}
		r.Summary = ""
		if runErr != nil {
			r.Summary = fmt.Sprintf("The agent's run ended with an error: %v\n", runErr)
		}
		// What an agent its provider interrupted left is unfinished, however
		// it would do: it's never checked, reviewed or taken back (#261).
		if synth.ProviderUnavailable(runErr) {
			r.Passed, r.Approved, r.ProviderError = false, false, runErr.Error()
			return r, nil
		}
		if err := ctx.Err(); err != nil {
			return r, err
		}
		if r.Changes, err = Diff(ctx, root, ws, plan); err != nil {
			return r, err
		}
		// Each candidate gets fresh factory checks, whatever the agent says.
		staged, err := os.MkdirTemp("", "invariant-plumbing-staged-")
		if err != nil {
			return r, err
		}
		defer os.RemoveAll(staged)
		if err := Stage(ctx, root, ws, plan, staged); err != nil {
			return r, err
		}
		final, checkErr := b.Sandbox.Run(ctx, staged, plan)
		r.Final = final
		r.Summary += final.Summary()
		os.WriteFile(filepath.Join(out, fmt.Sprintf("checks-%d.txt", round)), []byte(final.Output), 0o644)
		os.WriteFile(filepath.Join(out, "final.txt"), []byte(final.Output), 0o644)
		if err := ctx.Err(); err != nil {
			return r, err
		}
		if checkErr != nil {
			return r, checkErr
		}
		r.Passed = final.Passed() && len(r.Changes.Written) > 0
		if len(r.Changes.Written) == 0 {
			r.Summary = "The build changed none of the permitted files.\n" + r.Summary
		}
		if !r.Passed {
			return r, nil
		}
		review, approved, reviewErr := b.review(ctx, root, staged, n, plan, r, out, round)
		if reviewErr != nil {
			// A failed review must not erase the last unresolved findings.
			if review != "" {
				r.Review += "\n" + review
			}
			r.Review += "\nThe review didn't finish: " + reviewErr.Error()
			// A review its provider never let finish leaves the build
			// unfinished, though its checks passed (#261).
			if synth.ProviderUnavailable(reviewErr) {
				r.Approved, r.ProviderError = false, reviewErr.Error()
				return r, nil
			}
			return r, reviewErr
		}
		r.Review, r.Approved = review, approved
		if r.Approved {
			return r, nil
		}
		m := verdictLine.FindStringSubmatch(review)
		if m == nil || !strings.EqualFold(m[1], "changes needed") {
			return r, nil
		}
	}
	return r, nil
}

// apply copies the named files and admitted regressions into root, removing
// the ones the build removed. extra comes from Diff against the original root.
func apply(root, ws string, plan *Plan, extra ...string) error {
	for _, f := range append(append([]string{}, plan.Files...), extra...) {
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
func (b Builder) review(ctx context.Context, root, candidate string, n int, plan *Plan, r *BuildResult, out string, round int) (string, bool, error) {
	ws, err := os.MkdirTemp("", "invariant-plumbing-review-")
	if err != nil {
		return "", false, err
	}
	defer os.RemoveAll(ws)
	if err := CopyCheckout(ctx, candidate, filepath.Join(ws, "repo")); err != nil {
		return "", false, err
	}
	if err := CopyCheckout(ctx, root, filepath.Join(ws, "base")); err != nil {
		return "", false, err
	}
	// Compare the whole candidate to the original checkout on every review,
	// including new tests and changes made before the most recent repair.
	diff, err := exec.CommandContext(ctx, "git", "-C", ws, "diff", "--no-index", "--no-color", "--", "base", "repo").Output()
	var exit *exec.ExitError
	if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1) {
		return "", false, fmt.Errorf("diffing the change: %w", err)
	}
	var news []string
	for _, f := range r.Changes.Written {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(f))); errors.Is(err, os.ErrNotExist) {
			news = append(news, f)
		}
	}
	planJSON, _ := json.MarshalIndent(plan, "", "  ")
	for name, text := range map[string]string{"plan.json": string(planJSON) + "\n", "change.diff": string(diff), "new-files.txt": strings.Join(news, "\n"), "tests.txt": r.Summary + "\n" + r.Final.Tail(6000)} {
		if err := os.WriteFile(filepath.Join(ws, name), []byte(text), 0o644); err != nil {
			return "", false, err
		}
	}
	transcript, err := os.OpenFile(filepath.Join(out, "review-transcript.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return "", false, err
	}
	defer transcript.Close()
	usage, _, err := synth.RunAgent(ctx, b.Reviewer, synth.Job{Workspace: ws, Prompt: ReviewPrompt(plan, n), ReadOnly: true, Transcript: transcript}, b.Timeout, "")
	r.Usage = synth.AddUsage(r.Usage, usage)
	text := strings.TrimSpace(usage.Summary)
	os.WriteFile(filepath.Join(out, fmt.Sprintf("review-%d.txt", round)), []byte(text), 0o644)
	// A provider that stayed unavailable until the time ran out is still why
	// the review didn't finish (#261).
	if ctx.Err() != nil && !synth.ProviderUnavailable(err) {
		return text, false, ctx.Err()
	}
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
