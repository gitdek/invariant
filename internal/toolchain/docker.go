package toolchain

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// started and runs make container names unique: this process's ID and start
// time tell it from every other, and the counter tells its runs apart.
var (
	started = time.Now().UnixNano()
	runs    atomic.Int64
)

// DockerCommand adds acquisition retries and cancellation cleanup to a Docker
// client command. Its embedded command holds the caller's execution settings.
type DockerCommand struct {
	*exec.Cmd
	ctx         context.Context
	name        string
	cancelled   atomic.Bool
	pull        bool
	started     bool
	waited      bool
	nextPull    func() *exec.Cmd
	diagnostic  dockerDiagnostic
	pipeReaders []*os.File
	pipeWriters []*os.File
	pullDone    chan error
}

// Docker returns a command for the docker client. A run is named, and when
// its context ends the container is removed before the client is killed and
// again after it exits: a pending create can land after the first removal.
// Only acquiring a pinned image is retried, never a build or a workload.
func Docker(ctx context.Context, args ...string) *DockerCommand {
	// Explicit empty values override client config.json proxy defaults without
	// changing the user's configuration, credentials or daemon selection.
	if len(args) > 0 && (args[0] == "run" || args[0] == "build") {
		flag := "--env"
		if args[0] == "build" {
			flag = "--build-arg"
		}
		clean := []string{args[0]}
		for _, key := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "FTP_PROXY", "ftp_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy"} {
			clean = append(clean, flag, key+"=")
		}
		args = append(clean, args[1:]...)
	}
	c := &DockerCommand{ctx: ctx}
	if len(args) > 0 && args[0] == "run" {
		c.name = fmt.Sprintf("invariant-%d-%d-%d", os.Getpid(), started, runs.Add(1))
		args = append([]string{"run", "--name", c.name}, args[1:]...)
	}
	c.Cmd = exec.CommandContext(ctx, "docker", args...)
	if c.name != "" {
		c.Cancel = func() error {
			c.cancelled.Store(true)
			c.remove()
			return c.Process.Kill()
		}
		c.WaitDelay = 5 * time.Second
	}
	return c
}

func (c *DockerCommand) remove() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := c.client(ctx, "rm", "--force", c.name)
	cmd.WaitDelay = time.Second
	cmd.Run()
}

func (c *DockerCommand) client(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, c.Path, args...)
	cmd.Env, cmd.Dir = c.Env, c.Dir
	return cmd
}

func (c *DockerCommand) Start() error {
	if c.started {
		return errors.New("exec: already started")
	}
	if err := c.acquire(); err != nil {
		closeDockerPipes(c.pipeWriters)
		closeDockerPipes(c.pipeReaders)
		return err
	}
	c.pull = pinnedPull(c.Args[1:])
	if c.pull {
		base := c.Cmd
		stdout := &dockerPullWriter{diagnostic: &c.diagnostic, dst: base.Stdout}
		stderr := &dockerPullWriter{diagnostic: &c.diagnostic, dst: base.Stderr}
		c.nextPull = func() *exec.Cmd {
			cmd := exec.CommandContext(c.ctx, base.Path, base.Args[1:]...)
			cmd.Args, cmd.Env, cmd.Dir = base.Args, base.Env, base.Dir
			cmd.Stdin, cmd.Stdout, cmd.Stderr = base.Stdin, stdout, stderr
			cmd.ExtraFiles, cmd.SysProcAttr = base.ExtraFiles, base.SysProcAttr
			cmd.WaitDelay = base.WaitDelay
			return cmd
		}
		base.Stdout, base.Stderr = stdout, stderr
	}
	if err := c.Cmd.Start(); err != nil {
		closeDockerPipes(c.pipeWriters)
		closeDockerPipes(c.pipeReaders)
		return err
	}
	c.started = true
	if c.pull && len(c.pipeWriters) > 0 {
		// Piped callers drain before Wait. Keep the writers through every
		// attempt, then close them so those readers can reach EOF.
		c.pullDone = make(chan error, 1)
		go func() {
			err := c.finishPull()
			closeDockerPipes(c.pipeWriters)
			c.pullDone <- err
		}()
	} else {
		closeDockerPipes(c.pipeWriters)
	}
	return nil
}

