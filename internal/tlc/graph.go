package tlc

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gitdek/invariant/internal/toolchain"
)

// Dump model-checks module like Check, and returns the state graph TLC
// explored, in DOT, with every edge labeled by its action. The dashboard
// draws it (D-0049). The gate never calls Dump, so it can't change what the
// gate checks.
func (r Runner) Dump(ctx context.Context, dir, module string, cfg Config) (string, error) {
	if err := os.WriteFile(filepath.Join(dir, "Invariant.cfg"), []byte(cfg.render()), 0o644); err != nil {
		return "", err
	}
	jarDir, jar := filepath.Split(r.Jar)
	cmd := toolchain.Docker(ctx, "run", "--rm", "--network", "none",
		"-v", dir+":/work", "-v", filepath.Clean(jarDir)+":/opt/tla:ro", "-w", "/work",
		r.Image, "java", "-XX:+UseParallelGC", "-cp", "/opt/tla/"+jar, "tlc2.TLC",
		"-tool", "-workers", "1", "-metadir", "/tmp/states", "-dump", "dot,actionlabels", "/work/states",
		"-config", "Invariant.cfg", module+".tla")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("TLC couldn't dump the state graph: %w\n%s", err, tail(out.String(), 20))
	}
	dot, err := os.ReadFile(filepath.Join(dir, "states.dot"))
	return string(dot), err
}
