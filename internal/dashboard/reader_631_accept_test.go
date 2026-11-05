package dashboard

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"io/fs"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/verify"
)

// Presentation contract: each modeled Project has reader with status, message,
// commit, fingerprint, format, page and (only for a chart) svg. page and svg are
// same-origin immutable content URLs. page is HTML usable without scripts; svg
// is a complete native SVG. The surrounding dashboard embeds that image and
// links the page. Plumbing issues acquire neither a reader nor these links.
// Diagnostic status distinguishes missing_view, legacy_receipt,
// missing_artifacts, snapshot_mismatch, too_large and the receipt's own failure
// code. Additional diagnostic codes may distinguish damaged artifacts.
type reader631Presentation struct {
	Status, Message, Commit, Fingerprint, Format, Page, SVG string
}
type reader631Project struct {
	Repo, Dir, Name string
	Reader          *reader631Presentation
}
type reader631Fixture struct {
	Dir      string
	Manifest project.Manifest
	Lock     project.Lock
	Files    map[string][]byte // paths relative to the project
	Report   *verify.Report
}

func reader631JSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func reader631Hash(b []byte) string { return fmt.Sprintf("sha256:%x", sha256.Sum256(b)) }
func reader631Write(t *testing.T, p string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
}

const reader631Model = `---- MODULE Dashboard631 ----
EXTENDS Naturals, Helper631
CONSTANT Limit
VARIABLE n
vars == <<n>>
Init == n = Zero
Advance == /\ n < Limit /\ n' = n + 1
Reset == /\ n = Limit /\ n' = Zero
Poll == UNCHANGED vars
Next == Advance \/ Reset \/ Poll
Spec == Init /\ [][Next]_vars
Safe == n \in 0..Limit
====
`

func reader631FixtureFor(t *testing.T, n int, label string) reader631Fixture {
	t.Helper()
	f := reader631Fixture{Dir: "examples/reader", Manifest: project.Manifest{Name: "Reader example", Module: ".invariant/specs/Dashboard631.tla", Code: "core", Language: "go"}, Files: map[string][]byte{}}
	f.Files[f.Manifest.Module] = []byte(reader631Model)
	f.Files[".invariant/specs/Helper631.tla"] = []byte("---- MODULE Helper631 ----\nZero == 0\n====\n")
	f.Lock = project.Lock{Bounds: map[string]string{"Limit": strconv.Itoa(n - 1)}, Statements: []project.Statement{
		{Name: "Spec", Kind: project.Spec, Says: "Start, advance, reset or poll."},
		{Name: "Safe", Kind: project.Invariant, Says: "Keep  every result <safe> & literal.\nExactly as ratified."},
	}}
	for i := range f.Lock.Statements {
		s := &f.Lock.Statements[i]
		var err error
		s.SHA256, err = tla.PinHash(reader631Model, s.Name, project.Model)
		if err != nil {
			t.Fatal(err)
		}
	}
	view := fmt.Sprintf(`{"version":1,"state":[
 {"id":"initial","when":{"eq":[{"var":"n"},"0"]},"label":%s},
 {"id":"finished","when":{"eq":[{"var":"n"},%s]},"label":"Finished"},
 {"id":"working","when":true,"label":"Working"}],
 "step":[{"id":"poll-detail","when":{"eq":[{"action":true},"Poll"]},"label":"Internal poll","hide":true},
 {"id":"advance","when":{"eq":[{"action":true},"Advance"]},"label":"Continue <now> & later"},
 {"id":"reset","when":true,"label":"Start again"}]}`, strconv.Quote(label), strconv.Quote(strconv.Itoa(n-1)))
	f.Files[".invariant/view.json"] = []byte(view)
	g := tlc.GraphResult{Module: "Dashboard631", Config: tlc.Config{Specification: "Spec", Constants: f.Lock.Bounds, Invariants: []string{"Safe"}}, Tool: tlc.GraphTool{Image: "java@sha256:" + strings.Repeat("a", 64), JarSHA256: strings.Repeat("b", 64)}, Run: tlc.Result{Outcome: tlc.Passed, Version: "TLC2 Version 2.19", Depth: n}}
	g.Graph.Initial = []string{"0"}
	truth := map[string]map[string]bool{}
	for i := 0; i < n; i++ {
		id := strconv.Itoa(i)
		g.Graph.States = append(g.Graph.States, tlc.GraphState{ID: id, Label: "n = " + id, Values: map[string]string{"n": id}})
		to, action := (i+1)%n, "Advance"
		if i == n-1 {
			action = "Reset"
		}
		g.Graph.Steps = append(g.Graph.Steps, tlc.GraphStep{From: id, To: strconv.Itoa(to), Action: action}, tlc.GraphStep{From: id, To: id, Action: "Poll"})
		truth[id] = map[string]bool{"Safe": true}
	}
	g.StateCount, g.StepCount = n, len(g.Graph.Steps)
	g.Run.DistinctStates, g.Run.StatesGenerated = int64(n), int64(g.StepCount+1)
	r := &verify.Report{Project: f.Manifest.Name, Passed: true, ModelOnly: true, Bounds: f.Lock.Bounds, Proposal: project.ProposalHash(f.Lock.Bounds, f.Lock.Statements), Design: verify.Design{Passed: true, Outcome: string(tlc.Passed), Spec: "Spec", Invariants: []string{"Safe"}, DistinctStates: int64(n), StatesGenerated: g.Run.StatesGenerated, Depth: n}, Toolchain: verify.Toolchain{TLC: g.Run.Version, JavaImage: g.Tool.Image, TLCJarSHA256: g.Tool.JarSHA256}}
	r.Reader = &verify.Reader{Version: 1}
	if n <= statechart.MaxStates {
		var err error
		r.Reader.Chart, err = statechart.Compute(t.Context(), statechart.Input{Graph: g, Lock: f.Lock, Invariants: truth}, []byte(view))
		if err != nil {
			t.Fatal(err)
		}
	} else {
		r.Reader.Limitation = &statechart.Limitation{Code: "too_large", Actual: n, Limit: 500, Source: statechart.Source{Module: g.Module, Config: g.Config, Tool: g.Tool, TLC: g.Run.Version}}
	}
	f.Report = r
	f.bind(t)
	return f
}

