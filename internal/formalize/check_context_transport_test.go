package formalize_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/synth"
)

func loadDraftContext(t *testing.T, ctx context.Context, job synth.Job, parent int) formalize.CheckContext {
	t.Helper()
	var ref formalize.CheckContextReference
	for i, arg := range job.GateServer {
		if arg == "-draft-context" || len(arg) > 4096 {
			t.Fatal("inline or oversized caller context argument")
		}
		if i+1 >= len(job.GateServer) {
			continue
		}
		switch arg {
		case "-draft-context-endpoint":
			ref.Endpoint = job.GateServer[i+1]
		case "-draft-context-token":
			ref.Token = job.GateServer[i+1]
		case "-draft-context-sha256":
			ref.SHA256 = job.GateServer[i+1]
		}
	}
	if ref.Endpoint == "" || ref.Token == "" || ref.SHA256 == "" {
		t.Fatal("check process lost its caller context reference")
	}
	authority, err := formalize.LoadCheckContext(ctx, ref, parent)
	if err != nil {
		t.Fatal(err)
	}
	return authority
}

func TestCheckContextRefusesRedirectsAndNonLoopbackEndpoints(t *testing.T) {
	body := []byte(`{"parent_plan":667,"choices":{"F0":"A"}}`)
	var forwarded atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded.Add(1)
		_, _ = w.Write(body)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirect.Close()
	ref := formalize.CheckContextReference{Endpoint: redirect.URL, Token: "run-credential", SHA256: fmt.Sprintf("sha256:%x", sha256.Sum256(body))}
	if _, err := formalize.LoadCheckContext(t.Context(), ref, 667); err == nil {
		t.Fatal("a redirect substituted the caller's service")
	}
	if forwarded.Load() != 0 {
		t.Fatal("fetch forwarded the run credential")
	}
	for _, endpoint := range []string{"https://127.0.0.1:1234", "http://localhost:1234", "http://192.0.2.1:1234", "http://127.0.0.1", "http://127.0.0.1:0", "http://127.0.0.1:65536", "http://user@127.0.0.1:1234", target.URL + "?token=elsewhere", target.URL + "#fragment"} {
		ref.Endpoint = endpoint
		if _, err := formalize.LoadCheckContext(t.Context(), ref, 667); err == nil {
			t.Errorf("accepted endpoint %q", endpoint)
		}
	}
	if forwarded.Load() != 0 {
		t.Fatal("invalid endpoint reached the service")
	}
}
