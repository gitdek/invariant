package plumbing

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/gitdek/invariant/internal/regular"
	"github.com/gitdek/invariant/internal/repository"
)

// RepositoryAcceptanceAdapter is selected by the host, never by a candidate.
// Select validates the captured invocation form and narrows its package scope.
// Go is enabled by default; discovery is an optional second boundary.
type RepositoryAcceptanceAdapter interface {
	Select(context.Context, repository.Part, Test) (RepositoryAcceptanceSelection, error)
}

type RepositoryAcceptanceSelection struct {
	Package string
	Argv    []string
}

// RepositoryAcceptanceDiscoverer confirms a declaration in the prepared part.
// Discovery may replace only the binding's package, and is not runtime evidence.
type RepositoryAcceptanceDiscoverer interface {
	Discover(context.Context, repository.Part, RepositoryAcceptanceInvocation, RepositoryCheckEnvironment) (RepositoryAcceptanceDiscovery, error)
}

type RepositoryAcceptanceDiscovery struct {
	Binding  RepositoryAcceptanceBinding
	TestName string
}

// RepositoryAcceptanceFileEnvironment reads clean part-relative regular files
// with safe ancestors from the same prepared environment that runs commands.
type RepositoryAcceptanceFileEnvironment interface {
	RepositoryCheckEnvironment
	ReadFile(context.Context, string) ([]byte, error)
}

type RepositoryAcceptanceDiscoveryError struct {
	Kind   string // refused or setup; neither is a completed test failure
	Reason string
	Err    error
}

func (e *RepositoryAcceptanceDiscoveryError) Error() string { return e.Kind + ": " + e.Reason }
func (e *RepositoryAcceptanceDiscoveryError) Unwrap() error { return e.Err }

func repositoryAcceptancePath(name string) bool {
	if !nativeText(name) || strings.Contains(name, "\\") || path.IsAbs(name) || path.Clean(name) != name || name == "." || name == ".." || strings.HasPrefix(name, "../") {
		return false
	}
	return !slices.Contains(strings.Split(name, "/"), ".git")
}

// RepositoryAcceptanceEnvironment supplies evidence separately from diagnostics.
// An error reports execution infrastructure, not a test's verdict. Run/Close-only
// environments can still run suites, but cannot supply individual acceptance.
type RepositoryAcceptanceEnvironment interface {
	RepositoryCheckEnvironment
	RunAcceptance(context.Context, RepositoryAcceptanceInvocation) (RepositoryAcceptanceEvidence, []byte, error)
}

type RepositoryAcceptanceInvocation struct {
	Binding RepositoryAcceptanceBinding
	Argv    []string
	Source  string
}

func cloneRepositoryPlan(plan *Plan) *Plan {
	if plan == nil {
		return nil
	}
	p := *plan
	p.Files, p.Tests, p.Trusted = slices.Clone(p.Files), slices.Clone(p.Tests), slices.Clone(p.Trusted)
	p.Sources = maps.Clone(p.Sources)
	if p.Execution != nil {
		execution := *p.Execution
		p.Execution = &execution
	}
	return &p
}

func (s Sandbox) repositoryAdapter(framework string) RepositoryAcceptanceAdapter {
	if adapter := s.Repository.Adapters[framework]; adapter != nil {
		return adapter
	}
	if framework == "go" {
		return GoRepositoryAcceptanceAdapter{}
	}
	return nil
}

func (s Sandbox) selectRepositoryAcceptance(ctx context.Context, plan *Plan) ([]RepositoryAcceptanceSelection, error) {
	selections := make([]RepositoryAcceptanceSelection, len(plan.Tests))
	for i, test := range plan.Tests {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		adapter := s.repositoryAdapter(test.Framework)
		if adapter == nil {
			return nil, fmt.Errorf("individual acceptance execution adapter is not available for %s", test.Identity())
		}
		// Config returns independent copies, including the command arguments.
		// An adapter cannot change the policy used by another selection or suite.
		var owner repository.Part
		for _, part := range s.Repository.Snapshot.Config().Parts {
			if part.Name == test.Part {
				owner = part
				break
			}
		}
		selection, err := adapter.Select(ctx, owner, test)
		if err := errors.Join(err, ctx.Err()); err != nil {
			return nil, fmt.Errorf("select %s: %w", test.Identity(), err)
		}
		if len(selection.Argv) == 0 || selection.Argv[0] == "" || test.Framework == "go" && selection.Package == "" {
			return nil, fmt.Errorf("select %s: adapter returned no invocation or package attribution", test.Identity())
		}
		for _, arg := range append(slices.Clone(selection.Argv), selection.Package) {
			if strings.ContainsRune(arg, 0) {
				return nil, fmt.Errorf("select %s: adapter returned an argument containing NUL", test.Identity())
			}
		}
		selection.Argv = slices.Clone(selection.Argv)
		selections[i] = selection
	}
	return selections, nil
}