func (f *reader631Fixture) bind(t *testing.T) {
	t.Helper()
	f.Files[".invariant/invariant.json"] = reader631JSON(t, f.Manifest)
	f.Files[".invariant/ratified.lock"] = reader631JSON(t, f.Lock)
	r := f.Report
	r.Bounds, r.Proposal, r.Pins = f.Lock.Bounds, project.ProposalHash(f.Lock.Bounds, f.Lock.Statements), nil
	for _, s := range f.Lock.Statements {
		r.Pins = append(r.Pins, verify.Pin{Name: s.Name, Kind: s.Kind, Says: s.Says, Want: s.SHA256, Got: s.SHA256, Match: true})
	}
	if rr := r.Reader; rr != nil {
		rr.Inputs = verify.ReaderInputs{Module: strings.TrimSuffix(path.Base(f.Manifest.Module), ".tla"), Model: reader631Hash(f.Files[f.Manifest.Module]), Includes: map[string]string{}}
		if view, ok := f.Files[".invariant/view.json"]; ok {
			rr.Inputs.View = reader631Hash(view)
		}
		for p, b := range f.Files {
			if p != f.Manifest.Module && path.Dir(p) == path.Dir(f.Manifest.Module) && strings.HasSuffix(p, ".tla") {
				rr.Inputs.Includes[path.Base(p)] = reader631Hash(b)
			}
		}
		rr.Fingerprint = ""
		rr.Fingerprint = reader631Hash(reader631JSON(t, struct {
			Reader verify.Reader `json:"reader"`
			Lock   project.Lock  `json:"lock"`
		}{*rr, f.Lock}))
	}
	r.Fingerprint = verify.Fingerprint(*r)
}

