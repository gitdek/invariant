package verify

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func replayJSONRecord913(record, position, value string) string {
	state := `{"label":{"kind":"string","atom":""},"Label":{"kind":"string","atom":"distinct"}}`
	initial, before, after, args := state, state, state, `[]`
	switch position {
	case "initial":
		initial = `{"label":` + value + `}`
	case "before":
		before = `{"label":` + value + `}`
	case "after":
		after = `{"label":` + value + `}`
	case "args":
		args = `[` + value + `]`
	}
	call := fmt.Sprintf(`{"index":0,"action":"Deliver","args":%s,"before":%s,"after":%s,"dispatched":true,"outcome":"refused","error":""}`, args, before, after)
	common := fmt.Sprintf(`"request_id":"request","invocation_id":"fresh","initial":%s,"calls":[%s]`, initial, call)
	if record == "result" {
		return `{"version":1,"outcome":"refused",` + common + `}`
	}
	return `{"started":true,"completed":true,"exit_code":0,` + common + `}`
}

func readReplayJSON913(t *testing.T, record, body string) error {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "record.json"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if record == "result" {
		var got BugReplayResult
		return readReplayRecord(dir, "record.json", &got)
	}
	var got BugReplayObservation
	return readReplayRecord(dir, "record.json", &got)
}

// Validate the wire before struct decoding can turn null into an empty
// string, or a differently cased field into a replacement observation.
// Every state and argument position uses the same lossless value grammar.
func TestCommandReplayNestedJSONValues913(t *testing.T) {
	for _, record := range []string{"result", "observation"} {
		for _, tc := range []struct {
			name, value string
			valid       bool
		}{
			{"empty string", `{"kind":"string","atom":""}`, true},
			{"omitted empty atom", `{"kind":"string"}`, true},
			{"exact integer", `{"kind":"integer","atom":"9007199254740993"}`, true},
			{"empty set", `{"kind":"set","elements":[]}`, true},
			{"omitted empty elements", `{"kind":"sequence"}`, true},
			{"empty record", `{"kind":"record","entries":[]}`, true},
			{"omitted empty entries", `{"kind":"record"}`, true},
			{"nested value", `{"kind":"sequence","elements":[{"kind":"function","entries":[{"key":{"kind":"integer","atom":"1"},"value":{"kind":"set","elements":[{"kind":"string"}]}}]}]}`, true},
			{"null value", `null`, false},
			{"scalar value", `"string"`, false},
			{"missing kind", `{"atom":""}`, false},
			{"null kind", `{"kind":null}`, false},
			{"nonstring kind", `{"kind":true}`, false},
			{"null atom", `{"kind":"string","atom":null}`, false},
			{"nonstring atom", `{"kind":"string","atom":0}`, false},
			{"kind alias", `{"Kind":"string"}`, false},
			{"conflicting kind alias", `{"kind":"integer","Kind":"string"}`, false},
			{"atom alias", `{"kind":"string","Atom":""}`, false},
			{"conflicting atom alias", `{"kind":"string","atom":"failure","Atom":""}`, false},
			{"null elements", `{"kind":"set","elements":null}`, false},
			{"nonarray elements", `{"kind":"set","elements":{}}`, false},
			{"elements alias", `{"kind":"set","Elements":[]}`, false},
			{"null element", `{"kind":"sequence","elements":[null]}`, false},
			{"nested null atom", `{"kind":"set","elements":[{"kind":"string","atom":null}]}`, false},
			{"nested atom alias", `{"kind":"set","elements":[{"kind":"string","atom":"bad","Atom":""}]}`, false},
			{"null entries", `{"kind":"record","entries":null}`, false},
			{"nonarray entries", `{"kind":"record","entries":{}}`, false},
			{"entries alias", `{"kind":"record","Entries":[]}`, false},
			{"null entry", `{"kind":"function","entries":[null]}`, false},
			{"missing entry key", `{"kind":"function","entries":[{"value":{"kind":"string"}}]}`, false},
			{"missing entry value", `{"kind":"function","entries":[{"key":{"kind":"string"}}]}`, false},
			{"null entry key", `{"kind":"function","entries":[{"key":null,"value":{"kind":"string"}}]}`, false},
			{"null entry value", `{"kind":"function","entries":[{"key":{"kind":"string"},"value":null}]}`, false},
			{"entry key alias", `{"kind":"function","entries":[{"key":{"kind":"string","atom":"bad"},"Key":{"kind":"string"},"value":{"kind":"string"}}]}`, false},
			{"entry value alias", `{"kind":"function","entries":[{"key":{"kind":"string"},"value":{"kind":"string","atom":"bad"},"Value":{"kind":"string"}}]}`, false},
			{"null key atom", `{"kind":"function","entries":[{"key":{"kind":"string","atom":null},"value":{"kind":"string"}}]}`, false},
			{"null entry value atom", `{"kind":"record","entries":[{"key":{"kind":"string","atom":"label"},"value":{"kind":"string","atom":null}}]}`, false},
		} {
			t.Run(record+"/"+tc.name, func(t *testing.T) {
				for _, position := range []string{"initial", "before", "after", "args"} {
					body := replayJSONRecord913(record, position, tc.value)
					if err := readReplayJSON913(t, record, body); (err == nil) != tc.valid {
						t.Errorf("%s: valid=%v, error=%v", position, tc.valid, err)
					}
				}
			})
		}
	}
}

