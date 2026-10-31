package dashboard_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/dashboard"
	"github.com/gitdek/invariant/internal/synth"
)

// recorded269Agent is a stand-in coding agent: each run writes what run
// says to its job's transcript.
type recorded269Agent struct {
	name string
	run  func(call int, job synth.Job) (synth.Usage, error)
	n    int
}

func (a *recorded269Agent) Name() string { return a.name }
func (a *recorded269Agent) Run(_ context.Context, job synth.Job) (synth.Usage, error) {
	a.n++
	return a.run(a.n, job)
}

type recorded269Fixture struct {
	work, workspace, dir string
	since                time.Time
}

func newRecorded269(t *testing.T, files map[string]string) *recorded269Fixture {
	t.Helper()
	f := &recorded269Fixture{work: t.TempDir(), workspace: t.TempDir(), since: time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)}
	f.dir = filepath.Join(f.work, "owner", "app", "issues", "issue-269", "build-opaque")
	for path, text := range files {
		recorded269Write(t, filepath.Join(f.workspace, filepath.FromSlash(path)), text)
	}
	if err := os.MkdirAll(f.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

func recorded269Write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *recorded269Fixture) transcript(t *testing.T) *os.File {
	t.Helper()
	out, err := os.Create(filepath.Join(f.dir, "transcript.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { out.Close() })
	return out
}

func (f *recorded269Fixture) at(second int) string {
	return f.since.Add(time.Duration(second) * time.Second).Format(time.RFC3339Nano)
}

func recorded269Line(t *testing.T, event map[string]any) string {
	t.Helper()
	b, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

// read is a shell read as Claude Code writes it: its call and its result.
func (f *recorded269Fixture) read(t *testing.T, id, command string, second int) string {
	t.Helper()
	return recorded269Line(t, map[string]any{"type": "assistant", "timestamp": f.at(second), "message": map[string]any{"id": "message-" + id,
		"content": []any{map[string]any{"type": "tool_use", "id": id, "name": "Bash", "input": map[string]any{"command": command}}}}}) +
		recorded269Line(t, map[string]any{"type": "user", "timestamp": f.at(second + 1), "message": map[string]any{
			"content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": "PRIVATE269_OUTPUT"}}}})
}

// codexRead is a shell read as Codex's backend writes it, with the item's
// id, which each run of Codex numbers from item_0.
func (f *recorded269Fixture) codexRead(t *testing.T, id, command string, second int) string {
	t.Helper()
	return recorded269Line(t, map[string]any{"type": "item.completed", "timestamp": f.at(second), "item": map[string]any{
		"id": id, "type": "command_execution", "command": command, "aggregated_output": "PRIVATE269_OUTPUT", "status": "completed"}})
}

func (f *recorded269Fixture) saved(t *testing.T) (dashboard.Live, string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.dir, "activity.json"))
	if err != nil {
		t.Fatalf("no saved activity: %v", err)
	}
	var v dashboard.Live
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v, string(b)
}

func recorded269Files(v dashboard.Live) map[string]dashboard.LiveFile {
	files := map[string]dashboard.LiveFile{}
	for _, file := range v.Files {
		file.Lit = false
		files[file.Path] = file
	}
	return files
}

func recorded269Fail(t *testing.T) func(string, ...any) {
	return func(format string, args ...any) { t.Errorf("recording activity: "+format, args...) }
}

