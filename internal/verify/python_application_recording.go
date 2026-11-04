package verify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/gitdek/invariant/internal/conformance"
)

// Conversion leaves the authenticated rows intact for the receipt. Only one
// state or attempt is converted at a time; variable-size deduplication keys
// and converted states live in scratch storage, not a second observation tree.
func writePythonApplicationRecording(ctx context.Context, output io.Writer, x PythonApplicationExecutionResult) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !x.Success || !x.Complete || len(x.Observations) == 0 ||
		x.ObservationMode != "sampled" && x.ObservationMode != "explored" ||
		x.ObservationMode == "explored" && x.Exploration == nil {
		return fmt.Errorf("no complete authenticated %s observations", x.ObservationMode)
	}
	f, err := os.CreateTemp("", "invariant-python-recording-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close(), os.Remove(f.Name())) }()
	s := pythonRecordingStore{ctx: ctx, file: f, index: map[[32]byte]int{}}
	w := recordingWriter{ctx: ctx, out: output}
	if x.ObservationMode == "sampled" {
		err = s.traces(&w, x)
	} else {
		err = s.attempts(&w, x)
	}
	return errors.Join(err, w.err, ctx.Err())
}

func pythonApplicationRecording(ctx context.Context, x PythonApplicationExecutionResult, explored bool) (*recording, error) {
	mode := "sampled"
	if explored {
		mode = "explored"
	}
	if x.ObservationMode != mode {
		return nil, fmt.Errorf("no complete authenticated %s observations", mode)
	}
	dir, err := os.MkdirTemp("", "invariant-recording-")
	if err != nil {
		return nil, err
	}
	r := &recording{dir: dir, path: filepath.Join(dir, "traces.json")}
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, errors.Join(err, r.close())
	}
	err = writePythonApplicationRecording(ctx, f, x)
	err = errors.Join(err, f.Close())
	if err != nil {
		return nil, errors.Join(err, r.close())
	}
	return r, nil
}

type pythonRecordingSpan struct{ offset, size int64 }

type pythonRecordingState struct {
	raw, model pythonRecordingSpan
	next       int
}

type pythonRecordingStore struct {
	ctx                      context.Context
	file                     *os.File
	end                      int64
	states                   []pythonRecordingState
	index                    map[[32]byte]int
	raw                      bytes.Buffer
	modelJSON                bytes.Buffer
	rawEncoder, modelEncoder *json.Encoder
	copyBuffer               [32 << 10]byte
	equalBuffer              [4096]byte
}

func (s *pythonRecordingStore) put(b []byte) (pythonRecordingSpan, error) {
	if err := s.ctx.Err(); err != nil {
		return pythonRecordingSpan{}, err
	}
	r := pythonRecordingSpan{offset: s.end, size: int64(len(b))}
	n, err := s.file.WriteAt(b, s.end)
	s.end += int64(n)
	if err == nil && n != len(b) {
		err = io.ErrShortWrite
	}
	return r, err
}

func (s *pythonRecordingStore) model(row PythonApplicationObservation, state map[string]any) (pythonRecordingSpan, error) {
	s.modelJSON.Reset()
	if s.modelEncoder == nil {
		s.modelEncoder = json.NewEncoder(&s.modelJSON)
	}
	if row.Form == "module" {
		if err := s.modelValue(state); err != nil {
			return pythonRecordingSpan{}, err
		}
	} else if err := s.modelEncoder.Encode(state); err != nil {
		return pythonRecordingSpan{}, err
	}
	return s.put(s.modelJSON.Bytes())
}

var pythonRecordingInteger = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)

