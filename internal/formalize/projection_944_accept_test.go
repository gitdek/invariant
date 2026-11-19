package formalize_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

func proposal944View(n int) string {
	var b strings.Builder
	b.WriteString(`{"version":1,"state":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":"s%d","when":{"eq":[{"var":"n"},"%d"]},"label":"Stage %d"}`, i, i, i)
	}
	b.WriteString(`,{"id":"rest","when":true,"label":"Stage 0"}],"step":[{"id":"all","when":true,"label":"Continue"}]}`)
	return b.String()
}

func TestReader944ProposalReplayAndPreview(t *testing.T) {
	tc, log := reader603Tools(t)
	for _, n := range []int{501, 24760} {
		ws := t.TempDir()
		reader603Workspace(t, ws)
		reader603Write(t, filepath.Join(ws, "proposal.json"), strings.Replace(reader603Draft, `"Limit":"2"`, fmt.Sprintf(`"Limit":"%d"`, n-1), 1))
		p, r := reader603Checked(t, ws, tc)
		if p.Reader.Chart == nil || p.Reader.Limitation != nil || r.Reader == nil || r.Reader.Chart == nil {
			t.Fatalf("%d concrete states must draw in both independent checks", n)
		}
		if p.Reader.Chart.Report.Counts["tlc_states"] != int64(n) || p.Reader.Chart.Report.Counts["hidden_steps"] != int64(n) || p.Reader.Chart.Report.Counts["shown_steps"] != int64(n) {
			t.Fatal("large check lost occurrences")
		}
		if err := p.ValidateReader(); err != nil {
			t.Fatal(err)
		}
		if p.Hash != project.ProposalHash(p.Bounds, p.Statements) {
			t.Fatal("reader changed proposal identity")
		}
		if strings.Contains(p.ReaderFeedback(), "too_large") {
			t.Fatal("stale capacity feedback")
		}
		for _, view := range []string{
			strings.ReplaceAll(reader603View, `"var":"n"`, `"var":"absent"`),
			strings.Replace(reader603View, `"label":"Continue"`, `"label":"Continue","hide":true`, 1),
		} {
			reader603Write(t, filepath.Join(ws, "view.json"), view)
			q, _, err := formalize.Check(t.Context(), ws, tc)
			if err == nil || q != nil && q.Reader != nil {
				t.Fatal("runtime view failure became size success")
			}
		}
	}
	replays := 0
	for _, call := range reader603Calls(t, log) {
		if call["dump"] != "" && call["module"] != "Reader603" {
			replays++
		}
		if call["main"] != reader603Model || call["include"] != reader603Include {
			t.Fatal("source snapshot changed")
		}
		for _, flag := range []string{" tlc2.TLC ", " --network none ", " -workers 1 "} {
			if !strings.Contains(call["args"], flag) {
				t.Fatal("fake lost invocation check", flag)
			}
		}
		if strings.Contains(call["args"], " -deadlock ") {
			t.Fatal("deadlock contract changed")
		}
	}
	if replays < 4 {
		t.Fatal("large proposals and independent receipts did not replay invariants")
	}
	d := preview639Draft(t, 1, nil)
	preview639Input(d, 0, 0)["model"].(map[string]any)["bounds"] = map[string]string{"Limit": "500"}
	ws := t.TempDir()
	preview639Write(t, ws, d)
	p := preview639Checked(t, ws, tc)
	v := p.Forks[0].Options[0].Preview
	if v.Model.Reader.Chart == nil || v.Receipt.Reader.Chart == nil || v.Model.Reader.Chart.Report.Counts["tlc_states"] != 501 {
		t.Fatal("modeled alternative retained concrete-500 limitation")
	}
	if err := p.Forks[0].ValidatePreview(p.Forks[0].Options[0]); err != nil {
		t.Fatal(err)
	}
}

