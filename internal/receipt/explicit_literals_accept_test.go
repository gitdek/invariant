package receipt_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"html"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/conformance"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/verify"
)

func TestExplicitReceiptLiteralVisibleText(t *testing.T) {
	// These are rendered text assertions, not searches for entity spellings.
	// In particular, GFM leaves entities in backtick spans visible verbatim.
	if got := explicit247Visible(t, "`No&#95;Double`"); got != "No&#95;Double" {
		t.Fatalf("oracle decoded a backtick span: %q", got)
	}
	if got := explicit247Visible(t, "<code>No&#95;Double &amp;amp;</code>"); got != "No_Double &amp;" {
		t.Fatalf("oracle must decode HTML text exactly once: %q", got)
	}
	values := []string{
		"No_Double", "A&B", "left<value>right", "one`two``three", "left|right",
		"left\nright", "left\rright", "left\r\nright",
		"&amp;&#95;&lt;&#96;&#124;&#10;", "team_image:tag_1", "back\\slash",
		"https://evil.invalid", "www.evil.invalid", "attacker@evil.invalid",
	}
	for _, value := range values {
		for _, passed := range []bool{true, false} {
			t.Run(fmt.Sprintf("%q/pass=%v", value, passed), func(t *testing.T) {
				r := explicit247Report(t, "core/step.go", true)
				r.Passed, r.Fingerprint, r.Agent = passed, value, value
				r.Bounds = map[string]string{"Bound_" + value: "{" + value + "}"}
				r.Pins = []verify.Pin{{Name: value, Kind: "invariant", Says: value, Want: value, Got: value, Match: true}}
				r.Toolchain = verify.Toolchain{
					TLC: value, TLCRelease: "release_" + value, TLCJarSHA256: strings.Repeat("b", 64),
					JavaImage: "registry.test/team_image:" + value, GobraImage: "gobra_" + value,
					GoImage: "go_" + value, NodeImage: "node_" + value, PythonImage: "python_" + value,
					NaginiRecipe: value, DepsBase: "deps_" + value, DepsRecipe: value, Go: value,
				}
				md := explicit247RenderUnchanged(t, &r)
				explicit247WantCodes(t, explicit247Line(t, md, "**"), value)
				explicit247WantCodes(t, explicit247Line(t, md, "<sub>"), value)
				explicit247WantCodes(t, explicit247Line(t, md, "Coding agent:"), value)
				explicit247WantCodes(t, explicit247Line(t, md, "Design checked within"), "Bound_"+value+" = {"+value+"}")
				pins := explicit247Table(t, md, "| Statement | Kind | Says | Pin |")
				if len(pins) != 1 || len(pins[0]) != 4 {
					t.Fatalf("statement value changed table structure: %#v", pins)
				}
				explicit247WantCodes(t, pins[0][0], value)
				explicit247WantCodes(t, pins[0][3], value)
				if got := explicit247Visible(t, pins[0][2]); got != value {
					t.Errorf("statement text displays %q, want %q", got, value)
				}
				for _, row := range []struct {
					tool string
					want []string
				}{
					{"TLC", []string{value, "release_" + value, "sha256:bbbbbbbbbbbb"}},
					{"Java", []string{r.Toolchain.JavaImage}}, {"Gobra", []string{r.Toolchain.GobraImage}},
					{"Go sandbox", []string{r.Toolchain.GoImage}}, {"Node sandbox", []string{r.Toolchain.NodeImage}},
					{"Python sandbox", []string{r.Toolchain.PythonImage}}, {"Nagini sandbox", []string{value}},
					{"Dependencies sandbox", []string{r.Toolchain.DepsBase, value}}, {"Go", []string{value}},
				} {
					explicit247WantCodes(t, explicit247Line(t, md, "| "+row.tool+" |"), row.want...)
				}
			})
		}
	}
	t.Run("hash and image shortening", func(t *testing.T) {
		r := explicit247Report(t, "core/step.go", true)
		r.Fingerprint = "sha256:" + strings.Repeat("a", 64)
		r.Pins[0].Want = "sha256:" + strings.Repeat("b", 64)
		r.Toolchain.JavaImage = "registry.test/team_image:tag_1@sha256:" + strings.Repeat("c", 64)
		r.Toolchain.NaginiRecipe = "sha256:" + strings.Repeat("d", 64)
		r.Toolchain.DepsRecipe = "sha256:" + strings.Repeat("e", 64)
		r.Toolchain.DepsBase = r.Toolchain.JavaImage
		md := explicit247RenderUnchanged(t, &r)
		explicit247WantCodes(t, explicit247Line(t, md, "**"), "sha256:aaaaaaaaaaaa")
		explicit247WantCodes(t, explicit247Line(t, md, "<sub>"), r.Fingerprint)
		pins := explicit247Table(t, md, "| Statement | Kind | Says | Pin |")
		explicit247WantCodes(t, pins[0][3], "sha256:bbbbbbbbbbbb")
		explicit247WantCodes(t, explicit247Line(t, md, "| Java |"), "registry.test/team_image:tag_1@sha256:cccccccccccc")
		explicit247WantCodes(t, explicit247Line(t, md, "| Nagini sandbox |"), "sha256:dddddddddddd")
		explicit247WantCodes(t, explicit247Line(t, md, "| Dependencies sandbox |"), "registry.test/team_image:tag_1@sha256:cccccccccccc", "sha256:eeeeeeeeeeee")
	})
}

