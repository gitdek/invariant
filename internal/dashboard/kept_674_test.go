package dashboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/github"
)

// keptGH is a gh that answers the dashboard's reads of o/r from a
// directory: issues.json, comments-<n>.json, pull-<n>.json and runs.json
// for any commit's check runs. Every other read is Not Found. It logs each
// path it's asked for to calls.
const keptGH = `#!/bin/sh
d='@DIR@'
for last; do :; done
echo "$last" >> "$d/calls"
case "$last" in
*/issues\?*) cat "$d/issues.json" ;;
*/issues/*/comments\?*)
	n=${last#*/issues/}
	n=${n%%/*}
	cat "$d/comments-$n.json" ;;
*/pulls/*)
	n=${last##*/pulls/}
	cat "$d/pull-$n.json" ;;
*/check-runs\?*) cat "$d/runs.json" ;;
*) echo "gh: Not Found (HTTP 404)" >&2; exit 1 ;;
esac
`

// A restart keeps each issue's comments and each merge's gate (#674): its
// first refresh reads again only an issue GitHub says has changed since, and
// no merge it already checked. A damaged file keeps nothing.
func TestARestartReadsOnlyWhatChanged(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, v any) {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(strings.ReplaceAll(keptGH, "@DIR@", dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	issue := func(updated string) []github.Issue {
		return []github.Issue{{Number: 5, Title: "Hold one lease", State: "closed", User: github.User{Login: "gitdek"}, CreatedAt: "2026-11-06T10:00:00Z", UpdatedAt: updated,
			Labels: []github.Label{{Name: factory.LabelTrigger}, {Name: factory.LabelMerged}}}}
	}
	write("issues.json", issue("2026-11-06T11:00:00Z"))
	write("comments-5.json", []github.Comment{{ID: 1, User: github.User{Login: "factory[bot]", Type: "Bot"}, CreatedAt: "2026-11-06T10:30:00Z", Body: marker(t, factory.Marker{Kind: factory.KindMerged, PR: 9})}})
	write("pull-9.json", github.PullRequest{Number: 9, Merged: true, Head: github.Ref{SHA: strings.Repeat("a", 40)}})
	write("runs.json", map[string][]github.CheckRun{"check_runs": {{Name: "invariant/gate", Status: "completed", Conclusion: "success"}}})
	cache := t.TempDir()
	run := func() []string {
		t.Helper()
		os.Remove(filepath.Join(dir, "calls"))
		s := &Server{Repos: []*Repo{{Name: "o/r", GitHub: github.Client{Repo: "o/r", GH: filepath.Join(dir, "gh")}, FactoryLogin: "factory[bot]"}}, Branch: "main", Cache: cache, Log: t.Logf}
		s.prepare()
		if err := s.refresh(t.Context()); err != nil {
			t.Fatal(err)
		}
		if m, ok := s.Repos[0].src.merges[9]; !ok || !m.green {
			t.Fatalf("merge #9's gate is %+v, %v; want it green", m, ok)
		}
		b, _ := os.ReadFile(filepath.Join(dir, "calls"))
		return strings.Fields(string(b))
	}
	read := func(calls []string, what string) bool {
		for _, c := range calls {
			if strings.Contains(c, what) {
				return true
			}
		}
		return false
	}

	first := run()
	for _, what := range []string{"issues/5/comments", "pulls/9", "check-runs"} {
		if !read(first, what) {
			t.Fatalf("the first run didn't read %s: %v", what, first)
		}
	}
	again := run()
	if !read(again, "issues?") {
		t.Fatalf("the restart didn't list the issues: %v", again)
	}
	for _, what := range []string{"issues/5/comments", "pulls/9", "check-runs"} {
		if read(again, what) {
			t.Errorf("the restart read %s again, though nothing changed: %v", what, again)
		}
	}

	// GitHub says the issue changed: its comments are read again, and the
	// merge's gate still isn't.
	write("issues.json", issue("2026-11-06T12:00:00Z"))
	changed := run()
	if !read(changed, "issues/5/comments") || read(changed, "pulls/9") {
		t.Errorf("after the issue changed, the restart read %v", changed)
	}

	// A damaged file keeps nothing, and the restart reads everything.
	if err := os.WriteFile(filepath.Join(cache, "sources.json.gz"), []byte("not gzip"), 0o644); err != nil {
		t.Fatal(err)
	}
	damaged := run()
	for _, what := range []string{"issues/5/comments", "pulls/9", "check-runs"} {
		if !read(damaged, what) {
			t.Errorf("with a damaged file, the restart didn't read %s: %v", what, damaged)
		}
	}
}