func (c *DockerCommand) Wait() error {
	if !c.started {
		return errors.New("exec: not started")
	}
	if c.waited {
		return errors.New("exec: Wait was already called")
	}
	c.waited = true
	defer closeDockerPipes(c.pipeReaders)
	if c.pullDone != nil {
		return <-c.pullDone
	}
	if c.pull {
		return c.finishPull()
	}
	err := c.Cmd.Wait()
	if c.cancelled.Load() {
		c.remove()
	}
	return err
}

func (c *DockerCommand) finishPull() error {
	// Reap the started client even if cancellation arrived before Wait.
	retry, err := c.waitPull()
	if ctxErr := c.ctx.Err(); ctxErr != nil {
		return errors.Join(ctxErr, err)
	}
	first := true
	return retryAcquisition(c.ctx, func() (bool, error) {
		if first {
			first = false
			return retry, err
		}
		c.diagnostic.Reset()
		c.Cmd = c.nextPull()
		if err := c.Cmd.Start(); err != nil {
			return false, err
		}
		return c.waitPull()
	})
}

func (c *DockerCommand) waitPull() (bool, error) {
	err := c.Cmd.Wait()
	if err == nil {
		return false, nil
	}
	diagnostic := c.diagnostic.String()
	var exit *exec.ExitError
	retry := errors.As(err, &exit) && !dockerIntegrityFailure(diagnostic)
	return retry, fmt.Errorf("%w\n%s", err, diagnostic)
}

func (c *DockerCommand) Run() error {
	if err := c.Start(); err != nil {
		return err
	}
	return c.Wait()
}

func (c *DockerCommand) Output() ([]byte, error) {
	if c.Stdout != nil {
		return nil, errors.New("exec: Stdout already set")
	}
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	capture := c.Stderr == nil
	if capture {
		c.Stderr = &stderr
	}
	err := c.Run()
	var exit *exec.ExitError
	if capture && errors.As(err, &exit) {
		exit.Stderr = stderr.Bytes()
	}
	return stdout.Bytes(), err
}

func (c *DockerCommand) CombinedOutput() ([]byte, error) {
	if c.Stdout != nil {
		return nil, errors.New("exec: Stdout already set")
	}
	if c.Stderr != nil {
		return nil, errors.New("exec: Stderr already set")
	}
	var out bytes.Buffer
	c.Stdout, c.Stderr = &out, &out
	err := c.Run()
	return out.Bytes(), err
}

// StdoutPipe connects the command's output to a pipe. Read it before Wait,
// which closes the reader after the command's final attempt.
func (c *DockerCommand) StdoutPipe() (io.ReadCloser, error) {
	return c.outputPipe(&c.Stdout, "Stdout")
}

// StderrPipe is the standard-error counterpart of StdoutPipe.
func (c *DockerCommand) StderrPipe() (io.ReadCloser, error) {
	return c.outputPipe(&c.Stderr, "Stderr")
}