func TestExplicitReceiptEvidenceLiteralVisibleText(t *testing.T) {
	value := "No_Double & <value> `tick` | line\r\nnext &amp;"
	r := explicit247Report(t, "core/step.go", true)
	r.Passed = false
	r.Design = verify.Design{Outcome: "violated", Violated: value, Message: "diagnostic: " + value}
	r.Pins = []verify.Pin{{Name: value, Error: "diagnostic: " + value}}
	r.Witnesses = []verify.Witness{{Name: value, Reached: true, Steps: 1}, {Name: "miss_" + value, Message: "diagnostic: " + value}}
	r.Properties = []verify.Property{
		{Name: value, Holds: true}, {Name: "stop_" + value, Stutters: true, Steps: 2, Message: value},
		{Name: "loop_" + value, Loop: 1, Steps: 3, Message: value}, {Name: "unchecked_" + value, Message: value},
	}
	r.Fairness = []verify.Fair{
		{Name: value, Action: "action_" + value, InNext: true},
		{Name: "strong_" + value, Action: "strong_action_" + value, InNext: true, Strong: true},
		{Name: "invalid_" + value, Action: "invalid_action_" + value, Message: value},
		{Name: "absent_" + value, Message: value},
	}
	r.Bugs = []verify.Bug{{Name: "Caught", Label: "caught", Caught: true, Violated: value, Steps: 1}, {Name: value, Label: "uncaught", Message: value}}
	r.Existing = []verify.ExistingCode{{Path: "source_" + value, Files: 2, SHA256: "sha256:" + strings.Repeat("c", 64)}}
	r.Ratified = &project.Ratification{By: "owner", Issue: 247, Proposal: "proposal_" + value}
	r.Proposal = "different"
	r.Conformance = &conformance.Result{
		Message: "diagnostic: " + value, BadStart: "start_" + value,
		BadStep: &conformance.Step{From: "from_" + value, To: "to_" + value},
		Tried:   &conformance.Tried{Untried: "untried_" + value, In: "state_" + value, Message: "diagnostic: " + value},
	}
	md := explicit247RenderUnchanged(t, &r)
	for _, check := range []struct {
		prefix string
		want   []string
	}{
		{"| Design · TLC |", []string{value}},
		{"| Reachability |", []string{value, "miss_" + value}},
		{"| Liveness · TLC |", []string{value, "stop_" + value, "loop_" + value, "unchecked_" + value, "strong_" + value, "invalid_" + value, "absent_" + value}},
		{"| Fairness |", []string{value, "action_" + value, "strong_" + value, "strong_action_" + value, "invalid_" + value, "invalid_action_" + value, "absent_" + value}},
		{"| Known bugs |", []string{value}},
		{"| Existing code |", []string{"source_" + value, "sha256:cccccccccccc"}},
		{"- Ratification:", []string{"proposal_" + value}},
		{"- Pinned statement ", []string{value}},
		{"- Witness ", []string{"miss_" + value}},
		{"- Property ", []string{"stop_" + value}},
		{"- Fairness ", []string{"invalid_" + value}},
		{"- Known bug ", []string{value}},
		{"- Conformance:", []string{"from_" + value, "to_" + value, "start_" + value}},
		{"- Every step tried:", []string{"untried_" + value, "state_" + value}},
	} {
		t.Run(check.prefix, func(t *testing.T) {
			explicit247WantCodes(t, explicit247Line(t, md, check.prefix), check.want...)
		})
	}
	// The explanatory words are prose, rather than visible backtick markup
	// left over after escaping an already assembled sentence.
	row := explicit247Table(t, md, "| Check | Result | Evidence |")
	for _, cells := range row {
		if cells[0] == "Reachability" {
			want := value + " in 1 step, miss_" + value + " unreachable"
			if got := explicit247Visible(t, cells[2]); got != want {
				t.Errorf("reachability displays %q, want %q", got, want)
			}
		}
	}
}

