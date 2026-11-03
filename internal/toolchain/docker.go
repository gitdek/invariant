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

// DockerCommand keeps exec.Cmd's configuration and process state while adding
// acquisition retries and cleanup to its completion methods.
type DockerCommand struct {
	*exec.Cmd
	ctx       context.Context
	name      string
	cancelled atomic.Bool
	pull      bool
	waited    bool
	stdout    io.Writer
	stderr    io.Writer
	pullMu    sync.Mutex
	pullLog   bytes.Buffer
}

// Docker names runs and removes cancelled containers both before killing the
// client and after it exits. Only acquisition of digest-pinned images retries;
// builds and workloads execute once, and normally completed runs are left alone.
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
	if len(args) == 0 || args[0] != "run" {
		return &DockerCommand{Cmd: exec.CommandContext(ctx, "docker", args...), ctx: ctx}
	}
	name := fmt.Sprintf("invariant-%d-%d-%d", os.Getpid(), started, runs.Add(1))
	named := append([]string{"run", "--name", name}, args[1:]...)
	cmd := &DockerCommand{Cmd: exec.CommandContext(ctx, "docker", named...), ctx: ctx, name: name}
	cmd.Cancel = func() error {
		cmd.cancelled.Store(true)
		cmd.remove()
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

func (c *DockerCommand) remove() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := c.client(ctx, "rm", "--force", c.name)
	cmd.WaitDelay = time.Second
	cmd.Run()
}

// client uses the same executable, environment and daemon selection as the
// workload, without consuming its stdin or mixing acquisition into its output.
func (c *DockerCommand) client(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, c.Path, args...)
	cmd.Env, cmd.Dir = c.Env, c.Dir
	return cmd
}

func (c *DockerCommand) Start() error {
	if c.Process != nil {
		return errors.New("exec: already started")
	}
	args := c.Args[1:]
	if len(args) > 0 && args[0] == "run" {
		if err := c.acquire(args[1:]); err != nil {
			return err
		}
	}
	pullArgs := args
	if len(pullArgs) > 0 && pullArgs[0] == "image" {
		pullArgs = pullArgs[1:]
	}
	if len(pullArgs) > 0 && pullArgs[0] == "pull" {
		image, _, _ := dockerImage(pullArgs[1:])
		c.pull = digestPinned(image)
	}
	if c.pull {
		c.stdout, c.stderr = c.Stdout, c.Stderr
		c.capturePull()
	}
	return c.Cmd.Start()
}