// The record reads each event as the agent writes it, so a file the agent
// read and then removed in the same run, or one in a directory it left,
// keeps the path, read and size it showed then.
func TestRecordedActivityKeepsReadsOfFilesTheRunRemoved(t *testing.T) {
	const gone, kept = "PRIVATE269_REMOVED\n", "PRIVATE269_KEPT_A_WHILE_LONGER\n"
	f := newRecorded269(t, map[string]string{"old/gone.go": gone, "kept.go": kept})
	out := f.transcript(t)
	agent := &recorded269Agent{name: "claude-code", run: func(_ int, job synth.Job) (synth.Usage, error) {
		io.WriteString(job.Transcript, recorded269Line(t, map[string]any{"type": "system", "subtype": "init", "cwd": job.Workspace}))
		io.WriteString(job.Transcript, f.read(t, "first", "cd old && cat gone.go", 1))
		if err := os.RemoveAll(filepath.Join(job.Workspace, "old")); err != nil {
			t.Fatal(err)
		}
		io.WriteString(job.Transcript, f.read(t, "second", "cat kept.go", 3))
		return synth.Usage{}, nil
	}}
	backend := dashboard.RecordActivity(agent, recorded269Fail(t))
	if _, err := backend.Run(context.Background(), synth.Job{Workspace: f.workspace, Transcript: out}); err != nil {
		t.Fatal(err)
	}
	v, raw := f.saved(t)
	want := map[string]dashboard.LiveFile{
		"old/gone.go": {Path: "old/gone.go", Reads: 1, Size: int64(len(gone))},
		"kept.go":     {Path: "kept.go", Reads: 1, Size: int64(len(kept))},
	}
	if got := recorded269Files(v); !reflect.DeepEqual(got, want) || v.Tools["read"] != 2 || len(v.Tools) != 1 || len(v.Marks) != 2 {
		t.Errorf("saved activity %+v; want both reads, with the removed file's path and size", v)
	}
	for _, secret := range []string{"PRIVATE269", "cat ", f.workspace} {
		if strings.Contains(raw, secret) {
			t.Errorf("saved activity holds %q", secret)
		}
	}
}

