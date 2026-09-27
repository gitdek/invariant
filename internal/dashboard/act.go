package dashboard

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"math/big"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gitdek/invariant/internal/github"
)

// The page at /act can post @gitdek's answers and commands to GitHub, from
// any device (D-0065). Cloudflare Access keeps everyone else out of /act,
// and the server checks Access's signed token again on every request, so a
// policy that's missing or wrong lets nobody in. A command posts through the
// dashboard's own gh login, so on GitHub it's @gitdek's comment, as if he'd
// typed it. The rest of the page stays public and only reads.

// Access is the Cloudflare Access application in front of /act.
type Access struct {
	Team     string   // the team's domain, such as puglisij.cloudflareaccess.com
	Audience string   // the application's AUD tag
	Emails   []string // who may act
	Client   *http.Client
	Now      func() time.Time

	mu      sync.Mutex
	keys    map[string]*rsa.PublicKey
	fetched time.Time
}

// errNotSignedIn is why a request to /act is refused.
var errNotSignedIn = errors.New("sign in through Cloudflare Access at /act")

// Identity checks the token Cloudflare Access adds to a request, and
// returns the email it was issued to.
func (a *Access) Identity(r *http.Request) (string, error) {
	tok := r.Header.Get("Cf-Access-Jwt-Assertion")
	parts := strings.Split(tok, ".")
	if tok == "" || len(parts) != 3 {
		return "", errNotSignedIn
	}
	var head struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := decodePart(parts[0], &head); err != nil || head.Alg != "RS256" {
		return "", fmt.Errorf("%w: the token isn't RS256", errNotSignedIn)
	}
	key, err := a.key(r.Context(), head.Kid)
	if err != nil {
		return "", err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", fmt.Errorf("%w: the token's signature doesn't decode", errNotSignedIn)
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig) != nil {
		return "", fmt.Errorf("%w: the token's signature doesn't verify", errNotSignedIn)
	}
	var claims struct {
		Aud   audience `json:"aud"`
		Iss   string   `json:"iss"`
		Exp   int64    `json:"exp"`
		Nbf   int64    `json:"nbf"`
		Email string   `json:"email"`
	}
	if err := decodePart(parts[1], &claims); err != nil {
		return "", fmt.Errorf("%w: the token's claims don't decode", errNotSignedIn)
	}
	now := a.now().Unix()
	switch {
	case !claims.Aud.has(a.Audience):
		return "", fmt.Errorf("%w: the token is for another application", errNotSignedIn)
	case claims.Iss != "https://"+a.Team:
		return "", fmt.Errorf("%w: the token is from another team", errNotSignedIn)
	case claims.Exp <= now || claims.Nbf > now+60:
		return "", fmt.Errorf("%w: the token has expired", errNotSignedIn)
	}
	for _, e := range a.Emails {
		if claims.Email != "" && strings.EqualFold(e, claims.Email) {
			return claims.Email, nil
		}
	}
	return "", fmt.Errorf("%w: %s may not act here", errNotSignedIn, claims.Email)
}

func (a *Access) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// key is the team's signing key with that ID. It fetches the team's keys
// when it doesn't have that one, at most once a minute, and every six hours
// anyway, since Cloudflare rotates them.
func (a *Access) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	if k, ok := a.keys[kid]; ok && now.Sub(a.fetched) < 6*time.Hour {
		return k, nil
	}
	if !a.fetched.IsZero() && now.Sub(a.fetched) < time.Minute {
		if k, ok := a.keys[kid]; ok {
			return k, nil
		}
		return nil, fmt.Errorf("%w: the token's key isn't the team's", errNotSignedIn)
	}
	keys, err := a.fetch(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading Cloudflare Access's keys: %w", err)
	}
	a.keys, a.fetched = keys, now
	if k, ok := keys[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("%w: the token's key isn't the team's", errNotSignedIn)
}

func (a *Access) fetch(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	c := a.Client
	if c == nil {
		c = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, "GET", "https://"+a.Team+"/cdn-cgi/access/certs", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s", resp.Status)
	}
	var set struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&set); err != nil {
		return nil, err
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		n, err1 := base64.RawURLEncoding.DecodeString(k.N)
		e, err2 := base64.RawURLEncoding.DecodeString(k.E)
		if k.Kty != "RSA" || err1 != nil || err2 != nil || len(e) == 0 || len(e) > 4 {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	}
	if len(keys) == 0 {
		return nil, errors.New("the team has no RSA keys")
	}
	return keys, nil
}

