package tlc

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/gitdek/invariant/internal/tla"
)

type ReplayState map[string]ReplayValue

type ReplaySpec struct {
	Module, Bug, Expect, ExpectKind string
	Config                          Config
}

type ReplayStep struct {
	From   int           `json:"from"`
	To     int           `json:"to"`
	Action string        `json:"action"`
	Args   []ReplayValue `json:"args"`
	Bug    bool          `json:"bug"`
}

type ReplayTool struct {
	Image     string `json:"image"`
	JarSHA256 string `json:"jar_sha256"`
}

// ReplayPath owns the complete fresh counterexample and the exact inputs that
// established every interpretation. Offending is the first bug edge, even
// when the expected violation appears later in the retained suffix.
type ReplayPath struct {
	Version        int               `json:"version"`
	ID             string            `json:"id"`
	Module         string            `json:"module"`
	Bug            string            `json:"bug"`
	BugPin         string            `json:"bug_pin"`
	Expect         string            `json:"expect"`
	ExpectKind     string            `json:"expect_kind"`
	Specification  string            `json:"specification"`
	ModelSHA256    string            `json:"model_sha256"`
	IncludesSHA256 map[string]string `json:"includes_sha256"`
	Bounds         map[string]string `json:"bounds"`
	Tool           ReplayTool        `json:"tool"`
	States         []ReplayState     `json:"states"`
	Steps          []ReplayStep      `json:"steps"`
	Offending      int               `json:"offending"`
	Loop           int               `json:"loop,omitempty"`
	Stutters       bool              `json:"stutters,omitempty"`
}

func replayHash(v any) string {
	b, _ := json.Marshal(v)
	return replayBytesHash(b)
}

func replayBytesHash(b []byte) string { return fmt.Sprintf("sha256:%x", sha256.Sum256(b)) }

func (p ReplayPath) identity() string {
	p.ID = ""
	return replayHash(p)
}

// Validate refuses incomplete paths and edited content identities. It does
// not make a supplied path trusted execution evidence: the gate constructs
// its own paths and never accepts replacement paths from a driver.
func (p ReplayPath) Validate() error {
	if p.Version != 1 || p.ID == "" || p.ID != p.identity() || !isIdent(p.Module) || !isIdent(p.Bug) ||
		!isIdent(p.Expect) || !isIdent(p.Specification) || p.BugPin == "" || p.ExpectKind != "invariant" ||
		p.ModelSHA256 == "" || p.Tool.Image == "" || p.Tool.JarSHA256 == "" || len(p.Bounds) == 0 ||
		p.Loop != 0 || p.Stutters || len(p.States) < 2 || len(p.Steps) != len(p.States)-1 {
		return fmt.Errorf("replay-invalid-path: missing, stale or unsupported path binding")
	}
	for name, value := range p.Bounds {
		if !isIdent(name) {
			return fmt.Errorf("replay-invalid-path: invalid bound %q", name)
		}
		if _, err := ParseReplayValue(value); err != nil {
			return fmt.Errorf("replay-invalid-path: bound %s: %w", name, err)
		}
	}
	for name, hash := range p.IncludesSHA256 {
		if !tla.ValidIncludeName(name, p.Module) || hash == "" {
			return fmt.Errorf("replay-invalid-path: invalid include binding %q", name)
		}
	}
	vars := replayVariables(p.States[0])
	if len(vars) == 0 {
		return fmt.Errorf("replay-invalid-path: no state variables")
	}
	for _, s := range p.States {
		if _, err := replayStateText(s, vars); err != nil {
			return err
		}
	}
	first := -1
	for i, s := range p.Steps {
		if s.From != i || s.To != i+1 || !isIdent(s.Action) {
			return fmt.Errorf("replay-invalid-path: unordered or malformed edge %d", i)
		}
		for _, v := range s.Args {
			if _, err := v.TLA(); err != nil {
				return err
			}
		}
		if s.Bug && first == -1 {
			first = i
		}
	}
	if first < 0 || p.Offending != first {
		return fmt.Errorf("replay-invalid-path: missing offending operation")
	}
	return nil
}

