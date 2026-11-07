package dashboard

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"html"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/verify"
)

// Use the complete #631 fixtures, production receipt writer and literal gh
// fake. Every read is at one receipt commit; unrelated bindings stay intact.
const receipt723RepoName = "owner/receipt723"
const receipt723Commit = "7777777777777777777777777777777777777777"

func receipt723Snapshot(f reader631Fixture, name string) statechart.ComparisonSnapshot {
	return statechart.ComparisonSnapshot{Kind: "proposal", Name: name, Module: "Dashboard631",
		Model: string(f.Files[f.Manifest.Module]), View: string(f.Files[".invariant/view.json"]),
		Includes: map[string]string{"Helper631.tla": string(f.Files[".invariant/specs/Helper631.tla"])},
		Lock:     f.Lock, Chart: f.Report.Reader.Chart, Limitation: f.Report.Reader.Limitation}
}

func receipt723Fixture(t *testing.T) reader631Fixture {
	t.Helper()
	a := reader631FixtureFor(t, 2, "Proposal before")
	b := reader631FixtureFor(t, 3, "Proposal after")
	c, err := statechart.Compare(receipt723Snapshot(a, "Captured baseline"), receipt723Snapshot(b, "Selected proposal"))
	if err != nil {
		t.Fatal(err)
	}
	f := reader631FixtureFor(t, 3, `Final ready λ <script>literal</script> & "quoted"`)
	f.Files[f.Manifest.Module] = append(f.Files[f.Manifest.Module], []byte("\\* Independently synthesized final source.\n")...)
	f.Report.Reader.Comparison = c
	f.Files[".invariant/comparison.json"] = reader631JSON(t, c)
	f.bind(t)
	return f
}

