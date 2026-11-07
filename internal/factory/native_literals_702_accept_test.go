package factory

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/plumbing"
)

func TestNativeRatificationTableKeepsLiteralValuesAndContract(t *testing.T) {
	// Calibrate the oracle against actual GFM hazards. Entity-encoding an
	// @ or # outside a code context is not protection from GitHub references.
	for _, unsafe := range []string{"@user #123", "&#64;user &#35;123", "<code>www.evil.invalid</code>", "<code><img src=x></code>", "`before\\`**active**`after`"} {
		if _, tail, ok := literal702(unsafe); ok && tail == "" {
			t.Fatalf("literal oracle accepted active text: %q", unsafe)
		}
	}
	if cells := cells702("| `x|forged|y` | safe |"); len(cells) != 4 {
		t.Fatal("table oracle let code delimiters protect raw pipes")
	}
	if got, rest, ok := literal702("<code>&amp;#64;user &#64;org/team &#35;123</code>"); !ok || rest != "" || got != "&#64;user @org/team #123" {
		t.Fatal("literal oracle must decode entities exactly once inside code")
	}
	if got, rest, ok := literal702("`` ` &amp; ``"); !ok || rest != "" || got != "` &amp;" {
		t.Fatal("literal oracle must not decode entities inside backtick spans")
	}

	for i, label := range []string{
		"@user @org/team #123 owner/repository#123",
		"` @user | #123 `` @org/team \\` owner/repository#123 ```",
		"\\| \\\\| \\\\\\| **bold** _emphasis_ ~~strike~~ [link](https://evil.invalid)",
		"</code><img src=x onerror='alert(1)'><script>bad</script><!-- @user #123 -->",
		"&amp; &#64;user &#35;123 &lt;code&gt; &#96; &#124; &quot; \"double\" 'single'",
		"HTTP://evil.invalid https://evil.invalid FTP://evil.invalid www.evil.invalid a+b@evil.invalid",
		"  leading café 界 trailing  ",
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			p := proposal702(t, label)
			before, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			hash := p.Hash
			body := planComment(p, Marker{Kind: KindProposal, Proposal: p, Hash: hash})
			visible, _, found := strings.Cut(body, "<!-- invariant:")
			if !found {
				t.Fatal("comment lost its hidden marker")
			}
			rows := table702(t, visible)
			if len(rows) != len(p.Plan.Tests) {
				t.Fatalf("table has %d records, want %d", len(rows), len(p.Plan.Tests))
			}
			for j, test := range p.Plan.Tests {
				row := rows[j]
				if len(row) != 2 {
					t.Fatalf("record %d split into %d cells: %#v", j, len(row), row)
				}
				rest := row[0]
				for k, value := range []string{test.Name, test.Part, test.Framework, test.File, test.Selector} {
					got, tail, ok := literal702(rest)
					if !ok || got != value {
						t.Fatalf("record %d field %d escaped code context or changed visible text: %q, want %q (cell %q)", j, k, got, value, rest)
					}
					rest = tail
					if k < 4 {
						separator := []string{" — part ", "; framework ", "; file ", "; selector "}[k]
						if !strings.HasPrefix(rest, separator) {
							t.Fatalf("identity lost its field explanation: %q", rest)
						}
						rest = strings.TrimPrefix(rest, separator)
					}
				}
				if rest != "" {
					t.Fatalf("record %d has extra identity content: %q", j, rest)
				}
				if got, tail, ok := literal702(row[1]); !ok || tail != "" || got != test.Says {
					t.Fatalf("assertion escaped code context or changed text: %q, want %q (cell %q)", got, test.Says, row[1])
				}
			}

			// Prose still carries authored Markdown and ordinary links. Only
			// native table values acquire the new literal presentation.
			for _, want := range []string{
				"Here's my plan for **" + p.Plan.Name + "**.",
				"**What changes.** " + p.Plan.Summary,
				"Captured execution: " + p.Plan.Execution.Digest + ". Base provenance: " + p.Plan.Execution.Base + ".",
				"To ratify exactly this, comment `/invariant ratify " + strings.TrimPrefix(hash, "sha256:")[:12] + "`.",
				"comment `/invariant revise`.",
			} {
				if !strings.Contains(visible, want) {
					t.Errorf("comment changed or omitted %q", want)
				}
			}
			for _, file := range p.Plan.TestFiles() {
				checkSource702(t, visible, file, p.Plan.SourceLanguage(file), p.Plan.Sources[file])
			}
			after, err := json.Marshal(p)
			if err != nil || string(after) != string(before) || p.Hash != hash || p.Plan.Hash() != hash {
				t.Fatalf("rendering changed the raw proposal or hash: %v", err)
			}
			decoded, ok := DecodeMarker(body)
			if !ok || decoded.Hash != hash || !reflect.DeepEqual(decoded.Proposal, p) {
				t.Fatal("hidden marker changed labels, assertions, identities, execution or source bytes")
			}
			saved, err := json.Marshal(formalize.Result{Proposal: decoded.Proposal})
			if err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "result.json"), saved, 0o600); err != nil {
				t.Fatal(err)
			}
			saved, err = os.ReadFile(filepath.Join(root, "result.json"))
			if err != nil {
				t.Fatal(err)
			}
			var result formalize.Result
			if err := json.Unmarshal(saved, &result); err != nil || !reflect.DeepEqual(result.Proposal, p) || result.Proposal.Plan.Hash() != hash {
				t.Fatalf("saved proposal changed the contract: %v", err)
			}
			lock := plumbing.Lock{Plan: *result.Proposal.Plan, Ratified: plumbing.Ratification{
				By: "owner", Issue: 702, Proposal: hash, Comment: "https://github.com/o/r/issues/702#issuecomment-9",
			}}
			if err := lock.Write(root); err != nil {
				t.Fatal(err)
			}
			loaded, err := plumbing.ReadLockFile(root, 702)
			if err != nil || !reflect.DeepEqual(loaded, &lock) || loaded.Plan.Hash() != hash {
				t.Fatalf("lock round trip changed the contract: %v", err)
			}
			for file, source := range p.Plan.Sources {
				data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
				if err != nil || string(data) != source {
					t.Fatalf("ratified source %s changed: %v", file, err)
				}
			}
			if err := VerifyPlan(context.Background(), comments702{hash: hash}, "o/r", *loaded); err != nil {
				t.Fatalf("original ratification no longer verifies: %v", err)
			}
		})
	}
}

