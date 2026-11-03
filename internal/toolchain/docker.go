package toolchain

import (
	"bytes"
	"context"
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

// DockerCommand keeps exec.Cmd's configuration and process status while
// retrying pinned image acquisition and completing cancellation cleanup.
type DockerCommand struct {
	*exec.Cmd
	ctx        context.Context
	name       string
	cancelled  atomic.Bool
	pull       bool
	diagnostic bytes.Buffer
	done       chan error
	waited     bool
	pipes      []io.Closer
	readPipes  []io.Closer
}

// Docker returns a command for the docker client. A run is named, and when
// its context ends the container is removed before the client is killed and
// again after it exits. Only pinned image acquisition is retried, never a
// build or the command executed in a container.
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
	name := ""
	if len(args) > 0 && args[0] == "run" {
		name = fmt.Sprintf("invariant-%d-%d-%d", os.Getpid(), started, runs.Add(1))
		args = append([]string{"run", "--name", name}, args[1:]...)
	}
	cmd := &DockerCommand{Cmd: exec.CommandContext(ctx, "docker", args...), ctx: ctx, name: name}
	cmd.pull = digestPinned(dockerPullImage(args))
	if name == "" {
		return cmd
	}
	cmd.Cancel = func() error {
		cmd.cancelled.Store(true)
		cmd.remove()
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

func (c *DockerCommand) remove() {
	// Both removals and the client's five-second WaitDelay must fit within
	// the plumbing sandbox's fifteen-second cleanup reserve.
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	c.command(ctx, "rm", "--force", c.name).Run()
}

func (c *DockerCommand) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, c.Path, args...)
	cmd.Env, cmd.Dir = c.Env, c.Dir
	return cmd
}

func (c *DockerCommand) Start() (err error) {
	if c.done != nil || c.Process != nil {
		return errors.New("exec: already started")
	}
	defer func() {
		if err != nil {
			c.closePipes()
			c.closeReadPipes()
		}
	}()
	if c.Err != nil {
		return c.Err
	}
	if c.name != "" {
		if err := c.acquire(); err != nil {
			return err
		}
	}
	if c.pull {
		// Keep streaming output, also retaining the current attempt's diagnostic
		// when the caller has not supplied a writer. Serialize shared writers.
		mu := new(sync.Mutex)
		c.Stdout = &dockerPullWriter{mu, &c.diagnostic, c.Stdout}
		c.Stderr = &dockerPullWriter{mu, &c.diagnostic, c.Stderr}
	}
	if err := c.Cmd.Start(); err != nil {
		return err
	}
	if c.pull {
		c.done = make(chan error, 1)
		go func() {
			err := c.wait()
			c.closePipes()
			c.done <- err
		}()
	}
	return nil
}

func (c *DockerCommand) Wait() error {
	if c.done != nil {
		if c.waited {
			return errors.New("exec: Wait was already called")
		}
		c.waited = true
		err := <-c.done
		c.closeReadPipes()
		return err
	}
	if c.Process == nil || c.ProcessState != nil {
		return c.Cmd.Wait()
	}
	return c.wait()
}

func (c *DockerCommand) wait() error {
	for attempt := 1; ; attempt++ {
		err := c.Cmd.Wait()
		if c.cancelled.Load() {
			// Wait has joined the cancellation callback and reaped the client.
			// Its pending create may have landed after the first removal.
			c.remove()
		}
		if !c.pull || err == nil {
			return err
		}
		if c.ctx.Err() != nil {
			return c.ctx.Err()
		}
		diagnostic := strings.TrimSpace(c.diagnostic.String())
		var exit *exec.ExitError
		if !errors.As(err, &exit) || attempt == 3 || dockerIntegrityFailure(diagnostic) {
			return fmt.Errorf("docker pull: %w\n%s", err, diagnostic)
		}
		if err := retryDelay(c.ctx, attempt); err != nil {
			return err
		}
		previous := c.Cmd
		c.Cmd = c.command(c.ctx, previous.Args[1:]...)
		c.Args = append([]string(nil), previous.Args...)
		c.Stdin, c.Stdout, c.Stderr = previous.Stdin, previous.Stdout, previous.Stderr
		c.ExtraFiles, c.SysProcAttr, c.WaitDelay = previous.ExtraFiles, previous.SysProcAttr, previous.WaitDelay
		c.diagnostic.Reset()
		if err := c.Cmd.Start(); err != nil {
			return err
		}
	}
}

// Pulls keep their output pipes open across attempts. exec.Cmd's pipes belong
// to one process and are closed when that attempt finishes.
// Track delegated endpoints too, for failures before exec.Cmd.Start.
func (c *DockerCommand) StdoutPipe() (io.ReadCloser, error) {
	if !c.pull {
		r, err := c.Cmd.StdoutPipe()
		if err == nil {
			c.pipes = append(c.pipes, r, c.Stdout.(io.Closer))
		}
		return r, err
	}
	return c.outputPipe(&c.Stdout, "Stdout")
}

