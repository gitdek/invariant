package plumbing

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path"
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

//go:embed docker_process_helper.py
var dockerPythonProcessHelper []byte

const dockerProcessProtocol = "invariant-process-v1\n"
const dockerProcessFrameLimit = 64 << 10

var _ RepositoryProcessTransport = (*dockerRepositoryChecks)(nil)
var _ RepositoryAcceptanceEnvironment = (*dockerRepositoryChecks)(nil)

// Each Stage is a root-owned archive of one host file and its directories.
// No candidate path, host mount or image-provided helper supplies these bytes.
func (e *dockerRepositoryChecks) Stage(ctx context.Context, name string, data []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	e.mu.Lock()
	if e.closed || !repositoryAcceptancePath(name) {
		e.mu.Unlock()
		return "", errors.New("helper staging needs an open environment and a clean private path")
	}
	if e.helpers == "" {
		e.helpers = "/invariant/process-" + rand.Text()
		e.staged = map[string]bool{}
	}
	if e.staged[name] {
		e.mu.Unlock()
		return "", errors.New("helper path has already been staged")
	}
	e.staged[name] = true
	file := path.Join(e.helpers, name)
	e.mu.Unlock()
	// Refuse links and existing leaves, including changes made by an earlier
	// workload. Arguments stay positional even when a helper name has spaces.
	guard := `set -eu
file=$1
parent=${file%/*}
check_parent() {
  [ "$1" = / ] && return
  up=${1%/*}
  [ -n "$up" ] || up=/
  check_parent "$up"
  [ ! -L "$1" ] || exit 1
  if [ ! -e "$1" ]; then mkdir -m 700 -- "$1"; fi
  [ -d "$1" ]
}
check_parent "$parent"
[ ! -e "$file" ] && [ ! -L "$file" ]
`
	if out, err := e.Run(ctx, []string{"/bin/sh", "-c", guard, "invariant-helper", file}); err != nil {
		return "", fmt.Errorf("prepare private helper path: %w: %s", err, out)
	}
	var input bytes.Buffer
	archive := tar.NewWriter(&input)
	if err := archive.WriteHeader(&tar.Header{Name: strings.TrimPrefix(file, "/"), Typeflag: tar.TypeReg, Mode: 0600, Size: int64(len(data))}); err != nil {
		return "", err
	}
	if _, err := archive.Write(data); err != nil {
		return "", err
	}
	if err := archive.Close(); err != nil {
		return "", err
	}
	if _, err := dependencyDockerInput(ctx, &input, "cp", "-", e.request.Token+":/"); err != nil {
		return "", err
	}
	return file, ctx.Err()
}

