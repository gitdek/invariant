package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/project"
)

// These wire records specify the review API, independently of the Go
// implementation. Hash in Waiting stays the existing ratification shorthand.
// SourceHash is complete and SourceURL is /api/plan-sources/<64 hex>.json.
type source715PlanView struct {
	Summary    string              `json:"summary"`
	Files      []string            `json:"files"`
	Trusted    []string            `json:"trusted"`
	Tests      []plumbing.Test     `json:"tests"`
	Execution  *plumbing.Execution `json:"execution"`
	SourceHash string              `json:"sourceHash"`
	SourceURL  string              `json:"sourceURL"`
}

type source715Waiting struct {
	Hash       string             `json:"hash"`
	Name       string             `json:"name"`
	Statements []Said             `json:"statements"`
	Takes      []string           `json:"takes"`
	Together   bool               `json:"together"`
	Plan       *source715PlanView `json:"plan"`
	Proposal   json.RawMessage    `json:"proposal"`
	IssuePlan  *IssuePlanView     `json:"issuePlan"`
}

type source715State struct {
	Issues []struct {
		Number  int               `json:"number"`
		Stage   string            `json:"stage"`
		Waiting *source715Waiting `json:"waiting"`
		Acted   *Acted            `json:"acted"`
	} `json:"issues"`
}

type source715Response struct {
	Hash      string            `json:"hash"`
	Sources   map[string]string `json:"sources"`
	Languages map[string]string `json:"languages"`
}

var source715Now = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

func source715JSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func source715Proposal(native bool, text string) *formalize.Proposal {
	p := &plumbing.Plan{
		Name: "Review exact sources", Summary: "Review acceptance assertions and their complete identity.",
		Files: []string{"api/value.go", "web/value.ts"}, Trusted: []string{"api/value.go"},
		Tests: []plumbing.Test{
			{Name: "TestReady", File: "api/ready_accept_test.go", Says: "It keeps <literal> & entity-looking &#39; text."},
			{Name: "TestOther", File: "api/other_accept_test.go", Says: "It preserves every source byte."},
		},
		Sources: map[string]string{
			"api/ready_accept_test.go": "package api\r\nimport \"testing\"\r\nfunc TestReady(t *testing.T) { t.Fatal(\"not ready\") }\r\n// <script>& \"quoted\" 'literal'</script>\r\n// " + text,
			"api/other_accept_test.go": "package api\nimport \"testing\"\nfunc TestOther(t *testing.T) { t.Fatal(\"not ready\") }\n// " + text + "\n",
		},
	}
	if native {
		p.Execution = &plumbing.Execution{Base: strings.Repeat("b", 40), Digest: "sha256:" + strings.Repeat("e", 64)}
		p.Tests = []plumbing.Test{
			{Name: "<b>same label</b>", Part: "api", Framework: "go", File: "api/ready_accept_test.go", Selector: "TestReady/exact_case", Says: "The Go case is the selected case."},
			{Name: "<b>same label</b>", Part: "py", Framework: "pytest", File: "py/test_ready_accept.py", Selector: "test_ready_accept.py::test_ready[case-one]", Says: "The Python case is the selected case."},
			{Name: "<b>same label</b>", Part: "web", Framework: "vitest", File: "web/ready.accept.test.ts", Selector: "outer ready [case one]", Says: "The Vitest case is the selected case."},
			{Name: "<b>same label</b>", Part: "js", Framework: "jest", File: "js/ready.accept.test.js", Selector: "outer ready [case two]", Says: "The Jest case is the selected case."},
		}
		p.Sources = map[string]string{
			"api/ready_accept_test.go": "package api\r\nimport \"testing\"\r\nfunc TestReady(t *testing.T) { t.Run(\"exact_case\", func(t *testing.T) { t.Fatal(\"not ready\") }) }\r\n// " + text,
			"py/test_ready_accept.py":  "import pytest\r\n@pytest.mark.parametrize('case', ['case-one'])\r\ndef test_ready(case):\r\n    assert False\r\n# <img src=x onerror=alert(1)> & &#39;\r\n# " + strings.ReplaceAll(text, "\n", "\n# ") + "\r\n",
			"web/ready.accept.test.ts": "import { test, expect } from 'vitest';\ntest('outer ready [case one]', () => { expect(false).toBe(true); });\n// </code><script>bad()</script> \" & '\n// " + text,
			"js/ready.accept.test.js":  "import { test, expect } from '@jest/globals';\ntest('outer ready [case two]', () => { expect(false).toBe(true); });\n// " + text + "\n",
		}
	}
	return &formalize.Proposal{Draft: formalize.Draft{Name: p.Name, Language: "go"}, Plan: p, Hash: p.Hash()}
}

