package verify

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/tlc"
)

// Reader explains the checked bounded model. Its identity and diagnostics
// never participate in the gate's verdict or verification fingerprint.
type Reader struct {
	Preview              statechart.Preview     `json:"preview,omitzero"`
	Version              int                    `json:"version"`
	Fingerprint          string                 `json:"fingerprint"`
	Inputs               ReaderInputs           `json:"inputs"`
	Chart                *statechart.Result     `json:"chart,omitempty"`
	Limitation           *statechart.Limitation `json:"limitation,omitempty"`
	Unavailable          *ReaderDiagnostic      `json:"unavailable,omitempty"`
	Comparison           *statechart.Comparison `json:"comparison,omitempty"`
	ComparisonDiagnostic *ReaderDiagnostic      `json:"comparison_diagnostic,omitempty"`
}

type ReaderInputs struct {
	Module   string            `json:"module"`
	Model    string            `json:"model_sha256"`
	View     string            `json:"view_sha256"`
	Includes map[string]string `json:"includes"`
}

type ReaderDiagnostic struct {
	Code    string             `json:"code"`
	Message string             `json:"message"`
	Source  *statechart.Source `json:"source,omitempty"`
}

type readerSnapshot struct {
	reader Reader
	view   []byte
	lock   project.Lock
	dir    string
}

type readerCompletionKey struct{}

// The application gate calls the model gate before checking code. Keep its
// snapshot alive and defer explanatory work until the entire gate finishes.
type readerCompletion struct {
	compute       func()
	cleanup       func()
	view          []byte
	viewErr       error
	comparison    []byte
	comparisonErr error
}

func completeReader(ctx context.Context, p *project.Project, gate func(context.Context) (*Report, error)) (*Report, error) {
	c := &readerCompletion{}
	// Validate the original file before application staging can turn a link
	// into a regular copy. Reader input errors remain explanatory only.
	c.view, c.viewErr = readReaderView(p.Dir)
	c.comparison, c.comparisonErr = readReaderFile(p.Dir, ".invariant/comparison.json")
	defer func() {
		if c.cleanup != nil {
			c.cleanup()
		}
	}()
	r, err := gate(context.WithValue(ctx, readerCompletionKey{}, c))
	if err == nil && r != nil && c.compute != nil {
		c.compute()
	}
	return r, err
}

func readerHash(data []byte) string { return fmt.Sprintf("sha256:%x", sha256.Sum256(data)) }

// Read the optional view before any checks start. The model and includes are
// already captured in dir, which supplies every subsequent TLC check.
func snapshotReader(p *project.Project, dir string, model []byte, completion *readerCompletion) readerSnapshot {
	s := readerSnapshot{dir: dir, lock: p.Lock, reader: Reader{Version: 1,
		Inputs: ReaderInputs{Module: p.ModuleName(), Model: readerHash(model), Includes: map[string]string{}}}}
	var comparison []byte
	var comparisonErr error
	if completion != nil {
		comparison, comparisonErr = completion.comparison, completion.comparisonErr
	} else {
		comparison, comparisonErr = readReaderFile(p.Dir, ".invariant/comparison.json")
	}
	s.reader.captureComparison(comparison, comparisonErr, p.Lock)
	entries, err := os.ReadDir(dir)
	if err != nil {
		s.reader.Unavailable = &ReaderDiagnostic{Code: "reader_inputs", Message: err.Error()}
		return s
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".tla") || entry.Name() == p.ModuleName()+".tla" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			s.reader.Unavailable = &ReaderDiagnostic{Code: "reader_inputs", Message: err.Error()}
			return s
		}
		s.reader.Inputs.Includes[entry.Name()] = readerHash(data)
	}
	if completion != nil {
		s.view, err = completion.view, completion.viewErr
	} else {
		s.view, err = readReaderView(p.Dir)
	}
	if err != nil {
		code, message := "reader_inputs", err.Error()
		if os.IsNotExist(err) {
			code, message = "missing_view", "No reader chart is available. Supply .invariant/view.json and verify again."
		}
		s.reader.Unavailable = &ReaderDiagnostic{Code: code, Message: message}
		return s
	}
	s.reader.Inputs.View = readerHash(s.view)
	return s
}

// Keep this fixed input inside the project, without following symbolic links
// or accepting hard links. The opened file must be the regular file inspected.
func readReaderView(dir string) ([]byte, error) {
	return readReaderFile(dir, ".invariant/view.json")
}

func readReaderFile(dir, name string) ([]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var last os.FileInfo
	for _, part := range []string{".invariant", name} {
		info, err := root.Lstat(part)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("reader view: %s is a symbolic link", part)
		}
		last = info
	}
	if !last.Mode().IsRegular() {
		return nil, fmt.Errorf("reader view: %s is not a regular file", name)
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || !os.SameFile(last, info) {
		return nil, fmt.Errorf("reader view: %s changed while opening it", name)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Nlink > 1 {
		return nil, fmt.Errorf("reader view: %s is a hard link", name)
	}
	return io.ReadAll(f)
}

func (s readerSnapshot) compute(ctx context.Context, runner tlc.Runner, design Design) *Reader {
	r := s.reader
	if r.Unavailable == nil && !design.Passed {
		r.Unavailable = &ReaderDiagnostic{Code: "model_check", Message: "No reader chart is available: the design check did not complete successfully."}
	}
	if r.Unavailable == nil {
		var err error
		r.Chart, r.Limitation, err = statechart.Read(ctx, runner, s.dir, r.Inputs.Module, s.lock, s.view, design.DistinctStates)
		if err != nil {
			code := "reader_computation"
			var refusal *statechart.Refusal
			if errors.As(err, &refusal) {
				code = refusal.Code
			}
			r.Unavailable = &ReaderDiagnostic{Code: code, Message: err.Error()}
			var readErr *statechart.ReadError
			if errors.As(err, &readErr) {
				r.Unavailable.Source = &readErr.Source
			}
		}
	}
	// Input hashes bind exact bytes; the rest binds bounds, says words, pins,
	// tool/configuration provenance and the complete computed contents.
	data, _ := json.Marshal(struct {
		Reader Reader       `json:"reader"`
		Lock   project.Lock `json:"lock"`
	}{r, s.lock})
	r.Fingerprint = readerHash(data)
	// Bind the computed chart and its tools even when rendering must replace
	// it with a diagnostic. A render failure must not erase their identity.
	if r.Chart != nil {
		if _, err := r.Chart.SVG(); err != nil {
			r.Unavailable = &ReaderDiagnostic{Code: "reader_render", Message: err.Error(), Source: &r.Chart.Report.Source}
			r.Chart = nil
		}
	}
	return &r
}
