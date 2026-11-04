package conformance

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestRecordingStoreCollisionsAndDiagnostics(t *testing.T) {
	s, err := newRecordingStore(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.close(); err != nil {
			t.Error(err)
		}
	}()
	var hash [32]byte
	var ids []int
	for _, tail := range []string{"a", "b", "a"} {
		state := map[string]any{"x": map[string]any{"$seq": []any{strings.Repeat("p", 9000) + tail, map[string]any{"inner": " |-> , << \\\" >>"}}}, "y": true}
		text, pretty, err := encodeState(state, []string{"x", "y"})
		if err != nil {
			t.Fatal(err)
		}
		id, err := s.stateHash(text, hash)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		got, err := s.pretty(id)
		if err != nil || got != pretty {
			t.Fatalf("diagnostic differs: %v", err)
		}
	}
	if !reflect.DeepEqual(ids, []int{0, 1, 0}) || len(s.states) != 2 {
		t.Fatalf("hash collision changed canonical identities: %v", ids)
	}
	if order, err := s.compare(s.states[0].text, s.states[1].text); err != nil || order >= 0 {
		t.Fatalf("disk ordering lost a suffix: %d %v", order, err)
	}
	if s.end != s.states[0].text.size+s.states[1].text.size {
		t.Fatal("equal state text was stored twice")
	}
}

func TestStreamingRepeatedMapsMatchJSON(t *testing.T) {
	for _, raw := range []string{
		`{"states":[{"n":0},{"n":1}],"states":[{"n":2}],"states":[{},{}],"init":[1,0],"init":[0],"init":[null,null],"attempts":[]}`,
		`{"states":[{"n":0,"extra":1}],"states":[],"states":[{"n":1}],"init":[0],"attempts":[]}`,
		`{"traces":[[{"n":0},{"n":1}],[{"n":2}]],"traces":[[{"n":3}]],"traces":[[{},{}],[{}]]}`,
		`{"traces":[[{"n":0,"extra":1}]],"traces":[[]],"traces":[[{"n":1}]]}`,
		`{"traces":[[{"n":0}]],"traces":null,"traces":[[{"n":1}]]}`,
	} {
		t.Run(raw, func(t *testing.T) {
			var legacy struct {
				Traces [][]map[string]any `json:"traces"`
				attempts
			}
			if err := decodeValue([]byte(raw), &legacy); err != nil {
				t.Fatal(err)
			}
			canonical, err := json.Marshal(legacy)
			if err != nil {
				t.Fatal(err)
			}
			a, ap := decodeForTest(t, []byte(raw), []string{"n"})
			b, bp := decodeForTest(t, canonical, []string{"n"})
			if ap != bp || a.runs != b.runs || a.recorded != b.recorded || !reflect.DeepEqual(a.starts, b.starts) || !reflect.DeepEqual(a.steps, b.steps) || len(a.store.states) != len(b.store.states) {
				t.Fatalf("repeated fields differ from encoding/json: %+v %q, %+v %q", a, ap, b, bp)
			}
			for i := range a.store.states {
				x, xe := a.store.pretty(i)
				y, ye := b.store.pretty(i)
				if xe != nil || ye != nil || x != y {
					t.Fatalf("state %d differs: %s, %s, %v, %v", i, x, y, xe, ye)
				}
			}
		})
	}
}