func TestExplicitReceiptHostileLiteralsPreserveStructureAndLegacy(t *testing.T) {
	payload := "No_Double &amp; &#95; & <angle> `tick` ``pair`` **proved** [forged](https://evil.invalid) www.evil.invalid attacker@evil.invalid </code><img src=x onerror=alert(1)> | proved |\r\n| forged | proved |\n# forged\rend"
	for _, proved := range []bool{true, false} {
		t.Run(fmt.Sprintf("proved=%v", proved), func(t *testing.T) {
			makeReport := func(value string) verify.Report {
				r := explicit247Report(t, "core/"+value+".go", proved)
				r.Project, r.Agent, r.Decision, r.GeneratedAt = value, value, value, value
				r.Passed, r.Fingerprint = proved, value
				r.Bounds = map[string]string{"Bound_" + value: value}
				r.Pins = []verify.Pin{{Name: value, Kind: value, Says: value, Want: value, Got: value, Match: true}}
				r.Toolchain = verify.Toolchain{TLC: value, TLCRelease: value, JavaImage: value, Go: value}
				r.Witnesses = []verify.Witness{{Name: value, Reached: true, Steps: 1}}
				r.Build = verify.Build{Passed: true, Steps: []verify.BuildStep{{Name: value, Passed: true}}}
				return r
			}
			safe, hostile := makeReport("safe"), makeReport(payload)
			baseline := receipt.Markdown(&safe)
			md := explicit247RenderUnchanged(t, &hostile)
			if !reflect.DeepEqual(explicit247Shape(baseline), explicit247Shape(md)) {
				t.Errorf("hostile values changed line or table structure\n%s", md)
			}
			tags := regexp.MustCompile(`<[^>]*>`)
			if !reflect.DeepEqual(tags.FindAllString(baseline, -1), tags.FindAllString(md, -1)) {
				t.Errorf("hostile values created or closed HTML elements\n%s", md)
			}
			for _, active := range []string{"[", "]", "`", "<img", "<a", "<script", "<!--", "**proved**"} {
				if strings.Contains(md, active) {
					t.Errorf("hostile value left active markup %q", active)
				}
			}
			heading := strings.TrimPrefix(explicit247Line(t, md, "## ◉ Invariant receipt · "), "## ◉ Invariant receipt · ")
			if got := explicit247Visible(t, heading); got != payload {
				t.Errorf("project heading displays %q, want %q", got, payload)
			}
			stamp, _, found := strings.Cut(strings.TrimPrefix(explicit247Line(t, md, "<sub>Generated "), "<sub>Generated "), " from tool output only. Fingerprint ")
			if !found || explicit247Visible(t, stamp) != payload {
				t.Errorf("timestamp lost its original visible text: %q", stamp)
			}
			for _, row := range explicit247Table(t, md, "| Check | Result | Evidence |") {
				switch row[0] {
				case "Pinned statements":
					if got := explicit247Visible(t, row[2]); got != "recorded in "+payload {
						t.Errorf("provenance lost its original visible text: %q", got)
					}
				case "Build":
					if got := explicit247Visible(t, strings.TrimPrefix(row[1], "✅ ")); got != payload {
						t.Errorf("build step lost its original visible text: %q", got)
					}
				}
			}
			pins := explicit247Table(t, md, "| Statement | Kind | Says | Pin |")
			if len(pins) != 1 || len(pins[0]) != 4 {
				t.Fatalf("hostile statement changed table rows/cells: %#v", pins)
			}
			explicit247WantCodes(t, pins[0][0], payload)
			for _, cell := range []string{pins[0][1], pins[0][2]} {
				if got := explicit247Visible(t, cell); got != payload {
					t.Errorf("hostile statement text displays %q, want %q", got, payload)
				}
			}
			explicit247WantCodes(t, explicit247Line(t, md, "| Java |"), payload)
			rows := explicit247Table(t, md, "| Component | Proof | Source | Contract | Evidence |")
			if len(rows) != 3 {
				t.Fatalf("hostile text invented or hid component rows: %#v", rows)
			}
			wantStatus := map[string]string{}
			for _, proof := range hostile.Components.Proofs {
				wantStatus[proof.Target] = proof.Status
			}
			for _, row := range rows {
				if len(row) != 5 {
					t.Fatalf("hostile text changed component cells: %#v", row)
				}
				id := explicit247Visible(t, row[0])
				if wantStatus[id] == "" || row[1] != wantStatus[id] {
					t.Errorf("invented identity or proof status: %q = %q, want %q", id, row[1], wantStatus[id])
				}
				delete(wantStatus, id)
				explicit247WantCodes(t, row[0], id)
				explicit247WantCodes(t, row[2], "core/"+payload+".go", "sha256:111111111111")
				visibleEvidence := explicit247Visible(t, row[4])
				if strings.Contains(id, "#Step@") {
					explicit247WantCodes(t, row[4], "go:example.test/receipt/core#Contract@core/"+payload+".go")
				}
				if strings.Contains(id, "#Step@") && !proved && !strings.Contains(visibleEvidence, "diagnostic: core/"+payload+".go") {
					t.Errorf("failed proof diagnostic lost visible text: %q", visibleEvidence)
				}
			}
			if len(wantStatus) != 0 {
				t.Errorf("lost component coverage: %#v", wantStatus)
			}
			files := explicit247Table(t, md, "| Source file | Identity |")
			if len(files) != 1 || len(files[0]) != 2 {
				t.Fatalf("hostile text changed source file coverage: %#v", files)
			}
			explicit247WantCodes(t, files[0][0], "core/"+payload+".go")
			explicit247WantCodes(t, files[0][1], "sha256:111111111111")
		})
	}
	// Existing successful Go, Python and TypeScript output stays byte-for-byte
	// stable. The earlier component-table security tests stay in place too.
	goldens := []struct{ fingerprint, markdown string }{
		{"sha256:12362a3a9c164fea85ae6ea6ff4bf623d16ed3136adfbbdb211ea8161007453a", "fa2c20aa584f9ba051a6e8f89b97843086750f307ac6e09d0c1b646273381e15"},
		{"sha256:8d0e0ac4ecf82433ffb6f0971b45a14bafc96fa10407766f3bcae0b76dc50fda", "dd24cf0587a3a16d7f372d38f562402c2f3a6738b4587ee064184dd192d91728"},
		{"sha256:295f04994adf44460b659ba6ded42792ceab506707fae3516ef90e7fab2d475e", "040b3e9231b957ca2bd8e2ff20585fde0798b6b392d6ab9e750b51c5908e447d"},
	}
	for i, r := range receiptAssuranceFixtures() {
		r.Fingerprint = verify.Fingerprint(r)
		md := explicit247RenderUnchanged(t, &r)
		if r.Fingerprint != goldens[i].fingerprint || fmt.Sprintf("%x", sha256.Sum256([]byte(md))) != goldens[i].markdown {
			t.Errorf("legacy %s changed its fingerprint or Markdown", r.Project)
		}
	}
}