func receipt723Clone(t *testing.T, f reader631Fixture) reader631Fixture {
	t.Helper()
	var out reader631Fixture
	if err := json.Unmarshal(reader631JSON(t, f), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func receipt723Seal(t *testing.T, r *verify.Report, lock project.Lock) {
	t.Helper()
	r.Reader.Fingerprint = ""
	r.Reader.Fingerprint = reader631Hash(reader631JSON(t, struct {
		Reader verify.Reader `json:"reader"`
		Lock   project.Lock  `json:"lock"`
	}{*r.Reader, lock}))
	r.Fingerprint = verify.Fingerprint(*r)
}

func receipt723EditRecord(t *testing.T, lock project.Lock, entries map[string][]byte, change func(*verify.Reader)) {
	t.Helper()
	var r verify.Report
	if err := json.Unmarshal(entries["reader/receipt.json"], &r); err != nil {
		t.Fatal(err)
	}
	change(r.Reader)
	receipt723Seal(t, &r, lock)
	entries["reader/receipt.json"] = reader631JSON(t, &r)
	entries["reader/reader.json"] = reader631JSON(t, r.Reader)
	// Keep the original SVG. Comparison-only edits leave its final drawing
	// intact; final-chart edits deliberately create a companion mismatch.
}

func receipt723Evidence(t *testing.T, f reader631Fixture) readerEvidence {
	t.Helper()
	set, err := readReceiptZip(reader631Archive(t, []reader631Fixture{f}, nil))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for name, data := range f.Files {
		if path.Dir(name) == path.Dir(f.Manifest.Module) && strings.HasSuffix(name, ".tla") {
			files[path.Base(name)] = data
		}
	}
	return readerEvidence{Format: readerFormat, Repo: receipt723RepoName, Dir: f.Dir, Commit: receipt723Commit,
		Manifest: f.Manifest, Lock: f.Lock, ManifestBytes: f.Files[".invariant/invariant.json"], LockBytes: f.Files[".invariant/ratified.lock"],
		Files: files, View: f.Files[".invariant/view.json"], Comparison: f.Files[".invariant/comparison.json"],
		Report: set.reports["reader"], Artifacts: set.readers["reader"], Traces: set.traces["reader"]}
}

func receipt723Healthy(t *testing.T, f reader631Fixture) {
	t.Helper()
	e := receipt723Evidence(t, f)
	before := reader631JSON(t, e)
	status, message, svg := e.checked()
	if status != "chart" || svg == "" {
		t.Fatalf("fixture must have independently healthy evidence before tampering: %s %s", status, message)
	}
	if !bytes.Equal(before, reader631JSON(t, e)) {
		t.Fatal("checking evidence mutated the retained report")
	}
	if f.Report.Reader.Comparison != nil {
		if f.Report.Reader.Inputs.Model == f.Report.Reader.Comparison.CandidateIdentity.ModelSHA256 ||
			!strings.Contains(message, "not the synthesized final model") {
			t.Fatal("fixture lost the intentional proposal/final source distinction")
		}
	}
}

func receipt723Load(t *testing.T, f reader631Fixture, edit func(map[string][]byte)) (*Server, *Repo, reader631Presentation) {
	t.Helper()
	archive := reader631Archive(t, []reader631Fixture{f}, edit)
	expected, err := readReceiptZip(archive)
	if err != nil {
		t.Fatal(err)
	}
	before := reader631JSON(t, expected.reports["reader"])
	r := reader631Repo(t, receipt723RepoName, receipt723Commit, 723, []reader631Fixture{f}, archive)
	s := &Server{Repos: []*Repo{r}, Cache: t.TempDir()}
	p := reader631Find(t, reader631Load(t, s, r), r.Name, f.Dir)
	if !bytes.Equal(before, reader631JSON(t, r.src.receipts.reports["reader"])) {
		t.Fatal("dashboard rewrote the retained receipt to make fallback pass")
	}
	return s, r, p
}

func receipt723Restart(t *testing.T, s *Server, p reader631Presentation, f reader631Fixture) *Server {
	t.Helper()
	cold := &Server{Repos: []*Repo{{Name: receipt723RepoName}}, Cache: s.Cache}
	cold.loadSnapshot()
	w := reader631GET(cold, "GET", "/api/state.json", "")
	if w.Code != 200 {
		t.Fatalf("legitimately recorded comparison diagnostic did not survive restart: %d", w.Code)
	}
	var snap Snapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	project := readerRecoveryProject(t, snap, receipt723RepoName, f.Dir)
	if !sameReaderJSON(project.Reader, s.assemble(time.Now()).Projects[0].Reader) || project.Reader.Page != p.Page {
		t.Fatal("restart changed unchanged comparison/final presentation identity")
	}
	if !sameReaderJSON(cold.Repos[0].src.receipts.reports["reader"], s.Repos[0].src.receipts.reports["reader"]) {
		t.Fatal("restart resealed or replaced the retained report")
	}
	return cold
}

func receipt723HTTP(t *testing.T, s *Server, url string) []byte {
	t.Helper()
	w := reader631GET(s, "GET", url, "")
	if w.Code != 200 || w.Header().Get("ETag") == "" || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("missing immutable evidence at %s: %d %v", url, w.Code, w.Header())
	}
	for _, method := range []string{"GET", "HEAD"} {
		conditional := reader631GET(s, method, url, `"other", W/`+w.Header().Get("ETag"))
		if conditional.Code != 304 || conditional.Body.Len() != 0 {
			t.Fatalf("%s conditional evidence response: %d", method, conditional.Code)
		}
	}
	head := reader631GET(s, "HEAD", url, "")
	if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("ETag") != w.Header().Get("ETag") {
		t.Fatal("HEAD changed the evidence identity")
	}
	return w.Body.Bytes()
}

func receipt723Topology(t *testing.T, svg []byte, want *statechart.Result) {
	t.Helper()
	d := xml.NewDecoder(bytes.NewReader(svg))
	states, arrows, initial, reports := 0, 0, 0, 0
	for {
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("fallback must be native XML: %v", err)
		}
		el, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		attrs := map[string]string{}
		for _, a := range el.Attr {
			attrs[a.Name.Local] = a.Value
			if strings.HasPrefix(strings.ToLower(a.Name.Local), "on") {
				t.Fatal("active SVG attribute")
			}
		}
		if strings.EqualFold(el.Name.Local, "script") || strings.EqualFold(el.Name.Local, "foreignObject") {
			t.Fatal("literal comparison diagnostic became executable content")
		}
		if attrs["data-comparison-side"] != "" {
			t.Fatal("damaged comparison remains drawn")
		}
		if attrs["data-state-id"] != "" {
			states++
		}
		if attrs["data-from"] != "" && attrs["data-to"] != "" {
			arrows++
		}
		if attrs["data-initial"] != "" {
			initial++
		}
		if el.Name.Local == "metadata" && strings.HasSuffix(attrs["id"], "statechart-data") {
			var raw string
			if err := d.DecodeElement(&raw, &el); err != nil {
				t.Fatal(err)
			}
			var got statechart.Result
			if err := json.Unmarshal([]byte(raw), &got); err != nil {
				t.Fatal(err)
			}
			if !sameReaderJSON(got, want) {
				t.Fatal("fallback lost the complete independent final report")
			}
			reports++
		}
	}
	if states != len(want.States) || arrows != len(want.Arrows) || initial != len(want.Initial) || reports != 1 {
		t.Fatalf("fallback lost topology or invented comparison evidence: states=%d arrows=%d initials=%d reports=%d", states, arrows, initial, reports)
	}
	visible := comparison717SVGText(t, string(svg))
	for _, state := range want.States {
		if !strings.Contains(visible, strings.Join(strings.Fields(state.Label), " ")) {
			t.Fatalf("final state label is not visible: %q", state.Label)
		}
	}
	for _, arrow := range want.Arrows {
		if !strings.Contains(visible, arrow.Label) {
			t.Fatalf("final arrow label is not visible: %q", arrow.Label)
		}
	}
}

