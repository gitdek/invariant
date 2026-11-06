package repository

import "testing"

// A file that supplies a coding agent's instructions or tools is trusted in
// any directory and whatever its case, the override files the agents read
// in place of AGENTS.md and CLAUDE.md included. Lookalikes stay ordinary.
func TestAgentFilesAreTrustedWhateverTheirCase(t *testing.T) {
	for name, want := range map[string]bool{
		"AGENTS.md":                 true,
		"app/AGENTS.md":             true,
		"app/agents.md":             true,
		"AGENTS.override.md":        true,
		"app/Agents.Override.md":    true,
		"CLAUDE.md":                 true,
		"app/Claude.md":             true,
		"CLAUDE.local.md":           true,
		"app/claude.LOCAL.md":       true,
		".mcp.json":                 true,
		"app/.MCP.json":             true,
		".codex/config.toml":        true,
		"app/.Codex/rules.md":       true,
		".claude/settings.json":     true,
		"app/.CLAUDE/commands/x.md": true,
		"app/main.go":               false,
		"docs/agents.txt":           false,
		"agents.md.bak":             false,
		"app/my-agents.md":          false,
		".codexrc":                  false,
		"notes/claude.markdown":     false,
		"app/codex/README.md":       false,
		"app/claude/settings.json":  false,
	} {
		if got := agentPath(name); got != want {
			t.Errorf("agentPath(%q) = %v, want %v", name, got, want)
		}
	}
}
