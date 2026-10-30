package dashboard

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The private wire contract extends the existing live envelope only when
// commands=1 is explicitly requested through the Access-protected route.
// Each commands entry is a shell invocation, in transcript order, with
// command, kind, at and optional until. Pending calls have no until.
// Public Live and Mark JSON, and the private default, expose no command text.
type act236Command struct {
	Command string     `json:"command"`
	Kind    string     `json:"kind"`
	At      time.Time  `json:"at"`
	Until   *time.Time `json:"until,omitempty"`
}

type act236Step struct {
	Live
	Commands []act236Command `json:"commands"`
}

type act236Envelope struct {
	Steps []act236Step `json:"steps"`
}

type act236Fixture struct {
	s         *Server
	team      *accessTeam
	owner     string
	work      string
	workspace string
	path      string
	provider  string
	since     time.Time
}

func act236Line(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

func act236Write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func newAct236Fixture(t *testing.T, provider string) *act236Fixture {
	t.Helper()
	f := &act236Fixture{team: newAccessTeam(t), work: t.TempDir(), workspace: t.TempDir(), provider: provider,
		since: time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)}
	f.s = &Server{Work: f.work, Repos: []*Repo{{Name: "owner/app", Status: StatusPath(f.work, "owner/app")}}, Access: f.team.access()}
	f.owner = f.team.token(t, f.team.key, "RS256", f.team.claims(nil))
	f.path = filepath.Join(f.work, "owner/app/issues/issue-236/build-20261029-100000/transcript.jsonl")
	f.status(t, act236WithRun(t, Step{Issue: 236, Doing: "building", Since: f.since, Agent: provider}, filepath.Base(filepath.Dir(f.path))))
	act236Write(t, f.path, f.init(t))
	act236Write(t, filepath.Join(f.workspace, "app.go"), "PRIVATE_FILE_236\n")
	return f
}

func (f *act236Fixture) at(sec int) time.Time {
	return f.since.Add(time.Duration(sec) * time.Second)
}

func (f *act236Fixture) status(t *testing.T, steps ...Step) {
	t.Helper()
	if err := WriteStatus(StatusPath(f.work, "owner/app"), Status{PID: os.Getpid(), Repo: "owner/app", Started: f.since, Steps: steps}); err != nil {
		t.Fatal(err)
	}
}

func (f *act236Fixture) init(t *testing.T) string {
	t.Helper()
	if f.provider == "codex" {
		return act236Line(t, map[string]any{"type": "invariant.workspace", "cwd": f.workspace, "timestamp": f.at(0)})
	}
	return act236Line(t, map[string]any{"type": "system", "subtype": "init", "cwd": f.workspace})
}

