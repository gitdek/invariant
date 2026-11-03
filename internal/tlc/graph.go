package tlc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Dump model-checks module like Check, and returns the state graph TLC
// explored, in DOT, with every edge labeled by its action. The dashboard
// draws it (D-0049). The gate never calls Dump, so it can't change what the
// gate checks.
func (r Runner) Dump(ctx context.Context, dir, module string, cfg Config) (string, error) {
	release, err := r.Budget.acquire(ctx)
	if err != nil {
		return "", fmt.Errorf("TLC check %s cut off: %w", module, err)
	}
	defer release()
	if err := os.WriteFile(filepath.Join(dir, "Invariant.cfg"), []byte(cfg.render()), 0o644); err != nil {
		return "", err
	}
	cmd := r.command(ctx, dir, module, "-dump", "dot,actionlabels", "/work/states")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err = cmd.Run()
	if cause := context.Cause(ctx); cause != nil {
		return "", fmt.Errorf("TLC check %s cut off: %w", module, cause)
	}
	var exit *exec.ExitError
	exitCode := 0
	if errors.As(err, &exit) {
		exitCode = exit.ExitCode()
	}
	if res, failed := memoryFailure(module, out.String(), exitCode); failed {
		return "", fmt.Errorf("%s", res.Message)
	}
	if err != nil {
		return "", fmt.Errorf("TLC couldn't dump the state graph: %w\n%s", err, tail(out.String(), 20))
	}
	dot, err := os.ReadFile(filepath.Join(dir, "states.dot"))
	return string(dot), err
}
