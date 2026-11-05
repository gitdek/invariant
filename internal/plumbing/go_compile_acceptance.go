package plumbing

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/gitdek/invariant/internal/repository"
)

//go:embed go_compile_helper.go.txt
var goCompileHelper []byte

const goCompileEventEnv = "INVARIANT_GO_COMPILE_EVENTS"

// A discovery failure retains its private declaration binding, but supplies
// no evidence. Standalone discovery and runtime callers still refuse it.
type goCompilationFailure struct {
	discovery RepositoryAcceptanceDiscovery
	err       error
}

func (e *goCompilationFailure) Error() string { return e.err.Error() }
func (e *goCompilationFailure) Unwrap() error { return e.err }

type goCompileRequest struct {
	Binding RepositoryAcceptanceBinding
	File    string
	Package string
	Source  string
	Flags   []string
	Argv    []string
}

type goCompileReport struct {
	Binding  RepositoryAcceptanceBinding
	ExitCode int
	Reason   string
	Problem  string
}

// The initial Run remains a discovery check, including its transport veto.
// A host-owned helper independently observes a compiler exit and attributes
// type errors without reading compiler diagnostics or executing test code.
func collectGoCompilation(ctx context.Context, part repository.Part, in RepositoryAcceptanceInvocation, failed *goCompilationFailure, env RepositoryProcessTransport) (*RepositoryAcceptanceEvidence, []byte, error) {
	var diagnostics []byte
	refuse := func(reason string, err error) (*RepositoryAcceptanceEvidence, []byte, error) {
		return nil, diagnostics, &RepositoryAcceptanceDiscoveryError{Kind: "setup", Reason: reason, Err: errors.Join(err, ctx.Err())}
	}
	binding := in.Binding
	binding.Package = failed.discovery.Binding.Package
	if binding != failed.discovery.Binding || !nativeText(binding.Package) || failed.discovery.TestName != strings.Split(binding.Selector, "/")[0] {
		return refuse("compiler discovery changed the bound invocation", nil)
	}
	file, err := goAcceptanceFile(part, binding.File)
	if err != nil {
		return refuse("invalid compiler source path", err)
	}
	source, err := env.ReadFile(ctx, file)
	if err != nil || string(source) != in.Source {
		return refuse("compiler source differs from the verbatim pinned bytes", err)
	}
	request := goCompileRequest{Binding: binding, File: file, Package: in.Binding.Package, Source: in.Source,
		Flags: goDiscoveryBuildFlags(in.Argv), Argv: append([]string{"test", "-c", "-o", "/dev/null"}, in.Argv[2:]...)}
	data, err := json.Marshal(request)
	if err != nil {
		return refuse("cannot encode compiler analysis request", err)
	}
	private := "go-compile-" + binding.InvocationID
	helper, err := env.Stage(ctx, private+"/main.go", goCompileHelper)
	if err != nil {
		return refuse("cannot stage compiler analysis helper", err)
	}
	input, err := env.Stage(ctx, private+"/request.json", data)
	if err != nil {
		return refuse("cannot stage compiler analysis request", err)
	}
	binary, err := env.Stage(ctx, private+"/analyze", nil)
	if err != nil {
		return refuse("cannot stage compiler analysis binary", err)
	}
	diagnostics, err = env.Run(ctx, []string{"go", "build", "-o", binary, helper})
	if err := errors.Join(err, ctx.Err()); err != nil {
		return refuse("cannot compile compiler analysis helper", err)
	}
	prepared, err := env.Prepare(ctx, RepositoryProcessSpec{Argv: []string{binary, input}, EventEnv: goCompileEventEnv})
	if err != nil || prepared == nil {
		return refuse("cannot prepare compiler analysis helper", err)
	}
	process, startErr := prepared.Start(ctx)
	if process == nil {
		return refuse("compiler analysis helper did not start", startErr)
	}
	out, waitErr := process.Wait(ctx)
	diagnostics = append(slices.Clone(out.Stdout), out.Stderr...)
	if err := errors.Join(startErr, waitErr, ctx.Err()); err != nil {
		return refuse("compiler analysis process failed: "+err.Error(), err)
	}
	if !out.Started || !out.Exited || !out.Drained || out.ExitCode != 0 {
		return refuse("compiler analysis did not exit and drain successfully", nil)
	}
	var report goCompileReport
	if err := json.Unmarshal(out.Events, &report); err != nil {
		return refuse("invalid private compiler analysis response", err)
	}
	if report.Binding != binding {
		return refuse("compiler analysis binding differs from this invocation", nil)
	}
	if report.Problem != "" {
		// A completed, conservative refusal is independent of later selectors.
		return refuse("Go compiler attribution refused: "+report.Problem, failed.err)
	}
	if report.ExitCode <= 0 || report.ExitCode >= 125 || strings.TrimSpace(report.Reason) == "" {
		return refuse("compiler analysis supplied no attributed completed failure", nil)
	}
	source, err = env.ReadFile(ctx, file)
	if err != nil || string(source) != in.Source {
		return refuse("compiler source changed during attribution", err)
	}
	evidence := &RepositoryAcceptanceEvidence{Complete: true, ExitCode: report.ExitCode, Records: []RepositoryAcceptanceRecord{
		{Binding: binding, Kind: "selection"},
		{Binding: binding, Kind: "compile", Status: "failed", Reason: report.Reason},
	}}
	if _, err := repositoryEvidence(binding, *evidence, TestResult{Phase: "accept", Part: binding.Part, Framework: binding.Framework, File: binding.File, Selector: binding.Selector}); err != nil {
		return refuse(fmt.Sprintf("invalid compiler evidence: %v", err), err)
	}
	return evidence, diagnostics, ctx.Err()
}
