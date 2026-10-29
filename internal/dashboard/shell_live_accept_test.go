package dashboard

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// These tests use the transcript formats the two backends already write and
// the public live JSON. No classifier helper or shell process is involved.
// One shell tool invocation contributes one count and one timed mark, even
// when its command contains several simple commands.
type shell211Fixture struct {
	server     *Server
	workspace  string
	transcript string
	provider   string
	since      time.Time
	now        time.Time
}

func shell211JSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

func newShell211Fixture(t *testing.T, provider string) *shell211Fixture {
	t.Helper()
	work, ws := t.TempDir(), t.TempDir()
	f := &shell211Fixture{workspace: ws, provider: provider,
		since: time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC), now: time.Now()}
	status := StatusPath(work, "owner/app")
	if err := WriteStatus(status, Status{PID: os.Getpid(), Repo: "owner/app", Started: f.since, Heartbeat: f.since,
		Every: 30, Steps: []Step{{Issue: 211, Doing: "building", Since: f.since, Agent: provider}}}); err != nil {
		t.Fatal(err)
	}
	f.transcript = filepath.Join(work, "owner", "app", "issues", "issue-211", "build-20261029-100000", "transcript.jsonl")
	if err := os.MkdirAll(filepath.Dir(f.transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	f.server = &Server{Work: work, Repos: []*Repo{{Name: "owner/app", Status: status}}}
	init := map[string]any{"type": "system", "subtype": "init", "cwd": ws}
	if provider == "codex" {
		init = map[string]any{"type": "invariant.workspace", "cwd": ws, "timestamp": f.at(0)}
	}
	f.append(t, shell211JSON(t, init))
	return f
}

func (f *shell211Fixture) at(seconds int) string {
	return f.since.Add(time.Duration(seconds) * time.Second).Format(time.RFC3339Nano)
}

func (f *shell211Fixture) file(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(f.workspace, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("PRIVATE_FILE_CONTENT\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func (f *shell211Fixture) append(t *testing.T, text string) {
	t.Helper()
	out, err := os.OpenFile(f.transcript, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := out.WriteString(text)
	closeErr := out.Close()
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
}

func (f *shell211Fixture) start(t *testing.T, id, command string, sec int) string {
	t.Helper()
	if f.provider == "codex" {
		return shell211JSON(t, map[string]any{"type": "item.started", "timestamp": f.at(sec),
			"item": map[string]any{"id": id, "type": "command_execution", "command": command, "status": "in_progress"}})
	}
	return shell211JSON(t, map[string]any{"type": "assistant", "timestamp": f.at(sec),
		"message": map[string]any{"id": "message-" + id, "content": []map[string]any{{
			"type": "tool_use", "id": id, "name": "Bash",
			"input": map[string]any{"command": command, "description": "PRIVATE_DESCRIPTION"},
		}}}})
}

func (f *shell211Fixture) done(t *testing.T, id, command string, sec int) string {
	t.Helper()
	if f.provider == "codex" {
		return shell211JSON(t, map[string]any{"type": "item.completed", "timestamp": f.at(sec),
			"item": map[string]any{"id": id, "type": "command_execution", "command": command,
				"aggregated_output": "PRIVATE_TOOL_OUTPUT", "exit_code": 0, "status": "completed"}})
	}
	return shell211JSON(t, map[string]any{"type": "user", "timestamp": f.at(sec),
		"message": map[string]any{"content": []map[string]any{{"type": "tool_result", "tool_use_id": id, "content": "PRIVATE_TOOL_OUTPUT"}}}})
}

func (f *shell211Fixture) call(t *testing.T, id, command string, sec int) {
	t.Helper()
	f.append(t, f.start(t, id, command, sec)+f.done(t, id, command, sec+2))
}

func (f *shell211Fixture) read(t *testing.T) Live {
	t.Helper()
	f.now = f.now.Add(3 * time.Second)
	b, err := f.server.LiveJSON(f.now)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Steps []Live `json:"steps"`
	}
	if err := json.Unmarshal(b, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Steps) != 1 {
		t.Fatalf("live steps = %+v; want the one running step", body.Steps)
	}
	return body.Steps[0]
}

func shell211Files(l Live) map[string][2]int {
	out := map[string][2]int{}
	for _, f := range l.Files {
		out[f.Path] = [2]int{f.Reads, f.Edits}
	}
	return out
}

func shell211Mark(t *testing.T, f *shell211Fixture, m Mark, kind string, start, end int) {
	t.Helper()
	if m.Kind != kind || !m.At.Equal(f.since.Add(time.Duration(start)*time.Second)) {
		t.Errorf("mark = %+v; want %s starting at %s", m, kind, f.at(start))
	}
	if end < 0 {
		if m.Until != nil {
			t.Errorf("pending %s ended at %v", kind, m.Until)
		}
	} else if m.Until == nil || !m.Until.Equal(f.since.Add(time.Duration(end)*time.Second)) {
		t.Errorf("%s ended at %v; want %s", kind, m.Until, f.at(end))
	}
}

// The bounded command vocabulary includes common Go, Rust, JavaScript and
// Python builds/tests, literal bare shell wrappers, and local cd.
// Assignment prefixes and path-named commands are opaque, even when their
// command names look familiar: they make the whole invocation other.
// Compound invocations with one kind keep it; mixed or opaque invocations
// remain other, with no claim about time spent on individual subcommands.
func TestShellCallsHaveKindsAndTimes(t *testing.T) {
	cases := []struct{ command, kind string }{
		{`cat internal/app.go`, "read"},
		{`head -n 5 -- internal/app.go`, "read"},
		{`tail -n 5 internal/app.go`, "read"},
		{`sed -n '1,20p' internal/app.go`, "read"},
		{`rg -n --glob '*.go' 'PRIVATE_PATTERN' internal/app.go`, "search"},
		{`grep -n -e 'PRIVATE_PATTERN' -- internal/app.go`, "search"},
		{`ls -la internal`, "list"},
		{`pwd`, "list"},
		{`find internal -type f -name '*.go'`, "list"},
		{`rg --files internal`, "list"},
		{`go build ./...`, "build"},
		{`make all`, "build"},
		{`cargo build --release`, "build"},
		{`npm run build`, "build"},
		{`go test ./...`, "test"},
		{`cargo test --quiet`, "test"},
		{`npm test -- --runInBand`, "test"},
		{`pytest -q tests`, "test"},
		{`python -m pytest -q`, "test"},
		{`git -C . status --short`, "git"},
		{`git diff --stat`, "git"},
		{`zsh -lc 'cat internal/app.go'`, "read"},
		{`bash -c 'rg -n PRIVATE_PATTERN internal/app.go'`, "search"},
		{`sh -c 'go test ./...'`, "test"},
		{`/bin/zsh -lc 'cat internal/app.go'`, "other"},
		{`/bin/bash -c 'rg -n PRIVATE_PATTERN internal/app.go'`, "other"},
		{`/bin/sh -c 'go test ./...'`, "other"},
		{`env LC_ALL=C go test ./...`, "other"},
		{`CGO_ENABLED=0 go build ./...`, "other"},
		{`cd internal && cat app.go`, "read"},
		{`cat internal/app.go && head -n 3 internal/app.go`, "read"},
		{`cat internal/app.go | head -n 3`, "read"},
		{`go build ./... && go test ./...`, "other"},
		{`printf 'go test ./...; cat internal/app.go'`, "other"},
		{`python -c 'print("cat internal/app.go")'`, "other"},
		{`./custom-build-script`, "other"},
		{`sed -i 's/old/new/' internal/app.go`, "other"},
		{`find internal -type f -delete`, "other"},
		{`find internal -exec cat {} \;`, "other"},
		{`cat "unterminated`, "other"},
		{`cat $(printf internal/app.go)`, "other"},
		{`cat *.go`, "other"},
		{`cat internal/app.go > OUTPUT_CANARY`, "other"},
	}
	for _, provider := range []string{"codex", "claude-code"} {
		t.Run(provider, func(t *testing.T) {
			f := newShell211Fixture(t, provider)
			f.file(t, "internal/app.go")
			want := map[string]int{}
			for i, c := range cases {
				f.call(t, fmt.Sprintf("call-%d", i), c.command, 10*i+1)
				want[c.kind]++
			}
			l := f.read(t)
			if !reflect.DeepEqual(l.Tools, want) {
				t.Errorf("counts = %v; want %v, one count per shell invocation", l.Tools, want)
			}
			if len(l.Marks) != len(cases) {
				t.Fatalf("%d marks for %d invocations", len(l.Marks), len(cases))
			}
			for i, c := range cases {
				t.Logf("transcript command: %s", c.command)
				shell211Mark(t, f, l.Marks[i], c.kind, 10*i+1, 10*i+3)
			}
		})
	}
}

// Paths come from explicit literal file operands, never search patterns,
// option arguments, glob expansion or output. Directories and nonexistent
// operands are not reported as files, and symlinks may not escape the copy.
// Each known file is touched once per invocation, even if named twice.
func TestShellReadsAndSearchesPublishOnlyWorkspaceFiles(t *testing.T) {
	for _, provider := range []string{"codex", "claude-code"} {
		t.Run(provider, func(t *testing.T) {
			f := newShell211Fixture(t, provider)
			app := f.file(t, "internal/app.go")
			f.file(t, "docs/with space.md")
			f.file(t, "docs/semi;colon.md")
			f.file(t, "PATTERN_CANARY")
			f.file(t, "GLOB_CANARY")
			f.file(t, "GLOB_EXPANSION_CANARY.go")
			f.file(t, "OUTPUT_CANARY")
			outside := filepath.Join(t.TempDir(), "OUTSIDE_CANARY")
			if err := os.WriteFile(outside, []byte("PRIVATE_OUTSIDE_CONTENT"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(f.workspace, "ESCAPING_LINK")); err != nil {
				t.Fatal(err)
			}
			relOutside, err := filepath.Rel(f.workspace, outside)
			if err != nil {
				t.Fatal(err)
			}
			commands := []string{
				`cat "internal/app.go" "internal/app.go" 'docs/with space.md'`,
				`rg -n --glob GLOB_CANARY -e PATTERN_CANARY -- internal/app.go 'docs/with space.md'`,
				`sh -lc 'cd internal && sed -n "1,20p" app.go'`,
				`cat "` + app + `"`,
				`cat 'docs/semi;colon.md'`,
				`grep -n PATTERN_CANARY -- internal/app.go`,
				`cat "` + outside + `" "` + relOutside + `" ESCAPING_LINK MISSING_CANARY`,
				`rg -n PATTERN_CANARY internal`,
				`cat *.go`,
				`printf 'internal/app.go' > OUTPUT_CANARY`,
				// Redirections are outside this slice's supported syntax.
				`cat internal/app.go > OUTPUT_CANARY`,
				`cat $(touch "` + filepath.Join(f.workspace, "EXECUTION_CANARY") + `")`,
				// cd in the earlier invocation must not change the next one's base.
				`head -n 1 internal/app.go`,
			}
			for i, command := range commands {
				f.call(t, fmt.Sprintf("paths-%d", i), command, i*10+1)
			}
			l := f.read(t)
			want := map[string][2]int{"internal/app.go": {6, 0}, "docs/with space.md": {2, 0}, "docs/semi;colon.md": {1, 0}}
			if got := shell211Files(l); !reflect.DeepEqual(got, want) {
				t.Errorf("files = %v; want %v", got, want)
			}
			for _, file := range l.Files {
				if file.Size != int64(len("PRIVATE_FILE_CONTENT\n")) {
					t.Errorf("%s size = %d; want the known file's size", file.Path, file.Size)
				}
			}
			if _, err := os.Stat(filepath.Join(f.workspace, "EXECUTION_CANARY")); !os.IsNotExist(err) {
				t.Errorf("reading a transcript executed its command: probe stat error = %v", err)
			}
			rec := httptest.NewRecorder()
			f.server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/live.json", nil))
			if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("public live response: %d, cache %q", rec.Code, rec.Header().Get("Cache-Control"))
			}
			body := rec.Body.String()
			for _, private := range []string{"PRIVATE_", "PATTERN_CANARY", "GLOB_CANARY", "OUTPUT_CANARY", "OUTSIDE_CANARY",
				"ESCAPING_LINK", "MISSING_CANARY", "EXECUTION_CANARY", f.workspace, outside, `"command"`, `"commands"`, `"input"`, `"output_text"`} {
				if strings.Contains(body, private) {
					t.Errorf("public response contains private command material %q", private)
				}
			}
			var wire struct {
				Steps []Live `json:"steps"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &wire); err != nil || len(wire.Steps) != 1 {
				t.Fatalf("public JSON: %s, decode error %v", body, err)
			}
			if got := shell211Files(wire.Steps[0]); !reflect.DeepEqual(got, want) {
				t.Errorf("public files = %v; want %v", got, want)
			}
		})
	}
}

// Polling stays incremental: an unfinished JSON line contributes nothing,
// finishing it adds one call, its result supplies its end, and later polls
// do not rediscover operands or count the call again. Codex completion-only
// items have an honest zero-duration mark when no start was recorded.
func TestShellActivityUsesOnlyNewWholeTranscriptEvents(t *testing.T) {
	for _, provider := range []string{"codex", "claude-code"} {
		t.Run(provider, func(t *testing.T) {
			f := newShell211Fixture(t, provider)
			f.file(t, "internal/app.go")
			f.file(t, "later.go")
			f.call(t, "first", `cat internal/app.go`, 1)
			next := f.start(t, "second", `rg -n PRIVATE_PATTERN later.go`, 10)
			cut := len(next) / 2
			f.append(t, next[:cut])
			l := f.read(t)
			if !reflect.DeepEqual(l.Tools, map[string]int{"read": 1}) || len(l.Marks) != 1 || !reflect.DeepEqual(shell211Files(l), map[string][2]int{"internal/app.go": {1, 0}}) {
				t.Errorf("before partial event completes: counts %v, marks %+v, files %v", l.Tools, l.Marks, shell211Files(l))
			}
			f.append(t, next[cut:])
			l = f.read(t)
			wantTools := map[string]int{"read": 1, "search": 1}
			wantFiles := map[string][2]int{"internal/app.go": {1, 0}, "later.go": {1, 0}}
			if !reflect.DeepEqual(l.Tools, wantTools) || !reflect.DeepEqual(shell211Files(l), wantFiles) || l.Now != "search" {
				t.Errorf("after start: counts %v, files %v, now %q; want %v, %v, searching", l.Tools, shell211Files(l), l.Now, wantTools, wantFiles)
			}
			if len(l.Marks) != 2 {
				t.Fatalf("marks = %+v; want two calls", l.Marks)
			}
			shell211Mark(t, f, l.Marks[1], "search", 10, -1)
			if provider == "claude-code" {
				f.append(t, shell211JSON(t, map[string]any{"type": "tool_progress", "tool_name": "Bash", "tool_use_id": "second", "elapsed_time_seconds": 1}))
				if progress := f.read(t); progress.Now != "search" {
					t.Errorf("Bash progress changed searching to %q", progress.Now)
				}
			}
			f.append(t, f.done(t, "second", `rg -n PRIVATE_PATTERN later.go`, 14))
			l = f.read(t)
			if len(l.Marks) != 2 {
				t.Fatalf("completion duplicated a call: %+v", l.Marks)
			}
			shell211Mark(t, f, l.Marks[1], "search", 10, 14)
			// Newly appearing operands must not be inferred by rereading old calls.
			f.call(t, "missing", `cat appears_later.go`, 20)
			l = f.read(t)
			f.file(t, "appears_later.go")
			for i := 0; i < 2; i++ {
				again := f.read(t)
				if !reflect.DeepEqual(again.Tools, l.Tools) || !reflect.DeepEqual(again.Marks, l.Marks) || !reflect.DeepEqual(shell211Files(again), wantFiles) {
					t.Errorf("unchanged transcript changed activity: %+v", again)
				}
			}
			if provider == "codex" {
				f.append(t, f.done(t, "completion-only", `go build ./...`, 30))
				l = f.read(t)
				if l.Tools["build"] != 1 || len(l.Marks) != 4 {
					t.Fatalf("completion-only build: counts %v, marks %+v", l.Tools, l.Marks)
				}
				shell211Mark(t, f, l.Marks[3], "build", 30, 30)
			}
		})
	}
}

// Results close the invocation they belong to. An unrelated user message
// or result must not stop a tool, and one Claude result message can finish
// several tools. Existing ID-less transcript results retain compatibility.
func TestOverlappingShellCallsKeepTheirOwnTimes(t *testing.T) {
	for _, provider := range []string{"codex", "claude-code"} {
		t.Run(provider, func(t *testing.T) {
			f := newShell211Fixture(t, provider)
			f.file(t, "internal/app.go")
			f.append(t, f.start(t, "slow", `go build ./...`, 1))
			f.append(t, f.start(t, "fast", `cat internal/app.go`, 2))
			if provider == "claude-code" {
				f.append(t, shell211JSON(t, map[string]any{"type": "user", "timestamp": f.at(3), "message": map[string]any{"content": "PRIVATE_USER_WORDS"}}))
				f.append(t, f.done(t, "unknown-id", "", 4))
				pending := f.read(t)
				if len(pending.Marks) != 2 {
					t.Fatalf("pending marks = %+v", pending.Marks)
				}
				shell211Mark(t, f, pending.Marks[0], "build", 1, -1)
				shell211Mark(t, f, pending.Marks[1], "read", 2, -1)
			}
			f.append(t, f.done(t, "fast", `cat internal/app.go`, 5))
			l := f.read(t)
			if len(l.Marks) != 2 {
				t.Fatalf("marks = %+v; want two calls", l.Marks)
			}
			shell211Mark(t, f, l.Marks[0], "build", 1, -1)
			shell211Mark(t, f, l.Marks[1], "read", 2, 5)
			f.append(t, f.done(t, "slow", `go build ./...`, 9))
			l = f.read(t)
			shell211Mark(t, f, l.Marks[0], "build", 1, 9)
			if !reflect.DeepEqual(l.Tools, map[string]int{"build": 1, "read": 1}) {
				t.Errorf("overlapping calls counted %v", l.Tools)
			}
			if provider == "claude-code" {
				f.append(t, f.start(t, "a", `go test ./...`, 10)+f.start(t, "b", `git status`, 11))
				f.append(t, shell211JSON(t, map[string]any{"type": "user", "timestamp": f.at(15), "message": map[string]any{"content": []map[string]any{
					{"type": "tool_result", "tool_use_id": "b", "content": "PRIVATE_RESULT_B"},
					{"type": "tool_result", "tool_use_id": "a", "content": "PRIVATE_RESULT_A"},
				}}}))
				l = f.read(t)
				if len(l.Marks) != 4 {
					t.Fatalf("batched results: %+v", l.Marks)
				}
				shell211Mark(t, f, l.Marks[2], "test", 10, 15)
				shell211Mark(t, f, l.Marks[3], "git", 11, 15)
				f.append(t, f.start(t, "legacy", `ls internal`, 20))
				f.append(t, shell211JSON(t, map[string]any{"type": "user", "timestamp": f.at(21), "message": map[string]any{"content": []map[string]any{
					{"type": "tool_result", "content": "PRIVATE_LEGACY_RESULT"},
				}}}))
				l = f.read(t)
				if len(l.Marks) != 5 {
					t.Fatalf("legacy result: %+v", l.Marks)
				}
				shell211Mark(t, f, l.Marks[4], "list", 20, 21)
			}
		})
	}
}