func (e *dockerRepositoryChecks) Prepare(ctx context.Context, spec RepositoryProcessSpec) (RepositoryPreparedProcess, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	python := e.request.Env["PIP_NO_INDEX"] == "1"
	if !python && (e.request.Env["GOTOOLCHAIN"] != "local" || e.request.Env["GOPROXY"] != "off") {
		return nil, errors.New("Docker process transport requires a prepared offline Go or Python environment")
	}
	if len(spec.Argv) == 0 || spec.Argv[0] == "" || strings.HasPrefix(spec.Argv[0], "-") {
		return nil, errors.New("process preparation needs an executable")
	}
	for _, arg := range spec.Argv {
		if strings.ContainsRune(arg, 0) {
			return nil, errors.New("process argument contains NUL")
		}
	}
	policy := dependencyContainerEnv(e.request)
	policy["PWD"] = ""
	for key, value := range spec.Env {
		if key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, 0) {
			return nil, errors.New("invalid process environment")
		}
		if _, protected := policy[key]; protected {
			return nil, fmt.Errorf("process cannot override Docker policy %s", key)
		}
	}
	if strings.ContainsAny(spec.EventEnv, "=\x00") {
		return nil, errors.New("invalid event endpoint variable")
	}
	if _, protected := policy[spec.EventEnv]; protected {
		return nil, errors.New("event endpoint cannot override Docker policy")
	}
	if _, conflict := spec.Env[spec.EventEnv]; conflict {
		return nil, errors.New("event endpoint conflicts with process environment")
	}
	if spec.Directory != "" && spec.Directory != "." && !repositoryAcceptancePath(spec.Directory) {
		return nil, errors.New("process directory must be part-relative")
	}
	directory := path.Join(dependencyWork, e.request.Directory, spec.Directory)
	guard := `set -eu
parent=$1
while [ "$parent" != / ]; do
  [ ! -L "$parent" ] && [ -d "$parent" ] || exit 1
  parent=${parent%/*}
  [ -n "$parent" ] || parent=/
done
`
	if out, err := e.Run(ctx, []string{"/bin/sh", "-c", guard, "invariant-directory", directory}); err != nil {
		return nil, fmt.Errorf("unsafe process directory: %w: %s", err, out)
	}
	// Supply authoritative bytes at a fresh path for every invocation. A prior
	// workload can replace even a root-owned running executable by renaming it.
	e.prepareMu.Lock()
	defer e.prepareMu.Unlock()
	e.mu.Lock()
	closed := e.closed
	e.mu.Unlock()
	if closed {
		return nil, errors.New("repository check environment is closed")
	}
	private := "transport-" + rand.Text()
	var helper string
	if python {
		var err error
		helper, err = e.Stage(ctx, private+"/transport.py", dockerPythonProcessHelper)
		if err != nil {
			return nil, err
		}
	} else {
		source, err := e.Stage(ctx, private+"/main.go", dockerProcessHelper)
		if err != nil {
			return nil, err
		}
		binary, err := e.Stage(ctx, private+"/transport", nil)
		if err != nil {
			return nil, err
		}
		if out, err := e.Run(ctx, []string{"go", "build", "-o", binary, source}); err != nil {
			return nil, fmt.Errorf("compile Docker process transport: %w: %s", err, out)
		}
		helper = binary
	}
	spec.Argv, spec.Env = slices.Clone(spec.Argv), maps.Clone(spec.Env)
	p := &dockerPreparedProcess{owner: e, spec: spec, helper: helper, python: python}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, errors.New("repository check environment is closed")
	}
	e.prepared = append(e.prepared, p)
	return p, nil
}