func TestReader944ProjectedLimitationAndBaselineMemo(t *testing.T) {
	tc, log := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	reader603Write(t, filepath.Join(ws, "proposal.json"), strings.Replace(reader603Draft, `"Limit":"2"`, `"Limit":"501"`, 1))
	reader603Write(t, filepath.Join(ws, "view.json"), proposal944View(501))
	p, r := reader603Checked(t, ws, tc)
	if p.Reader.Chart != nil || p.Reader.Limitation == nil || r.Reader.Limitation == nil {
		t.Fatal("501 projected states need a complete limitation")
	}
	var l map[string]any
	if err := json.Unmarshal(reader603JSON(t, p.Reader.Limitation), &l); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]any{"code": "too_large", "kind": "projected_states", "actual": float64(501), "limit": float64(500), "concrete_states": float64(502), "projected_states": float64(501), "view_checked": true} {
		if l[k] != want {
			t.Fatalf("%s=%v, want %v", k, l[k], want)
		}
	}
	if err := p.ValidateReader(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(p.ReaderFeedback()), "not evaluated") {
		t.Fatal("feedback falsely says mapping was skipped")
	}
	cur := baseline701Current(t, reader603View, "500")
	authority := (formalize.Request{Current: cur}).CheckContext()
	session := formalize.NewCheckSession(authority)
	reader603Write(t, filepath.Join(ws, "view.json"), reader603View)
	first, _, err := session.Check(t.Context(), ws, tc)
	if err != nil || first == nil || first.Reader.Comparison.Baseline.Chart == nil {
		t.Fatalf("large baseline check: %v", err)
	}
	before := baseline701Counts(t, log, cur.ModuleText)
	if before.replay == 0 {
		t.Fatal("501-state baseline did not replay")
	}
	saved := reader603JSON(t, first.Reader.Comparison.Baseline)
	first.Reader.Comparison.Baseline.Chart.Report.Counts["tlc_states"] = 1
	second, _, err := session.Check(t.Context(), ws, tc)
	if err != nil || string(saved) != string(reader603JSON(t, second.Reader.Comparison.Baseline)) || baseline701Counts(t, log, cur.ModuleText) != before {
		t.Fatal("memo missed, aliased data or changed captured baseline", err)
	}
	final, _, err := formalize.CheckWithContext(t.Context(), ws, tc, authority)
	after := baseline701Counts(t, log, cur.ModuleText)
	if err != nil || final == nil || after.gate <= before.gate || after.graph <= before.graph || after.replay <= before.replay {
		t.Fatal("independent final check reused baseline memo", err)
	}
	if !reflect.DeepEqual(final.Reader.Comparison.Baseline.Lock, cur.Lock) || final.Reader.Comparison.Baseline.Model != cur.ModuleText {
		t.Fatal("caller-owned source authority lost")
	}
}

// Before any production edits, the builder must run a new fixture-capture
// regression test against unchanged main at 950fefa6 and commit these four files:
// testdata/reader944/{manifest,ordinary,preview,comparison}.json. The manifest
// records base_revision (950fefa6 or its full commit ID) and exact deterministic
// inputs/commands. The hashes below were captured by running main's own check
// and rendering code in a disposable copy of that base, not calculated by a
// candidate. They are authority even if a fixture or its manifest is replaced.
// Each evidence file stores the bytes below as JSON strings, plus the full
// proposal/preview and receipt inputs. Capture with reader603's unchanged
// complete-protocol fake, Limit=2, and for comparison a Limit=3 candidate with
// Working renamed Revised; its authority is Request{Previous:ordinary}.
// Preview uses preview639Draft(t,1,nil), option A (Limit=1, not Limit=2).
// Retain exact include/model/view, deterministic graph IDs, pinned tools and
// statements; exclude wall-clock report fields from byte assertions only.
// A fixture must NEVER be regenerated from candidate production output.
type reader944Golden struct {
	Proposal                                                                 *formalize.Proposal `json:"proposal"`
	Receipt                                                                  *verify.Report      `json:"receipt"`
	Preview                                                                  *formalize.Proposal `json:"preview,omitempty"`
	JSON, Markdown, SVG, Animated, Still, Fingerprint, ComparisonFingerprint string
}

