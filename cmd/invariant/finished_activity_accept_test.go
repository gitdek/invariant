package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/dashboard"
	"github.com/gitdek/invariant/internal/synth"
)

// RecordActivity is the watcher backend decorator. It preserves Name, Usage,
// errors and panics, and saves activity.json beside the primary transcript
// before Run returns to the caller that removes the workspace. The snapshot
// contains only the existing public projection, never private shell commands.
// It records activity, not the factory step's end: checking and publishing can
// continue after the backend returns. Review transcripts cannot replace it.
// The missing API deliberately produces a compile failure on the old base,
// isolated here from the existing dashboard and factory behavioral tests.
type activity269Backend struct {
	provider string
	run      func(context.Context, synth.Job) (synth.Usage, error)
}

func (b activity269Backend) Name() string { return b.provider }
func (b activity269Backend) Run(ctx context.Context, job synth.Job) (synth.Usage, error) {
	return b.run(ctx, job)
}

type activity269Fixture struct {
	work, workspace, dir, provider string
	since                          time.Time
}

func activity269New(t *testing.T, provider string) *activity269Fixture {
	t.Helper()
	f := &activity269Fixture{work: t.TempDir(), workspace: t.TempDir(), provider: provider,
		since: time.Date(2026, 10, 30, 0, 21, 0, 123456789, time.UTC)}
	f.dir = filepath.Join(f.work, "owner", "app", "issues", "issue-269", "current-opaque-run")
	if err := os.MkdirAll(f.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

func activity269Write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func activity269Open(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func activity269Event(t *testing.T, out io.Writer, event any) {
	t.Helper()
	if err := json.NewEncoder(out).Encode(event); err != nil {
		t.Fatal(err)
	}
}

func (f *activity269Fixture) begin(t *testing.T, job synth.Job) {
	t.Helper()
	event := map[string]any{"type": "system", "subtype": "init", "cwd": job.Workspace, "model": "PRIVATE269_MODEL"}
	if f.provider == "codex" {
		event["type"] = "invariant.workspace"
	}
	activity269Event(t, job.Transcript, event)
}

func (f *activity269Fixture) call(t *testing.T, job synth.Job, id, command string, second int) {
	t.Helper()
	at := func(n int) string { return f.since.Add(time.Duration(n) * time.Second).Format(time.RFC3339Nano) }
	if f.provider == "codex" {
		activity269Event(t, job.Transcript, map[string]any{"type": "item.started", "timestamp": at(second),
			"item": map[string]any{"id": id, "type": "command_execution", "command": command}})
		activity269Event(t, job.Transcript, map[string]any{"type": "item.completed", "timestamp": at(second + 1),
			"item": map[string]any{"id": id, "type": "command_execution", "command": command,
				"aggregated_output": "PRIVATE269_OUTPUT", "status": "completed", "exit_code": 0}})
		return
	}
	activity269Event(t, job.Transcript, map[string]any{"type": "assistant", "timestamp": at(second),
		"message": map[string]any{"id": "message-" + id, "content": []any{map[string]any{
			"type": "tool_use", "id": id, "name": "Bash", "input": map[string]any{
				"command": command, "description": "PRIVATE269_DESCRIPTION"}}}}})
	activity269Event(t, job.Transcript, map[string]any{"type": "user", "timestamp": at(second + 1),
		"message": map[string]any{"content": []any{map[string]any{
			"type": "tool_result", "tool_use_id": id, "content": "PRIVATE269_OUTPUT"}}}})
}

func (f *activity269Fixture) finishStatus(t *testing.T) time.Time {
	t.Helper()
	ended := f.since.Add(24 * time.Minute)
	// End is supplied explicitly by the watcher after its entire step, not
	// by the recorder or the transcript's last event. The watcher is stopped.
	status := map[string]any{"pid": 0, "repo": "owner/app", "limit": 300,
		"finished": []any{map[string]any{"issue": 269, "doing": "building", "since": f.since,
			"agent": f.provider, "run": filepath.Base(f.dir), "ended": ended, "limit": 9000}}}
	b, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	activity269Write(t, dashboard.StatusPath(f.work, "owner/app"), string(b))
	return ended
}

func (f *activity269Fixture) card(t *testing.T, hour int) (dashboard.Live, string) {
	t.Helper()
	// Every call is a cold dashboard with no transcript or page cache.
	s := &dashboard.Server{Work: f.work, Repos: []*dashboard.Repo{
		{Name: "owner/app", Status: dashboard.StatusPath(f.work, "owner/app")}}}
	b, err := s.LiveJSON(f.since.Add(time.Duration(hour) * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Steps []dashboard.Live `json:"steps"`
	}
	if err := json.Unmarshal(b, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Steps) != 1 {
		t.Fatalf("cold dashboard has %d cards, want the recorded completion: %s", len(body.Steps), b)
	}
	return body.Steps[0], string(b)
}

func activity269Privacy(t *testing.T, raw string, forbidden ...string) {
	t.Helper()
	for _, secret := range append([]string{"PRIVATE269", `"command"`, `"commands"`, "cat ", "ESCAPING269_LINK", "MISSING269_FILE", "OUTSIDE269_SECRET"}, forbidden...) {
		if strings.Contains(raw, secret) {
			t.Errorf("saved or public activity leaked %q", secret)
		}
	}
}

func activity269Invoke(backend synth.Backend, ctx context.Context, job synth.Job) (usage synth.Usage, err error, panicked any) {
	defer func() { panicked = recover() }()
	usage, err = backend.Run(ctx, job)
	return
}

func TestFinishedActivitySurvivesWorkspaceCleanupWithoutPageReads269(t *testing.T) {
	for _, provider := range []string{"claude-code", "codex"} {
		for _, mode := range []string{"success", "error", "cancel", "panic"} {
			t.Run(provider+"/"+mode, func(t *testing.T) {
				f := activity269New(t, provider)
				content := "package hidden // PRIVATE269_CODE\n"
				activity269Write(t, filepath.Join(f.workspace, "src", "inside.go"), content)
				outside := filepath.Join(t.TempDir(), "OUTSIDE269_SECRET")
				activity269Write(t, outside, "PRIVATE269_OUTSIDE_CONTENT")
				if err := os.Symlink(outside, filepath.Join(f.workspace, "ESCAPING269_LINK")); err != nil {
					t.Fatal(err)
				}
				out := activity269Open(t, filepath.Join(f.dir, "transcript.jsonl"))
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				failure := errors.New("PRIVATE269_BACKEND_ERROR")
				wantUsage := synth.Usage{Turns: 4, Model: "PRIVATE269_MODEL", Summary: "PRIVATE269_SUMMARY"}
				backend := dashboard.RecordActivity(activity269Backend{provider, func(ctx context.Context, job synth.Job) (synth.Usage, error) {
					f.begin(t, job)
					for i, command := range []string{"cd src && cat inside.go", "cat ESCAPING269_LINK", "cat '" + outside + "'", "cat MISSING269_FILE"} {
						f.call(t, job, fmt.Sprintf("call-%d", i), command, 2*i+1)
					}
					switch mode {
					case "error":
						return wantUsage, failure
					case "cancel":
						cancel()
						return wantUsage, ctx.Err()
					case "panic":
						panic("PRIVATE269_PANIC")
					}
					return wantUsage, nil
				}}, func(format string, args ...any) { t.Errorf("recording activity: "+format, args...) })
				if backend.Name() != provider {
					t.Fatalf("decorator changed provider to %q", backend.Name())
				}
				usage, err, panicked := activity269Invoke(backend, ctx, synth.Job{Workspace: f.workspace, Transcript: out})
				if mode == "panic" {
					if panicked != "PRIVATE269_PANIC" {
						t.Fatalf("decorator changed panic: %v", panicked)
					}
				} else if panicked != nil || !reflect.DeepEqual(usage, wantUsage) {
					t.Fatalf("decorator changed backend result: usage %+v, panic %v", usage, panicked)
				}
				if mode == "error" && !errors.Is(err, failure) || mode == "cancel" && !errors.Is(err, context.Canceled) || mode == "success" && err != nil {
					t.Fatalf("decorator changed %s error: %v", mode, err)
				}
				if err := out.Close(); err != nil {
					t.Fatal(err)
				}
				// No page has read the transcript. The recorder must already have
				// saved the safe paths while existence/symlink checks were possible.
				snapshot, err := os.ReadFile(filepath.Join(f.dir, "activity.json"))
				if err != nil {
					t.Fatalf("backend return lost its pre-cleanup activity: %v", err)
				}
				activity269Privacy(t, string(snapshot), f.workspace, f.work, outside)
				var saved dashboard.Live
				if err := json.Unmarshal(snapshot, &saved); err != nil {
					t.Fatal(err)
				}
				if saved.Ended != nil {
					t.Error("backend recorder invented the factory step's completion time")
				}
				if err := os.RemoveAll(f.workspace); err != nil {
					t.Fatal(err)
				}
				ended := f.finishStatus(t)
				var before dashboard.Live
				for _, hour := range []int{7, 23} {
					card, raw := f.card(t, hour)
					if card.Run != filepath.Base(f.dir) || card.Issue != 269 || card.By != provider || card.Since == nil || !card.Since.Equal(f.since) || card.Ended == nil || !card.Ended.Equal(ended) || card.Limit != 9000 || card.Now != "" {
						t.Errorf("completion lost its original identity or clock: %+v", card)
					}
					if len(card.Files) != 1 || card.Files[0].Path != "src/inside.go" || card.Files[0].Reads != 1 || card.Files[0].Size != int64(len(content)) {
						t.Errorf("deleted workspace lost safe file counts/size or admitted another path: %+v", card.Files)
					}
					if card.Tools["read"] != 4 || len(card.Tools) != 1 || len(card.Marks) != 4 {
						t.Errorf("deleted directory changed command classification/counts: tools %v, marks %+v", card.Tools, card.Marks)
					}
					for i, mark := range card.Marks {
						wantAt := f.since.Add(time.Duration(2*i+1) * time.Second)
						if mark.Kind != "read" || !mark.At.Equal(wantAt) || mark.Until == nil || !mark.Until.Equal(wantAt.Add(time.Second)) {
							t.Errorf("recorded tool %d changed after cleanup: %+v", i, mark)
						}
					}
					activity269Privacy(t, raw, f.workspace, f.work, outside)
					if hour == 23 && !reflect.DeepEqual(before, card) {
						t.Error("a later cold dashboard changed the retained activity")
					}
					before = card
				}
			})
		}
	}
}

func TestFinishedActivityKeepsFallbackCountsAndIgnoresReview269(t *testing.T) {
	for _, provider := range []string{"claude-code", "codex"} {
		t.Run(provider, func(t *testing.T) {
			f := activity269New(t, provider)
			for _, name := range []string{"first.go", "second.go"} {
				activity269Write(t, filepath.Join(f.workspace, name), "PRIVATE269_FILE\n")
			}
			out := activity269Open(t, filepath.Join(f.dir, "transcript.jsonl"))
			calls := 0
			backend := dashboard.RecordActivity(activity269Backend{provider, func(_ context.Context, job synth.Job) (synth.Usage, error) {
				f.begin(t, job)
				if job.ReadOnly {
					f.call(t, job, "review", "cat review.go", 20)
					return synth.Usage{}, nil
				}
				calls++
				if calls == 1 {
					if job.Effort != "" {
						t.Errorf("first attempt's effort changed: %q", job.Effort)
					}
					f.call(t, job, "first", "cat first.go", 1)
					return synth.Usage{}, synth.ErrThinkingLoop
				}
				if job.Effort != "xhigh" {
					t.Errorf("fallback effort changed: %q", job.Effort)
				}
				f.call(t, job, "second", "cat second.go", 3)
				return synth.Usage{}, nil
			}}, func(format string, args ...any) { t.Errorf("recording activity: "+format, args...) })
			_, fallback, err := synth.RunAgent(context.Background(), backend,
				synth.Job{Workspace: f.workspace, Transcript: out}, time.Minute, "xhigh")
			if err != nil || fallback == nil || calls != 2 {
				t.Fatalf("fallback did not retain its behavior: calls %d, fallback %+v, error %v", calls, fallback, err)
			}
			if err := out.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(f.dir, "activity.json")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			reviewWorkspace := t.TempDir()
			activity269Write(t, filepath.Join(reviewWorkspace, "review.go"), "PRIVATE269_REVIEW\n")
			review := activity269Open(t, filepath.Join(f.dir, "review-transcript.jsonl"))
			if _, err := backend.Run(context.Background(), synth.Job{Workspace: reviewWorkspace, Transcript: review, ReadOnly: true}); err != nil {
				t.Fatal(err)
			}
			if err := review.Close(); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("review replaced the build's saved activity: error %v", err)
			}
			if err := os.RemoveAll(f.workspace); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(reviewWorkspace); err != nil {
				t.Fatal(err)
			}
			f.finishStatus(t)
			card, raw := f.card(t, 7)
			files := map[string]int{}
			for _, file := range card.Files {
				files[file.Path] = file.Reads
			}
			if card.Tools["read"] != 2 || len(card.Marks) != 2 || !reflect.DeepEqual(files, map[string]int{"first.go": 1, "second.go": 1}) {
				t.Errorf("fallback lost or double-counted events, or review replaced them: %+v", card)
			}
			activity269Privacy(t, string(after), f.work, f.workspace, reviewWorkspace)
			activity269Privacy(t, raw, f.work, f.workspace, reviewWorkspace, "review.go")
		})
	}
}

func TestFinishedActivityPreservesSanitizedReadsAcrossFallbackMutation269(t *testing.T) {
	for _, provider := range []string{"claude-code", "codex"} {
		t.Run(provider, func(t *testing.T) {
			f := activity269New(t, provider)
			contents := map[string]string{
				"gone/read.go":  "PRIVATE269_DELETED_CONTENT\n",
				"old/nested.go": "PRIVATE269_RENAMED_CONTENT_WITH_A_DIFFERENT_SIZE\n",
				"shared.go":     "PRIVATE269_SHARED_CONTENT\n",
			}
			for path, content := range contents {
				activity269Write(t, filepath.Join(f.workspace, path), content)
			}
			out := activity269Open(t, filepath.Join(f.dir, "transcript.jsonl"))
			calls := 0
			var first dashboard.Live
			backend := dashboard.RecordActivity(activity269Backend{provider, func(_ context.Context, job synth.Job) (synth.Usage, error) {
				calls++
				f.begin(t, job)
				if calls == 1 {
					for i, command := range []string{"cd gone && cat read.go", "cd gone && cat read.go", "cd old && cat nested.go", "cat shared.go"} {
						f.call(t, job, fmt.Sprintf("first-%d", i), command, 2*i+1)
					}
					return synth.Usage{}, synth.ErrThinkingLoop
				}
				// These paths, sizes and classifications already passed the
				// workspace checks before the fallback deletes and renames them.
				snapshot, err := os.ReadFile(filepath.Join(f.dir, "activity.json"))
				if err != nil {
					t.Fatalf("first attempt did not save activity before fallback: %v", err)
				}
				if err := json.Unmarshal(snapshot, &first); err != nil {
					t.Fatal(err)
				}
				if first.Tools["read"] != 4 || len(first.Tools) != 1 || len(first.Marks) != 4 || len(first.Files) != 3 {
					t.Fatalf("first attempt did not save its complete sanitized activity: %+v", first)
				}
				activity269Privacy(t, string(snapshot), f.workspace, f.work)
				if err := os.RemoveAll(filepath.Join(f.workspace, "gone")); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(filepath.Join(f.workspace, "old"), filepath.Join(f.workspace, "renamed")); err != nil {
					t.Fatal(err)
				}
				f.call(t, job, "fallback-renamed", "cd renamed && cat nested.go", 9)
				f.call(t, job, "fallback-shared", "cat shared.go", 11)
				return synth.Usage{}, nil
			}}, func(format string, args ...any) { t.Errorf("recording activity: "+format, args...) })
			_, fallback, err := synth.RunAgent(context.Background(), backend,
				synth.Job{Workspace: f.workspace, Transcript: out}, time.Minute, "xhigh")
			if err != nil || fallback == nil || calls != 2 {
				t.Fatalf("fallback changed behavior: calls %d, fallback %+v, error %v", calls, fallback, err)
			}
			if err := out.Close(); err != nil {
				t.Fatal(err)
			}
			snapshot, err := os.ReadFile(filepath.Join(f.dir, "activity.json"))
			if err != nil {
				t.Fatal(err)
			}
			activity269Privacy(t, string(snapshot), f.workspace, f.work)
			if err := os.RemoveAll(f.workspace); err != nil {
				t.Fatal(err)
			}
			f.finishStatus(t)
			var saved dashboard.Live
			if err := json.Unmarshal(snapshot, &saved); err != nil {
				t.Fatal(err)
			}
			card, raw := f.card(t, 7)
			later, _ := f.card(t, 23)
			if !reflect.DeepEqual(card, later) {
				t.Error("a later cold dashboard changed fallback activity")
			}
			for _, activity := range []dashboard.Live{saved, card} {
				files := map[string]dashboard.LiveFile{}
				for _, file := range activity.Files {
					file.Lit = false
					files[file.Path] = file
				}
				wantFiles := map[string]dashboard.LiveFile{
					"gone/read.go":      {Path: "gone/read.go", Reads: 2, Size: int64(len(contents["gone/read.go"]))},
					"old/nested.go":     {Path: "old/nested.go", Reads: 1, Size: int64(len(contents["old/nested.go"]))},
					"renamed/nested.go": {Path: "renamed/nested.go", Reads: 1, Size: int64(len(contents["old/nested.go"]))},
					"shared.go":         {Path: "shared.go", Reads: 2, Size: int64(len(contents["shared.go"]))},
				}
				if len(activity.Files) != len(wantFiles) || !reflect.DeepEqual(files, wantFiles) {
					t.Errorf("fallback lost verified paths/sizes or duplicated reads: got %+v, want %+v", activity.Files, wantFiles)
				}
				if activity.Tools["read"] != 6 || len(activity.Tools) != 1 || len(activity.Marks) != 6 {
					t.Errorf("fallback changed earlier command kinds or duplicated events: tools %v, marks %+v", activity.Tools, activity.Marks)
				}
				if len(activity.Marks) >= len(first.Marks) && !reflect.DeepEqual(activity.Marks[:len(first.Marks)], first.Marks) {
					t.Error("fallback changed the earlier attempt's sanitized marks")
				}
				for i, mark := range activity.Marks {
					wantAt := f.since.Add(time.Duration(2*i+1) * time.Second)
					if mark.Kind != "read" || !mark.At.Equal(wantAt) || mark.Until == nil || !mark.Until.Equal(wantAt.Add(time.Second)) {
						t.Errorf("fallback changed tool %d's kind or time: %+v", i, mark)
					}
				}
			}
			activity269Privacy(t, raw, f.workspace, f.work)
		})
	}
}

func TestFinishedActivityAcceptsAgentSizedEvents269(t *testing.T) {
	// One valid event exceeds the old snapshot's 32 MiB limit while staying
	// below the 64 MiB accepted by both actual agent stream readers.
	padding := strings.Repeat("x", 33<<20)
	for _, provider := range []string{"claude-code", "codex"} {
		for _, mode := range []string{"first-attempt", "fallback"} {
			t.Run(provider+"/"+mode, func(t *testing.T) {
				f := activity269New(t, provider)
				content := "PRIVATE269_LARGE_EVENT_FILE\n"
				for _, path := range []string{"seed.go", "before.go", "during.go", "after.go"} {
					activity269Write(t, filepath.Join(f.workspace, path), content)
				}
				out := activity269Open(t, filepath.Join(f.dir, "transcript.jsonl"))
				calls := 0
				var earlier []byte
				backend := dashboard.RecordActivity(activity269Backend{provider, func(_ context.Context, job synth.Job) (synth.Usage, error) {
					calls++
					f.begin(t, job)
					if mode == "fallback" && calls == 1 {
						f.call(t, job, "seed", "cat seed.go", 1)
						return synth.Usage{}, synth.ErrThinkingLoop
					}
					if mode == "fallback" {
						var err error
						earlier, err = os.ReadFile(filepath.Join(f.dir, "activity.json"))
						if err != nil {
							t.Fatalf("fallback has no earlier snapshot: %v", err)
						}
					}
					f.call(t, job, "before", "cat before.go", 3)
					at := func(second int) string {
						return f.since.Add(time.Duration(second) * time.Second).Format(time.RFC3339Nano)
					}
					var completed map[string]any
					if provider == "codex" {
						activity269Event(t, job.Transcript, map[string]any{"type": "item.started", "timestamp": at(5),
							"item": map[string]any{"id": "large", "type": "command_execution", "command": "cat during.go"}})
						completed = map[string]any{"type": "item.completed", "timestamp": at(6),
							"item": map[string]any{"id": "large", "type": "command_execution", "command": "cat during.go",
								"aggregated_output": "PRIVATE269_LARGE_OUTPUT", "status": "completed", "exit_code": 0}}
					} else {
						activity269Event(t, job.Transcript, map[string]any{"type": "assistant", "timestamp": at(5),
							"message": map[string]any{"id": "message-large", "content": []any{map[string]any{
								"type": "tool_use", "id": "large", "name": "Bash", "input": map[string]any{"command": "cat during.go"}}}}})
						completed = map[string]any{"type": "user", "timestamp": at(6),
							"message": map[string]any{"content": []any{map[string]any{
								"type": "tool_result", "tool_use_id": "large", "content": "PRIVATE269_LARGE_OUTPUT"}}}}
					}
					encoded, err := json.Marshal(completed)
					if err != nil {
						t.Fatal(err)
					}
					head, tail, found := strings.Cut(string(encoded), "PRIVATE269_LARGE_OUTPUT")
					if !found {
						t.Fatal("large event fixture has no output field")
					}
					if size := len(encoded) + len(padding) + 1; size <= 32<<20 || size >= 64<<20 {
						t.Fatalf("event size %d is outside the regression interval", size)
					}
					stream := io.MultiReader(strings.NewReader(head+"PRIVATE269_LARGE_OUTPUT"), strings.NewReader(padding), strings.NewReader(tail+"\n"))
					if provider == "codex" {
						_, err = synth.ReadCodexStream(stream, job.Transcript, 0, false)
					} else {
						_, err = synth.ReadStream(stream, job.Transcript)
					}
					if err != nil {
						t.Fatalf("agent reader rejected valid event: %v", err)
					}
					f.call(t, job, "after", "cat after.go", 7)
					return synth.Usage{}, nil
				}}, func(format string, args ...any) { t.Errorf("recording activity: "+format, args...) })
				_, fallback, err := synth.RunAgent(context.Background(), backend,
					synth.Job{Workspace: f.workspace, Transcript: out}, time.Minute, "xhigh")
				wantCalls := 1
				if mode == "fallback" {
					wantCalls = 2
				}
				if err != nil || calls != wantCalls || (fallback != nil) != (mode == "fallback") {
					t.Fatalf("large event changed backend behavior: calls %d, fallback %+v, error %v", calls, fallback, err)
				}
				if err := out.Close(); err != nil {
					t.Fatal(err)
				}
				snapshot, err := os.ReadFile(filepath.Join(f.dir, "activity.json"))
				if err != nil {
					t.Fatalf("valid large event prevented activity from being saved: %v", err)
				}
				if mode == "fallback" && bytes.Equal(earlier, snapshot) {
					t.Fatal("valid large event left the first attempt's snapshot stale")
				}
				activity269Privacy(t, string(snapshot), f.workspace, f.work)
				if err := os.RemoveAll(f.workspace); err != nil {
					t.Fatal(err)
				}
				f.finishStatus(t)
				var saved dashboard.Live
				if err := json.Unmarshal(snapshot, &saved); err != nil {
					t.Fatal(err)
				}
				card, raw := f.card(t, 7)
				later, _ := f.card(t, 23)
				if !reflect.DeepEqual(card, later) {
					t.Error("a later cold dashboard changed activity after a valid large event")
				}
				wantFiles := map[string]int{"before.go": 1, "during.go": 1, "after.go": 1}
				firstSecond := 3
				if mode == "fallback" {
					wantFiles["seed.go"] = 1
					firstSecond = 1
				}
				for _, activity := range []dashboard.Live{saved, card} {
					files := map[string]int{}
					for _, file := range activity.Files {
						files[file.Path] = file.Reads
						if file.Size != int64(len(content)) || file.Edits != 0 {
							t.Errorf("large event changed saved file counts/size: %+v", file)
						}
					}
					if len(activity.Files) != len(wantFiles) || !reflect.DeepEqual(files, wantFiles) || activity.Tools["read"] != len(wantFiles) || len(activity.Tools) != 1 || len(activity.Marks) != len(wantFiles) {
						t.Errorf("large event lost, skipped or duplicated activity before/after it: %+v", activity)
					}
					for i, mark := range activity.Marks {
						wantAt := f.since.Add(time.Duration(firstSecond+2*i) * time.Second)
						if mark.Kind != "read" || !mark.At.Equal(wantAt) || mark.Until == nil || !mark.Until.Equal(wantAt.Add(time.Second)) {
							t.Errorf("large event was skipped or changed tool %d's timing: %+v", i, mark)
						}
					}
				}
				activity269Privacy(t, raw, f.workspace, f.work)
			})
		}
	}
}
