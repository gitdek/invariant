package plumbing

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/gitdek/invariant/internal/repository"
)

// GoFrameworkEvent is a raw observation from testing, not acceptance evidence.
// Parent is testing's runtime parent, which need not be a slash prefix of Name.
type GoFrameworkEvent struct {
	Name, Parent, Action, Text string
}

type GoAcceptanceStream struct {
	Invocation RepositoryAcceptanceInvocation
	Events     []GoFrameworkEvent
	Output     RepositoryProcessOutput
}

type GoAcceptanceRun struct {
	Invocation RepositoryAcceptanceInvocation
	Process    RepositoryProcess

	environment RepositoryProcessTransport
	prepared    RepositoryPreparedProcess
	mu          sync.Mutex
	err         error
	closed      bool
}

// StartGoAcceptance reuses selection, verbatim staging (in prepare), and
// discovery before compiling any instrumentation. It grants no new allowance.
// A nonnil run must be closed even when preparation or startup returns an error.
func StartGoAcceptance(ctx context.Context, part repository.Part, test Test, source, executionDigest string, prepare func(context.Context, RepositoryAcceptanceInvocation) (RepositoryProcessTransport, error)) (*GoAcceptanceRun, error) {
	adapter := GoRepositoryAcceptanceAdapter{}
	selection, err := adapter.Select(ctx, part, test)
	if err != nil {
		return nil, err
	}
	if !dependencyID.MatchString(executionDigest) || prepare == nil {
		return nil, errors.New("Go producer needs a captured execution digest and host preparer")
	}
	in := repositoryInvocation(&Plan{Sources: map[string]string{test.File: source}, Execution: &Execution{Digest: executionDigest}}, test, selection)
	r := &GoAcceptanceRun{Invocation: in}
	fail := func(err error) (*GoAcceptanceRun, error) {
		r.err = errors.Join(err, ctx.Err())
		return r, r.err
	}
	r.environment, err = prepare(ctx, cloneGoInvocation(in))
	if err = errors.Join(err, ctx.Err()); err != nil {
		return fail(err)
	}
	if r.environment == nil {
		return fail(errors.New("Go producer preparer returned no environment"))
	}
	discovered, err := adapter.Discover(ctx, part, cloneGoInvocation(in), r.environment)
	if err != nil {
		return fail(err)
	}
	r.Invocation.Binding = discovered.Binding
	helper, err := r.environment.Stage(ctx, "go-producer/main.go", []byte(goTestingHelper))
	if err != nil {
		return fail(err)
	}
	launcher := helper + ".bin"
	// The helper runs in the prepared toolchain's filesystem. A future transport
	// needs no host path access and uses these same producer assets and protocol.
	if out, err := r.environment.Run(ctx, []string{"go", "build", "-o", launcher, helper}); err != nil {
		return fail(fmt.Errorf("build Go testing helper: %w: %s", err, out))
	}
	overlay := helper + ".overlay.json"
	if out, err := r.environment.Run(ctx, []string{launcher, "instrument", overlay}); err != nil {
		return fail(fmt.Errorf("instrument pinned Go testing: %w: %s", err, out))
	}
	binary := helper + ".test"
	argv := append([]string{"go", "test", "-c", "-o", binary, "-overlay", overlay}, in.Argv[2:]...)
	if out, err := r.environment.Run(ctx, argv); err != nil {
		return fail(fmt.Errorf("compile instrumented Go tests: %w: %s", err, out))
	}
	file, err := goAcceptanceFile(part, test.File)
	if err != nil {
		return fail(err)
	}
	// go test normally starts its binary in the package directory, not the part
	// directory. The host helper chdirs and execs without a shell or child runner.
	argv = append([]string{launcher, "execute", path.Dir(file), binary}, goProducerTestFlags(in.Argv)...)
	r.prepared, err = r.environment.Prepare(ctx, RepositoryProcessSpec{Argv: argv, EventEnv: "INVARIANT_GO_EVENTS"})
	if err = errors.Join(err, ctx.Err()); err != nil {
		return fail(err)
	}
	if r.prepared == nil {
		return fail(errors.New("Go producer transport returned no prepared process"))
	}
	r.Process, err = r.prepared.Start(ctx)
	if err = errors.Join(err, ctx.Err()); err != nil {
		return fail(err)
	}
	if r.Process == nil {
		return fail(errors.New("Go producer transport returned no process"))
	}
	return r, nil
}