type replaySnapshot struct {
	dir      string
	model    *tla.Model
	source   string
	includes map[string]string
	runner   Runner
	tool     ReplayTool
}

func (r Runner) replaySnapshot(dir, module string) (*replaySnapshot, error) {
	if !isIdent(module) {
		return nil, fmt.Errorf("invalid replay module")
	}
	src, err := os.ReadFile(filepath.Join(dir, module+".tla"))
	if err != nil {
		return nil, err
	}
	includes, err := tla.Siblings(dir, module+".tla")
	if err != nil {
		return nil, err
	}
	m, err := tla.Read(string(src), includes)
	if err != nil {
		return nil, err
	}
	if m.Name() != module {
		return nil, fmt.Errorf("replay module name differs from its file")
	}
	// Generated names must not capture anything in the checked model.
	for _, body := range append([]string{string(src)}, replayIncludeTexts(includes)...) {
		if strings.Contains(body, "Invariant_Replay") || strings.Contains(body, "invariant_replay_") {
			return nil, fmt.Errorf("model uses reserved replay names")
		}
	}
	work, err := os.MkdirTemp("", "invariant-replay-")
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(work)
		}
	}()
	if err = os.WriteFile(filepath.Join(work, module+".tla"), src, 0600); err != nil {
		return nil, err
	}
	for name, body := range includes {
		if !tla.ValidIncludeName(name, module) {
			return nil, fmt.Errorf("invalid replay include %q", name)
		}
		if err = os.WriteFile(filepath.Join(work, name), []byte(body), 0600); err != nil {
			return nil, err
		}
	}
	jar := filepath.Join(work, "tla2tools.jar")
	sum, err := snapshotGraphJar(r.Jar, jar)
	if err != nil {
		return nil, err
	}
	r.Jar = jar
	ok = true
	return &replaySnapshot{dir: work, model: m, source: string(src), includes: includes, runner: r, tool: ReplayTool{Image: r.Image, JarSHA256: sum}}, nil
}

func replayIncludeTexts(includes map[string]string) []string {
	var out []string
	for _, body := range includes {
		out = append(out, body)
	}
	return out
}

func (s *replaySnapshot) hashes() (string, map[string]string) {
	includes := map[string]string{}
	for name, body := range s.includes {
		includes[name] = replayBytesHash([]byte(body))
	}
	return replayBytesHash([]byte(s.source)), includes
}