// Use the production receipt writer and the same per-project directory layout
// as CI's whole-directory artifact. Edits simulate missing or inconsistent
// artifacts; none of the renderer or dashboard's computations is mocked.
func reader631Archive(t *testing.T, fixtures []reader631Fixture, edit func(map[string][]byte)) []byte {
	t.Helper()
	entries := map[string][]byte{}
	for _, f := range fixtures {
		dir := t.TempDir()
		if err := receipt.Write(dir, f.Report); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"receipt.json", "receipt.md", "reader.json", "reader.svg"} {
			b, err := os.ReadFile(filepath.Join(dir, name))
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			entries[path.Base(f.Dir)+"/"+name] = b
		}
	}
	if edit != nil {
		edit(entries)
	}
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	names := make([]string, 0, len(entries))
	for p := range entries {
		names = append(names, p)
	}
	sort.Strings(names)
	for _, p := range names {
		w, err := z.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(entries[p]); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// The sole subprocess double is gh. It accepts only literal read requests,
// and only paths at the fixture receipt commit. In particular, main and a
// newer view cannot answer a read. Its flag contract matches github.Client.
func init() {
	if len(os.Args) < 3 || os.Args[1] != "--reader631-gh" {
		return
	}
	dir, args := os.Args[2], os.Args[3:]
	if len(args) > 0 && args[0] == "auth" {
		os.Exit(1)
	}
	if len(args) < 7 || args[0] != "api" || args[1] != "--include" || args[2] != "-X" || args[3] != "GET" {
		fmt.Fprintln(os.Stderr, "reader631 gh: unexpected arguments", args)
		os.Exit(91)
	}
	endpoint := args[len(args)-1]
	log, err := os.OpenFile(filepath.Join(dir, "calls"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(92)
	}
	fmt.Fprintln(log, endpoint)
	log.Close()
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(endpoint)))
	b, err := os.ReadFile(filepath.Join(dir, key))
	if err != nil {
		fmt.Print("HTTP/1.1 404 Not Found\r\n\r\nfixture endpoint absent")
		os.Exit(1)
	}
	fmt.Print("HTTP/1.1 200 OK\r\n\r\n")
	os.Stdout.Write(b)
	os.Exit(0)
}

func reader631Repo(t *testing.T, name, sha string, run int64, fixtures []reader631Fixture, archive []byte) *Repo {
	t.Helper()
	dir := t.TempDir()
	write := func(endpoint string, b []byte) {
		reader631Write(t, filepath.Join(dir, fmt.Sprintf("%x", sha256.Sum256([]byte("repos/"+name+"/"+endpoint)))), b)
	}
	write(fmt.Sprintf("actions/runs/%d/artifacts?per_page=100", run), []byte(`{"artifacts":[{"id":631,"name":"receipts"}]}`))
	write("actions/artifacts/631/zip", archive)
	var tree []github.TreeEntry
	for _, f := range fixtures {
		for p, b := range f.Files {
			p = f.Dir + "/" + p
			tree = append(tree, github.TreeEntry{Type: "blob", Path: p, SHA: fmt.Sprintf("%x", sha256.Sum256(b))})
			write("contents/"+p+"?ref="+url.QueryEscape(sha), b)
		}
	}
	sort.Slice(tree, func(i, j int) bool { return tree[i].Path < tree[j].Path })
	write("git/trees/"+sha+"?recursive=1", reader631JSON(t, map[string]any{"tree": tree}))
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	gh := filepath.Join(dir, "gh")
	reader631Write(t, gh, []byte("#!/bin/sh\nexec "+quote(exe)+" --reader631-gh "+quote(dir)+" \"$@\"\n"))
	if err := os.Chmod(gh, 0755); err != nil {
		t.Fatal(err)
	}
	return &Repo{Name: name, GitHub: github.Client{Repo: name, GH: gh}, src: sources{files: map[string][]byte{}, gates: map[int64]string{run: gateRan}, runs: []github.Run{{ID: run, Name: "gate", Status: "completed", Conclusion: "success", HeadSHA: sha}}}}
}

func reader631Load(t *testing.T, s *Server, r *Repo) []reader631Project {
	t.Helper()
	if err := s.readReceipts(t.Context(), r); err != nil {
		t.Fatalf("receipt archive load: %v", err)
	}
	snap := s.assemble(time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC))
	b := reader631JSON(t, snap)
	gz, err := gzipped(b)
	if err != nil {
		t.Fatal(err)
	}
	s.state = gz
	s.saveSnapshot(gz)
	var out struct{ Projects []reader631Project }
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out.Projects
}
func reader631Find(t *testing.T, ps []reader631Project, repo, dir string) reader631Presentation {
	t.Helper()
	for _, p := range ps {
		if p.Repo == repo && p.Dir == dir {
			if p.Reader == nil {
				t.Fatalf("%s:%s has no reader presentation in dashboard evidence", repo, dir)
			}
			return *p.Reader
		}
	}
	t.Fatalf("missing project %s:%s in %+v", repo, dir, ps)
	return reader631Presentation{}
}
func reader631GET(s *Server, method, u, etag string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, u, nil)
	if etag != "" {
		r.Header.Set("If-None-Match", etag)
	}
	s.Handler().ServeHTTP(w, r)
	return w
}