// The caller supplies an already discovered host invocation. Revalidate its
// literal selection and source in this part; never read candidate policy or
// replace its invocation ID. Shared repository orchestration uses the same run.
func (e *dockerRepositoryChecks) RunAcceptance(ctx context.Context, in RepositoryAcceptanceInvocation) (RepositoryAcceptanceEvidence, []byte, error) {
	in = cloneGoInvocation(in)
	fail := func(err error) (RepositoryAcceptanceEvidence, []byte, error) {
		return RepositoryAcceptanceEvidence{ExitCode: -1, Records: []RepositoryAcceptanceRecord{{Binding: in.Binding, Kind: "setup", Status: "failed", Reason: err.Error()}}}, nil, err
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	b := in.Binding
	e.mu.Lock()
	if e.used == nil {
		e.used = map[string]bool{}
	}
	if e.closed || e.used[b.InvocationID] || !nativeText(b.InvocationID) || b.Framework != "go" && b.Framework != "pytest" {
		e.mu.Unlock()
		return fail(errors.New("Docker Go acceptance is closed, consumed or invalid"))
	}
	e.used[b.InvocationID] = true
	e.mu.Unlock()
	command := slices.Clone(in.Argv)
	if b.Framework == "pytest" {
		if e.request.Env["PIP_NO_INDEX"] != "1" {
			return fail(errors.New("pytest acceptance needs the prepared Python environment"))
		}
		for i := range command {
			if command[i] == b.Selector {
				command[i] = "{test}"
			}
		}
		part := repository.Part{Name: b.Part, Directory: e.request.Directory, Language: "python", Commands: map[string]repository.Command{"test_one": {Argv: command}}}
		return runRepositoryPytestAcceptance(ctx, part, in, e)
	}
	for i := 2; i < len(command); i++ {
		if command[i] == "-run" && i+1 < len(command) {
			i++
			command[i] = "{test}"
		} else if strings.HasPrefix(command[i], "-run=") {
			command[i] = "-run={test}"
		}
	}
	part := repository.Part{Name: b.Part, Directory: e.request.Directory, Language: "go", Commands: map[string]repository.Command{"test_one": {Argv: command}}}
	test := Test{Name: "Go acceptance", Part: b.Part, Framework: b.Framework, File: b.File, Selector: b.Selector}
	adapter := GoRepositoryAcceptanceAdapter{}
	selection, err := adapter.Select(ctx, part, test)
	if err != nil {
		return fail(err)
	}
	if !slices.Equal(selection.Argv, in.Argv) {
		return fail(errors.New("Docker Go acceptance differs from its literal selection"))
	}
	expected := cloneGoInvocation(in)
	expected.Binding.Package = selection.Package
	discovery, err := adapter.Discover(ctx, part, expected, e)
	if err != nil {
		return fail(err)
	}
	if discovery.Binding != in.Binding || discovery.TestName != strings.Split(b.Selector, "/")[0] {
		return fail(errors.New("Docker Go acceptance differs from its discovered invocation"))
	}
	return runRepositoryGoAcceptance(ctx, part, in, e)
}

func (e *dockerRepositoryChecks) cleanupContext(ctx context.Context) context.Context {
	e.cleanupOnce.Do(func() { e.cleanup, e.cleanupDone = goProcessCleanupContext(ctx) })
	return e.cleanup
}

func (e *dockerRepositoryChecks) removeContainer(ctx context.Context) error {
	e.removeOnce.Do(func() {
		e.mu.Lock()
		e.closed = true
		e.mu.Unlock()
		// Leave time within the same reserve to join clients and remove staging.
		if end, ok := ctx.Deadline(); ok {
			var done context.CancelFunc
			reserve := min(2*time.Second, max(0, time.Until(end)/3))
			ctx, done = context.WithDeadline(ctx, end.Add(-reserve))
			defer done()
		}
		e.removeErr = e.runner.Cleanup(ctx, e.request.Token)
	})
	return e.removeErr
}

type dockerCheckBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *dockerCheckBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(data)
}

func (b *dockerCheckBuffer) bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.b.Bytes())
}

// Clients stay owned even when their caller stops waiting. Cancellation removes
// the container first, allowing Docker to report and drain its remote process.
type dockerCheckClient struct {
	owner        *dockerRepositoryChecks
	cmd          *exec.Cmd
	input        *os.File
	done         chan struct{}
	stop         chan error
	err, joinErr error
}

