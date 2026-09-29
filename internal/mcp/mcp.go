// Package mcp is a minimal Model Context Protocol server over stdio: enough to
// offer a coding agent a few tools, with no dependencies.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"time"
)

// Tool is one tool the server offers.
type Tool struct {
	Name        string
	Description string
	Schema      map[string]any // JSON Schema for the arguments
	Call        func(ctx context.Context, args json.RawMessage) (text string, isError bool)
}

// Server answers MCP requests with its tools.
type Server struct {
	Name, Version string
	Tools         []Tool
}

type request struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Serve answers newline-delimited JSON-RPC requests from in until it closes.
// Notifications, which carry no id, get no answer. A server whose client
// has gone stops the call it's in, rather than finish it for no one: an
// agent's run that's stopped can leave its server behind, holding a gate
// run and its containers.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		for t := time.NewTicker(orphanCheck); ; {
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
				if orphaned() {
					cancel()
					t.Stop()
					return
				}
			}
		}
	}()
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 1<<20), 64<<20)
	enc := json.NewEncoder(out)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			if err := enc.Encode(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error"}}); err != nil {
				return err
			}
			continue
		}
		if len(req.ID) == 0 {
			continue
		}
		result, rpcErr := s.handle(ctx, req)
		if err := enc.Encode(response{JSONRPC: "2.0", ID: req.ID, Result: result, Error: rpcErr}); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// orphanCheck is how often Serve checks that its client is still there.
var orphanCheck = 5 * time.Second

// orphaned says whether the server's client has gone: a process whose parent
// exits is taken on by the system's first process, launchd or init.
var orphaned = func() bool { return os.Getppid() == 1 }

func (s *Server) handle(ctx context.Context, req request) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		if p.ProtocolVersion == "" {
			p.ProtocolVersion = "2025-06-18"
		}
		return map[string]any{
			"protocolVersion": p.ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": s.Name, "version": s.Version},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		tools := make([]map[string]any, 0, len(s.Tools))
		for _, t := range s.Tools {
			tools = append(tools, map[string]any{"name": t.Name, "description": t.Description, "inputSchema": t.Schema})
		}
		return map[string]any{"tools": tools}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{-32602, "invalid params"}
		}
		for _, t := range s.Tools {
			if t.Name == p.Name {
				text, isError := t.Call(ctx, p.Arguments)
				return map[string]any{
					"content": []map[string]any{{"type": "text", "text": text}},
					"isError": isError,
				}, nil
			}
		}
		return nil, &rpcError{-32602, "unknown tool: " + p.Name}
	default:
		return nil, &rpcError{-32601, "method not found: " + req.Method}
	}
}
