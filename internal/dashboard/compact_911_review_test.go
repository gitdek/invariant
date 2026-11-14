package dashboard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/github"
)

// #911's acceptance test refreshes a server it built by hand and never
// prepared, where each test before it prepared its server first. Start
// always prepares, so only a test can ask for that order, and keeping an
// issue's comments used to panic in it. refresh makes the comment cache when
// it first keeps a read, as file, gateIn and graph make their caches: the
// server that wasn't prepared shows the cards a prepared one shows of the
// same repository, and both read an unchanged issue's comments once. GitHub
// answers through #674's gh.
func TestCompact911ReviewRefreshMakesItsCommentCache(t *testing.T) {
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
	const updated = "2026-11-12T12:00:00Z"
	bot := github.User{Login: "factory[bot]", Type: "Bot"}
	labels := []github.Label{{Name: factory.LabelTrigger}, {Name: factory.LabelHumanReview}}
	numbers := []int{911, 912}
	var issues []github.Issue
	for _, n := range numbers {
		issues = append(issues, github.Issue{Number: n, Title: "Stopped before a pull request", State: "open", User: github.User{Login: "owner"}, CreatedAt: updated, UpdatedAt: updated, Labels: labels})
		write(fmt.Sprintf("comments-%d.json", n), []github.Comment{{ID: int64(n), User: bot, CreatedAt: updated, Body: marker(t, factory.Marker{Kind: factory.KindFailed, Failure: factory.FailStopped})}})
	}
	write("issues.json", issues)

	// show refreshes a new server twice, and gives the cards it shows and how
	// often it read each issue's comments.
	show := func(prepare bool) (string, map[int]int) {
		t.Helper()
		os.Remove(filepath.Join(dir, "calls"))
		s := &Server{Repos: []*Repo{{Name: "o/r", GitHub: github.Client{Repo: "o/r", GH: filepath.Join(dir, "gh")}, FactoryLogin: bot.Login}}, Branch: "main", Cache: t.TempDir(), Log: t.Logf}
		if prepare {
			s.prepare()
		}
		defer func() {
			if p := recover(); p != nil {
				t.Fatalf("refresh, prepared %v, panicked: %v", prepare, p)
			}
		}()
		for range 2 {
			if err := s.refresh(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		for _, n := range numbers {
			if c := s.Repos[0].src.comments[n]; c.updated != updated || len(c.comments) != 1 {
				t.Fatalf("refresh, prepared %v, kept %+v of #%d's comments", prepare, c, n)
			}
		}
		cards, err := json.Marshal(s.assemble(time.Date(2026, 11, 12, 13, 0, 0, 0, time.UTC)).Issues)
		if err != nil {
			t.Fatal(err)
		}
		reads := map[int]int{}
		b, _ := os.ReadFile(filepath.Join(dir, "calls"))
		for _, c := range strings.Fields(string(b)) {
			for _, n := range numbers {
				if strings.Contains(c, fmt.Sprintf("/issues/%d/comments", n)) {
					reads[n]++
				}
			}
		}
		return string(cards), reads
	}

	bare, bareReads := show(false)
	prepared, preparedReads := show(true)
	if bare != prepared {
		t.Errorf("a server that wasn't prepared shows\n%s\nand a prepared one shows\n%s", bare, prepared)
	}
	for _, n := range numbers {
		if !strings.Contains(bare, fmt.Sprintf(`"number":%d,`, n)) {
			t.Errorf("the page has no card for #%d: %s", n, bare)
		}
		if bareReads[n] != 1 || preparedReads[n] != 1 {
			t.Errorf("two refreshes read #%d's unchanged comments %d times unprepared and %d prepared, want once each", n, bareReads[n], preparedReads[n])
		}
	}
}
