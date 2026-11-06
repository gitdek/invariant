package formalize_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/synth"
)

func context691Request() formalize.Request {
	// These are source strings, so quoting, Unicode and whitespace must survive
	// transport byte for byte, not merely produce an equivalent parsed model.
	large := strings.Repeat("quote \" slash \\ λ 雪\n", 12000)
	view, _ := json.Marshal(map[string]any{"version": 1, "state": []any{map[string]any{"id": "all", "when": true, "label": large}}})
	return formalize.Request{
		ParentPlan:      667,
		Answers:         []formalize.Answer{{Fork: "F0", Option: "A", By: "owner"}},
		RevisionChoices: []formalize.Revision{{Fork: "F0", Option: "B", By: "owner"}},
		Current: &formalize.Current{
			Commit: "captured-691", Dir: "examples/691-reader",
			Manifest:   project.Manifest{Name: "captured reader", Module: ".invariant/specs/Reader603.tla", Code: "reader", Language: "go"},
			ModuleText: reader603Model + "\\* \"captured\" \\ λ 雪\n",
			Includes:   map[string]string{"Helper603.tla": reader603Include + "\\* \"include\" \\ λ 雪\n"},
			View:       string(view),
			Lock:       project.Lock{Decision: "D-0362", Bounds: map[string]string{"Limit": "2"}, Statements: []project.Statement{{Name: "Safe", Kind: "invariant", Says: "Preserve \"source\" \\ λ 雪"}}},
		},
	}
}

func context691JSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func context691Reference(t *testing.T, job synth.Job) formalize.CheckContextReference {
	t.Helper()
	flags := map[string]string{}
	for i, a := range job.GateServer {
		if a == "-draft-context" {
			t.Fatal("caller authority is still embedded in process arguments")
		}
		if len(a) > 4096 {
			t.Fatalf("checker argument has %d bytes", len(a))
		}
		if strings.HasPrefix(a, "-draft-context-") && i+1 < len(job.GateServer) {
			flags[a] = job.GateServer[i+1]
		}
	}
	r := formalize.CheckContextReference{Endpoint: flags["-draft-context-endpoint"], Token: flags["-draft-context-token"], SHA256: flags["-draft-context-sha256"]}
	u, err := url.Parse(r.Endpoint)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || len(r.Token) < 32 || len(r.SHA256) != 71 || !strings.HasPrefix(r.SHA256, "sha256:") {
		t.Fatalf("missing short authenticated loopback reference: endpoint=%q, token length=%d, digest=%q", r.Endpoint, len(r.Token), r.SHA256)
	}
	return r
}

func context691HTTP(t *testing.T, r formalize.CheckContextReference, method, token string, body []byte) (int, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, r.Endpoint, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	tr := &http.Transport{Proxy: nil}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 3 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, b
}

func context691Closed(t *testing.T, r formalize.CheckContextReference, parent int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := formalize.LoadCheckContext(ctx, r, parent); err == nil {
		t.Fatal("authority service outlived its run")
	}
}

