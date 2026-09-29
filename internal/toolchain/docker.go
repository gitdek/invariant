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