func decodePart(part string, v any) error {
	b, err := base64.RawURLEncoding.DecodeString(part)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// audience is a token's aud claim, which is a list or a single string.
type audience []string

func (a *audience) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*a = audience{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*a = many
	return nil
}

func (a audience) has(want string) bool {
	for _, v := range a {
		if want != "" && v == want {
			return true
		}
	}
	return false
}

// act is a command the page asks to post on an issue.
type act struct {
	Repo  string `json:"repo"`
	Issue int    `json:"issue"`
	Body  string `json:"body"`
}

var (
	chooseLine = regexp.MustCompile(`^/invariant choose ([A-Za-z]\d{1,2}) ([A-Za-z])$`)
	ratifyLine = regexp.MustCompile(`^/invariant ratify ([0-9a-f]{12,64})$`)
)

// check says whether a command is one the issue is waiting for: answers to
// its open questions, a ratification of its proposal, a retry of its failed
// pull request, or a revise. The page offers only those.
func (c act) check(issues []Issue) error {
	var is *Issue
	for i := range issues {
		if issues[i].Repo == c.Repo && issues[i].Number == c.Issue {
			is = &issues[i]
		}
	}
	if is == nil || !is.Open || is.Waiting == nil {
		return errors.New("that issue isn't waiting on anyone")
	}
	w := is.Waiting
	lines := strings.Split(strings.TrimSpace(c.Body), "\n")
	if len(lines) == 0 || len(lines) > len(w.Forks)+1 {
		return errors.New("that's more commands than the issue is waiting for")
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		switch m := chooseLine.FindStringSubmatch(line); {
		case line == "/invariant revise" && (w.Kind == "forks" || w.Kind == "proposal"):
		case line == "/invariant retry" && w.Kind == "failed":
		case m != nil && w.Kind == "forks":
			if !offered(w.Forks, m[1], m[2]) {
				return fmt.Errorf("%s has no option %s", m[1], m[2])
			}
		case w.Kind == "proposal" && ratifyLine.MatchString(line) && w.Hash != "" && ratifyLine.FindStringSubmatch(line)[1] == w.Hash:
		default:
			return fmt.Errorf("%q isn't something #%d is waiting for", line, c.Issue)
		}
	}
	return nil
}

func offered(forks []Question, id, option string) bool {
	for _, q := range forks {
		if strings.EqualFold(q.ID, id) {
			for _, o := range q.Options {
				if strings.EqualFold(o.ID, option) {
					return true
				}
			}
		}
	}
	return false
}

// actPage is the page, marked for acting as the person signed in.
func actPage(email string) []byte {
	page := strings.ReplaceAll(string(assets["/index.html"]), "{{version}}", version)
	return []byte(strings.Replace(page, "<body>", `<body data-act="`+html.EscapeString(email)+`">`, 1))
}

// serveAct answers /act: the page, and the command it posts.
func (s *Server) serveAct(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	if s.Access == nil {
		http.NotFound(w, r)
		return
	}
	email, err := s.Access.Identity(r)
	if err != nil {
		s.logf("act: refused %s %s: %v", r.Method, r.URL.Path, err)
		http.Error(w, "Only @gitdek can act here. "+strings.ToUpper(errNotSignedIn.Error()[:1])+errNotSignedIn.Error()[1:]+".", http.StatusForbidden)
		return
	}
	switch {
	case r.URL.Path == "/act" && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		h.Set("Content-Type", "text/html; charset=utf-8")
		w.Write(actPage(email))
	case r.URL.Path == "/act/api/comment" && r.Method == http.MethodPost:
		s.postAct(w, r, email)
	case r.URL.Path == "/act/api/issue" && r.Method == http.MethodPost:
		if !fromPage(r) {
			http.Error(w, "only the page can post", http.StatusForbidden)
			return
		}
		s.openIssue(w, r, email, "")
	default:
		http.NotFound(w, r)
	}
}

// postAct posts a command on an issue as @gitdek. Only the page itself can
// send it: another site can't set its header, and its origin must be this
// one.
func (s *Server) postAct(w http.ResponseWriter, r *http.Request, email string) {
	if !fromPage(r) {
		http.Error(w, "only the page can post", http.StatusForbidden)
		return
	}
	s.post(w, r, email, "")
}

// fromPage says whether a request came from the page itself: another site
// can't set its header, and its origin must be this one.
func fromPage(r *http.Request) bool {
	return r.Header.Get("X-Invariant") == "act" && strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") &&
		(r.Header.Get("Origin") == "" || r.Header.Get("Origin") == "https://"+r.Host) &&
		(r.Header.Get("Sec-Fetch-Site") == "" || r.Header.Get("Sec-Fetch-Site") == "same-origin")
}

// agentNote ends every comment the agent's door posts, so the issue says
// who really wrote it.
const agentNote = "\n\n_Posted for @gitdek by a coding agent, through the dashboard._"

// AgentHandler is the agent's door (D-0066). A coding agent acting for
// @gitdek posts through the same narrow check as his page, so it can only
// give an issue what it's waiting for. It's served on its own listener,
// which only this machine can reach and the tunnel never publishes, and it
// takes the token the dashboard wrote where only @gitdek's account can read
// it. Browsers can't use it at all.
func (s *Server) AgentHandler(token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.Error(w, "the agent's token is missing or wrong", http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") != "" {
			http.Error(w, "browsers can't use the agent's door", http.StatusForbidden)
			return
		}
		switch {
		case r.URL.Path == "/act/api/comment" && r.Method == http.MethodPost:
			s.post(w, r, "a coding agent", agentNote)
		case r.URL.Path == "/act/api/issue" && r.Method == http.MethodPost:
			s.openIssue(w, r, "a coding agent", agentNote)
		default:
			http.NotFound(w, r)
		}
	})
}

