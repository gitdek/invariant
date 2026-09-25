// Package verify runs every check the gate requires on one project and
// reports what each check established.
package verify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gitdek/invariant/internal/conformance"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/toolchain"
)

// Report is everything one verification established. Receipts render it,
// and every value in it comes from tool output or the lock file.
type Report struct {
	Project   string            `json:"project"`
	Passed    bool              `json:"passed"`
	Decision  string            `json:"decision"`
	Bounds    map[string]string `json:"bounds"`
	Pins      []Pin             `json:"pins"`
	Design    Design            `json:"design"`
	Witnesses []Witness         `json:"witnesses"`
	Bugs      []Bug             `json:"bugs"`
	// Code-level evidence: agreement for a state machine explored from its
	// initial state, conformance for code run by a driver, and a proof when
	// the language has a verifier for the code.
	Agreement   *Agreement          `json:"agreement,omitempty"`
	Conformance *conformance.Result `json:"conformance,omitempty"`
	Code        *Code               `json:"code,omitempty"`
	// Assurance says how the code was checked: "proved", or "tested against
	// the model". A receipt never blurs the two (D-0024).
	Assurance   string    `json:"assurance"`
	Build       Build     `json:"build"`
	Toolchain   Toolchain `json:"toolchain"`
	Fingerprint string    `json:"fingerprint"`
	GeneratedAt string    `json:"generated_at"`
}

// Pin compares a ratified statement's pinned hash with its text today,
// including everything the statement depends on.
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
	Passed          bool        `json:"passed"`
	Outcome         string      `json:"outcome"`
	Spec            string      `json:"spec"`
	Invariants      []string    `json:"invariants"`
	Violated        string      `json:"violated,omitempty"`
	Message         string      `json:"message,omitempty"`
	DistinctStates  int64       `json:"distinct_states"`
	StatesGenerated int64       `json:"states_generated"`
	Depth           int         `json:"depth"`
	Trace           []tlc.State `json:"trace,omitempty"` // the counterexample, when there is one
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

// Bug shows the invariants catch a ratified bug: with the bug's action added
// to Next, TLC must find the expected invariant violated.
type Bug struct {
	Name     string `json:"name"`  // the bug's TLA+ action
	Label    string `json:"label"` // the same name in kebab case, for files and receipts
	Says     string `json:"says"`
	Expect   string `json:"expect"`
	Caught   bool   `json:"caught"`
	Outcome  string `json:"outcome"`
	Violated string `json:"violated,omitempty"`
	Steps    int    `json:"steps,omitempty"`
	Trace    string `json:"trace,omitempty"` // file under traces/, when written
	Message  string `json:"message,omitempty"`
}

// Agreement compares the implementation's state space with the model's:
// exploring the code from its initial state must reach exactly the states TLC
// found, at the same depth.
type Agreement struct {
	Passed     bool   `json:"passed"`
	States     int64  `json:"states"`
	Depth      int    `json:"depth"`
	WantStates int64  `json:"want_states"`
	WantDepth  int    `json:"want_depth"`
	Message    string `json:"message,omitempty"`
}

// Code is the code-level verifier's result.
type Code struct {
	Verifier   string   `json:"verifier"`
	Passed     bool     `json:"passed"`
	Functions  []string `json:"functions"`
	Contracts  []string `json:"contracts"`
	Unverified []string `json:"unverified,omitempty"` // functions the verifier never saw
	Overflow   bool     `json:"overflow_checked"`
	Errors     []string `json:"errors,omitempty"`
}

// Build is the language's own static checks and tests, such as go vet and
// go test, run in a sandbox.
type Build struct {
	Passed bool        `json:"passed"`
	Steps  []BuildStep `json:"steps"`
	Output string      `json:"output,omitempty"`
}

// BuildStep is one build or test command.
type BuildStep struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
}

