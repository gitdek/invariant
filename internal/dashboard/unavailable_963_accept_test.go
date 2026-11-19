package dashboard

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/verify"
)

const reader963Hostile = "Source λ <script>literal</script> & quoted"

// These are retained, independently validated records, using #631's pinned
// model, lock, completed model check and tool provenance. The historical case
// stays historical; new dimensions do not reinterpret or reseal its evidence.
func reader963Limited(t *testing.T, kind string) reader631Fixture {
	t.Helper()
	f := reader631FixtureFor(t, 501, reader963Hostile)
	if kind == "historical" {
		return f
	}
	n, projected, checked := 24760, 3100, true
	l := &statechart.Limitation{Code: "too_large", Kind: kind, Actual: projected, Limit: 500,
		ConcreteStates: n, ProjectedStates: &projected, ViewChecked: &checked, Source: f.Report.Reader.Limitation.Source}
	switch kind {
	case "projected_states":
		f.Files[".invariant/view.json"] = []byte(reader944PartitionView(projected))
	case "concrete_states":
		n, checked = 100001, false
		l.Actual, l.Limit, l.ConcreteStates, l.ProjectedStates = n, statechart.MaxConcreteStates, n, nil
	case "input_bytes", "replay_bytes", "output_bytes", "steps", "invariant_cells":
		l.Kind, l.Dimension, l.Side, l.Unit = "evidence_volume", kind, "projection", "bytes"
		checked = kind == "output_bytes"
		l.ProjectedStates = nil
		switch kind {
		case "input_bytes":
			l.Limit, l.Representation = statechart.MaxInputBytes, "compact_json"
		case "replay_bytes":
			l.Limit, l.Representation = statechart.MaxReplayBytes, "replay_modules_and_configurations"
		case "output_bytes":
			projected = 3
			l.ProjectedStates = &projected
			l.Limit, l.Representation, l.Side = statechart.MaxEvidenceBytes, "svg", "proposal"
		case "steps":
			l.Limit, l.Representation, l.Unit = statechart.MaxSteps, "graph.steps", "occurrences"
		case "invariant_cells":
			l.Limit, l.Representation, l.Unit = statechart.MaxInvariantCells, "invariants", "cells"
		}
		l.Actual = l.Limit + 17
	default:
		t.Fatal("unknown fixture dimension", kind)
	}
	f.Lock.Bounds["Limit"] = strconv.Itoa(n - 1)
	l.Source.Config.Constants = f.Lock.Bounds
	f.Report.Design.DistinctStates, f.Report.Design.StatesGenerated, f.Report.Design.Depth = int64(n), int64(2*n+1), n
	f.Report.Reader.Limitation = l
	f.bind(t)
	if err := l.Validate(); err != nil {
		t.Fatal("invalid retained limitation fixture", err)
	}
	return f
}

func reader963Proposal(t *testing.T, f reader631Fixture) *formalize.Proposal {
	t.Helper()
	s := receipt723Snapshot(f, "Candidate "+reader963Hostile)
	p := &formalize.Proposal{Draft: formalize.Draft{Name: "Candidate " + reader963Hostile, Module: s.Module, Bounds: s.Lock.Bounds, Statements: s.Lock.Statements},
		Hash: project.ProposalHash(s.Lock.Bounds, s.Lock.Statements), ModuleText: s.Model, Includes: s.Includes, View: s.View,
		Reader: &formalize.Reader{Version: 1, Chart: s.Chart, Limitation: s.Limitation}}
	b := reader631JSON(t, struct {
		Version    int                    `json:"version"`
		Module     string                 `json:"module"`
		Model      string                 `json:"model"`
		Includes   map[string]string      `json:"includes,omitempty"`
		View       string                 `json:"view"`
		Bounds     map[string]string      `json:"bounds"`
		Statements []project.Statement    `json:"statements"`
		Chart      *statechart.Result     `json:"chart,omitempty"`
		Limitation *statechart.Limitation `json:"limitation,omitempty"`
	}{1, p.Module, p.ModuleText, p.Includes, p.View, p.Bounds, p.Statements, p.Reader.Chart, p.Reader.Limitation})
	p.Reader.Fingerprint = reader631Hash(b)
	if err := p.ValidateReader(); err != nil {
		t.Fatal("invalid proposal fixture", err)
	}
	return p
}