// post checks a command against what its issue is waiting for, and posts
// it as @gitdek, followed by note.
func (s *Server) post(w http.ResponseWriter, r *http.Request, who, note string) {
	var c act
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&c); err != nil {
		http.Error(w, "that isn't a command", http.StatusBadRequest)
		return
	}
	s.mu.RLock()
	issues := s.issues
	s.mu.RUnlock()
	if err := c.check(issues); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	post := s.Post
	if post == nil {
		post = func(ctx context.Context, repo string, issue int, body string) (string, error) {
			comment, err := github.Client{Repo: repo}.PostComment(ctx, issue, body)
			return comment.URL, err
		}
	}
	url, err := post(r.Context(), c.Repo, c.Issue, strings.TrimSpace(c.Body)+note)
	if err != nil {
		s.logf("act: posting on %s#%d for %s: %v", c.Repo, c.Issue, who, err)
		http.Error(w, "GitHub didn't take it; try again", http.StatusBadGateway)
		return
	}
	s.logf("act: %s posted %q on %s#%d", who, c.Body, c.Repo, c.Issue)
	h := w.Header()
	h.Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"url": url})
}

// newIssue is an issue the page asks to open, for the factory to solve.
type newIssue struct {
	Repo     string   `json:"repo"`
	Title    string   `json:"title"`
	Body     string   `json:"body"`
	Project  string   `json:"project"`  // where the project goes, or the one it changes
	Code     []string `json:"code"`     // existing code for the project to check as it is
	Language string   `json:"language"` // go, typescript or python; empty for the repository's default
}

var issuePath = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(/[A-Za-z0-9][A-Za-z0-9._-]*)*$`)

// compose writes the issue the factory reads: what must be true, the
// Project: and Code: lines, and the /invariant solve line that hands it to
// the factory.
func (n newIssue) compose(repos []*Repo) (github.NewIssue, error) {
	known := false
	for _, r := range repos {
		known = known || r.Name == n.Repo
	}
	title, body := strings.TrimSpace(n.Title), strings.TrimSpace(n.Body)
	switch {
	case !known:
		return github.NewIssue{}, errors.New("the dashboard doesn't show that repository")
	case title == "" || len(title) > 200 || strings.ContainsAny(title, "\r\n"):
		return github.NewIssue{}, errors.New("give the issue a title of one line")
	case body == "" || len(body) > 20000:
		return github.NewIssue{}, errors.New("say what must be true, in under 20,000 characters")
	case len(n.Code) > 10:
		return github.NewIssue{}, errors.New("name at most ten paths of code")
	}
	var b strings.Builder
	b.WriteString(body + "\n\n")
	for _, p := range append([]string{n.Project}, n.Code...) {
		if p = strings.TrimSpace(p); p != "" && (len(p) > 120 || !issuePath.MatchString(p) || strings.Contains(p, "..")) {
			return github.NewIssue{}, fmt.Errorf("%q isn't a path in the repository", p)
		}
	}
	for _, c := range n.Code {
		if c = strings.TrimSpace(c); c != "" {
			b.WriteString("Code: " + c + "\n")
		}
	}
	if p := strings.TrimSpace(n.Project); p != "" {
		b.WriteString("Project: " + p + "\n")
	}
	b.WriteString("\n/invariant solve\n")
	is := github.NewIssue{Title: title, Body: b.String()}
	switch n.Language {
	case "":
	case "go", "typescript", "python":
		is.Labels = []string{"invariant:" + n.Language}
	default:
		return github.NewIssue{}, errors.New("the language is go, typescript or python")
	}
	return is, nil
}

// openIssue opens an issue as @gitdek, with /invariant solve, so the
// factory takes it, followed by note.
func (s *Server) openIssue(w http.ResponseWriter, r *http.Request, who, note string) {
	var n newIssue
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<10)).Decode(&n); err != nil {
		http.Error(w, "that isn't an issue", http.StatusBadRequest)
		return
	}
	is, err := n.compose(s.Repos)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	is.Body += strings.TrimPrefix(note, "\n")
	open := s.Open
	if open == nil {
		open = func(ctx context.Context, repo string, is github.NewIssue) (int, string, error) {
			out, err := github.Client{Repo: repo}.CreateIssue(ctx, is)
			return out.Number, out.URL, err
		}
	}
	number, url, err := open(r.Context(), n.Repo, is)
	if err != nil {
		s.logf("act: opening an issue on %s for %s: %v", n.Repo, who, err)
		http.Error(w, "GitHub didn't take it; try again", http.StatusBadGateway)
		return
	}
	s.logf("act: %s opened %s#%d, %q", who, n.Repo, number, is.Title)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"number": number, "url": url})
}
