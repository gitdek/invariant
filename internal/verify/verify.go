// Package verify runs every check the gate requires on one project and
// reports what each check established.
package verify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gitdek/invariant/internal/gobra"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/toolchain"
)

// Report is everything one verification established. Receipts render it,
// and every value in it comes from tool output or the lock file.
type Report struct {
	Project     string            `json:"project"`
	Passed      bool              `json:"passed"`
	Decision    string            `json:"decision"`
	Bounds      map[string]string `json:"bounds"`
	Pins        []Pin             `json:"pins"`
	Design      Design            `json:"design"`
	Witnesses   []Witness         `json:"witnesses"`
	Mutants     []Mutant          `json:"mutants"`
	Code        Code              `json:"code"`
	Build       Build             `json:"build"`
	Toolchain   Toolchain         `json:"toolchain"`
	Fingerprint string            `json:"fingerprint"`
	GeneratedAt string            `json:"generated_at"`
}

// Pin compares a ratified statement's pinned hash with its text today.
type Pin struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Says  string `json:"says"`
	Want  string `json:"want"`
	Got   string `json:"got,omitempty"`
	Match bool   `json:"match"`
	Error string `json:"error,omitempty"`
}

// Design is TLC's check of every ratified invariant within the bounds.
type Design struct {
	Passed          bool     `json:"passed"`
	Outcome         string   `json:"outcome"`
	Invariants      []string `json:"invariants"`
	Violated        string   `json:"violated,omitempty"`
	Message         string   `json:"message,omitempty"`
	DistinctStates  int64    `json:"distinct_states"`
	StatesGenerated int64    `json:"states_generated"`
	Depth           int      `json:"depth"`
}

// Witness shows a ratified witness is reachable, so the invariants aren't
// holding only because the model can't do anything.
type Witness struct {
	Name    string `json:"name"`
	Says    string `json:"says"`
	Reached bool   `json:"reached"`
	Steps   int    `json:"steps,omitempty"` // the shortest behavior that reaches it
	Message string `json:"message,omitempty"`
}

// Mutant shows the invariants catch a known bug.
type Mutant struct {
	Name     string `json:"name"`
	Says     string `json:"says"`
	Expect   string `json:"expect"`
	Caught   bool   `json:"caught"`
	Outcome  string `json:"outcome"`
	Violated string `json:"violated,omitempty"`
	Steps    int    `json:"steps,omitempty"`
	Trace    string `json:"trace,omitempty"` // file under traces/, when written
	Message  string `json:"message,omitempty"`
}

// Code is the code-level verifier's result.
type Code struct {
	Verifier  string   `json:"verifier"`
	Passed    bool     `json:"passed"`
	Functions []string `json:"functions"`
	Contracts []string `json:"contracts"`
	Overflow  bool     `json:"overflow_checked"`
	Errors    []string `json:"errors,omitempty"`
}

// Build is go vet and go test on the implementation.
type Build struct {
	Passed bool   `json:"passed"`
	Vet    bool   `json:"vet"`
	Test   bool   `json:"test"`
	Output string `json:"output,omitempty"`
}

// Toolchain records exactly what did the checking.
type Toolchain struct {
	TLC          string `json:"tlc"`
	TLCRelease   string `json:"tlc_release"`
	TLCJarSHA256 string `json:"tlc_jar_sha256"`
	JavaImage    string `json:"java_image"`
	GobraImage   string `json:"gobra_image"`
	Go           string `json:"go"`
}

