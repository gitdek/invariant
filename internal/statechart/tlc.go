package statechart

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/tlc"
)

// GraphDumper is the existing graph runner boundary. The runner retains its
// admission budget, resource limits, isolation and joined cancellation cleanup.
type GraphDumper interface {
	DumpGraph(context.Context, string, string, tlc.Config) (*tlc.GraphResult, error)
}

// EvaluateRules asks TLC for every locked invariant's value at every supplied
// state. The caller supplies the same immutable module snapshot used to dump
// graph. Replay output supplies only the matrix; it never replaces that graph.
func EvaluateRules(ctx context.Context, runner GraphDumper, dir string, graph tlc.GraphResult, lock project.Lock) (matrix map[string]map[string]bool, err error) {
	defer func() {
		if cause := context.Cause(ctx); cause != nil {
			matrix, err = nil, cause
		}
	}()
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	states, err := validateGraph(ctx, graph)
	if err != nil {
		return nil, err
	}
	invariants, err := validateLock(graph, lock)
	if err != nil {
		return nil, err
	}
	src, err := os.ReadFile(filepath.Join(dir, graph.Module+".tla"))
	if err != nil {
		return nil, fmt.Errorf("statechart source: %w", err)
	}
	module, err := tla.ModuleName(string(src))
	if err != nil || module != graph.Module {
		return nil, refuse("invalid_evidence", "source module differs from graph")
	}
	for _, statement := range lock.Statements {
		pin, err := tla.PinHash(string(src), statement.Name, project.Model)
		if err != nil || pin != statement.SHA256 {
			return nil, refuse("invalid_evidence", "source does not match locked pin for "+statement.Name)
		}
	}
	variables := slices.Sorted(maps.Keys(graph.Graph.States[0].Values))
	declared := tla.Variables(string(src))
	for _, name := range declared {
		if !slices.Contains(variables, name) {
			return nil, refuse("invalid_evidence", "source variable missing from graph: "+name)
		}
	}
	values := map[string]map[string]string{}
	for _, id := range slices.Sorted(maps.Keys(states)) {
		s := states[id]
		values[id] = map[string]string{}
		for _, name := range variables {
			value, err := canonicalValue(s.Values[name])
			if err != nil {
				return nil, &Refusal{Code: "invalid_evidence", Message: "invalid TLA+ value for " + name + ": " + err.Error(), States: []string{s.ID}}
			}
			values[id][name] = value
		}
	}
	matrix = map[string]map[string]bool{}
	if len(invariants) == 0 {
		for id := range states {
			matrix[id] = map[string]bool{}
		}
		return matrix, nil
	}
	if runner == nil {
		return nil, refuse("invalid_evidence", "invariant evaluation requires a TLC graph runner")
	}
	f, err := os.CreateTemp(dir, "InvariantReader*.tla")
	if err != nil {
		return nil, err
	}
	defer func() {
		if cleanup := os.Remove(f.Name()); cleanup != nil {
			matrix, err = nil, errors.Join(err, cleanup)
		}
	}()
	replay := strings.TrimSuffix(filepath.Base(f.Name()), ".tla")
	fresh := func(suffix string) string {
		name := replay + suffix
		for strings.Contains(string(src), name) {
			name += "X"
		}
		return name
	}
	idVar, rulesVar := fresh("Identity"), fresh("Rules")
	init, stay, spec := fresh("Init"), fresh("Stay"), fresh("Spec")
	cfg := tlc.Config{Specification: spec, Constants: maps.Clone(graph.Config.Constants)}
	// TLC prints model values as bare names, which need declarations to be
	// expressions in a module. Private constants bind those exact values while
	// preserving every original constant and avoiding source-name collisions.
	modelValues := map[string]string{}
	for _, s := range states {
		for _, value := range s.Values {
			replaceModelValues(value, func(name string) string {
				modelValues[name] = ""
				return name
			})
		}
	}
	aliases := []string{}
	for i, name := range slices.Sorted(maps.Keys(modelValues)) {
		if cfg.Constants == nil {
			cfg.Constants = map[string]string{}
		}
		alias := fresh(fmt.Sprintf("Value%d", i))
		modelValues[name] = alias
		aliases = append(aliases, alias)
		cfg.Constants[alias] = name
	}
	allVars := append(slices.Clone(variables), idVar, rulesVar)
	tuple := "<<" + strings.Join(allVars, ", ") + ">>"
	fields := make([]string, len(invariants))
	for i, name := range invariants {
		fields[i] = name + " |-> " + name
	}
	var b strings.Builder
	// TLC supplies :> and @@ in the syntax TLC uses to print finite functions.
	fmt.Fprintf(&b, "---- MODULE %s ----\nEXTENDS %s, TLC\n", replay, graph.Module)
	if len(aliases) > 0 {
		fmt.Fprintf(&b, "CONSTANTS %s\n", strings.Join(aliases, ", "))
	}
	fmt.Fprintf(&b, "VARIABLES %s, %s\n%s ==\n", idVar, rulesVar, init)
	for _, id := range slices.Sorted(maps.Keys(states)) {
		b.WriteString("    \\/ /\\ " + idVar + " = " + strconv.Quote(id) + "\n")
		for _, name := range variables {
			value := replaceModelValues(states[id].Values[name], func(name string) string { return modelValues[name] })
			fmt.Fprintf(&b, "       /\\ %s = (%s)\n", name, value)
		}
		fmt.Fprintf(&b, "       /\\ %s = [%s]\n", rulesVar, strings.Join(fields, ", "))
	}
	fmt.Fprintf(&b, "%s == UNCHANGED %s\n%s == %s /\\ [][%s]_%s\n====\n", stay, tuple, spec, init, stay, tuple)
	_, writeErr := f.WriteString(b.String())
	if err := errors.Join(writeErr, f.Close()); err != nil {
		return nil, err
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	got, err := runner.DumpGraph(ctx, dir, replay, cloneConfig(cfg))
	if err != nil {
		return nil, fmt.Errorf("statechart invariant replay: %w", err)
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	if got == nil {
		return nil, refuse("invalid_evidence", "TLC returned no invariant replay")
	}
	if _, err := validateGraph(ctx, *got); err != nil {
		return nil, err
	}
	if got.Module != replay || got.Config.Specification != spec || !maps.Equal(got.Config.Constants, cfg.Constants) ||
		len(got.Config.Invariants) != 0 || len(got.Config.Properties) != 0 || got.Tool != graph.Tool || got.Run.Version != graph.Run.Version {
		return nil, refuse("invalid_evidence", "invariant replay configuration or tool provenance differs")
	}
	if got.StateCount != len(states) || len(got.Graph.Initial) != len(states) || got.StepCount != len(states) {
		return nil, refuse("invalid_evidence", "invariant replay must contain exactly one initial snapshot and unchanged step per concrete state")
	}
	loops := map[string]bool{}
	for _, step := range got.Graph.Steps {
		if step.From != step.To || loops[step.From] {
			return nil, refuse("invalid_evidence", "invariant replay has a changed or duplicate successor")
		}
		loops[step.From] = true
	}
	for _, s := range got.Graph.States {
		id, err := strconv.Unquote(s.Values[idVar])
		_, ok := states[id]
		if err != nil || !ok || matrix[id] != nil || len(s.Values) != len(variables)+2 {
			return nil, refuse("invalid_evidence", "missing, duplicate or unknown replay identity or variables")
		}
		for _, name := range variables {
			value, exists := s.Values[name]
			canonical, err := canonicalValue(value)
			if !exists || err != nil || canonical != values[id][name] {
				return nil, &Refusal{Code: "invalid_evidence", Message: "replay changed variable " + name, States: []string{id}}
			}
		}
		row, err := booleanRecord(s.Values[rulesVar], invariants)
		if err != nil {
			return nil, &Refusal{Code: "invalid_evidence", Message: err.Error(), States: []string{id}}
		}
		for _, name := range graph.Config.Invariants {
			if !row[name] {
				return nil, &Refusal{Code: "invalid_evidence", Message: "replay contradicts successful invariant check " + name, States: []string{id}}
			}
		}
		matrix[id] = row
	}
	return matrix, nil
}

var valueToken = regexp.MustCompile(`"(?:[^"\\]|\\.)*"|[A-Za-z_][A-Za-z0-9_]*`)

func replaceModelValues(value string, replace func(string) string) string {
	var b strings.Builder
	end := 0
	for _, loc := range valueToken.FindAllStringIndex(value, -1) {
		word := value[loc[0]:loc[1]]
		b.WriteString(value[end:loc[0]])
		if word[0] != '"' && word != "TRUE" && word != "FALSE" && !strings.HasPrefix(strings.TrimSpace(value[loc[1]:]), "|->") {
			word = replace(word)
		}
		b.WriteString(word)
		end = loc[1]
	}
	b.WriteString(value[end:])
	return b.String()
}

var booleanField = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*\|->\s*(TRUE|FALSE)$`)

// This is deliberately narrower than ParseValue: rule evidence is exactly a
// record of unique names and booleans, with no lossy conversion of duplicates.
func booleanRecord(text string, names []string) (map[string]bool, error) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "[") || !strings.HasSuffix(text, "]") {
		return nil, fmt.Errorf("invariant evidence is not a boolean record")
	}
	row := map[string]bool{}
	for _, field := range strings.Split(text[1:len(text)-1], ",") {
		m := booleanField.FindStringSubmatch(strings.TrimSpace(field))
		if m == nil || !slices.Contains(names, m[1]) {
			return nil, fmt.Errorf("unknown or nonboolean invariant field %q", field)
		}
		if _, exists := row[m[1]]; exists {
			return nil, fmt.Errorf("duplicate invariant field %s", m[1])
		}
		row[m[1]] = m[2] == "TRUE"
	}
	if len(row) != len(names) {
		return nil, fmt.Errorf("incomplete invariant record")
	}
	return row, nil
}

var replayToken = regexp.MustCompile(`"(?:[^"\\]|\\.)*"|<<|>>|\|->|:>|@@|\.\.|-?[0-9]+|[A-Za-z_][A-Za-z0-9_]*|[{}\[\](),]`)

