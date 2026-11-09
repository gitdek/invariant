package plumbing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gitdek/invariant/internal/regular"
	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/synth"
)

// RepositoryChecks binds execution to a captured contract. Neither the
// candidate nor the separate test-tool process supplies execution policy.
type RepositoryChecks struct {
	Snapshot     *repository.Snapshot
	Dependencies DependencyPreparer
	Adapters     map[string]RepositoryAcceptanceAdapter
}

// RepositoryCheckEnvironment retains a part's build outputs between commands.
// The caller closes it, including after a failed or canceled command, using
// a fresh bounded context. Close reports shutdown deadline errors before
// removing host staging under its separate reserve. Diagnostics never supply
// result records.
type RepositoryCheckEnvironment interface {
	Run(context.Context, []string) ([]byte, error)
	Close(context.Context) error
}

type repositoryCheckRunner interface {
	OpenChecks(context.Context, DependencyRequest) (RepositoryCheckEnvironment, error)
}

// An environment that runs a part's declared commands differently from
// Invariant's own helpers implements RunCommand; Run stays the helpers' (#765).
type repositoryCommandEnvironment interface {
	RunCommand(context.Context, []string) ([]byte, error)
}

func runRepositoryCommand(ctx context.Context, environment RepositoryCheckEnvironment, argv []string) ([]byte, error) {
	if commands, ok := environment.(repositoryCommandEnvironment); ok {
		return commands.RunCommand(ctx, argv)
	}
	return environment.Run(ctx, argv)
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
	return s.runRepositoryMode(ctx, root, plan, false)
}

func (s Sandbox) runRepositoryMode(ctx context.Context, root string, plan *Plan, base bool) (Result, error) {
	started := time.Now()
	plan = cloneRepositoryPlan(plan)
	if s.Repository != nil {
		captured := *s.Repository
		captured.Adapters = maps.Clone(captured.Adapters)
		s.Repository = &captured
	}
	run := &repositoryCheck{sandbox: s, root: root, plan: plan, base: base, started: started, result: unstartedAcceptance(plan)}
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
	base    bool
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
	r.result, err = r.sandbox.checkRepository(ctx, r.root, r.plan, r.started, r.started.Add(limit), r.base)
	return synth.Usage{}, err
}