// Run verifies the project in dir. When outDir isn't empty, it writes each
// counterexample to outDir/traces as JSON.
func Run(ctx context.Context, dir, outDir string, tc toolchain.Toolchain) (*Report, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	p, err := project.Load(dir)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(p.ModulePath())
	if err != nil {
		return nil, err
	}
	src := string(raw)
	if name, err := tla.ModuleName(src); err != nil || name != p.ModuleName() {
		return nil, fmt.Errorf("%s: the MODULE header must name %s", p.Manifest.Module, p.ModuleName())
	}
	work, err := os.MkdirTemp("", "invariant-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)

	r := &Report{
		Project:  p.Manifest.Name,
		Decision: p.Lock.Decision,
		Bounds:   p.Lock.Bounds,
		Pins:     checkPins(src, p.Lock.Statements),
		Toolchain: Toolchain{
			TLCRelease: toolchain.TLCRelease, TLCJarSHA256: toolchain.TLCJarSHA256,
			JavaImage: tc.JavaImage, GobraImage: tc.GobraImage, Go: goVersion(ctx),
		},
	}
	runner := tlc.Runner{Image: tc.JavaImage, Jar: tc.TLCJar}
	cfg := tlc.Config{Specification: p.Manifest.Specification, Constants: p.Lock.Bounds, Invariants: p.Invariants()}

	var witnesses []project.Statement
	for _, s := range p.Lock.Statements {
		if s.Kind == project.Witness {
			witnesses = append(witnesses, s)
		}
	}
	r.Witnesses = make([]Witness, len(witnesses))
	r.Mutants = make([]Mutant, len(p.Lock.Mutants))

	// Every check is independent, so they all run at once. Each goroutine
	// writes only its own part of the report.
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)
	spawn := func(check func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := check(); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}()
	}

	spawn(func() error {
		d, err := stage(work, "design", p, src, "")
		if err != nil {
			return err
		}
		res, err := runner.Check(ctx, d, p.ModuleName(), cfg)
		if err != nil {
			return err
		}
		r.Toolchain.TLC = res.Version
		r.Design = Design{
			Passed: res.Outcome == tlc.Passed, Outcome: string(res.Outcome), Invariants: cfg.Invariants,
			Violated: res.Invariant, Message: res.Message,
			DistinctStates: res.DistinctStates, StatesGenerated: res.StatesGenerated, Depth: res.Depth,
		}
		return nil
	})

	for i, w := range witnesses {
		spawn(func() error {
			name, never := "Witness_"+w.Name, "Invariant_Never_"+w.Name
			wrapper := fmt.Sprintf("---- MODULE %s ----\nEXTENDS %s\n%s == ~%s\n====\n", name, p.ModuleName(), never, w.Name)
			d, err := stage(work, name, p, src, wrapper)
			if err != nil {
				return err
			}
			res, err := runner.Check(ctx, d, name, tlc.Config{Specification: cfg.Specification, Constants: cfg.Constants, Invariants: []string{never}})
			if err != nil {
				return err
			}
			out := Witness{Name: w.Name, Says: w.Says}
			switch {
			case res.Outcome == tlc.Violated && res.Invariant == never:
				out.Reached, out.Steps = true, len(res.Trace)-1
			case res.Outcome == tlc.Passed:
				out.Message = "no reachable state satisfies it, so the invariants may hold vacuously"
			default:
				out.Message = describe(res)
			}
			r.Witnesses[i] = out
			return nil
		})
	}

	for i, m := range p.Lock.Mutants {
		spawn(func() error {
			out := Mutant{Name: m.Name, Says: m.Says, Expect: m.Expect}
			defer func() { r.Mutants[i] = out }()
			mutated, err := tla.Mutate(src, m.Replace, m.With)
			if err != nil {
				out.Message = err.Error()
				return nil
			}
			if changed := changedStatements(src, mutated, p.Lock.Statements); len(changed) > 0 {
				out.Message = "the mutation changes ratified statements (" + strings.Join(changed, ", ") + "); a mutant may only change the model"
				return nil
			}
			d, err := stage(work, "mutant-"+m.Name, p, mutated, "")
			if err != nil {
				return err
			}
			res, err := runner.Check(ctx, d, p.ModuleName(), cfg)
			if err != nil {
				return err
			}
			out.Outcome, out.Violated = string(res.Outcome), res.Invariant
			if res.Outcome == tlc.Violated && res.Invariant == m.Expect {
				out.Caught, out.Steps = true, len(res.Trace)-1
			} else {
				out.Message = "expected " + m.Expect + " to be violated; " + describe(res)
			}
			if outDir != "" && len(res.Trace) > 0 {
				out.Trace = m.Name + ".json"
				return writeTrace(filepath.Join(outDir, "traces", out.Trace), res, p.ModuleName(), "mutant "+m.Name)
			}
			return nil
		})
	}

	spawn(func() error {
		res, err := gobra.Run(ctx, tc.GobraImage, p.CodeDir(), true)
		if err != nil {
			return err
		}
		r.Code = Code{Verifier: "Gobra", Passed: res.Passed, Functions: res.Functions, Contracts: res.Contracts, Overflow: res.Overflow, Errors: res.Errors}
		return nil
	})

	spawn(func() error {
		vetOut, vetErr := goCmd(ctx, p.CodeDir(), "vet", ".")
		testOut, testErr := goCmd(ctx, p.CodeDir(), "test", "-count=1", ".")
		r.Build = Build{Vet: vetErr == nil, Test: testErr == nil, Output: strings.TrimSpace(vetOut + testOut)}
		r.Build.Passed = r.Build.Vet && r.Build.Test
		return nil
	})

	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	r.Passed = r.Design.Passed && r.Code.Passed && r.Build.Passed
	for _, pin := range r.Pins {
		r.Passed = r.Passed && pin.Match
	}
	for _, w := range r.Witnesses {
		r.Passed = r.Passed && w.Reached
	}
	for _, m := range r.Mutants {
		r.Passed = r.Passed && m.Caught
	}
	r.Fingerprint = Fingerprint(*r)
	r.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	return r, nil
}

