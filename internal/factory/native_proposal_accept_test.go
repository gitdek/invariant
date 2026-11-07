package factory

import (
	"context"
	"encoding/json"
	"html"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/plumbing"
)

func proposal503(t *testing.T) *formalize.Proposal {
	t.Helper()
	const text = `{"name":"native identities","summary":"Pin the exact tests.","files":["worker/main.py","web/main.ts"],"tests":[{"name":"a label","part":"worker","framework":"pytest","file":"worker/test_accept.py","selector":"test_accept.py::test_ready[one case]","says":"The worker is ready."},{"name":"a label","part":"web","framework":"vitest","file":"web/ready.test.ts","selector":"checkout handles a case","says":"The page is ready."}],"sources":{"worker/test_accept.py":"def test_ready():\r\n    assert False\r\n","web/ready.test.ts":"it('checkout handles a case', () => {});\n"},"execution":{"base":"0123456789012345678901234567890123456789","digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}`
	var p plumbing.Plan
	if err := json.Unmarshal([]byte(text), &p); err != nil {
		t.Fatal(err)
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	return &formalize.Proposal{Draft: formalize.Draft{Name: p.Name}, Plan: &p, Hash: p.Hash()}
}

func TestNativeProposalShowsIdentityLanguageAndSafeNames(t *testing.T) {
	p := proposal503(t)
	// Labels are free text. Code delimiters and HTML in them cannot become
	// markup. The source remains a language-tagged code block.
	p.Plan.Tests[0].Name = "label ` <img src=x onerror=alert(1)> |"
	p.Hash = p.Plan.Hash()
	m := Marker{Kind: KindProposal, Proposal: p}
	text := planComment(p, m)
	visible := strings.Split(text, "<!-- invariant:")[0]
	rendered := html.UnescapeString(visible)
	for _, test := range p.Plan.Tests {
		for _, want := range []string{test.Name, test.Part, test.Framework, test.File, test.Selector, test.Says} {
			if !strings.Contains(rendered, "<code>"+want+"</code>") {
				t.Fatalf("proposal hides literal %q: %s", want, visible)
			}
		}
	}
	for _, want := range []string{"```python", "```typescript"} {
		if !strings.Contains(visible, want) {
			t.Fatalf("proposal hides %q: %s", want, visible)
		}
	}
	if strings.Contains(visible, "<img src=x") {
		t.Fatal("native display label became HTML")
	}
	decoded, ok := DecodeMarker(text)
	if !ok || decoded.Proposal == nil || !reflect.DeepEqual(decoded.Proposal.Plan, p.Plan) || decoded.Proposal.Hash != p.Hash {
		t.Fatal("proposal marker lost pinned metadata or source")
	}
	for _, test := range p.Plan.Tests {
		prompt := plumbing.BuildPrompt(p.Plan, 503, 4)
		for _, want := range []string{test.Part, test.Framework, test.File, test.Selector} {
			if !strings.Contains(prompt, want) {
				t.Fatalf("builder cannot see identity %q", want)
			}
		}
	}
}

type comments503 struct{ body string }

func (c comments503) Comment(context.Context, int64) (github.Comment, error) {
	return github.Comment{Body: c.body, User: github.User{Login: "owner", Type: "User"}, URL: "https://github.com/o/r/issues/503#issuecomment-9", IssueURL: "https://api.github.com/repos/o/r/issues/503"}, nil
}
func (comments503) Permission(context.Context, string) (string, error) { return "write", nil }

func TestNativeSavedProposalAndRatificationRetainSelectors(t *testing.T) {
	p := proposal503(t)
	// This is the same JSON envelope saved in result.json and passed as the
	// previous proposal for a revision. No native identity may be dropped.
	before := formalize.Result{Proposal: p}
	data, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	var after formalize.Result
	if err := json.Unmarshal(data, &after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after.Proposal.Plan, p.Plan) || after.Proposal.Plan.Hash() != p.Hash {
		t.Fatal("saved proposal changed contract")
	}
	lock := plumbing.Lock{Plan: *after.Proposal.Plan, Ratified: plumbing.Ratification{By: "owner", Issue: 503, Comment: "https://github.com/o/r/issues/503#issuecomment-9", Proposal: p.Hash}}
	comments := comments503{body: "/invariant ratify " + strings.TrimPrefix(p.Hash, "sha256:")}
	if err := VerifyPlan(context.Background(), comments, "o/r", lock); err != nil {
		t.Fatal(err)
	}
	lock.Plan.Tests = append([]plumbing.Test(nil), lock.Plan.Tests...)
	lock.Plan.Tests[0].Selector += " changed"
	if err := VerifyPlan(context.Background(), comments, "o/r", lock); err == nil {
		t.Fatal("old ratification approved a different native selector")
	}
}
