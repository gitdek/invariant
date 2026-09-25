package tlc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Runner runs TLC inside a pinned Java image, from a jar whose checksum the
// caller has already verified.
type Runner struct {
	Image string // Java runtime image, pinned by digest
	Jar   string // local path to tla2tools.jar
}

// Config is what TLC checks. Invariant writes the config file itself, from
// ratified bounds and statements, so nothing the factory writes can change
// what gets checked.
type Config struct {
	Specification string
	Constants     map[string]string
	Invariants    []string
}

func (c Config) render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "SPECIFICATION %s\n", c.Specification)
	names := make([]string, 0, len(c.Constants))
	for name := range c.Constants {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&b, "CONSTANT %s = %s\n", name, c.Constants[name])
	}
	for _, inv := range c.Invariants {
		fmt.Fprintf(&b, "INVARIANT %s\n", inv)
	}
	return b.String()
}

// Check model-checks module (a file in dir) against cfg, with no network.
// Deadlock checking is always on: Check never passes -deadlock.
func (r Runner) Check(ctx context.Context, dir, module string, cfg Config) (Result, error) {
	if err := os.WriteFile(filepath.Join(dir, "Invariant.cfg"), []byte(cfg.render()), 0o644); err != nil {
		return Result{}, err
	}
	jarDir, jar := filepath.Split(r.Jar)
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "none",
		"-v", dir+":/work", "-v", filepath.Clean(jarDir)+":/opt/tla:ro", "-w", "/work",
		r.Image, "java", "-XX:+UseParallelGC", "-cp", "/opt/tla/"+jar, "tlc2.TLC",
		"-tool", "-workers", "1", "-metadir", "/tmp/states", "-config", "Invariant.cfg", module+".tla")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return Parse(out.String(), 0), nil
	case errors.As(err, &exit):
		return Parse(out.String(), exit.ExitCode()), nil
	default:
		return Result{}, fmt.Errorf("running TLC: %w", err)
	}
}
