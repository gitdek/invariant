package dashboard

import (
	"html"
	"maps"
	"regexp"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/verify"
)

// An evidence page wears the dashboard's look, links back to its
// repository's view, and is served script-free with only the dashboard's
// fonts added to its policy. Its URL carries its own hash, so a redesign
// reaches readers while the evidence keeps its identity (#786, #723).
func TestAnEvidencePageWearsTheDashboardsLook786(t *testing.T) {
	e := receipt723Evidence(t, reader631FixtureFor(t, 2, "Ready"))
	page := makeReader(e)
	p := page.presentation
	if p.Status != "chart" || page.posters == nil {
		t.Fatalf("the fixture's page has status %q and posters %v; want a chart with posters", p.Status, page.posters)
	}
	h := html.UnescapeString(string(page.html))
	for _, want := range []string{
		`https://fonts.googleapis.com/css2?family=JetBrains+Mono`, "--accent:#38d9a9", `class="top"`,
		`href="/?repo=receipt723"`, "Back to receipt723 on the dashboard", `class="chip go"`,
		`href="https://github.com/owner/receipt723/tree/` + receipt723Commit + `/examples/reader"`,
		`href="https://github.com/owner/receipt723/commit/` + receipt723Commit + `"`,
		`href="https://github.com/owner/receipt723/blob/` + receipt723Commit + `/examples/reader/.invariant/specs/Dashboard631.tla"`,
		`href="https://github.com/owner/receipt723"`, "What must always hold", `<img class="poster" src="` + page.posters.Animated + `"`,
	} {
		if !strings.Contains(h, want) {
			t.Errorf("the evidence page doesn't have %q", want)
		}
	}
	if strings.Contains(strings.ToLower(h), "<script") {
		t.Error("the evidence page runs a script")
	}
	hash := strings.TrimPrefix(readerHash(page.html), "sha256:")[:16]
	if want := "/reader/" + strings.TrimPrefix(p.Fingerprint, "sha256:") + ".html?h=" + hash; p.Page != want || readerPath.FindStringSubmatch(p.Page) == nil {
		t.Fatalf("page URL %q; want %q, which readerPath reads", p.Page, want)
	}

	s := &Server{Repos: []*Repo{{Name: receipt723RepoName}}, Cache: t.TempDir()}
	s.keepReader(e)
	for _, u := range []string{p.Page, strings.TrimSuffix(p.Page, "?h="+hash)} {
		w := reader631GET(s, "GET", u, "")
		csp := w.Header().Get("Content-Security-Policy")
		if w.Code != 200 || w.Body.String() != string(page.html) {
			t.Fatalf("GET %s: %d", u, w.Code)
		}
		for _, want := range []string{"default-src 'none'", "style-src 'unsafe-inline' https://fonts.googleapis.com", "font-src https://fonts.gstatic.com", "sandbox"} {
			if !strings.Contains(csp, want) {
				t.Errorf("GET %s: the page's policy %q lacks %q", u, csp, want)
			}
		}
	}
	if csp := reader631GET(s, "GET", page.posters.Still, "").Header().Get("Content-Security-Policy"); strings.Contains(csp, "font") || !strings.Contains(csp, "sandbox") {
		t.Errorf("a poster's policy %q changed; only pages may use the fonts", csp)
	}
}

