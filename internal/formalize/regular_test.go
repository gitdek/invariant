package formalize

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/regular"
)

// A draft is read from regular files only (#153). Its proposal, its model
// or a plan of issues, left as a link to a file on the host that holds a
// draft of its own and a secret, standing in for the factory's key, is
// refused by name, and the file's text never reaches what's read.
func TestADraftTakesNoLinkFromItsWorkspace(t *testing.T) {
	const secret = "-----BEGIN PRIVATE KEY----- stand-in"
	for _, name := range []string{"proposal.json", "BoundedBuffer.tla", "issues.json"} {
		for _, link := range []func(string, string) error{os.Symlink, os.Link} {
			ws := workspace(t, nil)
			at := filepath.Join(ws, name)
			// What the host's file holds would read as a draft, if it were
			// followed.
			text := secret
			if b, err := os.ReadFile(at); err == nil {
				text = strings.Replace(string(b), "BoundedBuffer", "BoundedBuffer", 1) + "\n\\* " + secret + "\n"
			}
			key := filepath.Join(t.TempDir(), "factory.pem")
			if err := os.WriteFile(key, []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
			os.Remove(at)
			if err := link(key, at); err != nil {
				t.Fatal(err)
			}
			var err error
			var got string
			if name == "issues.json" {
				var p *IssuePlan
				p, err = ReadIssues(ws)
				if p != nil {
					got = p.Summary
				}
			} else {
				var p *Proposal
				p, err = Read(ws)
				if p != nil {
					got = p.ModuleText
				}
			}
			var refused *regular.Refused
			if !errors.As(err, &refused) || refused.File != name {
				t.Errorf("%s as a link: %v; want it refused", name, err)
			}
			if strings.Contains(got, secret) || strings.Contains(errText(err), secret) {
				t.Errorf("%s as a link: the key's text came through", name)
			}
		}
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