func TestCommandReplayNestedJSONCalls913(t *testing.T) {
	for _, record := range []string{"result", "observation"} {
		for _, tc := range []struct {
			name, from, to string
			valid          bool
		}{
			{"healthy", `"dispatched":true`, `"dispatched":true`, true},
			{"empty null args", `"args":[]`, `"args":null`, true},
			{"omitted optional error", `,"error":""`, ``, true},
			{"conflicting dispatch alias", `"dispatched":true`, `"dispatched":false,"Dispatched":true`, false},
			{"dispatch alias", `"dispatched":true`, `"Dispatched":true`, false},
			{"null dispatch", `"dispatched":true`, `"dispatched":null`, false},
			{"nonboolean dispatch", `"dispatched":true`, `"dispatched":"true"`, false},
			{"conflicting index alias", `"index":0`, `"index":1,"Index":0`, false},
			{"null index", `"index":0`, `"index":null`, false},
			{"noninteger index", `"index":0`, `"index":0.5`, false},
			{"conflicting action alias", `"action":"Deliver"`, `"action":"Other","Action":"Deliver"`, false},
			{"null action", `"action":"Deliver"`, `"action":null`, false},
			{"nonstring action", `"action":"Deliver"`, `"action":false`, false},
			{"conflicting args alias", `"args":[]`, `"args":[{"kind":"string"}],"Args":[]`, false},
			{"nonarray args", `"args":[]`, `"args":{}`, false},
			{"before alias", `"before":`, `"Before":`, false},
			{"after alias", `"after":`, `"After":`, false},
			{"conflicting outcome alias", `"outcome":"refused","error"`, `"outcome":"failed","Outcome":"refused","error"`, false},
			{"null outcome", `"outcome":"refused","error"`, `"outcome":null,"error"`, false},
			{"nonstring outcome", `"outcome":"refused","error"`, `"outcome":true,"error"`, false},
			{"conflicting error alias", `"error":""`, `"error":"failure","Error":""`, false},
			{"null error", `"error":""`, `"error":null`, false},
			{"nonstring error", `"error":""`, `"error":false`, false},
		} {
			t.Run(record+"/"+tc.name, func(t *testing.T) {
				body := replayJSONRecord913(record, "", "")
				if !strings.Contains(body, tc.from) {
					t.Fatal("mutation did not match the fixture")
				}
				body = strings.Replace(body, tc.from, tc.to, 1)
				if err := readReplayJSON913(t, record, body); (err == nil) != tc.valid {
					t.Fatalf("valid=%v, error=%v", tc.valid, err)
				}
			})
		}
	}
}
