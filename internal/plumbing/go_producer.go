package plumbing

import (
	"context"
	"errors"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gitdek/invariant/internal/repository"
)

type GoFrameworkEvent struct {
	Name, Parent, Action, Text string
}

// GoAcceptanceStream contains raw observations, not qualified acceptance.
// Only the host supplies Invocation; the producer cannot supply a binding.
type GoAcceptanceStream struct {
	Invocation RepositoryAcceptanceInvocation
	Events     []GoFrameworkEvent
	Output     RepositoryProcessOutput
}

type GoAcceptanceRun struct {
	Invocation RepositoryAcceptanceInvocation
	Process    RepositoryProcess

	mu          sync.Mutex
	invocation  RepositoryAcceptanceInvocation
	environment RepositoryProcessTransport
	prepared    RepositoryPreparedProcess
	err         error
	closed      bool
}

// StartGoAcceptance reuses selection and discovery before preparing the host
// producer. It never starts a new run or test allowance. Any nonnil run must be
// closed, even when startup returns an error alongside an acquired handle.
func StartGoAcceptance(ctx context.Context, part repository.Part, test Test, source, digest string, prepare func(context.Context, RepositoryAcceptanceInvocation) (RepositoryProcessTransport, error)) (run *GoAcceptanceRun, runErr error) {
	adapter := GoRepositoryAcceptanceAdapter{}
	selection, err := adapter.Select(ctx, part, test)
	if err != nil {
		return nil, err
	}
	if !executionDigest.MatchString(digest) || prepare == nil {
		return nil, errors.New("Go producer needs captured execution authority and a host preparer")
	}
	in := repositoryInvocation(&Plan{Sources: map[string]string{test.File: source}, Execution: &Execution{Digest: digest}}, test, selection)
	run = &GoAcceptanceRun{Invocation: cloneGoInvocation(in), invocation: cloneGoInvocation(in)}
	defer func() {
		if runErr == nil {
			runErr = ctx.Err()
		}
		if runErr == nil {
			return
		}
		// A partial Start may have returned a live child with its error. Stop it
		// now, retaining environment and helper ownership for the caller's Close.
		cleanup, done := goProcessCleanupContext(context.Background())
		defer done()
		if run.Process != nil {
			runErr = errors.Join(runErr, run.Process.Close(cleanup))
		}
		if run.prepared != nil {
			runErr = errors.Join(runErr, run.prepared.Close(cleanup))
		}
		run.err = runErr
	}()
	run.environment, err = prepare(ctx, cloneGoInvocation(in))
	if err != nil {
		return run, err
	}
	if run.environment == nil {
		return run, errors.New("Go preparer returned no process transport")
	}
	discovery, err := adapter.Discover(ctx, part, cloneGoInvocation(in), run.environment)
	if err != nil {
		return run, err
	}
	expected := in.Binding
	expected.Package = discovery.Binding.Package
	if discovery.Binding != expected || !nativeText(expected.Package) || discovery.TestName != strings.Split(test.Selector, "/")[0] {
		return run, errors.New("Go discovery changed the bound invocation")
	}
	in.Binding = discovery.Binding
	run.Invocation, run.invocation = cloneGoInvocation(in), cloneGoInvocation(in)
	binary, err := prepareGoTesting(ctx, run.environment, in)
	if err != nil {
		return run, err
	}
	file, err := goAcceptanceFile(part, test.File)
	if err != nil {
		return run, err
	}
	spec := RepositoryProcessSpec{Argv: goTestingArguments(binary, in.Argv), EventEnv: goTestingEventEnv, Directory: path.Dir(file)}
	run.prepared, err = run.environment.Prepare(ctx, spec)
	if err != nil {
		return run, err
	}
	if run.prepared == nil {
		return run, errors.New("Go transport returned no prepared process")
	}
	run.Process, err = run.prepared.Start(ctx)
	if err == nil && run.Process == nil {
		err = errors.New("Go transport returned no process")
	}
	return run, err
}

func cloneGoInvocation(in RepositoryAcceptanceInvocation) RepositoryAcceptanceInvocation {
	in.Argv = slices.Clone(in.Argv)
	return in
}

// Build policy has already reached both compilers verbatim. Go's driver adds
// the test. prefix, the exit-zero panic check and a default ten-minute timeout.
func goTestingArguments(binary string, captured []string) []string {
	argv := []string{binary, "-test.paniconexit0"}
	timeout := false
	for i := 2; i < len(captured); i++ {
		flag, _, equal := strings.Cut(captured[i], "=")
		switch flag {
		case "-tags", "-p", "-mod":
			if !equal {
				i++
			}
		case "-race", "-trimpath":
		case "-run", "-timeout", "-parallel", "-cpu", "-shuffle", "-count", "-short", "-v":
			timeout = timeout || flag == "-timeout"
			argv = append(argv, "-test."+strings.TrimPrefix(captured[i], "-"))
			if !equal && flag != "-short" && flag != "-v" {
				i++
				argv = append(argv, captured[i])
			}
		}
	}
	if !timeout {
		argv = append(argv, "-test.timeout=10m")
	}
	return argv
}

func (r *GoAcceptanceRun) Wait(ctx context.Context) (GoAcceptanceStream, error) {
	stream := GoAcceptanceStream{Invocation: cloneGoInvocation(r.invocation)}
	var err error
	if r.Process == nil {
		err = errors.New("Go runtime was not started")
	} else {
		stream.Output, err = r.Process.Wait(ctx)
		var decodeErr error
		stream.Events, decodeErr = decodeGoTesting(stream.Output.Events)
		err = errors.Join(err, decodeErr)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.err = errors.Join(r.err, err)
	return stream, r.err
}

func goProcessCleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	end := time.Now().Add(SandboxCleanupAllowance)
	if earlier, ok := ctx.Deadline(); ok && earlier.Before(end) && ctx.Err() == nil {
		end = earlier
	}
	return context.WithDeadline(context.WithoutCancel(ctx), end)
}

func (r *GoAcceptanceRun) Close(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return r.err
	}
	r.closed = true
	cleanup, done := goProcessCleanupContext(ctx)
	defer done()
	if r.Process != nil {
		r.err = errors.Join(r.err, r.Process.Close(cleanup))
	}
	if r.prepared != nil {
		r.err = errors.Join(r.err, r.prepared.Close(cleanup))
	}
	if r.environment != nil {
		r.err = errors.Join(r.err, r.environment.Close(cleanup))
	}
	return r.err
}
