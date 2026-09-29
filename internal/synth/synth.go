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
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/regular"
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
	// ReadOnly gives the agent only the tools that read files, and no gate:
	// a reviewer's job (D-0086).
	ReadOnly bool
	// Effort is how hard the agent thinks, in place of the backend's own. Only
	// a build's fallback run names one (D-0125); every first run leaves it
	// empty, and thinks at the backend's own effort.
	Effort string
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
	// Tokens is what the run used, where the agent reports tokens rather
	// than a cost, as Codex does on an account (#153).
	Tokens *Tokens `json:"tokens,omitempty"`
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
	// KeepCode starts the agent from the project's existing code, for an
	// amendment (D-0045). Without it, the agent never sees the code, so it
	// can't copy an answer.
	KeepCode  bool
	GateRuns  int // the most gate runs the agent gets: one attempt and its repairs
	Timeout   time.Duration
	Toolchain toolchain.Toolchain
	// FallbackEffort is the effort the agent runs at once more when the loop
	// guard stops its first run (D-0125). Empty never runs it again.
	FallbackEffort string
}

// Result is what a synthesis produced.
type Result struct {
	Project  string         `json:"project"`
	Usage    Usage          `json:"usage"`
	GateRuns []GateRun      `json:"gate_runs"`
	Final    *verify.Report `json:"final"`
	Tampered []string       `json:"tampered,omitempty"` // protected files the agent changed; the changes were discarded
	Dir      string         `json:"dir"`                // the finished project, under Out/result
	Seconds  float64        `json:"seconds"`
	// Review is a second agent's reading of the driver, when synthesis
	// passed and the project has one (D-0082, D-0086).
	Review *Review `json:"review,omitempty"`
	// Fallback says the agent ran once more, when the loop guard stopped its
	// first run (D-0125). Usage is the second run's.
	Fallback *Fallback `json:"fallback,omitempty"`
}

// Spend is the agents' estimated cost for the build: synthesis, and the
// review if there was one.
func (r *Result) Spend() float64 {
	spend := r.Usage.CostUSD
	if r.Review != nil {
		spend += r.Review.Usage.CostUSD
	}
	return spend
}

// Fallback is a build's second run of its agent, after the loop guard
// stopped the first (D-0125).
type Fallback struct {
	Effort string `json:"effort"` // what the second run thought at
	Why    string `json:"why"`    // the first run's error
}

// RunAgent runs a build's agent on job, cut off after timeout. When the loop
// guard stops the run and fallback names an effort, it runs the same job once
// more at that effort, its timeout starting again, in the same workspace, so
// the agent keeps what it wrote (D-0125). That run's usage and error are the
// build's, and there's never a third. RunAgent returns the fallback it took,
// or nil.
func RunAgent(ctx context.Context, backend Backend, job Job, timeout time.Duration, fallback string) (Usage, *Fallback, error) {
	usage, err := runWithin(ctx, backend, job, timeout)
	if fallback == "" || !errors.Is(err, ErrThinkingLoop) {
		return usage, nil, err
	}
	fb := &Fallback{Effort: fallback, Why: err.Error()}
	job.Effort = fallback
	usage, err = runWithin(ctx, backend, job, timeout)
	return usage, fb, err
}

// runWithin runs backend on job, cut off after timeout.
func runWithin(ctx context.Context, backend Backend, job Job, timeout time.Duration) (Usage, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return backend.Run(ctx, job)
}