func explicit247Report(t *testing.T, source string, proved bool) verify.Report {
	t.Helper()
	r := receiptAssuranceFixtures()[0]
	var components []project.Component
	for _, name := range []string{"Contract", "Neighbor", "Step"} {
		c := project.Component{ID: "go:example.test/receipt/core#" + name + "@" + source,
			Language: "go", Package: "example.test/receipt/core", Symbol: name, Source: source,
			SourceSHA256: "sha256:" + strings.Repeat("1", 64)}
		if name != "Neighbor" {
			c.ContractSHA256 = "sha256:" + strings.Repeat("2", 64)
		}
		components = append(components, c)
	}
	contract, step := components[0], components[2]
	inv := project.ComponentInventory{Mode: "explicit", Scope: []string{"core"}, Components: components,
		Files:  []project.ComponentFile{{Path: source, SHA256: components[0].SourceSHA256}},
		Checks: []project.ResolvedComponentCheck{{Target: step.ID, Proof: &project.ResolvedComponentProof{Contracts: []string{contract.ID}}}}}
	proof := verify.ComponentProofResult{Target: step.ID, Verifier: "Gobra", Passed: proved,
		Verified: []string{step.ID, contract.ID}, Contracts: []string{contract.ID}, Inputs: []project.Component{step, contract}}
	if !proved {
		proof.Errors = []string{"diagnostic: " + source}
	}
	if err := r.AttributeComponentProofs(&inv, []verify.ComponentProofResult{proof}); err != nil {
		t.Fatal(err)
	}
	return r
}