// ExtractReplay checks the exact retained trace, including Init, every edge,
// and the final expected violation. The labels TLC printed are never used.
func (r Runner) ExtractReplay(ctx context.Context, dir string, spec ReplaySpec, trace Result) (ReplayPath, error) {
	p := ReplayPath{Loop: trace.Loop, Stutters: trace.Stutters, Offending: -1}
	if spec.ExpectKind == "property" || trace.Outcome == PropertyViolated || trace.Loop != 0 || trace.Stutters {
		return p, fmt.Errorf("replay-unsupported-temporal: the retained infinite behavior cannot be checked by executable replay version 1")
	}
	if spec.ExpectKind != "invariant" || trace.Outcome != Violated || trace.Invariant != spec.Expect || len(trace.Trace) < 2 ||
		!isIdent(spec.Bug) || !isIdent(spec.Expect) || !isIdent(spec.Config.Specification) {
		return p, fmt.Errorf("replay-invalid-counterexample: expected a complete %s violation", spec.Expect)
	}
	s, err := r.replaySnapshot(dir, spec.Module)
	if err != nil {
		return p, err
	}
	defer os.RemoveAll(s.dir)
	if err = s.model.ReplaySpecification(spec.Config.Specification); err != nil {
		return p, err
	}
	normal, buggy, err := s.model.ReplayActions(spec.Bug)
	if err != nil {
		return p, err
	}
	vars := s.model.Variables()
	sort.Strings(vars)
	if len(vars) == 0 {
		return p, fmt.Errorf("replay has no state variables")
	}
	for i, state := range trace.Trace {
		if state.Index != i+1 {
			return ReplayPath{}, fmt.Errorf("replay-invalid-counterexample: state indexes are not consecutive")
		}
		values := ReplayState{}
		for _, v := range state.Vars {
			if _, exists := values[v.Name]; exists {
				return ReplayPath{}, fmt.Errorf("replay-invalid-counterexample: duplicate variable %s", v.Name)
			}
			x, err := ParseReplayValue(v.Value)
			if err != nil {
				return ReplayPath{}, err
			}
			values[v.Name] = x
		}
		if _, err := replayStateText(values, vars); err != nil {
			return ReplayPath{}, err
		}
		p.States = append(p.States, values)
	}
	p.Version, p.Module, p.Bug, p.Expect, p.ExpectKind = 1, spec.Module, spec.Bug, spec.Expect, spec.ExpectKind
	p.Specification, p.Bounds, p.Tool = spec.Config.Specification, maps.Clone(spec.Config.Constants), s.tool
	p.ModelSHA256, p.IncludesSHA256 = s.hashes()
	p.BugPin, err = s.model.PinHash(spec.Bug, map[string]bool{"Init": true, "Next": true})
	if err != nil {
		return ReplayPath{}, err
	}
	header, constants, err := replayHeader(spec.Module, p.Bounds, p.States)
	if err != nil {
		return ReplayPath{}, err
	}
	states, err := replayStatesText(p.States, vars)
	if err != nil {
		return ReplayPath{}, err
	}
	target := replayIs("Invariant_ReplayStates[invariant_replay_i + 1]", vars, true)
	candidates := "(" + tla.ReplayCandidates(normal, false, target) + ") \\cup (" + tla.ReplayCandidates(buggy, true, target) + ")"
	all := "<<" + strings.Join(append(append([]string(nil), vars...), "invariant_replay_i"), ", ") + ">>"
	body := "---- MODULE Invariant_ReplayPath ----\n" + header + "VARIABLE invariant_replay_i\n" +
		"Invariant_ReplayStates == " + states + "\n" +
		"Invariant_ReplayInit == \\E invariant_replay_j \\in 1..Len(Invariant_ReplayStates) :\n" +
		"  invariant_replay_i = invariant_replay_j /\\ " + replayIs("Invariant_ReplayStates[invariant_replay_j]", vars, false) + "\n" +
		"Invariant_ReplaySpec == Invariant_ReplayInit /\\ [][UNCHANGED " + all + "]_" + all + "\n" +
		"Invariant_ReplayCandidates == " + candidates + "\n" +
		"Invariant_ReplayChecked ==\n" +
		"  /\\ (invariant_replay_i = 1 => Init)\n" +
		"  /\\ IF invariant_replay_i = Len(Invariant_ReplayStates) THEN ~(" + spec.Expect + ")\n" +
		"     ELSE /\\ " + spec.Expect + "\n" +
		"          /\\ ENABLED ((Next \\/ " + spec.Bug + ") /\\ (" + target + "))\n" +
		"          /\\ Cardinality(Invariant_ReplayCandidates) = 1\n" +
		"          /\\ PrintT(<<\"invariant-replay-v1\", invariant_replay_i - 1, Invariant_ReplayCandidates>>)\n====\n"
	res, err := s.check(ctx, "Invariant_ReplayPath", body, Config{Specification: "Invariant_ReplaySpec", Constants: constants, Invariants: []string{"Invariant_ReplayChecked"}})
	if err != nil {
		return ReplayPath{}, err
	}
	p.Steps, err = replayEdges(res.Printed, len(p.States)-1, normal, buggy)
	if err != nil {
		return ReplayPath{}, err
	}
	for i, step := range p.Steps {
		if step.Bug {
			p.Offending = i
			break
		}
	}
	p.ID = p.identity()
	if err = p.Validate(); err != nil {
		return ReplayPath{}, err
	}
	return p, nil
}

func (s *replaySnapshot) check(ctx context.Context, module, body string, cfg Config) (Result, error) {
	if err := os.WriteFile(filepath.Join(s.dir, module+".tla"), []byte(body), 0600); err != nil {
		return Result{}, err
	}
	res, err := s.runner.Check(ctx, s.dir, module, cfg)
	if err != nil {
		return Result{}, err
	}
	if res.Outcome != Passed {
		return Result{}, fmt.Errorf("replay-check-failed: %s: %s %s", module, res.Outcome, res.Message)
	}
	return res, nil
}