func source715Path(hash string) string {
	return "/api/plan-sources/" + strings.TrimPrefix(hash, "sha256:") + ".json"
}

func source715Set(t *testing.T, s *Server, number int, p *formalize.Proposal, by string) {
	t.Helper()
	r := s.Repos[0]
	if r.src.comments == nil {
		r.src.comments = map[int]cachedComments{}
	}
	at := source715Now.Add(-time.Minute).Format(time.RFC3339)
	found := false
	for _, is := range r.src.issues {
		found = found || is.Number == number
	}
	if !found {
		r.src.issues = append(r.src.issues, github.Issue{
			Number: number, Title: fmt.Sprintf("Proposal %d", number), State: "open", CreatedAt: at, UpdatedAt: at,
			Labels: []github.Label{{Name: factory.LabelTrigger}, {Name: factory.LabelProposal}},
		})
	}
	r.src.comments[number] = cachedComments{updated: at, comments: []github.Comment{{
		ID: int64(number), CreatedAt: at, User: github.User{Login: by, Type: "Bot"},
		Body: marker(t, factory.Marker{Kind: factory.KindProposal, Proposal: p}),
	}}}
}

func source715Server(t *testing.T, proposals ...*formalize.Proposal) *Server {
	t.Helper()
	s := &Server{Cache: t.TempDir(), Repos: []*Repo{{Name: "owner/review", FactoryLogin: "factory[bot]"}}, Log: t.Logf}
	for i, p := range proposals {
		source715Set(t, s, 715+i, p, "factory[bot]")
	}
	return s
}

func source715Publish(t *testing.T, s *Server) []byte {
	t.Helper()
	gz, err := s.publish(s.assemble(source715Now))
	if err != nil {
		t.Fatal(err)
	}
	s.saveSnapshot(gz)
	b, err := gunzip(gz)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func source715GET(s *Server, method, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}

func source715ReadState(t *testing.T, s *Server) (source715State, []byte) {
	t.Helper()
	w := source715GET(s, http.MethodGet, "/api/state.json")
	if w.Code != http.StatusOK {
		t.Fatalf("state: HTTP %d: %s", w.Code, w.Body.String())
	}
	var state source715State
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	return state, w.Body.Bytes()
}

func source715WaitingFor(t *testing.T, state source715State, number int) *source715Waiting {
	t.Helper()
	for _, is := range state.Issues {
		if is.Number == number && is.Waiting != nil {
			return is.Waiting
		}
	}
	t.Fatalf("issue %d has no waiting view", number)
	return nil
}

func source715Exact(t *testing.T, s *Server, p *formalize.Proposal) {
	t.Helper()
	w := source715GET(s, http.MethodGet, source715Path(p.Hash))
	if w.Code != http.StatusOK {
		t.Fatalf("sources for %s: HTTP %d: %s", p.Hash, w.Code, w.Body.String())
	}
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("source response is not JSON: %v", w.Header())
	}
	var got source715Response
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	languages := map[string]string{}
	for _, file := range p.Plan.TestFiles() {
		languages[file] = p.Plan.SourceLanguage(file)
	}
	if got.Hash != p.Hash || !reflect.DeepEqual(got.Sources, p.Plan.Sources) || !reflect.DeepEqual(got.Languages, languages) {
		t.Fatalf("source identity, exact bytes or language hints changed for %s (got hash %s, %d sources, %d languages)", p.Hash, got.Hash, len(got.Sources), len(got.Languages))
	}
}

func source715Cold(t *testing.T, cache string) *Server {
	t.Helper()
	// prepare is startup before the first refresh. There is deliberately no
	// working GitHub executable and no network dependency in this fixture.
	s := &Server{Cache: cache, Repos: []*Repo{{
		Name: "owner/review", FactoryLogin: "factory[bot]",
		GitHub: github.Client{Repo: "owner/review", GH: filepath.Join(cache, "no-github-executable")},
	}}, Log: t.Logf}
	s.prepare()
	return s
}