func receipt723Fallback(t *testing.T, s *Server, p reader631Presentation, f reader631Fixture, detail string) {
	t.Helper()
	if p.Status != "chart" || p.SVG == "" || !strings.Contains(p.Message, "comparison_evidence") {
		t.Fatalf("comparison-only failure must keep a checked final chart and its specific diagnostic: %+v", p)
	}
	svg := receipt723HTTP(t, s, p.SVG)
	receipt723Topology(t, svg, f.Report.Reader.Chart)
	page := receipt723HTTP(t, s, p.Page)
	visible := comparison717SVGText(t, string(svg))
	for name, text := range map[string]string{"card": p.Message, "page": html.UnescapeString(string(page)), "SVG": visible} {
		if !strings.Contains(text, "comparison_evidence") || (detail != "" && !strings.Contains(text, detail)) {
			t.Fatalf("%s lost comparison cause %q: %s", name, detail, text)
		}
		if strings.ContainsRune(text, '\x01') {
			t.Fatalf("%s contains an unprintable diagnostic", name)
		}
	}
	if !strings.Contains(visible, "Independently checked final-model chart") || !strings.Contains(visible, f.Report.Reader.Inputs.Model) {
		t.Fatal("fallback did not identify the independently checked final sources")
	}
	if strings.Contains(strings.ToLower(string(page)), "<script") || !strings.Contains(string(page), p.SVG) {
		t.Fatal("fallback evidence page lost its safe native image link")
	}
	project := readerRecoveryProject(t, s.assemble(time.Now()), receipt723RepoName, f.Dir)
	if project.Poster == nil || project.Reader.SVG != p.SVG || project.Reader.Message != p.Message {
		t.Fatal("project card dropped the healthy final poster or comparison diagnostic")
	}
	for _, item := range []struct {
		url      string
		animated bool
	}{{project.Poster.Animated, true}, {project.Poster.Still, false}} {
		want, err := f.Report.Reader.Chart.Poster(statechart.PosterOptions{Title: f.Manifest.Name, Animated: item.animated})
		if err != nil {
			t.Fatal(err)
		}
		if string(receipt723HTTP(t, s, item.url)) != want {
			t.Fatal("fallback poster differs from the complete checked final chart")
		}
	}
}

