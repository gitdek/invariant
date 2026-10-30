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
