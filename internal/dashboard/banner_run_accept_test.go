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

// The banner reads the same explicit run as the activity strip. Its checks
// must not come from a directory chosen by its name, modification time, or
// position among another watcher's or issue's directories.
var banner236Kinds = []struct {
	name, doing, prefix, file, kind string
}{
	{"modeled-build", "building", "build-", "gate-runs.jsonl", "gate"},
	{"plumbing-build", "building", "build-", "test-runs.jsonl", "test"},
	{"draft", "formalizing", "formalize-", "check-runs.jsonl", "check"},
	{"answer", "answering", "formalize-", "check-runs.jsonl", "check"},
}

// Write the future run field as JSON so these tests compile on the base,
// where ReadStatus ignores it and the observable banner behavior is wrong.
// Put the other issue first to require selection of the headline's step,
// rather than the first step that happens to name a run.
func banner236Status(t *testing.T, work, holder, doing string, since time.Time, run *string) {
	t.Helper()
	step := map[string]any{"issue": 236, "doing": doing, "since": since, "agent": "codex"}
	if run != nil {
		step["run"] = *run
	}
	esWrite(t, StatusPath(work, "owner/app"), map[string]any{
		"pid": os.Getpid(), "repo": "owner/app", "holder": holder,
		"started": since.Add(-time.Hour), "heartbeat": since, "every": 60,
		"issue": 236, "doing": doing, "since": since,
		"steps": []any{
			map[string]any{"issue": 237, "doing": doing, "since": since.Add(time.Hour), "run": "other-issue-run"},
			step,
		},
	})
}

func banner236Log(t *testing.T, work string, issue int, run, file string, marks []RunMark) {
	t.Helper()
	var lines []string
	for _, mark := range marks {
		b, err := json.Marshal(mark)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(b))
	}
	esStepDir(t, work, "owner/app", issue, run, nil, file, lines...)
}

// Wait only for the status/lease update to be reflected in the public
// response, never for the desired checks: the first fresh snapshot must be
// right, and polling until stale checks disappear would hide a disclosure.
func banner236Snapshot(t *testing.T, s *Server, since, after time.Time, holder string) Snapshot {
	t.Helper()
	var snap Snapshot
	esSoon(t, "the status update in the banner", func() (bool, string) {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/state.json", nil))
		if rec.Code != http.StatusOK {
			return false, fmt.Sprintf("HTTP %d", rec.Code)
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
			t.Fatal(err)
		}
		if !snap.GeneratedAt.After(after) || snap.Factory.Since == nil || !snap.Factory.Since.Equal(since) {
			return false, fmt.Sprintf("generated %s, factory %+v", snap.GeneratedAt, snap.Factory)
		}
		if len(snap.Repos) != 1 || snap.Repos[0].Lease == nil || snap.Repos[0].Lease.Holder != holder {
			return false, "selected watcher's lease is not visible yet"
		}
		return true, ""
	})
	return snap
}

func banner236Want(t *testing.T, snap Snapshot, kind string, want []RunMark) {
	t.Helper()
	for name, factory := range map[string]Watcher{"combined banner": snap.Factory, "repository banner": snap.Repos[0].Factory} {
		if factory.Issue != 236 {
			t.Errorf("%s shows issue %d; want 236", name, factory.Issue)
		}
		if len(want) == 0 {
			if len(factory.Runs) != 0 {
				t.Errorf("%s fell back to earlier checks: %+v", name, factory.Runs)
			}
			continue
		}
		if factory.RunsKind != kind || !reflect.DeepEqual(factory.Runs, want) {
			t.Errorf("%s has %s checks %+v; want only current run's %s checks %+v", name, factory.RunsKind, factory.Runs, kind, want)
		}
	}
}

func TestStatusBannerChecksFollowRetryRun(t *testing.T) {
	for _, c := range banner236Kinds {
		t.Run(c.name, func(t *testing.T) {
			work, other := t.TempDir(), t.TempDir()
			since := time.Date(2026, 10, 30, 1, 2, 3, 100000000, time.UTC)
			retry := since.Add(600 * time.Millisecond)
			previous, current := c.prefix+"20261030-010203-z-previous", c.prefix+"20261030-010203-a-retry"
			old := []RunMark{{Run: 1, Passed: false, Failed: []string{"previous-run"}, At: "old"}}
			want := []RunMark{{Run: 1, Passed: true, At: "retry-pass"}, {Run: 2, Passed: false, Failed: []string{"retry-only"}, At: "retry-fail"}}
			decoy := []RunMark{{Run: 99, Passed: false, Failed: []string{"wrong-watcher-or-issue"}}}
			banner236Log(t, work, 236, previous, c.file, old)
			banner236Status(t, work, "selected-watcher", c.doing, since, &previous)
			banner236Status(t, other, "other-watcher", c.doing, since, &current)
			banner236Log(t, other, 236, current, c.file, decoy)
			banner236Log(t, work, 237, current, c.file, decoy)
			banner236Log(t, work, 237, "other-issue-run", c.file, decoy)
			// Work points at the unselected watcher; the lease selects Works[1].
			s, fake := esServer(t, "owner/app", other, work)
			esLease(t, fake, "selected-watcher", strings.Repeat("b", 40))
			banner236Want(t, banner236Snapshot(t, s, since, time.Time{}, "selected-watcher"), c.kind, old)

			banner236Log(t, work, 236, current, c.file, want)
			// Make the predecessor newer on disk too. The run identity, not
			// name sorting or mtime, must select the retry's checks.
			banner236Log(t, work, 236, previous, c.file, append(old, RunMark{Run: 3, Passed: true, At: "late-previous"}))
			updated := time.Now()
			banner236Status(t, work, "selected-watcher", c.doing, retry, &current)
			banner236Want(t, banner236Snapshot(t, s, retry, updated, "selected-watcher"), c.kind, want)
		})
	}
}

func TestStatusBannerNeverFallsBackWhenRunChecksAreUnavailable(t *testing.T) {
	for _, c := range banner236Kinds {
		for _, absent := range []string{"run-omitted", "run-empty", "directory-missing", "checks-missing"} {
			t.Run(c.name+"/"+absent, func(t *testing.T) {
				work := t.TempDir()
				since := time.Date(2026, 10, 30, 1, 2, 3, 100000000, time.UTC)
				previous := c.prefix + "20261030-010203-z-previous"
				old := []RunMark{{Run: 1, Passed: true, At: "previous-only"}}
				banner236Log(t, work, 236, previous, c.file, old)
				banner236Status(t, work, "selected-watcher", c.doing, since, &previous)
				s, fake := esServer(t, "owner/app", work)
				esLease(t, fake, "selected-watcher", strings.Repeat("c", 40))
				banner236Want(t, banner236Snapshot(t, s, since, time.Time{}, "selected-watcher"), c.kind, old)

				var run *string
				if absent != "run-omitted" {
					name := ""
					if absent != "run-empty" {
						name = c.prefix + "20261030-010203-a-retry"
					}
					run = &name
					if absent == "checks-missing" {
						// The current transcript exists but has no checks log yet.
						dir := filepath.Join(work, "owner/app/issues/issue-236", name)
						if err := os.MkdirAll(dir, 0o755); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(dir, "transcript.jsonl"), []byte("{}\n"), 0o644); err != nil {
							t.Fatal(err)
						}
					}
				}
				retry := since.Add(600 * time.Millisecond)
				updated := time.Now()
				banner236Status(t, work, "selected-watcher", c.doing, retry, run)
				banner236Want(t, banner236Snapshot(t, s, retry, updated, "selected-watcher"), "", nil)
			})
		}
	}
}
