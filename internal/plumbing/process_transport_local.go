package plumbing

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gitdek/invariant/internal/repository"
)

type LocalGoProcessOptions struct {
	SourceDir, GoExecutable, CacheDir string
	Part                              repository.Part
	Invocation                        RepositoryAcceptanceInvocation
	OnStart                           func(context.Context, []string, int)
}

type localGoTransport struct {
	mu                  sync.Mutex
	root, work, helpers string
	goExecutable        string
	env                 map[string]string
	onStart             func(context.Context, []string, int)
	prepared            []*localPreparedProcess
	part                repository.Part
	invocation          RepositoryAcceptanceInvocation
	acceptanceUsed      bool
	closed              bool
	err                 error
}

// OpenLocalGoProcessTransport admits an empty external dependency graph. It
// cannot stand in for a verified prepared dependency image. The cache and the
// resolved executable are host-owned inputs, never candidate configuration.
func OpenLocalGoProcessTransport(ctx context.Context, options LocalGoProcessOptions) (RepositoryProcessTransport, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if options.Part.Language != "go" || options.Part.Name != options.Invocation.Binding.Part || options.Invocation.Binding.Framework != "go" {
		return nil, errors.New("local Go transport needs its captured Go owner")
	}
	if _, err := goAcceptanceFile(options.Part, options.Invocation.Binding.File); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(options.GoExecutable) || !filepath.IsAbs(options.CacheDir) || !filepath.IsAbs(options.SourceDir) {
		return nil, errors.New("local Go transport needs absolute host toolchain, source and cache paths")
	}
	info, err := os.Lstat(options.GoExecutable)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return nil, fmt.Errorf("local Go toolchain must be a resolved executable regular file: %v", err)
	}
	if err := localProcessReaper(); err != nil {
		return nil, err
	}
	cache, err := filepath.EvalSymlinks(options.CacheDir)
	if err != nil {
		return nil, err
	}
	source, err := filepath.EvalSymlinks(options.SourceDir)
	if err != nil {
		return nil, err
	}
	if rel, err := filepath.Rel(source, cache); err != nil || rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, errors.New("local Go cache must be outside the candidate")
	}
	root, err := os.MkdirTemp(cache, "invariant-go-")
	if err != nil {
		return nil, err
	}
	e := &localGoTransport{root: root, goExecutable: options.GoExecutable, onStart: options.OnStart}
	e.invocation = cloneGoInvocation(options.Invocation)
	e.part = options.Part
	e.part.Inputs, e.part.Lockfiles = slices.Clone(e.part.Inputs), slices.Clone(e.part.Lockfiles)
	e.part.Commands = maps.Clone(e.part.Commands)
	for name, command := range e.part.Commands {
		command.Argv = slices.Clone(command.Argv)
		e.part.Commands[name] = command
	}
	fail := func(err error) (RepositoryProcessTransport, error) {
		e.err = err
		return e, err // The caller retains even partially acquired storage.
	}
	work := filepath.Join(root, "work")
	e.work = filepath.Join(work, filepath.FromSlash(options.Part.Directory))
	e.helpers = filepath.Join(root, "helpers")
	e.env = dependencyEnv("go", false)
	e.env["PWD"] = e.work
	e.env["PATH"] = filepath.Dir(options.GoExecutable) + ":/usr/bin:/bin"
	for key, name := range map[string]string{"HOME": "home", "GOMODCACHE": "gomod", "GOPATH": "gopath", "TMPDIR": "tmp", "GOTMPDIR": "tmp"} {
		e.env[key] = filepath.Join(root, name)
	}
	e.env["GOCACHE"] = filepath.Join(cache, "build")
	for _, dir := range append([]string{work, e.helpers}, e.env["HOME"], e.env["GOMODCACHE"], e.env["GOPATH"], e.env["TMPDIR"], e.env["GOCACHE"]) {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fail(err)
		}
	}
	from, err := os.OpenRoot(source)
	if err != nil {
		return fail(err)
	}
	err = copyDependencyTree(ctx, from, work, "", nil, false)
	err = errors.Join(err, from.Close())
	if err != nil {
		return fail(err)
	}
	in := options.Invocation
	plan := &Plan{Tests: []Test{{File: in.Binding.File}}, Sources: map[string]string{in.Binding.File: in.Source}}
	if err := stageRepositoryAcceptance(ctx, work, plan); err != nil {
		return fail(err)
	}
	mod, err := e.ReadFile(ctx, "go.mod")
	if err != nil {
		return fail(err)
	}
	// With no declared sum, the existing dependency validator refuses every
	// nonempty require graph as well as replacements, exclusions and workspaces.
	if err := goDependencyLock(repository.Part{Directory: ".", Inputs: []string{"go.mod"}}, map[string][]byte{"go.mod": mod}); err != nil {
		return fail(fmt.Errorf("local transport has no verified external dependencies: %w", err))
	}
	if _, err := os.Lstat(filepath.Join(e.work, "vendor")); !errors.Is(err, os.ErrNotExist) {
		return fail(errors.New("local transport cannot admit unverified vendored dependencies"))
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	// Go's telemetry sidecar creates its own session. Disable it in this
	// private home before any loading or compiler command can start one.
	// The Go command itself special-cases telemetry off before starting the
	// sidecar, so this setup process stays in its owned group too.
	if out, err := e.Run(ctx, []string{"go", "telemetry", "off"}); err != nil {
		return fail(fmt.Errorf("disable private Go telemetry: %w: %s", err, out))
	}
	return e, nil
}

