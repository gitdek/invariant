package dashboard

import (
	"os"
	"strings"
	"testing"
	"time"
)

// The first tile counts merges without a green gate. At zero its sentence
// says every gate passed. At any other count it says how many did and names
// the pull requests that didn't, and the page draws the tile and the hero's
// chip as a failure: the sentence used to say all of them passed under a
// count of three.
func TestTheMergesTileNamesWhatMergedWithoutAGate(t *testing.T) {
	clean := Tiles(Totals{Merged: 163, MergesChecked: 152}, Who{})[0]
	if clean.Key != "bad" || clean.Shown != "0" || !strings.HasPrefix(clean.Small, "CI's gate passed on the exact head of all 152 pull requests the factory merged. ") {
		t.Errorf("a clean record's tile: %+v", clean)
	}
	bad := Tiles(Totals{Merged: 163, MergesChecked: 152, BadMerges: 3, BadMergeRefs: []string{"#703", "#690", "ask3d#12"}}, Who{})[0]
	if bad.Shown != "3" || strings.Contains(bad.Small, " all ") ||
		!strings.HasPrefix(bad.Small, "CI's gate passed on the exact head of 149 of the 152 pull requests merged. #703, #690 and ask3d#12 merged without it. ") {
		t.Errorf("the tile over three merges without a gate: %+v", bad)
	}
	for refs, want := range map[string]string{
		"#1":                         "#1 merged without it.",
		"#1 #2":                      "#1 and #2 merged without it.",
		"#1 #2 #3 #4 #5 #6":          "#1, #2, #3, #4, #5 and #6 merged without it.",
		"#1 #2 #3 #4 #5 #6 #7 #8 #9": "#1, #2, #3, #4, #5, #6 and 3 more merged without it.",
	} {
		names := strings.Fields(refs)
		if got := Tiles(Totals{MergesChecked: 20, BadMerges: len(names), BadMergeRefs: names}, Who{})[0].Small; !strings.Contains(got, want) {
			t.Errorf("%d merges without a gate read %q; want %q in it", len(names), got, want)
		}
	}
	for file, wants := range map[string][]string{
		"web/app.js":    {`el.classList.toggle("breaks", x.shown !== "0")`, `class="chip breaks"`, `merges had no green gate`},
		"web/style.css": {".chip.breaks", ".num.breaks .v"},
	} {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range wants {
			if !strings.Contains(string(b), want) {
				t.Errorf("%s doesn't draw a count above zero as a failure: no %q", file, want)
			}
		}
	}
}

// With no step running, an open issue is between steps, and takes the
// headline as work in progress only while that's believable: the factory
// picks an issue up within a few polls, and CI's gate finishes within the
// hour. The page's biggest words once read "Picking up #571 · The factory is
// working · for 5d 7h" while nothing ran, because #571 waited on a step of
// its plan.
func TestAStalledIssueDoesNotTakeTheHeadline(t *testing.T) {
	now := time.Date(2026, 11, 11, 22, 0, 0, 0, time.UTC)
	issue := func(number int, stage string, age time.Duration, who string) Issue {
		return Issue{Number: number, PR: number + 1000, Title: "An issue", Open: true, Stage: stage,
			Spans: []Span{{From: now.Add(-age), To: now, Who: who, Open: true}}}
	}
	for _, c := range []struct {
		name     string
		issues   []Issue
		headline string
		waiting  string
	}{
		{"just queued", []Issue{issue(571, StageQueued, time.Minute, WhoFactory)}, "Picking up #571", WhoFactory},
		{"queued for five days", []Issue{issue(571, StageQueued, 5*24*time.Hour, WhoFactory)}, "Idle, and watching for issues", ""},
		{"CI's gate running", []Issue{issue(9, StageGate, 20*time.Minute, WhoCI)}, "Waiting for CI's gate on #1009", WhoCI},
		{"CI's gate four hours on", []Issue{issue(9, StageGate, 4*time.Hour, WhoCI)}, "Idle, and watching for issues", ""},
		{"a build between steps", []Issue{issue(7, StageBuilding, 10*time.Minute, WhoFactory)}, "Writing the code for #7", WhoFactory},
		{"a build stopped hours ago", []Issue{issue(7, StageBuilding, 2*time.Hour, WhoFactory)}, "Idle, and watching for issues", ""},
		{"a stalled issue ahead of a fresh one", []Issue{issue(571, StageQueued, 5*24*time.Hour, WhoFactory), issue(802, StageQueued, 2*time.Minute, WhoFactory)}, "Picking up #802", WhoFactory},
	} {
		n := nowLine([]namedWatcher{{repo: "gitdek/invariant", w: Watcher{Running: true}}}, c.issues, "gitdek/invariant")
		if n.Headline != c.headline || n.WaitingOn != c.waiting || (c.waiting == "") != (n.Stage == "idle") || (c.waiting == "") != (n.Since == nil) {
			t.Errorf("%s: the headline is %+v; want %q waiting on %q", c.name, n, c.headline, c.waiting)
		}
	}
}