func (s Sandbox) checkRepository(parent context.Context, root string, plan *Plan, started, end time.Time, base bool) (result Result, runErr error) {
	ownDeadline := true
	if parentEnd, ok := parent.Deadline(); ok && !parentEnd.After(end) {
		ownDeadline = false
		end = parentEnd
	}
	ctx, cancel := context.WithDeadline(parent, end)
	defer cancel()
	preparerLimit := SandboxTimeout
	if s.Repository != nil {
		preparerLimit = sandboxLimit(s.Repository.Dependencies.Timeout, SandboxTimeout)
	}
	execution, finish := context.WithDeadline(ctx, started.Add(preparerLimit))
	defer finish()
	var stopTests context.CancelFunc
	defer func() {
		if stopTests != nil {
			stopTests()
		}
	}()
	executionEnd, _ := execution.Deadline()
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
		} else if !time.Now().Before(executionEnd) {
			result.TimedOut = true
		}
		if runErr != nil {
			result.Problems = append(result.Problems, runErr.Error())
		}
		reason := "earlier work did not finish successfully"
		if len(result.Problems) > 0 {
			reason = result.Problems[len(result.Problems)-1]
		} else if result.Interrupted || result.DeadlineExceeded || result.TimedOut {
			reason = "the check exhausted its allowance or was interrupted"
		}
		for i := range result.CommandResults {
			command := &result.CommandResults[i]
			if command.Status == "not_run" {
				command.Reason = reason
			}
		}
		for i := range result.TestResults {
			test := &result.TestResults[i]
			if test.Status == "not_run" && (test.Reason == "acceptance invocation has not started" ||
				len(result.Problems) > 0 && test.Reason == "individual acceptance execution adapter is not available") {
				test.Reason = reason
			}
		}
		// Cancellation still delivers completed and unfinished work to tools.
		if result.Interrupted || result.DeadlineExceeded {
			runErr = nil
		}
	}()
	if s.Repository == nil || s.Repository.Snapshot == nil || plan == nil || s.Original == "" {
		return result, errors.New("repository checks need a captured snapshot, plan and original checkout")
	}
	if plan.Native() || base {
		if err := plan.Validate(); err != nil {
			return result, err
		}
	}
	if !plan.Native() && (base || len(plan.Tests) > 0) {
		result.Problems = append(result.Problems, "configured acceptance needs native identities; legacy acceptance requires the unconfigured Go runner")
		return result, nil
	}
	if err := plan.ValidateRepository(execution, s.Repository.Snapshot); err != nil {
		return result, err
	}
	var selections []RepositoryAcceptanceSelection
	if plan.Native() {
		var err error
		selections, err = s.selectRepositoryAcceptance(execution, plan)
		if err != nil {
			// Unsupported native execution has always been a failed report,
			// including across the queue, rather than an IPC transport error.
			result.Problems = append(result.Problems, err.Error())
			return result, nil
		}
		for i, selection := range selections {
			if _, discovers := s.repositoryAdapter(plan.Tests[i].Framework).(RepositoryAcceptanceDiscoverer); !discovers {
				result.TestResults[i].Package = selection.Package
			}
			result.TestResults[i].Reason = "acceptance invocation has not started"
		}
	}
	parts, err := s.repositoryParts(execution, root, plan)
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
	var work string
	if base || s.stage {
		work, err = os.MkdirTemp("", "invariant-base-check-")
	} else {
		work, err = stageDependencySource(execution, DependencyRequest{SourceDir: root, Directory: "."}, false)
	}
	if err != nil {
		return result, err
	}
	var environment RepositoryCheckEnvironment
	activePart := ""
	partWork := ""
	cleanupWork := func(last bool) error {
		cleanup, done := context.WithTimeout(context.Background(), SandboxCleanupAllowance)
		defer done()
		var err error
		if environment != nil {
			err = environment.Close(cleanup)
			environment = nil
		}
		if partWork != "" {
			err = errors.Join(err, removeRepositoryStage(cleanup, partWork))
			partWork = ""
		}
		if err != nil {
			err = fmt.Errorf("part %q cleanup: %w", activePart, err)
		}
		if last || err != nil || execution.Err() != nil {
			if stageErr := removeRepositoryStage(cleanup, work); stageErr != nil {
				err = errors.Join(err, fmt.Errorf("repository staging cleanup: %w", stageErr))
			}
			work = ""
		}
		return err
	}
	defer func() {
		if work != "" {
			runErr = errors.Join(runErr, cleanupWork(true))
		}
	}()
	src := filepath.Join(work, "invariant", "work")
	if base {
		if err := stageRepositoryBase(execution, s.Original, s.Repository.Snapshot, src); err != nil {
			return result, err
		}
	} else if s.stage {
		if err := Stage(execution, s.Original, root, plan, src); err != nil {
			return result, err
		}
	}
	if !base {
		if err := checkRegressionPolicy(execution, s.Original, src, plan); err != nil {
			return result, err
		}
	}
	if plan.Native() {
		if err := stageRepositoryAcceptance(execution, src, plan); err != nil {
			return result, err
		}
		if err := s.validateRepositoryInputs(execution, src, parts); err != nil {
			return result, err
		}
	}
	if base {
		problem, err := checkPlanDecisions(src, src)
		if err != nil {
			return result, err
		}
		if problem != "" {
			result.Problems = append(result.Problems, problem)
			return result, nil
		}
	}
	// All selection and safe staging finish before preparation. Prepare inherits
	// the original deadline, including any shorter preparer allowance.
	var session *DependencySession
	if plan.Native() {
		session, err = s.prepareRepositoryGo(execution, dependencies, parts)
	} else {
		session, err = dependencies.prepareInputs(execution, s.Repository.Snapshot, names, func(ctx context.Context, name string) ([]byte, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return regular.ReadFile(src, name)
		})
	}
	if err != nil {
		return result, err
	}
	testStarted := false
	startTests := func() {
		if !testStarted {
			execution, stopTests = context.WithTimeout(execution, sandboxLimit(s.ShardTimeout, SandboxShardTimeout))
			executionEnd, _ = execution.Deadline()
			testStarted = true
		}
	}
	// Only private copies reach workloads. The staged base/candidate and the
	// verified prepared image remain pristine for every later invocation.
	openWork := func(part repository.Part) error {
		if err := execution.Err(); err != nil {
			return err
		}
		prepared := session.parts[part.Name]
		q := cloneDependencyRequest(prepared.request)
		activePart = part.Name
		partWork, err = stageDependencySource(execution, DependencyRequest{SourceDir: src, Directory: "."}, false)
		if err != nil {
			return err
		}
		q.Image, q.SourceDir = prepared.artifact.Image, filepath.Join(partWork, "invariant", "work")
		if err := verifyDependencyInputs(execution, q.SourceDir, q.Inputs); err != nil {
			return err
		}
		q.Network, q.Pull, q.Argv = false, false, nil
		q.Env = dependencyEnv(part.Language, false)
		// Installed Node dependencies come only from the prepared image. A
		// candidate's node_modules must not overlay that captured preparation.
		if part.Language == "typescript" {
			if err := os.RemoveAll(filepath.Join(q.SourceDir, filepath.FromSlash(part.Directory), "node_modules")); err != nil {
				return err
			}
		}
		q.Token, err = dependencyToken()
		if err != nil {
			return err
		}
		environment, err = runner.OpenChecks(execution, q)
		if err != nil {
			return fmt.Errorf("part %q startup: %w", part.Name, err)
		}
		if environment == nil {
			return fmt.Errorf("part %q startup returned no environment", part.Name)
		}
		return execution.Err()
	}
	for i, part := range parts {
		for j, phase := range repositoryPhases {
			command := &result.CommandResults[i*len(repositoryPhases)+j]
			if command.Status == "skipped" {
				continue
			}
			// A part's commands share build outputs, but an entirely
			// inapplicable command list needs no container.
			if environment == nil {
				if err := openWork(part); err != nil {
					return result, err
				}
			}
			if phase == "test" {
				startTests()
			}
			if err := execution.Err(); err != nil {
				result.TimedOut = ctx.Err() == nil
				return result, err
			}
			command.Status = "running"
			out, err := runRepositoryCommand(execution, environment, slices.Clone(part.Commands[phase].Argv))
			result.Output += fmt.Sprintf("%s/%s:\n%s\n", part.Name, phase, out)
			err = errors.Join(err, execution.Err())
			if err != nil {
				command.Status = "failed"
				command.Reason = err.Error()
				result.TimedOut = errors.Is(execution.Err(), context.DeadlineExceeded) && ctx.Err() == nil
				if base && execution.Err() == nil && (phase == "build" || phase == "test") && repositoryCommandFailed(err) {
					// Ordinary suite failures never substitute for individual
					// evidence or prevent independent selectors from running.
					continue
				}
				result.Problems = append(result.Problems, fmt.Sprintf("part %q %s: %v", part.Name, phase, err))
				return result, nil
			}
			command.Status = "passed"
		}
		for j, test := range plan.Tests {
			if !plan.Native() || test.Part != part.Name {
				continue
			}
			// Cleanup owns the queue slot and must finish before another
			// invocation starts, including after ordinary base failures.
			if err := cleanupWork(false); err != nil {
				return result, err
			}
			startTests()
			if err := openWork(part); err != nil {
				return result, err
			}
			invocation := repositoryInvocation(plan, test, selections[j])
			transport, processCapable := environment.(RepositoryProcessTransport)
			goRuntime := test.Framework == "go" && processCapable
			_, defaultPytest := s.repositoryAdapter(test.Framework).(PytestRepositoryAcceptanceAdapter)
			pytestRuntime := defaultPytest && processCapable
			nodeRuntime := defaultNodeAdapter(s.repositoryAdapter(test.Framework)) && processCapable
			nodeSeal := ""
			if nodeRuntime {
				nodeSeal, err = sealNodeDependencies(execution, environment)
				if err != nil {
					return result, err
				}
			}
			discovered := false
			if adapter, ok := s.repositoryAdapter(test.Framework).(RepositoryAcceptanceDiscoverer); ok {
				startTests()
				expected := invocation.Binding
				input := invocation
				input.Argv = slices.Clone(invocation.Argv)
				discovery, err := adapter.Discover(execution, part, input, environment)
				err = errors.Join(err, execution.Err())
				var compilation *RepositoryAcceptanceEvidence
				var failed *goCompilationFailure
				if goRuntime && execution.Err() == nil && repositoryCommandFailed(err) && errors.As(err, &failed) {
					var out []byte
					discovery = failed.discovery
					compilation, out, err = collectGoCompilation(execution, part, input, failed, transport)
					result.Output += fmt.Sprintf("compile %s:\n%s\n", test.Identity(), out)
				}
				if err == nil {
					expected.Package = discovery.Binding.Package
					if discovery.Binding != expected || !nativeText(expected.Package) || discovery.TestName != strings.Split(test.Selector, "/")[0] {
						err = &RepositoryAcceptanceDiscoveryError{Kind: "refused", Reason: "discovery does not match this invocation, identity, source and configuration"}
					}
				}
				if err != nil {
					record := &result.TestResults[j]
					record.Package, record.Kind, record.Status, record.Reason = "", "setup", "refused", err.Error()
					var detail *RepositoryAcceptanceDiscoveryError
					if errors.As(err, &detail) && detail.Kind == "refused" {
						record.Kind = "test"
					}
					if base && goRuntime && execution.Err() == nil && detail != nil && detail.Kind == "setup" && repositoryCommandFailed(err) {
						// A selected package's compiler failure is a setup refusal,
						// not evidence about any independent test or package.
						continue
					}
					return result, fmt.Errorf("discover %s: %w", test.Identity(), err)
				}
				invocation.Binding = discovery.Binding
				result.TestResults[j].Package = discovery.Binding.Package
				if compilation != nil {
					var evidenceErr error
					result.TestResults[j], evidenceErr = repositoryEvidence(invocation.Binding, *compilation, result.TestResults[j])
					if err := errors.Join(evidenceErr, execution.Err()); err != nil {
						return result, err
					}
					continue
				}
				discovered = true
			}
			acceptance, ok := environment.(RepositoryAcceptanceEnvironment)
			if goRuntime && !discovered {
				return result, fmt.Errorf("accept %s: Go process execution needs successful discovery", test.Identity())
			}
			if !goRuntime && !pytestRuntime && !nodeRuntime && !ok {
				result.TestResults[j].Reason = "prepared environment has no runtime acceptance evidence collector"
				return result, fmt.Errorf("part %q environment cannot supply individual acceptance evidence", part.Name)
			}
			startTests()
			if err := execution.Err(); err != nil {
				return result, err
			}
			expected := invocation.Binding
			var evidence RepositoryAcceptanceEvidence
			var out []byte
			var err error
			if goRuntime {
				evidence, out, err = runRepositoryGoAcceptance(execution, part, invocation, transport)
			} else if pytestRuntime {
				evidence, out, err = runRepositoryPytestAcceptance(execution, part, invocation, transport)
			} else if nodeRuntime {
				if err = checkNodeDependencies(execution, environment, nodeSeal); err == nil {
					err = checkNodeInputs(execution, s.Repository.Snapshot, part, transport)
				}
				if err == nil {
					evidence, out, err = runRepositoryNodeAcceptance(execution, part, invocation, transport)
					err = errors.Join(err, checkNodeInputs(execution, s.Repository.Snapshot, part, transport), checkNodeDependencies(execution, environment, nodeSeal))
				}
			} else {
				evidence, out, err = acceptance.RunAcceptance(execution, invocation)
			}
			result.Output += fmt.Sprintf("accept %s:\n%s\n", test.Identity(), out)
			var evidenceErr error
			result.TestResults[j], evidenceErr = repositoryEvidenceMode(expected, evidence, result.TestResults[j], goRuntime)
			result.Accepted[test.Identity()] = result.TestResults[j].Status == "passed"
			if err := errors.Join(err, evidenceErr, execution.Err()); err != nil {
				return result, fmt.Errorf("accept %s: %w", test.Identity(), err)
			}
		}
		// Container shutdown and host tree removal have separate reserves.
		if err := cleanupWork(i+1 == len(parts)); err != nil {
			return result, err
		}
		if err := execution.Err(); err != nil {
			return result, err
		}
	}
	return result, nil
}