func (c *DockerCommand) outputPipe(dst *io.Writer, name string) (io.ReadCloser, error) {
	if *dst != nil {
		return nil, fmt.Errorf("exec: %s already set", name)
	}
	if c.started {
		return nil, fmt.Errorf("exec: %sPipe after process started", name)
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	// exec.Cmd must not own these descriptors: a pull's diagnostic writer
	// needs them after Start closes the command's child descriptors.
	*dst = w
	c.pipeReaders = append(c.pipeReaders, r)
	c.pipeWriters = append(c.pipeWriters, w)
	return r, nil
}

func closeDockerPipes(pipes []*os.File) {
	for _, pipe := range pipes {
		pipe.Close()
	}
}

// Both streams share a lock, including when the caller gave them one writer.
type dockerDiagnostic struct {
	sync.Mutex
	bytes.Buffer
}

type dockerPullWriter struct {
	diagnostic *dockerDiagnostic
	dst        io.Writer
}

func (w *dockerPullWriter) Write(p []byte) (int, error) {
	w.diagnostic.Lock()
	defer w.diagnostic.Unlock()
	w.diagnostic.Write(p)
	if w.dst != nil {
		return w.dst.Write(p)
	}
	return len(p), nil
}

func dockerIntegrityFailure(diagnostic string) bool {
	s := strings.ToLower(diagnostic)
	return strings.Contains(s, "verification failed") ||
		strings.Contains(s, "checksum mismatch") || strings.Contains(s, "digest mismatch") ||
		strings.Contains(s, "unexpected commit digest") ||
		(strings.Contains(s, "digest") && (strings.Contains(s, "does not match") ||
			strings.Contains(s, "did not match") || strings.Contains(s, "unexpected") || strings.Contains(s, "mismatch")))
}

func pinnedImage(image string) bool {
	name, digest, ok := strings.Cut(image, "@sha256:")
	if !ok || name == "" || len(digest) != 64 {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}

func pinnedPull(args []string) bool {
	if len(args) > 1 && args[0] == "image" {
		args = args[1:]
	}
	if len(args) < 2 || args[0] != "pull" {
		return false
	}
	image := ""
	for i := 1; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--help":
			return false
		case arg == "--platform":
			i++
		case strings.HasPrefix(arg, "-"):
			continue
		case image != "":
			return false
		default:
			image = arg
		}
	}
	return pinnedImage(image)
}

func (c *DockerCommand) acquire() error {
	image, platform, policy := dockerRunImage(c.Args[1:])
	if !pinnedImage(image) || policy == "never" {
		return nil
	}
	if platform == "" {
		for _, env := range c.Environ() {
			if value, ok := strings.CutPrefix(env, "DOCKER_DEFAULT_PLATFORM="); ok {
				platform = value
			}
		}
	}
	format := "{{.Os}}/{{.Architecture}}{{if .Variant}}/{{.Variant}}{{end}}"
	out, err := c.client(c.ctx, "image", "inspect", "--format", format, image).Output()
	if err == nil {
		got := strings.TrimSpace(string(out))
		if platform == "" || got == platform || (strings.Count(platform, "/") == 1 && strings.HasPrefix(got, platform+"/")) {
			return nil
		}
	}
	args := []string{"pull"}
	if platform != "" {
		args = append(args, "--platform", platform)
	}
	pull := Docker(c.ctx, append(args, image)...)
	pull.Path, pull.Env, pull.Dir = c.Path, c.Env, c.Dir
	if err := pull.Run(); err != nil {
		return fmt.Errorf("acquiring %s: %w", image, err)
	}
	return nil
}

// Find the positional image, not a pin in an option value or the workload's
// arguments. Long options take values except Docker's boolean switches.
func dockerRunImage(args []string) (image, platform, policy string) {
	if len(args) == 0 || args[0] != "run" {
		return
	}
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			if i+1 < len(args) {
				image = args[i+1]
			}
			return
		}
		if !strings.HasPrefix(arg, "-") {
			image = arg
			return
		}
		key, value, equals := strings.Cut(arg, "=")
		if !equals {
			switch key {
			case "--help":
				return
			case "--rm", "--init", "--read-only", "--privileged", "--detach", "--interactive", "--tty", "--sig-proxy", "--oom-kill-disable", "--publish-all", "--disable-content-trust", "--no-healthcheck", "--quiet":
				continue
			}
			if !strings.HasPrefix(key, "--") {
				short := strings.TrimLeft(key[1:], "ditPq")
				if short == "" {
					continue
				}
				if !strings.ContainsAny(short[:1], "acehlmpuvw") {
					return
				}
				if len(short) > 1 {
					// A short value option can carry its value without a space.
					continue
				}
			}
			if i+1 >= len(args) {
				return
			}
			i++
			value = args[i]
		}
		switch key {
		case "--platform":
			platform = value
		case "--pull":
			policy = value
		}
	}
	return
}