// Synthesize runs one synthesis. The result lands in Out/result as a
// complete project, with the final gate's receipt in Out/gate. When the
// final gate can't run, Synthesize returns the result with no final report,
// and an error.
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
	if o.KeepCode {
		if err := copyOwned(p, ws); err != nil {
			return nil, err
		}
	}
	if len(p.Manifest.Existing) > 0 {
		if err := seedExisting(p, ws); err != nil {
			return nil, err
		}
	}
	transcript, err := os.Create(filepath.Join(out, "transcript.jsonl"))
	if err != nil {
		return nil, err
	}
	defer transcript.Close()
	gateLog := filepath.Join(out, "gate-runs.jsonl")

	usage, fallback, runErr := RunAgent(ctx, o.Backend, Job{
		Workspace:  ws,
		Prompt:     Prompt(p, skeleton, request, o.GateRuns, o.KeepModel, o.KeepCode),
		GateServer: []string{o.Binary, "mcp", "-ratified", src, "-max-runs", fmt.Sprint(o.GateRuns), "-log", gateLog, ws},
		Transcript: transcript,
	}, o.Timeout, o.FallbackEffort)
	usage.Backend = o.Backend.Name()

	r := &Result{Project: p.Manifest.Name, Usage: usage, Fallback: fallback}
	if r.Tampered, err = Tampered(p, ws); err != nil {
		return nil, err
	}
	final, err := Stage(p, ws, filepath.Join(out, "result"))
	if err != nil {
		return nil, err
	}
	r.Dir = final
	// A final gate that can't run, as when the agent wrote no code, leaves the
	// result with no final report.
	report, gateErr := verify.Run(ctx, final, filepath.Join(out, "gate"), o.Toolchain)
	if gateErr == nil {
		r.Final = report
	}
	if r.GateRuns, err = readGateRuns(gateLog); err != nil {
		return nil, err
	}
	r.Seconds = time.Since(start).Seconds()
	if err := writeJSON(filepath.Join(out, "synthesis.json"), r); err != nil {
		return nil, err
	}
	// A failed run's error comes first, since it's the likelier cause: #91's
	// error said only that the gate found no Go files, when its agent had
	// looped.
	switch {
	case runErr != nil && gateErr != nil:
		return r, fmt.Errorf("the agent's run failed: %w; then the final gate couldn't run: %w", runErr, gateErr)
	case gateErr != nil:
		return r, fmt.Errorf("the final gate couldn't run: %w", gateErr)
	case runErr != nil:
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
	// The spec refers to the model, and a fairness statement may too, as in
	// WF_vars(Next), so they go after it.
	return tla.Skeleton(string(raw), keep, append([]string{p.SpecName()}, p.FairnessNames()...))
}

// protected lists the files that belong to the people, not the factory: the
// manifest, the lock, the request, and the file where the language keeps its
// dependencies (go.mod and go.sum, or package.json).
func protected(language string) []string {
	files := []string{".invariant/invariant.json", ".invariant/ratified.lock", ".invariant/request.md"}
	switch language {
	case "typescript":
		return append(files, "package.json")
	case "python":
		return files
	default:
		return append(files, "go.mod", "go.sum")
	}
}

// manifestFile is the project's manifest, which the people own, except for
// the one field the agent writes: the sizes its code takes as parameters.
const manifestFile = ".invariant/invariant.json"

// agentParameters is what the agent named as its code's parameters in its
// copy of the manifest (D-0082, D-0085). It counts only when the rest of
// that copy is the people's manifest exactly.
func agentParameters(p *project.Project, ws string) ([]string, bool) {
	b, err := regular.ReadFile(ws, manifestFile)
	if err != nil {
		return nil, false
	}
	var m project.Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, false
	}
	params := m.Parameters
	m.Parameters = p.Manifest.Parameters
	if !reflect.DeepEqual(m, p.Manifest) {
		return nil, false
	}
	return params, true
}

// Owned says whether a project file is the agent's to write: the module,
// anything in the code's directory, the conformance driver, and for Python,
// the tests beside the driver. Nothing else the agent writes reaches the
// project.
func Owned(m project.Manifest, rel string) bool { return owned(m, rel) }

// copyOwned copies the project's own code into the agent's workspace, for
// an amendment. The module is already there.
func copyOwned(p *project.Project, ws string) error {
	return filepath.WalkDir(p.Dir, func(file string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return err
		}
		rel, _ := filepath.Rel(p.Dir, file)
		if rel = filepath.ToSlash(rel); rel == p.Manifest.Module || !owned(p.Manifest, rel) {
			return nil
		}
		return copyFile(file, filepath.Join(ws, filepath.FromSlash(rel)))
	})
}

func owned(m project.Manifest, rel string) bool {
	if len(m.Existing) > 0 {
		// A project that checks existing code holds only the model, the
		// driver and its helpers (D-0054). The copies of the code in
		// existing/ are there to read, and never come back.
		switch {
		case rel == m.Module:
			return true
		case strings.HasPrefix(rel, ".invariant/"), strings.HasPrefix(rel, "existing/"):
			return false
		}
		switch path.Base(rel) {
		case "package.json", "package-lock.json", "node_modules":
			return false
		}
		return !strings.Contains(rel, "node_modules/")
	}
	switch {
	case rel == m.Module, m.Conformance != "" && rel == m.Conformance:
		return true
	case strings.HasPrefix(rel, m.Code+"/"):
		switch path.Base(rel) {
		case "go.mod", "go.sum", "package.json":
			return false
		}
		return true
	case m.Language == "python" && !strings.Contains(rel, "/"):
		return strings.HasPrefix(rel, "test_") && strings.HasSuffix(rel, ".py")
	}
	return false
}