// Preparation uses captured inputs. The code under test must see those same
// bytes, including lockfiles, rather than a candidate's replacement graph.
func (s Sandbox) validateRepositoryInputs(ctx context.Context, root string, parts []repository.Part) error {
	inputs := map[string]string{}
	for _, part := range parts {
		for _, name := range append(slices.Clone(part.Inputs), part.Lockfiles...) {
			data, err := s.Repository.Snapshot.ReadFileContext(ctx, name)
			if err != nil {
				return err
			}
			inputs[name] = dependencyHash(data)
		}
	}
	return verifyDependencyInputs(ctx, root, inputs)
}

// Native Go may declare its module identity while requiring no installation.
// Seal that empty graph with the existing preparation, input verification and
// cache machinery. Command-only checks retain their original install policy.
func (s Sandbox) prepareRepositoryGo(ctx context.Context, preparer DependencyPreparer, parts []repository.Part) (*DependencySession, error) {
	type emptyPart struct {
		part    repository.Part
		files   map[string][]byte
		request DependencyRequest
	}
	var empty []emptyPart
	var names []string
	for _, part := range parts {
		if part.Language != "go" || part.Commands["install"].NotApplicable == "" || len(part.Inputs)+len(part.Lockfiles) == 0 {
			names = append(names, part.Name)
			continue
		}
		files := map[string][]byte{}
		for _, name := range append(slices.Clone(part.Inputs), part.Lockfiles...) {
			data, err := s.Repository.Snapshot.ReadFileContext(ctx, name)
			if err != nil {
				return nil, err
			}
			files[name] = data
		}
		if err := repositoryGoEmptyInputs(part, files); err != nil {
			return nil, fmt.Errorf("part %q: %w", part.Name, err)
		}
		q := DependencyRequest{Image: part.Image, Platform: preparer.Platform, Directory: part.Directory,
			Env: dependencyEnv("go", false), Inputs: map[string]string{}, Pull: true,
			Init: true, MemoryBytes: 4 << 30, PidsLimit: 1024}
		for name, data := range files {
			q.Inputs[name] = dependencyHash(data)
		}
		// Keep declared roles, exact bytes and the inapplicable reason in the
		// recipe. This offline empty-graph recipe cannot reuse an install's key.
		data, err := json.Marshal(struct {
			Version       string
			Inputs, Locks []string
			Install       repository.Command
			Request       DependencyRequest
		}{dependencyRecipe + "/go-empty-v1", part.Inputs, part.Lockfiles, part.Commands["install"], q})
		if err != nil {
			return nil, err
		}
		q.Key = dependencyHash(data)
		empty = append(empty, emptyPart{part, files, q})
	}
	if len(empty) > 0 && (!dependencyPlatform.MatchString(preparer.Platform) || preparer.CacheDir == "") {
		return nil, errors.New("dependency preparation needs a cache directory and explicit Linux platform")
	}
	session, err := preparer.Prepare(ctx, s.Repository.Snapshot, names)
	if err != nil {
		return nil, err
	}
	if len(empty) > 0 {
		if err := os.MkdirAll(preparer.CacheDir, 0o700); err != nil {
			return nil, err
		}
	}
	for _, item := range empty {
		artifact, err := preparer.prepare(ctx, session.runner, item.request, item.files)
		if err != nil {
			return nil, fmt.Errorf("prepare part %q: %w", item.part.Name, err)
		}
		session.parts[item.part.Name] = preparedDependency{part: item.part, request: item.request, artifact: cloneDependencyArtifact(artifact)}
	}
	return session, ctx.Err()
}

