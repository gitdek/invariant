package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// A server whose client has gone stops the call it's in, such as a gate
// run, rather than finish it for no one.
func TestAnOrphanedServerStopsItsCall(t *testing.T) {
	wasCheck, wasOrphaned := orphanCheck, orphaned
	orphanCheck, orphaned = 10*time.Millisecond, func() bool { return true }
	t.Cleanup(func() { orphanCheck, orphaned = wasCheck, wasOrphaned })
	stopped := make(chan bool, 1)
	s := &Server{Name: "test", Version: "0", Tools: []Tool{{Name: "gate", Schema: map[string]any{"type": "object"},
		Call: func(ctx context.Context, _ json.RawMessage) (string, bool) {
			select {
			case <-ctx.Done():
				stopped <- true
				return "stopped", true
			case <-time.After(10 * time.Second):
				stopped <- false
				return "finished for no one", false
			}
		}}}}
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"gate","arguments":{}}}` + "\n")
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- s.Serve(context.Background(), in, &out) }()
	select {
	case ok := <-stopped:
		if !ok {
			t.Fatal("the call ran to the end with its client gone")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the call wasn't stopped")
	}
	<-done
}

// A server whose client is there answers as before, even a client that
// closes its input right after its requests.
func TestAServerWithItsClientFinishesItsCall(t *testing.T) {
	wasCheck, wasOrphaned := orphanCheck, orphaned
	orphanCheck, orphaned = 10*time.Millisecond, func() bool { return false }
	t.Cleanup(func() { orphanCheck, orphaned = wasCheck, wasOrphaned })
	s := &Server{Name: "test", Version: "0", Tools: []Tool{{Name: "gate", Schema: map[string]any{"type": "object"},
		Call: func(ctx context.Context, _ json.RawMessage) (string, bool) {
			time.Sleep(50 * time.Millisecond)
			return "passed", ctx.Err() != nil
		}}}}
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"gate","arguments":{}}}` + "\n")
	var out bytes.Buffer
	if err := s.Serve(context.Background(), in, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "passed") || strings.Contains(out.String(), `"isError":true`) {
		t.Errorf("the answer: %s", out.String())
	}
}