func (e *localGoTransport) ReadFile(ctx context.Context, name string) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, errors.New("local Go environment is closed")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !repositoryAcceptancePath(name) {
		return nil, errors.New("source read needs a clean part-relative path")
	}
	root, err := os.OpenRoot(e.work)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := localProcessAncestors(ctx, root, name, false); err != nil {
		return nil, err
	}
	before, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, errors.New("source is not a regular file")
	}
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, info) || !info.Mode().IsRegular() || info.Sys().(*syscall.Stat_t).Nlink != 1 {
		return nil, errors.New("source changed or is linked")
	}
	var b bytes.Buffer
	if err := copyDependencyBytes(ctx, &b, f, info.Size()); err != nil {
		return nil, err
	}
	return b.Bytes(), ctx.Err()
}

func localProcessAncestors(ctx context.Context, root *os.Root, name string, create bool) error {
	parts := strings.Split(name, "/")
	for i := 1; i < len(parts); i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		at := strings.Join(parts[:i], "/")
		info, err := root.Lstat(at)
		if create && errors.Is(err, os.ErrNotExist) {
			if err = root.Mkdir(at, 0700); err != nil {
				return err
			}
			info, err = root.Lstat(at)
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("unsafe process storage ancestor %q", at)
		}
	}
	return ctx.Err()
}

func (e *localGoTransport) Stage(ctx context.Context, name string, data []byte) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if e.closed || !repositoryAcceptancePath(name) {
		return "", errors.New("helper staging needs an open environment and a clean private path")
	}
	root, err := os.OpenRoot(e.helpers)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if err := localProcessAncestors(ctx, root, name, true); err != nil {
		return "", err
	}
	f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return "", err
	}
	err = copyDependencyBytes(ctx, f, bytes.NewReader(data), int64(len(data)))
	if err := errors.Join(err, f.Close(), ctx.Err()); err != nil {
		return "", err
	}
	return filepath.Join(e.helpers, filepath.FromSlash(name)), nil
}

func (e *localGoTransport) Prepare(ctx context.Context, spec RepositoryProcessSpec) (RepositoryPreparedProcess, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if e.closed || len(spec.Argv) == 0 || spec.Argv[0] == "" {
		return nil, errors.New("process preparation needs an open environment and an executable")
	}
	for _, arg := range spec.Argv {
		if strings.ContainsRune(arg, 0) {
			return nil, errors.New("process argument contains NUL")
		}
	}
	for key, value := range spec.Env {
		if key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, 0) {
			return nil, errors.New("invalid process environment")
		}
		if _, protected := e.env[key]; protected {
			return nil, fmt.Errorf("process cannot override local policy %s", key)
		}
	}
	if strings.ContainsAny(spec.EventEnv, "=\x00") {
		return nil, errors.New("invalid event endpoint variable")
	}
	if _, protected := e.env[spec.EventEnv]; protected {
		return nil, errors.New("event endpoint cannot override local policy")
	}
	dir := e.work
	if spec.Directory != "" && spec.Directory != "." {
		if !repositoryAcceptancePath(spec.Directory) {
			return nil, errors.New("process directory must be part-relative")
		}
		root, err := os.OpenRoot(e.work)
		if err != nil {
			return nil, err
		}
		err = localProcessAncestors(ctx, root, spec.Directory+"/unused", false)
		root.Close()
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(dir, filepath.FromSlash(spec.Directory))
	}
	spec.Argv, spec.Env = slices.Clone(spec.Argv), maps.Clone(spec.Env)
	executable := spec.Argv[0]
	if executable == "go" {
		executable = e.goExecutable
	} else if !filepath.IsAbs(executable) {
		return nil, errors.New("local process executable must be host-resolved")
	}
	p := &localPreparedProcess{owner: e, spec: spec, executable: executable, dir: dir}
	e.prepared = append(e.prepared, p)
	return p, nil
}

