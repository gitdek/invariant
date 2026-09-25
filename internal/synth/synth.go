// Package synth is the factory's synthesis step. It gives a coding agent a
// skeleton holding only the ratified statements, plus the request. The agent
// writes the model and the code, checking them with the gate. Then Invariant
// runs the gate itself.
package synth

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

// Backend drives a coding agent through one synthesis.
type Backend interface {
	Name() string
	Run(ctx context.Context, job Job) (Usage, error)
}

// Job is what a backend needs.
type Job struct {
	Workspace  string    // the agent's working directory
	Prompt     string    // the task
	GateServer []string  // the command that starts Invariant's MCP server
	Tools      []string  // the server's tools the agent may call; just the gate when empty
	Transcript io.Writer // where the backend records the agent's events
}

// Usage is how the agent's run went, by the agent's own account.
type Usage struct {
	Backend   string         `json:"backend"`
	Model     string         `json:"model"`
	Turns     int            `json:"turns"`
	CostUSD   float64        `json:"cost_usd"` // the agent's estimate, not a charge
	Outcome   string         `json:"outcome"`  // how the agent's run ended
	Summary   string         `json:"summary"`  // the agent's closing message
	ToolCalls map[string]int `json:"tool_calls"`
	GateTool  string         `json:"gate_tool"` // whether the gate's MCP server connected
}

// GateRun is one call to the gate tool during synthesis.
type GateRun struct {
	Run    int      `json:"run"`
	Passed bool     `json:"passed"`
	Failed []string `json:"failed,omitempty"`
	At     string   `json:"at"`
}

// Options configures a synthesis.
type Options struct {
	Project string // the project whose ratified statements to build against
	Out     string // where the result, receipt and logs go
	Backend Backend
	Binary  string // this invariant binary, which serves the gate tool
	// KeepModel starts the agent from the whole module, draft model and all,
	// instead of a skeleton of the pinned definitions. The factory does this
	// for a project whose model it drafted with the statements.
	KeepModel bool
	GateRuns  int // the most gate runs the agent gets: one attempt and its repairs
	Timeout   time.Duration
	Toolchain toolchain.Toolchain
}

// Result is what a synthesis produced.
type Result struct {
	Project  string         `json:"project"`
	Usage    Usage          `json:"usage"`
	GateRuns []GateRun      `json:"gate_runs"`
	Final    *verify.Report `json:"final"`
	Tampered []string       `json:"tampered,omitempty"` // protected files the agent changed; the changes were discarded
	Seconds  float64        `json:"seconds"`
}

// Synthesize runs one synthesis. The result lands in Out/result as a
// complete project, with the final gate's receipt in Out/gate.
func Synthesize(ctx context.Context, o Options) (*Result, error) {
	start := time.Now()
	src, err := filepath.Abs(o.Project)
	if err != nil {
		return nil, err
	}
	out, err := filepath.Abs(o.Out)
	if err != nil {
		return nil, err
	}
	p, err := project.Load(src)
	if err != nil {
		return nil, err
	}
	request, err := p.Request()
	if err != nil {
		return nil, fmt.Errorf("the project needs a request: %w", err)
	}
	skeleton, err := Skeleton(p)
	if err != nil {
		return nil, err
	}
	if o.KeepModel {
		raw, err := os.ReadFile(p.ModulePath())
		if err != nil {
			return nil, err
		}
		skeleton = string(raw)
	}
	if err := os.RemoveAll(out); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return nil, err
	}
	// The agent works outside the repository, so it can't read the answer or
	// the repository's own instructions.
	ws, err := os.MkdirTemp("", "invariant-synth-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(ws)
	if err := Prepare(p, skeleton, ws); err != nil {
		return nil, err
	}
	transcript, err := os.Create(filepath.Join(out, "transcript.jsonl"))
	if err != nil {
		return nil, err
	}
	defer transcript.Close()
	gateLog := filepath.Join(out, "gate-runs.jsonl")

	runCtx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	usage, runErr := o.Backend.Run(runCtx, Job{
		Workspace:  ws,
		Prompt:     Prompt(p, skeleton, request, o.GateRuns, o.KeepModel),
		GateServer: []string{o.Binary, "mcp", "-ratified", src, "-max-runs", fmt.Sprint(o.GateRuns), "-log", gateLog, ws},
		Transcript: transcript,
	})
	usage.Backend = o.Backend.Name()

	r := &Result{Project: p.Manifest.Name, Usage: usage}
	if r.Tampered, err = Tampered(p, ws); err != nil {
		return nil, err
	}
	final := filepath.Join(out, "result")
	if err := Assemble(p, ws, final); err != nil {
		return nil, err
	}
	if r.Final, err = verify.Run(ctx, final, filepath.Join(out, "gate"), o.Toolchain); err != nil {
		return nil, fmt.Errorf("the final gate couldn't run: %w", err)
	}
	if r.GateRuns, err = readGateRuns(gateLog); err != nil {
		return nil, err
	}
	r.Seconds = time.Since(start).Seconds()
	if err := writeJSON(filepath.Join(out, "synthesis.json"), r); err != nil {
		return nil, err
	}
	if runErr != nil {
		return r, fmt.Errorf("the agent's run failed: %w", runErr)
	}
	return r, nil
}

