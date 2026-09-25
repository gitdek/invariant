package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestServe(t *testing.T) {
	calls := 0
	s := Server{Name: "invariant", Version: "test", Tools: []Tool{{
		Name: "gate", Description: "run the gate", Schema: map[string]any{"type": "object"},
		Call: func(ctx context.Context, args json.RawMessage) (string, bool) {
			calls++
			return "The gate passed.", false
		},
	}}}
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"gate","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"resources/list"}`,
		`not json`,
	}, "\n")
	var out bytes.Buffer
	if err := s.Serve(context.Background(), strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 5 {
		t.Fatalf("got %d responses; want 5 (the notification gets none):\n%s", len(lines), out.String())
	}
	want := []string{
		`"protocolVersion":"2025-06-18"`,
		`"name":"gate"`,
		`"text":"The gate passed."`,
		`"code":-32601`,
		`"code":-32700`,
	}
	for i, w := range want {
		if !strings.Contains(lines[i], w) {
			t.Errorf("response %d = %s; want it to contain %s", i+1, lines[i], w)
		}
	}
	if calls != 1 {
		t.Errorf("the tool ran %d times; want 1", calls)
	}
}