func repositoryGoEmptyInputs(part repository.Part, files map[string][]byte) error {
	mod, sum := path.Join(part.Directory, "go.mod"), path.Join(part.Directory, "go.sum")
	if !slices.Equal(part.Inputs, []string{mod}) || len(part.Lockfiles) > 1 || len(part.Lockfiles) == 1 && part.Lockfiles[0] != sum {
		return errors.New("Go install.not_applicable permits only go.mod and an optional empty go.sum")
	}
	if err := goDependencyLock(part, files); err != nil {
		return err
	}
	if strings.TrimSpace(string(files[sum])) != "" {
		return errors.New("Go install.not_applicable requires an empty dependency graph")
	}
	inRequire := false
	for _, line := range strings.Split(string(files[mod]), "\n") {
		line, _, _ = strings.Cut(line, "//")
		words := strings.Fields(line)
		if len(words) == 0 {
			continue
		}
		if inRequire {
			if len(words) != 1 || words[0] != ")" {
				return errors.New("Go install.not_applicable requires an empty dependency graph")
			}
			inRequire = false
		} else if words[0] == "require" {
			if len(words) != 2 || words[1] != "(" {
				return errors.New("Go install.not_applicable requires an empty dependency graph")
			}
			inRequire = true
		}
	}
	return nil
}