// Write the module boundary's list-to-sequence encoding directly. Reusing
// the encoder and buffer avoids a converted tree and per-scalar JSON copies.
func (s *pythonRecordingStore) modelValue(value any) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	switch v := value.(type) {
	case []any:
		s.modelJSON.WriteString(`{"$seq":[`)
		for i, item := range v {
			if i > 0 {
				s.modelJSON.WriteByte(',')
			}
			if err := s.modelValue(item); err != nil {
				return err
			}
		}
		s.modelJSON.WriteString("]}")
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			if !pythonApplicationRecordField.MatchString(key) {
				return fmt.Errorf("module observation record field %q is not a TLA+ identifier", key)
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		s.modelJSON.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				s.modelJSON.WriteByte(',')
			}
			// Validated identifiers need no JSON escaping.
			s.modelJSON.WriteByte('"')
			s.modelJSON.WriteString(key)
			s.modelJSON.WriteString(`":`)
			if err := s.modelValue(v[key]); err != nil {
				return err
			}
		}
		s.modelJSON.WriteByte('}')
	default:
		switch v := value.(type) {
		case string:
			if pythonRecordingPlainString(v) {
				s.modelJSON.WriteByte('"')
				s.modelJSON.WriteString(v)
				s.modelJSON.WriteByte('"')
				return nil
			}
		case bool:
			if v {
				s.modelJSON.WriteString("true")
			} else {
				s.modelJSON.WriteString("false")
			}
			return nil
		case json.Number:
			if !pythonRecordingInteger.MatchString(string(v)) {
				return fmt.Errorf("module observation cannot encode as TLA+: %s isn't an integer; TLA+ has no fractions", v)
			}
			s.modelJSON.WriteString(string(v))
			return nil
		default:
			if _, err := conformance.Encode(value); err != nil {
				return fmt.Errorf("module observation cannot encode as TLA+: %w", err)
			}
		}
		if err := s.modelEncoder.Encode(value); err != nil {
			return err
		}
		s.modelJSON.Truncate(s.modelJSON.Len() - 1) // Encoder's trailing newline.
	}
	return nil
}

// Strings needing any escaping still use encoding/json's exact rules.
func pythonRecordingPlainString(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c >= 0x80 || c == '"' || c == '\\' || c == '<' || c == '>' || c == '&' {
			return false
		}
	}
	return true
}

func (s *pythonRecordingStore) copy(w *recordingWriter, r pythonRecordingSpan) error {
	n, err := io.CopyBuffer(w, io.NewSectionReader(s.file, r.offset, r.size), s.copyBuffer[:])
	if err == nil && n != r.size {
		err = io.ErrUnexpectedEOF
	}
	return err
}

func (s *pythonRecordingStore) equal(r pythonRecordingSpan, b []byte) (bool, error) {
	if r.size != int64(len(b)) {
		return false, nil
	}
	buf := s.equalBuffer[:]
	for off := int64(0); off < r.size; {
		if err := s.ctx.Err(); err != nil {
			return false, err
		}
		n := min(int64(len(buf)), r.size-off)
		if _, err := s.file.ReadAt(buf[:n], r.offset+off); err != nil {
			return false, err
		}
		if !bytes.Equal(buf[:n], b[off:off+n]) {
			return false, nil
		}
		off += n
	}
	return true, nil
}

func (s *pythonRecordingStore) state(row PythonApplicationObservation, state map[string]any) (int, error) {
	s.raw.Reset()
	if s.rawEncoder == nil {
		s.rawEncoder = json.NewEncoder(&s.raw)
	}
	if err := s.rawEncoder.Encode(state); err != nil {
		return 0, err
	}
	s.raw.Truncate(s.raw.Len() - 1)
	b := s.raw.Bytes()
	return s.stateHash(row, state, b, sha256.Sum256(b))
}

// Hashes select a collision chain; complete raw JSON equality assigns IDs.
func (s *pythonRecordingStore) stateHash(row PythonApplicationObservation, state map[string]any, b []byte, hash [32]byte) (int, error) {
	for link := s.index[hash]; link != 0; link = s.states[link-1].next {
		equal, err := s.equal(s.states[link-1].raw, b)
		if err != nil {
			return 0, err
		}
		if equal {
			return link - 1, nil
		}
	}
	model, err := s.model(row, state)
	if err != nil {
		return 0, err
	}
	raw, err := s.put(b)
	if err != nil {
		return 0, err
	}
	id := len(s.states)
	s.states = append(s.states, pythonRecordingState{raw: raw, model: model, next: s.index[hash]})
	s.index[hash] = id + 1
	return id, nil
}

