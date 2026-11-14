package verify

import (
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/tlc"
)

func TestCommandReplayUnicodeEscapes913(t *testing.T) {
	for _, tc := range []struct {
		name, atom string
		valid      bool
	}{
		{"replacement character", `\ufffd`, true},
		{"UTF-8", `漢🚀`, true},
		{"surrogate pair", `\ud83d\ude80`, true},
		{"uppercase pair", `\uD83D\uDE80`, true},
		{"lowest pair", `\uD800\uDC00`, true},
		{"highest pair", `\udbff\udfff`, true},
		{"surrogate boundary", `\ud7ff\ue000`, true},
		{"escaped backslash", `\\ud800`, true},
		{"escaped quote", `\"\\ud800`, true},
		{"high surrogate", `\ud800`, false},
		{"low surrogate", `\udc00`, false},
		{"reversed pair", `\ude80\ud83d`, false},
		{"two high surrogates", `\ud800\udbff`, false},
		{"two low surrogates", `\udc00\udfff`, false},
		{"nonadjacent pair", `\ud800x\udc00`, false},
		{"high then ordinary", `\ud800\u0041`, false},
		{"escaped low surrogate", `\ud800\\udc00`, false},
		{"high after escaped backslash", `\\\ud800`, false},
		{"bad hex", `\ud80x`, false},
		{"short escape", `\ud80`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, record := range []string{"result", "observation"} {
				for _, position := range []string{"initial", "before", "after", "args"} {
					for _, value := range []string{
						`{"kind":"string","atom":"` + tc.atom + `"}`,
						`{"kind":"record","entries":[{"key":{"kind":"string","atom":"` + tc.atom + `"},"value":{"kind":"sequence","elements":[{"kind":"string"}]}}]}`,
					} {
						if err := readReplayJSON913(t, record, replayJSONRecord913(record, position, value)); (err == nil) != tc.valid {
							t.Errorf("%s/%s: valid=%v, error=%v", record, position, tc.valid, err)
						}
					}
				}
				// Keys, action labels and diagnostics must also remain lossless.
				for _, from := range []string{`"label":`, `"Deliver"`, `"error":""`} {
					to := `"` + tc.atom + `"`
					if from == `"label":` {
						to += ":"
					} else if from == `"error":""` {
						to = `"error":` + to
					}
					body := strings.Replace(replayJSONRecord913(record, "", ""), from, to, 1)
					if err := readReplayJSON913(t, record, body); (err == nil) != tc.valid {
						t.Errorf("%s/%s: valid=%v, error=%v", record, from, tc.valid, err)
					}
				}
			}
		})
	}
}

const unicodeReplayTS913 = `class Label {
  value = process.env.BEFORE!;
  change() { this.value = JSON.parse(process.env.AFTER!); console.log('dispatched'); return true; }
  read() { return {label:{kind:'string',atom:this.value}}; }
  close() { console.log('closed'); }
}
export function invariantReplay(initial:any) {
  const app = new Label();
  return {read:()=>app.read(),bind:()=>({target:app,method:'change',args:[]}),close:()=>app.close()};
}
`

const unicodeReplayPY913 = `import os, json
class Label:
    def __init__(self):
        self.value = os.environ['BEFORE']
    def change(self):
        self.value = json.loads(os.environ['AFTER'])
        print('dispatched')
        return True
    def read(self):
        return dict(label=dict(kind='string', atom=self.value))
    def close(self):
        print('closed')
def invariant_replay(initial):
    app = Label()
    return dict(read=app.read, bind=lambda action,args:dict(target=app,method='change',args=[]), close=app.close)
`

func TestCommandReplayRuntimeUnicode913(t *testing.T) {
	state := tlc.ReplayState{"label": {Kind: "string", Atom: "\ufffd"}}
	q := BugReplayRequest{Version: 1, ID: "request", InvocationID: "fresh", Path: tlc.ReplayPath{Offending: 0,
		States: []tlc.ReplayState{state, {"label": {Kind: "string", Atom: "forbidden"}}},
		Steps:  []tlc.ReplayStep{{From: 0, To: 1, Action: "Change", Bug: true}}}}
	for _, language := range []string{"typescript", "python"} {
		for _, tc := range []struct {
			name, after, want string
			valid             bool
		}{
			{"high surrogate", `"\ud800"`, "", false},
			{"low surrogate", `"\udfff"`, "", false},
			{"reversed pair", `"\ude80\ud83d"`, "", false},
			{"unchanged replacement", `"\ufffd"`, "\ufffd", true},
			{"valid pair", `"\ud83d\ude80"`, "🚀", true},
			{"valid UTF-8", `"漢é"`, "漢é", true},
			{"literal escape", `"\\ud800"`, `\ud800`, true},
		} {
			t.Run(language+"/"+tc.name, func(t *testing.T) {
				source := unicodeReplayTS913
				if language == "python" {
					source = unicodeReplayPY913
				}
				dir, output, err := runReplayHarness913(t, language, source, q, "BEFORE=\ufffd", "AFTER="+tc.after)
				if err != nil || !strings.Contains(output, "dispatched") || !strings.Contains(output, "closed") {
					t.Fatalf("runtime did not complete its real operation and cleanup: %v\n%s", err, output)
				}
				var result BugReplayResult
				var observed BugReplayObservation
				for name, dst := range map[string]any{"result.json": &result, "observed.json": &observed} {
					if err := readReplayRecord(dir, name, dst); (err == nil) != tc.valid {
						t.Fatalf("%s: valid=%v, error=%v", name, tc.valid, err)
					}
				}
				if tc.valid && (len(result.Calls) != 1 || !sameReplayCalls(result.Calls, observed.Calls) || !observed.Completed || result.Calls[0].After["label"].Atom != tc.want) {
					t.Fatalf("valid Unicode changed during replay: %+v %+v", result, observed)
				}
			})
		}
	}
}
