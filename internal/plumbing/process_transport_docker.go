package plumbing

import (
	"archive/tar"
	"bytes"
	"context"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gitdek/invariant/internal/repository"
)

//go:embed docker_process_helper.go.txt
var dockerProcessHelper []byte

const dockerProcessProtocol = "invariant-process-v1\n"

func (e *dockerRepositoryChecks) Stage(ctx context.Context, name string, data []byte) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stageHelper(ctx, name, data)
}

func (e *dockerRepositoryChecks) stageHelper(ctx context.Context, name string, data []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if e.closed || !repositoryAcceptancePath(name) {
		return "", errors.New("helper staging needs an open environment and a clean private path")
	}
	if e.helperStage == "" {
		var err error
		e.helperStage, err = os.MkdirTemp("", "invariant-process-")
		if err != nil {
			return "", err
		}
	}
	root, err := os.OpenRoot(e.helperStage)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if err := localProcessAncestors(ctx, root, name, true); err != nil {
		return "", err
	}
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return "", err
	}
	err = copyDependencyBytes(ctx, file, bytes.NewReader(data), int64(len(data)))
	if err = errors.Join(err, file.Close()); err != nil {
		return "", err
	}
	// Copy only this new file: copying the whole staging directory would replace
	// binaries the image has already compiled. All archive ownership is root.
	name = path.Join("invariant", filepath.Base(e.helperStage), name)
	var buf bytes.Buffer
	w := tar.NewWriter(&buf)
	components := strings.Split(name, "/")
	for i := 1; i < len(components); i++ {
		if err := w.WriteHeader(&tar.Header{Name: strings.Join(components[:i], "/"), Mode: 0700, Typeflag: tar.TypeDir}); err != nil {
			return "", err
		}
	}
	if err := w.WriteHeader(&tar.Header{Name: name, Mode: 0600, Typeflag: tar.TypeReg, Size: int64(len(data))}); err != nil {
		return "", err
	}
	if _, err := w.Write(data); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	if _, err := dependencyDockerInput(ctx, &buf, "cp", "-", e.request.Token+":/"); err != nil {
		return "", err
	}
	return "/" + name, ctx.Err()
}

func (e *dockerRepositoryChecks) Prepare(ctx context.Context, spec RepositoryProcessSpec) (RepositoryPreparedProcess, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if e.closed || len(spec.Argv) == 0 || spec.Argv[0] == "" || strings.HasPrefix(spec.Argv[0], "-") {
		return nil, errors.New("process preparation needs an open environment and an executable")
	}
	for _, arg := range spec.Argv {
		if strings.ContainsRune(arg, 0) {
			return nil, errors.New("process argument contains NUL")
		}
	}
	env := dependencyContainerEnv(e.request)
	for key, value := range spec.Env {
		if key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, 0) {
			return nil, errors.New("invalid process environment")
		}
		if _, protected := env[key]; protected || key == "PWD" {
			return nil, fmt.Errorf("process cannot override container policy %s", key)
		}
	}
	if strings.ContainsAny(spec.EventEnv, "=\x00") {
		return nil, errors.New("invalid event endpoint variable")
	}
	if _, protected := env[spec.EventEnv]; protected || spec.EventEnv == "PWD" {
		return nil, errors.New("event endpoint cannot override container policy")
	}
	if _, duplicate := spec.Env[spec.EventEnv]; duplicate {
		return nil, errors.New("event endpoint conflicts with process environment")
	}
	if spec.Directory != "" && spec.Directory != "." && !repositoryAcceptancePath(spec.Directory) {
		return nil, errors.New("process directory must be part-relative")
	}
	dir := path.Join(dependencyWork, e.request.Directory, spec.Directory)
	if _, err := e.run(ctx, []string{"/bin/sh", "-c", dockerProcessDirectoryGuard, "invariant-directory", dir}); err != nil {
		return nil, fmt.Errorf("unsafe process directory: %w", err)
	}
	if e.helper == "" {
		source, err := e.stageHelper(ctx, "transport/main.go", dockerProcessHelper)
		if err != nil {
			return nil, err
		}
		binary, err := e.stageHelper(ctx, "transport/helper", nil)
		if err != nil {
			return nil, err
		}
		if out, err := e.run(ctx, []string{"go", "build", "-o", binary, source}); err != nil {
			return nil, fmt.Errorf("compile prepared process helper: %w: %s", err, out)
		}
		e.helper = binary
	}
	spec.Argv, spec.Env = slices.Clone(spec.Argv), maps.Clone(spec.Env)
	p := &dockerPreparedProcess{owner: e, spec: spec}
	e.prepared = append(e.prepared, p)
	return p, nil
}