func (c *DockerCommand) Wait() error {
	if c.Process == nil || c.waited {
		return c.Cmd.Wait()
	}
	c.waited = true
	for attempt := 1; ; attempt++ {
		err := c.Cmd.Wait()
		if c.cancelled.Load() {
			// Cancel's first removal can race a pending daemon create. Wait has
			// joined both the client and Cancel before this independent removal.
			c.remove()
		}
		if !c.pull {
			return err
		}
		if c.ctx.Err() != nil {
			return c.ctx.Err()
		}
		if err == nil {
			return nil
		}
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return err
		}
		diagnostic := strings.TrimSpace(c.pullLog.String())
		if attempt == 3 || dockerIntegrityFailure(diagnostic) {
			return fmt.Errorf("docker pull: %w\n%s", err, diagnostic)
		}
		if err := acquisitionBackoff(c.ctx, attempt); err != nil {
			return err
		}
		previous := c.Cmd
		c.Cmd = c.client(c.ctx, previous.Args[1:]...)
		c.Args = append([]string(nil), previous.Args...)
		c.Stdin = previous.Stdin
		c.ExtraFiles, c.SysProcAttr = previous.ExtraFiles, previous.SysProcAttr
		c.WaitDelay = previous.WaitDelay
		c.capturePull()
		if err := c.Cmd.Start(); err != nil {
			return err
		}
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
	captureStderr := c.Stderr == nil
	if captureStderr {
		c.Stderr = &stderr
	}
	err := c.Run()
	var exit *exec.ExitError
	if captureStderr && errors.As(err, &exit) && c.ProcessState != nil && exit.ProcessState == c.ProcessState {
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

// Both streams share a lock: a caller may send them to the same bytes.Buffer.
type dockerPullWriter struct {
	command *DockerCommand
	target  io.Writer
}

func (w dockerPullWriter) Write(p []byte) (int, error) {
	w.command.pullMu.Lock()
	defer w.command.pullMu.Unlock()
	w.command.pullLog.Write(p)
	if w.target != nil {
		return w.target.Write(p)
	}
	return len(p), nil
}

func (c *DockerCommand) capturePull() {
	c.pullLog.Reset()
	c.Stdout = dockerPullWriter{c, c.stdout}
	c.Stderr = dockerPullWriter{c, c.stderr}
}

func dockerIntegrityFailure(diagnostic string) bool {
	diagnostic = strings.ToLower(diagnostic)
	for _, phrase := range []string{"digest mismatch", "checksum mismatch", "verification failed", "failed verification", "unexpected commit digest", "invalid checksum", "invalid digest"} {
		if strings.Contains(diagnostic, phrase) {
			return true
		}
	}
	if strings.Contains(diagnostic, "digest") || strings.Contains(diagnostic, "checksum") {
		for _, phrase := range []string{"does not match", "did not match", "mismatch"} {
			if strings.Contains(diagnostic, phrase) {
				return true
			}
		}
	}
	return false
}

func digestPinned(image string) bool {
	name, digest, ok := strings.Cut(image, "@sha256:")
	if !ok || name == "" || len(digest) != 64 {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}

func (c *DockerCommand) acquire(args []string) error {
	image, platform, pull := dockerImage(args)
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
	args = []string{"image", "inspect", "--format", format}
	if platform != "" {
		args = append(args, "--platform", platform)
	}
	args = append(args, image)
	out, err := c.client(c.ctx, args...).Output()
	if err != nil && platform != "" && c.ctx.Err() == nil {
		// Older clients and daemons cannot inspect with --platform. The
		// unqualified inspection still proves a cache hit if its platform
		// matches, and never needs the registry.
		out, err = c.client(c.ctx, "image", "inspect", "--format", format, image).Output()
	}
	actual := strings.TrimSpace(string(out))
	if err == nil && (platform == "" || actual == platform || strings.HasPrefix(actual, platform+"/")) {
		return nil
	}
	args = []string{"pull"}
	if platform != "" {
		args = append(args, "--platform", platform)
	}
	args = append(args, image)
	cmd := &DockerCommand{Cmd: c.client(c.ctx, args...), ctx: c.ctx}
	_, err = cmd.CombinedOutput()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		// Callers parse workload exits as verifier results. Keep the pull's
		// diagnostic without exposing its exit status as a workload exit.
		return fmt.Errorf("acquiring Docker image %s: %v", image, err)
	}
	return err
}

// dockerImage stops at the image operand, never at an option's value or a
// workload argument. Unknown split options are left to Docker to interpret.
func dockerImage(args []string) (image, platform, pull string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			if i+1 < len(args) {
				image = args[i+1]
			}
			return
		}
		if !strings.HasPrefix(arg, "-") {
			return arg, platform, pull
		}
		key, value, equal := strings.Cut(arg, "=")
		if !equal {
			switch key {
			case "--rm", "--init", "--detach", "--interactive", "--tty", "--privileged", "--read-only", "--sig-proxy", "--oom-kill-disable", "--publish-all", "--disable-content-trust", "--quiet", "--all-tags", "--no-healthcheck", "--use-api-socket", "--help":
				continue
			case "--name", "--platform", "--pull", "--network", "--net", "--env", "--env-file", "--volume", "--mount", "--workdir", "--user", "--entrypoint",
				"--memory", "--memory-swap", "--memory-reservation", "--memory-swappiness", "--kernel-memory", "--cpus", "--cpu-shares", "--cpu-period", "--cpu-quota", "--cpuset-cpus", "--cpuset-mems", "--cpu-count", "--cpu-percent", "--cpu-rt-period", "--cpu-rt-runtime",
				"--pids-limit", "--cap-add", "--cap-drop", "--security-opt", "--tmpfs", "--ulimit", "--hostname", "--domainname", "--add-host", "--dns", "--dns-option", "--dns-search",
				"--label", "--label-file", "--publish", "--expose", "--device", "--group-add", "--ipc", "--pid", "--uts", "--userns", "--shm-size", "--stop-signal", "--stop-timeout", "--restart", "--runtime", "--gpus", "--log-driver", "--log-opt", "--cidfile", "--attach",
				"--health-cmd", "--health-interval", "--health-retries", "--health-timeout", "--health-start-period", "--health-start-interval", "--annotation", "--blkio-weight", "--blkio-weight-device", "--cgroup-parent", "--cgroupns", "--detach-keys",
				"--device-cgroup-rule", "--device-read-bps", "--device-read-iops", "--device-write-bps", "--device-write-iops", "--io-maxbandwidth", "--io-maxiops", "--ip", "--ip6", "--isolation", "--link", "--link-local-ip", "--mac-address", "--network-alias", "--oom-score-adj", "--storage-opt", "--sysctl", "--volume-driver", "--volumes-from":
				if i+1 >= len(args) {
					return
				}
				i++
				value = args[i]
			default:
				if strings.HasPrefix(key, "--") {
					return
				}
				// Short booleans may be grouped (-it); value options may
				// carry their value (-v/path:/work) or use the next argument.
				for j := 1; j < len(key); j++ {
					if strings.ContainsRune("ditPq", rune(key[j])) {
						continue
					}
					if !strings.ContainsRune("aehclmpuvw", rune(key[j])) {
						return
					}
					if j+1 == len(key) {
						i++
					}
					break
				}
			}
		}
		if key == "--platform" {
			platform = value
		}
		if key == "--pull" {
			pull = value
		}
	}
	return
}
