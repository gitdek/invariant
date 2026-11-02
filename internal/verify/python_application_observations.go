package verify

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/gitdek/invariant/internal/project"
)

// PythonApplicationObservation is raw read-back, sampled or explored,
// without a model verdict, assertion record or coverage claim.
type PythonApplicationObservation struct {
	Form         string                         `json:"form,omitempty"`
	Kind         string                         `json:"kind"`
	Run          int                            `json:"run"`
	Instance     int                            `json:"instance"`
	Sequence     int                            `json:"sequence"`
	Key          PythonApplicationInvocationKey `json:"key"`
	Target       string                         `json:"target"`
	Driver       string                         `json:"driver"`
	SourceSHA256 string                         `json:"source_sha256"`
	DriverSHA256 string                         `json:"driver_sha256"`
	Symbol       string                         `json:"symbol"`
	Entry        string                         `json:"entry"`
	EntrySHA256  string                         `json:"entry_sha256"`
	Readback     map[string]string              `json:"readback"`
	State        map[string]any                 `json:"state,omitempty"`
	Before       map[string]any                 `json:"before,omitempty"`
	After        map[string]any                 `json:"after,omitempty"`
	// An explored driver's rows also say which start a run began from, and
	// which model step and arguments each step tried, whether it replayed
	// an earlier attempt and whether the selected target was called (#342).
	// An omitted row records a step the environment's bounds rule out.
	Start   *int                      `json:"start,omitempty"`
	Step    string                    `json:"step,omitempty"`
	Args    []any                     `json:"args,omitempty"`
	Replay  *bool                     `json:"replay,omitempty"`
	Called  *bool                     `json:"called,omitempty"`
	Outcome string                    `json:"outcome,omitempty"`
	Refusal *PythonApplicationRefusal `json:"refusal,omitempty"`
}

// PythonApplicationRefusal records only the exception class and attributes
// the driver declared, never arbitrary exception messages or objects.
type PythonApplicationRefusal struct {
	Type       string         `json:"type"`
	Attributes map[string]any `json:"attributes"`
}

func pythonApplicationRefusal(raw []byte) (*PythonApplicationRefusal, error) {
	if _, err := pythonApplicationJSONFields(raw, true, "type", "attributes"); err != nil {
		return nil, err
	}
	var refusal PythonApplicationRefusal
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&refusal); err != nil {
		return nil, err
	}
	if !strings.Contains(refusal.Type, ".") || strings.IndexFunc(refusal.Type, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 || refusal.Attributes == nil {
		return nil, fmt.Errorf("invalid refusal exception descriptor")
	}
	for _, part := range strings.Split(refusal.Type, ".") {
		if part == "" {
			return nil, fmt.Errorf("invalid refusal exception descriptor")
		}
	}
	for _, value := range refusal.Attributes {
		if !pythonApplicationValue(value, false) {
			return nil, fmt.Errorf("refusal attributes must be finite JSON scalars")
		}
	}
	return &refusal, nil
}

func (s *PythonApplicationSession) freshObserver() error {
	b, err := os.ReadFile(filepath.Join(s.runtime, "invariant_observe.py"))
	if err != nil || pythonApplicationHash(b) != s.provenance.ObserverSHA256 {
		return fmt.Errorf("Python application observer changed")
	}
	return nil
}

