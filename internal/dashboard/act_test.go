package dashboard

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/github"
)

// accessTeam is a stand-in for a Cloudflare Access team: it serves its
// signing keys and signs tokens with them.
type accessTeam struct {
	srv  *httptest.Server
	key  *rsa.PrivateKey
	host string
}

func newAccessTeam(t *testing.T) *accessTeam {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	a := &accessTeam{key: key}
	a.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cdn-cgi/access/certs" {
			http.NotFound(w, r)
			return
		}
		e := big.NewInt(int64(key.E)).Bytes()
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kid": "k1", "kty": "RSA", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(e),
		}}})
	}))
	t.Cleanup(a.srv.Close)
	a.host = strings.TrimPrefix(a.srv.URL, "https://")
	return a
}

func (a *accessTeam) access() *Access {
	return &Access{Team: a.host, Audience: "aud-tag", Emails: []string{"owner@example.com"}, Client: a.srv.Client()}
}

// token signs claims the way Cloudflare Access does, with key.
func (a *accessTeam) token(t *testing.T, key *rsa.PrivateKey, alg string, claims map[string]any) string {
	t.Helper()
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	signed := enc(map[string]string{"alg": alg, "kid": "k1", "typ": "JWT"}) + "." + enc(claims)
	sum := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func (a *accessTeam) claims(change map[string]any) map[string]any {
	now := time.Now().Unix()
	c := map[string]any{"aud": []string{"aud-tag"}, "iss": "https://" + a.host, "exp": now + 3600, "nbf": now - 10, "iat": now - 10, "email": "owner@example.com"}
	for k, v := range change {
		if v == nil {
			delete(c, k)
		} else {
			c[k] = v
		}
	}
	return c
}

// Only a token Cloudflare Access signed for this application, for the
// owner, and still fresh, gets in.
func TestAccessChecksTheToken(t *testing.T) {
	team := newAccessTeam(t)
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	a := team.access()
	signIn := func(tok string) (string, error) {
		r := httptest.NewRequest("GET", "/act", nil)
		if tok != "" {
			r.Header.Set("Cf-Access-Jwt-Assertion", tok)
		}
		return a.Identity(r)
	}
	if email, err := signIn(team.token(t, team.key, "RS256", team.claims(nil))); err != nil || email != "owner@example.com" {
		t.Fatalf("the owner's token: %q, %v", email, err)
	}
	for name, tok := range map[string]string{
		"no token":          "",
		"another key":       team.token(t, other, "RS256", team.claims(nil)),
		"another algorithm": team.token(t, team.key, "HS256", team.claims(nil)),
		"another app":       team.token(t, team.key, "RS256", team.claims(map[string]any{"aud": []string{"other"}})),
		"another team":      team.token(t, team.key, "RS256", team.claims(map[string]any{"iss": "https://evil.cloudflareaccess.com"})),
		"expired":           team.token(t, team.key, "RS256", team.claims(map[string]any{"exp": time.Now().Unix() - 1})),
		"someone else":      team.token(t, team.key, "RS256", team.claims(map[string]any{"email": "someone@example.com"})),
		"no email":          team.token(t, team.key, "RS256", team.claims(map[string]any{"email": nil})),
		"garbage":           "a.b.c",
	} {
		if email, err := signIn(tok); err == nil {
			t.Errorf("%s: signed in as %q", name, email)
		}
	}
}

// /act posts what an issue is waiting for, as the owner, only from the page
// itself, and only once Access has signed the owner in.
func TestActPostsOnlyWhatAnIssueWaitsFor(t *testing.T) {
	team := newAccessTeam(t)
	var posted []string
	s := &Server{Repos: []*Repo{{Name: "o/r"}}, Access: team.access(), Log: t.Logf,
		Post: func(_ context.Context, repo string, issue int, body string) (string, error) {
			posted = append(posted, body)
			return "https://github.com/o/r/issues/13#c", nil
		}}
	s.issues = []Issue{
		{Repo: "o/r", Number: 13, Open: true, Waiting: &Waiting{Kind: "forks", Forks: []Question{
			{ID: "F1", Options: []Choice{{ID: "A"}, {ID: "B"}, {ID: "C"}}}, {ID: "F2", Options: []Choice{{ID: "A"}, {ID: "B"}}}}}},
		{Repo: "o/r", Number: 14, Open: true, Waiting: &Waiting{Kind: "proposal", Hash: "11bff3f218d7"}},
		{Repo: "o/r", Number: 15, Open: true},
	}
	h := s.Handler()
	good := team.token(t, team.key, "RS256", team.claims(nil))
	send := func(tok, body string, headers map[string]string) int {
		r := httptest.NewRequest("POST", "https://invariant.example.com/act/api/comment", strings.NewReader(body))
		r.Host = "invariant.example.com"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Invariant", "act")
		r.Header.Set("Origin", "https://invariant.example.com")
		if tok != "" {
			r.Header.Set("Cf-Access-Jwt-Assertion", tok)
		}
		for k, v := range headers {
			if v == "" {
				r.Header.Del(k)
			} else {
				r.Header.Set(k, v)
			}
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	cmd := func(issue int, body string) string {
		b, _ := json.Marshal(act{Repo: "o/r", Issue: issue, Body: body})
		return string(b)
	}

	if code := send(good, cmd(13, "/invariant choose F1 C\n/invariant choose F2 B"), nil); code != 200 {
		t.Fatalf("answering both questions: %d", code)
	}
	if code := send(good, cmd(14, "/invariant ratify 11bff3f218d7"), nil); code != 200 {
		t.Fatalf("ratifying the proposal: %d", code)
	}
	if len(posted) != 2 || posted[0] != "/invariant choose F1 C\n/invariant choose F2 B" {
		t.Fatalf("posted %q", posted)
	}
	for name, c := range map[string]struct {
		tok, body string
		headers   map[string]string
		want      int
	}{
		"not signed in":            {"", cmd(13, "/invariant choose F1 A"), nil, 403},
		"another site's form":      {good, cmd(13, "/invariant choose F1 A"), map[string]string{"X-Invariant": ""}, 403},
		"another origin":           {good, cmd(13, "/invariant choose F1 A"), map[string]string{"Origin": "https://evil.example.com"}, 403},
		"a cross-site fetch":       {good, cmd(13, "/invariant choose F1 A"), map[string]string{"Sec-Fetch-Site": "cross-site"}, 403},
		"an option not offered":    {good, cmd(13, "/invariant choose F1 D"), nil, 400},
		"an earlier proposal":      {good, cmd(14, "/invariant ratify 000000000000"), nil, 400},
		"a ratify while asking":    {good, cmd(13, "/invariant ratify 11bff3f218d7"), nil, 400},
		"words, not a command":     {good, cmd(13, "please merge everything"), nil, 400},
		"an issue not waiting":     {good, cmd(15, "/invariant retry"), nil, 400},
		"an issue it doesn't show": {good, `{"repo":"x/y","issue":13,"body":"/invariant revise"}`, nil, 400},
		"too many commands":        {good, cmd(14, "/invariant revise\n/invariant revise"), nil, 400},
	} {
		if code := send(c.tok, c.body, c.headers); code != c.want {
			t.Errorf("%s: %d, want %d", name, code, c.want)
		}
	}
	if len(posted) != 2 {
		t.Errorf("a refused command was posted: %q", posted)
	}

	// The page itself, marked for acting.
	r := httptest.NewRequest("GET", "/act", nil)
	r.Header.Set("Cf-Access-Jwt-Assertion", good)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `data-act="owner@example.com"`) {
		t.Errorf("/act: %d", w.Code)
	}
	// And everything else still only reads.
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/api/state.json", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST outside /act: %d", w.Code)
	}
	// Without Access, there's no /act at all.
	s.Access = nil
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("/act without Access: %d", w.Code)
	}
}