func reader963Comparison(t *testing.T, a, b reader631Fixture) *statechart.Comparison {
	t.Helper()
	candidateName := "Proposal " + project.ProposalHash(b.Lock.Bounds, b.Lock.Statements) + " with view " + reader631Hash(b.Files[".invariant/view.json"])[:19]
	c, err := statechart.Compare(receipt723Snapshot(a, "Baseline "+reader963Hostile), receipt723Snapshot(b, candidateName))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Validate(); err != nil {
		t.Fatal("invalid comparison fixture", err)
	}
	return c
}

// A small HTML tree lets assertions distinguish folded text from visible
// explanations and one alternative from its siblings, without a browser or
// a new dependency. The production pages use explicit end tags and HTML void
// elements; decoding is only for inspection, never an alternate renderer.
type reader963Node struct {
	tag, text string
	attrs     map[string]string
	parent    *reader963Node
	children  []*reader963Node
}

func reader963HTML(t *testing.T, data []byte) *reader963Node {
	t.Helper()
	root := &reader963Node{tag: "document"}
	stack := []*reader963Node{root}
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict, d.Entity = false, xml.HTMLEntity
	void := map[string]bool{"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true, "img": true, "input": true, "link": true, "meta": true, "param": true, "source": true, "track": true, "wbr": true}
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal("invalid evidence HTML", err)
		}
		switch v := tok.(type) {
		case xml.StartElement:
			n := &reader963Node{tag: strings.ToLower(v.Name.Local), attrs: map[string]string{}, parent: stack[len(stack)-1]}
			for _, a := range v.Attr {
				n.attrs[strings.ToLower(a.Name.Local)] = a.Value
			}
			n.parent.children = append(n.parent.children, n)
			if !void[n.tag] {
				stack = append(stack, n)
			}
		case xml.EndElement:
			if void[strings.ToLower(v.Name.Local)] {
				continue
			}
			for i := len(stack) - 1; i > 0; i-- {
				if stack[i].tag == strings.ToLower(v.Name.Local) {
					stack = stack[:i]
					break
				}
			}
		case xml.CharData:
			p := stack[len(stack)-1]
			p.children = append(p.children, &reader963Node{text: string(v), parent: p})
		}
	}
	return root
}

func (n *reader963Node) all(tag string) []*reader963Node {
	var out []*reader963Node
	if n.tag == tag {
		out = append(out, n)
	}
	for _, c := range n.children {
		out = append(out, c.all(tag)...)
	}
	return out
}

func (n *reader963Node) words(folded bool) string {
	if n.tag == "head" || n.tag == "style" || n.tag == "script" || !folded && n.tag == "details" {
		return ""
	}
	var b strings.Builder
	b.WriteString(n.text)
	for _, c := range n.children {
		b.WriteString(c.words(folded))
	}
	return b.String()
}

func reader963Space(s string) string { return strings.Join(strings.Fields(s), " ") }

func reader963NoImages(t *testing.T, n *reader963Node) {
	t.Helper()
	for _, tag := range []string{"img", "picture", "source"} {
		if len(n.all(tag)) != 0 {
			t.Errorf("unavailable presentation contains <%s>", tag)
		}
	}
	for _, svg := range n.all("svg") {
		if svg.attrs["aria-hidden"] != "true" {
			t.Error("unavailable presentation contains an inline chart SVG")
		}
	}
	for _, tag := range []string{"a", "img", "source"} {
		for _, el := range n.all(tag) {
			for _, key := range []string{"href", "src", "srcset"} {
				if strings.Contains(el.attrs[key], ".svg") {
					t.Errorf("unavailable presentation exposes image URL %q", el.attrs[key])
				}
			}
		}
	}
}

func reader963Safe(t *testing.T, n *reader963Node) {
	t.Helper()
	for _, tag := range []string{"script", "iframe", "object", "embed", "foreignobject", "form"} {
		if len(n.all(tag)) != 0 {
			t.Errorf("evidence page contains active <%s>", tag)
		}
	}
	var visit func(*reader963Node)
	visit = func(n *reader963Node) {
		for key, value := range n.attrs {
			if strings.HasPrefix(key, "on") || strings.HasPrefix(strings.ToLower(value), "javascript:") {
				t.Error("evidence became active HTML", key, value)
			}
		}
		for _, c := range n.children {
			visit(c)
		}
	}
	visit(n)
}