const dockerProcessDirectoryGuard = `set -eu
dir=$1
while [ "$dir" != / ]; do
  [ ! -L "$dir" ] && [ -d "$dir" ] || exit 1
  dir=${dir%/*}
  [ -n "$dir" ] || dir=/
done
`

// The direct API accepts host-selected authority. OpenChecks owns the part's
// directory; no command or binding is loaded from candidate configuration.
// Rediscovery checks the supplied selected arguments and import path before
// using the same producer and collector as the shared repository lifecycle.
func (e *dockerRepositoryChecks) RunAcceptance(ctx context.Context, in RepositoryAcceptanceInvocation) (RepositoryAcceptanceEvidence, []byte, error) {
	in = cloneGoInvocation(in)
	fail := func(err error) (RepositoryAcceptanceEvidence, []byte, error) {
		return RepositoryAcceptanceEvidence{ExitCode: -1, Records: []RepositoryAcceptanceRecord{{Binding: in.Binding, Kind: "setup", Status: "failed", Reason: err.Error()}}}, nil, err
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	e.mu.Lock()
	if e.closed || e.invocations[in.Binding.InvocationID] || in.Binding.InvocationID == "" {
		e.mu.Unlock()
		return fail(errors.New("Docker acceptance is closed or its invocation was consumed"))
	}
	if e.invocations == nil {
		e.invocations = map[string]bool{}
	}
	e.invocations[in.Binding.InvocationID] = true
	e.mu.Unlock()
	argv := slices.Clone(in.Argv)
	for i := 2; i < len(argv); i++ {
		if strings.HasPrefix(argv[i], "-run=") {
			argv[i] = "-run={test}"
		} else if argv[i] == "-run" && i+1 < len(argv) {
			i++
			argv[i] = "{test}"
		}
	}
	part := repository.Part{Name: in.Binding.Part, Language: "go", Directory: e.request.Directory,
		Commands: map[string]repository.Command{"test_one": {Argv: argv}}}
	test := Test{Name: "Go acceptance", Part: in.Binding.Part, Framework: in.Binding.Framework, File: in.Binding.File, Selector: in.Binding.Selector}
	adapter := GoRepositoryAcceptanceAdapter{}
	selection, err := adapter.Select(ctx, part, test)
	if err != nil {
		return fail(err)
	}
	if !slices.Equal(selection.Argv, in.Argv) {
		return fail(errors.New("Docker acceptance arguments differ from exact Go selection"))
	}
	expected := cloneGoInvocation(in)
	expected.Binding.Package = selection.Package
	discovery, err := adapter.Discover(ctx, part, expected, e)
	if err != nil {
		return fail(err)
	}
	if discovery.Binding != in.Binding {
		return fail(errors.New("Docker acceptance differs from its discovered binding"))
	}
	return runRepositoryGoAcceptance(ctx, part, in, e)
}

// Ordinary commands keep their existing argv and diagnostics contract. On
// cancellation, remove the container before terminating the client, so owned
// remote processes finish and the client can drain and join its children.
func (e *dockerRepositoryChecks) command(ctx context.Context, args []string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := localProcessReaper(); err != nil {
		return nil, err
	}
	cmd := exec.Command("docker", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = time.Second
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	var err error
	select {
	case err = <-wait:
	case <-ctx.Done():
		cleanup, done := context.WithTimeout(context.Background(), SandboxCleanupAllowance)
		err = errors.Join(ctx.Err(), e.removeContainer(cleanup), dockerJoinClient(cleanup, cmd, wait))
		done()
	}
	cleanup, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	err = errors.Join(err, localReapGroup(cleanup, cmd.Process.Pid))
	if err != nil {
		return out.Bytes(), fmt.Errorf("docker exec: %w: %s", err, strings.TrimSpace(out.String()))
	}
	return out.Bytes(), ctx.Err()
}

type dockerPreparedProcess struct {
	mu           sync.Mutex
	owner        *dockerRepositoryChecks
	spec         RepositoryProcessSpec
	process      *dockerRepositoryProcess
	closed, used bool
	err          error
}

func (p *dockerPreparedProcess) Start(ctx context.Context) (RepositoryProcess, error) {
	p.owner.mu.Lock()
	defer p.owner.mu.Unlock()
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.owner.closed || p.closed || p.used {
		return p.process, errors.New("prepared Docker process is closed or already started")
	}
	p.used = true
	if err := localProcessReaper(); err != nil {
		return nil, err
	}
	child := &dockerRepositoryProcess{owner: p.owner, done: make(chan struct{}), stop: make(chan error, 1), output: RepositoryProcessOutput{ExitCode: -1}}
	p.process = child
	args := p.owner.execArgs(p.spec.Directory, p.spec.Env, true)
	args = append(args, p.owner.helper, p.spec.EventEnv)
	args = append(args, p.spec.Argv...)
	cmd := exec.Command("docker", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = time.Second
	var files []*os.File
	fail := func(err error) (RepositoryProcess, error) {
		for _, file := range files {
			_ = file.Close()
		}
		child.err = err
		close(child.done)
		return child, err
	}
	input, control, err := os.Pipe()
	if err != nil {
		return fail(err)
	}
	files = append(files, input, control)
	output, wire, err := os.Pipe()
	if err != nil {
		return fail(err)
	}
	files = append(files, output, wire)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = input, wire, dockerProcessDiagnostic{child}
	if err := cmd.Start(); err != nil {
		return fail(err)
	}
	_ = input.Close()
	_ = wire.Close()
	go child.collect(ctx, cmd, control, output)
	return child, nil
}

func (p *dockerPreparedProcess) Close(ctx context.Context) error {
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

type dockerRepositoryProcess struct {
	mu     sync.Mutex
	owner  *dockerRepositoryChecks
	output RepositoryProcessOutput
	err    error
	done   chan struct{}
	stop   chan error
}

type dockerProcessDiagnostic struct{ process *dockerRepositoryProcess }

func (d dockerProcessDiagnostic) Write(data []byte) (int, error) {
	d.process.mu.Lock()
	defer d.process.mu.Unlock()
	d.process.output.Stderr = append(d.process.output.Stderr, data...)
	return len(data), nil
}

func (p *dockerRepositoryProcess) Snapshot() RepositoryProcessOutput {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.output
	out.Events, out.Stdout, out.Stderr = slices.Clone(out.Events), slices.Clone(out.Stdout), slices.Clone(out.Stderr)
	return out
}

func (p *dockerRepositoryProcess) interrupt(err error) {
	select {
	case p.stop <- err:
	default:
	}
}

func (p *dockerRepositoryProcess) Wait(ctx context.Context) (RepositoryProcessOutput, error) {
	select {
	case <-p.done:
	default:
		select {
		case <-p.done:
		case <-ctx.Done():
			p.interrupt(ctx.Err())
			<-p.done
		}
	}
	p.mu.Lock()
	err := p.err
	p.mu.Unlock()
	return p.Snapshot(), err
}

func (p *dockerRepositoryProcess) Close(ctx context.Context) error {
	select {
	case <-p.done:
	default:
		p.interrupt(errors.New("Docker process closed before completion"))
	}
	_, err := p.Wait(ctx)
	return err
}

func (p *dockerRepositoryProcess) collect(ctx context.Context, cmd *exec.Cmd, control, wire *os.File) {
	defer close(p.done)
	defer control.Close()
	defer wire.Close()
	decoded := make(chan error, 1)
	go func() { decoded <- p.decode(wire) }()
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	var err error
	waited := false
	var decode <-chan error = decoded
waiting:
	for !waited {
		select {
		case waitErr := <-wait:
			err = errors.Join(err, waitErr)
			waited = true
		case decodeErr := <-decode:
			decode = nil
			err = errors.Join(err, decodeErr)
			if decodeErr != nil {
				_ = wire.Close()
				break waiting
			}
		case <-ctx.Done():
			err = errors.Join(err, ctx.Err())
			break waiting
		case stopErr := <-p.stop:
			err = errors.Join(err, stopErr)
			break waiting
		}
	}
	cleanup, done := context.WithTimeout(context.Background(), SandboxCleanupAllowance)
	defer done()
	if !waited {
		_ = control.Close()
		// The helper has a two-second descendant/drain reserve. Allow the
		// connection to deliver that evidence before removing its container.
		timer := time.NewTimer(3 * time.Second)
		select {
		case waitErr := <-wait:
			err = errors.Join(err, waitErr)
		case <-timer.C:
			err = errors.Join(err, p.owner.removeContainer(cleanup), dockerJoinClient(cleanup, cmd, wait))
		}
		timer.Stop()
	}
	if decode != nil {
		select {
		case decodeErr := <-decode:
			err = errors.Join(err, decodeErr)
		case <-cleanup.Done():
			_ = wire.Close()
			err = errors.Join(err, cleanup.Err(), <-decode)
		}
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	err = errors.Join(err, localReapGroup(cleanup, cmd.Process.Pid))
	p.mu.Lock()
	p.err = err
	p.mu.Unlock()
}

func dockerJoinClient(ctx context.Context, cmd *exec.Cmd, wait <-chan error) error {
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case err := <-wait:
		return err
	case <-timer.C:
	case <-ctx.Done():
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	// WaitDelay bounds client pipe copies after Kill; always join the client.
	return errors.Join(errors.New("Docker client required termination"), <-wait, ctx.Err())
}

func (p *dockerRepositoryProcess) decode(r io.Reader) error {
	hello := make([]byte, len(dockerProcessProtocol))
	if _, err := io.ReadFull(r, hello); err != nil {
		return fmt.Errorf("Docker process protocol header: %w", err)
	}
	if string(hello) != dockerProcessProtocol {
		return errors.New("invalid Docker process protocol header")
	}
	finished := false
	var transportErr error
	for {
		var header [5]byte
		_, err := io.ReadFull(r, header[:])
		if err == io.EOF && finished {
			return transportErr
		}
		if err != nil {
			return errors.Join(transportErr, fmt.Errorf("unfinished Docker process transport: %w", err))
		}
		if finished {
			return errors.New("Docker process transport continued after completion")
		}
		size := binary.BigEndian.Uint32(header[1:])
		if size > 64<<10 {
			return errors.New("oversized Docker process frame")
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(r, data); err != nil {
			return errors.Join(transportErr, fmt.Errorf("truncated Docker process frame: %w", err))
		}
		p.mu.Lock()
		out := &p.output
		valid := true
		switch header[0] {
		case 'S':
			valid = size == 0 && !out.Started
			if valid {
				out.Started = true
			}
		case 'O', 'E', 'V':
			valid = out.Started
			if valid {
				switch header[0] {
				case 'O':
					out.Stdout = append(out.Stdout, data...)
				case 'E':
					out.Stderr = append(out.Stderr, data...)
				case 'V':
					out.Events = append(out.Events, data...)
				}
			}
		case 'X':
			code, err := strconv.Atoi(string(data))
			valid = out.Started && !out.Exited && err == nil && code >= -1 && code <= 255 && strconv.Itoa(code) == string(data)
			if valid {
				out.Exited, out.ExitCode = true, code
			}
		case 'F':
			valid = size > 0 && transportErr == nil
			if valid {
				transportErr = fmt.Errorf("Docker process helper: %s", data)
				finished = true
			}
		case 'D':
			valid = size == 0 && out.Started && out.Exited
			if valid {
				out.Drained = true
				finished = true
			}
		default:
			valid = false
		}
		p.mu.Unlock()
		if !valid {
			return errors.New("invalid Docker process frame or ordering")
		}
	}
}