func TestReceipt723ComparisonPresenceIsSymmetric(t *testing.T) {
	base := receipt723Fixture(t)
	receipt723Healthy(t, base)
	// Captured from the original checkout, including native image, posters,
	// HTML and evidence bytes. A format/template bump must not invalidate
	// unchanged evidence from a commit that has no comparison.
	legacy := reader631FixtureFor(t, 3, "Legacy ready")
	if got := makeReader(receipt723Evidence(t, legacy)).presentation.Fingerprint; got != "sha256:883064660d238f3da66b73b6430aa35c1a36f822046c7d6817cfe00c2aa7fcb6" {
		t.Fatalf("unchanged legacy evidence changed identity: %s", got)
	}
	for _, mode := range []string{"valid", "legacy absent", "omitted receipt", "unexpected receipt", "diagnostic with valid file", "diagnostic without file", "different valid comparison"} {
		t.Run(mode, func(t *testing.T) {
			f := receipt723Clone(t, base)
			switch mode {
			case "legacy absent":
				delete(f.Files, ".invariant/comparison.json")
				f.Report.Reader.Comparison = nil
			case "omitted receipt":
				f.Report.Reader.Comparison = nil
			case "unexpected receipt":
				delete(f.Files, ".invariant/comparison.json")
			case "diagnostic with valid file", "diagnostic without file":
				f.Report.Reader.Comparison = nil
				f.Report.Reader.ComparisonDiagnostic = &verify.ReaderDiagnostic{Code: "comparison_evidence", Message: "Proposal comparison unavailable: invented failure"}
				if mode == "diagnostic without file" {
					delete(f.Files, ".invariant/comparison.json")
				}
			case "different valid comparison":
				c := f.Report.Reader.Comparison
				a := c.Baseline
				a.Name = "Another legitimate baseline"
				different, err := statechart.Compare(a, c.Candidate)
				if err != nil {
					t.Fatal(err)
				}
				if different.Fingerprint == c.Fingerprint {
					t.Fatal("ineffective comparison substitution")
				}
				f.Files[".invariant/comparison.json"] = reader631JSON(t, different)
			}
			f.bind(t)
			s, _, p := receipt723Load(t, f, nil)
			if mode == "valid" {
				if p.Status != "chart" || strings.Contains(p.Message, "comparison_evidence") {
					t.Fatalf("valid comparison refused: %+v", p)
				}
				svg := receipt723HTTP(t, s, p.SVG)
				if !bytes.Contains(svg, []byte(`data-comparison-side="candidate"`)) || !strings.Contains(p.Message, "not the synthesized final model") {
					t.Fatal("valid proposal/final source distinction lost")
				}
				receipt723Restart(t, s, p, f)
			} else if mode == "legacy absent" {
				reader631Chart(t, s, p, f.Report.Reader.Chart)
				if strings.Contains(p.Message, "comparison") {
					t.Fatal("absence was invented into comparison evidence")
				}
				receipt723Restart(t, s, p, f)
			} else {
				receipt723Fallback(t, s, p, f, "")
				cold := receipt723Restart(t, s, p, f)
				receipt723Fallback(t, cold, p, f, "")
			}
		})
	}
}