func reader963HasRecord(t *testing.T, n *reader963Node, want any) {
	t.Helper()
	var expected any
	if err := json.Unmarshal(reader631JSON(t, want), &expected); err != nil {
		t.Fatal(err)
	}
	for _, pre := range n.all("pre") {
		var got any
		if json.Unmarshal([]byte(pre.words(true)), &got) == nil && reflect.DeepEqual(got, expected) {
			return
		}
	}
	t.Error("folded evidence does not preserve the complete retained record")
}

// The reason is visible once, and the next element is native closed details.
// A hash, a repeated too_large token, or a provenance paragraph is not a
// substitute for the short explanation a reader needs.
func reader963Fold(t *testing.T, n *reader963Node, message string) *reader963Node {
	t.Helper()
	if message == "" || len([]rune(message)) > 420 || strings.Contains(message, "sha256:") || strings.Contains(message, "too_large") || strings.Contains(message, "\n") {
		t.Errorf("reason is not a concise human-readable explanation: %q", message)
	}
	want := reader963Space(message)
	if strings.Count(reader963Space(n.words(false)), want) != 1 {
		t.Error("unavailable reason must be visible exactly once outside details")
	}
	for _, p := range n.all("p") {
		if reader963Space(p.words(true)) != want {
			continue
		}
		seen := false
		for _, sibling := range p.parent.children {
			if sibling == p {
				seen = true
				continue
			}
			if !seen || sibling.tag == "" {
				continue
			}
			if sibling.tag != "details" {
				break
			}
			if _, open := sibling.attrs["open"]; open {
				t.Error("technical evidence starts open")
			}
			if len(sibling.children) == 0 || len(sibling.all("summary")) != 1 || reader963Space(sibling.all("summary")[0].words(true)) == "" {
				t.Error("details needs a keyboard-accessible named summary")
			}
			return sibling
		}
	}
	t.Error("closed evidence details must immediately follow the unavailable explanation")
	return &reader963Node{}
}

func reader963Words(t *testing.T, text string, words ...string) {
	t.Helper()
	for _, word := range words {
		if !strings.Contains(strings.ToLower(reader963Space(text)), strings.ToLower(reader963Space(word))) {
			t.Errorf("text omits %q", word)
		}
	}
}

func reader963GET(t *testing.T, s *Server, page string) *reader963Node {
	t.Helper()
	w := reader631GET(s, "GET", page, "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s = %d", page, w.Code)
	}
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		t.Fatal("evidence is not HTML")
	}
	for _, policy := range []string{"default-src 'none'", "sandbox", "frame-ancestors 'none'", "form-action 'none'"} {
		if !strings.Contains(w.Header().Get("Content-Security-Policy"), policy) {
			t.Error("lost evidence security policy", policy)
		}
	}
	head := reader631GET(s, "HEAD", page, "")
	if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("ETag") != w.Header().Get("ETag") || head.Header().Get("Content-Length") != w.Header().Get("Content-Length") {
		t.Error("HEAD changed representation or returned a body")
	}
	for _, method := range []string{"GET", "HEAD"} {
		conditional := reader631GET(s, method, page, `"unrelated", W/`+w.Header().Get("ETag"))
		if w.Header().Get("ETag") == "" || conditional.Code != 304 || conditional.Body.Len() != 0 {
			t.Error("conditional representation read failed")
		}
	}
	n := reader963HTML(t, w.Body.Bytes())
	reader963Safe(t, n)
	return n
}

func reader963Unavailable(t *testing.T, s *Server, p *ReaderPresentation) (*reader963Node, *reader963Node) {
	t.Helper()
	if p == nil {
		t.Fatal("missing unavailable presentation")
	}
	if p.Status == "chart" || p.SVG != "" || s.postersFor(p) != nil {
		t.Errorf("unavailable evidence advertises chart/image/posters: %+v", p)
	}
	n := reader963GET(t, s, p.Page)
	reader963NoImages(t, n)
	base := "/reader/" + strings.TrimPrefix(p.Fingerprint, "sha256:")
	for _, suffix := range []string{".svg", ".poster.svg", ".still.svg"} {
		for _, method := range []string{"GET", "HEAD"} {
			w := reader631GET(s, method, base+suffix, "")
			if w.Code != 404 || method == "HEAD" && w.Body.Len() != 0 {
				t.Errorf("nonexistent image %s %s = %d", method, suffix, w.Code)
			}
		}
	}
	return n, reader963Fold(t, n, p.Message)
}