// A merged project's page links to what it came from on GitHub, at its
// receipt commit, and only where the place is certain (#786).
func TestAPagesLinksPointAtWhatWasChecked786(t *testing.T) {
	commit := strings.Repeat("a", 40)
	hrefs := func(e readerEvidence) map[string]string {
		out := map[string]string{}
		for _, l := range projectLinks(e) {
			out[l.Label] = l.Href
		}
		return out
	}
	factory := readerEvidence{Repo: "gitdek/ask3d", Commit: commit, Dir: "invariant/02-shared-library",
		Manifest: project.Manifest{Name: "shared library safety", Module: ".invariant/specs/SharedLibrary.tla", Language: "typescript", Existing: []string{"app/api/library/route.ts", "lib/missing.ts"}},
		Lock:     project.Lock{Ratified: &project.Ratification{By: "gitdek", At: "2026-11-09T18:25:25Z", Issue: 11, Comment: "https://github.com/gitdek/ask3d/issues/11#issuecomment-1"}},
		Code:     map[string]string{"app/api/library/route.ts": "app/api/library/route.ts"}}
	got := hrefs(factory)
	want := map[string]string{
		"Issue #11":           "https://github.com/gitdek/ask3d/issues/11",
		"Ratified by @gitdek": "https://github.com/gitdek/ask3d/issues/11#issuecomment-1",
		"The model":           "https://github.com/gitdek/ask3d/blob/" + commit + "/invariant/02-shared-library/.invariant/specs/SharedLibrary.tla",
		"The project":         "https://github.com/gitdek/ask3d/tree/" + commit + "/invariant/02-shared-library",
		"Receipt commit":      "https://github.com/gitdek/ask3d/commit/" + commit,
		"gitdek/ask3d":        "https://github.com/gitdek/ask3d",
	}
	for label, href := range want {
		if got[label] != href {
			t.Errorf("%s links %q; want %q", label, got[label], href)
		}
	}
	var code []string
	for _, l := range projectLinks(factory) {
		if l.Label == "Code checked" {
			code = append(code, l.Detail+" "+l.Href)
		}
	}
	if len(code) != 2 || code[0] != "app/api/library/route.ts https://github.com/gitdek/ask3d/blob/"+commit+"/app/api/library/route.ts" || code[1] != "lib/missing.ts " {
		t.Errorf("code checked: %q; want the placed file linked and the unplaced one named without a link", code)
	}

	hand := readerEvidence{Repo: "gitdek/ask3d", Commit: commit, Dir: "statue-service/verification/generation",
		Manifest: project.Manifest{Name: "Statue generation", Module: ".invariant/specs/StatueGeneration.tla", Language: "python"},
		Lock:     project.Lock{Decision: "D-0002"}, Report: &verify.Report{Journal: &verify.JournalRatification{Decision: "D-0002", By: "@gitdek", At: "2026-11-09T17:43:57Z"}}}
	for _, l := range projectLinks(hand) {
		if l.Label == "Decision D-0002" && (l.Href != "https://github.com/gitdek/ask3d/blob/"+commit+"/decisions/journal/D-0002.jsonl" || l.Detail != "ratified by @gitdek, 2026-11-09") {
			t.Errorf("the decision links %q, %q", l.Href, l.Detail)
		}
	}

	// Nothing outside the repository, or not quite a commit, becomes a link.
	stray := factory
	stray.Lock.Ratified = &project.Ratification{By: "gitdek", Issue: 11, Comment: "https://evil.example/gitdek/ask3d/issues/11"}
	stray.Code = map[string]string{"app/api/library/route.ts": "../escape.ts"}
	if got := hrefs(stray); got["Ratified by @gitdek"] != "" {
		t.Errorf("a comment outside the repository is linked: %q", got["Ratified by @gitdek"])
	}
	for _, l := range projectLinks(stray) {
		if l.Label == "Code checked" && l.Href != "" {
			t.Errorf("a path outside the repository is linked: %q", l.Href)
		}
	}
	short := factory
	short.Commit = "abc"
	for _, l := range projectLinks(short) {
		if l.Href != "" && l.Label != "Issue #11" && l.Label != "Ratified by @gitdek" && l.Label != "gitdek/ask3d" {
			t.Errorf("%s links %q without a full commit", l.Label, l.Href)
		}
	}
	odd := factory
	odd.Repo = `owner/<x> "y"`
	for _, l := range projectLinks(odd) {
		if l.Href != "" {
			t.Errorf("%s links %q for a malformed repository", l.Label, l.Href)
		}
	}
}

// The code a project checks is placed as the gate finds it: TypeScript's in
// the nearest folder above with a package-lock.json, and Python's in the one
// folder above that holds every named source (#786).
func TestExistingCodeIsPlacedAsTheGateFindsIt786(t *testing.T) {
	tree := func(paths ...string) []github.TreeEntry {
		var out []github.TreeEntry
		for _, p := range paths {
			out = append(out, github.TreeEntry{Path: p, Type: "blob"})
		}
		return out
	}
	ts := func(existing ...string) project.Manifest {
		return project.Manifest{Language: "typescript", Existing: existing}
	}
	py := func(existing ...string) project.Manifest {
		return project.Manifest{Language: "python", Existing: existing}
	}
	for _, c := range []struct {
		name string
		tree []github.TreeEntry
		dir  string
		m    project.Manifest
		want map[string]string
	}{
		{"package at the root", tree("package-lock.json", "app/api/library/route.ts"), "invariant/02-shared-library", ts("app/api/library/route.ts"), map[string]string{"app/api/library/route.ts": "app/api/library/route.ts"}},
		{"nested package", tree("package-lock.json", "web/package-lock.json", "web/src/x.ts"), "web/invariant/p", ts("src/x.ts"), map[string]string{"src/x.ts": "web/src/x.ts"}},
		{"missing from the commit", tree("package-lock.json"), "invariant/p", ts("src/x.ts"), nil},
		{"no package", tree("src/x.ts"), "invariant/p", ts("src/x.ts"), nil},
		{"python application", tree("statue-service/server.py", "statue-service/verification/generation/driver.py"), "statue-service/verification/generation", py("server.py"), map[string]string{"server.py": "statue-service/server.py"}},
		{"python ambiguous", tree("server.py", "statue-service/server.py"), "statue-service/verification/generation", py("server.py"), nil},
		{"project at the root", tree("package-lock.json", "x.ts"), ".", ts("x.ts"), nil},
		{"no existing code", tree("package-lock.json"), "invariant/p", project.Manifest{Language: "typescript"}, nil},
	} {
		if got := existingCode(c.tree, c.dir, c.m); !maps.Equal(got, c.want) {
			t.Errorf("%s: placed %v; want %v", c.name, got, c.want)
		}
	}
}