func TestDraftContext691CapturedService(t *testing.T) {
	req := context691Request()
	want := context691JSON(t, req.CheckContext())
	if len(want) <= 128*1024 {
		t.Fatal("fixture does not exceed Linux's per-argument limit")
	}
	var ref formalize.CheckContextReference
	agent := reader603Agent{run: func(ctx context.Context, job synth.Job) (synth.Usage, error) {
		ref = context691Reference(t, job)
		if ref.SHA256 != fmt.Sprintf("sha256:%x", sha256.Sum256(want)) {
			t.Fatal("launch digest does not bind the captured bytes")
		}
		// Mutate every caller-owned reference after capture and the workspace's
		// guidance, without modifying the service's retained capture.
		req.Current.ModuleText = "replacement"
		req.Current.Includes["Helper603.tla"] = "replacement"
		req.Current.Lock.Bounds["Limit"] = "99"
		req.Answers[0].Option = "C"
		req.RevisionChoices[0].Option = "C"
		for _, name := range []string{"check-context.json", "baseline/Reader603.tla", "baseline/ratified.lock", "previous/proposal.json", "reader.json"} {
			reader603Write(t, filepath.Join(job.Workspace, name), `{}`)
		}
		for i := 0; i < 3; i++ {
			status, raw := context691HTTP(t, ref, "GET", ref.Token, nil)
			if status != 200 || !bytes.Equal(raw, want) {
				t.Fatalf("fetch %d lost exact captured bytes (status %d)", i, status)
			}
			got, err := formalize.LoadCheckContext(ctx, ref, 667)
			if err != nil || !bytes.Equal(context691JSON(t, got), want) {
				t.Fatalf("checker restart %d lost authority: %v", i, err)
			}
			got.Choices["F0"] = "mutated client copy"
			got.Baseline.Includes["Helper603.tla"] = "mutated client copy"
		}
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
			status, _ := context691HTTP(t, ref, method, ref.Token, []byte(`{"parent_plan":999}`))
			if status < 400 {
				t.Fatalf("service accepts mutation method %s", method)
			}
		}
		for _, token := range []string{"", ref.Token + "invented"} {
			status, raw := context691HTTP(t, ref, "GET", token, nil)
			if status < 400 || bytes.Contains(raw, []byte("captured-691")) {
				t.Fatal("service disclosed capture without its run credential")
			}
		}
		status, raw := context691HTTP(t, ref, "GET", ref.Token, nil)
		if status != 200 || !bytes.Equal(raw, want) {
			t.Fatal("a denied write changed the capture")
		}
		reader603Write(t, filepath.Join(job.Workspace, "proposal.json"), `{"unsupported":"Transport fixture, no model check required."}`)
		return synth.Usage{}, nil
	}}
	r, err := (formalize.Formalizer{Backend: agent, Binary: "invariant"}).Formalize(t.Context(), req, t.TempDir())
	if err != nil || r == nil || r.Problem != "" {
		t.Fatalf("formalization: %+v, %v", r, err)
	}
	if r.CheckContext == nil || !bytes.Equal(context691JSON(t, r.CheckContext), want) {
		t.Fatal("final validation lost the detached caller-memory context")
	}
	context691Closed(t, ref, 667)
}

func TestDraftContext691RevisionsAndFinalCheck(t *testing.T) {
	tc, _ := reader603Tools(t)
	for _, authorized := range []bool{true, false} {
		t.Run(fmt.Sprint(authorized), func(t *testing.T) {
			req := formalize.Request{ParentPlan: 667, Answers: []formalize.Answer{{Fork: "F0", Option: "A", By: "owner"}}}
			revision := formalize.Revision{Fork: "F0", Option: "B", By: "owner"}
			if authorized {
				req.RevisionChoices = []formalize.Revision{revision}
			}
			var ref formalize.CheckContextReference
			agent := reader603Agent{run: func(ctx context.Context, job synth.Job) (synth.Usage, error) {
				ref = context691Reference(t, job)
				reader603Write(t, filepath.Join(job.Workspace, "check-context.json"), `{"parent_plan":999,"choices":{"F0":"B"},"revisions":[{"fork":"F0","option":"B","by":"owner"}]}`)
				reader603Write(t, filepath.Join(job.Workspace, "previous/proposal.json"), `{"choices":{"F0":"B"}}`)
				d := preview639Draft(t, 1, map[string]string{"F0": "B"})
				d["revised"] = []formalize.Revision{revision}
				preview639Write(t, job.Workspace, d)
				authority, err := formalize.LoadCheckContext(ctx, ref, 667)
				if err != nil {
					t.Fatal(err)
				}
				if authority.Choices["F0"] != "A" || authority.ParentPlan != 667 {
					t.Fatal("guidance replaced caller authority")
				}
				_, _, fetchedErr := formalize.CheckWithContext(ctx, job.Workspace, tc, authority)
				_, _, directErr := formalize.Check(ctx, job.Workspace, tc)
				if (fetchedErr == nil) != authorized || (directErr == nil) != authorized {
					t.Fatalf("authorization: fetched=%v, direct=%v", fetchedErr, directErr)
				}
				reader603Write(t, filepath.Join(job.Workspace, "reader.json"), `{"comparison":{"baseline":{"model":"forged"}}}`)
				return synth.Usage{}, nil
			}}
			r, err := (formalize.Formalizer{Backend: agent, Toolchain: tc, Binary: "invariant"}).Formalize(t.Context(), req, t.TempDir())
			if err != nil || r == nil || (r.Problem == "") != authorized {
				t.Fatalf("independent final check: %+v, %v", r, err)
			}
			if !authorized && !strings.Contains(r.Problem, "no caller authorization") {
				t.Fatalf("wrong refusal: %s", r.Problem)
			}
			context691Closed(t, ref, 667)
		})
	}
}