func (e *localGoTransport) Run(ctx context.Context, argv []string) ([]byte, error) {
	p, err := e.Prepare(ctx, RepositoryProcessSpec{Argv: argv})
	if err != nil {
		return nil, err
	}
	child, err := p.Start(ctx)
	if child == nil {
		return nil, err
	}
	out, waitErr := child.Wait(ctx)
	err = errors.Join(err, waitErr)
	if out.Exited && out.ExitCode != 0 {
		err = errors.Join(err, fmt.Errorf("exit status %d", out.ExitCode))
	}
	return append(out.Stdout, out.Stderr...), err
}

// RunAcceptance consumes the retained invocation once. Rediscovery uses the
// original selection and captured part, never caller-supplied package authority.
func (e *localGoTransport) RunAcceptance(ctx context.Context, in RepositoryAcceptanceInvocation) (RepositoryAcceptanceEvidence, []byte, error) {
	in = cloneGoInvocation(in)
	fail := func(err error) (RepositoryAcceptanceEvidence, []byte, error) {
		return RepositoryAcceptanceEvidence{ExitCode: -1, Records: []RepositoryAcceptanceRecord{{Binding: in.Binding, Kind: "setup", Status: "failed", Reason: err.Error()}}}, nil, err
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	e.mu.Lock()
	expected := cloneGoInvocation(e.invocation)
	// The import path is established below by real discovery. Every other field
	// must already agree with the original host selection before any work starts.
	expected.Binding.Package = in.Binding.Package
	if e.closed || e.err != nil || e.acceptanceUsed || !sameGoInvocation(expected, in) {
		err := errors.Join(e.err, errors.New("local Go acceptance is closed, consumed or differs from its original invocation"))
		e.mu.Unlock()
		return fail(err)
	}
	e.acceptanceUsed = true
	e.mu.Unlock()
	b := expected.Binding
	test := Test{Name: "Go acceptance", Part: b.Part, Framework: b.Framework, File: b.File, Selector: b.Selector}
	adapter := GoRepositoryAcceptanceAdapter{}
	selection, err := adapter.Select(ctx, e.part, test)
	if err != nil {
		return fail(err)
	}
	expected.Binding.Package = selection.Package
	discovery, err := adapter.Discover(ctx, e.part, expected, e)
	if err != nil {
		return fail(err)
	}
	expected.Binding.Package = discovery.Binding.Package
	if discovery.Binding != expected.Binding || discovery.TestName != strings.Split(b.Selector, "/")[0] || !sameGoInvocation(expected, in) {
		return fail(errors.New("local Go acceptance differs from the discovered invocation"))
	}
	run := &GoAcceptanceRun{Invocation: cloneGoInvocation(expected), invocation: cloneGoInvocation(expected), environment: e}
	startErr := run.start(ctx, e.part)
	var stream GoAcceptanceStream
	var waitErr error
	if run.Process != nil {
		stream, waitErr = run.Wait(ctx)
	}
	// The environment owns storage until its explicit Close. Release the runtime
	// handle now, including any partial startup, within the cleanup reserve.
	cleanup, done := goProcessCleanupContext(ctx)
	defer done()
	var closeErr error
	if run.prepared != nil {
		closeErr = run.prepared.Close(cleanup)
	}
	err = errors.Join(startErr, waitErr, closeErr, ctx.Err())
	if run.Process == nil {
		if err == nil {
			err = errors.New("local Go acceptance did not start a process")
		}
		return fail(err)
	}
	evidence, collectErr := CollectGoAcceptance(expected, stream, err)
	diagnostics := append(slices.Clone(stream.Output.Stdout), stream.Output.Stderr...)
	return evidence, diagnostics, collectErr
}

func (e *localGoTransport) Close(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return e.err
	}
	e.closed = true
	cleanup, done := goProcessCleanupContext(ctx)
	defer done()
	for _, p := range e.prepared {
		e.err = errors.Join(e.err, p.Close(cleanup))
	}
	e.err = errors.Join(e.err, removeRepositoryStage(cleanup, e.root))
	return e.err
}