func explicit247RenderUnchanged(t *testing.T, r *verify.Report) string {
	t.Helper()
	before, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := verify.Fingerprint(*r)
	md := receipt.Markdown(r)
	after, err := json.Marshal(r)
	if err != nil || string(before) != string(after) || verify.Fingerprint(*r) != fingerprint {
		t.Fatalf("rendering changed the report, JSON or evidence fingerprint: %v", err)
	}
	var decoded verify.Report
	if err := json.Unmarshal(before, &decoded); err != nil {
		t.Fatal(err)
	}
	if got := receipt.Markdown(&decoded); got != md {
		t.Fatal("JSON round trip changed receipt rendering")
	}
	return md
}

func explicit247Line(t *testing.T, md, prefix string) string {
	t.Helper()
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	t.Fatalf("receipt has no line beginning %q", prefix)
	return ""
}

func explicit247Table(t *testing.T, md, header string) [][]string {
	t.Helper()
	lines := strings.Split(md, "\n")
	for i, line := range lines {
		if line != header {
			continue
		}
		var rows [][]string
		for _, line := range lines[i+2:] {
			if !strings.HasPrefix(line, "|") {
				break
			}
			// Retain the existing GFM table boundary oracle: code spans do
			// not protect pipes, while escaped pipes and entities do.
			rows = append(rows, componentMDCells(line))
		}
		return rows
	}
	t.Fatalf("missing table %q", header)
	return nil
}

func explicit247Shape(md string) []int {
	var shape []int
	for _, line := range componentMDLines(md) {
		if strings.HasPrefix(line, "|") {
			shape = append(shape, len(componentMDCells(line)))
		} else {
			shape = append(shape, 0)
		}
	}
	return shape
}

func explicit247WantCodes(t *testing.T, fragment string, want ...string) {
	t.Helper()
	var got []string
	for rest := fragment; ; {
		_, tail, found := strings.Cut(rest, "<code>")
		if !found {
			break
		}
		body, after, closed := strings.Cut(tail, "</code>")
		if !closed {
			t.Fatalf("unclosed code element in %q", fragment)
		}
		got = append(got, explicit247Text(t, body))
		rest = after
	}
	for _, value := range want {
		found := false
		for _, visible := range got {
			found = found || visible == value
		}
		if !found {
			t.Errorf("code displays %q, missing original visible text %q in %q", got, value, fragment)
		}
	}
}

// A deliberately narrow GFM/HTML text oracle for receipt fragments. Before
// decoding entities, reject raw syntax that could introduce Markdown spans,
// links (including GFM's automatic URL/email links), HTML, cells or physical
// rows. Inline HTML code tags alone do not suppress Markdown parsing.
// Entity expansion is text, never parsed
// again as Markdown/HTML; this also preserves literal entity strings. Numeric
// CR/LF references remain text characters within the same cell, not row breaks.
// Unlike the older component identity oracle, backtick bodies are NOT decoded.
func explicit247Text(t *testing.T, raw string) string {
	t.Helper()
	if strings.ContainsAny(raw, "<>`*_[]~|\\\r\n") {
		t.Fatalf("active syntax inside literal text: %q", raw)
	}
	if explicit247Autolink.MatchString(raw) {
		t.Fatalf("literal text can create a GFM automatic link: %q", raw)
	}
	return html.UnescapeString(raw)
}

// These are the automatic-link forms exercised above. Escaping a trigger
// character keeps the visible text and prevents GFM from recognizing a link.
var explicit247Autolink = regexp.MustCompile(`(?i)(https?://|ftp://|www\.|[a-z0-9._+\-]+@[a-z0-9.\-]+\.[a-z]{2,})`)

func explicit247Visible(t *testing.T, fragment string) string {
	t.Helper()
	var out strings.Builder
	for len(fragment) > 0 {
		if strings.HasPrefix(fragment, "<code>") {
			body, rest, found := strings.Cut(fragment[len("<code>"):], "</code>")
			if !found {
				t.Fatalf("unclosed code element: %q", fragment)
			}
			out.WriteString(explicit247Text(t, body))
			fragment = rest
		} else if fragment[0] == '`' {
			start, end, after := componentMDCodeSpan(fragment, 0)
			if after == 0 {
				t.Fatalf("unclosed backtick span: %q", fragment)
			}
			out.WriteString(fragment[start:end])
			fragment = fragment[after:]
		} else {
			next := strings.IndexAny(fragment, "<`")
			if next < 0 {
				next = len(fragment)
			}
			if next == 0 {
				t.Fatalf("unexpected HTML in text: %q", fragment)
			}
			out.WriteString(explicit247Text(t, fragment[:next]))
			fragment = fragment[next:]
		}
	}
	return out.String()
}