func reader631Chart(t *testing.T, s *Server, p reader631Presentation, want *statechart.Result) string {
	t.Helper()
	if p.Status != "chart" || p.SVG == "" || p.Page == "" || p.Commit == "" || p.Fingerprint == "" || p.Format == "" {
		t.Fatalf("incomplete chart presentation: %+v", p)
	}
	for _, u := range []string{p.SVG, p.Page} {
		parsed, err := url.Parse(u)
		if err != nil || !strings.HasPrefix(u, "/") || parsed.Host != "" || parsed.Scheme != "" || strings.HasPrefix(u, "//") {
			t.Fatalf("not a same-origin URL: %q", u)
		}
	}
	w := reader631GET(s, "GET", p.SVG, "")
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "image/svg+xml") {
		t.Fatalf("SVG: %d %v %s", w.Code, w.Header(), w.Body.String())
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") || w.Header().Get("ETag") == "" {
		t.Fatalf("SVG identity/security headers: %v", w.Header())
	}
	d := xml.NewDecoder(bytes.NewReader(w.Body.Bytes()))
	var metadata string
	var texts strings.Builder
	states, arrows, initials := 0, 0, 0
	for {
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("standalone XML: %v", err)
		}
		switch x := token.(type) {
		case xml.StartElement:
			switch strings.ToLower(x.Name.Local) {
			case "script", "foreignobject", "image", "iframe", "style", "link":
				t.Fatalf("SVG needs active/external content: %s", x.Name.Local)
			}
			attrs := map[string]string{}
			for _, a := range x.Attr {
				attrs[a.Name.Local] = a.Value
				if strings.HasPrefix(strings.ToLower(a.Name.Local), "on") || a.Name.Local == "href" {
					t.Fatalf("active SVG attribute: %+v", a)
				}
			}
			if attrs["data-state-id"] != "" {
				states++
			}
			if attrs["data-from"] != "" && attrs["data-to"] != "" {
				arrows++
			}
			if attrs["data-initial"] != "" {
				initials++
			}
			if x.Name.Local == "metadata" && attrs["id"] == "statechart-data" {
				if err := d.DecodeElement(&metadata, &x); err != nil {
					t.Fatal(err)
				}
			}
		case xml.CharData:
			texts.Write(x)
		case xml.Directive:
			t.Fatalf("SVG has a directive: %s", x)
		}
	}
	var got statechart.Result
	if err := json.Unmarshal([]byte(metadata), &got); err != nil {
		t.Fatalf("SVG did not retain checked result: %v", err)
	}
	if !reflect.DeepEqual(reader631JSON(t, &got), reader631JSON(t, want)) {
		t.Fatal("served SVG explanation differs from the exact receipt result")
	}
	if states != len(want.States) || arrows != len(want.Arrows) || initials != len(want.Initial) {
		t.Fatalf("SVG topology is incomplete: states %d arrows %d initials %d", states, arrows, initials)
	}
	for _, s := range want.States {
		if !strings.Contains(texts.String(), s.Label) {
			t.Errorf("label is not literal visible text: %q", s.Label)
		}
	}
	head := reader631GET(s, "HEAD", p.SVG, "")
	if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("ETag") != w.Header().Get("ETag") {
		t.Fatalf("HEAD: %d %v body=%d", head.Code, head.Header(), head.Body.Len())
	}
	conditional := reader631GET(s, "GET", p.SVG, w.Header().Get("ETag"))
	if conditional.Code != 304 || conditional.Body.Len() != 0 {
		t.Fatalf("conditional SVG: %d body=%d", conditional.Code, conditional.Body.Len())
	}
	page := reader631GET(s, "GET", p.Page, "")
	body := page.Body.String()
	if page.Code != 200 || !strings.HasPrefix(page.Header().Get("Content-Type"), "text/html") || !strings.Contains(body, "<img") || !strings.Contains(html.UnescapeString(body), p.SVG) || !strings.Contains(body, p.Commit) {
		t.Fatalf("script-free evidence page: %d %s", page.Code, body)
	}
	if strings.Contains(strings.ToLower(body), "<script") || strings.Contains(strings.ToLower(body), "<canvas") {
		t.Fatal("evidence page requires a drawing script")
	}
	for _, u := range []string{p.SVG, p.Page} {
		if w := reader631GET(s, "POST", u, ""); w.Code != 405 {
			t.Fatalf("POST %s: %d", u, w.Code)
		}
	}
	return w.Body.String()
}