func source715NoBodies(t *testing.T, b []byte, proposals ...*formalize.Proposal) {
	t.Helper()
	var value any
	if err := json.Unmarshal(b, &value); err != nil {
		t.Fatal(err)
	}
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			for k, x := range v {
				if k == "sources" {
					if m, ok := x.(map[string]any); ok && len(m) != 0 {
						t.Fatal("polled state still contains an acceptance source map")
					}
				}
				walk(x)
			}
		case []any:
			for _, x := range v {
				walk(x)
			}
		case string:
			for _, p := range proposals {
				for _, source := range p.Plan.Sources {
					if source != "" && strings.Contains(v, source) {
						t.Fatal("polled state contains a full acceptance body in another field")
					}
				}
			}
		}
	}
	walk(value)
}

func Test715PolledPlansStaySmallAndKeepReviewIdentity(t *testing.T) {
	var sizes []int
	for _, padding := range []string{"small source", strings.Repeat("// source715 large acceptance body\r\n", 8192)} {
		a, b := source715Proposal(false, padding), source715Proposal(true, padding)
		beforeA, beforeB := source715JSON(t, a), source715JSON(t, b)
		s := source715Server(t, a, b)
		beforeComments := source715JSON(t, s.Repos[0].src.comments[715].comments)
		for _, p := range []*formalize.Proposal{a, b} {
			w := waitingFor(factory.Marker{Kind: factory.KindProposal, Proposal: p}, source715Now)
			source715NoBodies(t, source715JSON(t, w), p)
		}
		source715Publish(t, s)
		state, body := source715ReadState(t, s)
		source715NoBodies(t, body, a, b)
		sizes = append(sizes, len(body))
		if len(body) > 24000 {
			t.Fatalf("two waiting plans send %d uncompressed state bytes; source bodies belong to the source endpoint", len(body))
		}
		for i, p := range []*formalize.Proposal{a, b} {
			w := source715WaitingFor(t, state, 715+i)
			if w.Plan == nil {
				t.Fatal("lost plumbing plan")
			}
			v := w.Plan
			if w.Hash != strings.TrimPrefix(p.Hash, "sha256:")[:12] || w.Name != p.Name || !w.Together ||
				!reflect.DeepEqual(w.Takes, []string{factory.Ratify, factory.Revise}) {
				t.Fatalf("changed ratification view: %+v", w)
			}
			if v.SourceHash != p.Hash || v.SourceURL != source715Path(p.Hash) {
				t.Fatalf("source authority must be the full proposal hash, separate from %s: %+v", w.Hash, v)
			}
			if v.Summary != p.Plan.Summary || !reflect.DeepEqual(v.Files, p.Plan.Files) ||
				!reflect.DeepEqual(v.Trusted, p.Plan.Trusted) || !reflect.DeepEqual(v.Tests, p.Plan.Tests) ||
				!reflect.DeepEqual(v.Execution, p.Plan.Execution) {
				t.Fatal("lightweight state lost review metadata or full native identities")
			}
			if len(w.Statements) != len(p.Plan.Tests) {
				t.Fatal("lost acceptance assertions")
			}
			for j, test := range p.Plan.Tests {
				if w.Statements[j] != (Said{Name: test.Name, Kind: "test", Says: test.Says}) {
					t.Fatal("changed an acceptance assertion")
				}
			}
		}
		if !bytes.Equal(source715JSON(t, a), beforeA) || !bytes.Equal(source715JSON(t, b), beforeB) ||
			!bytes.Equal(source715JSON(t, s.Repos[0].src.comments[715].comments), beforeComments) {
			t.Fatal("state projection changed the authoritative proposal or factory marker")
		}
	}
	if sizes[1] > sizes[0]+256 {
		t.Fatalf("source growth increased polled state from %d to %d bytes", sizes[0], sizes[1])
	}
}

func Test715SourceEndpointReturnsVerbatimLegacyAndNativeFiles(t *testing.T) {
	a, b := source715Proposal(false, "legacy no final newline"), source715Proposal(true, "native no final newline")
	s := source715Server(t, a, b)
	source715Publish(t, s)
	for _, p := range []*formalize.Proposal{a, b} {
		for range 3 {
			source715Exact(t, s, p)
		}
	}
	_, state := source715ReadState(t, s)
	source715NoBodies(t, state, a, b)
}

