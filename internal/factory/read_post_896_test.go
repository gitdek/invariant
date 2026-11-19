package factory

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/github"
)

// ReadPost is the factory's own rule for reading its posts, which the
// dashboard shares so that it shows an issue the factory has stopped on as
// stopped (#896). markPost's refusals keep their words exactly.
func TestReadPostIsTheFactorysRule896(t *testing.T) {
	const bot = "invariant-code-factory[bot]"
	f := &Factory{Self: bot}
	whole := post("proposal for ratification", "Here's what I propose.", Marker{Kind: KindProposal})
	cut := whole[:strings.Index(whole, markerStart)]
	broken := header + "proposal for ratification\n\n" + `<!-- invariant:` + base64JSON(t, `{"kind":"proposal","proposal_ref":{"version":1`) + ` -->`
	for _, c := range []struct {
		name, body string
		own, ok    bool
		why        string
	}{
		{"whole", whole, true, true, ""},
		{"cut before its marker", cut, true, false, "the factory's marker is cut short or damaged"},
		{"cut, read by its markers alone", cut, false, false, ""},
		{"a broken saved reference", broken, true, false, "draft reference: malformed marker: "},
		{"no post", "Thanks, looks right.", true, false, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			m, ok, bad := ReadPost(c.body, c.own)
			if ok != c.ok || (c.ok && m.Kind != KindProposal) {
				t.Fatalf("read %+v, %v; want a post: %v", m, ok, c.ok)
			}
			if c.why == "" {
				if bad != nil {
					t.Fatalf("unreadable %+v; want none", bad)
				}
				return
			}
			if bad == nil || !strings.HasPrefix(bad.Why, c.why) || bad.Repair == "" {
				t.Fatalf("unreadable %+v; want why %q and a repair", bad, c.why)
			}
			if !c.own {
				return
			}
			_, _, err := f.markPost(github.Comment{ID: 42, Body: c.body, User: github.User{Login: bot, Type: "Bot"}})
			if err == nil || err.Error() != "comment 42: "+bad.Why+"; "+bad.Repair {
				t.Fatalf("markPost says %v; want the shared rule's words", err)
			}
		})
	}
	// The exact words markPost used before the rule was shared.
	_, _, err := f.markPost(github.Comment{ID: 7, Body: cut, User: github.User{Login: bot, Type: "Bot"}})
	if err == nil || err.Error() != "comment 7: the factory's marker is cut short or damaged; restore the comment, or delete it to fall back to the post before it" {
		t.Fatalf("markPost's refusal changed: %v", err)
	}
}

func base64JSON(t *testing.T, s string) string {
	t.Helper()
	var raw json.RawMessage
	if json.Unmarshal([]byte(s), &raw) == nil {
		t.Fatalf("%s should be cut short", s)
	}
	return base64.StdEncoding.EncodeToString([]byte(s))
}
