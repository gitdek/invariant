package dashboard

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/project"
)

// The page claims only what was earned, and names what it's talking about
// (#848): the models section says what's checked, the page's description
// says checks, a plan in the activity list is a plan, and a touch screen
// gets an instruction of its own.
func TestThePageSaysWhatWasEarned848(t *testing.T) {
	page, err := web.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)
	for _, want := range []string{"<h2>What's checked</h2>", "the code factory that checks every pull request against statements people ratified",
		`<span class="with-pointer">Rest on a chart`, `<span class="with-touch">Tap a chart for its evidence page.</span>`} {
		if !strings.Contains(html, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	for _, not := range []string{"proves every pull request", "<h2>What's proved</h2>"} {
		if strings.Contains(html, not) {
			t.Errorf("the page still says %q", not)
		}
	}
	plan := factory.Marker{Kind: factory.KindProposal, Proposal: &formalize.Proposal{Plan: &plumbing.Plan{
		Files: []string{"a.go", "b.go", "c.go"}, Tests: []plumbing.Test{{Name: "TestA"}, {Name: "TestB"}}}}}
	issues := factory.Marker{Kind: factory.KindProposal, Proposal: &formalize.Proposal{IssuePlan: &formalize.IssuePlan{Issues: make([]formalize.PlannedIssue, 4)}}}
	statements := factory.Marker{Kind: factory.KindProposal, Proposal: &formalize.Proposal{Statements: make([]project.Statement, 3)}}
	for want, m := range map[string]factory.Marker{
		"proposed a plan: 3 files to write, 2 acceptance tests": plan,
		"proposed a plan of 4 issues":                           issues,
		"proposed 3 statements, checked by TLC":                 statements,
	} {
		if got := postText(m, factory.Marker{}); got != want {
			t.Errorf("the activity list says %q, want %q", got, want)
		}
	}
	// A plan is ratified as a plan on /act.
	script, err := web.ReadFile("web/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"Ratify this plan"`, "this plan and its acceptance tests, and the factory starts building", "this plan, and the factory opens its issues one at a time"} {
		if !strings.Contains(string(script), want) {
			t.Errorf("the page's script lacks %q", want)
		}
	}
}

// Small text in the light theme reads at 4.5 to 1 or better against the
// page and against a card, as WCAG computes it (#848): the quiet ink, and
// the accent and the amber where they're text. The brand's own accent and
// amber stay for the graphics.
func TestTheLightThemesSmallTextReads848(t *testing.T) {
	sheet, err := web.ReadFile("web/style.css")
	if err != nil {
		t.Fatal(err)
	}
	block := regexp.MustCompile(`(?s):root\[data-theme="light"\] \{(.*?)\n\}`).FindSubmatch(sheet)
	if block == nil {
		t.Fatal("the stylesheet has no light theme")
	}
	colors := map[string]string{}
	for _, m := range regexp.MustCompile(`--([a-z0-9-]+): (#[0-9a-fA-F]{6});`).FindAllSubmatch(block[1], -1) {
		colors[string(m[1])] = string(m[2])
	}
	luminance := func(hex string) float64 {
		var total float64
		for i, weight := range []float64{0.2126, 0.7152, 0.0722} {
			n, err := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
			if err != nil {
				t.Fatalf("%s: %v", hex, err)
			}
			c := float64(n) / 255
			if c <= 0.03928 {
				c /= 12.92
			} else {
				c = math.Pow((c+0.055)/1.055, 2.4)
			}
			total += weight * c
		}
		return total
	}
	ratio := func(a, b string) float64 {
		la, lb := luminance(a), luminance(b)
		return (math.Max(la, lb) + 0.05) / (math.Min(la, lb) + 0.05)
	}
	for _, name := range []string{"ink3", "accent-ink", "people-ink"} {
		for _, ground := range []string{"bg", "card-solid"} {
			if colors[name] == "" || colors[ground] == "" {
				t.Fatalf("the light theme doesn't set --%s or --%s", name, ground)
			}
			if got := ratio(colors[name], colors[ground]); got < 4.5 {
				t.Errorf("--%s on --%s is %.2f to 1 in the light theme, under 4.5", name, ground, got)
			}
		}
	}
	// Text takes the darker colors, and the brand's stay.
	if colors["accent"] != "#0ca678" || colors["people"] != "#b7791f" {
		t.Errorf("the light theme's brand colors moved: accent %s, amber %s", colors["accent"], colors["people"])
	}
	for _, name := range []string{"accent", "people"} {
		if regexp.MustCompile(`[^-\w]color: var\(--` + name + `\)`).Match(sheet) {
			t.Errorf("some text is still colored with --%s, which is too pale on light", name)
		}
	}
	// Another repository's items are quieter, and still read.
	aside := regexp.MustCompile(`(?m)^\.aside \{ opacity: ([0-9.]+); \}`).FindSubmatch(sheet)
	if aside == nil {
		t.Fatal("the stylesheet doesn't quiet another repository's items")
	}
	if opacity, _ := strconv.ParseFloat(string(aside[1]), 64); opacity < 0.7 || opacity >= 1 {
		t.Errorf("another repository's items are drawn at %s", fmt.Sprint(opacity))
	}
}
