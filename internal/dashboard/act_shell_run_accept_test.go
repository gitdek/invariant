package dashboard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// run is the watcher's explicit directory basename under repo/issues/issue-N.
// Using the status wire format keeps these tests executable against old
// watchers, which do not yet preserve this field, instead of failing to compile.
func act236WithRun(t *testing.T, step Step, run string) Step {
	t.Helper()
	b, err := json.Marshal(struct {
		Step
		Run string `json:"run,omitempty"`
	}{step, run})
	if err != nil {
		t.Fatal(err)
	}
	var out Step
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func act236Public(t *testing.T, s *Server, now time.Time) act236Envelope {
	t.Helper()
	b, err := s.LiveJSON(now)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"command"`) || strings.Contains(string(b), `"commands"`) {
		t.Fatalf("public live response exposes commands: %s", b)
	}
	var e act236Envelope
	if err := json.Unmarshal(b, &e); err != nil {
		t.Fatal(err)
	}
	return e
}

func act236SameStrip(t *testing.T, private, public act236Envelope) {
	t.Helper()
	if len(private.Steps) != len(public.Steps) {
		t.Fatalf("public and private step counts differ: private %+v, public %+v", private, public)
	}
	for i := range private.Steps {
		if !reflect.DeepEqual(private.Steps[i].Live, public.Steps[i].Live) {
			t.Fatalf("public and private strips select different activity: private %+v, public %+v", private, public)
		}
	}
}

// Both signs matter: an old directory in an eastward zone can look newer
// than the UTC start, and the correct westward directory can look older.
func TestActShellCommandsUseRunIdentityAcrossTimeZones(t *testing.T) {
	for _, offset := range []int{9, -5} {
		t.Run(fmt.Sprintf("watcher_UTC%+d", offset), func(t *testing.T) {
			t.Parallel()
			act236Retry(t, time.FixedZone("watcher", offset*3600), false)
		})
	}
}

func TestActShellCommandsUseRunIdentityForSameSecondRetries(t *testing.T) {
	act236Retry(t, time.UTC, true)
}

func TestActShellCommandsWaitForTheCurrentRunTranscript(t *testing.T) {
	act236Retry(t, time.UTC, false)
}

// Drive status, directory creation, run publication, and transcript creation
// separately, in the watcher's order. Repeat with warm and cold readers.
func act236Retry(t *testing.T, watcher *time.Location, sameSecond bool) {
	t.Helper()
	for _, provider := range []string{"claude-code", "codex"} {
		for _, doing := range []string{"building", "formalizing", "answering"} {
			t.Run(provider+"/"+doing, func(t *testing.T) {
				t.Parallel()
				f := newAct236Fixture(t, provider)
				// Remove the helper's initial directory so only these two runs exist.
				if err := os.RemoveAll(filepath.Dir(f.path)); err != nil {
					t.Fatal(err)
				}
				prefix := "build-"
				if doing != "building" {
					prefix = "formalize-"
				}
				f.since = f.since.Add(100 * time.Millisecond)
				oldSince := f.since
				retry := oldSince.Add(time.Minute + 650*time.Millisecond)
				if sameSecond {
					retry = oldSince.Add(650 * time.Millisecond)
				}
				created := retry
				if doing != "building" && !sameSecond {
					created = retry.Add(3 * time.Second) // workspace preparation can delay creation
				}
				oldRun := prefix + oldSince.In(watcher).Format("20060102-150405") + "-z-previous"
				newRun := prefix + created.In(watcher).Format("20060102-150405") + "-a-current"
				issueDir := filepath.Join(f.work, "owner/app/issues/issue-236")
				f.path = filepath.Join(issueDir, oldRun, "transcript.jsonl")
				const oldCommand = "go build ./... # PRIVATE_PREVIOUS_RUN_236"
				act236Write(t, f.path, f.init(t)+f.start(t, "same-id", oldCommand, 0))
				f.status(t, act236WithRun(t, Step{Issue: 236, Doing: doing, Since: oldSince, Agent: provider}, oldRun))
				first := f.read(t)
				if len(first.Steps) != 1 || len(first.Steps[0].Commands) != 1 {
					t.Fatalf("previous run must be visible before retry: %+v", first)
				}
				if first.Steps[0].Commands[0].Command != oldCommand {
					t.Fatalf("wrong previous run: %+v", first)
				}
				act236SameStrip(t, first, act236Public(t, f.s, time.Now()))

				// New status comes first, with no run. Even a known agent name
				// is not permission to guess a transcript from directory times.
				f.status(t, Step{Issue: 236, Doing: doing, Since: retry, Agent: provider})
				f.append(t, f.done(t, "same-id", oldCommand, 61)+
					f.start(t, "old-late", "printf 'PRIVATE_LATE_PREVIOUS_RUN_236'", 62))
				oldPath := f.path

				cold := func() *act236Fixture {
					fresh := *f
					fresh.s = &Server{Work: f.work, Repos: f.s.Repos, Access: f.s.Access}
					return &fresh
				}
				assertNoRun := func(stage string) {
					t.Helper()
					// Refresh once after the existing two-second cache expires.
					// Do not poll until a transient disclosure disappears.
					time.Sleep(2100 * time.Millisecond)
					for _, reader := range []*act236Fixture{f, cold()} {
						e := reader.read(t)
						for _, step := range e.Steps {
							if len(step.Commands) != 0 {
								t.Fatalf("%s: old commands exposed before current transcript exists: %+v", stage, e)
							}
							// Ended public cards can retain their historical strip;
							// a current card may never inherit that activity.
							if step.Ended == nil && (len(step.Marks) != 0 || len(step.Tools) != 0) {
								t.Fatalf("%s: previous activity attributed to a current step: %+v", stage, step)
							}
						}
						act236SameStrip(t, e, act236Public(t, reader.s, time.Now()))
					}
				}
				assertNoRun("status before directory")

				f.path = filepath.Join(issueDir, newRun, "transcript.jsonl")
				if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
					t.Fatal(err)
				}
				f.status(t, act236WithRun(t, Step{Issue: 236, Doing: doing, Since: retry, Agent: provider}, newRun))
				assertNoRun("published run before transcript")

				f.since = created
				const newCommand = "git status --short"
				act236Write(t, f.path, f.init(t)+f.start(t, "same-id", newCommand, 1)+f.done(t, "same-id", newCommand, 2))
				// The previous transcript keeps growing and has a newer mtime;
				// neither that nor lexical directory order can select it.
				old := *f
				old.path = oldPath
				old.append(t, old.start(t, "late-again", "printf 'PRIVATE_OLD_AFTER_CURRENT_236'", 3))
				got := f.eventually(t, "current named transcript appears", func(e act236Envelope) bool {
					for _, step := range e.Steps {
						for _, command := range step.Commands {
							if command.Command != newCommand {
								t.Fatalf("retry restored previous commands: %+v", e)
							}
						}
					}
					return len(e.Steps) == 1 && len(e.Steps[0].Commands) == 1
				})
				step := got.Steps[0]
				if step.Since == nil || !step.Since.Equal(retry) || step.Ended != nil || step.Doing != doing {
					t.Fatalf("commands assigned to the wrong step: %+v", step)
				}
				until := f.at(2)
				act236Want(t, step.Commands[0], newCommand, "git", f.at(1), &until)
				if len(step.Marks) != 1 || step.Marks[0].Kind != "git" || step.Tools["git"] != 1 || len(step.Tools) != 1 {
					t.Fatalf("current strip includes another run's activity: %+v", step.Live)
				}
				act236SameStrip(t, got, act236Public(t, f.s, time.Now()))
				fresh := cold()
				freshView := fresh.read(t)
				if len(freshView.Steps) != 1 || !reflect.DeepEqual(freshView.Steps[0].Commands, step.Commands) {
					t.Fatalf("fresh and warmed readers disagree: warm %+v, fresh %+v", got, freshView)
				}
				act236SameStrip(t, freshView, act236Public(t, fresh.s, time.Now()))
			})
		}
	}
}

// This public-only regression fails independently of the new private route.
// A run name has no temporal meaning; changing it changes the source even
// when Issue, Doing, Since and Agent all stay identical.
func TestLiveStripUsesOnlyTheStatusRun(t *testing.T) {
	for _, provider := range []string{"claude-code", "codex"} {
		t.Run(provider, func(t *testing.T) {
			f := newAct236Fixture(t, provider)
			if err := os.RemoveAll(filepath.Dir(f.path)); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(f.work, "owner/app/issues/issue-236")
			oldRun, currentRun := "build-z-previous", "build-a-current"
			act236Write(t, filepath.Join(dir, oldRun, "transcript.jsonl"), f.init(t)+f.start(t, "same-id", "go build ./...", 1))
			act236Write(t, filepath.Join(dir, currentRun, "transcript.jsonl"), f.init(t)+f.start(t, "same-id", "git status --short", 2))
			step := Step{Issue: 236, Doing: "building", Since: f.since, Agent: provider}
			now := time.Now()
			read := func() act236Envelope {
				now = now.Add(3 * time.Second)
				return act236Public(t, f.s, now)
			}
			wantKind := func(e act236Envelope, kind string) {
				t.Helper()
				if len(e.Steps) != 1 || e.Steps[0].Ended != nil || len(e.Steps[0].Marks) != 1 ||
					e.Steps[0].Marks[0].Kind != kind || e.Steps[0].Tools[kind] != 1 || len(e.Steps[0].Tools) != 1 {
					t.Fatalf("named run should contain only %s, got %+v", kind, e)
				}
			}
			f.status(t, act236WithRun(t, step, oldRun))
			wantKind(read(), "build")
			f.status(t, act236WithRun(t, step, currentRun))
			wantKind(read(), "git")
			for _, run := range []string{"", "build-uncreated", "build-no-transcript"} {
				if run == "build-no-transcript" {
					if err := os.Mkdir(filepath.Join(dir, run), 0o755); err != nil {
						t.Fatal(err)
					}
				}
				f.status(t, act236WithRun(t, step, run))
				warm := read()
				cold := act236Public(t, &Server{Work: f.work, Repos: f.s.Repos}, now)
				for _, e := range []act236Envelope{warm, cold} {
					for _, live := range e.Steps {
						if live.Ended == nil && (len(live.Marks) != 0 || len(live.Tools) != 0) {
							t.Fatalf("run %q fell back to another transcript: %+v", run, live)
						}
					}
				}
			}
		})
	}
}