func TestReader631ArchiveToStandaloneSVG(t *testing.T) {
	label := `Ready <script>alert("label")</script> & "quoted"`
	f := reader631FixtureFor(t, 3, label)
	r := reader631Repo(t, "owner/one", strings.Repeat("1", 40), 631, []reader631Fixture{f}, reader631Archive(t, []reader631Fixture{f}, nil))
	s := &Server{Repos: []*Repo{r}, Cache: t.TempDir()} // no TLC runner or renderer executable
	p := reader631Find(t, reader631Load(t, s, r), r.Name, f.Dir)
	svg := reader631Chart(t, s, p, f.Report.Reader.Chart)
	for _, word := range []string{"hidden", "unavailable", "Limit", "Safe", "poll-detail", "TLC"} {
		if !strings.Contains(strings.ToLower(svg), strings.ToLower(word)) {
			t.Errorf("missing reader report content %q", word)
		}
	}
	// Existing raw graphs remain a separate API with their own coverage data.
	key := "0123456789abcdef"
	gz, err := gzipped([]byte(`{"states":9000,"allEdges":70000,"edges":[0,1,0]}`))
	if err != nil {
		t.Fatal(err)
	}
	s.graphs = map[string][]byte{key: gz}
	if w := reader631GET(s, "GET", "/api/graph/"+key+".json", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "70000") {
		t.Fatal("raw graph coverage disappeared")
	}
	if got := reader631GET(s, "GET", p.SVG, "").Body.String(); got != svg {
		t.Fatal("raw graph cache changed reader evidence")
	}
	// Public reader endpoints must not open the authenticated action surface.
	if w := reader631GET(s, "GET", "/act/api/live.json?commands=1", ""); w.Code == 200 {
		t.Fatal("reader presentation opened private action data")
	}
}