// Replay can change TLC's model-value order and line wrapping. Compare typed
// values, sorting sets and mapping entries but retaining tuple order and every
// quoted byte. ParseValue's plain data loses these type distinctions. Intervals
// stay compact even when their cardinality is much larger than the state graph.
func canonicalValue(text string) (string, error) {
	p := replayValueParser{}
	end := 0
	for _, loc := range replayToken.FindAllStringIndex(text, -1) {
		if strings.TrimSpace(text[end:loc[0]]) != "" {
			return "", fmt.Errorf("invalid TLA+ value at offset %d", end)
		}
		p.tokens = append(p.tokens, text[loc[0]:loc[1]])
		end = loc[1]
	}
	if strings.TrimSpace(text[end:]) != "" {
		return "", fmt.Errorf("invalid TLA+ value at offset %d", end)
	}
	v, err := p.value()
	if err != nil {
		return "", err
	}
	if p.i != len(p.tokens) {
		return "", fmt.Errorf("unexpected value token %q", p.tokens[p.i])
	}
	return v, nil
}

type replayValueParser struct {
	tokens []string
	i      int
}

func (p *replayValueParser) take(token string) bool {
	if p.i < len(p.tokens) && p.tokens[p.i] == token {
		p.i++
		return true
	}
	return false
}