func reader963Source(t *testing.T, fold *reader963Node, f reader631Fixture) {
	t.Helper()
	l := f.Report.Reader.Limitation
	source := statechart.Source{}
	if l != nil {
		source = l.Source
	} else {
		source = f.Report.Reader.Chart.Report.Source
	}
	reader963Words(t, fold.words(true), source.Module, source.Tool.Image, source.Tool.JarSHA256, source.TLC, "Limit", f.Lock.Bounds["Limit"], source.Config.Specification)
}

func TestReader963ProjectedProposal(t *testing.T) {
	f := reader963Limited(t, "projected_states")
	p := reader963Proposal(t, f)
	before := reader631JSON(t, p)
	s := &Server{Repos: []*Repo{{Name: "owner/proposal"}}, Cache: t.TempDir()}
	r := s.keepComparison(comparisonEvidence{Format: comparisonFormat, Repo: "owner/proposal", Issue: 963, Proposal: p})
	n, fold := reader963Unavailable(t, s, r)
	reader963Words(t, r.Message, "24,760", "3,100", "500", "view")
	reader963Source(t, fold, f)
	reader963HasRecord(t, fold, p)
	reader963Words(t, fold.words(true), p.Hash, p.Reader.Fingerprint)
	reader963Words(t, n.words(false), f.Lock.Statements[1].Says)
	if !bytes.Equal(before, reader631JSON(t, p)) {
		t.Fatal("presentation mutated retained evidence")
	}
}

func TestReader963LimitReasons(t *testing.T) {
	for _, kind := range []string{"concrete_states", "input_bytes", "replay_bytes", "output_bytes", "steps", "invariant_cells", "historical"} {
		f := reader963Limited(t, kind)
		p := reader963Proposal(t, f)
		s := &Server{Repos: []*Repo{{Name: "owner/limits"}}}
		r := s.keepComparison(comparisonEvidence{Format: comparisonFormat, Repo: "owner/limits", Issue: 963, Proposal: p})
		_, fold := reader963Unavailable(t, s, r)
		l := p.Reader.Limitation
		switch kind {
		case "historical":
			reader963Words(t, r.Message, "501", "500", "earlier", "fresh", "projected")
			if strings.Contains(r.Message, "3,100") || strings.Contains(r.Message, "24,760") {
				t.Error("historical record invented projected evidence")
			}
		case "concrete_states":
			reader963Words(t, r.Message, "100,001", "100,000", "projection", "view", "not evaluated")
		default:
			reader963Words(t, r.Message, strings.TrimSuffix(counted(int64(l.Actual), "unit"), " units"), strings.TrimSuffix(counted(int64(l.Limit), "unit"), " units"), l.Unit, "view")
			if *l.ViewChecked {
				reader963Words(t, r.Message, "checked")
			} else {
				reader963Words(t, r.Message, "not evaluated")
			}
		}
		reader963Source(t, fold, f)
		reader963HasRecord(t, fold, p)
	}
}

func reader963BothSources(t *testing.T, fold *reader963Node, p *formalize.Proposal) {
	t.Helper()
	c := p.Reader.Comparison
	reader963HasRecord(t, fold, p)
	reader963Words(t, fold.words(true), c.Baseline.Name, c.Candidate.Name, c.Fingerprint)
	for _, id := range []statechart.ComparisonIdentity{c.BaselineIdentity, c.CandidateIdentity} {
		reader963Words(t, fold.words(true), id.ProposalHash, id.ModelSHA256, id.ViewSHA256, id.LockSHA256)
		for name, hash := range id.Includes {
			reader963Words(t, fold.words(true), name, hash)
		}
	}
	for _, snap := range []statechart.ComparisonSnapshot{c.Baseline, c.Candidate} {
		for name, value := range snap.Lock.Bounds {
			reader963Words(t, fold.words(true), name, value)
		}
	}
	for _, q := range c.Qualifications() {
		reader963Words(t, fold.words(true), q)
	}
}