type localPreparedProcess struct {
	mu              sync.Mutex
	owner           *localGoTransport
	spec            RepositoryProcessSpec
	executable, dir string
	process         *localRepositoryProcess
	closed, used    bool
	err             error
}

func (p *localPreparedProcess) Start(ctx context.Context) (RepositoryProcess, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.closed || p.used {
		return p.process, errors.New("prepared process is closed or already started")
	}
	p.used = true
	child := &localRepositoryProcess{done: make(chan struct{}), stop: make(chan error, 1), output: RepositoryProcessOutput{ExitCode: -1}}
	p.process = child
	cmd := exec.Command(p.executable, p.spec.Argv[1:]...)
	cmd.Dir, cmd.SysProcAttr = p.dir, &syscall.SysProcAttr{Setpgid: true}
	env := maps.Clone(p.owner.env)
	maps.Copy(env, p.spec.Env)
	env["PWD"] = p.dir
	var reads, writes []*os.File
	fail := func(err error) (RepositoryProcess, error) {
		for _, f := range append(reads, writes...) {
			_ = f.Close()
		}
		child.err = err
		close(child.done)
		return child, err
	}
	count := 2
	if p.spec.EventEnv != "" {
		count++
	}
	for i := 0; i < count; i++ {
		r, w, err := os.Pipe()
		if err != nil {
			return fail(err)
		}
		reads, writes = append(reads, r), append(writes, w)
	}
	cmd.Stdout, cmd.Stderr = writes[0], writes[1]
	if count == 3 {
		cmd.ExtraFiles = []*os.File{writes[2]}
		env[p.spec.EventEnv] = "/dev/fd/3"
	}
	for _, key := range slices.Sorted(maps.Keys(env)) {
		cmd.Env = append(cmd.Env, key+"="+env[key])
	}
	if err := cmd.Start(); err != nil {
		return fail(err)
	}
	child.output.Started = true
	for _, f := range writes {
		_ = f.Close()
	}
	if p.owner.onStart != nil {
		p.owner.onStart(ctx, slices.Clone(p.spec.Argv), cmd.Process.Pid)
	}
	go child.collect(ctx, cmd, reads)
	if err := ctx.Err(); err != nil {
		_, waitErr := child.Wait(ctx)
		return child, errors.Join(err, waitErr)
	}
	return child, nil
}

func (p *localPreparedProcess) Close(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.closed = true
		if p.process != nil {
			p.err = p.process.Close(ctx)
		}
	}
	return p.err
}

type localRepositoryProcess struct {
	mu     sync.Mutex
	output RepositoryProcessOutput
	err    error
	done   chan struct{}
	stop   chan error
}

func (p *localRepositoryProcess) Snapshot() RepositoryProcessOutput {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.output
	out.Events, out.Stdout, out.Stderr = slices.Clone(out.Events), slices.Clone(out.Stdout), slices.Clone(out.Stderr)
	return out
}

func (p *localRepositoryProcess) interrupt(err error) {
	select {
	case p.stop <- err:
	default:
	}
}

func (p *localRepositoryProcess) Wait(ctx context.Context) (RepositoryProcessOutput, error) {
	select {
	case <-p.done:
	default:
		select {
		case <-p.done:
		case <-ctx.Done():
			p.interrupt(ctx.Err())
			<-p.done // collect has a bounded, uncanceled termination reserve.
		}
	}
	p.mu.Lock()
	err := p.err
	p.mu.Unlock()
	return p.Snapshot(), err
}

func (p *localRepositoryProcess) Close(ctx context.Context) error {
	select {
	case <-p.done:
	default:
		p.interrupt(errors.New("process closed before completion"))
	}
	_, err := p.Wait(ctx)
	return err
}