func Test715SourceEndpointAcceptsOnlyFullHashesAndReadMethods(t *testing.T) {
	p := source715Proposal(false, "literal source715 endpoint")
	s := source715Server(t, p)
	source715Publish(t, s)
	source715Exact(t, s, p)
	path := source715Path(p.Hash)
	get := source715GET(s, "GET", path)
	head := source715GET(s, "HEAD", path)
	if head.Code != http.StatusOK || head.Body.Len() != 0 {
		t.Fatalf("HEAD sources: HTTP %d with %d body bytes", head.Code, head.Body.Len())
	}
	page := source715GET(s, "GET", "/")
	for _, w := range []*httptest.ResponseRecorder{get, head} {
		for _, header := range []string{"Content-Security-Policy", "X-Content-Type-Options", "Referrer-Policy", "Cross-Origin-Opener-Policy", "Permissions-Policy"} {
			if w.Header().Get(header) == "" || w.Header().Get(header) != page.Header().Get(header) {
				t.Fatalf("sources lost public security header %s: %v", header, w.Header())
			}
		}
	}
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
		w := source715GET(s, method, path)
		if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "GET, HEAD" {
			t.Fatalf("%s sources: HTTP %d, Allow %q", method, w.Code, w.Header().Get("Allow"))
		}
	}
	full := strings.TrimPrefix(p.Hash, "sha256:")
	for _, bad := range []string{
		strings.Repeat("0", 64), full[:12], full[:63], full + "0",
		full[:12] + strings.Repeat("f", 52), strings.ToUpper(full),
		"715", "sha256:" + full, "api/ready_accept_test.go", "..%2f..%2fgo.mod",
	} {
		for _, method := range []string{"GET", "HEAD"} {
			w := source715GET(s, method, "/api/plan-sources/"+bad+".json")
			if w.Code < 400 || w.Code >= 600 {
				t.Fatalf("%s accepted invalid/unknown source authority %q: HTTP %d", method, bad, w.Code)
			}
		}
	}
	for _, bad := range []string{"/api/plan-sources/.json?issue=715", "/api/plan-sources/.json?path=api/ready_accept_test.go", "/api/plan-sources/.json?hash=" + full} {
		if w := source715GET(s, "GET", bad); w.Code < 400 {
			t.Fatalf("query supplied source authority: %s", bad)
		}
	}
}

func Test715SourceAuthorityRequiresTheCompleteFactoryProposal(t *testing.T) {
	good := source715Proposal(true, "source715 genuine")
	s := source715Server(t, good)
	source715Publish(t, s)
	source715Exact(t, s, good)
	cases := []struct {
		name string
		edit func(*formalize.Proposal)
		by   string
	}{
		{"changed source", func(p *formalize.Proposal) { p.Plan.Sources[p.Plan.Tests[0].File] += "changed" }, "factory[bot]"},
		{"changed summary", func(p *formalize.Proposal) { p.Plan.Summary += "changed" }, "factory[bot]"},
		{"changed writable files", func(p *formalize.Proposal) { p.Plan.Files = append(p.Plan.Files, "other.go") }, "factory[bot]"},
		{"changed selector", func(p *formalize.Proposal) { p.Plan.Tests[0].Selector += "/changed" }, "factory[bot]"},
		{"changed execution digest", func(p *formalize.Proposal) { p.Plan.Execution.Digest = "sha256:" + strings.Repeat("a", 64) }, "factory[bot]"},
		{"missing source with self-consistent hash", func(p *formalize.Proposal) { delete(p.Plan.Sources, p.Plan.Tests[0].File); p.Hash = p.Plan.Hash() }, "factory[bot]"},
		{"no sources with self-consistent hash", func(p *formalize.Proposal) { p.Plan.Sources = nil; p.Hash = p.Plan.Hash() }, "factory[bot]"},
		{"short saved hash", func(p *formalize.Proposal) { p.Hash = p.Hash[:19] }, "factory[bot]"},
		{"same display hash", func(p *formalize.Proposal) { p.Hash = p.Hash[:19] + strings.Repeat("0", 52) }, "factory[bot]"},
		{"another author", func(*formalize.Proposal) {}, "other[bot]"},
		{"unknown factory", func(*formalize.Proposal) {}, "factory[bot]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := source715Proposal(true, "source715 genuine")
			tc.edit(p)
			s := source715Server(t)
			if tc.name == "unknown factory" {
				s.Repos[0].FactoryLogin = ""
			}
			source715Set(t, s, 715, p, tc.by)
			before := source715JSON(t, p)
			body := source715Publish(t, s)
			source715NoBodies(t, body, p)
			for _, hash := range []string{p.Hash, p.Plan.Hash()} {
				w := source715GET(s, "GET", source715Path(hash))
				if w.Code < 400 || w.Code >= 600 {
					t.Fatalf("%s published unverified authority %s: HTTP %d", tc.name, hash, w.Code)
				}
			}
			if !bytes.Equal(before, source715JSON(t, p)) {
				t.Fatal("invalid proposal was rewritten rather than refused as source authority")
			}
		})
	}
}