func TestReader963LimitedComparisonSides(t *testing.T) {
	for _, kind := range []string{"projected_states", "concrete_states", "output_bytes", "input_bytes", "historical"} {
		for _, side := range []string{"baseline", "candidate", "both"} {
			a, b := reader631FixtureFor(t, 2, "Before"), reader631FixtureFor(t, 3, "After")
			if side != "candidate" {
				a = reader963Limited(t, kind)
			}
			if side != "baseline" {
				b = reader963Limited(t, kind)
			}
			c := reader963Comparison(t, a, b)
			p := comparison717Proposal(t, c)
			before := reader631JSON(t, p)
			s := &Server{Repos: []*Repo{{Name: "owner/comparisons"}}, Cache: t.TempDir()}
			r := s.keepComparison(comparisonEvidence{Format: comparisonFormat, Repo: "owner/comparisons", Issue: 963, Proposal: p})
			_, fold := reader963Unavailable(t, s, r)
			reader963Words(t, r.Message, "comparison", "unavailable")
			if side != "candidate" {
				reader963Words(t, r.Message, "baseline")
			}
			if side != "baseline" {
				reader963Words(t, r.Message, "candidate")
			}
			reader963BothSources(t, fold, p)
			if !bytes.Equal(before, reader631JSON(t, p)) {
				t.Fatal("limited comparison was rewritten")
			}
		}
	}
}