// Only a reported command exit may be expected on the base. Launch failures
// and Docker's reserved startup/signal statuses veto it even if tests failed.
// Run's error is a trusted host result; diagnostics are never inspected here.
func repositoryCommandFailed(err error) bool {
	if err == nil {
		return false
	}
	// A joined transport or setup error still vetoes the check, even when
	// another cause reports an ordinary command exit.
	switch wrapped := err.(type) {
	case interface{ Unwrap() []error }:
		causes := wrapped.Unwrap()
		if len(causes) == 0 {
			return false
		}
		for _, cause := range causes {
			if !repositoryCommandFailed(cause) {
				return false
			}
		}
		return true
	case interface{ Unwrap() error }:
		return repositoryCommandFailed(wrapped.Unwrap())
	}
	code := 0
	if exit, ok := err.(interface{ ExitCode() int }); ok {
		code = exit.ExitCode()
	} else if text, ok := strings.CutPrefix(err.Error(), "exit status "); ok {
		code, _ = strconv.Atoi(text)
	}
	return code > 0 && code < 125
}

func (s Sandbox) repositoryParts(ctx context.Context, root string, plan *Plan) ([]repository.Part, error) {
	// Touches also includes a generated plan-lock path, which is not a
	// change whose owner runs checks. Only explicit files and admitted tests
	// enter ownership selection.
	paths := append(slices.Clone(plan.Files), plan.TestFiles()...)
	scope, err := checkoutRegressionScope(ctx, s.Original, plan)
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
	paths = append(paths, scope.Files(candidates)...)
	return s.Repository.Snapshot.PartsFor(paths)
}

