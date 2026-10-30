package toolchain

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"
)

// started and runs make container names unique: this process's ID and start
// time tell it from every other, and the counter tells its runs apart.
var (
	started = time.Now().UnixNano()
	runs    atomic.Int64
)

// Docker's client configuration injects both forms of each proxy variable
// into runs and builds, including credentials. Explicit empty assignments
// override default and daemon-specific configuration without replacing the
// user's configuration or changing how the client selects its daemon.
var dockerProxyVariables = []string{
	"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy",
	"FTP_PROXY", "ftp_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy",
}

// Docker returns a command for the docker client. A run is named, and when
// its context ends the container is removed before the client is killed:
// killing the client alone leaves the container running. A run that finishes
// by itself is left alone. Runs and builds suppress client-configured proxy
// injection; every other command keeps its arguments.
func Docker(ctx context.Context, args ...string) *exec.Cmd {
	if len(args) > 0 && args[0] == "build" {
		build := append([]string{"build"}, dockerProxyArguments("--build-arg")...)
		args = append(build, args[1:]...)
	}
	if len(args) == 0 || args[0] != "run" {
		return dockerClientCommand(ctx, args...)
	}
	name := fmt.Sprintf("invariant-%d-%d-%d", os.Getpid(), started, runs.Add(1))
	named := append([]string{"run", "--name", name}, dockerProxyArguments("-e")...)
	named = append(named, args[1:]...)
	cmd := dockerClientCommand(ctx, named...)
	cmd.Cancel = func() error {
		rmCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		dockerClientCommand(rmCtx, "rm", "--force", name).Run()
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

func dockerProxyArguments(flag string) []string {
	var args []string
	for _, key := range dockerProxyVariables {
		args = append(args, flag, key+"=")
	}
	return args
}

func dockerClientCommand(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Env = []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		proxy := false
		for _, name := range dockerProxyVariables {
			if key == name {
				proxy = true
				break
			}
		}
		if !proxy {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	return cmd
}
