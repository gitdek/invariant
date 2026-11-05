package synth

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

func TestCodexMCPContainerCPU622(t *testing.T) {
	for _, value := range []string{"0.5", "2.5", "8"} {
		for _, tool := range []string{"check", "test", "gate"} {
			t.Run(value+"/"+tool, func(t *testing.T) {
				t.Setenv("INVARIANT_CONTAINER_CPUS", value)
				c := Codex{Home: t.TempDir()}
				job := Job{Workspace: t.TempDir(), Prompt: "CPU setting fixture", GateServer: []string{"invariant", "mcp"},
					Tools: []string{tool}, allowance: "127.0.0.1:622"}
				args, err := c.args(job, t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				var serverConfig []string
				for i, arg := range args {
					if i > 0 && args[i-1] == "-c" && strings.HasPrefix(arg, "mcp_servers.invariant") {
						serverConfig = append(serverConfig, arg)
					}
				}
				config := strings.Join(serverConfig, "\n")
				for name, expected := range map[string]string{"INVARIANT_CONTAINER_CPUS": value, ToolAllowanceEnv: job.allowance} {
					pattern := regexp.MustCompile(`"?` + regexp.QuoteMeta(name) + `"?\s*=\s*"([^"]*)"`)
					matches := pattern.FindAllStringSubmatch(config, -1)
					if len(matches) != 1 || matches[0][1] != expected {
						t.Errorf("MCP server must explicitly receive %s=%s exactly once, config: %s", name, expected, config)
					}
				}
				job.ReadOnly = true
				args, err = c.args(job, t.TempDir())
				if err != nil || strings.Contains(strings.Join(args, "\n"), "mcp_servers.invariant") {
					t.Errorf("review job acquired an MCP server: %q, %v", args, err)
				}
			})
		}
	}
}

func TestClaudeMCPContainerCPU622(t *testing.T) {
	for _, value := range []string{"0.5", "2.5", "8"} {
		for _, tool := range []string{"check", "test", "gate"} {
			t.Run(value+"/"+tool, func(t *testing.T) {
				t.Setenv("INVARIANT_CONTAINER_CPUS", value)
				job := Job{Workspace: t.TempDir(), Prompt: "CPU setting fixture", GateServer: []string{"invariant", "mcp"},
					Tools: []string{tool}, allowance: "127.0.0.1:622"}
				args, err := (ClaudeCode{}).args(job)
				if err != nil {
					t.Fatal(err)
				}
				var configs []string
				for i, arg := range args {
					if arg == "--mcp-config" && i+1 < len(args) {
						configs = append(configs, args[i+1])
					}
				}
				if len(configs) != 1 {
					t.Fatalf("expected one MCP configuration, got %q", args)
				}
				var config struct {
					Servers map[string]struct {
						Env map[string]string `json:"env"`
					} `json:"mcpServers"`
				}
				if err := json.Unmarshal([]byte(configs[0]), &config); err != nil {
					t.Fatal(err)
				}
				env := config.Servers["invariant"].Env
				if env["INVARIANT_CONTAINER_CPUS"] != value || env[ToolAllowanceEnv] != job.allowance {
					t.Errorf("MCP environment lost CPU setting or tool allowance: %v", env)
				}
				job.ReadOnly = true
				args, err = (ClaudeCode{}).args(job)
				if err != nil || strings.Contains(strings.Join(args, "\n"), "--mcp-config") {
					t.Errorf("review job acquired an MCP server: %q, %v", args, err)
				}
			})
		}
	}
}
