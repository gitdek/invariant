package tlc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"

	"github.com/gitdek/invariant/internal/toolchain"
)

// Graph retains the concrete states and every dumped step, before any display
// compaction. IDs are TLC fingerprints, not indexes or normalized labels.
type Graph struct {
	States  []GraphState
	Initial []string
	Steps   []GraphStep
}

type GraphState struct {
	ID, Label string
	Values    map[string]string // exact TLA+ text, including continuation indentation
}

type GraphStep struct {
	From, Action, To string
}

// GraphResult is a completed bounded search, with independently counted graph
// entries and the configuration and tool that produced them.
type GraphResult struct {
	Graph                 Graph
	Run                   Result
	StateCount, StepCount int
	Module                string
	Config                Config
	Tool                  GraphTool
}

type GraphTool struct {
	Image, JarSHA256 string
}

// DumpGraph requires complete, consistent DOT and successful completion
// records. It never returns partial evidence, even if TLC printed success.
func (r Runner) DumpGraph(ctx context.Context, dir, module string, cfg Config) (result *GraphResult, err error) {
	defer func() {
		if cause := context.Cause(ctx); cause != nil {
			result, err = nil, fmt.Errorf("TLC check %s cut off: %w", module, cause)
		}
	}()
	cfg.Constants = maps.Clone(cfg.Constants)
	cfg.Invariants = slices.Clone(cfg.Invariants)
	cfg.Properties = slices.Clone(cfg.Properties)
	dot, out, sum, err := r.dump(ctx, dir, module, cfg, true)
	if err != nil {
		return nil, err
	}
	g, err := ParseGraphDOT(dot)
	if err != nil {
		return nil, err
	}
	run, err := graphCompletion(out, g)
	if err != nil {
		return nil, fmt.Errorf("TLC graph %s: %w", module, err)
	}
	return &GraphResult{Graph: *g, Run: run, StateCount: len(g.States), StepCount: len(g.Steps),
		Module: module, Config: cfg, Tool: GraphTool{Image: r.Image, JarSHA256: sum}}, nil
}

// Dump model-checks module like Check, and returns the state graph TLC
// explored, in DOT, with every edge labeled by its action. The dashboard
// draws it (D-0049). The gate never calls Dump, so it can't change what the
// gate checks.
func (r Runner) Dump(ctx context.Context, dir, module string, cfg Config) (string, error) {
	dot, _, _, err := r.dump(ctx, dir, module, cfg, false)
	return dot, err
}

func (r Runner) dump(ctx context.Context, dir, module string, cfg Config, provenance bool) (dot, transcript, sum string, err error) {
	if err := r.Budget.acquire(ctx); err != nil {
		return "", "", "", fmt.Errorf("TLC check %s cut off: %w", module, err)
	}
	// Docker Run joins cancellation cleanup before this allocation is released.
	defer r.Budget.release()
	dir, err = filepath.Abs(dir)
	if err != nil {
		return "", "", "", err
	}
	runDir, err := os.MkdirTemp(dir, ".invariant-graph-")
	if err != nil {
		return "", "", "", err
	}
	defer os.RemoveAll(runDir)
	if err := os.WriteFile(filepath.Join(runDir, "Invariant.cfg"), []byte(cfg.render()), 0o644); err != nil {
		return "", "", "", err
	}
	jarDir, jar := filepath.Split(r.Jar)
	if provenance {
		// Hash the snapshot TLC actually mounts, so replacing the caller's jar
		// while the run is queued or running cannot change its provenance.
		jarDir, jar = runDir, "tla2tools.jar"
		sum, err = snapshotGraphJar(r.Jar, filepath.Join(jarDir, jar))
		if err != nil {
			return "", "", "", err
		}
	}
	containerDir := "/work/" + filepath.Base(runDir)
	memory, heap := r.limits()
	cmd := toolchain.Docker(ctx, "run", "--rm", "--network", "none",
		"--memory", memory, "--memory-swap", memory,
		"-v", dir+":/work", "-v", filepath.Clean(jarDir)+":/opt/tla:ro", "-w", "/work",
		r.Image, "java", "-Xmx"+heap, "-XX:+UseParallelGC", "-cp", "/opt/tla/"+jar, "tlc2.TLC",
		"-tool", "-workers", "1", "-metadir", "/tmp/states", "-dump", "dot,actionlabels", containerDir+"/states",
		"-config", containerDir+"/Invariant.cfg", module+".tla")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err = cmd.Run()
	if cause := context.Cause(ctx); cause != nil {
		return "", "", "", fmt.Errorf("TLC check %s cut off: %w", module, cause)
	}
	var exit *exec.ExitError
	exitCode := 0
	if errors.As(err, &exit) {
		exitCode = exit.ExitCode()
	}
	if failure, ok := resourceFailure(module, out.String(), exitCode); ok {
		return "", "", "", errors.New(failure.Message)
	}
	if err != nil {
		return "", "", "", fmt.Errorf("TLC couldn't dump the state graph: %w\n%s", err, tail(out.String(), 20))
	}
	file := filepath.Join(runDir, "states.dot")
	info, err := os.Lstat(file)
	if err != nil {
		return "", "", "", err
	}
	if !info.Mode().IsRegular() {
		return "", "", "", fmt.Errorf("TLC graph output is not a regular file: %s", file)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return "", "", "", err
	}
	if cause := context.Cause(ctx); cause != nil {
		return "", "", "", fmt.Errorf("TLC check %s cut off: %w", module, cause)
	}
	return string(data), out.String(), sum, nil
}

func snapshotGraphJar(source, target string) (string, error) {
	in, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("TLC jar is not a regular file: %s", source)
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(out, h), in)
	closeErr := out.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
