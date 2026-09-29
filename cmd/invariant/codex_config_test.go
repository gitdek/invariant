package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Codex doesn't read .mcp.json, so .codex/config.toml starts the decision
// graph's server for Codex in a trusted checkout, and the README gives the
// configuration for a Codex that starts anywhere else (#153). Both must
// start the server .mcp.json starts, so the three can't drift apart.
func TestCodexStartsTheDecisionServerMCPJSONStarts(t *testing.T) {
	root := filepath.Join("..", "..")
	var mcp struct {
		Servers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(read(t, filepath.Join(root, ".mcp.json")), &mcp); err != nil {
		t.Fatal(err)
	}
	want, ok := mcp.Servers["invariant-decisions"]
	if !ok {
		t.Fatal(".mcp.json starts no invariant-decisions server")
	}
	readme := ""
	for _, block := range tomlBlocks(string(read(t, filepath.Join(root, "README.md")))) {
		if strings.Contains(block, "[mcp_servers.invariant-decisions]") {
			readme = block
		}
	}
	for _, c := range []struct {
		where, toml string
		cwd         bool
	}{
		{".codex/config.toml", string(read(t, filepath.Join(root, ".codex", "config.toml"))), false},
		{"the README's configuration for Codex", readme, true},
	} {
		v := codexServer(c.toml, "invariant-decisions")
		if v == nil {
			t.Errorf("%s starts no invariant-decisions server", c.where)
			continue
		}
		var command string
		var args []string
		if json.Unmarshal([]byte(v["command"]), &command) != nil || json.Unmarshal([]byte(v["args"]), &args) != nil {
			t.Errorf("%s gives the server's command as %s %s, which isn't a string and a list of strings", c.where, v["command"], v["args"])
			continue
		}
		if command != want.Command || !slices.Equal(args, want.Args) {
			t.Errorf("%s starts %s %q, but .mcp.json starts %s %q", c.where, command, args, want.Command, want.Args)
		}
		// The first go run builds the CLI, so the server needs longer than
		// Codex's default ten seconds to start.
		var wait int
		if json.Unmarshal([]byte(v["startup_timeout_sec"]), &wait) != nil || wait < 60 {
			t.Errorf("%s gives the server %q seconds to start; the first go run needs at least 60", c.where, v["startup_timeout_sec"])
		}
		var cwd string
		if c.cwd && (json.Unmarshal([]byte(v["cwd"]), &cwd) != nil || cwd == "") {
			t.Errorf("%s names no checkout for the server to start in", c.where)
		}
	}
}

func read(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// tomlBlocks are the text of a Markdown file's toml code blocks.
func tomlBlocks(md string) []string {
	var blocks []string
	var b strings.Builder
	in := false
	for _, line := range strings.Split(md, "\n") {
		switch l := strings.TrimSpace(line); {
		case !in && l == "```toml":
			in = true
			b.Reset()
		case in && l == "```":
			in = false
			blocks = append(blocks, b.String())
		case in:
			b.WriteString(l + "\n")
		}
	}
	return blocks
}

// codexServer is the raw value of each key in a Codex configuration's
// [mcp_servers.NAME] table, or nil if it has none. It reads only the one-line
// keys these files use, whose strings, lists of strings and numbers read as
// JSON.
func codexServer(toml, name string) map[string]string {
	var v map[string]string
	for _, line := range strings.Split(toml, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "["):
			if v != nil {
				return v
			}
			if line == "[mcp_servers."+name+"]" {
				v = map[string]string{}
			}
		case v != nil:
			if key, value, ok := strings.Cut(line, "="); ok {
				v[strings.TrimSpace(key)] = strings.TrimSpace(value)
			}
		}
	}
	return v
}