func (c *DockerCommand) StderrPipe() (io.ReadCloser, error) {
	if !c.pull {
		r, err := c.Cmd.StderrPipe()
		if err == nil {
			c.pipes = append(c.pipes, r, c.Stderr.(io.Closer))
		}
		return r, err
	}
	return c.outputPipe(&c.Stderr, "Stderr")
}

func (c *DockerCommand) StdinPipe() (io.WriteCloser, error) {
	w, err := c.Cmd.StdinPipe()
	if err == nil {
		c.pipes = append(c.pipes, w, c.Stdin.(io.Closer))
	}
	return w, err
}

func (c *DockerCommand) outputPipe(dst *io.Writer, name string) (io.ReadCloser, error) {
	if *dst != nil {
		return nil, fmt.Errorf("exec: %s already set", name)
	}
	if c.Process != nil {
		return nil, errors.New("exec: pipe after process started")
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	*dst = w
	c.pipes = append(c.pipes, w)
	c.readPipes = append(c.readPipes, r)
	return r, nil
}

func (c *DockerCommand) closePipes() {
	for _, pipe := range c.pipes {
		pipe.Close()
	}
}

func (c *DockerCommand) closeReadPipes() {
	for _, pipe := range c.readPipes {
		pipe.Close()
	}
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
	var output bytes.Buffer
	c.Stdout, c.Stderr = &output, &output
	err := c.Run()
	return output.Bytes(), err
}

type dockerPullWriter struct {
	mu         *sync.Mutex
	diagnostic *bytes.Buffer
	output     io.Writer
}

func (w *dockerPullWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.diagnostic.Write(p)
	if w.output != nil {
		return w.output.Write(p)
	}
	return len(p), nil
}

func dockerIntegrityFailure(diagnostic string) bool {
	s := strings.ToLower(diagnostic)
	return strings.Contains(s, "digest mismatch") || strings.Contains(s, "unexpected commit digest") ||
		strings.Contains(s, "verification failed") || strings.Contains(s, "checksum mismatch") ||
		(strings.Contains(s, "digest") && (strings.Contains(s, "does not match") || strings.Contains(s, "did not match"))) ||
		(strings.Contains(s, "failed to verify") && (strings.Contains(s, "digest") || strings.Contains(s, "layer")))
}

func digestPinned(image string) bool {
	name, digest, ok := strings.Cut(image, "@sha256:")
	return ok && name != "" && len(digest) == 64 && strings.Trim(digest, "0123456789abcdef") == ""
}

func dockerPullImage(args []string) string {
	if len(args) >= 2 && args[0] == "image" {
		args = args[1:]
	}
	if len(args) == 0 || args[0] != "pull" {
		return ""
	}
	image := ""
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "--platform" {
			i++
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if image != "" {
			return "" // Invalid multiple-image commands are Docker's to reject.
		}
		image = arg
	}
	return image
}

func (c *DockerCommand) acquire() error {
	image, platform, pull := dockerRunImage(c.Args[2:])
	if !digestPinned(image) || pull == "never" {
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
	out, err := c.command(c.ctx, "image", "inspect", "--format", format, image).Output()
	actual := strings.TrimSpace(string(out))
	if err == nil && (platform == "" || actual == platform || (strings.Count(platform, "/") == 1 && strings.HasPrefix(actual, platform+"/"))) {
		return nil
	}
	args := []string{"pull"}
	if platform != "" {
		args = append(args, "--platform", platform)
	}
	args = append(args, image)
	cmd := Docker(c.ctx, args...)
	cmd.Path, cmd.Env, cmd.Dir = c.Path, c.Env, c.Dir
	out, err = cmd.CombinedOutput()
	if err != nil {
		// Callers that recognize ExitError read their configured output for
		// diagnostics, just as they do for a failed workload.
		if c.Stderr != nil {
			c.Stderr.Write(cmd.diagnostic.Bytes())
		}
		return fmt.Errorf("acquiring %s: %w\n%s", image, err, out)
	}
	return nil
}

// Parse only run options, stopping at the image so an environment value or
// workload argument containing a digest cannot become the acquisition target.
func dockerRunImage(args []string) (image, platform, pull string) {
	for i := 0; i < len(args); i++ {
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
			case "--rm", "--read-only", "--privileged", "--init", "--detach", "--interactive", "--tty", "--publish-all", "--disable-content-trust", "--sig-proxy", "--oom-kill-disable", "--no-healthcheck", "--quiet", "--use-api-socket", "--help":
				continue
			}
			if !strings.HasPrefix(key, "--") {
				// Short booleans can be bundled before a value-taking flag,
				// such as -itv /input:/work or -ieKEY=value.
				short := strings.TrimLeft(key[1:], "ditPq")
				if len(short) != 1 {
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
			pull = value
		}
	}
	return
}
