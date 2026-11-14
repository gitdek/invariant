//go:build integration

package verify

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/toolchain"
)

// Exercise the actual command boundary, including privilege separation,
// trusted Python startup and host decoding of the pinned runtimes' JSON.
func TestCommandReplayPinnedUnicode913(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	tc, err := toolchain.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state := tlc.ReplayState{"label": {Kind: "string", Atom: "\ufffd"}}
	q := BugReplayRequest{Version: 1, ID: "request", InvocationID: "fresh", Path: tlc.ReplayPath{Offending: 0,
		States: []tlc.ReplayState{state, {"label": {Kind: "string", Atom: "forbidden"}}},
		Steps:  []tlc.ReplayStep{{From: 0, To: 1, Action: "Change", Bug: true}}}}
	for _, language := range []string{"typescript", "python"} {
		for _, after := range []string{`"\ud800"`, `"\udfff"`, `"\ufffd"`, `"\ud83d\ude80"`, `"漢é"`, `"\\ud800"`} {
			t.Run(language+"/"+after, func(t *testing.T) {
				s := &commandReplaySource{dir: t.TempDir(), language: language, image: tc.NodeImage, driver: "driver.ts"}
				source := strings.ReplaceAll(unicodeReplayTS913, "process.env.BEFORE!", `"\ufffd"`)
				source = strings.ReplaceAll(source, "process.env.AFTER!", strconv.Quote(after))
				if language == "python" {
					s.image, s.driver = tc.PythonImage, "driver.py"
					source = strings.ReplaceAll(unicodeReplayPY913, "os.environ['BEFORE']", `"\ufffd"`)
					source = strings.ReplaceAll(source, "os.environ['AFTER']", strconv.Quote(after))
				}
				src := filepath.Join(s.dir, "src")
				if err := os.Mkdir(src, 0755); err != nil {
					t.Fatal(err)
				}
				for name, body := range map[string]string{s.driver: source, "package.json": `{"type":"module"}`} {
					if err := os.WriteFile(filepath.Join(src, name), []byte(body), 0644); err != nil {
						t.Fatal(err)
					}
				}
				result, observed, err := s.run(ctx, q)
				if after == `"\ud800"` || after == `"\udfff"` {
					if err == nil || !strings.Contains(err.Error(), "Unicode surrogate") {
						t.Fatalf("malformed runtime evidence survived host decoding: %+v %v", result, err)
					}
					return
				}
				want := map[string]string{`"\ufffd"`: "\ufffd", `"\ud83d\ude80"`: "🚀", `"漢é"`: "漢é", `"\\ud800"`: `\ud800`}[after]
				if err != nil || !observed.Completed || len(result.Calls) != 1 || result.Calls[0].After["label"].Atom != want || !sameReplayCalls(result.Calls, observed.Calls) {
					t.Fatalf("valid Unicode lost through command replay: %+v %+v %v", result, observed, err)
				}
			})
		}
	}
}
