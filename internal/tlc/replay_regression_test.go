package tlc

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/tla"
)

func TestReplayValueRejectsMalformedTypedData(t *testing.T) {
	for _, v := range []ReplayValue{
		{Kind: "integer", Atom: "1 /\\ TRUE"},
		{Kind: "bool", Atom: "true"},
		{Kind: "model", Atom: "TRUE"},
		{Kind: "model", Atom: "p1'"},
		{Kind: "string", Atom: "x", Elements: []ReplayValue{{Kind: "integer", Atom: "1"}}},
		{Kind: "string", Atom: string([]byte{0xff})},
		{Kind: "set", Atom: "source"},
		{Kind: "function"},
		{Kind: "record", Entries: []ReplayEntry{{Key: ReplayValue{Kind: "model", Atom: "field"}, Value: ReplayValue{Kind: "bool", Atom: "TRUE"}}}},
	} {
		if text, err := v.TLA(); err == nil {
			t.Errorf("malformed value became %q: %+v", text, v)
		}
	}
	for _, source := range []string{`"a\\b\"c\n\tλ"`, `{<<1,2>>, <<2,1>>}`, `(1 :> "1" @@ "1" :> 1)`} {
		v, err := ParseReplayValue(source)
		if err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var back ReplayValue
		if err := json.Unmarshal(b, &back); err != nil {
			t.Fatal(err)
		}
		text, err := back.TLA()
		if err != nil {
			t.Fatal(err)
		}
		round, err := ParseReplayValue(text)
		if err != nil || !reflect.DeepEqual(v, round) {
			t.Fatalf("lossy string/key value: %s %v", text, err)
		}
	}
	if _, err := ParseReplayValue("1..999999999999999999999999999999999"); err == nil {
		t.Fatal("expanded an unbounded interval")
	}
	if _, err := ParseReplayValue("\"" + string([]byte{0xff}) + "\""); err == nil {
		t.Fatal("accepted a string JSON cannot preserve")
	}
}

func TestReplayRowsSeparateProtocolFromSANYAndKeepDuplicates(t *testing.T) {
	row := "<<\n  \"invariant-replay-v1\", 0,\n  {<<\"Put\", <<p1>>, TRUE>>}\n>>"
	steps := []tla.Step{{Name: "Put", Args: []string{"p"}}}
	printed := []string{"Parsing file /tmp/temporary/Naturals.tla", row}
	got, err := replayEdges(printed, 1, nil, steps)
	if err != nil || len(got) != 1 || got[0].Args[0].Kind != "model" || !got[0].Bug {
		t.Fatalf("multiline TLC row: %+v %v", got, err)
	}
	for _, bad := range [][]string{
		{row, row},
		{strings.Replace(row, "Put", "Other", 1)},
		{strings.Replace(row, "TRUE", "\"TRUE\"", 1)},
		{strings.TrimSuffix(row, ">>")},
		{"Parsing file /tmp/temporary/Naturals.tla"},
	} {
		if _, err := replayEdges(bad, 1, nil, steps); err == nil {
			t.Errorf("accepted broken edge records: %q", bad)
		}
	}
}