func TestReader963CombinedComparisonVolume(t *testing.T) {
	// Each side fits; repeated source bytes make only the combined encoding
	// overflow. Compare measures the actual complete representation itself.
	a, b := reader631FixtureFor(t, 2, "Before"), reader631FixtureFor(t, 3, "After")
	candidateName := "Proposal " + project.ProposalHash(b.Lock.Bounds, b.Lock.Statements) + " with view " + reader631Hash(b.Files[".invariant/view.json"])[:19]
	as, bs := receipt723Snapshot(a, "Baseline "+reader963Hostile), receipt723Snapshot(b, candidateName)
	as.Model += "\n\\* " + strings.Repeat("x", 31<<20) + "\n"
	bs.Model += "\n\\* " + strings.Repeat("y", 31<<20) + "\n"
	c, err := statechart.Compare(as, bs)
	if err != nil || c == nil || c.Volume() == nil || c.Baseline.Chart == nil || c.Candidate.Chart == nil {
		t.Fatalf("combined-volume fixture: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	p := comparison717Proposal(t, c)
	s := &Server{Repos: []*Repo{{Name: "owner/volume"}}}
	r := s.keepComparison(comparisonEvidence{Format: comparisonFormat, Repo: "owner/volume", Issue: 963, Proposal: p})
	_, fold := reader963Unavailable(t, s, r)
	l := c.Volume()
	reader963Words(t, r.Message, "comparison", "bytes", "view", "checked", strings.TrimSuffix(counted(int64(l.Actual), "unit"), " units"), "62,914,560")
	if !strings.Contains(strings.ToLower(r.Message), "json") {
		t.Error("combined overflow must identify the measured JSON representation")
	}
	if strings.Contains(strings.ToLower(r.Message), "candidate exceeds") || strings.Contains(strings.ToLower(r.Message), "baseline exceeds") {
		t.Error("combined overflow invented an oversized side")
	}
	reader963BothSources(t, fold, p)
}

func reader963Questions(t *testing.T) *formalize.Result {
	t.Helper()
	tc, _ := reader603Tools(t)
	d := preview639Draft(t, 1, nil)
	m := preview639Input(d, 0, 0)["model"].(map[string]any)
	m["bounds"], m["view"] = map[string]string{"Limit": "501"}, reader944PartitionView(502)
	preview639Option(d, 0, 0)["says"] = "Limited option " + reader963Hostile
	ws := t.TempDir()
	preview639Write(t, ws, d)
	ctx := (formalize.Request{}).CheckContext()
	p, _, err := formalize.CheckWithContext(t.Context(), ws, tc, ctx)
	if err != nil || p == nil {
		t.Fatal("checked alternative fixture", err)
	}
	return &formalize.Result{Proposal: p, CheckContext: &ctx}
}

func reader963Download(t *testing.T, s *Server, d *saved897Presentation, payload []byte) *reader963Node {
	t.Helper()
	for _, method := range []string{"GET", "HEAD"} {
		w := reader631GET(s, method, d.Locator, "")
		if w.Code != 302 || w.Header().Get("Location") != d.Reader.Page || !strings.Contains(w.Header().Get("Cache-Control"), "no-cache") || method == "HEAD" && w.Body.Len() != 0 {
			t.Error("durable locator stopped resolving current presentation")
		}
	}
	n := reader963GET(t, s, d.Reader.Page)
	x := reader631GET(s, "GET", d.Download, "")
	if x.Code != 200 || !bytes.Equal(x.Body.Bytes(), payload) || !strings.Contains(x.Header().Get("Content-Disposition"), "attachment") {
		t.Fatal("saved result download changed")
	}
	head := reader631GET(s, "HEAD", d.Download, "")
	if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("ETag") != x.Header().Get("ETag") {
		t.Error("download HEAD changed")
	}
	htmlTag := reader631GET(s, "GET", d.Reader.Page, "").Header().Get("ETag")
	if x.Header().Get("ETag") == htmlTag || reader631GET(s, "GET", d.Download, htmlTag).Code != 200 || reader631GET(s, "GET", d.Download, x.Header().Get("ETag")).Code != 304 {
		t.Error("ETags do not bind representation bytes")
	}
	return n
}

func TestReader963PreviewAndSavedQuestions(t *testing.T) {
	result := reader963Questions(t)
	f := saved897FixtureFor(t, result)
	f.s.prepare()
	f.refresh(t)
	for _, s := range []*Server{f.s, f.restart(t, saved897Author)} {
		w := saved897Healthy(t, s, f.ref, result)
		if len(w.Forks) != 1 || len(w.Forks[0].Options) != 2 {
			t.Fatal("options missing")
		}
		limited, healthy := w.Forks[0].Options[0], w.Forks[0].Options[1]
		if err := result.Proposal.Forks[0].ValidatePreview(result.Proposal.Forks[0].Options[0]); err != nil {
			t.Fatal(err)
		}
		_, fold := reader963Unavailable(t, s, limited.Reader)
		reader963Words(t, limited.Reader.Message, "502", "500", "view")
		reader963HasRecord(t, fold, limited.Preview)
		if healthy.Reader.Status != "chart" || healthy.Posters == nil {
			t.Error("limited option erased healthy sibling")
		}
		if reader631GET(s, "GET", healthy.Reader.SVG, "").Code != 200 {
			t.Error("healthy sibling chart missing")
		}
		n := reader963Download(t, s, w.SavedDraft, f.payload)
		fold = reader963Fold(t, n, limited.Reader.Message)
		reader963HasRecord(t, fold, limited.Preview)
		// Scope the no-image check to the unavailable option's presentation;
		// its healthy sibling is expected to remain drawn on this same page.
		for _, img := range n.all("img") {
			if strings.Contains(img.attrs["src"], strings.TrimPrefix(limited.Reader.Fingerprint, "sha256:")) {
				t.Error("saved question embeds unavailable option")
			}
		}
		reader963Words(t, n.words(false), w.Forks[0].Question, limited.Says, healthy.Says)
	}
}

func TestReader963SavedProposalLifecycle(t *testing.T) {
	baseline := saved897Proposal(t, false, 501, nil)
	result := saved897Proposal(t, false, 2, baseline.Proposal)
	if result.Proposal.Reader.Comparison == nil || result.Proposal.Reader.Comparison.Baseline.Limitation == nil || result.Proposal.Reader.Chart == nil {
		t.Fatal("fixture must retain limited baseline and drawable candidate")
	}
	before := reader631JSON(t, result)
	f := saved897FixtureFor(t, result)
	f.s.prepare()
	f.refresh(t)
	f.refresh(t)
	for _, s := range []*Server{f.s, f.restart(t, saved897Author)} {
		w := saved897Healthy(t, s, f.ref, result)
		n, fold := reader963Unavailable(t, s, w.Reader)
		reader963BothSources(t, fold, result.Proposal)
		reader963Words(t, n.words(false), result.Proposal.Statements[1].Says)
		saved := reader963Download(t, s, w.SavedDraft, f.payload)
		reader963NoImages(t, saved)
		fold = reader963Fold(t, saved, w.SavedDraft.Reader.Message)
		reader963HasRecord(t, fold, result)
		reader963Words(t, fold.words(true), f.ref.Commit, f.ref.SHA256, result.Proposal.Hash)
		if w.Hash != strings.TrimPrefix(result.Proposal.Hash, "sha256:")[:12] {
			t.Error("ratification hash changed")
		}
		snap, _ := saved897State(t, s, 897)
		if err := (act{Repo: f.r.Name, Issue: 897, Body: "/invariant " + factory.Ratify + " " + w.Hash}).check(snap.Issues, nil); err != nil {
			t.Error("valid limited proposal lost ratification", err)
		}
		// Both entry points expose the same authoritative card; browser
		// regressions exercise their actual JS rendering and keyboard controls.
		team := newAccessTeam(t)
		s.Access = team.access()
		for _, path := range []string{"/", "/act"} {
			req := httptest.NewRequest("GET", path, nil)
			if path == "/act" {
				req.Header.Set("Cf-Access-Jwt-Assertion", team.token(t, team.key, "RS256", team.claims(nil)))
			}
			response := httptest.NewRecorder()
			s.Handler().ServeHTTP(response, req)
			if response.Code != 200 {
				t.Errorf("dashboard entry %s = %d", path, response.Code)
			}
		}
	}
	if !bytes.Equal(before, reader631JSON(t, result)) {
		t.Fatal("saved result was mutated")
	}
}

func TestReader963ReceiptOutcomes(t *testing.T) {
	// Run the failing unavailable/unavailable case as well as every drawable
	// combination. The retained candidate and final lock share bounds, but
	// the final source and view remain independently checked identities.
	for _, comparisonDraws := range []bool{false, true} {
		for _, finalOutcome := range []string{"too_large", "missing_view", "chart"} {
			finalDraws := finalOutcome == "chart"
			a := reader631FixtureFor(t, 2, "Before")
			if !comparisonDraws {
				a = reader963Limited(t, "historical")
			}
			b := reader631FixtureFor(t, 3, "Candidate")
			f := reader631FixtureFor(t, 3, "Independent final")
			if finalOutcome == "too_large" {
				b = dashboard944Fixture(t, 501, "Candidate")
				f = reader631FixtureFor(t, 501, "Independent historical final")
			}
			c := reader963Comparison(t, a, b)
			if finalOutcome == "missing_view" {
				f.Report.Reader.Chart = nil
				f.Report.Reader.Unavailable = &verify.ReaderDiagnostic{Code: "missing_view", Message: "No saved reader view."}
				delete(f.Files, ".invariant/view.json")
			}
			f.Files[f.Manifest.Module] = append(f.Files[f.Manifest.Module], []byte("\\* independent final source\n")...)
			f.Report.Reader.Comparison, f.Files[".invariant/comparison.json"] = c, reader631JSON(t, c)
			f.bind(t)
			before := reader631JSON(t, f.Report)
			s, _, p := receipt723Load(t, f, nil)
			for _, server := range []*Server{s, receipt723Restart(t, s, p, f)} {
				n := reader963GET(t, server, p.Page)
				project := readerRecoveryProject(t, server.assemble(time.Now()), receipt723RepoName, f.Dir)
				if finalDraws {
					if p.Status != "chart" || project.Poster == nil {
						t.Fatal("independent final chart disappeared")
					}
					for _, item := range []struct {
						url      string
						animated bool
					}{{project.Poster.Animated, true}, {project.Poster.Still, false}} {
						want, err := f.Report.Reader.Chart.Poster(statechart.PosterOptions{Title: f.Manifest.Name, Animated: item.animated})
						if err != nil || string(receipt723HTTP(t, server, item.url)) != want {
							t.Error("final poster bytes changed")
						}
					}
				} else {
					if p.Status != finalOutcome || project.Poster != nil {
						t.Errorf("final unavailable outcome relabeled by comparison: %+v", p)
					}
					if finalOutcome == "missing_view" {
						reader963Words(t, n.words(false), "saved reader view")
					} else {
						reader963Words(t, n.words(false), "501", "earlier", "fresh")
					}
				}
				if !comparisonDraws {
					var reason string
					for _, para := range n.all("p") {
						text := para.words(true)
						if strings.Contains(strings.ToLower(text), "comparison") && strings.Contains(strings.ToLower(text), "unavailable") {
							reason = text
							break
						}
					}
					fold := reader963Fold(t, n, reason)
					reader963Words(t, fold.words(true), c.Baseline.Name, c.Candidate.Name, c.BaselineIdentity.ModelSHA256, c.CandidateIdentity.ViewSHA256, c.Fingerprint)
					// A drawable final SVG must not contain the unavailable
					// comparison's old text panels or pretend they are a graph.
					if finalDraws {
						svg := receipt723HTTP(t, server, p.SVG)
						receipt723Topology(t, svg, f.Report.Reader.Chart)
						if bytes.Contains(svg, []byte("Comparison unavailable")) || bytes.Contains(svg, []byte("data-comparison-side")) {
							t.Error("unavailable comparison still presented in an image")
						}
					}
				} else {
					reader963Words(t, n.words(true), "retained proposal comparison")
					found := false
					for _, link := range n.all("a") {
						u, err := url.Parse(link.attrs["href"])
						if err == nil && strings.HasSuffix(u.Path, ".svg") {
							w := reader631GET(server, "GET", link.attrs["href"], "")
							found = found || w.Code == 200 && bytes.Contains(w.Body.Bytes(), []byte(`data-comparison-side="candidate"`))
						}
					}
					if !found {
						t.Error("drawable retained comparison disappeared")
					}
				}
				if !comparisonDraws && !finalDraws {
					reader963NoImages(t, n)
					if p.SVG != "" {
						t.Error("neither outcome draws, but receipt has an image link")
					}
				}
			}
			if !bytes.Equal(before, reader631JSON(t, f.Report)) {
				t.Fatal("receipt was resealed")
			}
		}
	}
}

func TestReader963SpecificReaderFailures(t *testing.T) {
	for _, kind := range []string{"missing_view", "reader_computation", "missing_artifacts"} {
		f := reader631FixtureFor(t, 3, "Ready")
		var edit func(map[string][]byte)
		switch kind {
		case "missing_view":
			f.Report.Reader.Chart = nil
			f.Report.Reader.Unavailable = &verify.ReaderDiagnostic{Code: kind, Message: "No saved reader view."}
			delete(f.Files, ".invariant/view.json")
		case "reader_computation":
			f.Report.Reader.Chart = nil
			f.Report.Reader.Unavailable = &verify.ReaderDiagnostic{Code: kind, Message: "Invariant replay failed <literal> & preserved."}
		case "missing_artifacts":
			edit = func(entries map[string][]byte) { delete(entries, "reader/reader.svg") }
		}
		f.bind(t)
		s, _, p := receipt723Load(t, f, edit)
		r := ReaderPresentation{Status: p.Status, Message: p.Message, Commit: p.Commit, Fingerprint: p.Fingerprint, Format: p.Format, Page: p.Page, SVG: p.SVG}
		_, fold := reader963Unavailable(t, s, &r)
		if p.Status != kind {
			t.Errorf("specific %s reason became %s", kind, p.Status)
		}
		reader963HasRecord(t, fold, f.Report.Reader)
		if kind == "reader_computation" {
			reader963Words(t, p.Message, "Invariant replay failed <literal> & preserved.")
		}
		if strings.Contains(p.Message, "500") || strings.Contains(p.Message, "projection ceiling") {
			t.Error("non-size failure became size explanation")
		}
	}
	// Damage only the retained comparison. A final chart survives, and the
	// comparison_evidence diagnostic remains literal in folded HTML evidence.
	f := receipt723Fixture(t)
	f.Files[".invariant/comparison.json"] = []byte(`{"version":`)
	s, _, p := receipt723Load(t, f, nil)
	if p.Status != "chart" {
		t.Error("comparison-only damage erased valid final chart")
	}
	n := reader963GET(t, s, p.Page)
	reader963Words(t, n.words(true), "comparison_evidence", "unexpected EOF")
	receipt723Topology(t, receipt723HTTP(t, s, p.SVG), f.Report.Reader.Chart)
}