// Every run of Codex numbers its items from item_0, so a fallback's items
// reuse the first run's ids. Each run's items count once, in the record and
// in the live view of the running step.
func TestCodexFallbackItemsCountOnceInEachRun(t *testing.T) {
	f := newRecorded269(t, map[string]string{"first.go": "package app\n", "second.go": "package app\n"})
	out := f.transcript(t)
	agent := &recorded269Agent{name: "codex", run: func(call int, job synth.Job) (synth.Usage, error) {
		io.WriteString(job.Transcript, recorded269Line(t, map[string]any{"type": "invariant.workspace", "cwd": job.Workspace, "timestamp": f.at(10 * call)}))
		io.WriteString(job.Transcript, recorded269Line(t, map[string]any{"type": "turn.started", "timestamp": f.at(10*call + 1)}))
		if call == 1 {
			io.WriteString(job.Transcript, f.codexRead(t, "item_0", "cat first.go", 12))
			return synth.Usage{}, synth.ErrThinkingLoop
		}
		io.WriteString(job.Transcript, f.codexRead(t, "item_0", "cat second.go", 22))
		return synth.Usage{}, nil
	}}
	if _, fallback, err := synth.RunAgent(context.Background(), dashboard.RecordActivity(agent, recorded269Fail(t)),
		synth.Job{Workspace: f.workspace, Transcript: out}, time.Minute, "xhigh"); err != nil || fallback == nil || agent.n != 2 {
		t.Fatalf("the fallback didn't run: %d runs, fallback %+v, %v", agent.n, fallback, err)
	}
	want := map[string]dashboard.LiveFile{"first.go": {Path: "first.go", Reads: 1, Size: 12}, "second.go": {Path: "second.go", Reads: 1, Size: 12}}
	saved, _ := f.saved(t)
	running := dashboard.Step{Issue: 269, Doing: "building", Since: f.since, Agent: "codex", Run: filepath.Base(f.dir)}
	if err := dashboard.WriteStatus(dashboard.StatusPath(f.work, "owner/app"), dashboard.Status{PID: os.Getpid(), Repo: "owner/app", Steps: []dashboard.Step{running}}); err != nil {
		t.Fatal(err)
	}
	s := &dashboard.Server{Work: f.work, Repos: []*dashboard.Repo{{Name: "owner/app", Status: dashboard.StatusPath(f.work, "owner/app")}}}
	b, err := s.LiveJSON(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var body struct{ Steps []dashboard.Live }
	if err := json.Unmarshal(b, &body); err != nil || len(body.Steps) != 1 || body.Steps[0].Ended != nil {
		t.Fatalf("live view %s, %v; want the running step", b, err)
	}
	for name, v := range map[string]dashboard.Live{"saved activity": saved, "running card": body.Steps[0]} {
		if got := recorded269Files(v); !reflect.DeepEqual(got, want) || v.Tools["read"] != 2 || len(v.Marks) != 2 || v.Turns != 2 {
			t.Errorf("%s %+v; want each run's read once, and two turns", name, v)
		}
	}
}

// The record reads a line once it's whole, however the backend writes it,
// and never the start of one a run left unfinished.
func TestRecordedActivityReadsOnlyWholeLines(t *testing.T) {
	f := newRecorded269(t, map[string]string{"split.go": "package app\n", "never.go": "package app\n"})
	out := f.transcript(t)
	agent := &recorded269Agent{name: "claude-code", run: func(_ int, job synth.Job) (synth.Usage, error) {
		io.WriteString(job.Transcript, recorded269Line(t, map[string]any{"type": "system", "subtype": "init", "cwd": job.Workspace}))
		read := f.read(t, "split", "cat split.go", 1)
		for _, part := range []string{read[:7], read[7 : len(read)/2], read[len(read)/2:]} {
			io.WriteString(job.Transcript, part)
		}
		unfinished := f.read(t, "never", "cat never.go", 3)
		io.WriteString(job.Transcript, unfinished[:strings.IndexByte(unfinished, '\n')-1])
		return synth.Usage{}, nil
	}}
	if _, err := dashboard.RecordActivity(agent, recorded269Fail(t)).Run(context.Background(), synth.Job{Workspace: f.workspace, Transcript: out}); err != nil {
		t.Fatal(err)
	}
	v, _ := f.saved(t)
	if files := recorded269Files(v); len(files) != 1 || files["split.go"].Reads != 1 || v.Tools["read"] != 1 || len(v.Marks) != 1 || v.Marks[0].Until == nil {
		t.Errorf("saved activity %+v; want the split read once, finished, and nothing of the unfinished line", v)
	}
}

// A build's agent runs share one time limit, whose clock stops while Codex
// waits for its account's turn (D-0154). Recording a run's activity must
// leave that in place: a run that waits longer than the whole limit for its
// turn still runs once its turn comes.
func TestRecordedCodexWaitsForItsTurnOffTheClock(t *testing.T) {
	f := newRecorded269(t, map[string]string{"app.go": "package app\n"})
	bin := filepath.Join(t.TempDir(), "codex")
	recorded269Write(t, filepath.Join(filepath.Dir(bin), "events.jsonl"), `{"type":"turn.started"}`+"\n"+
		`{"type":"item.completed","item":{"id":"item_0","type":"command_execution","command":"cat app.go","status":"completed"}}`+"\n"+
		`{"type":"turn.completed","usage":{"input_tokens":1,"cached_input_tokens":0,"output_tokens":1,"reasoning_output_tokens":0}}`+"\n")
	recorded269Write(t, bin, "#!/bin/sh\ncat \"$(dirname \"$0\")/events.jsonl\"\n")
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	// Another process holds Codex's turn for longer than the build's limit.
	lock, err := os.OpenFile(filepath.Join(home, "invariant.lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	const limit = 800 * time.Millisecond
	time.AfterFunc(limit+400*time.Millisecond, func() { lock.Close() })
	ctx, cancel := synth.WithExecutionBudget(context.Background(), limit)
	defer cancel()
	backend := dashboard.RecordActivity(synth.Codex{Binary: bin, Home: home, MaxTurns: 20}, recorded269Fail(t))
	out := f.transcript(t)
	if _, _, err := synth.RunAgent(ctx, backend, synth.Job{Workspace: f.workspace, Prompt: "build", GateServer: []string{"/bin/invariant"}, Transcript: out}, limit, ""); err != nil {
		t.Fatalf("waiting for Codex's turn spent the build's time limit: %v", err)
	}
	if v, _ := f.saved(t); len(v.Files) != 1 || v.Files[0].Path != "app.go" || v.Tools["read"] != 1 {
		t.Errorf("saved activity %+v; want the read of app.go", v)
	}
}
