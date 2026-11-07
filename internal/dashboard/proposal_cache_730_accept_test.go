package dashboard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/statechart"
)

// The shared cache exposes this package-private observation seam on *Server:
// proposalCacheWork() (comparisonBuilds, previewBuilds, writeAttempts uint64).
// Counts are per server, cumulative and safe to read concurrently. A build is
// an actual invocation of the typed validation-and-render operation, including
// diagnostic builds and restores. A write attempt starts before mkdir/write/
// rename, including failures; it excludes snapshot, receipt and source writes.
// Observe real work at those boundaries, not inferred hits or served requests.
// This is internal test observability, not another dashboard API or metric UI.
func cache730Work(t *testing.T, s *Server) [3]uint64 {
	t.Helper()
	v, ok := any(s).(interface {
		proposalCacheWork() (uint64, uint64, uint64)
	})
	if !ok {
		t.Fatal("shared proposal cache must expose actual build and evidence-write counts through proposalCacheWork")
	}
	c, p, w := v.proposalCacheWork()
	return [3]uint64{c, p, w}
}

func cache730WantWork(t *testing.T, s *Server, want [3]uint64) {
	t.Helper()
	if got := cache730Work(t, s); got != want {
		t.Fatalf("comparison builds, preview builds, persistence attempts = %v, want %v", got, want)
	}
}

