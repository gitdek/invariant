package verify

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
)

// PythonApplicationObservation is raw sampled read-back, without a model
// verdict, assertion record or coverage claim.
type PythonApplicationObservation struct {
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
}

func (s *PythonApplicationSession) freshObserver() error {
	b, err := os.ReadFile(filepath.Join(s.runtime, "invariant_observe.py"))
	if err != nil || pythonApplicationHash(b) != s.provenance.ObserverSHA256 {
		return fmt.Errorf("Python application observer changed")
	}
	return nil
}

func pythonApplicationState(state map[string]any, reader map[string]string) bool {
	if len(state) != len(reader) || len(state) == 0 {
		return false
	}
	for key, value := range state {
		if reader[key] == "" {
			return false
		}
		switch value.(type) {
		case nil, string, bool, json.Number:
		default:
			return false
		}
	}
	return true
}

func (s *PythonApplicationSession) observeTransitions(c PythonApplicationCommand, execution string, authentication, events []byte, r *PythonApplicationExecutionResult) error {
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
	maxInstance := 0
	runLimit, _ := strconv.Atoi(c.Sampling["runs"])
	stepLimit, _ := strconv.Atoi(c.Sampling["steps"])
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
		case "observation":
			if !begun {
				return fmt.Errorf("observation before begin")
			}
			var row PythonApplicationObservation
			d := json.NewDecoder(bytes.NewReader(line))
			d.UseNumber()
			if err := d.Decode(&row); err != nil {
				return err
			}
			required := []string{"type", "kind", "run", "instance", "sequence", "key", "target", "driver", "source_sha256", "driver_sha256", "symbol", "entry", "entry_sha256", "readback"}
			switch row.Kind {
			case "initial":
				required = append(required, "state")
			case "transition":
				required = append(required, "before", "after")
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
			if _, err := pythonApplicationJSON(fields["key"], "target", "check", "kind", "id"); err != nil {
				return err
			}
			target, driver := s.declarations[c.Key.Target], s.declarations[c.Key.Check]
			if s.legacy {
				for _, t := range s.targets {
					if t.ID == row.Target {
						target = t
					}
				}
				for _, d := range s.drivers {
					if d.ID == row.Driver {
						driver = d
					}
				}
			}
			if row.Key != c.Key || target.ID == "" || driver.ID == "" || row.Target != target.ID || row.Driver != driver.ID || row.SourceSHA256 != target.SourceSHA256 || row.DriverSHA256 != driver.SourceSHA256 || row.Symbol != target.Symbol {
				return fmt.Errorf("foreign observation binding")
			}
			// Entries may live outside production scope; the Python collector
			// validates their lexical declarations against the full staged tree.
			prefix, source, ok := strings.Cut(row.Entry, "@")
			module, symbol, named := strings.Cut(prefix, "#")
			file, staged := s.snapshot[source]
			if !ok || !named || !staged || !strings.HasSuffix(source, ".py") || module != "python:"+strings.ReplaceAll(strings.TrimSuffix(source, ".py"), "/", ".") || !pythonApplicationName.MatchString(symbol) || row.EntrySHA256 != file.hash || source == driver.Source {
				return fmt.Errorf("unknown or substituted application entry")
			}
			functional := false
			for key, reader := range row.Readback {
				if key == "" || reader == "" {
					return fmt.Errorf("empty read-back binding")
				}
				if reader == "$return" {
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
				if exists || row.Run != len(runs)+1 || row.Run > runLimit || row.Instance < 1 || row.Instance > maxInstance+1 || row.Sequence != 0 || !pythonApplicationState(row.State, row.Readback) || (!functional && row.Entry != row.Target) || (functional && row.Entry == row.Target) {
					return fmt.Errorf("invalid initial observation")
				}
				if row.Instance > maxInstance {
					maxInstance = row.Instance
				}
			} else {
				state := previous.State
				if previous.Kind == "transition" {
					state = previous.After
				}
				if !exists || row.Instance != previous.Instance || row.Sequence != previous.Sequence+1 || row.Sequence > stepLimit || !maps.Equal(row.Readback, previous.Readback) || !pythonApplicationState(row.Before, row.Readback) || !pythonApplicationState(row.After, row.Readback) || !reflect.DeepEqual(row.Before, state) || row.Entry == row.Target {
					return fmt.Errorf("invalid transition or state continuity")
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
				if row.Sequence == 0 {
					return fmt.Errorf("empty lifecycle")
				}
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
