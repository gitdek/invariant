package plumbing

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/gitdek/invariant/internal/regular"
	"github.com/gitdek/invariant/internal/repository"
)

// RepositoryAcceptanceAdapter is selected by the host, never by a candidate.
// Select validates the captured invocation form and resolves one exact test.
// There are no default adapters; framework discovery belongs to this boundary.
type RepositoryAcceptanceAdapter interface {
	Select(context.Context, repository.Part, Test) (RepositoryAcceptanceSelection, error)
}

type RepositoryAcceptanceSelection struct {
	Package string
	Argv    []string
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

func (s Sandbox) selectRepositoryAcceptance(ctx context.Context, plan *Plan) ([]RepositoryAcceptanceSelection, error) {
	selections := make([]RepositoryAcceptanceSelection, len(plan.Tests))
	for i, test := range plan.Tests {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		adapter := s.Repository.Adapters[test.Framework]
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

// checkRepositoryBase is the lower-level failure-evidence path. Public Check
// still refuses configured proposals until production adapters are admitted.
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
	for _, result := range run.TestResults {
		if result.Phase == "accept" && (result.Status != "failed" || result.Kind != "test" && result.Kind != "compile") {
			problems = append(problems, fmt.Sprintf("%s did not complete with attributable failure: %s (%s)", result.identity(), result.Status, result.Reason))
		}
	}
	c.Problem = strings.Join(problems, "; ")
	return c, err
}
