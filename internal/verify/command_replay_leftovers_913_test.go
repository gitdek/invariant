package verify

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tlc"
)

// leftoverDocker913 stands in for Docker. Every image is present, and each
// run leaves in its source mount what a Linux container can leave there: a
// directory the gate's user can't empty, as Python's __pycache__ is when
// the container's root or the replay worker writes it. A sample then ends
// with its recording, and a replay with the records in RECORDS.
const leftoverDocker913 = `#!/bin/sh
[ "$1" = image ] && exit 0
[ "$1" = run ] || exit 90
src= out= replay= prev=
for a in "$@"; do
	if [ "$prev" = -v ]; then
		case "$a" in
		*:/src) src=${a%:/src} ;;
		*:/out) out=${a%:/out} ;;
		esac
	fi
	[ "$a" = INVARIANT_REPLAY_REQUEST=/replay/request.json ] && replay=1
	prev=$a
done
mkdir -p "$src/__pycache__" && : > "$src/__pycache__/driver.cpython-313.pyc" && chmod 555 "$src/__pycache__" || exit 91
if [ -n "$replay" ]; then
	cp 'RECORDS/replay-result.json' 'RECORDS/replay-observation.json' "$out/" || exit 92
	echo "@@invariant replay=0"
else
	echo '{"traces":[]}' > "$out/traces.json" || exit 92
	echo "@@invariant conform=0"
fi
`

// A sandbox's workspace was never evidence, so what a container leaves in
// it that the gate's user can't remove fails nothing: not a sample, as it
// never did before #913, and not a replay. CI's Linux shards failed every
// ordinary Python gate on the __pycache__ a sample's checks write as root.
func TestASandboxsLeftoversFailNothing913(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can remove whatever a sandbox leaves")
	}
	tmp := t.TempDir()
	// Cleanups run last first, so this hands the leftovers back before
	// TempDir removes them.
	t.Cleanup(func() {
		filepath.WalkDir(tmp, func(file string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				os.Chmod(file, 0o755)
			}
			return nil
		})
	})
	t.Setenv("TMPDIR", tmp)
	bin, records := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(strings.ReplaceAll(leftoverDocker913, "RECORDS", records)), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	write := func(dir, name, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()

	t.Run("sample", func(t *testing.T) {
		dir := t.TempDir()
		write(dir, "driver.ts", "export {};\n")
		p := &project.Project{Dir: dir, Manifest: project.Manifest{Name: "leftovers", Language: "typescript", Module: ".invariant/specs/M.tla", Code: "src", Conformance: "driver.ts"}}
		_, evidence, err := runConformance(ctx, "sample-image", p, nil, "true")
		if err != nil || string(evidence.Traces) != "{\"traces\":[]}\n" {
			t.Fatalf("a sample's leftovers failed its check: %v %q", err, evidence.Traces)
		}
	})

	t.Run("existing sample", func(t *testing.T) {
		root := t.TempDir()
		write(root, "package.json", `{"name":"leftovers","version":"1.0.0","type":"module"}`)
		write(root, "package-lock.json", `{"name":"leftovers","version":"1.0.0","lockfileVersion":3,"requires":true,"packages":{"":{"name":"leftovers","version":"1.0.0"}}}`)
		write(root, "src/app.ts", "export const app = 1;\n")
		write(root, "verification/driver.ts", "import '../src/app.ts';\n")
		p := &project.Project{Dir: filepath.Join(root, "verification"), Manifest: project.Manifest{Name: "leftovers", Language: "typescript", Module: ".invariant/specs/M.tla", Code: ".", Conformance: "driver.ts", Existing: []string{"src/app.ts"}}}
		build, evidence, err := ExistingTypeScript{}.Check(ctx, p)
		if err != nil || !build.Passed || string(evidence.Traces) != "{\"traces\":[]}\n" {
			t.Fatalf("an existing sample's leftovers failed its check: %v %+v %q", err, build, evidence.Traces)
		}
	})

	t.Run("replay", func(t *testing.T) {
		state := func(n string) tlc.ReplayState { return tlc.ReplayState{"n": {Kind: "integer", Atom: n}} }
		q := BugReplayRequest{Version: 1, ID: "request", InvocationID: "fresh", Path: tlc.ReplayPath{Offending: 0,
			States: []tlc.ReplayState{state("0"), state("1")},
			Steps:  []tlc.ReplayStep{{From: 0, To: 1, Action: "Increment", Bug: true}}}}
		calls := []BugReplayCall{{Index: 0, Action: "Increment", Before: state("0"), After: state("0"), Dispatched: true, Outcome: "refused"}}
		for name, record := range map[string]any{
			"replay-result.json":      BugReplayResult{Version: 1, RequestID: q.ID, InvocationID: q.InvocationID, Outcome: "refused", Initial: state("0"), Calls: calls},
			"replay-observation.json": BugReplayObservation{RequestID: q.ID, InvocationID: q.InvocationID, Initial: state("0"), Calls: calls, Started: true, Completed: true},
		} {
			b, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			write(records, name, string(b))
		}
		s := &commandReplaySource{dir: t.TempDir(), image: "replay-image", driver: "driver.ts", language: "typescript"}
		write(s.dir, "src/driver.ts", "export {};\n")
		result, observed, err := s.run(ctx, q)
		if err != nil || result.Outcome != "refused" || !observed.Completed {
			t.Fatalf("a replay's leftovers failed it: %v %+v %+v", err, result, observed)
		}
	})
}
