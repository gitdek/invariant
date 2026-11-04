package tlc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/gitdek/invariant/internal/toolchain"
)

// Dump model-checks module like Check, and returns the state graph TLC
// explored, in DOT, with every edge labeled by its action. The dashboard
// draws it (D-0049). The gate never calls Dump, so it can't change what the
// gate checks.
func (r Runner) Dump(ctx context.Context, dir, module string, cfg Config) (string, error) {
	if err := r.Budget.acquire(ctx); err != nil {
		return "", fmt.Errorf("TLC check %s cut off: %w", module, err)
	}
	defer r.Budget.release()
	if err := os.WriteFile(filepath.Join(dir, "Invariant.cfg"), []byte(cfg.render()), 0o644); err != nil {
		return "", err
	}
	jarDir, jar := filepath.Split(r.Jar)
	memory, heap := r.limits()
	cmd := toolchain.Docker(ctx, "run", "--rm", "--network", "none",
		"--memory", memory, "--memory-swap", memory,
		"-v", dir+":/work", "-v", filepath.Clean(jarDir)+":/opt/tla:ro", "-w", "/work",
		r.Image, "java", "-Xmx"+heap, "-XX:+UseParallelGC", "-cp", "/opt/tla/"+jar, "tlc2.TLC",
		"-tool", "-workers", "1", "-metadir", "/tmp/states", "-dump", "dot,actionlabels", "/work/states",
		"-config", "Invariant.cfg", module+".tla")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if cause := context.Cause(ctx); cause != nil {
		return "", fmt.Errorf("TLC check %s cut off: %w", module, cause)
	}
	var exit *exec.ExitError
	exitCode := 0
	if errors.As(err, &exit) {
		exitCode = exit.ExitCode()
	}
	if failure, ok := resourceFailure(module, out.String(), exitCode); ok {
		return "", errors.New(failure.Message)
	}
	if err != nil {
		return "", fmt.Errorf("TLC couldn't dump the state graph: %w\n%s", err, tail(out.String(), 20))
	}
	dot, err := os.ReadFile(filepath.Join(dir, "states.dot"))
	return string(dot), err
}