func cache730Clone[T any](t *testing.T, in T) T {
	t.Helper()
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out T
	if err := decodeReaderJSON(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// Reuse the existing checked chart fixtures and Docker double, including its
// checks of network isolation, TLC config, complete graph and rule replay.
// The server still receives its proposals through authenticated factory markers.
func cache730Fixture(t *testing.T) (*Server, comparisonEvidence, []previewEvidence) {
	t.Helper()
	snapshot := func(n int, label string) statechart.ComparisonSnapshot {
		f := reader631FixtureFor(t, n, label)
		return statechart.ComparisonSnapshot{Kind: "proposal", Name: label, Module: "Dashboard631",
			Model: string(f.Files[f.Manifest.Module]), View: string(f.Files[".invariant/view.json"]),
			Includes: map[string]string{"Helper631.tla": string(f.Files[".invariant/specs/Helper631.tla"])},
			Lock:     f.Lock, Chart: f.Report.Reader.Chart}
	}
	c, err := statechart.Compare(snapshot(2, "Original <words> & bounds"), snapshot(3, "Candidate <script>literal</script>"))
	if err != nil {
		t.Fatal(err)
	}
	p := comparison717Proposal(t, c)
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	preview639Write(t, ws, preview639Draft(t, 1, nil))
	forks := preview639Checked(t, ws, tc)
	s := preview639Server(t, forks, "owner/cache", 731)
	r := s.Repos[0]
	r.src.issues = append(r.src.issues, github.Issue{Number: 730, Title: "Compare revisions", State: "open",
		CreatedAt: "2026-11-05T12:00:00Z", Labels: []github.Label{{Name: factory.LabelProposal}}})
	cache730Marker(t, r, 730, factory.Marker{Kind: factory.KindProposal, Proposal: p})
	e := comparisonEvidence{Format: comparisonFormat, Repo: r.Name, Issue: 730, Proposal: p}
	f := forks.Forks[0]
	var options []previewEvidence
	for _, o := range f.Options {
		options = append(options, previewEvidence{Format: previewFormat, Repo: r.Name, Issue: 731,
			Fork: f.ID, Kind: f.Kind, Question: f.Question, Option: o})
	}
	return s, e, options
}

func cache730Marker(t *testing.T, r *Repo, issue int, m factory.Marker) {
	t.Helper()
	r.src.comments[issue] = cachedComments{comments: []github.Comment{{ID: int64(issue),
		CreatedAt: "2026-11-05T12:00:00Z", User: github.User{Login: "factory[bot]"}, Body: marker(t, m)}}}
}

func cache730Publish(t *testing.T, s *Server, snap Snapshot) {
	t.Helper()
	gz, err := s.publish(snap)
	if err != nil {
		t.Fatal(err)
	}
	s.saveSnapshot(gz)
}

func cache730Snapshot(t *testing.T, s *Server) Snapshot {
	t.Helper()
	snap := s.assemble(time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC))
	cache730Publish(t, s, snap)
	return snap
}

func cache730State(t *testing.T, s *Server) Snapshot {
	t.Helper()
	r := reader631GET(s, "GET", "/api/state.json", "")
	if r.Code != 200 {
		t.Fatalf("snapshot status %d: %s", r.Code, r.Body.String())
	}
	var snap Snapshot
	if err := json.Unmarshal(r.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	return snap
}

func cache730Refs(snap Snapshot) []ReaderPresentation {
	var out []ReaderPresentation
	for _, is := range snap.Issues {
		if w := is.Waiting; w != nil {
			if w.Reader != nil {
				out = append(out, *w.Reader)
			}
			for _, q := range w.Forks {
				for _, o := range q.Options {
					if o.Reader != nil {
						out = append(out, *o.Reader)
					}
				}
			}
		}
	}
	for _, p := range snap.Projects {
		if p.Reader != nil {
			out = append(out, *p.Reader)
		}
	}
	return out
}

type cache730Body struct{ body, etag string }

func cache730Bodies(t *testing.T, s *Server, refs []ReaderPresentation) map[string]cache730Body {
	t.Helper()
	out := map[string]cache730Body{}
	for _, p := range refs {
		if p.Page == "" || p.Fingerprint == "" {
			t.Fatal("missing immutable presentation")
		}
		for _, url := range []string{p.Page, p.SVG} {
			if url == "" {
				continue
			}
			r := reader631GET(s, "GET", url, "")
			if r.Code != 200 || r.Header().Get("ETag") == "" || !strings.Contains(r.Header().Get("Cache-Control"), "immutable") {
				t.Fatalf("missing immutable evidence %s: %d", url, r.Code)
			}
			if strings.Contains(strings.ToLower(r.Body.String()), "<script") || !strings.Contains(r.Header().Get("Content-Security-Policy"), "default-src 'none'") {
				t.Fatalf("active content in %s", url)
			}
			out[url] = cache730Body{r.Body.String(), r.Header().Get("ETag")}
		}
	}
	return out
}

func cache730SameBodies(t *testing.T, s *Server, before map[string]cache730Body) {
	t.Helper()
	for url, want := range before {
		r := reader631GET(s, "GET", url, "")
		if r.Code != 200 || r.Body.String() != want.body || r.Header().Get("ETag") != want.etag {
			t.Fatalf("immutable response changed or disappeared: %s (%d)", url, r.Code)
		}
		if r := reader631GET(s, "GET", url, want.etag); r.Code != 304 || r.Body.Len() != 0 {
			t.Fatalf("conditional response changed: %s", url)
		}
	}
}

type cache730File struct {
	info os.FileInfo
	data []byte
}

func cache730FileName(s *Server, p ReaderPresentation) string {
	key := strings.TrimPrefix(p.Fingerprint, "sha256:")
	if p.Format == comparisonFormat {
		return s.comparisonFile(key)
	}
	return s.previewFile(key)
}

func cache730Files(t *testing.T, s *Server, refs []ReaderPresentation) map[string]cache730File {
	t.Helper()
	out := map[string]cache730File{}
	for _, p := range refs {
		if p.Format != comparisonFormat && p.Format != previewFormat {
			continue
		}
		file := cache730FileName(s, p)
		stamp := time.Unix(1000000000, 0)
		if err := os.Chtimes(file, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(file)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		out[file] = cache730File{info, data}
	}
	return out
}

func cache730SameFiles(t *testing.T, before map[string]cache730File) {
	t.Helper()
	for file, want := range before {
		info, err := os.Stat(file)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(file)
		if err != nil || !bytes.Equal(data, want.data) || !os.SameFile(info, want.info) || !info.ModTime().Equal(want.info.ModTime()) {
			t.Fatalf("unchanged evidence was rewritten: %s (read error %v)", file, err)
		}
	}
}

func TestProposalCache730RepeatedSnapshotsReuseWork(t *testing.T) {
	s, c, options := cache730Fixture(t)
	first := cache730Snapshot(t, s)
	refs := cache730Refs(first)
	if len(refs) != 3 {
		t.Fatalf("want one comparison and two previews, got %d", len(refs))
	}
	bodies, files := cache730Bodies(t, s, refs), cache730Files(t, s, refs)
	for i := 0; i < 25; i++ {
		got := cache730Snapshot(t, s)
		if !reflect.DeepEqual(refs, cache730Refs(got)) {
			t.Fatal("identical refresh changed a presentation")
		}
	}
	cache730SameFiles(t, files)
	cache730SameBodies(t, s, bodies)
	cache730WantWork(t, s, [3]uint64{1, 2, 3})
	// A caller cannot mutate the presentation held by a cache entry.
	p := s.keepComparison(cache730Clone(t, c))
	p.Message, p.Page = "caller mutation", "/wrong"
	q := s.keepPreview(cache730Clone(t, options[0]))
	q.Message, q.Page = "caller mutation", "/wrong"
	if *s.keepComparison(c) != makeComparison(c).presentation || *s.keepPreview(options[0]) != makePreview(options[0]).presentation {
		t.Fatal("returned presentation aliases the cached record")
	}
	cache730WantWork(t, s, [3]uint64{1, 2, 3})
}

func TestProposalCache730ColdRestoreBuildsOnce(t *testing.T) {
	s, c, options := cache730Fixture(t)
	refs := cache730Refs(cache730Snapshot(t, s))
	before, files := cache730Bodies(t, s, refs), cache730Files(t, s, refs)
	cold := &Server{Cache: s.Cache, Repos: []*Repo{{Name: c.Repo}}}
	cold.loadSnapshot()
	if !reflect.DeepEqual(refs, cache730Refs(cache730State(t, cold))) {
		t.Fatal("healthy restore changed presentation identities")
	}
	cache730SameFiles(t, files)
	cache730SameBodies(t, cold, before)
	cache730WantWork(t, cold, [3]uint64{1, 2, 0})
	for i := 0; i < 25; i++ {
		cold.keepComparison(cache730Clone(t, c))
		for _, e := range options {
			cold.keepPreview(cache730Clone(t, e))
		}
	}
	cache730WantWork(t, cold, [3]uint64{1, 2, 0})
	cache730SameFiles(t, files)
}

func TestProposalCache730CompleteInputIdentity(t *testing.T) {
	_, c, options := cache730Fixture(t)
	comparisons := map[string]func(*comparisonEvidence){
		"repository":        func(e *comparisonEvidence) { e.Repo = "owner/other" },
		"issue":             func(e *comparisonEvidence) { e.Issue++ },
		"format":            func(e *comparisonEvidence) { e.Format += "; changed validation contract" },
		"words":             func(e *comparisonEvidence) { e.Proposal.Name += " changed words" },
		"model":             func(e *comparisonEvidence) { e.Proposal.ModuleText += "\n\\* changed source\n" },
		"include":           func(e *comparisonEvidence) { e.Proposal.Includes["Helper631.tla"] += "\n\\* changed include\n" },
		"view":              func(e *comparisonEvidence) { e.Proposal.View += " " },
		"reader":            func(e *comparisonEvidence) { e.Proposal.Reader.Chart.States[0].Label += " changed reader" },
		"comparison":        func(e *comparisonEvidence) { e.Proposal.Reader.Comparison.Baseline.Name += " changed baseline words" },
		"comparison source": func(e *comparisonEvidence) { e.Proposal.Reader.Comparison.Baseline.Model += "\n\\* baseline\n" },
		"diagnostic":        func(e *comparisonEvidence) { e.Problem = "a distinct literal <diagnostic>" },
	}
	for name, edit := range comparisons {
		t.Run("comparison/"+name, func(t *testing.T) {
			s := &Server{Cache: t.TempDir(), Repos: []*Repo{{Name: c.Repo}, {Name: "owner/other"}}}
			input := cache730Clone(t, c)
			first := *s.keepComparison(input)
			oldBytes := cache730Bodies(t, s, []ReaderPresentation{first})
			edit(&input) // mutate the same pointers and maps after installation
			if input.Proposal.Hash != c.Proposal.Hash || input.Proposal.Reader.Fingerprint != c.Proposal.Reader.Fingerprint {
				t.Fatal("identity fixture changed a retained hash")
			}
			want := makeComparison(cache730Clone(t, input))
			got := *s.keepComparison(input)
			if got == first || got != want.presentation {
				t.Fatal("changed complete evidence did not receive its independently checked presentation")
			}
			if r := reader631GET(s, "GET", got.Page, ""); r.Code != 200 || !bytes.Equal(r.Body.Bytes(), want.html) {
				t.Fatal("changed evidence was not rendered literally")
			}
			if *s.keepComparison(cache730Clone(t, c)) != first || *s.keepComparison(cache730Clone(t, input)) != got {
				t.Fatal("lookup depends on pointer identity or mutated retained inputs")
			}
			cache730SameBodies(t, s, oldBytes)
			cache730WantWork(t, s, [3]uint64{2, 0, 2})
		})
	}
	previews := map[string]func(*previewEvidence){
		"repository":         func(e *previewEvidence) { e.Repo = "owner/other" },
		"issue":              func(e *previewEvidence) { e.Issue++ },
		"format":             func(e *previewEvidence) { e.Format += "; changed renderer contract" },
		"fork":               func(e *previewEvidence) { e.Fork = "F2" },
		"kind":               func(e *previewEvidence) { e.Kind = "changed-kind" },
		"question":           func(e *previewEvidence) { e.Question += " changed question" },
		"option":             func(e *previewEvidence) { e.Option.ID = "C" },
		"words":              func(e *previewEvidence) { e.Option.Says += " <script>literal</script>" },
		"choices":            func(e *previewEvidence) { e.Option.Preview.Choices = map[string]string{"F0": "B"} },
		"model":              func(e *previewEvidence) { e.Option.Preview.Model.ModuleText += "\n\\* model\n" },
		"include":            func(e *previewEvidence) { e.Option.Preview.Model.Includes["Helper603.tla"] += "\n\\* include\n" },
		"view":               func(e *previewEvidence) { e.Option.Preview.Model.View += " " },
		"reader":             func(e *previewEvidence) { e.Option.Preview.Model.Reader.Chart.States[0].Label += " changed reader" },
		"receipt":            func(e *previewEvidence) { e.Option.Preview.Receipt.Project += " changed receipt" },
		"preview diagnostic": func(e *previewEvidence) { e.Option.Preview.Problem = "changed check diagnostic" },
		"cache diagnostic":   func(e *previewEvidence) { e.Problem = "changed cached diagnostic" },
	}
	for name, edit := range previews {
		t.Run("preview/"+name, func(t *testing.T) {
			e := cache730Clone(t, options[0])
			s := &Server{Cache: t.TempDir(), Repos: []*Repo{{Name: e.Repo}, {Name: "owner/other"}}}
			first := *s.keepPreview(e)
			oldBytes := cache730Bodies(t, s, []ReaderPresentation{first})
			edit(&e)
			if e.Option.Preview.Fingerprint != options[0].Option.Preview.Fingerprint || e.Option.Preview.Model.Hash != options[0].Option.Preview.Model.Hash {
				t.Fatal("identity fixture changed a retained hash")
			}
			want := makePreview(cache730Clone(t, e))
			got := *s.keepPreview(e)
			if got == first || got != want.presentation {
				t.Fatal("changed option evidence reused a previously checked presentation")
			}
			if r := reader631GET(s, "GET", got.Page, ""); r.Code != 200 || !bytes.Equal(r.Body.Bytes(), want.html) {
				t.Fatal("changed option words were not rendered literally")
			}
			if *s.keepPreview(cache730Clone(t, options[0])) != first || *s.keepPreview(cache730Clone(t, e)) != got {
				t.Fatal("option lookup depends on retained pointers or fingerprints")
			}
			cache730SameBodies(t, s, oldBytes)
			cache730WantWork(t, s, [3]uint64{0, 2, 2})
		})
	}
}

func TestProposalCache730RetriesFailedPersistence(t *testing.T) {
	_, c, options := cache730Fixture(t)
	for _, kind := range []string{"comparisons", "previews"} {
		t.Run(kind, func(t *testing.T) {
			s := &Server{Cache: t.TempDir(), Repos: []*Repo{{Name: c.Repo}}}
			blocked := filepath.Join(s.Cache, kind)
			if err := os.WriteFile(blocked, []byte("not a directory"), 0600); err != nil {
				t.Fatal(err)
			}
			keep := func() ReaderPresentation {
				if kind == "comparisons" {
					return *s.keepComparison(cache730Clone(t, c))
				}
				return *s.keepPreview(cache730Clone(t, options[0]))
			}
			first := keep()
			bodies := cache730Bodies(t, s, []ReaderPresentation{first})
			if _, err := os.Stat(cache730FileName(s, first)); err == nil {
				t.Fatal("failure fixture unexpectedly saved evidence")
			}
			if err := os.Remove(blocked); err != nil {
				t.Fatal(err)
			}
			if keep() != first {
				t.Fatal("retry changed an already validated presentation")
			}
			files := cache730Files(t, s, []ReaderPresentation{first})
			for i := 0; i < 25; i++ {
				keep()
			}
			cache730SameFiles(t, files)
			cache730SameBodies(t, s, bodies)
			want := [3]uint64{1, 0, 2}
			if kind == "previews" {
				want = [3]uint64{0, 1, 2}
			}
			cache730WantWork(t, s, want)
			entries, err := os.ReadDir(blocked)
			if err != nil || len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".json") {
				t.Fatalf("atomic save left partial evidence: %v, %v", entries, err)
			}
		})
	}
}

func TestProposalCache730ConcurrentReuse(t *testing.T) {
	s, c, options := cache730Fixture(t)
	wantC, wantP := makeComparison(c).presentation, makePreview(options[0]).presentation
	start := make(chan struct{})
	errors := make(chan error, 32)
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		ci, pi := cache730Clone(t, c), cache730Clone(t, options[0])
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 5; j++ {
				gotC, gotP := *s.keepComparison(ci), *s.keepPreview(pi)
				if gotC != wantC || gotP != wantP {
					errors <- fmt.Errorf("concurrent lookup changed a presentation")
					return
				}
				for _, p := range []ReaderPresentation{gotC, gotP} {
					if r := reader631GET(s, "GET", p.SVG, ""); r.Code != 200 || r.Body.Len() == 0 {
						errors <- fmt.Errorf("concurrent SVG read: %d", r.Code)
						return
					}
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	cache730WantWork(t, s, [3]uint64{1, 1, 2})
	for _, p := range []ReaderPresentation{wantC, wantP} {
		var saved any
		b, err := os.ReadFile(cache730FileName(s, p))
		if err != nil || decodeReaderJSON(b, &saved) != nil {
			t.Fatalf("concurrent save did not leave complete JSON: %v", err)
		}
	}
}