func Test715SourceURLsStayIsolatedAcrossReplacementAndRestart(t *testing.T) {
	a := source715Proposal(false, "source715 first proposal")
	b := source715Proposal(false, "source715 replacement proposal")
	s := source715Server(t, a)
	source715Publish(t, s)
	source715Exact(t, s, a)
	source715Set(t, s, 715, b, "factory[bot]")
	source715Publish(t, s)
	state, _ := source715ReadState(t, s)
	v := source715WaitingFor(t, state, 715).Plan
	if v == nil || v.SourceHash != b.Hash || v.SourceURL != source715Path(b.Hash) {
		t.Fatalf("replacement retained old source identity: %+v", v)
	}
	for _, server := range []*Server{s, source715Cold(t, s.Cache)} {
		source715Exact(t, server, a)
		source715Exact(t, server, b)
		var wg sync.WaitGroup
		errors := make(chan string, 24)
		for i := range 24 {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				p := a
				if i%2 != 0 {
					p = b
				}
				w := source715GET(server, "GET", source715Path(p.Hash))
				var got source715Response
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Hash != p.Hash || !reflect.DeepEqual(got.Sources, p.Plan.Sources) {
					errors <- p.Hash
				}
			}(i)
		}
		wg.Wait()
		close(errors)
		for hash := range errors {
			t.Errorf("concurrent retrieval crossed or lost proposal %s", hash)
		}
	}
}

func Test715SavedSourcesAreAvailableBeforeAnyGitHubRead(t *testing.T) {
	a, b := source715Proposal(false, "source715 cached legacy"), source715Proposal(true, "source715 cached native")
	s := source715Server(t, a, b)
	source715Publish(t, s)
	// No source endpoint has been opened. Persistence accompanies publication.
	cold := source715Cold(t, s.Cache)
	state, body := source715ReadState(t, cold)
	source715NoBodies(t, body, a, b)
	for i, p := range []*formalize.Proposal{a, b} {
		v := source715WaitingFor(t, state, 715+i).Plan
		if v == nil || v.SourceHash != p.Hash || v.SourceURL != source715Path(p.Hash) {
			t.Fatalf("restart lost matching source identity: %+v", v)
		}
		source715Exact(t, cold, p)
	}
}

// Work only inside this fixture's otherwise empty cache, retaining its
// lightweight snapshot. Cache records follow the dashboard's existing JSON
// or gzipped-JSON convention; no private helper or filename is prescribed.
func source715Damage(t *testing.T, s *Server, how string) {
	t.Helper()
	changed := 0
	err := filepath.WalkDir(s.Cache, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path == s.snapshotFile() {
			return err
		}
		if how == "missing" {
			changed++
			return os.Remove(path)
		}
		if how == "truncated" {
			changed++
			return os.WriteFile(path, []byte("{"), 0600)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		compressed := len(raw) >= 2 && raw[0] == 0x1f && raw[1] == 0x8b
		if compressed {
			raw, err = gunzip(raw)
			if err != nil {
				return err
			}
		}
		replaced := bytes.ReplaceAll(raw, []byte("source715_CACHE_AUTHORITY"), []byte("source715_CACHE_TAMPERING"))
		if bytes.Equal(raw, replaced) {
			return nil
		}
		if !json.Valid(replaced) {
			return fmt.Errorf("tampering must leave a well-formed cache record")
		}
		changed++
		if compressed {
			replaced, err = gzipped(replaced)
			if err != nil {
				return err
			}
		}
		return os.WriteFile(path, replaced, 0600)
	})
	if err != nil {
		t.Fatal(err)
	}
	if changed == 0 {
		t.Fatal("no persisted source payload to damage independently of the snapshot")
	}
}