func removeRepositoryStage(_ context.Context, dir string) error {
	return CleanupRepositoryStage(dir)
}

// CleanupRepositoryStage owns a fresh two-minute host filesystem reserve,
// independent of container shutdown and execution. Removal is synchronous:
// callers retain their queue slot until it finishes or stops at its deadline.
func CleanupRepositoryStage(dir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return errors.Join(removeRepositoryTree(ctx, dir), ctx.Err())
}

func removeRepositoryTree(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Lstat(name)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return os.Remove(name)
	}
	dir, err := os.Open(name)
	if err != nil {
		return err
	}
	var removalErr error
	for ctx.Err() == nil {
		entries, readErr := dir.ReadDir(128)
		for _, entry := range entries {
			removalErr = errors.Join(removalErr, removeRepositoryTree(ctx, filepath.Join(name, entry.Name())))
			if ctx.Err() != nil {
				break
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				removalErr = errors.Join(removalErr, readErr)
			}
			break
		}
	}
	removalErr = errors.Join(removalErr, dir.Close(), ctx.Err())
	if ctx.Err() == nil {
		removalErr = errors.Join(removalErr, os.Remove(name))
	}
	return removalErr
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
		fmt.Fprintf(&b, "Acceptance tests without a unique runtime pass: %s\n", strings.Join(failing, ", "))
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