var pythonApplicationModulePath = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*|\[([0-9]+|\*)\])*$`)

func pythonApplicationValue(value any, module bool) bool {
	switch v := value.(type) {
	case nil, string, bool:
		return true
	case json.Number:
		if !json.Valid([]byte(v)) {
			return false
		}
		if !strings.ContainsAny(string(v), ".eE") {
			return true
		}
		n, err := strconv.ParseFloat(string(v), 64)
		return err == nil && !math.IsInf(n, 0) && !math.IsNaN(n)
	case []any:
		if !module {
			return false
		}
		for _, item := range v {
			if !pythonApplicationValue(item, true) {
				return false
			}
		}
		return true
	case map[string]any:
		if !module {
			return false
		}
		for _, item := range v {
			if !pythonApplicationValue(item, true) {
				return false
			}
		}
		return true
	}
	return false
}

func pythonApplicationState(state map[string]any, reader map[string]string, module bool) bool {
	if len(state) != len(reader) || len(state) == 0 {
		return false
	}
	for key, value := range state {
		if reader[key] == "" {
			return false
		}
		if !pythonApplicationValue(value, module) {
			return false
		}
	}
	return true
}

// observeTransitions accepts an authenticated collector stream for c. check
// and drivers are the declarations of the driver this execution ran, which
// a larger run's copy keeps with its own hash. An explored stream is also
// held to the exploration harness's protocol (#342).
func (s *PythonApplicationSession) observeTransitions(c PythonApplicationCommand, execution string, authentication, events []byte, r *PythonApplicationExecutionResult, check project.Component, drivers []project.Component, explored bool) (err error) {
	// No prefix of a bad stream is evidence, even when a caller uses the
	// collector directly rather than through the execution session.
	defer func() {
		if err != nil {
			r.Observations, r.Exploration, r.Complete = nil, nil, false
		}
	}()
	if len(events) > pythonApplicationEventsLimit {
		return fmt.Errorf("Python application observations exceed limit")
	}
	// Authenticate the exact ordered stream before accepting any of its rows.
	// The footer is transport only, like the execution nonce in begin.
	if len(events) == 0 || events[len(events)-1] != '\n' {
		return fmt.Errorf("missing Python application collector authentication")
	}
	end := bytes.LastIndexByte(events[:len(events)-1], '\n') + 1
	footer := events[end : len(events)-1]
	if _, err := pythonApplicationJSON(footer, "type", "hmac_sha256"); err != nil {
		return fmt.Errorf("invalid Python application collector authentication: %w", err)
	}
	var seal struct {
		Type string `json:"type"`
		MAC  string `json:"hmac_sha256"`
	}
	if err := json.Unmarshal(footer, &seal); err != nil {
		return err
	}
	mac, err := hex.DecodeString(seal.MAC)
	h := hmac.New(sha256.New, authentication)
	h.Write(events[:end])
	if err != nil || len(authentication) != 32 || seal.Type != "authentication" || !hmac.Equal(mac, h.Sum(nil)) {
		return fmt.Errorf("Python application collector authentication mismatch")
	}
	events = events[:end]
	begun, ended := false, false
	runs := map[int]PythonApplicationObservation{}
	instanceModules := map[int]string{}
	instanceForms := map[int]string{}
	maxInstance := 0
	runLimit, _ := strconv.Atoi(c.Sampling["runs"])
	stepLimit, _ := strconv.Atoi(c.Sampling["steps"])
	var explorer *pythonApplicationExplorer
	if explored {
		explorer = &pythonApplicationExplorer{}
	}
	for len(events) > 0 {
		n := bytes.IndexByte(events, '\n')
		if n < 0 {
			return fmt.Errorf("truncated Python application observation")
		}
		line := events[:n]
		events = events[n+1:]
		var event struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &event); err != nil {
			return err
		}
		if ended {
			return fmt.Errorf("observation follows end")
		}
		switch event.Type {
		case "begin":
			fields, err := pythonApplicationJSON(line, "type", "version", "key", "snapshot_sha256", "runner_sha256", "execution")
			if err != nil {
				return err
			}
			if _, err := pythonApplicationJSON(fields["key"], "target", "check", "kind", "id"); err != nil {
				return err
			}
			var b struct {
				Version   int                            `json:"version"`
				Key       PythonApplicationInvocationKey `json:"key"`
				Snapshot  string                         `json:"snapshot_sha256"`
				Runner    string                         `json:"runner_sha256"`
				Execution string                         `json:"execution"`
			}
			if err := json.Unmarshal(line, &b); err != nil {
				return err
			}
			if begun || b.Version != 2 || b.Key != c.Key || b.Snapshot != c.SnapshotSHA256 || b.Runner != c.RunnerSHA256 || b.Execution != execution {
				return fmt.Errorf("foreign, replayed or duplicate observation begin")
			}
			begun = true
		case "exploration":
			if !begun || explorer == nil || explorer.declared || len(r.Observations) > 0 {
				return fmt.Errorf("exploration steps outside an explored invocation's start")
			}
			if err := explorer.declare(line); err != nil {
				return err
			}
		case "observation":
			if !begun {
				return fmt.Errorf("observation before begin")
			}
			if explorer != nil && !explorer.declared {
				return fmt.Errorf("observation before the exploration's steps")
			}
			var row PythonApplicationObservation
			d := json.NewDecoder(bytes.NewReader(line))
			d.UseNumber()
			if err := d.Decode(&row); err != nil {
				return err
			}
			required := []string{"type", "kind", "run", "instance", "sequence", "key", "target", "driver", "source_sha256", "driver_sha256", "symbol", "readback"}
			if row.Kind != "omitted" {
				required = append(required, "entry", "entry_sha256")
			}
			if row.Form != "" {
				if row.Form != "module" {
					return fmt.Errorf("unknown observation form")
				}
				required = append(required, "form")
			}
			switch row.Kind {
			case "initial":
				required = append(required, "state")
				if explored {
					required = append(required, "start")
				}
			case "transition":
				required = append(required, "before", "after")
				if explored {
					required = append(required, "step", "args", "replay", "called")
					if explorer.refusals[row.Step] != nil {
						required = append(required, "outcome")
						if row.Outcome == "refused" {
							required = append(required, "refusal")
						}
					}
				}
			case "omitted":
				if !explored {
					return fmt.Errorf("unknown observation kind")
				}
				required = append(required, "state", "step", "args")
			default:
				return fmt.Errorf("unknown observation kind")
			}
			fields, err := pythonApplicationJSONFields(line, true, required...)
			if err != nil {
				return err
			}
			for key, value := range fields {
				if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
					return fmt.Errorf("null observation field %s", key)
				}
			}
			if raw, ok := fields["refusal"]; ok {
				refusal, err := pythonApplicationRefusal(raw)
				if err != nil {
					return err
				}
				row.Refusal = refusal
			}
			if _, err := pythonApplicationJSON(fields["key"], "target", "check", "kind", "id"); err != nil {
				return err
			}
			target, driver := s.declarations[c.Key.Target], check
			if s.legacy {
				for _, t := range s.targets {
					if t.ID == row.Target {
						target = t
					}
				}
				for _, d := range drivers {
					if d.ID == row.Driver {
						driver = d
					}
				}
			}
			if row.Key != c.Key || target.ID == "" || driver.ID == "" || row.Target != target.ID || row.Driver != driver.ID || row.SourceSHA256 != target.SourceSHA256 || row.DriverSHA256 != driver.SourceSHA256 || row.Symbol != target.Symbol {
				return fmt.Errorf("foreign observation binding")
			}
			isModule := row.Form == "module"
			// An omitted step calls no application entry.
			if row.Kind != "omitted" {
				// Entries may live outside production scope; the Python collector
				// validates their lexical declarations against the full staged tree.
				prefix, source, ok := strings.Cut(row.Entry, "@")
				module, symbol, named := strings.Cut(prefix, "#")
				file, staged := s.snapshot[source]
				if !ok || !named || !staged || !strings.HasSuffix(source, ".py") || module != "python:"+strings.ReplaceAll(strings.TrimSuffix(source, ".py"), "/", ".") || !pythonApplicationName.MatchString(symbol) || row.EntrySHA256 != file.hash || source == driver.Source {
					return fmt.Errorf("unknown or substituted application entry")
				}
				if isModule && strings.Contains(symbol, ".") {
					return fmt.Errorf("module observations require module functions")
				}
			}
			functional := false
			if isModule && strings.Contains(row.Symbol, ".") {
				return fmt.Errorf("module observations require module functions")
			}
			for key, reader := range row.Readback {
				if key == "" || reader == "" {
					return fmt.Errorf("empty read-back binding")
				}
				if isModule {
					if !pythonApplicationModulePath.MatchString(reader) {
						return fmt.Errorf("invalid module read-back path")
					}
				} else if reader == "$return" {
					functional = true
				} else if !pythonApplicationName.MatchString(reader) || strings.Contains(reader, ".") {
					return fmt.Errorf("invalid attribute reader")
				}
			}
			if functional && len(row.Readback) != 1 {
				return fmt.Errorf("invalid functional reader")
			}
			previous, exists := runs[row.Run]
			if row.Kind == "initial" {
				if exists || row.Run != len(runs)+1 || row.Run > runLimit || row.Instance < 1 || row.Instance > maxInstance+1 || row.Sequence != 0 || !pythonApplicationState(row.State, row.Readback, isModule) || (!functional && row.Entry != row.Target) || (functional && row.Entry == row.Target) {
					return fmt.Errorf("invalid initial observation")
				}
				if form, seen := instanceForms[row.Instance]; seen && form != row.Form {
					return fmt.Errorf("instance changed observation form")
				}
				instanceForms[row.Instance] = row.Form
				if isModule {
					if source, seen := instanceModules[row.Instance]; seen && source != target.Source {
						return fmt.Errorf("module instance changed source")
					}
					instanceModules[row.Instance] = target.Source
				}
				if row.Instance > maxInstance {
					maxInstance = row.Instance
				}
			} else {
				state := previous.State
				if previous.Kind == "transition" {
					state = previous.After
				}
				if !exists || previous.Kind == "omitted" || row.Form != previous.Form || row.Target != previous.Target || row.Driver != previous.Driver || row.Instance != previous.Instance || !maps.Equal(row.Readback, previous.Readback) {
					return fmt.Errorf("invalid transition or state continuity")
				}
				if row.Kind == "omitted" {
					// An omission ends its run in the state it reached.
					if row.Sequence != previous.Sequence || !pythonApplicationState(row.State, row.Readback, isModule) || !reflect.DeepEqual(row.State, state) {
						return fmt.Errorf("invalid omitted step or state continuity")
					}
				} else if row.Sequence != previous.Sequence+1 || row.Sequence > stepLimit || !pythonApplicationState(row.Before, row.Readback, isModule) || !pythonApplicationState(row.After, row.Readback, isModule) || !reflect.DeepEqual(row.Before, state) || row.Entry == row.Target {
					return fmt.Errorf("invalid transition or state continuity")
				}
			}
			if explorer != nil {
				if err := explorer.observe(row); err != nil {
					return err
				}
			}
			runs[row.Run] = row
			r.Observations = append(r.Observations, row)
		case "end":
			if _, err := pythonApplicationJSON(line, "type", "count"); err != nil {
				return err
			}
			var e struct {
				Count int `json:"count"`
			}
			if err := json.Unmarshal(line, &e); err != nil {
				return err
			}
			if !begun || e.Count != len(r.Observations) || len(runs) == 0 {
				return fmt.Errorf("missing or miscounted observations")
			}
			for _, row := range runs {
				if row.Sequence == 0 && row.Kind != "omitted" {
					return fmt.Errorf("empty lifecycle")
				}
			}
			if explorer != nil {
				x, err := explorer.finish()
				if err != nil {
					return err
				}
				r.Exploration = x
			}
			ended = true
		default:
			return fmt.Errorf("unknown Python application observation event")
		}
	}
	if !ended {
		return fmt.Errorf("missing Python application observation end")
	}
	r.Complete = true
	return nil
}