func (f *act236Fixture) append(t *testing.T, body string) {
	t.Helper()
	out, err := os.OpenFile(f.path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = out.WriteString(body)
	closeErr := out.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
}

func (f *act236Fixture) start(t *testing.T, id, command string, sec int) string {
	t.Helper()
	if f.provider == "codex" {
		return act236Line(t, map[string]any{"type": "item.started", "timestamp": f.at(sec),
			"item": map[string]any{"id": id, "type": "command_execution", "command": command, "status": "in_progress"}})
	}
	return act236Line(t, map[string]any{"type": "assistant", "timestamp": f.at(sec),
		"message": map[string]any{"id": "message-" + id, "content": []map[string]any{{"type": "tool_use", "id": id,
			"name": "Bash", "input": map[string]any{"command": command, "description": "PRIVATE_DESCRIPTION_236"}}}}})
}

func (f *act236Fixture) done(t *testing.T, id, command string, sec int) string {
	t.Helper()
	if f.provider == "codex" {
		return act236Line(t, map[string]any{"type": "item.completed", "timestamp": f.at(sec),
			"item": map[string]any{"id": id, "type": "command_execution", "command": command,
				"aggregated_output": "PRIVATE_OUTPUT_236", "status": "completed", "exit_code": 0}})
	}
	return act236Line(t, map[string]any{"type": "user", "timestamp": f.at(sec),
		"message": map[string]any{"content": []map[string]any{{"type": "tool_result", "tool_use_id": id, "content": "PRIVATE_OUTPUT_236"}}}})
}

func act236Request(h http.Handler, method, path, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://dashboard.example"+path, nil)
	if token != "" {
		r.Header.Set("Cf-Access-Jwt-Assertion", token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func (f *act236Fixture) read(t *testing.T) act236Envelope {
	t.Helper()
	w := act236Request(f.s.Handler(), "GET", "/act/api/live.json?commands=1", f.owner)
	if w.Code != http.StatusOK {
		t.Fatalf("opted-in private live response: HTTP %d, want 200: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("private live response must be uncacheable JSON: %v", w.Header())
	}
	for _, forbidden := range []string{"PRIVATE_OUTPUT_236", "PRIVATE_DESCRIPTION_236", "PRIVATE_REASONING_236", "PRIVATE_FILE_236", "PRIVATE_MESSAGE_236"} {
		if strings.Contains(w.Body.String(), forbidden) {
			t.Errorf("private commands response also exposed %s", forbidden)
		}
	}
	var e act236Envelope
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatal(err)
	}
	return e
}

func (f *act236Fixture) eventually(t *testing.T, says string, ready func(act236Envelope) bool) act236Envelope {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := f.read(t)
		if ready(got) {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: private live view never updated: %+v", says, got)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func act236Want(t *testing.T, got act236Command, command, kind string, at time.Time, until *time.Time) {
	t.Helper()
	if got.Command != command || got.Kind != kind || !got.At.Equal(at) {
		t.Errorf("invocation = %+v; want command %q, kind %s, at %v", got, command, kind, at)
	}
	if until == nil && got.Until != nil || until != nil && (got.Until == nil || !got.Until.Equal(*until)) {
		t.Errorf("%q until = %v; want %v", command, got.Until, until)
	}
}

func TestActShellCommandsKeepExactTextKindsAndTimes(t *testing.T) {
	for _, provider := range []string{"claude-code", "codex"} {
		t.Run(provider, func(t *testing.T) {
			f := newAct236Fixture(t, provider)
			cases := []struct{ text, kind string }{
				{"cat  'app.go'", "read"},
				{"rg -n 'PRIVATE_PATTERN_236' app.go", "search"},
				{"ls -la", "list"},
				{"go build ./...", "build"},
				{"go test ./...", "test"},
				{"git status --short", "git"},
				{"TOKEN=PRIVATE_COMMAND_236 printf '<img src=x onerror=alert(1)> & café'\nprintf 'line two'", "other"},
				{"/bin/zsh -lc 'cat app.go'", "read"},
			}
			// Start every call before completing any, and finish out of order.
			for i, c := range cases {
				f.append(t, f.start(t, fmt.Sprint(i), c.text, i+1))
			}
			for i := len(cases) - 1; i > 0; i-- {
				f.append(t, f.done(t, fmt.Sprint(i), cases[i].text, 30+i))
			}
			if provider == "codex" {
				// A completion without a start is still one invocation; a repeated
				// completion must not duplicate it or replace its first text.
				f.append(t, f.done(t, "completion-only", "pwd", 50)+f.done(t, "completion-only", "pwd", 50))
				f.append(t, act236Line(t, map[string]any{"type": "item.completed", "timestamp": f.at(51), "item": map[string]any{
					"id": "think", "type": "reasoning", "text": "PRIVATE_REASONING_236"}}))
				f.append(t, act236Line(t, map[string]any{"type": "item.completed", "timestamp": f.at(52), "item": map[string]any{
					"id": "edit", "type": "file_change", "command": "NOT_A_SHELL_236", "changes": []map[string]any{{"path": "app.go"}}}}))
			} else {
				f.append(t, act236Line(t, map[string]any{"type": "assistant", "timestamp": f.at(51), "message": map[string]any{
					"id": "other-tools", "content": []map[string]any{
						{"type": "text", "text": "PRIVATE_MESSAGE_236"},
						{"type": "thinking", "thinking": "PRIVATE_REASONING_236"},
						{"type": "tool_use", "id": "read-tool", "name": "Read", "input": map[string]any{"file_path": "app.go", "command": "NOT_A_SHELL_236"}},
					}}}))
			}
			// Reading the public route first must not discard the text needed
			// when the owner subsequently turns the private option on.
			pub := act236Request(f.s.Handler(), "GET", "/api/live.json", "")
			if pub.Code != 200 {
				t.Fatal(pub.Code)
			}
			got := f.read(t)
			wantLen := len(cases)
			if provider == "codex" {
				wantLen++
			}
			if len(got.Steps) != 1 || len(got.Steps[0].Commands) != wantLen {
				t.Fatalf("private steps = %+v; want one step with %d shell commands", got.Steps, wantLen)
			}
			for i, c := range cases {
				var until *time.Time
				if i > 0 {
					u := f.at(30 + i)
					until = &u
				}
				act236Want(t, got.Steps[0].Commands[i], c.text, c.kind, f.at(i+1), until)
			}
			if provider == "codex" {
				u := f.at(50)
				act236Want(t, got.Steps[0].Commands[len(cases)], "pwd", "list", u, &u)
			}
			var public act236Envelope
			if err := json.Unmarshal(pub.Body.Bytes(), &public); err != nil {
				t.Fatal(err)
			}
			if len(public.Steps) != 1 || !reflect.DeepEqual(public.Steps[0].Live, got.Steps[0].Live) {
				t.Errorf("opting in changed the ordinary activity strip: public %+v, private %+v", public.Steps, got.Steps)
			}
			for _, c := range got.Steps[0].Commands {
				found := false
				for _, m := range got.Steps[0].Marks {
					found = found || m.Kind == c.Kind && m.At.Equal(c.At) && reflect.DeepEqual(m.Until, c.Until)
				}
				if !found {
					t.Errorf("command has no matching public strip mark: %+v", c)
				}
			}
		})
	}
}

func act236NoText(t *testing.T, w *httptest.ResponseRecorder, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if strings.Contains(w.Body.String(), marker) {
			t.Errorf("response exposed private text %q: HTTP %d", marker, w.Code)
		}
	}
}

func TestActShellCommandsStayBehindAccess(t *testing.T) {
	f := newAct236Fixture(t, "codex")
	const marker = "PRIVATE_BOUNDARY_236"
	f.append(t, f.start(t, "secret", "printf '"+marker+"'", 1))
	// A real successful private read makes this test fail on the old code,
	// and exercises isolation after any shared read/cache has been populated.
	got := f.read(t)
	if len(got.Steps) != 1 || len(got.Steps[0].Commands) != 1 || !strings.Contains(got.Steps[0].Commands[0].Command, marker) {
		t.Fatal("signed owner opting in did not receive the command")
	}
	h := f.s.Handler()
	for _, path := range []string{"/act/api/live.json", "/act/api/live.json?commands=0", "/act/api/live.json?commands=true"} {
		w := act236Request(h, "GET", path, f.owner)
		if w.Code != 200 || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
			t.Errorf("private live without commands=1: %s HTTP %d, %v", path, w.Code, w.Header())
		}
		act236NoText(t, w, marker, `"commands"`, `"command"`)
	}
	for _, token := range []string{"", f.owner} {
		for _, path := range []string{"/", "/index.html", "/app.js", "/style.css", "/api/live.json", "/api/live.json?commands=1", "/api/state.json?commands=1", "/api/commands.json"} {
			act236NoText(t, act236Request(h, "GET", path, token), marker)
		}
		w := act236Request(h, "GET", "/api/live.json?commands=1", token)
		if w.Code != 200 {
			t.Errorf("public live response: HTTP %d", w.Code)
		}
		act236NoText(t, w, `"commands"`, `"command"`)
	}
	// Direct callers of the existing public serializer are safe too.
	b, err := f.s.LiveJSON(time.Now())
	if err != nil || strings.Contains(string(b), marker) {
		t.Fatalf("public LiveJSON after private read: %s, %v", b, err)
	}
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	bad := map[string]string{
		"absent": "", "malformed": "a.b.c",
		"wrong signature": f.team.token(t, other, "RS256", f.team.claims(nil)),
		"wrong algorithm": f.team.token(t, f.team.key, "HS256", f.team.claims(nil)),
		"expired":         f.team.token(t, f.team.key, "RS256", f.team.claims(map[string]any{"exp": time.Now().Unix() - 1})),
		"not yet valid":   f.team.token(t, f.team.key, "RS256", f.team.claims(map[string]any{"nbf": time.Now().Unix() + 600})),
		"wrong audience":  f.team.token(t, f.team.key, "RS256", f.team.claims(map[string]any{"aud": "other"})),
		"wrong issuer":    f.team.token(t, f.team.key, "RS256", f.team.claims(map[string]any{"iss": "https://elsewhere.example"})),
		"wrong email":     f.team.token(t, f.team.key, "RS256", f.team.claims(map[string]any{"email": "other@example.com"})),
	}
	for name, token := range bad {
		w := act236Request(h, "GET", "/act/api/live.json?commands=1", token)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s token: HTTP %d, want 403", name, w.Code)
		}
		act236NoText(t, w, marker)
	}
	for _, method := range []string{"POST", "PUT", "OPTIONS", "HEAD"} {
		w := act236Request(h, method, "/act/api/live.json?commands=1", f.owner)
		act236NoText(t, w, marker)
	}
	r := httptest.NewRequest("GET", "/act/api/live.json?commands=1", nil)
	r.Header.Set("Authorization", "Bearer agent-secret")
	w := httptest.NewRecorder()
	f.s.AgentHandler("agent-secret").ServeHTTP(w, r)
	if w.Code == 200 {
		t.Error("the agent listener offered the owner's private command response")
	}
	act236NoText(t, w, marker)
	f.s.Access = nil
	w = act236Request(h, "GET", "/act/api/live.json?commands=1", f.owner)
	if w.Code != 404 {
		t.Errorf("Access disabled: HTTP %d, want 404", w.Code)
	}
	act236NoText(t, w, marker)
}

func TestActShellCommandsFollowWholeEventsAndCurrentStep(t *testing.T) {
	for _, provider := range []string{"claude-code", "codex"} {
		t.Run(provider, func(t *testing.T) {
			f := newAct236Fixture(t, provider)
			f.append(t, f.start(t, "first", "go build ./...", 1))
			first := f.read(t)
			if len(first.Steps) != 1 || len(first.Steps[0].Commands) != 1 {
				t.Fatalf("first invocation missing: %+v", first)
			}
			next := f.start(t, "next", "cat app.go", 2)
			cut := len(next) / 2
			f.append(t, next[:cut])
			// Let the live reader's two-second refresh window expire, so this
			// observes the incomplete event rather than merely a cached body.
			time.Sleep(2100 * time.Millisecond)
			partial := f.read(t)
			if !reflect.DeepEqual(first.Steps[0].Commands, partial.Steps[0].Commands) {
				t.Fatalf("partial line changed commands: %+v", partial)
			}
			f.append(t, next[cut:]+next) // duplicate pending call, still one row
			pending := f.eventually(t, "new whole call", func(e act236Envelope) bool {
				return len(e.Steps) == 1 && len(e.Steps[0].Commands) == 2
			})
			act236Want(t, pending.Steps[0].Commands[1], "cat app.go", "read", f.at(2), nil)
			f.append(t, f.done(t, "next", "cat app.go", 4))
			complete := f.eventually(t, "matching result", func(e act236Envelope) bool {
				return len(e.Steps) == 1 && len(e.Steps[0].Commands) == 2 && e.Steps[0].Commands[1].Until != nil
			})
			u := f.at(4)
			act236Want(t, complete.Steps[0].Commands[1], "cat app.go", "read", f.at(2), &u)
			act236Want(t, complete.Steps[0].Commands[0], "go build ./...", "build", f.at(1), nil)
			for i := 0; i < 3; i++ {
				if again := f.read(t); !reflect.DeepEqual(complete.Steps[0].Commands, again.Steps[0].Commands) {
					t.Errorf("unchanged transcript duplicated or altered commands: %+v", again)
				}
			}
			act236Write(t, f.path, f.init(t)+f.start(t, "reset", "pwd", 10))
			reset := f.eventually(t, "truncated transcript", func(e act236Envelope) bool {
				return len(e.Steps) == 1 && len(e.Steps[0].Commands) == 1 && e.Steps[0].Commands[0].Command == "pwd"
			})
			act236Want(t, reset.Steps[0].Commands[0], "pwd", "list", f.at(10), nil)
			f.path = filepath.Join(f.work, "owner/app/issues/issue-236/build-20261029-100020/transcript.jsonl")
			run := *f
			run.since = f.at(20)
			act236Write(t, f.path, run.init(t)+run.start(t, "new-run", "git diff --stat", 0))
			f.status(t, act236WithRun(t, Step{Issue: 236, Doing: "building", Since: f.at(20), Agent: provider}, filepath.Base(filepath.Dir(f.path))))
			f.eventually(t, "new run of the same issue", func(e act236Envelope) bool {
				return len(e.Steps) == 1 && e.Steps[0].Since != nil && e.Steps[0].Since.Equal(f.at(20)) &&
					len(e.Steps[0].Commands) == 1 && e.Steps[0].Commands[0].Command == "git diff --stat"
			})
			f.status(t)
			ended := f.eventually(t, "step ending", func(e act236Envelope) bool {
				return len(e.Steps) == 1 && e.Steps[0].Ended != nil
			})
			if len(ended.Steps[0].Commands) != 0 {
				t.Errorf("ended step retained private commands: %+v", ended)
			}
			act236NoText(t, act236Request(f.s.Handler(), "GET", "/act/api/live.json?commands=1", f.owner), "git diff --stat", `"command"`)
		})
	}
}

func TestActShellCommandsFollowEverySelectedWatcher(t *testing.T) {
	f := newAct236Fixture(t, "codex")
	other := t.TempDir()
	f.s.Works = []string{other, f.work}
	f.s.Repos = append(f.s.Repos, &Repo{Name: "owner/second", Status: StatusPath(f.work, "owner/second")})
	steps := []Step{{Issue: 236, Doing: "building", Since: f.at(0), Agent: "codex"},
		{Issue: 237, Doing: "formalizing", Since: f.at(10), Agent: "codex"}}
	for _, item := range []struct {
		work, repo, holder string
		steps              []Step
	}{
		{f.work, "owner/app", "chosen", steps}, {other, "owner/app", "other", steps},
		{f.work, "owner/second", "chosen", []Step{{Issue: 236, Doing: "building", Since: f.at(5), Agent: "codex"}}},
	} {
		item.steps = append([]Step(nil), item.steps...)
		for i, step := range item.steps {
			prefix := "build-"
			if step.Doing == "formalizing" {
				prefix = "formalize-"
			}
			item.steps[i] = act236WithRun(t, step, prefix+step.Since.Format("20060102-150405"))
		}
		if err := WriteStatus(StatusPath(item.work, item.repo), Status{PID: os.Getpid(), Repo: item.repo, Holder: item.holder, Steps: item.steps}); err != nil {
			t.Fatal(err)
		}
		for _, step := range item.steps {
			prefix := "build-"
			if step.Doing == "formalizing" {
				prefix = "formalize-"
			}
			path := filepath.Join(item.work, item.repo, "issues", fmt.Sprintf("issue-%d", step.Issue), prefix+step.Since.Format("20060102-150405"), "transcript.jsonl")
			command := fmt.Sprintf("printf '%s %s %d'", item.holder, item.repo, step.Issue)
			run := *f
			run.since = step.Since
			act236Write(t, path, run.init(t)+run.start(t, "same-id", command, 1))
		}
	}
	f.s.holding("owner/app", &Lease{Holder: "chosen"})
	got := f.read(t)
	if len(got.Steps) != 3 {
		t.Fatalf("steps = %+v; want all three selected running steps", got.Steps)
	}
	want := []string{"owner/app#236", "owner/second#236", "owner/app#237"}
	for i, step := range got.Steps {
		if key := fmt.Sprintf("%s#%d", step.Repo, step.Issue); key != want[i] {
			t.Errorf("step %d = %s; want %s", i, key, want[i])
		}
		if len(step.Commands) != 1 || step.Commands[0].Command != fmt.Sprintf("printf 'chosen %s %d'", step.Repo, step.Issue) {
			t.Errorf("commands assigned to wrong watcher or step: %+v", step)
		}
	}
	pub := act236Request(f.s.Handler(), "GET", "/api/live.json", "")
	var public act236Envelope
	if err := json.Unmarshal(pub.Body.Bytes(), &public); err != nil {
		t.Fatal(err)
	}
	if len(public.Steps) != len(got.Steps) {
		t.Fatal("public and private step selection differs")
	}
	for i := range public.Steps {
		if !reflect.DeepEqual(public.Steps[i].Live, got.Steps[i].Live) {
			t.Errorf("private step %d differs from public strip", i)
		}
	}
	f.s.holding("owner/app", &Lease{Holder: "other"})
	switched := f.eventually(t, "lease changes owner", func(e act236Envelope) bool {
		return len(e.Steps) == 3 && len(e.Steps[0].Commands) == 1 && e.Steps[0].Commands[0].Command == "printf 'other owner/app 236'"
	})
	for _, step := range switched.Steps {
		holder := "other"
		if step.Repo == "owner/second" {
			holder = "chosen"
		}
		if len(step.Commands) != 1 || step.Commands[0].Command != fmt.Sprintf("printf '%s %s %d'", holder, step.Repo, step.Issue) {
			t.Errorf("lease change mixed command histories: %+v", step)
		}
	}
}

func TestActShellCommandsRetainOnlyTheStripWindow(t *testing.T) {
	for _, provider := range []string{"claude-code", "codex"} {
		t.Run(provider, func(t *testing.T) {
			f := newAct236Fixture(t, provider)
			var log strings.Builder
			// More than the existing strip's 2000 marks, leaving its first
			// invocation pending while later ones finish.
			for i := 0; i < 2010; i++ {
				command := fmt.Sprintf("printf 'WINDOW_236_%04d'", i)
				log.WriteString(f.start(t, fmt.Sprint(i), command, 2*i+1))
				if i != 0 {
					log.WriteString(f.done(t, fmt.Sprint(i), command, 2*i+2))
				}
			}
			f.append(t, log.String())
			got := f.read(t)
			if len(got.Steps) != 1 || len(got.Steps[0].Commands) != 2000 || len(got.Steps[0].Marks) != 2000 {
				t.Fatalf("want the same bounded 2000 commands and strip marks: got %d steps", len(got.Steps))
			}
			for i, c := range got.Steps[0].Commands {
				n := i + 10
				u := f.at(2*n + 2)
				act236Want(t, c, fmt.Sprintf("printf 'WINDOW_236_%04d'", n), "other", f.at(2*n+1), &u)
			}
			f.append(t, f.done(t, "0", "printf 'WINDOW_236_0000'", 5000))
			time.Sleep(2100 * time.Millisecond)
			after := f.read(t)
			if len(after.Steps) != 1 || !reflect.DeepEqual(got.Steps[0].Commands, after.Steps[0].Commands) {
				t.Errorf("completion of a trimmed invocation altered retained commands")
			}
			// Mixed tool kinds consume the same window: non-shell marks must
			// not leave an independent, larger private command history behind.
			var tools strings.Builder
			for i := 0; i < 2000; i++ {
				if provider == "codex" {
					tools.WriteString(act236Line(t, map[string]any{"type": "item.completed", "timestamp": f.at(6000 + i),
						"item": map[string]any{"id": fmt.Sprintf("edit-%d", i), "type": "file_change"}}))
				} else {
					tools.WriteString(act236Line(t, map[string]any{"type": "assistant", "timestamp": f.at(6000 + i),
						"message": map[string]any{"id": fmt.Sprintf("edit-message-%d", i), "content": []map[string]any{{
							"type": "tool_use", "id": fmt.Sprintf("edit-%d", i), "name": "Edit", "input": map[string]any{"file_path": "app.go"}}}}}))
				}
			}
			f.append(t, tools.String())
			f.eventually(t, "shell marks leave the retained strip", func(e act236Envelope) bool {
				return len(e.Steps) == 1 && len(e.Steps[0].Marks) == 2000 && e.Steps[0].Marks[0].Kind == "edit" && len(e.Steps[0].Commands) == 0
			})
		})
	}
}
