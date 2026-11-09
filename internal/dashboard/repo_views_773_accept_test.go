package dashboard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/github"
)

// Another repository's view shows its own decisions, read from its own
// journal at its branch's head, and its decisions tile counts its own calls,
// while the snapshot's decisions stay Invariant's (#773). Another
// repository's commits still aren't read, and one without a journal has no
// decisions.
func TestAnotherRepositoryShowsItsOwnDecisions773(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake gh is a shell script")
	}
	ours, theirs, quiet := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	mine, _ := queryJournal(t, 3)
	other, _ := queryJournal(t, 2)
	fake := queryFake(t)
	// The fake answers each other repository's branch from a file of its own.
	refs := "*/o/other/git/ref/heads/main) printf '{\"object\":{\"sha\":\"%s\"}}\\n' \"$(cat \"$d/other\")\" ;;\n" +
		"*/o/quiet/git/ref/heads/main) printf '{\"object\":{\"sha\":\"%s\"}}\\n' \"$(cat \"$d/quiet\")\" ;;\n*) missing ;;"
	gh := strings.Replace(strings.ReplaceAll(queryGH, "@DIR@", fake), "*) missing ;;", refs, 1)
	for name, text := range map[string]string{"gh": gh, "other": theirs, "quiet": quiet} {
		if err := os.WriteFile(filepath.Join(fake, name), []byte(text), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	queryCommit(t, fake, ours, "# r\n", queryFiles(t, mine), nil)
	queryCommit(t, fake, theirs, "# other\n", queryFiles(t, other), nil)
	queryHead(t, fake, ours)

	var mu sync.Mutex
	var logged []string
	logf := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		logged = append(logged, fmt.Sprintf(format, args...))
	}
	repo := func(name string) *Repo {
		return &Repo{Name: name, GitHub: github.Client{Repo: name, GH: filepath.Join(fake, "gh")}}
	}
	s := &Server{Repos: []*Repo{repo("o/r"), repo("o/other"), repo("o/quiet")}, Branch: "main", Every: 50 * time.Millisecond, Log: logf}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		time.Sleep(300 * time.Millisecond)
	})
	s.Start(ctx)

	var snap Snapshot
	for deadline := time.Now().Add(time.Minute); ; {
		snap = querySnapshot(t, s)
		if len(snap.Repos) == 3 && snap.Repos[1].Decisions != nil && snap.Repos[1].Decisions.Total == 2 && snap.Decisions.Graph != nil {
			break
		}
		if time.Now().After(deadline) {
			mu.Lock()
			last := strings.Join(logged[max(len(logged)-4, 0):], "\n")
			mu.Unlock()
			t.Fatalf("the page never showed o/other's own decisions; the server last logged:\n%s", last)
		}
		time.Sleep(20 * time.Millisecond)
	}

	theirsShown := snap.Repos[1]
	d := theirsShown.Decisions
	var ids []string
	for _, x := range d.Latest {
		ids = append(ids, x.ID)
	}
	if strings.Join(ids, " ") != "D-0002 D-0001" || d.Status["decided"] != 1 || d.Status["proposed"] != 1 || d.Graph == nil || len(d.Graph.Nodes) != 2 {
		t.Errorf("o/other's decisions are %v, with statuses %v and graph %v; want its own two, newest first, from its journal", ids, d.Status, d.Graph)
	}
	if theirsShown.Totals.Decisions != 2 {
		t.Errorf("o/other's totals count %d decisions, want its own 2", theirsShown.Totals.Decisions)
	}
	if got, want := decisionsTile(t, theirsShown.Tiles), "0 ratified by @gitdek, 1 decided by agents as they built"; got != want {
		t.Errorf("o/other's decisions tile says %q, want its own calls: %q", got, want)
	}
	quietShown := snap.Repos[2]
	if quietShown.Decisions == nil || quietShown.Decisions.Total != 0 || len(quietShown.Decisions.Latest) != 0 || quietShown.Decisions.Graph != nil {
		t.Errorf("o/quiet, which has no journal, shows decisions %+v; want none", quietShown.Decisions)
	}
	if got, want := decisionsTile(t, quietShown.Tiles), "none logged in this repository yet"; got != want {
		t.Errorf("o/quiet's decisions tile says %q, want %q", got, want)
	}
	if snap.Repos[0].Decisions != nil || len(snap.Decisions.Graph.Nodes) != 3 {
		t.Errorf("Invariant's own decisions moved: its repository line has %+v, and the snapshot's graph %d nodes, want 3", snap.Repos[0].Decisions, len(snap.Decisions.Graph.Nodes))
	}
	for _, call := range queryCalls(t, fake) {
		if strings.Contains(call, "o/other/commits") || strings.Contains(call, "o/quiet/commits") {
			t.Errorf("the server read another repository's commits: %s", call)
		}
	}
}

// decisionsTile is the small text of the decisions tile in tiles.
func decisionsTile(t *testing.T, tiles []Tile) string {
	t.Helper()
	for _, x := range tiles {
		if x.Key == "dec" {
			return x.Small
		}
	}
	t.Fatalf("no decisions tile in %+v", tiles)
	return ""
}