// Toolchain records exactly what did the checking.
type Toolchain struct {
	TLC          string `json:"tlc"`
	TLCRelease   string `json:"tlc_release"`
	TLCJarSHA256 string `json:"tlc_jar_sha256"`
	JavaImage    string `json:"java_image"`
	GobraImage   string `json:"gobra_image,omitempty"`
	GoImage      string `json:"go_image,omitempty"`
	NodeImage    string `json:"node_image,omitempty"`
	PythonImage  string `json:"python_image,omitempty"`
	Go           string `json:"go,omitempty"`
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
	lang, err := languageFor(p, tc)
	if err != nil {
		return nil, err
	}
	if name, err := tla.ModuleName(src); err != nil || name != p.ModuleName() {
		return nil, fmt.Errorf("%s: the MODULE header must name %s", p.Manifest.Module, p.ModuleName())
	}
	work, err := os.MkdirTemp("", "invariant-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)

	r := &Report{
		Project:   p.Manifest.Name,
		Decision:  p.Lock.Decision,
		Bounds:    p.Lock.Bounds,
		Pins:      checkPins(src, p.Lock.Statements),
		Toolchain: Toolchain{TLCRelease: toolchain.TLCRelease, TLCJarSHA256: toolchain.TLCJarSHA256, JavaImage: tc.JavaImage},
	}
	switch lang.(type) {
	case Go:
		r.Toolchain.GobraImage, r.Toolchain.GoImage, r.Toolchain.Go = tc.GobraImage, tc.GoImage, goVersion(ctx)
	case TypeScript:
		r.Toolchain.NodeImage = tc.NodeImage
	case Python:
		r.Toolchain.PythonImage = tc.PythonImage
	}
	runner := tlc.Runner{Image: tc.JavaImage, Jar: tc.TLCJar}
	cfg := tlc.Config{Specification: p.SpecName(), Constants: p.Lock.Bounds, Invariants: p.Invariants()}
	witnesses, bugs := p.Of(project.Witness), p.Of(project.Bug)
	r.Witnesses = make([]Witness, len(witnesses))
	r.Bugs = make([]Bug, len(bugs))
	var evidence Evidence

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
			Passed: res.Outcome == tlc.Passed, Outcome: string(res.Outcome), Spec: cfg.Specification,
			Invariants: cfg.Invariants, Violated: res.Invariant, Message: res.Message,
			DistinctStates: res.DistinctStates, StatesGenerated: res.StatesGenerated, Depth: res.Depth,
			Trace: res.Trace,
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

	for i, b := range bugs {
		spawn(func() error {
			out := Bug{Name: b.Name, Label: kebab(b.Name), Says: b.Says, Expect: b.Expect}
			defer func() { r.Bugs[i] = out }()
			// The bug runs alongside the model: every behavior the model allows,
			// plus the bug's own steps.
			name := "Bug_" + b.Name
			wrapper := fmt.Sprintf("---- MODULE %s ----\nEXTENDS %s\nInvariant_BuggySpec == Init /\\ [][Next \\/ %s]_vars\n====\n", name, p.ModuleName(), b.Name)
			d, err := stage(work, name, p, src, wrapper)
			if err != nil {
				return err
			}
			res, err := runner.Check(ctx, d, name, tlc.Config{Specification: "Invariant_BuggySpec", Constants: cfg.Constants, Invariants: cfg.Invariants})
			if err != nil {
				return err
			}
			out.Outcome, out.Violated = string(res.Outcome), res.Invariant
			if res.Outcome == tlc.Violated && res.Invariant == b.Expect {
				out.Caught, out.Steps = true, len(res.Trace)-1
			} else {
				out.Message = "expected " + b.Expect + " to be violated; " + describe(res)
			}
			if outDir != "" && len(res.Trace) > 0 {
				out.Trace = out.Label + ".json"
				return writeTrace(filepath.Join(outDir, "traces", out.Trace), res, p.ModuleName(), "known bug "+out.Label)
			}
			return nil
		})
	}

	spawn(func() (err error) {
		r.Code, err = lang.Verify(ctx, p.CodeDir())
		return err
	})

	spawn(func() (err error) {
		r.Build, evidence, err = lang.Check(ctx, p)
		return err
	})

	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if evidence.Exploration != nil {
		a := agree(r.Design, *evidence.Exploration)
		r.Agreement = &a
	} else {
		c := conformance.Result{ModelStates: r.Design.DistinctStates, Message: evidence.Message}
		if evidence.Traces != nil {
			d, err := stage(work, "conformance", p, src, "")
			if err != nil {
				return nil, err
			}
			if c, err = conformance.Check(ctx, runner, d, p.ModuleName(), p.Lock.Bounds, tla.Variables(src), r.Design.DistinctStates, evidence.Traces); err != nil {
				return nil, err
			}
		}
		r.Conformance = &c
	}
	r.Assurance = "tested against the model"
	if r.Code != nil {
		r.Assurance = "proved"
	}
	r.Passed = r.Design.Passed && r.Build.Passed && (r.Agreement != nil || r.Conformance != nil)
	if r.Agreement != nil {
		r.Passed = r.Passed && r.Agreement.Passed
	}
	if r.Conformance != nil {
		r.Passed = r.Passed && r.Conformance.Passed
	}
	if r.Code != nil {
		r.Passed = r.Passed && r.Code.Passed
	}
	for _, pin := range r.Pins {
		r.Passed = r.Passed && pin.Match
	}
	for _, w := range r.Witnesses {
		r.Passed = r.Passed && w.Reached
	}
	for _, b := range r.Bugs {
		r.Passed = r.Passed && b.Caught
	}
	r.Fingerprint = Fingerprint(*r)
	r.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	return r, nil
}

// agree compares the implementation's exploration with TLC's.
func agree(d Design, e Exploration) Agreement {
	a := Agreement{States: e.States, Depth: e.Depth, WantStates: d.DistinctStates, WantDepth: d.Depth}
	switch {
	case !e.OK:
		a.Message = e.Message
	case !d.Passed:
		a.Message = "TLC didn't finish checking the model, so there's no state space to compare against"
	case e.States != d.DistinctStates || e.Depth != d.Depth:
		a.Message = fmt.Sprintf("the implementation reaches %d states in %d levels; the model reaches %d in %d",
			e.States, e.Depth, d.DistinctStates, d.Depth)
	default:
		a.Passed = true
	}
	return a
}

// Fingerprint hashes what a report certifies: the statements, bounds,
// results and verifier pins. It leaves out timestamps, test timings, the Go
// toolchain's patch version and where traces were written, so a local run
// and a CI run of the same commit have the same fingerprint.
func Fingerprint(r Report) string {
	r.Fingerprint, r.GeneratedAt, r.Build.Output, r.Toolchain.Go = "", "", "", ""
	r.Bugs = append([]Bug(nil), r.Bugs...)
	for i := range r.Bugs {
		r.Bugs[i].Trace = ""
	}
	b, _ := json.Marshal(r)
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func checkPins(src string, statements []project.Statement) []Pin {
	pins := make([]Pin, 0, len(statements))
	for _, s := range statements {
		pin := Pin{Name: s.Name, Kind: s.Kind, Says: s.Says, Want: s.SHA256}
		if h, err := tla.PinHash(src, s.Name, project.Model); err != nil {
			pin.Error = err.Error()
		} else {
			pin.Got = h
			pin.Match = pin.Got == pin.Want
		}
		pins = append(pins, pin)
	}
	return pins
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

var wordStart = regexp.MustCompile(`([a-z0-9])([A-Z])`)

// kebab turns EarlyCommit into early-commit.
func kebab(name string) string {
	return strings.ToLower(wordStart.ReplaceAllString(name, "$1-$2"))
}
