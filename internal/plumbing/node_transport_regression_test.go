package plumbing

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Optional local asset checks supplement the pinned Docker matrix. They never
// install a runtime or make Node a prerequisite for the default Go suite.
func TestNodeEmbeddedAssetsAndPrivateTransport(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("local Node is unavailable; pinned-image transport coverage is opt-in")
	}
	root := t.TempDir()
	for name, source := range map[string][]byte{"transport.cjs": dockerNodeProcessHelper, "node_runner_common.cjs": nodeRunnerCommon, "vitest_runner.mjs": vitestRunner, "jest_runner.cjs": jestRunner} {
		file := filepath.Join(root, name)
		if err := os.WriteFile(file, source, 0600); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		out, err := exec.CommandContext(ctx, node, "--check", file).CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("%s syntax: %v %s", name, err, out)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	read, control, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer control.Close()
	p := &dockerRepositoryProcess{output: RepositoryProcessOutput{ExitCode: -1}}
	command := exec.CommandContext(ctx, node, filepath.Join(root, "transport.cjs"), "NODE_TEST_EVENTS", node, "-e",
		`require('node:fs').writeFileSync(process.env.NODE_TEST_EVENTS,'private\n');console.log('diagnostic');process.exit(7);`)
	var diagnostic strings.Builder
	command.Stdin, command.Stdout, command.Stderr = read, p, &diagnostic
	command.WaitDelay = time.Second
	if err := command.Run(); err != nil {
		t.Fatalf("Node helper: %v %s", err, diagnostic.String())
	}
	out := p.Snapshot()
	if p.err != nil || !out.Started || !out.Exited || !out.Drained || out.ExitCode != 7 || string(out.Events) != "private\n" || string(out.Stdout) != "diagnostic\n" || len(out.Stderr) != 0 || len(p.pending) != 0 {
		t.Fatalf("transport lost independent evidence, diagnostics or normal exit: %v %+v", p.err, out)
	}
}