func Test715DamagedSourceCachesCannotBecomeSuccessfulReviews(t *testing.T) {
	for _, damage := range []string{"missing", "truncated", "valid JSON tampering"} {
		t.Run(damage, func(t *testing.T) {
			p := source715Proposal(true, "source715_CACHE_AUTHORITY")
			s := source715Server(t, p)
			source715Publish(t, s)
			source715Exact(t, s, p)
			source715Damage(t, s, damage)
			cold := source715Cold(t, s.Cache)
			state, body := source715ReadState(t, cold)
			source715NoBodies(t, body, p)
			w := source715WaitingFor(t, state, 715)
			if w.Hash != strings.TrimPrefix(p.Hash, "sha256:")[:12] || w.Plan == nil || !reflect.DeepEqual(w.Plan.Tests, p.Plan.Tests) {
				t.Fatal("unavailable sources changed review identities or ratification shorthand")
			}
			for range 2 {
				if res := source715GET(cold, "GET", source715Path(p.Hash)); res.Code < 400 || res.Code >= 600 {
					t.Fatalf("damaged source cache became a successful review: HTTP %d", res.Code)
				}
			}
			// A later authoritative refresh restores the same identity; a failed
			// request or rejected cache must not permanently poison its URL.
			source715Set(t, cold, 715, p, "factory[bot]")
			source715Publish(t, cold)
			source715Exact(t, cold, p)
			source715Exact(t, source715Cold(t, s.Cache), p)
		})
	}
}

func Test715OldInlineSnapshotsAreDiscardedBeforeServing(t *testing.T) {
	for _, native := range []bool{false, true} {
		p := source715Proposal(native, "source715 obsolete inline body")
		s := source715Server(t)
		// Construct the old wire form directly: its hash is only the display
		// prefix, even though the inline map happens to match a real plan.
		old := map[string]any{
			"repo": "owner/review",
			"issues": []any{map[string]any{
				"repo": "owner/review", "number": 715, "open": true, "stage": StageRatifying,
				"waiting": map[string]any{
					"kind": factory.KindProposal, "name": p.Name, "hash": strings.TrimPrefix(p.Hash, "sha256:")[:12],
					"plan": map[string]any{"summary": p.Plan.Summary, "files": p.Plan.Files, "trusted": p.Plan.Trusted,
						"tests": p.Plan.Tests, "execution": p.Plan.Execution, "sources": p.Plan.Sources},
				},
			}},
		}
		gz, err := gzipped(source715JSON(t, old))
		if err != nil {
			t.Fatal(err)
		}
		s.saveSnapshot(gz)
		cold := source715Cold(t, s.Cache)
		if w := source715GET(cold, "GET", "/api/state.json"); w.Code != http.StatusServiceUnavailable {
			t.Fatalf("old inline snapshot was served: HTTP %d", w.Code)
		}
		if w := source715GET(cold, "GET", source715Path(p.Hash)); w.Code < 400 {
			t.Fatal("short-hash legacy state invented verified full-hash authority")
		}
		source715Set(t, cold, 715, p, "factory[bot]")
		source715Publish(t, cold)
		source715Exact(t, cold, p)
	}
}

