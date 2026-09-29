package dashboard

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// The watcher's status file lists every step it's running, each with its
// issue, what it's doing and since when (D-0113), and reads back the same.
func TestTheStatusListsEveryStep(t *testing.T) {
	path := StatusPath(t.TempDir(), "o/r")
	since := time.Date(2026, 9, 28, 22, 39, 0, 0, time.UTC)
	steps := []Step{{Issue: 91, Doing: "building", Since: since}, {Issue: 97, Doing: "formalizing", Since: since.Add(time.Minute)}}
	if err := WriteStatus(path, Status{PID: 1, Repo: "o/r", Started: since, Heartbeat: since, Steps: steps}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Steps []struct {
			Issue int    `json:"issue"`
			Doing string `json:"doing"`
			Since string `json:"since"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(b, &file); err != nil {
		t.Fatal(err)
	}
	var have []string
	for _, s := range file.Steps {
		have = append(have, fmt.Sprintf("%d %s %s", s.Issue, s.Doing, s.Since))
	}
	want := []string{"91 building 2026-09-28T22:39:00Z", "97 formalizing 2026-09-28T22:40:00Z"}
	if strings.Join(have, ", ") != strings.Join(want, ", ") {
		t.Fatalf("the status file lists %v; want %v:\n%s", have, want, b)
	}
	got, err := ReadStatus(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Steps) != 2 {
		t.Fatalf("read back %d steps; want 2", len(got.Steps))
	}
	for i, s := range got.Steps {
		if s.Issue != steps[i].Issue || s.Doing != steps[i].Doing || !s.Since.Equal(steps[i].Since) {
			t.Errorf("step %d read back as %+v; want %+v", i, s, steps[i])
		}
	}
}