func (p *localRepositoryProcess) collect(ctx context.Context, cmd *exec.Cmd, reads []*os.File) {
	drained := make(chan error, len(reads))
	for i, f := range reads {
		go func() {
			defer f.Close()
			buf := make([]byte, 32<<10)
			for {
				n, err := f.Read(buf)
				p.mu.Lock()
				switch i {
				case 0:
					p.output.Stdout = append(p.output.Stdout, buf[:n]...)
				case 1:
					p.output.Stderr = append(p.output.Stderr, buf[:n]...)
				case 2:
					p.output.Events = append(p.output.Events, buf[:n]...)
				}
				p.mu.Unlock()
				if err != nil {
					if errors.Is(err, io.EOF) {
						err = nil
					}
					drained <- err
					return
				}
			}
		}()
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	var err, exitErr error
	exited := false
	select {
	case exitErr = <-wait:
		exited = true
	case <-ctx.Done():
		err = ctx.Err()
	case err = <-p.stop:
	}
	// Even a normal exit with EOF on every pipe can leave silent compilers or
	// grandchildren. The group, not the pipe lifetime, owns termination.
	killErr := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if killErr != nil && !errors.Is(killErr, syscall.ESRCH) {
		err = errors.Join(err, killErr)
	}
	cleanup, done := context.WithTimeout(context.Background(), 2*time.Second)
	defer done()
	if !exited {
		select {
		case exitErr = <-wait:
			exited = true
		case <-cleanup.Done():
			err = errors.Join(err, errors.New("process did not exit after termination"), cleanup.Err())
		}
	}
	if exited {
		var ordinary *exec.ExitError
		if exitErr != nil && !errors.As(exitErr, &ordinary) {
			err = errors.Join(err, exitErr)
		}
		if cmd.ProcessState == nil {
			err = errors.Join(err, errors.New("process wait returned no exit state"))
		} else {
			p.mu.Lock()
			p.output.Exited, p.output.ExitCode = true, cmd.ProcessState.ExitCode()
			p.mu.Unlock()
		}
	}
	err = errors.Join(err, localReapGroup(cleanup, cmd.Process.Pid))
	complete := true
	remaining := len(reads)
	for remaining > 0 {
		select {
		case drainErr := <-drained:
			remaining--
			err = errors.Join(err, drainErr)
			complete = complete && drainErr == nil
		case <-cleanup.Done():
			complete = false
			err = errors.Join(err, errors.New("process channels did not drain"), cleanup.Err())
			for _, f := range reads {
				_ = f.Close()
			}
			// Closing a local pipe interrupts Read. Join every reader before
			// publishing the final snapshot, including on a failed drain.
			for remaining > 0 {
				err = errors.Join(err, <-drained)
				remaining--
			}
		}
	}
	p.mu.Lock()
	p.output.Drained, p.err = complete, err
	p.mu.Unlock()
	close(p.done)
}

// Linux orphans must be adopted before their group can be reaped. Only owned
// groups are waited on; never wait(-1), which could steal an unrelated child.
// Darwin's init reaps orphans, and group disappearance is still awaited there.
var localReaperOnce sync.Once
var localReaperErr error

func localProcessReaper() error {
	localReaperOnce.Do(func() {
		if runtime.GOOS == "darwin" {
			return
		}
		if runtime.GOOS != "linux" {
			localReaperErr = errors.New("local process groups require Linux or Darwin")
			return
		}
		// prctl(PR_SET_CHILD_SUBREAPER, 1). Keep this small platform boundary in
		// the local transport; syscall has no cross-platform Prctl wrapper.
		var number uintptr
		switch runtime.GOARCH {
		case "amd64":
			number = 157
		case "arm64", "riscv64":
			number = 167
		default:
			localReaperErr = errors.New("unsupported local Linux process reaper architecture")
			return
		}
		_, _, errno := syscall.Syscall6(number, 36, 1, 0, 0, 0, 0)
		if errno != 0 {
			localReaperErr = fmt.Errorf("enable owned descendant reaping: %w", errno)
		}
	})
	return localReaperErr
}

func localReapGroup(ctx context.Context, group int) error {
	for {
		var status syscall.WaitStatus
		for {
			pid, err := syscall.Wait4(-group, &status, syscall.WNOHANG, nil)
			if err != nil && !errors.Is(err, syscall.ECHILD) && !errors.Is(err, syscall.EINTR) {
				return err
			}
			if pid <= 0 {
				break
			}
		}
		if err := syscall.Kill(-group, 0); errors.Is(err, syscall.ESRCH) {
			return nil
		} else if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("process group %d was not fully reaped: %w", group, err)
		}
		// A descendant may have forked while the first group signal arrived.
		_ = syscall.Kill(-group, syscall.SIGKILL)
		time.Sleep(time.Millisecond)
	}
}