// Rules read as cards grouped by what they promise, with what the check
// found for each when a receipt says (#786).
func TestRulesSayWhatTheCheckFound786(t *testing.T) {
	statements := []project.Statement{
		{Name: "Spec", Kind: project.Spec, Says: "s"},
		{Name: "Safe", Kind: project.Invariant, Says: "safe"},
		{Name: "Ends", Kind: project.Property, Says: "ends"},
		{Name: "CanFinish", Kind: project.Witness, Says: "finish"},
		{Name: "Lost", Kind: project.Witness, Says: "lost"},
		{Name: "SkipCheck", Kind: project.Bug, Says: "skip"},
	}
	r := &verify.Report{Design: verify.Design{Passed: true, DistinctStates: 438},
		Properties: []verify.Property{{Name: "Ends", Holds: true}},
		Witnesses:  []verify.Witness{{Name: "CanFinish", Reached: true, Steps: 8}, {Name: "Lost"}},
		Bugs:       []verify.Bug{{Name: "SkipCheck", Caught: true, Violated: "Safe", Steps: 3}}}
	groups := rulesOf(statements, r)
	var titles []string
	found := map[string]string{}
	missed := map[string]bool{}
	for _, g := range groups {
		titles = append(titles, g.Title)
		for _, rule := range g.Rules {
			found[rule.Name], missed[rule.Name] = rule.Found, rule.Missed
		}
	}
	if strings.Join(titles, "|") != "What must always hold|What can happen|Bugs it must catch|How it runs" {
		t.Errorf("groups %q", titles)
	}
	for name, want := range map[string]string{
		"Safe": "Held in all 438 checked states", "Ends": "Holds under the model's fairness", "CanFinish": "Reached in 8 steps",
		"Lost": "Not reached", "SkipCheck": "Caught: breaks Safe in 3 steps; not replayed: not classified", "Spec": "",
	} {
		if found[name] != want {
			t.Errorf("%s found %q; want %q", name, found[name], want)
		}
	}
	if !missed["Lost"] || missed["CanFinish"] {
		t.Error("an unreached witness must read as missed, and only it")
	}
	if regexp.MustCompile(`Held in all|Reached|Caught`).MatchString(strings.Join(func() []string {
		var out []string
		for _, g := range rulesOf(statements, nil) {
			for _, rule := range g.Rules {
				out = append(out, rule.Found)
			}
		}
		return out
	}(), " ")) {
		t.Error("a proposal's rules claim findings without a receipt")
	}
}

// A proposal's page and an alternative's link their issue, and neither offers
// to ratify: the public page only reads (#786).
func TestProposalAndAlternativePagesLinkTheirIssue786(t *testing.T) {
	for _, links := range [][]evidenceLink{proposalView(comparisonEvidence{Repo: "gitdek/ask3d", Issue: 23}).Links, previewView(previewEvidence{Repo: "gitdek/ask3d", Issue: 23, Fork: "F1"}).Links} {
		if len(links) == 0 || links[0].Href != "https://github.com/gitdek/ask3d/issues/23" {
			t.Errorf("links %+v; want issue #23 first", links)
		}
	}
	for name, text := range map[string]string{"proposal": comparisonHTML, "alternative": previewHTML} {
		if strings.Contains(text, "/invariant ratify") || strings.Contains(strings.ToLower(text), "<script") {
			t.Errorf("the %s page offers a command or runs a script", name)
		}
	}
}