// Each digest covers the UTF-8 bytes of the named JSON string value, without
// JSON quoting or an added newline. Fingerprint values include their sha256:
// prefix; ComparisonFingerprint is empty for ordinary and preview records.
// Do not regenerate these pins during the build or derive them from fixtures.
func reader944MainHashes() map[string]map[string]string {
	return map[string]map[string]string{
		"ordinary": {
			"JSON":                  "53bb12e9da73e383203dd696158faa6dfaaf66ea705198e943bffdc06d3ebe89",
			"Markdown":              "0e97179c0733df4be5e75acc0f8dc79b154c6df9a6d1a7667c74a755c0d57849",
			"SVG":                   "529a8bc6fc8909e1a609068ac3762094bc7d64822dd2eec35af40d20b4f50dda",
			"Animated":              "6a8208318c0a709b6f726a19fb13d40eae042ea6dcdb09a1ddad7d1bd909aa4d",
			"Still":                 "2848a2d5b76d293fd31b7e7022bb87e748d820c92f636fa170df04bff717920a",
			"Fingerprint":           "9508e33d28484f53034db0c11c3b4dd6cd3fc79e562f8ec62a0009fcf92bed4b",
			"ComparisonFingerprint": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		"preview": {
			"JSON":                  "397125a6655d9f59b77a0671af1afb4add97e2004bc86dcd57b421c11476092c",
			"Markdown":              "e6ff5c46793466afcabff3f914c0edf26a8e8c5be62c04a54d572ba4e868e96a",
			"SVG":                   "5162b38e096299bad96e046462e9cd1a9e9455fba2dc023de7fddf5ac6d44c6a",
			"Animated":              "01f1a13513423cdf774f5a2985465458ff0e68e4bea40cc5ab2a5580ec4bab46",
			"Still":                 "7100cc0d263799b2c572514b152af67d76e044ef9ab13291a12be467d46691b2",
			"Fingerprint":           "1db658246685c5456a10941d337479e82575c006365426c4544bccb72609d8f1",
			"ComparisonFingerprint": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		"comparison": {
			"JSON":                  "e4bf6d6dcd974830d06a7dcd640b04b0839be9fa52c4498606115feb2ee55bce",
			"Markdown":              "9fd428a5863b3e8d975c7b5a7484598e85ec10087fe2f0ed76af1560f2596392",
			"SVG":                   "6c63a3663671490a2167960fbe61ed08f651b0179457b021a4b99239ce3bf80f",
			"Animated":              "680858de34a20ebcc2391846b1ab1c657f90d8da9431783de4c99c289fbe0f65",
			"Still":                 "2b86e47dec3f6091c4123d03a4ef92ef9b728a8b00a082c31b2fab21c328976d",
			"Fingerprint":           "2e8103a387d83d2a5c90b9fb8addd2e9a9fbe779b8dc827a6e9e1f10839e5311",
			"ComparisonFingerprint": "12081190c11770c360d785618cc5d05c83efbbff22a1a10ef326bdeeba20c58a",
		},
	}
}

func reader944PinnedBytes(t *testing.T, name string, g reader944Golden) {
	t.Helper()
	reader944PinnedAgainst(t, name, g, reader944MainHashes(), "main 950fefa6's output")
}

// reader944LayoutHashes is what production renders fresh since #814 gives a
// draft's statements their layout pins (D-0488): main 950fefa6's output,
// but for the outputs those pins reach, a chart's rules, its reader
// fingerprint and a comparison's identities, which moved (D-0512). The saved
// fixtures stay main 950fefa6's output.
func reader944LayoutHashes() map[string]map[string]string {
	h := reader944MainHashes()
	for name, moved := range map[string]map[string]string{
		"ordinary": {
			"JSON":        "06b84315132a4c90abbb199f6aae80da1f04896a474b122d3d45a2f151cad0ab",
			"SVG":         "7aa814e6a9019bf83bacfd17004065557885ae22e19bf08edae0bb26428495fd",
			"Fingerprint": "b21cf6dd05d0f32f1007018ef6702b00e04e2fe9e93a3bed65afb7b877162621",
		},
		"preview": {
			"JSON":        "cfbf767cf599440d223216f3cf49460dbd5d2c5561046689eca289e977acc6bd",
			"SVG":         "fc2e5395b8c597c3329571557c4e60541814d4cb37a55975aca4c7b0a76c9170",
			"Fingerprint": "207e5249791c5bb48daede5075bb568bd6e40dadd594e4da653a53a243fc86b0",
		},
		"comparison": {
			"JSON":                  "355a1e9c25cba7cf7fbb815b0c5a10cb5bb290c3647495d2d3c65fe30305eadc",
			"Markdown":              "2199f482fb4bc8743fa7aa1b1f1957eb27f84fb1632c57c2d4afe352c1421bdc",
			"SVG":                   "ef205ec06a6c379fbea452fbf413b416061edc1e7a077e9c8061c5e22904d8ea",
			"Fingerprint":           "975f0558443d1a238dda513dfb2c68974f9e50cb6b0696ed1187e7144c47584a",
			"ComparisonFingerprint": "746c9b6de6717e51cf6ffada61600ef42188dd50cd9dcc995d500084cf695d57",
		},
	} {
		for field, hash := range moved {
			h[name][field] = hash
		}
	}
	return h
}

func reader944PinnedAgainst(t *testing.T, name string, g reader944Golden, hashes map[string]map[string]string, what string) {
	t.Helper()
	values := map[string]string{"JSON": g.JSON, "Markdown": g.Markdown, "SVG": g.SVG,
		"Animated": g.Animated, "Still": g.Still, "Fingerprint": g.Fingerprint,
		"ComparisonFingerprint": g.ComparisonFingerprint}
	for field, want := range hashes[name] {
		if got := fmt.Sprintf("%x", sha256.Sum256([]byte(values[field]))); got != want {
			t.Fatalf("%s.%s is not %s: SHA-256 %s, want %s", name, field, what, got, want)
		}
	}
}

// Builders can call this helper from a new capture regression on unchanged
// main to write the fixtures; it also checks all three fresh candidate outputs.
// Saved Receipt/Preview fields retain full inputs; time-dependent receipt
// fields are deliberately outside the seven rendered-byte/fingerprint pins.
func reader944SmallFixtures(t *testing.T) map[string]reader944Golden {
	t.Helper()
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	ordinary, ordinaryReceipt := reader603Checked(t, ws, tc)
	authority := (formalize.Request{Previous: ordinary}).CheckContext()
	reader603Write(t, filepath.Join(ws, "proposal.json"), strings.Replace(reader603Draft, `"Limit":"2"`, `"Limit":"3"`, 1))
	reader603Write(t, filepath.Join(ws, "view.json"), strings.ReplaceAll(reader603View, "Working", "Revised"))
	comparison, comparisonReceipt, err := formalize.CheckWithContext(t.Context(), ws, tc, authority)
	if err != nil {
		t.Fatal(err)
	}
	pw := t.TempDir()
	preview639Write(t, pw, preview639Draft(t, 1, nil))
	preview := preview639Checked(t, pw, tc)
	v := preview.Forks[0].Options[0].Preview
	return map[string]reader944Golden{
		"ordinary":   {Proposal: ordinary, Receipt: ordinaryReceipt},
		"preview":    {Proposal: v.Model, Receipt: v.Receipt, Preview: preview},
		"comparison": {Proposal: comparison, Receipt: comparisonReceipt},
	}
}

func reader944Rendered(t *testing.T, g reader944Golden) reader944Golden {
	t.Helper()
	if g.Proposal == nil || g.Proposal.Reader == nil || g.Proposal.Reader.Chart == nil {
		t.Fatal("small fixture must contain a drawable proposal")
	}
	if err := g.Proposal.ValidateReader(); err != nil {
		t.Fatal("small evidence no longer validates", err)
	}
	c := g.Proposal.Reader.Chart
	var b []byte
	var err error
	if cmp := g.Proposal.Reader.Comparison; cmp != nil {
		b, err = json.Marshal(cmp)
		if err != nil {
			t.Fatal(err)
		}
		g.Markdown = cmp.Markdown()
		g.SVG, err = cmp.SVG()
		g.ComparisonFingerprint = cmp.Fingerprint
	} else {
		b, err = json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		g.Markdown = c.Markdown()
		g.SVG, err = c.SVG()
		g.ComparisonFingerprint = ""
	}
	if err != nil {
		t.Fatal(err)
	}
	g.JSON = string(b)
	g.Animated, err = c.Poster(statechart.PosterOptions{Title: g.Proposal.Name, Animated: true})
	if err != nil {
		t.Fatal(err)
	}
	g.Still, err = c.Poster(statechart.PosterOptions{Title: g.Proposal.Name})
	if err != nil {
		t.Fatal(err)
	}
	g.Fingerprint = g.Proposal.Reader.Fingerprint
	return g
}

func TestReader944LargeAdmissionKeepsSmallBytes(t *testing.T) {
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	reader603Workspace(t, ws)
	reader603Write(t, filepath.Join(ws, "proposal.json"), strings.Replace(reader603Draft, `"Limit":"2"`, `"Limit":"500"`, 1))
	p, _ := reader603Checked(t, ws, tc)
	if p.Reader.Chart == nil {
		t.Fatal("first admit the newly supported collapsing graph")
	}
	manifest, err := os.ReadFile("testdata/reader944/manifest.json")
	if err != nil {
		t.Fatal("missing original-base fixture provenance", err)
	}
	var provenance struct {
		BaseRevision string `json:"base_revision"`
	}
	if err := json.Unmarshal(manifest, &provenance); err != nil ||
		!(provenance.BaseRevision == "950fefa6" || len(provenance.BaseRevision) == 40 && strings.HasPrefix(provenance.BaseRevision, "950fefa6")) {
		t.Fatal("fixtures must record unchanged main 950fefa6 as their base", err)
	}
	fresh := reader944SmallFixtures(t)
	for _, name := range []string{"ordinary", "preview", "comparison"} {
		data, err := os.ReadFile("testdata/reader944/" + name + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var g reader944Golden
		if err := json.Unmarshal(data, &g); err != nil {
			t.Fatal(err)
		}
		// Pin both the saved strings and what current production renders from
		// their saved evidence. Recomputing all three also catches changed
		// Compute/preview/comparison output that old decoded charts would hide.
		reader944PinnedBytes(t, name, g)
		reader944PinnedBytes(t, name, reader944Rendered(t, g))
		reader944PinnedAgainst(t, name, reader944Rendered(t, fresh[name]), reader944LayoutHashes(), "main 950fefa6's output with #814's layout pins")
	}
}

func reader944Archive(t *testing.T, r *verify.Report) {
	t.Helper()
	dir := t.TempDir()
	before, _ := json.Marshal(r)
	if err := receipt.Write(dir, r); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			t.Fatal("unexpected nested archive evidence")
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if len(data) > 60<<20 {
			t.Fatalf("%s lacks 4 MiB archive headroom: %d bytes", e.Name(), len(data))
		}
		w, err := zw.Create("projection/" + e.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.UncompressedSize64 > 64<<20 {
			t.Fatal("archive reader would refuse", f.Name)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(rc)
		closeErr := rc.Close()
		if readErr != nil || closeErr != nil {
			t.Fatal(readErr, closeErr)
		}
		if strings.HasSuffix(f.Name, "/receipt.json") {
			var saved verify.Report
			if err := json.Unmarshal(data, &saved); err != nil || saved.Fingerprint != r.Fingerprint || saved.Passed != r.Passed {
				t.Fatal("archive changed verification outcome", err)
			}
		}
	}
	after, _ := json.Marshal(r)
	if !bytes.Equal(before, after) {
		t.Fatal("receipt writing mutated or resealed evidence")
	}
}

// Add internal/formalize/projection_944_integration_test.go in package formalize_test with //go:build integration and
// TestReader944HundredThousandRealIntegration(t) { reader944Real(t) }.
// Existing integration shard discovery must run it, without a skip or a new
// opt-in. Missing pinned tools fail the test rather than skipping it.
func reader944Real(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Minute)
	defer cancel()
	tc, err := toolchain.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	budget, err := tlc.NewBudget(1, 4<<30, 1)
	if err != nil {
		t.Fatal(err)
	}
	if budget.Parallel != 1 || budget.MemoryBytes != 2<<30 || budget.HeapBytes != 1536<<20 {
		t.Fatal("real ceiling test changed the standard resource slot")
	}
	runner := tlc.Runner{Image: tc.JavaImage, Jar: tc.TLCJar, Budget: budget}
	const model = `---- MODULE Ceiling944 ----
EXTENDS Naturals
CONSTANT Limit
VARIABLE n
Init == n = 0
N == n' = IF n < Limit THEN n + 1 ELSE 0
Spec == Init /\ [][N]_n
Safe == n \in 0..Limit
Nonnegative == n >= 0
====
`
	dir := t.TempDir()
	source := filepath.Join(dir, "Ceiling944.tla")
	if err := os.WriteFile(source, []byte(model), 0600); err != nil {
		t.Fatal(err)
	}
	lock := project.Lock{Bounds: map[string]string{"Limit": "99999"}, Statements: []project.Statement{
		{Name: "Spec", Kind: project.Spec, Says: "Count and reset."},
		{Name: "Safe", Kind: project.Invariant, Says: "Stay within the complete bound."},
		{Name: "Nonnegative", Kind: project.Invariant, Says: "Never go below zero."},
	}}
	for i := range lock.Statements {
		s := &lock.Statements[i]
		s.SHA256, err = tla.PinHash(model, s.Name, project.Model)
		if err != nil {
			t.Fatal(err)
		}
	}
	cfg := tlc.Config{Specification: "Spec", Constants: lock.Bounds, Invariants: []string{"Safe", "Nonnegative"}}
	g, err := runner.DumpGraph(ctx, dir, "Ceiling944", cfg)
	if err != nil || g == nil {
		t.Fatalf("real complete 100,000-state dump: %v", err)
	}
	if g.StateCount != 100000 || g.StepCount != 100000 || len(g.Graph.Initial) != 1 || g.Run.DistinctStates != 100000 || g.Run.StatesGenerated != 100001 {
		t.Fatal("real ceiling exploration is incomplete")
	}
	jar, err := os.ReadFile(tc.TLCJar)
	if err != nil {
		t.Fatal(err)
	}
	if g.Tool.Image != toolchain.JavaImage || g.Tool.JarSHA256 != fmt.Sprintf("%x", sha256.Sum256(jar)) || g.Run.Version == "" {
		t.Fatal("real graph lost pinned tool provenance")
	}
	matrix, err := statechart.EvaluateRules(ctx, runner, dir, *g, lock)
	if err != nil {
		t.Fatal("real invariant replay:", err)
	}
	if len(matrix) != 100000 {
		t.Fatal("real replay dropped rows")
	}
	for _, s := range g.Graph.States {
		row := matrix[s.ID]
		if len(row) != 2 || !row["Safe"] || !row["Nonnegative"] {
			t.Fatal("missing or changed invariant row", s.ID)
		}
	}
	view := []byte(`{"version":1,"state":[{"id":"all","when":true,"label":"Counting"}],"step":[{"id":"all","when":true,"label":"Continue"}]}`)
	c, err := statechart.Compute(ctx, statechart.Input{Graph: *g, Lock: lock, Invariants: matrix}, view)
	if err != nil || c == nil || len(c.States) != 1 || len(c.States[0].Members) != 100000 || c.Report.Counts["shown_steps"] != 100000 {
		t.Fatalf("real ceiling projection did not fit: %v", err)
	}
	if c.Report.Source.Tool != g.Tool || c.Report.Source.Module != g.Module {
		t.Fatal("projection changed provenance")
	}
	if _, err := c.SVG(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "Ceiling944.tla" {
		t.Fatal("real dump/replay cleanup incomplete", err)
	}
	got, err := os.ReadFile(source)
	if err != nil || string(got) != model {
		t.Fatal("replay modified source")
	}
	r := &verify.Report{Project: "projection ceiling", ModelOnly: true, Passed: true, Bounds: lock.Bounds, Proposal: project.ProposalHash(lock.Bounds, lock.Statements),
		Design:    verify.Design{Passed: true, Outcome: string(tlc.Passed), Spec: "Spec", Invariants: cfg.Invariants, DistinctStates: 100000, StatesGenerated: 100001, Depth: g.Run.Depth},
		Toolchain: verify.Toolchain{TLC: g.Run.Version, JavaImage: g.Tool.Image, TLCJarSHA256: g.Tool.JarSHA256},
		Reader:    &verify.Reader{Version: 1, Chart: c}}
	r.Fingerprint = verify.Fingerprint(*r)
	reader944Archive(t, r)
	if ctx.Err() != nil {
		t.Fatal(ctx.Err())
	}
}