func (p *replayValueParser) value() (string, error) {
	if p.i == len(p.tokens) {
		return "", fmt.Errorf("expected a TLA+ value")
	}
	token := p.tokens[p.i]
	p.i++
	switch token {
	case "{", "<<":
		end := "}"
		if token == "<<" {
			end = ">>"
		}
		var members []string
		if !p.take(end) {
			for {
				v, err := p.value()
				if err != nil {
					return "", err
				}
				members = append(members, v)
				if p.take(end) {
					break
				}
				if !p.take(",") {
					return "", fmt.Errorf("expected , or %s", end)
				}
			}
		}
		if token == "{" {
			members = unique(members)
			// An enumerated integer set and TLC's interval notation denote the
			// same set. Normalize contiguous members without expanding ranges.
			numbers := make([]int64, 0, len(members))
			for _, member := range members {
				n, err := strconv.ParseInt(member, 10, 64)
				if err != nil {
					break
				}
				numbers = append(numbers, n)
			}
			if len(numbers) > 0 && len(numbers) == len(members) {
				slices.Sort(numbers)
				contiguous := true
				for i := 1; i < len(numbers); i++ {
					if numbers[i] != numbers[i-1]+1 {
						contiguous = false
					}
				}
				if contiguous {
					return fmt.Sprintf("%d..%d", numbers[0], numbers[len(numbers)-1]), nil
				}
			}
		}
		return token + strings.Join(members, ",") + end, nil
	case "[", "(":
		end, arrow, separator := "]", "|->", ","
		if token == "(" {
			end, arrow, separator = ")", ":>", "@@"
		} else if p.take(end) {
			return "[]", nil
		}
		entries := map[string]string{}
		for {
			var key string
			if token == "[" {
				if p.i == len(p.tokens) || !identifier.MatchString(p.tokens[p.i]) {
					return "", fmt.Errorf("expected record field")
				}
				key = p.tokens[p.i]
				p.i++
			} else {
				var err error
				key, err = p.value()
				if err != nil {
					return "", err
				}
			}
			if !p.take(arrow) {
				return "", fmt.Errorf("expected %s", arrow)
			}
			v, err := p.value()
			if err != nil {
				return "", err
			}
			if _, exists := entries[key]; exists {
				return "", fmt.Errorf("duplicate mapping key %s", key)
			}
			entries[key] = v
			if p.take(end) {
				break
			}
			if !p.take(separator) {
				return "", fmt.Errorf("expected %s or %s", separator, end)
			}
		}
		var members []string
		for _, key := range slices.Sorted(maps.Keys(entries)) {
			members = append(members, key+arrow+entries[key])
		}
		return token + strings.Join(members, separator) + end, nil
	}
	if token[0] == '"' || identifier.MatchString(token) {
		return token, nil
	}
	lo, err := strconv.ParseInt(token, 10, 64)
	if err != nil {
		return "", fmt.Errorf("expected a value, got %q", token)
	}
	if !p.take("..") {
		return strconv.FormatInt(lo, 10), nil
	}
	if p.i == len(p.tokens) {
		return "", fmt.Errorf("expected interval endpoint")
	}
	hi, err := strconv.ParseInt(p.tokens[p.i], 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid interval endpoint %q", p.tokens[p.i])
	}
	p.i++
	if hi < lo {
		return "{}", nil
	}
	return fmt.Sprintf("%d..%d", lo, hi), nil
}