func (s *pythonRecordingStore) traces(w *recordingWriter, x PythonApplicationExecutionResult) error {
	// Lifecycles can interleave. Link their disk spans in observation order
	// without opening a file per run or retaining converted values per run.
	type item struct {
		span pythonRecordingSpan
		next int
	}
	var items []item
	var starts, ends []int
	for _, row := range x.Observations {
		state := row.After
		if row.Kind == "initial" {
			state = row.State
		}
		span, err := s.model(row, state)
		if err != nil {
			return err
		}
		i := len(items)
		if row.Kind == "initial" {
			if row.Run != len(starts)+1 {
				return fmt.Errorf("invalid observation run")
			}
			starts = append(starts, i)
			ends = append(ends, i)
		} else {
			if row.Run < 1 || row.Run > len(starts) {
				return fmt.Errorf("invalid observation run")
			}
			items[ends[row.Run-1]].next = i
			ends[row.Run-1] = i
		}
		items = append(items, item{span: span, next: -1})
	}
	w.text(`{"traces":[`)
	for run, start := range starts {
		if run > 0 {
			w.text(",")
		}
		w.text("[")
		for i := start; i != -1; i = items[i].next {
			if i != start {
				w.text(",")
			}
			if err := s.copy(w, items[i].span); err != nil {
				return err
			}
		}
		w.text("]")
	}
	w.text("]}")
	return w.err
}

func (s *pythonRecordingStore) attempts(w *recordingWriter, x PythonApplicationExecutionResult) error {
	// The authenticated count is only a capacity hint, bounded by the rows
	// already retained. Equality and IDs still come from the disk keys.
	capacity := min(max(x.Exploration.States, 0), len(x.Observations))
	s.states = make([]pythonRecordingState, 0, capacity)
	s.index = make(map[[32]byte]int, capacity)
	started := map[int]bool{}
	inits := []int{}
	declarations := map[string]*PythonApplicationRefusal{}
	for _, step := range x.Exploration.Steps {
		declarations[step.Name] = step.Refusal
	}
	w.text(`{"attempts":[`)
	attempts := 0
	for _, row := range x.Observations {
		if w.err != nil {
			return w.err
		}
		if err := s.ctx.Err(); err != nil {
			return err
		}
		if row.Kind == "transition" {
			if err := pythonApplicationOutcome(row, declarations[row.Step]); err != nil {
				return err
			}
		} else if row.Outcome != "" || row.Refusal != nil {
			return fmt.Errorf("refusal metadata outside an exploration transition")
		}
		switch {
		case row.Kind == "initial":
			i, err := s.state(row, row.State)
			if err != nil {
				return err
			}
			if !started[i] {
				started[i] = true
				inits = append(inits, i)
			}
		case row.Kind == "transition" && row.Replay != nil && !*row.Replay:
			from, err := s.state(row, row.Before)
			if err != nil {
				return err
			}
			to, err := s.state(row, row.After)
			if err != nil {
				return err
			}
			args := row.Args
			if args == nil {
				args = []any{}
			}
			attempt := []any{from, row.Step, args, to}
			if row.Outcome != "" {
				metadata := map[string]any{"outcome": row.Outcome}
				if row.Refusal != nil {
					metadata["refusal"] = row.Refusal
				}
				attempt = append(attempt, metadata)
			}
			if attempts > 0 {
				w.text(",")
			}
			w.value(attempt)
			attempts++
		}
	}
	if attempts == 0 {
		return fmt.Errorf("no explored attempts")
	}
	w.text(`],"init":`)
	w.value(inits)
	w.text(`,"states":[`)
	for i, state := range s.states {
		if i > 0 {
			w.text(",")
		}
		if err := s.copy(w, state.model); err != nil {
			return err
		}
	}
	w.text("]}")
	return w.err
}