func Test715SourceDeliveryPreservesActAndOtherProposalViews(t *testing.T) {
	p := source715Proposal(false, "source715 reviewed before ratification")
	modeled := &formalize.Proposal{Draft: formalize.Draft{Name: "A modeled rule",
		Statements: []project.Statement{{Name: "Safe", Kind: "invariant", Says: "The existing rule stays."}}},
		ModuleText: "---- MODULE Existing ----\n====", Hash: "sha256:" + strings.Repeat("a", 64)}
	issuePlan := &formalize.Proposal{Draft: formalize.Draft{Name: "Existing steps"},
		IssuePlan: &formalize.IssuePlan{Summary: "Keep the existing steps.", Issues: []formalize.PlannedIssue{{Title: "First", Body: "Keep this step."}}},
		Hash:      "sha256:" + strings.Repeat("c", 64)}
	s := source715Server(t, p, modeled, issuePlan)
	team := newAccessTeam(t)
	s.Access = team.access()
	owner := team.token(t, team.key, "RS256", team.claims(nil))
	var posted []string
	s.Post = func(_ context.Context, repo string, issue int, body string) (string, error) {
		posted = append(posted, fmt.Sprintf("%s#%d %s", repo, issue, body))
		return "https://github.com/owner/review/issues/715#issuecomment-1", nil
	}
	before := source715JSON(t, p)
	source715Publish(t, s)
	source715Exact(t, s, p)
	state, _ := source715ReadState(t, s)
	m := source715WaitingFor(t, state, 716)
	if m.Plan != nil || !bytes.Equal(m.Proposal, source715JSON(t, modeled)) || !reflect.DeepEqual(m.Statements, []Said{{Name: "Safe", Kind: "invariant", Says: "The existing rule stays."}}) {
		t.Fatal("modeled proposal view changed")
	}
	ip := source715WaitingFor(t, state, 717)
	if ip.Plan != nil || ip.IssuePlan == nil || ip.IssuePlan.Summary != issuePlan.IssuePlan.Summary || !reflect.DeepEqual(ip.IssuePlan.Issues, []string{"First"}) {
		t.Fatal("issue-plan view changed")
	}
	body := "/invariant ratify " + strings.TrimPrefix(p.Hash, "sha256:")[:12]
	r := httptest.NewRequest("POST", "https://invariant.example.com/act/api/comment",
		bytes.NewReader(source715JSON(t, act{Repo: "owner/review", Issue: 715, Body: body})))
	r.Header.Set("Cf-Access-Jwt-Assertion", owner)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Invariant", "act")
	r.Header.Set("Origin", "https://invariant.example.com")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK || !reflect.DeepEqual(posted, []string{"owner/review#715 " + body}) {
		t.Fatalf("existing short-hash ratification failed: HTTP %d, posted %q", w.Code, posted)
	}
	after, bytesAfter := source715ReadState(t, s)
	source715NoBodies(t, bytesAfter, p)
	found := false
	for _, is := range after.Issues {
		if is.Number == 715 {
			found = true
			if is.Waiting != nil || is.Acted == nil || is.Acted.Command != body || is.Stage != StageQueued {
				t.Fatal("acted publication lost the command or still waits for ratification")
			}
		}
	}
	if !found || !bytes.Equal(before, source715JSON(t, p)) {
		t.Fatal("ratification lost the issue or mutated the original proposal")
	}
	source715Exact(t, s, p)
}

// As in the existing embedded-asset acceptance tests, this pins served
// wiring, not execution of JavaScript. docs/factory.md must supply the
// reproducible controlled-response browser check for the interaction
// contract: both pages, no eager/poll downloads, concurrent/repeated opens,
// literal rendering, open-view retention, failure/retry, missing or wrong
// response identities and an old response arriving after replacement.
func Test715BothPagesWireTheSharedLazySourceDisclosure(t *testing.T) {
	team := newAccessTeam(t)
	s := &Server{Access: team.access(), Repos: []*Repo{{Name: "owner/review"}}}
	owner := team.token(t, team.key, "RS256", team.claims(nil))
	for _, path := range []string{"/", "/act"} {
		r := httptest.NewRequest("GET", path, nil)
		if path == "/act" {
			r.Header.Set("Cf-Access-Jwt-Assertion", owner)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "/app.js?v="+version) {
			t.Fatalf("%s does not serve the shared proposal consumer", path)
		}
	}
	script := source715GET(s, "GET", "/app.js?v="+version)
	if script.Code != 200 {
		t.Fatal("shared page script unavailable")
	}
	js := script.Body.String()
	for _, hook := range []string{"data-plan-sources", "data-source-retry", "sourceHash", "sourceURL", "planSource(", "language-"} {
		if !strings.Contains(js, hook) {
			t.Fatalf("both pages need the shared lazy-source review hook %q", hook)
		}
	}
	if !regexp.MustCompile("addEventListener\\(\\s*[\"']toggle[\"']").MatchString(js) {
		t.Fatal("source disclosure has no opening handler")
	}
	if regexp.MustCompile("w\\.plan\\.sources\\b").MatchString(js) {
		t.Fatal("shared proposal renderer still reads inline acceptance bodies")
	}
	if !strings.Contains(js, "esc(source.slice(") || !strings.Contains(js, "esc(token)") {
		t.Fatal("source highlighting lost literal token escaping")
	}
}
