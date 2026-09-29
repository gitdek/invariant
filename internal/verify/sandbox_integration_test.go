//go:build integration

package verify

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/toolchain"
)

// A sandbox that outlives its deadline is stopped, and its container is
// gone, not left running after the client that started it was killed. The
// run has no --rm, so only the removal on the deadline can take it away.
func TestSandboxPastItsDeadlineIsRemoved(t *testing.T) {
	if err := pulled(context.Background(), toolchain.GoImage); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := toolchain.Docker(ctx, "run", "--network", "none", toolchain.GoImage, "sleep", "600")
	name := cmd.Args[3]
	t.Cleanup(func() { exec.Command("docker", "rm", "--force", name).Run() })
	if err := cmd.Run(); err == nil {
		t.Fatal("a sandbox past its deadline must return an error")
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(250 * time.Millisecond) {
		out, err := exec.Command("docker", "ps", "--all", "--quiet", "--filter", "name=^/"+name+"$").Output()
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(out)) == "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("container %s is still there after its deadline", name)
		}
	}
}