func TestDraftContext691ConcurrentRunsAndCleanup(t *testing.T) {
	// Hold two normal formalizations open at once, then cancel one while the
	// other remains usable. Neither service is tied to a single check process.
	type running struct {
		ref     formalize.CheckContextReference
		cancel  context.CancelFunc
		release chan struct{}
		done    chan error
	}
	start := func(parent int) running {
		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)
		ready := make(chan formalize.CheckContextReference, 1)
		release := make(chan struct{})
		done := make(chan error, 1)
		out := t.TempDir()
		agent := reader603Agent{run: func(ctx context.Context, job synth.Job) (synth.Usage, error) {
			ref := context691Reference(t, job)
			ready <- ref
			select {
			case <-release:
				reader603Write(t, filepath.Join(job.Workspace, "proposal.json"), `{"unsupported":"Run finished."}`)
				return synth.Usage{}, nil
			case <-ctx.Done():
				// Check cleanup while the backend has not yet returned.
				deadline := time.Now().Add(time.Second)
				for {
					_, err := formalize.LoadCheckContext(context.Background(), ref, parent)
					if err != nil {
						break
					}
					if time.Now().After(deadline) {
						t.Error("canceled caller still serves authority before its backend returns")
						return synth.Usage{}, errors.New("canceled caller still serves authority")
					}
					time.Sleep(5 * time.Millisecond)
				}
				return synth.Usage{}, ctx.Err()
			}
		}}
		go func() {
			_, err := (formalize.Formalizer{Backend: agent}).Formalize(ctx, formalize.Request{ParentPlan: parent, Answers: []formalize.Answer{{Fork: "F0", Option: fmt.Sprint(parent)}}}, out)
			done <- err
		}()
		select {
		case ref := <-ready:
			return running{ref, cancel, release, done}
		case <-time.After(3 * time.Second):
			t.Fatal("formalization did not launch a service")
			return running{}
		}
	}
	a, b := start(667), start(668)
	if a.ref.Token == b.ref.Token || a.ref.Endpoint == b.ref.Endpoint {
		t.Fatal("independent runs share a credential or endpoint")
	}
	for _, pair := range []struct {
		ref    formalize.CheckContextReference
		parent int
	}{{a.ref, 667}, {b.ref, 668}} {
		got, err := formalize.LoadCheckContext(t.Context(), pair.ref, pair.parent)
		if err != nil || got.Choices["F0"] != fmt.Sprint(pair.parent) {
			t.Fatalf("crossed captures: %+v, %v", got, err)
		}
	}
	crossed := a.ref
	crossed.Token = b.ref.Token
	if _, err := formalize.LoadCheckContext(t.Context(), crossed, 667); err == nil {
		t.Fatal("another run's credential was accepted")
	}
	a.cancel()
	select {
	case <-a.done:
	case <-time.After(3 * time.Second):
		t.Fatal("canceled formalization did not finish")
	}
	context691Closed(t, a.ref, 667)
	if _, err := formalize.LoadCheckContext(t.Context(), b.ref, 668); err != nil {
		t.Fatalf("canceling another run closed this one: %v", err)
	}
	close(b.release)
	select {
	case err := <-b.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("finished formalization did not join cleanup")
	}
	context691Closed(t, b.ref, 668)

	var failedRef formalize.CheckContextReference
	setupFailure := reader603Agent{run: func(_ context.Context, job synth.Job) (synth.Usage, error) {
		failedRef = context691Reference(t, job)
		return synth.Usage{}, errors.New("backend setup failed before launching an agent")
	}}
	_, _ = (formalize.Formalizer{Backend: setupFailure}).Formalize(t.Context(), formalize.Request{}, t.TempDir())
	context691Closed(t, failedRef, 0)
}

