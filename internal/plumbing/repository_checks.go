package plumbing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/synth"
)

// RepositoryChecks binds execution to a captured contract. Neither the
// candidate nor the separate test-tool process supplies execution policy.
type RepositoryChecks struct {
	Snapshot     *repository.Snapshot
	Dependencies DependencyPreparer
}

// RepositoryCheckEnvironment retains a part's build outputs between commands.
// The caller closes it, including after a failed or canceled command, using
// a fresh bounded context. Diagnostics never supply result records.
type RepositoryCheckEnvironment interface {
	Run(context.Context, []string) ([]byte, error)
	Close(context.Context) error
}

type repositoryCheckRunner interface {
	OpenChecks(context.Context, DependencyRequest) (RepositoryCheckEnvironment, error)
}

// CommandResult records a declared check, not individual acceptance evidence.
type CommandResult struct {
	Part   string `json:"part"`
	Phase  string `json:"phase"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

var repositoryPhases = []string{"format", "lint", "build", "test"}

func (s Sandbox) runRepository(ctx context.Context, root string, plan *Plan) (Result, error) {
	run := &repositoryCheck{sandbox: s, root: root, plan: plan, started: time.Now(), result: unstartedAcceptance(plan)}
	// The build's pausable execution budget has no fixed Deadline. RunAgent
	// supplies its remaining allowance in Job.Timeout without pausing for this
	// host operation. No agent is started, and no fallback can renew the run.
	_, _, err := synth.RunAgent(ctx, run, synth.Job{}, sandboxLimit(s.Timeout, SandboxTimeout), "")
	// Once the checks ran, their own result already records their error.
	if !run.ran {
		run.result.Problems = append(run.result.Problems, fmt.Sprintf("repository checks could not start: %v", err))
	}
	if ctx.Err() != nil {
		run.result.Interrupted = true
		return run.result, nil
	}
	return run.result, err
}

type repositoryCheck struct {
	sandbox Sandbox
	root    string
	plan    *Plan
	started time.Time
	result  Result
	ran     bool
}

func (*repositoryCheck) Name() string { return "repository checks" }

func (r *repositoryCheck) Run(ctx context.Context, job synth.Job) (synth.Usage, error) {
	limit := sandboxLimit(r.sandbox.Timeout, SandboxTimeout)
	if job.Timeout < limit {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, r.started.Add(job.Timeout))
		defer cancel()
	}
	var err error
	r.ran = true
	r.result, err = r.sandbox.checkRepository(ctx, r.root, r.plan, r.started.Add(limit))
	return synth.Usage{}, err
}

func (s Sandbox) checkRepository(parent context.Context, root string, plan *Plan, end time.Time) (result Result, runErr error) {
	ownDeadline := true
	if parentEnd, ok := parent.Deadline(); ok && !parentEnd.After(end) {
		ownDeadline = false
		end = parentEnd
	}
	ctx, cancel := context.WithDeadline(parent, end)
	defer cancel()
	result = unstartedAcceptance(plan)
	defer func() {
		if parent.Err() != nil {
			result.Interrupted = true
			result.TimedOut = false
		} else if ctx.Err() != nil || !time.Now().Before(end) {
			// Equal-deadline child timers can fire before the whole-run timer.
			// The captured deadline, not timer delivery order, owns the result.
			result.DeadlineExceeded = ownDeadline
			result.Interrupted = !ownDeadline
			result.TimedOut = false
		}
		if runErr != nil {
			result.Problems = append(result.Problems, runErr.Error())
		}
		// Cancellation still delivers completed and unfinished work to tools.
		if result.Interrupted || result.DeadlineExceeded {
			runErr = nil
		}
	}()
	if s.Repository.Snapshot == nil || plan == nil || s.Original == "" {
		return result, errors.New("repository checks need a captured snapshot, plan and original checkout")
	}
	if err := plan.ValidateRepository(ctx, s.Repository.Snapshot); err != nil {
		return result, err
	}
	if plan.Native() {
		result.Problems = append(result.Problems, "native acceptance execution adapters are not available")
		return result, nil
	}
	parts, err := s.repositoryParts(ctx, root, plan)
	if err != nil {
		return result, err
	}
	var names []string
	for _, part := range parts {
		names = append(names, part.Name)
		for _, phase := range repositoryPhases {
			command := CommandResult{Part: part.Name, Phase: phase, Status: "not_run"}
			if reason := part.Commands[phase].NotApplicable; reason != "" {
				command.Status, command.Reason = "skipped", reason
			}
			result.CommandResults = append(result.CommandResults, command)
		}
	}
	if len(parts) == 0 {
		return result, nil
	}
	dependencies := s.Repository.Dependencies
	if dependencies.Runner == nil {
		dependencies.Runner = &DockerDependencyRunner{}
	}
	runner, ok := dependencies.Runner.(repositoryCheckRunner)
	if !ok {
		return result, errors.New("repository dependency runner cannot open persistent checks")
	}
	// All selection and preparation finish before candidate execution. Prepare
	// inherits this run's deadline, including time already spent on selection.
	session, err := dependencies.Prepare(ctx, s.Repository.Snapshot, names)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			result.TimedOut = true
		}
		return result, err
	}
	execution, finish := context.WithDeadline(ctx, session.deadline)
	defer finish()
	defer func() {
		if errors.Is(execution.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			result.TimedOut = true
		}
	}()
	work, err := stageDependencySource(execution, DependencyRequest{SourceDir: root, Directory: "."}, false)
	if err != nil {
		return result, err
	}
	var environment RepositoryCheckEnvironment
	activePart := ""
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), SandboxCleanupAllowance)
		defer done()
		if environment != nil {
			if err := environment.Close(cleanup); err != nil {
				runErr = errors.Join(runErr, fmt.Errorf("part %q cleanup: %w", activePart, err))
			}
		}
		if err := removeRepositoryStage(cleanup, work); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("repository staging cleanup: %w", err))
		}
	}()
	src := filepath.Join(work, "invariant", "work")
	testStarted := false
	for i, part := range parts {
		if err := execution.Err(); err != nil {
			result.TimedOut = ctx.Err() == nil
			return result, err
		}
		prepared := session.parts[part.Name]
		q := cloneDependencyRequest(prepared.request)
		q.Image, q.SourceDir = prepared.artifact.Image, src
		q.Network, q.Pull, q.Argv = false, false, nil
		q.Env = dependencyEnv(part.Language, false)
		q.Token, err = dependencyToken()
		if err != nil {
			return result, err
		}
		activePart = part.Name
		environment, err = runner.OpenChecks(execution, q)
		if err != nil {
			return result, fmt.Errorf("part %q startup: %w", part.Name, err)
		}
		if environment == nil {
			return result, fmt.Errorf("part %q startup returned no environment", part.Name)
		}
		for j, phase := range repositoryPhases {
			command := &result.CommandResults[i*len(repositoryPhases)+j]
			if command.Status == "skipped" {
				continue
			}
			if phase == "test" && !testStarted {
				var stop context.CancelFunc
				execution, stop = context.WithTimeout(execution, sandboxLimit(s.ShardTimeout, SandboxShardTimeout))
				defer stop()
				testStarted = true
			}
			if err := execution.Err(); err != nil {
				result.TimedOut = ctx.Err() == nil
				return result, err
			}
			command.Status = "running"
			out, err := environment.Run(execution, slices.Clone(part.Commands[phase].Argv))
			result.Output += fmt.Sprintf("%s/%s:\n%s\n", part.Name, phase, out)
			err = errors.Join(err, execution.Err())
			if err != nil {
				command.Status = "failed"
				result.TimedOut = errors.Is(execution.Err(), context.DeadlineExceeded) && ctx.Err() == nil
				result.Problems = append(result.Problems, fmt.Sprintf("part %q %s: %v", part.Name, phase, err))
				return result, nil
			}
			command.Status = "passed"
		}
		// Close before opening another part. The last environment and source
		// copy share the deferred cleanup reserve, even on cancellation.
		if i+1 < len(parts) {
			cleanup, done := context.WithTimeout(context.Background(), SandboxCleanupAllowance)
			err := environment.Close(cleanup)
			done()
			environment = nil
			if err != nil {
				return result, fmt.Errorf("part %q cleanup: %w", part.Name, err)
			}
		}
	}
	return result, nil
}

func (s Sandbox) repositoryParts(ctx context.Context, root string, plan *Plan) ([]repository.Part, error) {
	// Touches also includes a generated plan-lock path, which is not a
	// change whose owner runs checks. Only explicit files and admitted tests
	// enter ownership selection.
	paths := append(slices.Clone(plan.Files), plan.TestFiles()...)
	base, err := checkoutNames(ctx, s.Original)
	if err != nil {
		return nil, err
	}
	files, err := sandboxWalk(ctx, root)
	if err != nil {
		return nil, err
	}
	var candidates []string
	for name := range files {
		candidates = append(candidates, name)
	}
	paths = append(paths, plan.RegressionFiles(base, candidates)...)
	return s.Repository.Snapshot.PartsFor(paths)
}

func removeRepositoryStage(ctx context.Context, dir string) error {
	done := make(chan error, 1)
	go func() { done <- os.RemoveAll(dir) }()
	select {
	case err := <-done:
		return errors.Join(err, ctx.Err())
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r Result) repositorySummary() string {
	var b strings.Builder
	if r.Passed() {
		b.WriteString("Everything passed: the selected repository commands.\n")
	}
	if r.DeadlineExceeded {
		b.WriteString("The sandbox deadline expired; partial results follow.\n")
	}
	if r.Interrupted {
		b.WriteString("The sandbox was interrupted before all checks finished.\n")
	}
	if r.TimedOut {
		b.WriteString("The repository checks exhausted their shared phase allowance.\n")
	}
	for _, command := range r.CommandResults {
		fmt.Fprintf(&b, "%s/%s: %s", command.Part, command.Phase, command.Status)
		if command.Reason != "" {
			fmt.Fprintf(&b, " (%s)", command.Reason)
		}
		b.WriteByte('\n')
	}
	for _, result := range r.TestResults {
		if result.Phase == "accept" {
			fmt.Fprintf(&b, "accept %s: %s (%s)\n", result.identity(), result.Status, result.Reason)
		}
	}
	for _, problem := range r.Problems {
		fmt.Fprintln(&b, problem)
	}
	if failing := r.Failing(); len(failing) > 0 {
		fmt.Fprintf(&b, "Acceptance tests without individual evidence: %s\n", strings.Join(failing, ", "))
	}
	return b.String()
}

func unstartedAcceptance(plan *Plan) Result {
	r := Result{CommandResults: []CommandResult{}, Accepted: map[string]bool{}, TestResults: []TestResult{}}
	if plan != nil {
		for _, t := range plan.Tests {
			r.Accepted[t.Identity()] = false
			r.TestResults = append(r.TestResults, TestResult{Name: t.Name, File: t.File, Part: t.Part, Framework: t.Framework, Selector: t.Selector,
				Phase: "accept", Status: "not_run", Reason: "individual acceptance execution adapter is not available"})
		}
	}
	return r
}
