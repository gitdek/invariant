package verify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestPythonRecordingStateCollisions(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "states"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s := pythonRecordingStore{ctx: context.Background(), file: f, index: map[[32]byte]int{}}
	row := PythonApplicationObservation{Form: "module"}
	var hash [32]byte // Deliberately put every distinct raw value in one chain.
	for _, test := range []struct {
		n  string
		id int
	}{{"9007199254740992", 0}, {"9007199254740993", 1}, {"9007199254740992", 0}, {"9007199254740993", 1}} {
		state := map[string]any{"n": json.Number(test.n), "values": []any{json.Number("1")}}
		raw, err := json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		id, err := s.stateHash(row, state, raw, hash)
		if err != nil || id != test.id {
			t.Fatalf("state %s: id=%d, err=%v", test.n, id, err)
		}
		var out bytes.Buffer
		w := recordingWriter{ctx: context.Background(), out: &out}
		if err := s.copy(&w, s.states[id].model); err != nil {
			t.Fatal(err)
		}
		want := `{"n":` + test.n + `,"values":{"$seq":[1]}}`
		if out.String() != want {
			t.Fatalf("converted state: %s, want %s", out.String(), want)
		}
		if _, ok := state["values"].([]any); !ok {
			t.Fatal("changed the raw observation")
		}
	}
	if len(s.states) != 2 {
		t.Fatalf("retained %d states, want 2", len(s.states))
	}
	if err := f.Truncate(0); err != nil {
		t.Fatal(err)
	}
	w := recordingWriter{ctx: context.Background(), out: io.Discard}
	if err := s.copy(&w, s.states[0].model); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated conversion storage: %v", err)
	}
	if _, err := s.equal(s.states[0].raw, make([]byte, s.states[0].raw.size)); err == nil {
		t.Fatal("truncated identity storage was accepted")
	}
}

func TestPythonRecordingModuleEncoding(t *testing.T) {
	values := []any{
		"quotes\" slash\\ newline\n control\x01 html<>& unicode\u2028 invalid\xff",
		true, false, float64(1), json.Number("9007199254740993"), json.Number("-0"),
		[]any{json.Number("-2"), map[string]any{"nested": []any{true, "s"}}},
		[]any(nil), map[string]any(nil),
		float64(1.5), json.Number("1.5"), json.Number("1e2"), json.Number("01"),
		nil, 1, map[string]any{"$seq": []any{}}, map[string]any{"bad-key": true},
	}
	for c := 0; c < 128; c++ {
		values = append(values, string(rune(c)))
	}
	for _, value := range values {
		s := pythonRecordingStore{ctx: context.Background()}
		s.modelEncoder = json.NewEncoder(&s.modelJSON)
		converted, wantErr := pythonApplicationModelValue(value)
		err := s.modelValue(value)
		if wantErr != nil {
			if err == nil || err.Error() != wantErr.Error() {
				t.Fatalf("%#v: got %v, want %v", value, err, wantErr)
			}
			continue
		}
		want, marshalErr := json.Marshal(converted)
		if err != nil || marshalErr != nil || !bytes.Equal(s.modelJSON.Bytes(), want) {
			t.Fatalf("%#v: got %s (%v), want %s (%v)", value, s.modelJSON.Bytes(), err, want, marshalErr)
		}
	}
}

func TestPythonRecordingFailedExecutionDiagnostic(t *testing.T) {
	for _, explored := range []bool{false, true} {
		mode := "sampled"
		if explored {
			mode = "explored"
		}
		// Failed collection clears the mode; the declared invocation still
		// determines the gate's diagnostic, as the byte wrappers did.
		r, err := pythonApplicationRecording(context.Background(), PythonApplicationExecutionResult{}, explored)
		if r != nil {
			r.close()
			t.Fatal("failed collection produced a recording")
		}
		if err == nil || err.Error() != "no complete authenticated "+mode+" observations" {
			t.Fatalf("%s: %v", mode, err)
		}
	}
}

type recordingShortWriter struct{}

func (recordingShortWriter) Write(p []byte) (int, error) { return len(p) / 2, nil }

func TestPythonRecordingShortWriteAndStorageFailure(t *testing.T) {
	for _, mode := range []string{"sampled", "explored"} {
		t.Run(mode, func(t *testing.T) {
			tmp := t.TempDir()
			t.Setenv("TMPDIR", tmp)
			no := false
			state := map[string]any{"n": json.Number("0")}
			x := PythonApplicationExecutionResult{Success: true, Complete: true, ObservationMode: mode,
				Exploration: &PythonApplicationExploration{}, Observations: []PythonApplicationObservation{
					{Kind: "initial", Form: "module", Run: 1, State: state},
					{Kind: "transition", Form: "module", Run: 1, Before: state, After: state, Step: "Read", Replay: &no},
				}}
			if err := writePythonApplicationRecording(context.Background(), recordingShortWriter{}, x); !errors.Is(err, io.ErrShortWrite) {
				t.Fatalf("short output write: %v", err)
			}
			entries, err := os.ReadDir(tmp)
			if err != nil || len(entries) != 0 {
				t.Fatalf("conversion scratch remains: %v %v", entries, err)
			}
			missing := filepath.Join(tmp, "missing")
			t.Setenv("TMPDIR", missing)
			if err := writePythonApplicationRecording(context.Background(), io.Discard, x); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("scratch creation failure: %v", err)
			}
		})
	}
}

func TestRecordingCompatibilityCleanup(t *testing.T) {
	for _, raw := range []string{"", `{"traces":[`, `{"traces":[[{"n":0}]]}`} {
		path := filepath.Join(t.TempDir(), "traces.json")
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		r, err := takeRecording(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		dir := r.dir
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("producer still owns recording: %v", err)
		}
		_, e, err := recordingBytes(Build{}, recordedEvidence{recording: r}, nil)
		if err != nil || e.Traces == nil || string(e.Traces) != raw {
			t.Fatalf("byte compatibility: %q %v", e.Traces, err)
		}
		if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("compatibility storage remains: %v", err)
		}
		if err := r.close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRecordingLinkedOutput(t *testing.T) {
	producer := t.TempDir()
	raw := `{"traces":[[{"n":0}]]}`
	if err := os.WriteFile(filepath.Join(producer, "data.json"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(producer, "traces.json")
	if err := os.Symlink("data.json", path); err != nil {
		t.Fatal(err)
	}
	r, err := takeRecording(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.close()
	if err := os.RemoveAll(producer); err != nil {
		t.Fatal(err)
	}
	_, e, err := recordingBytes(Build{}, recordedEvidence{recording: r}, nil)
	if err != nil || string(e.Traces) != raw {
		t.Fatalf("linked output lost after producer cleanup: %q %v", e.Traces, err)
	}
}