func proposal702(t *testing.T, label string) *formalize.Proposal {
	t.Helper()
	plan := &plumbing.Plan{
		Name:      "Literal native records",
		Summary:   "Keep **authored prose**, @owner, #510 and [the discussion](https://example.invalid/discussion) readable.",
		Files:     []string{"app/main.go", "web/main.ts", "worker/main.py"},
		Execution: &plumbing.Execution{Base: strings.Repeat("1", 40), Digest: "sha256:" + strings.Repeat("a", 64)},
		Sources: map[string]string{
			"app/literal_accept_test.go":             "package app\nimport \"testing\"\n// @user #123 ` `` ``` ```` | &amp; <img src=x>\nfunc TestLiteral702(t *testing.T) {}\n",
			"worker/test_literal.py":                 "def test_literal():\r\n    # @user @org/team #123 owner/repo#123 ``` ```` </details>\r\n    assert False\r\n",
			"web/@user #123 `ticks` | &amp;.test.ts": "// @user #123 | &amp; <img src=x> ``` ````\nit('literal', () => {});",
			"web/literal.test.js":                    "// @org/team owner/repo#123 ``` ````\nit('literal', () => {});\n",
		},
	}
	for i, entry := range []struct{ framework, file, selector string }{
		{"go", "app/literal_accept_test.go", "TestLiteral702/case @user #123 `ticks` | &amp;"},
		{"pytest", "worker/test_literal.py", "test_literal.py::test_literal[" + label + "]"},
		{"vitest", "web/@user #123 `ticks` | &amp;.test.ts", "suite " + label},
		{"jest", "web/literal.test.js", "suite " + label},
	} {
		plan.Tests = append(plan.Tests, plumbing.Test{
			Name: label, Part: "part " + label, Framework: entry.framework, File: entry.file, Selector: entry.selector,
			Says: fmt.Sprintf("Record %d: %s\rCR\nLF\r\nCRLF | @user @org/team #123 owner/repository#123 end", i, label),
		})
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("invalid native presentation fixture: %v", err)
	}
	return &formalize.Proposal{Draft: formalize.Draft{Name: plan.Name}, Plan: plan, Hash: plan.Hash()}
}

// This is a deliberately narrow, independent GFM oracle, following the
// receipt tests. GitHub's reference postprocessing excludes code elements;
// plain entity-encoded text is deliberately not accepted here. Inline HTML
// still needs protection from Markdown and GFM automatic links.
var autolink702 = regexp.MustCompile(`(?i)(https?://|ftp://|www\.|[a-z0-9._+\-]+@[a-z0-9.\-]+\.[a-z]{2,})`)

