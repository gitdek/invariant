package verify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/regular"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/tlc"
)

// Reader is explanatory data, with an identity independent of gate evidence.
// Exactly one of Chart, Limitation or Unavailable is populated.
type Reader struct {
	Version     int                    `json:"version"`
	Fingerprint string                 `json:"fingerprint"`
	Inputs      ReaderInputs           `json:"inputs"`
	Chart       *statechart.Result     `json:"chart,omitempty"`
	Limitation  *statechart.Limitation `json:"limitation,omitempty"`
	Unavailable *ReaderDiagnostic      `json:"unavailable,omitempty"`
}

// Keep integer trace values exact when a saved receipt is read back.
func (r *Reader) UnmarshalJSON(data []byte) error {
	type wire Reader
	var decoded wire
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := d.Decode(&decoded); err != nil {
		return err
	}
	*r = Reader(decoded)
	return nil
}

type ReaderInputs struct {
	Module      string            `json:"module"`
	ModelSHA256 string            `json:"model_sha256"`
	ViewSHA256  string            `json:"view_sha256"`
	Includes    map[string]string `json:"includes"`
}

type ReaderDiagnostic struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type readerSnapshot struct {
	inputs      ReaderInputs
	view        []byte
	unavailable *ReaderDiagnostic
}

func readerHash(b []byte) string { return fmt.Sprintf("sha256:%x", sha256.Sum256(b)) }

// Snapshot before starting any checks. Only the model path changes in this
// internal project copy; code, proofs and ratification still use their roots.
func snapshotReader(work string, p *project.Project, src string) (*project.Project, readerSnapshot, error) {
	s := readerSnapshot{inputs: ReaderInputs{Module: p.ModuleName(), ModelSHA256: readerHash([]byte(src)), Includes: map[string]string{}}}
	var err error
	s.view, err = regular.ReadFile(p.Dir, ".invariant/view.json")
	if err != nil {
		code, message := "unreadable_view", err.Error()
		if os.IsNotExist(err) {
			code, message = "missing_view", "No reader chart: supply .invariant/view.json and verify again."
		}
		s.unavailable = &ReaderDiagnostic{Code: code, Message: message}
	} else {
		s.inputs.ViewSHA256 = readerHash(s.view)
	}
	// Without a readable saved view there is no reader computation. Keep
	// legacy staging and its operational errors unchanged.
	if s.unavailable != nil {
		return p, s, nil
	}
	dir, err := stage(work, "model-inputs", p, src, "")
	if err != nil {
		return nil, s, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, s, err
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == p.ModuleName()+".tla" || !strings.HasSuffix(name, ".tla") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, s, err
		}
		s.inputs.Includes[name] = readerHash(b)
	}
	model := *p
	model.Manifest.Module, err = filepath.Rel(p.Dir, filepath.Join(dir, p.ModuleName()+".tla"))
	return &model, s, err
}

func (s readerSnapshot) compute(ctx context.Context, runner tlc.Runner, p *project.Project, report *Report) *Reader {
	r := &Reader{Version: 1, Inputs: s.inputs, Unavailable: s.unavailable}
	// Input digests bind exact source bytes; the lock and complete computed
	// contents bind bounds, wording, tool/configuration provenance and topology.
	defer func() {
		b, err := json.Marshal(struct {
			Reader *Reader      `json:"reader"`
			Lock   project.Lock `json:"lock"`
		}{r, p.Lock})
		if err != nil {
			r.Chart, r.Limitation = nil, nil
			r.Unavailable = &ReaderDiagnostic{Code: "reader_identity", Message: err.Error()}
			return
		}
		r.Fingerprint = readerHash(b)
	}()
	if r.Unavailable != nil {
		return r
	}
	fail := func(code string, err error) *Reader {
		r.Chart, r.Limitation = nil, nil
		var refusal *statechart.Refusal
		if errors.As(err, &refusal) {
			code = refusal.Code
		}
		r.Unavailable = &ReaderDiagnostic{Code: code, Message: err.Error()}
		return r
	}
	if !report.Design.Passed {
		return fail("incomplete_model", fmt.Errorf("the design check did not complete successfully; no reader chart is available"))
	}
	var temporal []tlc.TraceFile
	for _, property := range report.Properties {
		if len(property.Behavior) == 0 {
			continue
		}
		result := tlc.Result{Property: property.Name, Version: report.Toolchain.TLC, Trace: property.Behavior, Loop: property.Loop, Stutters: property.Stutters}
		trace, err := result.TraceFile(p.ModuleName(), property.Name)
		if err != nil {
			return fail("reader_trace", err)
		}
		temporal = append(temporal, trace)
	}
	var err error
	r.Chart, r.Limitation, err = statechart.ReadModel(ctx, runner, filepath.Dir(p.ModulePath()), p.ModuleName(), p.Lock, s.view, report.Design.DistinctStates, temporal)
	if err != nil {
		return fail("reader_failed", err)
	}
	if r.Chart != nil {
		if _, err := r.Chart.SVG(); err != nil {
			return fail("render_failed", err)
		}
	}
	return r
}
