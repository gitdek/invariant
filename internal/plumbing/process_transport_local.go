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
	"path"
	"path/filepath"
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

type localGoProcessTransport struct {
	mu                  sync.Mutex
	root, work, helpers string
	goExecutable        string
	env                 map[string]string
	onStart             func(context.Context, []string, int)
	prepared            []*localPreparedProcess
	closed              bool
	err                 error
}

// OpenLocalGoProcessTransport is an explicit host-only local implementation,
// not a dependency installer or an evidence adapter. CacheDir must be private
// host-owned storage. A returned environment owns even an incomplete copy.
func OpenLocalGoProcessTransport(ctx context.Context, o LocalGoProcessOptions) (RepositoryProcessTransport, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(o.GoExecutable) || !filepath.IsAbs(o.CacheDir) || !filepath.IsAbs(o.SourceDir) {
		return nil, errors.New("local Go transport needs absolute host toolchain, source and cache paths")
	}
	if _, err := goAcceptanceFile(o.Part, o.Invocation.Binding.File); err != nil {
		return nil, err
	}
	tool, err := filepath.EvalSymlinks(o.GoExecutable)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(tool)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return nil, errors.Join(errors.New("local Go executable is not a regular executable"), err)
	}
	cache, err := filepath.EvalSymlinks(o.CacheDir)
	if err != nil {
		return nil, err
	}
	source, err := filepath.EvalSymlinks(o.SourceDir)
	if err != nil {
		return nil, err
	}
	if localPathsOverlap(source, cache) {
		return nil, errors.New("local Go cache and candidate must be separate")
	}
	root, err := os.MkdirTemp(cache, "invariant-process-")
	if err != nil {
		return nil, err
	}
	e := &localGoProcessTransport{root: root, work: filepath.Join(root, "work", filepath.FromSlash(o.Part.Directory)), helpers: filepath.Join(root, "helpers"), goExecutable: tool, env: dependencyEnv("go", false), onStart: o.OnStart}
	fail := func(err error) (RepositoryProcessTransport, error) {
		e.err = errors.Join(err, ctx.Err())
		return e, e.err
	}
	for key, dir := range map[string]string{
		"HOME": filepath.Join(root, "home"), "TMPDIR": filepath.Join(root, "tmp"),
		"GOTMPDIR": filepath.Join(root, "tmp"), "GOCACHE": filepath.Join(cache, "gocache"),
		"GOMODCACHE": filepath.Join(root, "gomod"), "GOPATH": filepath.Join(root, "gopath"),
	} {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fail(err)
		}
		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil || resolved != dir {
			return fail(errors.Join(errors.New("local Go storage contains a linked directory"), err))
		}
		e.env[key] = dir
	}
	e.env["PATH"] = filepath.Dir(tool) + ":/usr/bin:/bin"
	e.env["GOTELEMETRY"] = "off"
	for _, dir := range []string{e.work, e.helpers} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fail(err)
		}
	}
	input, err := os.OpenRoot(source)
	if err != nil {
		return fail(err)
	}
	err = copyDependencyTree(ctx, input, filepath.Join(root, "work"), "", nil, false)
	err = errors.Join(err, input.Close())
	if err != nil {
		return fail(err)
	}
	plan := &Plan{Tests: []Test{{File: o.Invocation.Binding.File}}, Sources: map[string]string{o.Invocation.Binding.File: o.Invocation.Source}}
	if err := stageRepositoryAcceptance(ctx, filepath.Join(root, "work"), plan); err != nil {
		return fail(err)
	}
	// This local boundary has no verified dependency artifact input. Refuse
	// required/replaced modules instead of trusting vendor or an ambient cache.
	mod, err := e.ReadFile(ctx, "go.mod")
	if err != nil {
		return fail(err)
	}
	part := o.Part
	modName := path.Join(part.Directory, "go.mod")
	part.Inputs, part.Lockfiles = []string{modName}, nil
	if err := goDependencyLock(part, map[string][]byte{modName: mod}); err != nil {
		return fail(fmt.Errorf("local Go transport requires a dependency-free module: %w", err))
	}
	return e, ctx.Err()
}