var replayRow = regexp.MustCompile(`(?m)^[\t ]*<<\s*"invariant-replay-v1"\s*,`)

func replayEdges(printed []string, count int, normal, buggy []tla.Step) ([]ReplayStep, error) {
	steps := make([]ReplayStep, count)
	seen := make([]bool, count)
	// TLC may wrap a printed value over several lines. Parse complete values
	// from each printed block, retaining duplicate rows as errors.
	for _, block := range printed {
		p := replayValueParser{valueParser: valueParser{s: block}}
		for {
			loc := replayRow.FindStringIndex(p.s[p.i:])
			if loc == nil {
				break
			}
			p.i += loc[0]
			start := p.i
			v, err := p.value(0)
			if err != nil {
				return nil, fmt.Errorf("replay-malformed-edge: %w", err)
			}
			if p.i == start {
				return nil, fmt.Errorf("replay-malformed-edge")
			}
			v, err = v.canonical(0)
			if err != nil {
				return nil, err
			}
			if v.Kind != "sequence" || len(v.Elements) != 3 || v.Elements[0].Kind != "string" || v.Elements[0].Atom != "invariant-replay-v1" || v.Elements[1].Kind != "integer" {
				return nil, fmt.Errorf("replay-malformed-edge: unexpected printed evidence")
			}
			i, err := strconv.Atoi(v.Elements[1].Atom)
			if err != nil || i < 0 || i >= count || seen[i] {
				return nil, fmt.Errorf("replay-malformed-edge: duplicate or invalid index")
			}
			set := v.Elements[2]
			if set.Kind != "set" || len(set.Elements) != 1 {
				return nil, fmt.Errorf("replay-ambiguous-edge: edge %d has no unique interpretation", i)
			}
			x := set.Elements[0]
			if x.Kind != "sequence" || len(x.Elements) != 3 || x.Elements[0].Kind != "string" || !isIdent(x.Elements[0].Atom) || x.Elements[1].Kind != "sequence" || x.Elements[2].Kind != "bool" {
				return nil, fmt.Errorf("replay-malformed-edge: invalid action tuple")
			}
			step := ReplayStep{From: i, To: i + 1, Action: x.Elements[0].Atom, Args: x.Elements[1].Elements, Bug: x.Elements[2].Atom == "TRUE"}
			allowed := normal
			if step.Bug {
				allowed = buggy
			}
			found := false
			for _, action := range allowed {
				if action.Name == step.Action && len(action.Args) == len(step.Args) {
					found = true
				}
			}
			if !found {
				return nil, fmt.Errorf("replay-malformed-edge: action is absent from the checked model")
			}
			steps[i], seen[i] = step, true
		}
	}
	for i, ok := range seen {
		if !ok {
			return nil, fmt.Errorf("replay-missing-edge: %d", i)
		}
	}
	return steps, nil
}