func literal702(raw string) (visible, rest string, ok bool) {
	if strings.HasPrefix(raw, "<code>") {
		body, tail, closed := strings.Cut(raw[len("<code>"):], "</code>")
		if !closed || strings.ContainsAny(body, "<>`*_[]~|\\\r\n") || autolink702.MatchString(body) {
			return "", raw, false
		}
		return html.UnescapeString(body), tail, true
	}
	if !strings.HasPrefix(raw, "`") {
		return "", raw, false
	}
	open := len(raw) - len(strings.TrimLeft(raw, "`"))
	for i := open; i < len(raw); {
		if raw[i] != '`' {
			i++
			continue
		}
		end := i
		for end < len(raw) && raw[end] == '`' {
			end++
		}
		if end-i == open {
			body := raw[open:i]
			if strings.ContainsAny(body, "\r\n") {
				return "", raw, false
			}
			// CommonMark strips one padding space at both ends unless the
			// contents are all spaces. Entities stay literal in code spans.
			if strings.HasPrefix(body, " ") && strings.HasSuffix(body, " ") && strings.Trim(body, " ") != "" {
				body = body[1 : len(body)-1]
			}
			return body, raw[end:], true
		}
		i = end
	}
	return "", raw, false
}

// Split cells before interpreting spans; backticks cannot protect a pipe.
func cells702(line string) []string {
	var cells []string
	var cell strings.Builder
	for i := 0; i < len(line); i++ {
		switch {
		case line[i] == '\\' && i+1 < len(line):
			if line[i+1] != '|' {
				cell.WriteByte('\\')
			}
			i++
			cell.WriteByte(line[i])
		case line[i] == '|':
			cells = append(cells, strings.TrimSpace(cell.String()))
			cell.Reset()
		default:
			cell.WriteByte(line[i])
		}
	}
	if len(cells) == 0 {
		return nil
	}
	cells = append(cells, strings.TrimSpace(cell.String()))
	if cells[0] == "" {
		cells = cells[1:]
	}
	if len(cells) > 0 && cells[len(cells)-1] == "" {
		cells = cells[:len(cells)-1]
	}
	return cells
}

func table702(t *testing.T, md string) [][]string {
	t.Helper()
	_, after, found := strings.Cut(md, "| Test | Shows |\n| :-- | :-- |\n")
	if !found {
		t.Fatal("missing acceptance table")
	}
	table, _, found := strings.Cut(after, "\n\n")
	if !found || strings.Contains(table, "\r") {
		t.Fatalf("acceptance table has broken physical rows: %q", table)
	}
	var rows [][]string
	for _, line := range strings.Split(table, "\n") {
		if !strings.HasPrefix(line, "|") || !strings.HasSuffix(line, "|") {
			t.Fatalf("non-table line inside acceptance table: %q", line)
		}
		rows = append(rows, cells702(line))
	}
	return rows
}

func checkSource702(t *testing.T, md, file, language, source string) {
	t.Helper()
	_, rest, found := strings.Cut(md, "<summary><code>"+html.EscapeString(file)+"</code></summary>\n\n")
	if !found {
		t.Fatalf("missing source heading for %s", file)
	}
	opener, rest, found := strings.Cut(rest, "\n")
	fence := strings.TrimSuffix(opener, language)
	if !found || fence == opener || len(fence) < 3 || strings.Trim(fence, "`") != "" || strings.Contains(source, fence) {
		t.Fatalf("unsafe or untagged source fence for %s: %q", file, opener)
	}
	want := source
	if !strings.HasSuffix(want, "\n") {
		want += "\n"
	}
	if !strings.HasPrefix(rest, want+fence+"\n\n</details>") {
		t.Fatalf("verbatim source block changed for %s", file)
	}
}

// Pure in-memory GitHub reader: no comment is sent and no network is used.
type comments702 struct{ hash string }

func (c comments702) Comment(context.Context, int64) (github.Comment, error) {
	return github.Comment{Body: "/invariant ratify " + strings.TrimPrefix(c.hash, "sha256:"),
		User: github.User{Login: "owner", Type: "User"}, URL: "https://github.com/o/r/issues/702#issuecomment-9",
		IssueURL: "https://api.github.com/repos/o/r/issues/702"}, nil
}

func (comments702) Permission(context.Context, string) (string, error) { return "write", nil }