func TestDraftContext691FetchFailures(t *testing.T) {
	// Fully absent transport is the only way to ask for standalone context.
	standalone, err := formalize.LoadCheckContext(t.Context(), formalize.CheckContextReference{}, 667)
	if err != nil || !reflect.DeepEqual(standalone, formalize.CheckContext{ParentPlan: 667}) {
		t.Fatalf("standalone check changed: %+v, %v", standalone, err)
	}
	valid := []byte(`{"parent_plan":667,"choices":{"F0":"A"}}`)
	for _, test := range []struct {
		name, body     string
		status, parent int
		digest         string
	}{
		{"substituted", `{"parent_plan":667,"choices":{"F0":"B"}}`, 200, 667, "original"},
		{"wrong parent", string(valid), 200, 668, ""},
		{"wrong digest", string(valid), 200, 667, "wrong"},
		{"malformed", `{`, 200, 667, ""},
		{"null", `null`, 200, 0, ""},
		{"array", `[]`, 200, 0, ""},
		{"incomplete", `{"parent_plan":`, 200, 667, ""},
		{"trailing object", string(valid) + `{}`, 200, 667, ""},
		{"service error", string(valid), 503, 667, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer fixture-run-credential-691" {
					w.WriteHeader(401)
					return
				}
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			defer s.Close()
			bound := []byte(test.body)
			if test.digest == "original" {
				bound = valid
			}
			if test.digest == "wrong" {
				bound = []byte("another capture")
			}
			ref := formalize.CheckContextReference{Endpoint: s.URL, Token: "fixture-run-credential-691", SHA256: fmt.Sprintf("sha256:%x", sha256.Sum256(bound))}
			if _, err := formalize.LoadCheckContext(t.Context(), ref, test.parent); err == nil {
				t.Fatal("invalid supplied authority became a check context")
			}
		})
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-run-credential-691" {
			w.WriteHeader(401)
			return
		}
		_, _ = w.Write(valid)
	}))
	ref := formalize.CheckContextReference{Endpoint: s.URL, Token: "fixture-run-credential-691", SHA256: fmt.Sprintf("sha256:%x", sha256.Sum256(valid))}
	defer s.Close()
	for mask := 1; mask < 7; mask++ {
		partial := ref
		if mask&1 != 0 {
			partial.Endpoint = ""
		}
		if mask&2 != 0 {
			partial.Token = ""
		}
		if mask&4 != 0 {
			partial.SHA256 = ""
		}
		if _, err := formalize.LoadCheckContext(t.Context(), partial, 667); err == nil {
			t.Fatalf("partial reference %d became standalone authority", mask)
		}
	}
	wrong := ref
	wrong.Token = "invented-run"
	if _, err := formalize.LoadCheckContext(t.Context(), wrong, 667); err == nil {
		t.Fatal("wrong credential accepted")
	}
	s.Close()
	if _, err := formalize.LoadCheckContext(t.Context(), ref, 667); err == nil {
		t.Fatal("unavailable service became empty authority")
	}

	for _, deadline := range []time.Duration{50 * time.Millisecond, 0} {
		t.Run(fmt.Sprintf("stalled-%s", deadline), func(t *testing.T) {
			arrived := make(chan struct{}, 1)
			slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(200)
				_, _ = io.WriteString(w, `{"parent_plan":`)
				w.(http.Flusher).Flush()
				arrived <- struct{}{}
				<-r.Context().Done()
			}))
			defer slow.Close()
			ref.Endpoint = slow.URL
			// An outer watchdog makes a missing production timeout fail locally,
			// rather than leaving this acceptance process blocked indefinitely.
			ctx, watchdog := context.WithTimeout(t.Context(), 9*time.Second)
			defer watchdog()
			if deadline > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, deadline)
				defer cancel()
			}
			started := time.Now()
			_, err := formalize.LoadCheckContext(ctx, ref, 667)
			if err == nil {
				t.Fatal("unfinished response became authority")
			}
			select {
			case <-arrived:
			default:
				t.Fatal("fetch never reached the service")
			}
			limit := 7 * time.Second
			if deadline > 0 {
				limit = 2 * time.Second
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("fetch lost caller deadline: %v", err)
				}
			}
			if time.Since(started) > limit {
				t.Fatalf("fetch did not bound startup: %s", time.Since(started))
			}
		})
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := formalize.LoadCheckContext(canceled, ref, 667); !errors.Is(err, context.Canceled) {
		t.Fatalf("fetch lost cancellation: %v", err)
	}
}