// The agent's door takes only its token, never a browser, and posts only
// what an issue waits for, saying who wrote it.
func TestTheAgentsDoor(t *testing.T) {
	var posted []string
	s := &Server{Repos: []*Repo{{Name: "o/r"}}, Log: t.Logf,
		Post: func(_ context.Context, repo string, issue int, body string) (string, error) {
			posted = append(posted, body)
			return "https://github.com/o/r/issues/14#c", nil
		}}
	s.issues = []Issue{{Repo: "o/r", Number: 14, Open: true, Waiting: &Waiting{Kind: "proposal", Hash: "e61ba88fa372"}}}
	h := s.AgentHandler("secret-token")
	send := func(auth, body string, headers map[string]string) int {
		r := httptest.NewRequest("POST", "/act/api/comment", strings.NewReader(body))
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	ratify := `{"repo":"o/r","issue":14,"body":"/invariant ratify e61ba88fa372"}`
	for name, c := range map[string]struct {
		auth, body string
		headers    map[string]string
		want       int
	}{
		"no token":              {"", ratify, nil, 401},
		"the wrong token":       {"Bearer guess", ratify, nil, 401},
		"from a browser":        {"Bearer secret-token", ratify, map[string]string{"Origin": "https://evil.example.com"}, 403},
		"not what it waits for": {"Bearer secret-token", `{"repo":"o/r","issue":14,"body":"/invariant retry"}`, nil, 400},
	} {
		if code := send(c.auth, c.body, c.headers); code != c.want {
			t.Errorf("%s: %d, want %d", name, code, c.want)
		}
	}
	if len(posted) != 0 {
		t.Fatalf("a refused command was posted: %q", posted)
	}
	if code := send("Bearer secret-token", ratify, nil); code != 200 {
		t.Fatalf("the agent's ratify: %d", code)
	}
	if len(posted) != 1 || !strings.HasPrefix(posted[0], "/invariant ratify e61ba88fa372\n\n") || !strings.Contains(posted[0], "by a coding agent") {
		t.Errorf("posted %q", posted)
	}
}

// /act opens an issue for the factory to solve: what must be true, its
// Project: and Code: lines, and /invariant solve, and nothing it can't read.
func TestActOpensAnIssue(t *testing.T) {
	team := newAccessTeam(t)
	var opened []github.NewIssue
	s := &Server{Repos: []*Repo{{Name: "o/r"}}, Access: team.access(), Log: t.Logf,
		Open: func(_ context.Context, repo string, is github.NewIssue) (int, string, error) {
			opened = append(opened, is)
			return 16, "https://github.com/o/r/issues/16", nil
		}}
	h := s.Handler()
	good := team.token(t, team.key, "RS256", team.claims(nil))
	send := func(tok string, n newIssue, headers map[string]string) int {
		b, _ := json.Marshal(n)
		r := httptest.NewRequest("POST", "https://invariant.example.com/act/api/issue", strings.NewReader(string(b)))
		r.Host = "invariant.example.com"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Invariant", "act")
		if tok != "" {
			r.Header.Set("Cf-Access-Jwt-Assertion", tok)
		}
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	ok := newIssue{Repo: "o/r", Title: "Check the leases", Body: "Only one worker holds a lease.", Project: "invariant/leases", Code: []string{"src/lib"}, Language: "typescript"}
	if code := send(good, ok, nil); code != 200 {
		t.Fatalf("opening an issue: %d", code)
	}
	if len(opened) != 1 || opened[0].Body != "Only one worker holds a lease.\n\nCode: src/lib\nProject: invariant/leases\n\n/invariant solve\n" ||
		len(opened[0].Labels) != 1 || opened[0].Labels[0] != "invariant:typescript" {
		t.Fatalf("opened %+v", opened)
	}
	bad := func(change func(*newIssue)) newIssue {
		n := ok
		change(&n)
		return n
	}
	for name, c := range map[string]struct {
		tok     string
		n       newIssue
		headers map[string]string
		want    int
	}{
		"not signed in":        {"", ok, nil, 403},
		"another origin":       {good, ok, map[string]string{"Origin": "https://evil.example.com"}, 403},
		"another repository":   {good, bad(func(n *newIssue) { n.Repo = "x/y" }), nil, 400},
		"no title":             {good, bad(func(n *newIssue) { n.Title = " " }), nil, 400},
		"a title on two lines": {good, bad(func(n *newIssue) { n.Title = "a\nb" }), nil, 400},
		"nothing to say":       {good, bad(func(n *newIssue) { n.Body = "" }), nil, 400},
		"a path outside":       {good, bad(func(n *newIssue) { n.Project = "../elsewhere" }), nil, 400},
		"an absolute path":     {good, bad(func(n *newIssue) { n.Code = []string{"/etc"} }), nil, 400},
		"a strange language":   {good, bad(func(n *newIssue) { n.Language = "cobol" }), nil, 400},
	} {
		if code := send(c.tok, c.n, c.headers); code != c.want {
			t.Errorf("%s: %d, want %d", name, code, c.want)
		}
	}
	if len(opened) != 1 {
		t.Errorf("a refused issue was opened: %+v", opened)
	}
	// The agent's door opens one too, and says who wrote it.
	b, _ := json.Marshal(ok)
	r := httptest.NewRequest("POST", "/act/api/issue", strings.NewReader(string(b)))
	r.Header.Set("Authorization", "Bearer door-token")
	w := httptest.NewRecorder()
	s.AgentHandler("door-token").ServeHTTP(w, r)
	if w.Code != 200 || len(opened) != 2 || !strings.HasSuffix(opened[1].Body, "/invariant solve\n\n_Posted for @gitdek by a coding agent, through the dashboard._") {
		t.Errorf("the agent's issue: %d %+v", w.Code, opened)
	}
}