func localPathsOverlap(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+string(filepath.Separator)) || strings.HasPrefix(b, a+string(filepath.Separator))
}

func (e *localGoProcessTransport) ReadFile(ctx context.Context, name string) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if e.closed || !repositoryAcceptancePath(name) {
		return nil, errors.New("local Go read needs an open environment and clean part-relative path")
	}
	root, err := os.OpenRoot(e.work)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var before os.FileInfo
	components := strings.Split(name, "/")
	for i := range components {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		before, err = root.Lstat(filepath.FromSlash(strings.Join(components[:i+1], "/")))
		if err != nil {
			return nil, err
		}
		if before.Mode()&os.ModeSymlink != 0 || i < len(components)-1 && !before.IsDir() {
			return nil, errors.New("local Go source has an unsafe ancestor or link")
		}
	}
	if !before.Mode().IsRegular() {
		return nil, errors.New("local Go source is not regular")
	}
	f, err := root.OpenFile(filepath.FromSlash(name), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, after) || !after.Mode().IsRegular() {
		return nil, errors.New("local Go source changed during read")
	}
	if st, ok := after.Sys().(*syscall.Stat_t); ok && st.Nlink != 1 {
		return nil, errors.New("local Go source is hard linked")
	}
	var data bytes.Buffer
	if err := copyDependencyBytes(ctx, &data, f, after.Size()); err != nil {
		return nil, err
	}
	end, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if end.Size() != after.Size() || !end.ModTime().Equal(after.ModTime()) {
		return nil, errors.New("local Go source changed during read")
	}
	return data.Bytes(), ctx.Err()
}

func (e *localGoProcessTransport) Stage(ctx context.Context, name string, data []byte) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if e.closed || !repositoryAcceptancePath(name) {
		return "", errors.New("local Go staging needs an open environment and clean private path")
	}
	if err := writeNativeFile(e.helpers, name, string(data)); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return filepath.Join(e.helpers, filepath.FromSlash(name)), nil
}

func (e *localGoProcessTransport) Prepare(ctx context.Context, spec RepositoryProcessSpec) (RepositoryPreparedProcess, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.prepare(ctx, spec)
}

func (e *localGoProcessTransport) prepare(ctx context.Context, spec RepositoryProcessSpec) (*localPreparedProcess, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if e.closed || len(spec.Argv) == 0 || spec.Argv[0] == "" {
		return nil, errors.New("local Go process needs an open environment and executable")
	}
	spec.Argv, spec.Env = slices.Clone(spec.Argv), maps.Clone(spec.Env)
	for _, arg := range spec.Argv {
		if strings.ContainsRune(arg, 0) {
			return nil, errors.New("local Go process argument contains NUL")
		}
	}
	if spec.Argv[0] == "go" {
		spec.Argv[0] = e.goExecutable
	} else if !filepath.IsAbs(spec.Argv[0]) {
		return nil, errors.New("local Go process executable must be host-resolved")
	}
	for key, value := range spec.Env {
		if !localEnvName(key) || strings.ContainsRune(value, 0) {
			return nil, errors.New("invalid local Go process environment")
		}
		if _, reserved := e.env[key]; reserved || strings.HasPrefix(key, "GO") || strings.HasPrefix(key, "LD_") || strings.HasPrefix(key, "DYLD_") {
			return nil, fmt.Errorf("local Go process environment reserves %s", key)
		}
	}
	if spec.EventEnv != "" && (!localEnvName(spec.EventEnv) || spec.Env[spec.EventEnv] != "" || e.env[spec.EventEnv] != "" || strings.HasPrefix(spec.EventEnv, "GO") || strings.HasPrefix(spec.EventEnv, "LD_") || strings.HasPrefix(spec.EventEnv, "DYLD_")) {
		return nil, errors.New("invalid or conflicting local Go event endpoint variable")
	}
	p := &localPreparedProcess{environment: e, spec: spec}
	e.prepared = append(e.prepared, p)
	return p, nil
}

func localEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, c := range name {
		if c != '_' && !(c >= 'A' && c <= 'Z') && !(c >= 'a' && c <= 'z') && !(i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func (e *localGoProcessTransport) Run(ctx context.Context, argv []string) ([]byte, error) {
	p, err := e.Prepare(ctx, RepositoryProcessSpec{Argv: argv})
	if err != nil {
		return nil, err
	}
	child, err := p.Start(ctx)
	if err != nil {
		if child != nil {
			out := child.Snapshot()
			return append(slices.Clone(out.Stdout), out.Stderr...), err
		}
		return nil, err
	}
	out, err := child.Wait(ctx)
	if err == nil && out.ExitCode != 0 {
		err = fmt.Errorf("local Go command exited with status %d", out.ExitCode)
	}
	return append(slices.Clone(out.Stdout), out.Stderr...), err
}

func (e *localGoProcessTransport) Close(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return e.err
	}
	e.closed = true
	ctx, cancel := context.WithTimeout(ctx, SandboxCleanupAllowance)
	defer cancel()
	// Stop all children first; a blocked drain must not leave another running
	// while consuming the owner's single cleanup allowance.
	for _, p := range e.prepared {
		p.stop()
	}
	for _, p := range e.prepared {
		e.err = errors.Join(e.err, p.Close(ctx))
	}
	e.err = errors.Join(e.err, removeRepositoryStage(ctx, e.root))
	return e.err
}

type localPreparedProcess struct {
	mu          sync.Mutex
	environment *localGoProcessTransport
	spec        RepositoryProcessSpec
	process     *localRepositoryProcess
	closed      bool
	err         error
}

func (p *localPreparedProcess) Start(ctx context.Context) (RepositoryProcess, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.closed || p.process != nil {
		return nil, errors.New("local Go prepared process is closed or already started")
	}
	e := p.environment
	env := maps.Clone(e.env)
	maps.Copy(env, p.spec.Env)
	cmd := exec.Command(p.spec.Argv[0], p.spec.Argv[1:]...)
	cmd.Dir, cmd.SysProcAttr = e.work, &syscall.SysProcAttr{Setpgid: true}
	r := &localRepositoryProcess{cmd: cmd, done: make(chan struct{}), out: RepositoryProcessOutput{ExitCode: -1}}
	p.process = r
	var writers []*os.File
	for i := 0; i < 3; i++ {
		if i == 2 && p.spec.EventEnv == "" {
			break
		}
		reader, writer, err := os.Pipe()
		if err != nil {
			for _, f := range writers {
				_ = f.Close()
			}
			r.launchFailed(err)
			return r, err
		}
		r.readers = append(r.readers, reader)
		writers = append(writers, writer)
	}
	cmd.Stdout, cmd.Stderr = writers[0], writers[1]
	if p.spec.EventEnv != "" {
		cmd.ExtraFiles = []*os.File{writers[2]}
		env[p.spec.EventEnv] = "/dev/fd/3"
	}
	for _, key := range slices.Sorted(maps.Keys(env)) {
		cmd.Env = append(cmd.Env, key+"="+env[key])
	}
	err := cmd.Start()
	for _, f := range writers {
		_ = f.Close()
	}
	if err != nil {
		r.launchFailed(err)
		return r, err
	}
	r.out.Started = true
	go r.collect(ctx)
	if e.onStart != nil {
		e.onStart(ctx, slices.Clone(p.spec.Argv), cmd.Process.Pid)
	}
	return r, ctx.Err()
}

func (p *localPreparedProcess) stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	if p.process != nil {
		p.process.stop()
	}
}