func TestReceipt723DamagedComparisonKeepsFinalTopology(t *testing.T) {
	base := receipt723Fixture(t)
	receipt723Healthy(t, base)
	for _, mode := range []string{"empty file", "malformed", "trailing JSON", "stale", "wrong lock", "undrawable", "omitted diagnostic", "matching diagnostic", "wrong diagnostic message", "wrong diagnostic code", "wrong diagnostic source", "literal diagnostic", "stale receipt comparison"} {
		t.Run(mode, func(t *testing.T) {
			f := receipt723Clone(t, base)
			c := f.Report.Reader.Comparison
			detail := ""
			var edit func(map[string][]byte)
			switch mode {
			case "empty file":
				f.Files[".invariant/comparison.json"] = []byte{}
				detail = "EOF"
			case "malformed", "omitted diagnostic", "matching diagnostic", "wrong diagnostic message", "wrong diagnostic code", "wrong diagnostic source":
				f.Files[".invariant/comparison.json"] = []byte(`{"version":`)
				detail = "unexpected EOF"
				if mode != "malformed" {
					f.Report.Reader.Comparison = nil
					if mode != "omitted diagnostic" {
						f.Report.Reader.ComparisonDiagnostic = &verify.ReaderDiagnostic{Code: "comparison_evidence", Message: "Proposal comparison unavailable: unexpected EOF"}
						switch mode {
						case "wrong diagnostic message":
							f.Report.Reader.ComparisonDiagnostic.Message = "Proposal comparison unavailable: invented error"
						case "wrong diagnostic code":
							f.Report.Reader.ComparisonDiagnostic.Code = "invented_code"
						case "wrong diagnostic source":
							f.Report.Reader.ComparisonDiagnostic.Source = &statechart.Source{Module: "Invented"}
						}
					}
				}
			case "trailing JSON":
				f.Files[".invariant/comparison.json"] = append(f.Files[".invariant/comparison.json"], []byte(" {}")...)
				detail = "extra comparison JSON data"
			case "stale":
				var broken statechart.Comparison
				if err := json.Unmarshal(f.Files[".invariant/comparison.json"], &broken); err != nil {
					t.Fatal(err)
				}
				broken.Fingerprint = "sha256:" + strings.Repeat("0", 64)
				f.Files[".invariant/comparison.json"] = reader631JSON(t, &broken)
				detail = "stale or damaged"
			case "wrong lock":
				other := reader631FixtureFor(t, 4, "Other bounded proposal")
				wrong, err := statechart.Compare(c.Baseline, receipt723Snapshot(other, "Other proposal"))
				if err != nil {
					t.Fatal(err)
				}
				f.Files[".invariant/comparison.json"] = reader631JSON(t, wrong)
				detail = "candidate rules or bounds"
			case "undrawable":
				other := receipt723Clone(t, base).Report.Reader.Comparison
				other.Baseline.Chart.States[0].Label += "\x01"
				undrawable, err := statechart.Compare(other.Baseline, other.Candidate)
				if err != nil {
					t.Fatalf("undrawable fixture must pass source validation: %v", err)
				}
				if _, err := undrawable.SVG(); err == nil {
					t.Fatal("fixture must fail rendering")
				}
				f.Files[".invariant/comparison.json"] = reader631JSON(t, undrawable)
				detail = "cannot be drawn"
			case "literal diagnostic":
				other := receipt723Clone(t, base).Report.Reader.Comparison
				other.Baseline.Lock.Statements[0].Name = "Missing<script>literal</script>\x01&λ"
				f.Files[".invariant/comparison.json"] = reader631JSON(t, other)
				err := other.Validate()
				if err == nil {
					t.Fatal("literal diagnostic fixture must be damaged")
				}
				detail = strings.Map(func(r rune) rune {
					if unicode.IsGraphic(r) {
						return r
					}
					return '\uFFFD'
				}, err.Error())
			case "stale receipt comparison":
				edit = func(entries map[string][]byte) {
					receipt723EditRecord(t, f.Lock, entries, func(r *verify.Reader) { r.Comparison.Fingerprint = "stale receipt identity" })
				}
			}
			f.bind(t)
			s, _, p := receipt723Load(t, f, edit)
			receipt723Fallback(t, s, p, f, detail)
			if strings.HasPrefix(mode, "wrong diagnostic") && !strings.Contains(strings.ToLower(p.Message), "receipt") {
				t.Fatal("the full diagnostic mismatch was not identified as a receipt discrepancy")
			}
			cold := receipt723Restart(t, s, p, f)
			receipt723Fallback(t, cold, p, f, detail)
		})
	}
}