// Prepare fills the agent's workspace: copies of the protected files, the
// skeleton in place of the module, and an empty package directory.
func Prepare(p *project.Project, skeleton, ws string) error {
	for _, rel := range protected(p.Manifest.Language) {
		if err := copyFile(filepath.Join(p.Dir, rel), filepath.Join(ws, rel)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := writeFile(filepath.Join(ws, p.Manifest.Module), skeleton); err != nil {
		return err
	}
	// Invariant's harness, for the driver to import (D-0085). It's not the
	// agent's, so it never reaches the project: the gate writes its own in
	// when the driver runs.
	if name, source := verify.Harness(p.Manifest.Language); name != "" && len(p.Manifest.Existing) == 0 {
		dir := ws
		if p.Manifest.Language == "typescript" {
			dir = filepath.Join(ws, filepath.Dir(p.Manifest.Conformance))
		}
		if err := writeFile(filepath.Join(dir, name), string(source)); err != nil {
			return err
		}
	}
	return os.MkdirAll(filepath.Join(ws, p.Manifest.Code), 0o755)
}

// Assemble builds a project from the people's files and the factory's work:
// the protected files come from the original project, and only the files the
// agent owns come from the workspace. So nothing the agent does to the lock,
// the request or the dependencies can reach the gate.
func Assemble(p *project.Project, ws, dst string) error {
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	for _, rel := range protected(p.Manifest.Language) {
		if err := copyFile(filepath.Join(p.Dir, rel), filepath.Join(dst, rel)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	// The agent may name its code's parameters, and nothing else, in the
	// manifest.
	if params, ok := agentParameters(p, ws); ok && !slices.Equal(params, p.Manifest.Parameters) {
		m := p.Manifest
		m.Parameters = params
		b, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dst, manifestFile), append(b, '\n'), 0o644); err != nil {
			return err
		}
	}
	// The factory takes only regular files from the agent's workspace, and
	// refuses a link by name (#153).
	module, err := regular.ReadFile(ws, p.Manifest.Module)
	if err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dst, p.Manifest.Module), string(module)); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dst, p.Manifest.Code), 0o755); err != nil {
		return err
	}
	return regular.Walk(ws, func(rel string) bool {
		return rel != p.Manifest.Module && owned(p.Manifest, rel)
	}, func(rel string, text []byte) error {
		return writeFile(filepath.Join(dst, filepath.FromSlash(rel)), string(text))
	})
}

// Stage assembles a project for the gate, as Assemble does, and returns its
// directory. A project that checks existing code needs its package around
// it (D-0054). So Stage lays the package's manifest, lockfile, tsconfig and
// named code into dir from the ratified project's package, and the project
// at its place in the package. The code always comes from the package,
// never from the agent's workspace.
func Stage(p *project.Project, ws, dir string) (string, error) {
	if len(p.Manifest.Existing) == 0 {
		return dir, Assemble(p, ws, dir)
	}
	root, rel, err := verify.PackageRoot(p.Dir)
	if err != nil {
		return "", err
	}
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	if err := verify.StagePackage(root, p.Manifest.Existing, dir); err != nil {
		return "", err
	}
	proj := filepath.Join(dir, filepath.FromSlash(rel))
	return proj, Assemble(p, ws, proj)
}

// seedExisting copies the code a project checks into the agent's workspace,
// under existing/, for it to read.
func seedExisting(p *project.Project, ws string) error {
	root, _, err := verify.PackageRoot(p.Dir)
	if err != nil {
		return err
	}
	return verify.StagePackage(root, p.Manifest.Existing, filepath.Join(ws, "existing"))
}

// Tampered lists the protected files the agent changed in its workspace.
func Tampered(p *project.Project, ws string) ([]string, error) {
	var changed []string
	for _, rel := range protected(p.Manifest.Language) {
		want, errWant := os.ReadFile(filepath.Join(p.Dir, rel))
		got, errGot := regular.ReadFile(ws, rel)
		if os.IsNotExist(errWant) && os.IsNotExist(errGot) {
			continue
		}
		if errWant != nil || errGot != nil || !bytes.Equal(want, got) {
			if _, ok := agentParameters(p, ws); rel == manifestFile && ok {
				continue // only the code's parameters changed, which are the agent's
			}
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
	if fb := r.Fallback; fb != nil {
		fmt.Fprintf(&b, "The loop guard stopped the agent's first run, so it ran once more, at %s effort (D-0125). The first run's error: %s\n\n", fb.Effort, fb.Why)
	}
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