func cloneGoInvocation(in RepositoryAcceptanceInvocation) RepositoryAcceptanceInvocation {
	in.Argv = slices.Clone(in.Argv)
	return in
}

// Select has validated the complete grammar. Only the Go driver's build flags
// and package operand disappear at the binary boundary; test values and their
// separate/equal spellings survive, including false and timeout zero.
func goProducerTestFlags(argv []string) []string {
	var out []string
	timeout := false
	for i := 2; i < len(argv); i++ {
		flag, _, equal := strings.Cut(argv[i], "=")
		switch flag {
		case "-run", "-timeout", "-parallel", "-cpu", "-shuffle", "-count", "-short", "-v":
			timeout = timeout || flag == "-timeout"
			out = append(out, "-test."+strings.TrimPrefix(argv[i], "-"))
			if !equal && flag != "-short" && flag != "-v" {
				i++
				out = append(out, argv[i])
			}
		case "-tags", "-p", "-mod":
			if !equal {
				i++
			}
		}
	}
	if !timeout {
		out = append(out, "-test.timeout=10m") // go test's default, still capped by ctx.
	}
	return out
}

func (r *GoAcceptanceRun) Wait(ctx context.Context) (GoAcceptanceStream, error) {
	s := GoAcceptanceStream{Invocation: cloneGoInvocation(r.Invocation)}
	var err error
	if r.Process == nil {
		err = errors.New("Go producer did not start")
	} else {
		s.Output, err = r.Process.Wait(ctx)
		var decodeErr error
		s.Events, decodeErr = decodeGoFrameworkEvents(s.Output.Events)
		err = errors.Join(err, decodeErr)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.err = errors.Join(r.err, err)
	return s, r.err
}

func (r *GoAcceptanceRun) Close(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return r.err
	}
	r.closed = true
	ctx, cancel := context.WithTimeout(ctx, SandboxCleanupAllowance)
	defer cancel()
	if r.Process != nil {
		r.err = errors.Join(r.err, r.Process.Close(ctx))
	}
	if r.prepared != nil {
		r.err = errors.Join(r.err, r.prepared.Close(ctx))
	}
	if r.environment != nil {
		r.err = errors.Join(r.err, r.environment.Close(ctx))
	}
	return r.err
}

func decodeGoFrameworkEvents(data []byte) ([]GoFrameworkEvent, error) {
	const header = "invariant-go-events-v1\n"
	if !bytes.HasPrefix(data, []byte(header)) {
		return nil, errors.New("Go testing event channel has no complete header")
	}
	data = data[len(header):]
	var events []GoFrameworkEvent
	for len(data) > 0 {
		line, rest, complete := bytes.Cut(data, []byte{'\n'})
		if !complete {
			return events, errors.New("Go testing event channel ended inside a record")
		}
		fields := strings.Split(string(line), "\t")
		if len(fields) != 4 {
			return events, errors.New("invalid Go testing event record")
		}
		for i := range fields {
			value, err := hex.DecodeString(fields[i])
			if err != nil {
				return events, fmt.Errorf("invalid Go testing event field: %w", err)
			}
			fields[i] = string(value)
		}
		e := GoFrameworkEvent{Name: fields[0], Parent: fields[1], Action: fields[2], Text: fields[3]}
		if e.Name == "" || !slices.Contains([]string{"start", "pause", "continue", "output", "pass", "fail", "skip"}, e.Action) {
			return events, errors.New("invalid Go testing event name or action")
		}
		events = append(events, e)
		data = rest
	}
	return events, nil
}