func (p *localPreparedProcess) Close(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	if p.process != nil {
		p.err = errors.Join(p.err, p.process.Close(ctx))
	}
	return p.err
}

type localRepositoryProcess struct {
	mu       sync.Mutex
	stopOnce sync.Once
	cmd      *exec.Cmd
	readers  []*os.File
	done     chan struct{}
	out      RepositoryProcessOutput
	err      error
}

func (p *localRepositoryProcess) launchFailed(err error) {
	for _, f := range p.readers {
		_ = f.Close()
	}
	p.err = err
	close(p.done)
}

func (p *localRepositoryProcess) stop() {
	p.stopOnce.Do(func() {
		if p.cmd.Process == nil {
			return
		}
		// The process group includes descendants that still hold any output pipe.
		if err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			p.record(err)
		}
	})
}

func (p *localRepositoryProcess) record(err error) {
	if err == nil {
		return
	}
	p.mu.Lock()
	p.err = errors.Join(p.err, err)
	p.mu.Unlock()
}

func (p *localRepositoryProcess) collect(ctx context.Context) {
	drained := make(chan error, len(p.readers))
	for i, f := range p.readers {
		go func() {
			defer f.Close()
			buf := make([]byte, 32<<10)
			for {
				n, err := f.Read(buf)
				if n > 0 {
					p.mu.Lock()
					switch i {
					case 0:
						p.out.Stdout = append(p.out.Stdout, buf[:n]...)
					case 1:
						p.out.Stderr = append(p.out.Stderr, buf[:n]...)
					case 2:
						p.out.Events = append(p.out.Events, buf[:n]...)
					}
					p.mu.Unlock()
				}
				if err != nil {
					if err == io.EOF {
						err = nil
					}
					drained <- err
					return
				}
			}
		}()
	}
	go func() {
		select {
		case <-ctx.Done():
			p.record(ctx.Err())
			p.stop()
		case <-p.done:
		}
	}()
	err := p.cmd.Wait() // Reap the direct child independently of the pipe drains.
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		err = nil
	}
	p.mu.Lock()
	p.err = errors.Join(p.err, err)
	p.out.Exited = p.cmd.ProcessState != nil
	if p.out.Exited {
		p.out.ExitCode = p.cmd.ProcessState.ExitCode()
	}
	p.mu.Unlock()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	complete := true
	timeout := deadline.C
	for remaining := len(p.readers); remaining > 0; {
		select {
		case err := <-drained:
			remaining--
			if err != nil {
				complete = false
				p.record(err)
			}
		case <-timeout:
			complete = false
			p.record(errors.New("local Go output drain did not finish after process exit"))
			p.stop()
			for _, f := range p.readers {
				_ = f.Close()
			}
			// Closing readers wakes every outstanding read without awaiting a
			// descendant that retained a writable endpoint.
			timeout = nil
		}
	}
	p.mu.Lock()
	p.out.Drained = complete
	p.mu.Unlock()
	// Release the group now, even if a descendant closed its output handles.
	// Never signal a completed group's numeric PID again during later Close.
	p.stop()
	close(p.done)
}

func (p *localRepositoryProcess) Snapshot() RepositoryProcessOutput {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.out
	out.Stdout, out.Stderr, out.Events = slices.Clone(out.Stdout), slices.Clone(out.Stderr), slices.Clone(out.Events)
	return out
}

func (p *localRepositoryProcess) Wait(ctx context.Context) (RepositoryProcessOutput, error) {
	select {
	case <-p.done:
	case <-ctx.Done():
		p.record(ctx.Err())
		p.stop()
	}
	p.record(ctx.Err())
	p.mu.Lock()
	err := p.err
	p.mu.Unlock()
	return p.Snapshot(), err
}

func (p *localRepositoryProcess) Close(ctx context.Context) error {
	p.stop()
	select {
	case <-p.done:
	case <-ctx.Done():
		p.record(ctx.Err())
		for _, f := range p.readers {
			_ = f.Close()
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}