// Skeleton is the project's TLA+ module reduced to what's ratified.
func Skeleton(p *project.Project) (string, error) {
	raw, err := os.ReadFile(p.ModulePath())
	if err != nil {
		return "", err
	}
	keep, err := p.Pinned(string(raw))
	if err != nil {
		return "", err
	}
	return tla.Skeleton(string(raw), keep, p.SpecName())
}

// protected lists the files that belong to the people, not the factory: the
// manifest, the lock, the request, and the module's go.mod and go.sum.
func protected() []string {
	return []string{".invariant/invariant.json", ".invariant/ratified.lock", ".invariant/request.md", "go.mod", "go.sum"}
}

// Prepare fills the agent's workspace: copies of the protected files, the
// skeleton in place of the module, and an empty package directory.
func Prepare(p *project.Project, skeleton, ws string) error {
	for _, rel := range protected() {
		if err := copyFile(filepath.Join(p.Dir, rel), filepath.Join(ws, rel)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := writeFile(filepath.Join(ws, p.Manifest.Module), skeleton); err != nil {
		return err
	}
	return os.MkdirAll(filepath.Join(ws, p.Manifest.Code), 0o755)
}

// Assemble builds a project from the people's files and the factory's work:
// the protected files come from the original project, and only the model and
// the code come from the workspace. So nothing the agent does to the lock,
// the request or go.mod can reach the gate.
func Assemble(p *project.Project, ws, dst string) error {
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	for _, rel := range protected() {
		if err := copyFile(filepath.Join(p.Dir, rel), filepath.Join(dst, rel)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := copyFile(filepath.Join(ws, p.Manifest.Module), filepath.Join(dst, p.Manifest.Module)); err != nil {
		return err
	}
	code := filepath.Join(ws, p.Manifest.Code)
	if err := os.MkdirAll(filepath.Join(dst, p.Manifest.Code), 0o755); err != nil {
		return err
	}
	return filepath.WalkDir(code, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return err
		}
		if n := d.Name(); n == "go.mod" || n == "go.sum" {
			return nil
		}
		rel, _ := filepath.Rel(ws, path)
		return copyFile(path, filepath.Join(dst, rel))
	})
}

// Tampered lists the protected files the agent changed in its workspace.
func Tampered(p *project.Project, ws string) ([]string, error) {
	var changed []string
	for _, rel := range protected() {
		want, errWant := os.ReadFile(filepath.Join(p.Dir, rel))
		got, errGot := os.ReadFile(filepath.Join(ws, rel))
		if os.IsNotExist(errWant) && os.IsNotExist(errGot) {
			continue
		}
		if errWant != nil || errGot != nil || !bytes.Equal(want, got) {
			changed = append(changed, rel)
		}
	}
	return changed, nil
}

func readGateRuns(path string) ([]GateRun, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var runs []GateRun
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var g GateRun
		if err := json.Unmarshal(scanner.Bytes(), &g); err == nil {
			runs = append(runs, g)
		}
	}
	return runs, scanner.Err()
}

// LogGateRun appends one gate run to the synthesis log.
func LogGateRun(path string, g GateRun) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(g)
}

func copyFile(from, to string) error {
	b, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return writeFile(to, string(b))
}

func writeFile(path, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(text), 0o644)
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// Summary renders a synthesis for people.
func Summary(r *Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Synthesis · %s\n\n", r.Project)
	verdict := "❌ The final gate failed."
	if r.Final != nil && r.Final.Passed {
		verdict = "✅ The final gate passed."
	}
	fmt.Fprintf(&b, "**%s** %s, model %s, %d turns, about $%.2f by the agent's own estimate, %.0f seconds.\n\n",
		verdict, r.Usage.Backend, r.Usage.Model, r.Usage.Turns, r.Usage.CostUSD, r.Seconds)
	if len(r.GateRuns) > 0 {
		b.WriteString("| Gate run | Result |\n| :-- | :-- |\n")
		for _, g := range r.GateRuns {
			result := "✅ passed"
			if !g.Passed {
				result = "❌ " + strings.Join(g.Failed, ", ")
			}
			fmt.Fprintf(&b, "| %d | %s |\n", g.Run, result)
		}
		b.WriteString("\n")
	}
	if len(r.Tampered) > 0 {
		fmt.Fprintf(&b, "The agent edited protected files (%s). Its edits were discarded.\n\n", strings.Join(r.Tampered, ", "))
	}
	if r.Usage.GateTool != "" && r.Usage.GateTool != "connected" {
		fmt.Fprintf(&b, "The gate tool didn't connect (%s).\n\n", r.Usage.GateTool)
	}
	return b.String()
}