func replayVariables(s ReplayState) []string {
	var out []string
	for k := range s {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func replayStateText(state ReplayState, vars []string) (string, error) {
	if len(state) != len(vars) {
		return "", fmt.Errorf("replay-invalid-state: variables differ from the model")
	}
	x := make([]string, len(vars))
	for i, name := range vars {
		v, ok := state[name]
		if !ok || !isIdent(name) {
			return "", fmt.Errorf("replay-invalid-state: missing or invalid variable %s", name)
		}
		value, err := v.TLA()
		if err != nil {
			return "", err
		}
		x[i] = name + " |-> " + value
	}
	return "[" + strings.Join(x, ", ") + "]", nil
}

func replayStatesText(states []ReplayState, vars []string) (string, error) {
	x := make([]string, len(states))
	for i, s := range states {
		text, err := replayStateText(s, vars)
		if err != nil {
			return "", err
		}
		x[i] = text
	}
	return "<<" + strings.Join(x, ",\n  ") + ">>", nil
}

func replayIs(record string, vars []string, primed bool) string {
	x := make([]string, len(vars))
	prime := ""
	if primed {
		prime = "'"
	}
	for i, name := range vars {
		x[i] = name + prime + " = (" + record + ")." + name
	}
	return strings.Join(x, " /\\ ")
}

func replayHeader(module string, bounds map[string]string, states []ReplayState) (string, map[string]string, error) {
	constants := maps.Clone(bounds)
	if constants == nil {
		return "", nil, fmt.Errorf("replay has no ratified bounds")
	}
	values := map[string]bool{}
	var visit func(ReplayValue)
	visit = func(v ReplayValue) {
		if v.Kind == "model" {
			values[v.Atom] = true
		}
		for _, e := range v.Elements {
			visit(e)
		}
		for _, e := range v.Entries {
			visit(e.Key)
			visit(e.Value)
		}
	}
	for name, text := range bounds {
		if !isIdent(name) {
			return "", nil, fmt.Errorf("invalid replay bound %q", name)
		}
		v, err := ParseReplayValue(text)
		if err != nil {
			return "", nil, fmt.Errorf("replay bound %s: %w", name, err)
		}
		visit(v)
	}
	for _, s := range states {
		for _, v := range s {
			visit(v)
		}
	}
	names := []string{}
	for name := range values {
		if bound, exists := constants[name]; exists {
			if bound != name {
				return "", nil, fmt.Errorf("model value collides with bound %s", name)
			}
			continue
		}
		constants[name] = name
		names = append(names, name)
	}
	sort.Strings(names)
	header := "EXTENDS " + module + ", TLC, Sequences, FiniteSets\n"
	if len(names) > 0 {
		header += "CONSTANTS " + strings.Join(names, ", ") + "\n"
	}
	return header, constants, nil
}

// CheckReplayAlternative checks a normally completed operation against the
// same snapshot, bounds and tools. A refusal must leave state unchanged; the
// specification permits stuttering. A returned alternative must be a Next
// step (or stutter) and preserve the expected invariant.
func (r Runner) CheckReplayAlternative(ctx context.Context, dir string, p ReplayPath, before, after ReplayState, refused bool) error {
	if err := p.Validate(); err != nil {
		return err
	}
	s, err := r.replaySnapshot(dir, p.Module)
	if err != nil {
		return err
	}
	defer os.RemoveAll(s.dir)
	model, includes := s.hashes()
	if model != p.ModelSHA256 || !maps.Equal(includes, p.IncludesSHA256) || s.tool != p.Tool {
		return fmt.Errorf("replay-stale-input: model, includes or tool changed")
	}
	if refused && !reflect.DeepEqual(before, after) {
		return fmt.Errorf("replay-invalid-refusal: state changed")
	}
	vars := s.model.Variables()
	sort.Strings(vars)
	states := []ReplayState{before, after}
	text, err := replayStatesText(states, vars)
	if err != nil {
		return err
	}
	header, constants, err := replayHeader(p.Module, p.Bounds, states)
	if err != nil {
		return err
	}
	all := "<<" + strings.Join(append(append([]string(nil), vars...), "invariant_replay_i"), ", ") + ">>"
	body := "---- MODULE Invariant_ReplayResult ----\n" + header + "VARIABLE invariant_replay_i\n" +
		"Invariant_ReplayStates == " + text + "\n" +
		"Invariant_ReplayInit == \\E invariant_replay_j \\in 1..2 : invariant_replay_i = invariant_replay_j /\\ " + replayIs("Invariant_ReplayStates[invariant_replay_j]", vars, false) + "\n" +
		"Invariant_ReplaySpec == Invariant_ReplayInit /\\ [][UNCHANGED " + all + "]_" + all + "\n" +
		"Invariant_ReplayAllowed == " + p.Expect + " /\\ (invariant_replay_i = 1 => ENABLED ((Next \\/ UNCHANGED <<" + strings.Join(vars, ", ") + ">>) /\\ " + replayIs("Invariant_ReplayStates[2]", vars, true) + "))\n====\n"
	_, err = s.check(ctx, "Invariant_ReplayResult", body, Config{Specification: "Invariant_ReplaySpec", Constants: constants, Invariants: []string{"Invariant_ReplayAllowed"}})
	return err
}