// Fingerprint hashes what a report certifies: the statements, bounds,
// results and verifier pins. It leaves out timestamps, test timings and the
// Go toolchain's patch version, so a local run and a CI run of the same
// commit have the same fingerprint.
func Fingerprint(r Report) string {
	r.Fingerprint, r.GeneratedAt, r.Build.Output, r.Toolchain.Go = "", "", "", ""
	b, _ := json.Marshal(r)
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func checkPins(src string, statements []project.Statement) []Pin {
	pins := make([]Pin, 0, len(statements))
	for _, s := range statements {
		pin := Pin{Name: s.Name, Kind: s.Kind, Says: s.Says, Want: s.SHA256}
		if def, err := tla.Definition(src, s.Name); err != nil {
			pin.Error = err.Error()
		} else {
			pin.Got = tla.Hash(def)
			pin.Match = pin.Got == pin.Want
		}
		pins = append(pins, pin)
	}
	return pins
}

// changedStatements names the ratified statements whose text differs
// between two versions of a module.
func changedStatements(before, after string, statements []project.Statement) []string {
	var changed []string
	for _, s := range statements {
		a, errA := tla.Definition(before, s.Name)
		b, errB := tla.Definition(after, s.Name)
		if errA != nil || errB != nil || tla.Hash(a) != tla.Hash(b) {
			changed = append(changed, s.Name)
		}
	}
	return changed
}

// stage copies the project's TLA+ modules into a fresh directory, with the
// main module replaced by src and, when wrapper isn't empty, a wrapper
// module added.
func stage(work, name string, p *project.Project, src, wrapper string) (string, error) {
	d := filepath.Join(work, name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", err
	}
	specDir := filepath.Dir(p.ModulePath())
	entries, err := os.ReadDir(specDir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tla") {
			b, err := os.ReadFile(filepath.Join(specDir, e.Name()))
			if err != nil {
				return "", err
			}
			if err := os.WriteFile(filepath.Join(d, e.Name()), b, 0o644); err != nil {
				return "", err
			}
		}
	}
	if err := os.WriteFile(filepath.Join(d, p.ModuleName()+".tla"), []byte(src), 0o644); err != nil {
		return "", err
	}
	if wrapper != "" {
		wrapperName, _ := tla.ModuleName(wrapper)
		return d, os.WriteFile(filepath.Join(d, wrapperName+".tla"), []byte(wrapper), 0o644)
	}
	return d, nil
}

func writeTrace(path string, res tlc.Result, module, check string) error {
	f, err := res.TraceFile(module, check)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func describe(res tlc.Result) string {
	switch res.Outcome {
	case tlc.Violated:
		return "TLC found " + res.Invariant + " violated"
	case tlc.Deadlock:
		return "TLC found a deadlock"
	case tlc.Passed:
		return "TLC found no violation"
	default:
		return "TLC failed: " + res.Message
	}
}

func goCmd(ctx context.Context, dir string, args ...string) (string, error) {
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, &out, &out
	err := cmd.Run()
	return out.String(), err
}

func goVersion(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "go", "env", "GOVERSION").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}
