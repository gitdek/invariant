package dashboard

import (
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/factory"
	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/plumbing"
)

func TestNativeProposalViewRetainsFullIdentityAndSource(t *testing.T) {
	var p plumbing.Plan
	if err := json.Unmarshal([]byte(`{"name":"native view","summary":"Show the test being ratified.","files":["web/main.ts"],"tests":[{"name":"<img src=x>","part":"web","framework":"jest","file":"web/ready.test.ts","selector":"outer exact title [case one]","says":"It is ready."}],"sources":{"web/ready.test.ts":"it('exact title [case one]', () => {});\n"}}`), &p); err != nil {
		t.Fatal(err)
	}
	proposal := &formalize.Proposal{Draft: formalize.Draft{Name: p.Name}, Plan: &p, Hash: p.Hash()}
	m := factory.Marker{Kind: factory.KindProposal, Proposal: proposal}
	view := waitingFor(m, time.Now())
	data, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	// JSON remains data for the page's existing text escaping. The full
	// tuple must survive this projection, not just Name/Says. Sources have
	// their own full-hash authority and never travel in polled state.
	var object any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	decoded, _ := json.Marshal(object)
	for _, want := range []string{"web", "jest", "web/ready.test.ts", "outer exact title [case one]", p.Hash()} {
		if !strings.Contains(string(decoded), want) {
			t.Fatalf("proposal view lost %q: %s", want, decoded)
		}
	}
	if strings.Contains(string(decoded), "it('exact title [case one]'") {
		t.Fatal("polled view contains acceptance source")
	}
	s := &Server{Cache: t.TempDir()}
	is := Issue{Repo: "owner/repo", Waiting: view}
	s.presentPlanSources(&is, []github.Comment{{User: github.User{Login: "factory[bot]"}, Body: marker(t, m)}}, "factory[bot]")
	if view.Plan.SourceHash != p.Hash() || view.Plan.SourceURL != planSourcesURL(p.Hash()) {
		t.Fatal("lost full source authority")
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", view.Plan.SourceURL, nil))
	var response planSourceResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil ||
		response.Hash != p.Hash() || !reflect.DeepEqual(response.Sources, p.Sources) ||
		!reflect.DeepEqual(response.Languages, map[string]string{"web/ready.test.ts": "typescript"}) {
		t.Fatalf("source retrieval changed the native proposal: %d %s", w.Code, w.Body)
	}
}