func TestReceipt723FallbackStillChecksFinalEvidence(t *testing.T) {
	base := receipt723Fixture(t)
	receipt723Healthy(t, base)
	base.Files[".invariant/comparison.json"] = []byte(`{"version":`)
	s, _, p := receipt723Load(t, base, nil)
	receipt723Fallback(t, s, p, base, "unexpected EOF")
	for _, mode := range []string{"model", "include", "view", "lock", "bounds", "report fingerprint", "reader fingerprint", "pin", "report count", "tool", "chart rules", "chart topology", "missing reader JSON", "different reader JSON", "missing SVG", "forged final drawing", "active SVG", "different final chart"} {
		t.Run(mode, func(t *testing.T) {
			f := receipt723Clone(t, base)
			status := "snapshot_mismatch"
			var edit func(map[string][]byte)
			switch mode {
			case "model":
				f.Files[f.Manifest.Module] = append(f.Files[f.Manifest.Module], []byte("\\* different final source\n")...)
			case "include":
				f.Files[".invariant/specs/Helper631.tla"] = append(f.Files[".invariant/specs/Helper631.tla"], '\n')
			case "view":
				f.Files[".invariant/view.json"] = append(f.Files[".invariant/view.json"], ' ')
			case "lock":
				f.Lock.Statements[0].Says += " Changed rule."
				f.Files[".invariant/ratified.lock"] = reader631JSON(t, f.Lock)
			case "bounds":
				f.Lock.Bounds = map[string]string{"Limit": "99"}
				f.Files[".invariant/ratified.lock"] = reader631JSON(t, f.Lock)
			case "report fingerprint":
				f.Report.Fingerprint = "forged report fingerprint"
			case "reader fingerprint":
				f.Report.Reader.Fingerprint = "forged reader fingerprint"
			case "pin":
				f.Report.Pins[0].Got = "wrong pin"
				f.Report.Fingerprint = verify.Fingerprint(*f.Report)
			case "report count":
				f.Report.Design.DistinctStates++
				f.Report.Fingerprint = verify.Fingerprint(*f.Report)
			case "tool":
				f.Report.Toolchain.TLCJarSHA256 = strings.Repeat("c", 64)
				f.Report.Fingerprint = verify.Fingerprint(*f.Report)
			case "chart rules":
				edit = func(entries map[string][]byte) {
					receipt723EditRecord(t, f.Lock, entries, func(r *verify.Reader) { r.Chart.Report.Rules[0].Says += " Not ratified." })
				}
			case "chart topology":
				status = ""
				edit = func(entries map[string][]byte) {
					receipt723EditRecord(t, f.Lock, entries, func(r *verify.Reader) { r.Chart.Arrows[0].Count++ })
				}
			case "missing reader JSON":
				status = "missing_artifacts"
				edit = func(entries map[string][]byte) { delete(entries, "reader/reader.json") }
			case "different reader JSON":
				status = "artifact_mismatch"
				edit = func(entries map[string][]byte) {
					var rr verify.Reader
					if err := json.Unmarshal(entries["reader/reader.json"], &rr); err != nil {
						t.Fatal(err)
					}
					rr.Inputs.Model = "different companion source"
					entries["reader/reader.json"] = reader631JSON(t, rr)
				}
			case "missing SVG":
				status = "missing_artifacts"
				edit = func(entries map[string][]byte) { delete(entries, "reader/reader.svg") }
			case "forged final drawing":
				status = "artifact_mismatch"
				edit = func(entries map[string][]byte) {
					// The archived comparison alone is stale too. Keeping its
					// final metadata must not license a different final drawing.
					receipt723EditRecord(t, f.Lock, entries, func(r *verify.Reader) { r.Comparison.Fingerprint = "stale" })
					svg := string(entries["reader/reader.svg"])
					// Last state group belongs to the final model in the native stack.
					at := strings.LastIndex(svg, `data-state-id=`)
					if at < 0 {
						t.Fatal("fixture has no final drawing")
					}
					entries["reader/reader.svg"] = []byte(svg[:at] + `transform="translate(9999 9999)" ` + svg[at:])
				}
			case "active SVG":
				status = "artifact_mismatch"
				edit = func(entries map[string][]byte) {
					entries["reader/reader.svg"] = []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>forged()</script></svg>`)
				}
			case "different final chart":
				status = "artifact_mismatch"
				edit = func(entries map[string][]byte) {
					receipt723EditRecord(t, f.Lock, entries, func(r *verify.Reader) { r.Chart.States[0].Label = "Different final label" })
				}
			}
			s, _, p := receipt723Load(t, f, edit)
			reader631Diagnostic(t, s, p, status)
			if p.Status == "comparison_evidence" {
				t.Fatal("comparison error hid an independent final-evidence failure")
			}
			if project := readerRecoveryProject(t, s.assemble(time.Now()), receipt723RepoName, f.Dir); project.Poster != nil {
				t.Fatal("invalid final evidence supplied a poster")
			}
			cold := receipt723Restart(t, s, p, f)
			reader631Diagnostic(t, cold, p, status)
		})
	}
}

func TestReceipt723FinalLimitationsStaySpecific(t *testing.T) {
	for _, outcome := range []string{"too_large", "missing_view", "reader_computation"} {
		for _, comparison := range []string{"omitted", "unexpected", "malformed"} {
			t.Run(outcome+"/"+comparison, func(t *testing.T) {
				f := receipt723Fixture(t)
				receipt723Healthy(t, f)
				if outcome == "too_large" {
					large := reader631FixtureFor(t, 501, "Large final model")
					c, err := statechart.Compare(f.Report.Reader.Comparison.Baseline, receipt723Snapshot(large, "Large proposal"))
					if err != nil {
						t.Fatal(err)
					}
					f = large
					f.Report.Reader.Comparison = c
					f.Files[".invariant/comparison.json"] = reader631JSON(t, c)
				} else {
					f.Report.Reader.Chart = nil
					message := "Invariant replay failed <literal> & preserved."
					if outcome == "missing_view" {
						delete(f.Files, ".invariant/view.json")
						message = "No reader chart is available. Supply .invariant/view.json and verify again."
					}
					f.Report.Reader.Unavailable = &verify.ReaderDiagnostic{Code: outcome, Message: message}
				}
				f.bind(t)
				// Independently validate the specific final outcome before
				// adding a comparison discrepancy; absence isn't an empty graph.
				plain := receipt723Clone(t, f)
				plain.Report.Reader.Comparison = nil
				delete(plain.Files, ".invariant/comparison.json")
				plain.bind(t)
				status, _, svg := receipt723Evidence(t, plain).checked()
				if status != outcome || svg != "" {
					t.Fatalf("invalid final outcome fixture: %s", status)
				}
				switch comparison {
				case "omitted":
					f.Report.Reader.Comparison = nil
				case "unexpected":
					delete(f.Files, ".invariant/comparison.json")
				case "malformed":
					f.Files[".invariant/comparison.json"] = []byte(`{"version":`)
				}
				f.bind(t)
				s, _, p := receipt723Load(t, f, nil)
				for _, server := range []*Server{s, receipt723Restart(t, s, p, f)} {
					reader631Diagnostic(t, server, p, outcome)
					if !strings.Contains(p.Message, "comparison_evidence") {
						t.Fatalf("specific final outcome lost comparison diagnostic: %+v", p)
					}
					if outcome == "too_large" && (!strings.Contains(p.Message, "501") || !strings.Contains(p.Message, "500")) {
						t.Fatal("limitation lost its counts")
					}
					if outcome == "reader_computation" && !strings.Contains(p.Message, "Invariant replay failed <literal> & preserved.") {
						t.Fatal("lost final unavailable cause")
					}
					if project := readerRecoveryProject(t, server.assemble(time.Now()), receipt723RepoName, f.Dir); project.Poster != nil {
						t.Fatal("no final chart may supply a poster")
					}
				}
			})
		}
	}
}

func TestReceipt723FallbackIdentityAndCacheRecovery(t *testing.T) {
	f := receipt723Fixture(t)
	receipt723Healthy(t, f)
	valid, _, old := receipt723Load(t, f, nil)
	oldSVG := receipt723HTTP(t, valid, old.SVG)
	oldETag := reader631GET(valid, "GET", old.SVG, "").Header().Get("ETag")
	f.Report.Reader.Comparison = nil
	f.bind(t)
	s, r, fallback := receipt723Load(t, f, nil)
	receipt723Fallback(t, s, fallback, f, "")
	if fallback.Fingerprint == old.Fingerprint || fallback.Page == old.Page || fallback.SVG == old.SVG {
		t.Fatal("different displayed comparison outcome reused a valid comparison's identity")
	}
	if w := reader631GET(s, "GET", fallback.SVG, oldETag); w.Code != 200 || bytes.Equal(w.Body.Bytes(), oldSVG) || w.Header().Get("ETag") == oldETag {
		t.Fatal("changed comparison presentation reused old bytes or a conditional response")
	}
	if w := reader631GET(s, "GET", old.SVG, oldETag); w.Code != 404 {
		t.Fatal("fallback server advertised a comparison it cannot validate at its old URL")
	}
	cold := receipt723Restart(t, s, fallback, f)
	receipt723Fallback(t, cold, fallback, f, "")
	// Different malformed bytes can have the same printable parser error;
	// the captured bytes must still participate in the immutable identity.
	seen := map[string]bool{fallback.Fingerprint: true}
	for _, raw := range []string{`{"version":`, `{"version": `} {
		other := receipt723Clone(t, f)
		other.Files[".invariant/comparison.json"] = []byte(raw)
		other.Report.Reader.ComparisonDiagnostic = &verify.ReaderDiagnostic{Code: "comparison_evidence", Message: "Proposal comparison unavailable: unexpected EOF"}
		other.bind(t)
		server, _, p := receipt723Load(t, other, nil)
		receipt723Fallback(t, server, p, other, "unexpected EOF")
		if seen[p.Fingerprint] {
			t.Fatal("different captured comparison bytes reused an immutable URL")
		}
		seen[p.Fingerprint] = true
	}

	// A legitimate diagnostic is retained evidence, not an exemption from
	// D-0353. Corrupt that very entry, keeping its old card and totals.
	file := s.readerFile(strings.TrimPrefix(fallback.Fingerprint, "sha256:"))
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var saved readerEvidence
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	saved.Comparison = []byte(`{"version":`)
	reader631Write(t, file, reader631JSON(t, saved))
	archive := reader631Archive(t, []reader631Fixture{f}, nil)
	unavailable := reader631Repo(t, r.Name, receipt723Commit, 723, []reader631Fixture{f}, archive)
	endpoint := "repos/" + r.Name + "/actions/artifacts/631/zip"
	if err := os.Remove(filepath.Join(filepath.Dir(unavailable.GitHub.GH), strings.TrimPrefix(reader631Hash([]byte(endpoint)), "sha256:"))); err != nil {
		t.Fatal(err)
	}
	broken := &Server{Repos: []*Repo{unavailable}, Cache: s.Cache}
	broken.loadSnapshot()
	if len(broken.state) != 0 || reader631GET(broken, "GET", "/api/state.json", "").Code != 503 {
		t.Fatal("corrupt fallback evidence was used to publish stale whole-snapshot totals")
	}
	for _, url := range []string{fallback.SVG, fallback.Page} {
		if w := reader631GET(broken, "GET", url, ""); w.Code != 404 {
			t.Fatalf("corrupt fallback still served at old URL %s", url)
		}
	}
	if err := broken.readReceipts(t.Context(), unavailable); err == nil {
		t.Fatal("cache corruption did not retry the unavailable archive")
	}
	project := readerRecoveryProject(t, broken.assemble(time.Now()), receipt723RepoName, f.Dir)
	readerRecoveryNoResults(t, project)
	reader631Diagnostic(t, broken, reader631Presentation(*project.Reader), "cached_evidence")
	recovered := reader631Repo(t, r.Name, receipt723Commit, 723, []reader631Fixture{f}, archive)
	unavailable.GitHub = recovered.GitHub
	p := reader631Find(t, reader631Load(t, broken, unavailable), r.Name, f.Dir)
	if !reflect.DeepEqual(p, fallback) {
		t.Fatal("unchanged authoritative archive did not restore the same fallback identity")
	}
	receipt723Fallback(t, broken, p, f, "")
	receipt723Restart(t, broken, p, f)
	if unavailable.src.receipts.retry {
		t.Fatal("recovered archive still marked for retry")
	}
}