// stageRepositoryAcceptance only changes the private, regular-file copy. An
// existing source may differ solely by the repository's line-ending convention.
func stageRepositoryAcceptance(ctx context.Context, root string, plan *Plan) error {
	for _, file := range plan.TestFiles() {
		if err := ctx.Err(); err != nil {
			return err
		}
		source := plan.Sources[file]
		data, err := regular.ReadFile(root, file)
		if err == nil {
			if !SameSource(string(data), source) {
				return fmt.Errorf("acceptance source %s differs from the ratified bytes", file)
			}
			if err := os.Remove(filepath.Join(root, filepath.FromSlash(file))); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := writeNativeFile(root, file, source); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func repositoryInvocation(plan *Plan, test Test, selection RepositoryAcceptanceSelection) RepositoryAcceptanceInvocation {
	source := plan.Sources[test.File]
	return RepositoryAcceptanceInvocation{
		Binding: RepositoryAcceptanceBinding{
			InvocationID: rand.Text(), Part: test.Part, Framework: test.Framework,
			File: test.File, Selector: test.Selector, Package: selection.Package,
			SourceDigest: dependencyHash([]byte(source)), ExecutionDigest: plan.Execution.Digest,
		},
		Argv: slices.Clone(selection.Argv), Source: source,
	}
}

// The shared lifecycle has already selected and discovered this invocation.
// The transport owns all acquired handles until the part's bounded cleanup;
// the producer and collector reuse the host's binding without another attempt.
func runRepositoryGoAcceptance(ctx context.Context, part repository.Part, in RepositoryAcceptanceInvocation, transport RepositoryProcessTransport) (RepositoryAcceptanceEvidence, []byte, error) {
	run := &GoAcceptanceRun{Invocation: cloneGoInvocation(in), invocation: cloneGoInvocation(in), environment: transport}
	startErr := run.start(ctx, part)
	if run.Process == nil {
		err := errors.Join(startErr, ctx.Err())
		if err == nil {
			err = errors.New("Go acceptance did not start a process")
		}
		return RepositoryAcceptanceEvidence{ExitCode: -1, Records: []RepositoryAcceptanceRecord{{Binding: in.Binding, Kind: "setup", Status: "failed", Reason: err.Error()}}}, nil, err
	}
	stream, waitErr := run.Wait(ctx)
	evidence, err := CollectGoAcceptance(in, stream, errors.Join(startErr, waitErr, ctx.Err()))
	out := append(slices.Clone(stream.Output.Stdout), stream.Output.Stderr...)
	return evidence, out, err
}

// checkRepositoryBase requires one eligible failure for every planned identity.
func (s Sandbox) checkRepositoryBase(ctx context.Context, root string, plan *Plan) (*Checked, error) {
	run, err := s.runRepositoryMode(ctx, root, plan, true)
	c := &Checked{Plan: plan, Run: run}
	problems := slices.Clone(run.Problems)
	if err != nil && len(problems) == 0 {
		problems = append(problems, err.Error())
	}
	if run.DeadlineExceeded || run.TimedOut || run.Interrupted {
		problems = append(problems, "acceptance checks exhausted their allowance or were interrupted")
	}
	for _, test := range plan.Tests {
		var matches []TestResult
		for _, result := range run.TestResults {
			if result.Phase == "accept" && result.identity() == test.Identity() {
				matches = append(matches, result)
			}
		}
		if len(matches) != 1 {
			problems = append(problems, fmt.Sprintf("%s needs exactly one completed failure, received %d results", test.Identity(), len(matches)))
			continue
		}
		result := matches[0]
		if result.Status != "failed" || result.Kind != "test" && result.Kind != "compile" {
			problems = append(problems, fmt.Sprintf("%s did not complete with attributable failure: %s (%s)", test.Identity(), result.Status, result.Reason))
		}
	}
	c.Problem = strings.Join(problems, "; ")
	return c, err
}