func TestReader631DiagnosticsAndBoundaries(t *testing.T) {
	for _, n := range []int{500, 501} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			f := reader631FixtureFor(t, n, "Ready")
			r := reader631Repo(t, "owner/limits", strings.Repeat("2", 40), 631, []reader631Fixture{f}, reader631Archive(t, []reader631Fixture{f}, nil))
			s := &Server{Repos: []*Repo{r}, Cache: t.TempDir()}
			p := reader631Find(t, reader631Load(t, s, r), r.Name, f.Dir)
			if n == 500 {
				reader631Chart(t, s, p, f.Report.Reader.Chart)
				return
			}
			reader631Diagnostic(t, s, p, "too_large")
			body := reader631GET(s, "GET", p.Page, "").Body.String()
			for _, word := range []string{"501", "500", "Limit", "500"} {
				if !strings.Contains(body, word) {
					t.Errorf("size limitation omitted %q: %s", word, body)
				}
			}
		})
	}
	cases := []struct {
		name, status string
		alter        func(*reader631Fixture)
		edit         func(map[string][]byte)
	}{
		{"no view", "missing_view", func(f *reader631Fixture) {
			delete(f.Files, ".invariant/view.json")
			f.Report.Reader.Chart = nil
			f.Report.Reader.Unavailable = &verify.ReaderDiagnostic{Code: "missing_view", Message: "No reader chart is available. Supply .invariant/view.json and verify again."}
			f.bind(t)
		}, nil},
		{"legacy", "legacy_receipt", func(f *reader631Fixture) { f.Report.Reader = nil }, nil},
		{"failed generation", "reader_computation", func(f *reader631Fixture) {
			f.Report.Reader.Chart = nil
			f.Report.Reader.Unavailable = &verify.ReaderDiagnostic{Code: "reader_computation", Message: "Invariant replay failed <literal>"}
			f.bind(t)
		}, nil},
		{"missing SVG", "missing_artifacts", nil, func(m map[string][]byte) { delete(m, "reader/reader.svg") }},
		{"missing reader JSON", "missing_artifacts", nil, func(m map[string][]byte) { delete(m, "reader/reader.json") }},
		{"different view", "snapshot_mismatch", func(f *reader631Fixture) {
			f.Files[".invariant/view.json"] = bytes.ReplaceAll(f.Files[".invariant/view.json"], []byte("Ready"), []byte("New explanation"))
		}, nil},
		{"different model", "snapshot_mismatch", func(f *reader631Fixture) {
			f.Files[f.Manifest.Module] = append(f.Files[f.Manifest.Module], []byte("\n\\* changed snapshot\n")...)
		}, nil},
		{"different include", "snapshot_mismatch", func(f *reader631Fixture) {
			f.Files[".invariant/specs/Helper631.tla"] = []byte("---- MODULE Helper631 ----\nZero == 1\n====\n")
		}, nil},
		{"different lock", "snapshot_mismatch", func(f *reader631Fixture) {
			f.Lock.Statements[1].Says = "Different rule"
			f.Files[".invariant/ratified.lock"] = reader631JSON(t, f.Lock)
		}, nil},
		{"different bounds", "snapshot_mismatch", func(f *reader631Fixture) {
			f.Lock.Bounds = map[string]string{"Limit": "9"}
			f.Files[".invariant/ratified.lock"] = reader631JSON(t, f.Lock)
		}, nil},
		{"foreign reader JSON", "", nil, func(m map[string][]byte) { m["reader/reader.json"] = []byte(`{"version":1,"fingerprint":"other"}`) }},
		{"active archived SVG", "", nil, func(m map[string][]byte) {
			m["reader/reader.svg"] = []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
		}},
		{"wrong report", "", nil, func(m map[string][]byte) {
			var r verify.Report
			if err := json.Unmarshal(m["reader/receipt.json"], &r); err != nil {
				t.Fatal(err)
			}
			r.Reader.Chart.Report.Rules[1].Says = "Not the checked wording"
			m["reader/receipt.json"] = reader631JSON(t, r)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := reader631FixtureFor(t, 3, "Ready")
			if tc.alter != nil {
				tc.alter(&f)
			}
			r := reader631Repo(t, "owner/diagnostics", strings.Repeat("3", 40), 631, []reader631Fixture{f}, reader631Archive(t, []reader631Fixture{f}, tc.edit))
			s := &Server{Repos: []*Repo{r}, Cache: t.TempDir()}
			p := reader631Find(t, reader631Load(t, s, r), r.Name, f.Dir)
			reader631Diagnostic(t, s, p, tc.status)
			if tc.status == "missing_view" && !strings.Contains(strings.ToLower(p.Message), "no saved reader view") {
				t.Fatalf("absence must name the missing saved view: %+v", p)
			}
		})
	}
	// A plumbing proposal is still a plumbing card, not a modeled project with
	// a confusing missing-view warning. Exercise it beside an actual chart.
	f := reader631FixtureFor(t, 3, "Ready")
	r := reader631Repo(t, "owner/plumbing", strings.Repeat("4", 40), 631, []reader631Fixture{f}, reader631Archive(t, []reader631Fixture{f}, nil))
	r.src.issues = []github.Issue{{Number: 19, Title: "Repair a button", State: "open", CreatedAt: "2026-11-05T00:00:00Z", Labels: []github.Label{{Name: factory.LabelTrigger}}}}
	r.src.comments = map[int]cachedComments{19: {comments: []github.Comment{{User: github.User{Login: "invariant-code-factory[bot]", Type: "Bot"}, Body: marker(t, factory.Marker{Kind: factory.KindProposal, Proposal: &formalize.Proposal{Plan: &plumbing.Plan{Name: "Repair a button", Summary: "Fix its label."}}})}}}}
	s := &Server{Repos: []*Repo{r}, Cache: t.TempDir()}
	ps := reader631Load(t, s, r)
	reader631Chart(t, s, reader631Find(t, ps, r.Name, f.Dir), f.Report.Reader.Chart)
	var wire struct{ Issues []map[string]json.RawMessage }
	b, err := gunzip(s.state)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Issues) != 1 {
		t.Fatalf("plumbing card disappeared: %s", b)
	}
	if rr := wire.Issues[0]["reader"]; len(rr) > 0 && string(rr) != "null" {
		t.Fatalf("plumbing acquired reader evidence: %s", rr)
	}
}