func (e *dockerRepositoryChecks) startClient(ctx context.Context, args []string, stdout, stderr io.Writer, interactive bool) (*dockerCheckClient, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if e.closed {
		return nil, errors.New("repository check environment is closed")
	}
	if err := localProcessReaper(); err != nil {
		return nil, err
	}
	cmd := exec.Command("docker", args...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = time.Second
	var input *os.File
	if interactive {
		r, w, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		defer r.Close()
		cmd.Stdin, input = r, w
	}
	if err := cmd.Start(); err != nil {
		if input != nil {
			_ = input.Close()
		}
		return nil, err
	}
	c := &dockerCheckClient{owner: e, cmd: cmd, input: input, done: make(chan struct{}), stop: make(chan error, 1)}
	e.clients = append(e.clients, c)
	go c.collect(ctx)
	return c, nil
}

func (c *dockerCheckClient) collect(ctx context.Context) {
	if c.input != nil {
		defer c.input.Close()
	}
	wait := make(chan error, 1)
	go func() { wait <- c.cmd.Wait() }()
	var err, exitErr error
	finished := false
	select {
	case exitErr = <-wait:
		finished = true
	case <-ctx.Done():
		err = ctx.Err()
	case err = <-c.stop:
	}
	cleanup, done := context.WithTimeout(context.Background(), 2*time.Second)
	defer done()
	if !finished {
		// Interruption may precede the execution deadline. Start an independent
		// cleanup reserve, or retain the one already established by Close.
		cleanup = c.owner.cleanupContext(context.WithoutCancel(ctx))
		if c.input != nil {
			// EOF is a host control signal, never workload input. Give the
			// helper time to stop its group and deliver the authentic prefix.
			_ = c.input.Close()
			grace := time.NewTimer(3 * time.Second)
			select {
			case exitErr = <-wait:
				finished = true
			case <-grace.C:
			case <-cleanup.Done():
			}
			grace.Stop()
		}
		err = errors.Join(err, c.owner.removeContainer(cleanup))
		if !finished {
			grace := time.NewTimer(100 * time.Millisecond)
			select {
			case exitErr = <-wait:
				finished = true
			case <-grace.C:
			case <-cleanup.Done():
			}
			grace.Stop()
		}
	}
	if err := syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		c.joinErr = err
	}
	if !finished {
		// WaitDelay bounds the copy goroutines after the client is killed.
		exitErr = <-wait
	}
	c.joinErr = errors.Join(c.joinErr, localReapGroup(cleanup, c.cmd.Process.Pid))
	c.err = errors.Join(err, exitErr, c.joinErr)
	close(c.done)
}

func (c *dockerCheckClient) interrupt(err error) {
	select {
	case c.stop <- err:
	default:
	}
}

func (c *dockerCheckClient) wait(ctx context.Context) error {
	select {
	case <-c.done:
	default:
		select {
		case <-c.done:
		case <-ctx.Done():
			c.interrupt(ctx.Err())
			<-c.done
		}
	}
	return c.err
}

func (c *dockerCheckClient) join(ctx context.Context) error {
	select {
	case <-c.done:
	default:
		c.interrupt(errors.New("Docker environment closed before command completion"))
		_ = c.wait(ctx)
	}
	return c.joinErr
}

type dockerPreparedProcess struct {
	mu           sync.Mutex
	owner        *dockerRepositoryChecks
	spec         RepositoryProcessSpec
	helper       string
	python       bool
	process      *dockerRepositoryProcess
	closed, used bool
	err          error
}

func (p *dockerPreparedProcess) Start(ctx context.Context) (RepositoryProcess, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.closed || p.used {
		return p.process, errors.New("Docker process is closed or already started")
	}
	p.used = true
	args := p.owner.execArgs(p.spec.Directory, p.spec.Env)
	args = append([]string{"exec", "--interactive"}, args[1:]...)
	if p.python {
		args = append(args, "python3", "-I", "-B")
	}
	args = append(args, p.helper, p.spec.EventEnv)
	args = append(args, p.spec.Argv...)
	process := &dockerRepositoryProcess{done: make(chan struct{}), invalid: make(chan struct{}), output: RepositoryProcessOutput{ExitCode: -1}}
	client, err := p.owner.startClient(ctx, args, process, &process.diagnostics, true)
	if err != nil {
		p.err = err
		return nil, err
	}
	process.client = client
	p.process = process
	go process.collect()
	return process, nil
}

func (p *dockerPreparedProcess) Close(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.closed = true
		if p.process != nil {
			p.err = errors.Join(p.err, p.process.Close(ctx))
		}
	}
	return p.err
}

type dockerRepositoryProcess struct {
	mu          sync.Mutex
	client      *dockerCheckClient
	done        chan struct{}
	invalid     chan struct{}
	diagnostics dockerCheckBuffer
	output      RepositoryProcessOutput
	pending     []byte
	header      bool
	err         error
}

