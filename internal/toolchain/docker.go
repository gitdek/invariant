package toolchain

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync/atomic"
	"time"
)

// started and runs make container names unique: this process's ID and start
// time tell it from every other, and the counter tells its runs apart.
var (
	started = time.Now().UnixNano()
	runs    atomic.Int64
)

// Docker returns a command for the docker client. A run is named, and when
// its context ends the container is removed before the client is killed:
// killing the client alone leaves the container running. A run that finishes
// by itself is left alone, and every other command is passed as it was given.
func Docker(ctx context.Context, args ...string) *exec.Cmd {
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
		return exec.CommandContext(ctx, "docker", args...)
	}
	name := fmt.Sprintf("invariant-%d-%d-%d", os.Getpid(), started, runs.Add(1))
	named := append([]string{"run", "--name", name}, args[1:]...)
	cmd := exec.CommandContext(ctx, "docker", named...)
	cmd.Cancel = func() error {
		rmCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		exec.CommandContext(rmCtx, "docker", "rm", "--force", name).Run()
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = 5 * time.Second
	return cmd
}
