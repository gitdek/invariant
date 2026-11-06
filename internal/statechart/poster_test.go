package statechart

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/tlc"
)

const posterView = `{"version":1,"state":[{"id":"ready","when":{"eq":[{"var":"n"},"0"]},"label":"Ready"},{"id":"one","when":{"eq":[{"var":"n"},"1"]},"label":"Working on the first item"},{"id":"rest","when":true,"label":"Done"}],"step":[{"id":"poll","when":{"eq":[{"action":true},"Poll"]},"label":"Check progress"},{"id":"all","when":true,"label":"Move on"}]}`

// posterChart is a checked chart with a forward chain, a cycle back to the
// start and a step that stays in place.
func posterChart(t *testing.T) *Result {
	t.Helper()
	model := "---- MODULE Poster665 ----\nEXTENDS Naturals\nCONSTANT Limit\nVARIABLE n\nvars == <<n>>\nInit == n = 0\nAdvance == /\\ n < 2 /\\ n' = n + 1\nReset == /\\ n = 2 /\\ n' = 0\nPoll == /\\ n = 1 /\\ UNCHANGED n\nNext == Advance \\/ Reset \\/ Poll\nSpec == Init /\\ [][Next]_vars\n====\n"
	pin, err := tla.PinHash(model, "Spec", project.Model)
	if err != nil {
		t.Fatal(err)
	}
	lock := project.Lock{Bounds: map[string]string{"Limit": "2"}, Statements: []project.Statement{{Name: "Spec", Kind: project.Spec, Says: "Advance, reset or poll.", SHA256: pin}}}
	g := tlc.GraphResult{Module: "Poster665", Config: tlc.Config{Specification: "Spec", Constants: lock.Bounds}, Tool: tlc.GraphTool{Image: "java@sha256:" + strings.Repeat("a", 64), JarSHA256: strings.Repeat("b", 64)},
		Run: tlc.Result{Outcome: tlc.Passed, Version: "TLC fixture", DistinctStates: 3, StatesGenerated: 5, Depth: 3}, StateCount: 3, StepCount: 4,
		Graph: tlc.Graph{States: []tlc.GraphState{{ID: "10", Label: "n = 0", Values: map[string]string{"n": "0"}}, {ID: "11", Label: "n = 1", Values: map[string]string{"n": "1"}}, {ID: "12", Label: "n = 2", Values: map[string]string{"n": "2"}}},
			Initial: []string{"10"},
			Steps:   []tlc.GraphStep{{From: "10", To: "11", Action: "Advance"}, {From: "11", To: "12", Action: "Advance"}, {From: "12", To: "10", Action: "Reset"}, {From: "11", To: "11", Action: "Poll"}}}}
	c, err := Compute(t.Context(), Input{Graph: g, Lock: lock, Invariants: map[string]map[string]bool{"10": {}, "11": {}, "12": {}}}, []byte(posterView))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// A poster draws every checked state and arrow exactly once, including
// steps that stay in a state, the same way every time, with no script or
// outside resource. The still poster has no motion.
func TestPosterDrawsTheWholeChart(t *testing.T) {
	c := posterChart(t)
	animated, err := c.Poster(PosterOptions{Title: "poster fixture", Animated: true})
	if err != nil {
		t.Fatal(err)
	}
	again, err := c.Poster(PosterOptions{Title: "poster fixture", Animated: true})
	if err != nil || again != animated {
		t.Fatalf("the same chart drew two different posters: %v", err)
	}
	for _, s := range c.States {
		if n := strings.Count(animated, fmt.Sprintf(`data-state-id="%s"`, svgEscape(s.ID))); n != 1 {
			t.Errorf("state %q drawn %d times", s.Label, n)
		}
	}
	for _, a := range c.Arrows {
		group := fmt.Sprintf(`data-from="%s" data-to="%s" data-label="%s"`, svgEscape(a.From), svgEscape(a.To), svgEscape(a.Label))
		if n := strings.Count(animated, group); n != 1 {
			t.Errorf("arrow %q drawn %d times", a.Label, n)
		}
	}
	if n := strings.Count(animated, "data-initial="); n != len(c.Initial) {
		t.Errorf("%d start markers for %d initial states", n, len(c.Initial))
	}
	for _, banned := range []string{"<script", "javascript:", "href=", "<foreignObject", "<image", "@import", "http://", "https://"} {
		if strings.Contains(strings.ToLower(strings.ReplaceAll(animated, `xmlns="http://www.w3.org/2000/svg"`, "")), strings.ToLower(banned)) {
			t.Errorf("poster contains %q", banned)
		}
	}
	for _, ref := range regexp.MustCompile(`url\(([^)]*)\)`).FindAllStringSubmatch(animated, -1) {
		if !strings.HasPrefix(ref[1], "#") {
			t.Errorf("poster refers outside itself: %s", ref[0])
		}
	}
	if !strings.Contains(animated, "<animateMotion") || !strings.Contains(animated, "replays one real behavior") {
		t.Error("the animated poster has no replay")
	}
	// Its static attributes are the finished chart, for viewers that don't
	// animate SVG; only the animations hide and reveal.
	if regexp.MustCompile(`<g data-state-id="[^"]*" opacity="0"`).MatchString(animated) || strings.Contains(animated, `stroke-dashoffset="1"`) {
		t.Error("the animated poster hides its chart until it animates")
	}
	still, err := c.Poster(PosterOptions{Title: "poster fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(still, "<animate") || strings.Contains(still, "replays") {
		t.Error("the still poster moves")
	}
	if !strings.Contains(still, "Poster fixture") || !strings.Contains(still, "Limit = 2") {
		t.Error("the poster lost its title or bounds")
	}
}

// The token replays a real behavior: each arrow it takes continues from a
// concrete state the previous one reached, starting at an initial state.
func TestPosterReplaysARealBehavior(t *testing.T) {
	c := posterChart(t)
	p := newPoster(c, PosterOptions{Animated: true})
	if len(p.tour) < 3 {
		t.Fatalf("the replay took %d steps of a chart with a cycle", len(p.tour))
	}
	reached := map[string]bool{}
	for _, s := range c.States {
		for _, m := range s.InitialMembers {
			reached[m] = true
		}
	}
	for i, a := range p.tour {
		next := map[string]bool{}
		for _, st := range a.Members {
			if reached[st.From] {
				next[st.To] = true
			}
		}
		if len(next) == 0 {
			t.Fatalf("step %d, %q, doesn't continue from any concrete state the replay reached", i, a.Label)
		}
		reached = next
	}
	states := map[string]bool{}
	for _, a := range p.tour {
		states[a.From], states[a.To] = true, true
	}
	if len(states) != len(c.States) {
		t.Errorf("the replay visits %d of %d states", len(states), len(c.States))
	}
}

// A poster refuses what the evidence image refuses, and an unprintable
// title.
func TestPosterRefusesWhatSVGRefuses(t *testing.T) {
	c := posterChart(t)
	if _, err := c.Poster(PosterOptions{Title: "bad \x01 title"}); err == nil {
		t.Error("an unprintable title was drawn")
	}
	c.Report.Counts["tlc_states"]++
	if _, err := c.SVG(); err == nil {
		t.Fatal("the fixture's damage doesn't break SVG")
	}
	if _, err := c.Poster(PosterOptions{Title: "fixture"}); err == nil {
		t.Error("a poster was drawn for a result SVG refuses")
	}
}