// Write consumes only the helper's private Docker connection. Workload output
// is payload, never framing or framework evidence. Keep a valid prefix on error.
func (p *dockerRepositoryProcess) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return len(data), nil
	}
	defer func() {
		if p.err != nil && p.invalid != nil {
			close(p.invalid)
		}
	}()
	p.pending = append(p.pending, data...)
	if !p.header {
		n := min(len(p.pending), len(dockerProcessProtocol))
		if string(p.pending[:n]) != dockerProcessProtocol[:n] {
			p.err = errors.New("invalid Docker process protocol")
			return len(data), nil
		}
		if n < len(dockerProcessProtocol) {
			return len(data), nil
		}
		p.header = true
		p.pending = p.pending[n:]
	}
	for len(p.pending) >= 5 {
		kind, size := p.pending[0], binary.BigEndian.Uint32(p.pending[1:5])
		if size > dockerProcessFrameLimit {
			p.err = errors.New("oversized Docker process frame")
			break
		}
		if len(p.pending)-5 < int(size) {
			break
		}
		payload := p.pending[5 : 5+int(size)]
		p.pending = p.pending[5+int(size):]
		if err := p.frame(kind, payload); err != nil {
			p.err = err
			break
		}
	}
	return len(data), nil
}

func (p *dockerRepositoryProcess) frame(kind byte, data []byte) error {
	bad := errors.New("invalid Docker process frame order or status")
	o := &p.output
	if o.Drained {
		return bad
	}
	switch kind {
	case 's':
		if o.Started || len(data) != 0 {
			return bad
		}
		o.Started = true
	case 'o', 'e', 'v':
		if !o.Started {
			return bad
		}
		switch kind {
		case 'o':
			o.Stdout = append(o.Stdout, data...)
		case 'e':
			o.Stderr = append(o.Stderr, data...)
		case 'v':
			o.Events = append(o.Events, data...)
		}
	case 'x':
		code, err := strconv.Atoi(string(data))
		if !o.Started || o.Exited || err != nil || code < -1 || code > 255 || strconv.Itoa(code) != string(data) {
			return bad
		}
		o.Exited, o.ExitCode = true, code
	case 'd':
		if !o.Exited || len(data) != 0 {
			return bad
		}
		o.Drained = true
	case 'f':
		return fmt.Errorf("Docker process helper: %s", data)
	default:
		return bad
	}
	return nil
}

func (p *dockerRepositoryProcess) collect() {
	select {
	case <-p.client.done:
	case <-p.invalid:
		p.client.interrupt(errors.New("invalid Docker process stream"))
		<-p.client.done
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.header || len(p.pending) != 0 || !p.output.Drained {
		p.err = errors.Join(p.err, errors.New("incomplete Docker process transport"))
	}
	if p.client.err != nil {
		p.err = errors.Join(p.err, fmt.Errorf("Docker process connection: %w: %s", p.client.err, p.diagnostics.bytes()))
	} else if diagnostic := p.diagnostics.bytes(); len(diagnostic) != 0 {
		p.err = errors.Join(p.err, fmt.Errorf("Docker process connection diagnostic: %s", diagnostic))
	}
	close(p.done)
}

func (p *dockerRepositoryProcess) Snapshot() RepositoryProcessOutput {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.output
	out.Events, out.Stdout, out.Stderr = slices.Clone(out.Events), slices.Clone(out.Stdout), slices.Clone(out.Stderr)
	return out
}

func (p *dockerRepositoryProcess) Wait(ctx context.Context) (RepositoryProcessOutput, error) {
	_ = p.client.wait(ctx)
	<-p.done
	p.mu.Lock()
	err := p.err
	p.mu.Unlock()
	return p.Snapshot(), err
}

func (p *dockerRepositoryProcess) Close(ctx context.Context) error {
	select {
	case <-p.done:
	default:
		p.client.interrupt(errors.New("Docker process closed before completion"))
	}
	_, err := p.Wait(ctx)
	return err
}