func reader631Diagnostic(t *testing.T, s *Server, p reader631Presentation, status string) {
	t.Helper()
	if p.Status == "" || p.Status == "chart" || p.SVG != "" || p.Page == "" || p.Message == "" || (status != "" && p.Status != status) {
		t.Fatalf("expected visible diagnostic %q: %+v", status, p)
	}
	w := reader631GET(s, "GET", p.Page, "")
	if w.Code != 200 || !strings.Contains(html.UnescapeString(w.Body.String()), p.Message) {
		t.Fatalf("diagnostic absent from script-free page: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(strings.ToLower(w.Body.String()), "<script") || strings.Contains(w.Body.String(), "<img") {
		t.Fatal("diagnostic page contains an active or invented chart")
	}
}

func TestReader631IdentityRefreshAndRestart(t *testing.T) {
	base := reader631FixtureFor(t, 3, "Ready")
	sha := strings.Repeat("5", 40)
	seen := map[string]string{}
	cases := []struct {
		name, repo, commit string
		mutate             func(*reader631Fixture)
	}{
		{"base", "owner/one", sha, nil},
		{"repository", "owner/two", sha, nil},
		{"project", "owner/one", sha, func(f *reader631Fixture) { f.Dir = "other/reader" }},
		{"commit", "owner/one", strings.Repeat("6", 40), nil},
		{"model", "owner/one", sha, func(f *reader631Fixture) {
			f.Files[f.Manifest.Module] = append(f.Files[f.Manifest.Module], []byte("\n\\* new source\n")...)
		}},
		{"include", "owner/one", sha, func(f *reader631Fixture) {
			f.Files[".invariant/specs/Helper631.tla"] = append(f.Files[".invariant/specs/Helper631.tla"], '\n')
		}},
		{"view", "owner/one", sha, func(f *reader631Fixture) {
			f.Files[".invariant/view.json"] = append(f.Files[".invariant/view.json"], '\n')
		}},
		{"rule wording", "owner/one", sha, func(f *reader631Fixture) {
			f.Lock.Statements[1].Says = "Keep every recorded result."
			f.Report.Reader.Chart.Report.Rules[1].Says = f.Lock.Statements[1].Says
		}},
		{"bounds", "owner/one", sha, func(f *reader631Fixture) {
			f.Lock.Bounds = map[string]string{"Limit": "02"}
			f.Report.Reader.Chart.Report.Source.Config.Constants = f.Lock.Bounds
		}},
		{"tool", "owner/one", sha, func(f *reader631Fixture) {
			f.Report.Toolchain.JavaImage = "java@sha256:" + strings.Repeat("c", 64)
			f.Report.Reader.Chart.Report.Source.Tool.Image = f.Report.Toolchain.JavaImage
		}},
		{"report contents", "owner/one", sha, func(f *reader631Fixture) {
			f.Report.Reader.Chart.Report.CountMeanings["tlc_steps"] += " Each occurrence is retained."
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var f reader631Fixture
			if err := json.Unmarshal(reader631JSON(t, base), &f); err != nil {
				t.Fatal(err)
			}
			if tc.mutate != nil {
				tc.mutate(&f)
			}
			f.bind(t)
			r := reader631Repo(t, tc.repo, tc.commit, 631, []reader631Fixture{f}, reader631Archive(t, []reader631Fixture{f}, nil))
			s := &Server{Repos: []*Repo{r}, Cache: t.TempDir()}
			p := reader631Find(t, reader631Load(t, s, r), r.Name, f.Dir)
			reader631Chart(t, s, p, f.Report.Reader.Chart)
			if old := seen[p.SVG]; old != "" {
				t.Fatalf("%s reused %s's SVG identity %s", tc.name, old, p.SVG)
			}
			seen[p.SVG] = tc.name
		})
	}
	// Refresh in place, then restart without GitHub or TLC. Both repositories
	// deliberately use the same project path and model, but different words.
	a := reader631FixtureFor(t, 3, "First repository")
	b := reader631FixtureFor(t, 3, "Second repository")
	ra := reader631Repo(t, "owner/one", sha, 631, []reader631Fixture{a}, reader631Archive(t, []reader631Fixture{a}, nil))
	rb := reader631Repo(t, "owner/two", sha, 631, []reader631Fixture{b}, reader631Archive(t, []reader631Fixture{b}, nil))
	s := &Server{Repos: []*Repo{ra, rb}, Cache: t.TempDir()}
	reader631Load(t, s, ra)
	ps := reader631Load(t, s, rb)
	pa := reader631Find(t, ps, ra.Name, a.Dir)
	pb := reader631Find(t, ps, rb.Name, b.Dir)
	reader631Chart(t, s, pa, a.Report.Reader.Chart)
	reader631Chart(t, s, pb, b.Report.Reader.Chart)
	next := reader631FixtureFor(t, 3, "Refreshed explanation")
	rnext := reader631Repo(t, ra.Name, strings.Repeat("7", 40), 632, []reader631Fixture{next}, reader631Archive(t, []reader631Fixture{next}, nil))
	ra.GitHub, ra.src.runs = rnext.GitHub, rnext.src.runs
	ra.src.gates[632] = gateRan
	ps = reader631Load(t, s, ra)
	pn := reader631Find(t, ps, ra.Name, next.Dir)
	if pn.SVG == pa.SVG || pn.Commit == pa.Commit {
		t.Fatal("refresh kept the old receipt's explanation")
	}
	reader631Chart(t, s, pn, next.Report.Reader.Chart)
	cold := &Server{Cache: s.Cache, Repos: []*Repo{{Name: ra.Name}, {Name: rb.Name}}}
	cold.loadSnapshot()
	reader631Chart(t, cold, pn, next.Report.Reader.Chart)
	reader631Chart(t, cold, pb, b.Report.Reader.Chart)
	// Removing a repository from the configured set must not expose its cached
	// reader URL merely because the primary repository still matches.
	only := &Server{Cache: s.Cache, Repos: []*Repo{{Name: ra.Name}}}
	only.loadSnapshot()
	if w := reader631GET(only, "GET", pb.SVG, ""); w.Code == 200 {
		t.Fatal("cache exposed a repository this server does not serve")
	}
	// A damaged persisted entry cannot be sent as SVG or leave a chart card
	// silently pointing at unavailable evidence. Rebuilding from independently
	// retained structured evidence is also an acceptable recovery.
	damagedCache := t.TempDir()
	if err := os.CopyFS(damagedCache, os.DirFS(s.Cache)); err != nil {
		t.Fatal(err)
	}
	if err := filepath.WalkDir(damagedCache, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() != filepath.Base(s.snapshotFile()) {
			return os.WriteFile(p, []byte("corrupt reader cache entry"), 0600)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	damaged := &Server{Cache: damagedCache, Repos: []*Repo{{Name: ra.Name}, {Name: rb.Name}}}
	damaged.loadSnapshot()
	if w := reader631GET(damaged, "GET", pn.SVG, ""); w.Code == 200 {
		reader631Chart(t, damaged, pn, next.Report.Reader.Chart)
	} else if len(damaged.state) > 0 {
		plain, err := gunzip(damaged.state)
		if err != nil {
			t.Fatal(err)
		}
		var saved struct{ Projects []reader631Project }
		if err := json.Unmarshal(plain, &saved); err != nil {
			t.Fatal(err)
		}
		for _, project := range saved.Projects {
			if rr := project.Reader; rr != nil && rr.SVG == pn.SVG && rr.Status == "chart" {
				t.Fatal("restart advertises a chart whose cached evidence is corrupt")
			}
		}
	}
	// Tamper with saved presentation format, leaving all evidence untouched.
	// A restart must discard or rebuild that presentation, never advertise the
	// old format as current. Legacy snapshots without readers remain readable.
	js, err := gunzip(s.state)
	if err != nil {
		t.Fatal(err)
	}
	var snap map[string]any
	if err := json.Unmarshal(js, &snap); err != nil {
		t.Fatal(err)
	}
	for _, raw := range snap["projects"].([]any) {
		p := raw.(map[string]any)
		if rr, ok := p["reader"].(map[string]any); ok {
			rr["format"] = "obsolete-reader-format"
		}
	}
	gz, err := gzipped(reader631JSON(t, snap))
	if err != nil {
		t.Fatal(err)
	}
	s.saveSnapshot(gz)
	changed := &Server{Cache: s.Cache, Repos: []*Repo{{Name: ra.Name}, {Name: rb.Name}}}
	changed.loadSnapshot()
	if len(changed.state) > 0 {
		plain, err := gunzip(changed.state)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(plain, []byte("obsolete-reader-format")) {
			t.Fatal("restart advertised an obsolete reader format")
		}
	}
}
