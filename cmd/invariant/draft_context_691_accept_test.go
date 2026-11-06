package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/mcp"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/synth"
	"github.com/gitdek/invariant/internal/toolchain"
)

// Provider and checker stand-ins reexec this test binary. The provider uses
// the actual backend-generated MCP configuration; the checker uses the
// production authority loader and check tool, with no verifier needed for
// the authenticated step-fit question. The real CLI is exercised separately.
func TestDraftContext691BackendLaunches(t *testing.T) {
	for i, a := range os.Args {
		if a == "draft691-helper" {
			os.Exit(draft691Child(os.Args[i+1:]))
			return
		}
	}
	for _, backendName := range []string{"codex", "claude-code"} {
		t.Run(backendName, func(t *testing.T) {
			dir := t.TempDir()
			checker := draft691Wrapper(t, dir, "checker", "checker", dir)
			agent := draft691Wrapper(t, dir, "agent", "agent", backendName, dir)
			var backend synth.Backend
			wait := func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
			if backendName == "codex" {
				backend = (synth.Codex{Binary: agent, Home: t.TempDir(), MaxTurns: 20}).WithProviderWait(wait)
			} else {
				backend = (synth.ClaudeCode{Binary: agent, BudgetUSD: 2, MaxTurns: 20}).WithProviderWait(wait)
			}
			view, _ := json.Marshal(map[string]string{"label": strings.Repeat("quote \" slash \\ λ 雪\n", 12000)})
			req := formalize.Request{ParentPlan: 667, Answers: []formalize.Answer{{Fork: "F0", Option: "A"}}, RevisionChoices: []formalize.Revision{{Fork: "F0", Option: "B", By: "owner"}}, Current: &formalize.Current{
				Dir: "examples/691-reader", Commit: "original-691",
				Manifest:   project.Manifest{Name: "captured reader", Module: ".invariant/specs/Reader603.tla", Code: "reader", Language: "go"},
				ModuleText: reader603Model + "\\* \"quoted\" \\ λ 雪\n", Includes: map[string]string{"Helper603.tla": reader603Include}, View: string(view),
				Lock: project.Lock{Decision: "D-0362", Bounds: map[string]string{"Limit": "2"}},
			}}
			want := draft691JSON(t, req.CheckContext())
			if len(want) <= 128*1024 {
				t.Fatal("fixture is too small")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			r, err := (formalize.Formalizer{Backend: backend, Binary: checker, CheckRuns: 3, Timeout: 20 * time.Second}).Formalize(ctx, req, t.TempDir())
			if err != nil || r == nil || r.Problem != "" || r.Usage.Outcome != "success" {
				t.Fatalf("normal backend launch failed: %+v, %v", r, err)
			}
			if !bytes.Equal(draft691JSON(t, r.CheckContext), want) {
				t.Fatal("final check lost caller authority")
			}
			var first formalize.CheckContextReference
			admitted, denied := 0, 0
			for n := 1; n <= 4; n++ {
				got := draft691Read(t, filepath.Join(dir, fmt.Sprintf("recovered.%d.json", n)))
				if !bytes.Equal(got, want) {
					t.Fatalf("checker %d did not recover every captured byte", n)
				}
				var ref formalize.CheckContextReference
				if err := json.Unmarshal(draft691Read(t, filepath.Join(dir, fmt.Sprintf("reference.%d.json", n))), &ref); err != nil {
					t.Fatal(err)
				}
				if n == 1 {
					first = ref
				} else if ref != first {
					t.Fatal("retry or checker restart replaced the run's authority reference")
				}
				var response struct {
					Result struct {
						IsError bool `json:"isError"`
						Content []struct {
							Text string `json:"text"`
						} `json:"content"`
					} `json:"result"`
				}
				if err := json.Unmarshal(draft691Read(t, filepath.Join(dir, fmt.Sprintf("reply.%d.json", n))), &response); err != nil {
					t.Fatal(err)
				}
				if len(response.Result.Content) != 1 {
					t.Fatal("checker did not answer the MCP call")
				}
				if response.Result.IsError {
					if !strings.Contains(response.Result.Content[0].Text, "No checks left") {
						t.Fatal(response.Result.Content[0].Text)
					}
					denied++
				} else {
					admitted++
				}
			}
			if admitted != 3 || denied != 1 {
				t.Fatalf("checker restarts reset the allowance: %d admitted, %d denied", admitted, denied)
			}
			var allowance string
			for attempt := 1; attempt <= 2; attempt++ {
				var args []string
				if err := json.Unmarshal(draft691Read(t, filepath.Join(dir, fmt.Sprintf("args.%d.json", attempt))), &args); err != nil {
					t.Fatal(err)
				}
				_, _, serverEnv, err := draft691Config(backendName, args)
				if err != nil {
					t.Fatal(err)
				}
				if attempt == 1 {
					allowance = serverEnv[synth.ToolAllowanceEnv]
				}
				if allowance == "" || serverEnv[synth.ToolAllowanceEnv] != allowance {
					t.Fatal("provider retry lost the run's allowance")
				}
				for _, value := range serverEnv {
					if len(value) > 4096 || strings.Contains(value, "original-691") {
						t.Fatal("authority payload moved into the MCP environment")
					}
				}
				joined := strings.Join(args, "\n")
				if backendName == "codex" {
					for _, required := range []string{`default_permissions="invariant"`, `permissions.invariant.extends=":workspace"`, `":root"="deny"`, `":tmpdir"="deny"`, `web_search="disabled"`, `enabled_tools=["check"]`, `required=true`} {
						if !strings.Contains(joined, required) {
							t.Fatalf("Codex configuration lost %s", required)
						}
					}
					if strings.Contains(joined, "network_access=true") || strings.Contains(joined, `network.enabled=true`) {
						t.Fatal("agent network restriction was widened")
					}
				} else {
					for _, required := range []string{"--strict-mcp-config", "Read,Write,Edit,Glob,Grep,mcp__invariant__check", "Bash,WebFetch,WebSearch,Task,NotebookEdit,Read(~/**),Edit(~/**),Write(~/**),Glob(~/**),Grep(~/**)"} {
						if !strings.Contains(joined, required) {
							t.Fatalf("Claude Code configuration lost %s", required)
						}
					}
				}
			}
			if _, err := formalize.LoadCheckContext(t.Context(), first, 667); err == nil {
				t.Fatal("completed backend run left authority service running")
			}
		})
	}
}

func draft691JSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func draft691Read(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func draft691Quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func draft691Wrapper(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := append([]string{exe, "-test.run=^TestDraftContext691BackendLaunches$", "--", "draft691-helper"}, args...)
	for i := range command {
		command[i] = draft691Quote(command[i])
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexec "+strings.Join(command, " ")+" \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func draft691Config(backend string, args []string) (command string, serverArgs []string, env map[string]string, err error) {
	env = map[string]string{}
	for i, a := range args {
		if backend == "claude-code" && a == "--mcp-config" && i+1 < len(args) {
			if len(args[i+1]) > 8192 {
				return "", nil, nil, fmt.Errorf("MCP JSON argument has %d bytes", len(args[i+1]))
			}
			var config struct {
				Servers map[string]struct {
					Command string            `json:"command"`
					Args    []string          `json:"args"`
					Env     map[string]string `json:"env"`
				} `json:"mcpServers"`
			}
			if err = json.Unmarshal([]byte(args[i+1]), &config); err != nil {
				return
			}
			if len(config.Servers) != 1 {
				return "", nil, nil, fmt.Errorf("unexpected MCP servers")
			}
			s := config.Servers["invariant"]
			command, serverArgs, env = s.Command, s.Args, s.Env
		}
		if backend != "codex" || i == 0 || args[i-1] != "-c" || !strings.HasPrefix(a, "mcp_servers.invariant") {
			continue
		}
		if len(a) > 8192 {
			return "", nil, nil, fmt.Errorf("MCP TOML argument has %d bytes", len(a))
		}
		key, value, _ := strings.Cut(a, "=")
		fields, e := providerMCPConfigFields(value)
		if e != nil {
			return "", nil, nil, e
		}
		if key == "mcp_servers.invariant" {
			if err = json.Unmarshal([]byte(fields["command"]), &command); err != nil {
				return
			}
			if err = json.Unmarshal([]byte(fields["args"]), &serverArgs); err != nil {
				return
			}
		} else if key == "mcp_servers.invariant.env" {
			for name, literal := range fields {
				var v string
				if err = json.Unmarshal([]byte(literal), &v); err != nil {
					return
				}
				env[name] = v
			}
		}
	}
	if command == "" || len(serverArgs) == 0 {
		err = fmt.Errorf("backend did not configure its checker")
	}
	return
}

func draft691Child(args []string) int {
	fail := func(err error) int { fmt.Fprintln(os.Stderr, err); return 92 }
	if len(args) == 0 {
		return fail(fmt.Errorf("missing fixture mode"))
	}
	if args[0] == "cli" {
		return mcpCmd(context.Background(), args[1:])
	}
	if args[0] == "checker" {
		if len(args) < 3 || args[2] != "mcp" {
			return fail(fmt.Errorf("bad checker launch"))
		}
		dir := args[1]
		fs := flag.NewFlagSet("checker", flag.ContinueOnError)
		formal := fs.Bool("formalize", false, "")
		max := fs.Int("max-runs", 4, "")
		log := fs.String("log", "", "")
		parent := fs.Int("parent-plan", 0, "")
		endpoint := fs.String("draft-context-endpoint", "", "")
		token := fs.String("draft-context-token", "", "")
		digest := fs.String("draft-context-sha256", "", "")
		if err := fs.Parse(args[3:]); err != nil {
			return fail(err)
		}
		if !*formal || fs.NArg() != 1 || *endpoint == "" || *token == "" || *digest == "" {
			return fail(fmt.Errorf("checker did not get complete caller reference"))
		}
		ref := formalize.CheckContextReference{Endpoint: *endpoint, Token: *token, SHA256: *digest}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		authority, err := formalize.LoadCheckContext(ctx, ref, *parent)
		if err != nil {
			return fail(err)
		}
		b, err := json.Marshal(authority)
		if err != nil {
			return fail(err)
		}
		n, err := draft691Count(dir, "checkers")
		if err != nil {
			return fail(err)
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("recovered.%d.json", n)), b, 0600); err != nil {
			return fail(err)
		}
		b, _ = json.Marshal(ref)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("reference.%d.json", n)), b, 0600); err != nil {
			return fail(err)
		}
		server := mcp.Server{Name: "invariant", Version: "691", Tools: []mcp.Tool{checkTool(fs.Arg(0), toolchain.Toolchain{}, *max, *log, *parent, authority)}}
		if err := server.Serve(ctx, os.Stdin, os.Stdout); err != nil {
			return fail(err)
		}
		return 0
	}
	if args[0] != "agent" || len(args) < 4 {
		return fail(fmt.Errorf("bad provider launch"))
	}
	backend, dir, cliArgs := args[1], args[2], args[3:]
	command, serverArgs, env, err := draft691Config(backend, cliArgs)
	if err != nil {
		return fail(err)
	}
	for _, a := range serverArgs {
		if len(a) > 4096 || a == "-draft-context" {
			return fail(fmt.Errorf("unbounded or inline authority argument"))
		}
	}
	if env[synth.ToolAllowanceEnv] == "" {
		return fail(fmt.Errorf("MCP configuration lost tool allowance"))
	}
	n, err := draft691Count(dir, "attempts")
	if err != nil {
		return fail(err)
	}
	b, _ := json.Marshal(cliArgs)
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("args.%d.json", n)), b, 0600); err != nil {
		return fail(err)
	}
	ws := serverArgs[len(serverArgs)-1]
	proposal := `{"forks":[{"id":"F1","kind":"step_fit","question":"The complete step does not fit; stop parent #667 and replan?","options":[{"id":"A","says":"Build the complete step as one change."},{"id":"B","says":"A writer stops parent #667 with /invariant stop and requests a replacement plan on a new top-level issue with /invariant plan."}]}]}`
	if err := os.WriteFile(filepath.Join(ws, "proposal.json"), []byte(proposal), 0600); err != nil {
		return fail(err)
	}
	// The visible file is deliberately wrong on every attempt.
	if err := os.WriteFile(filepath.Join(ws, "check-context.json"), []byte(`{"parent_plan":999}`), 0600); err != nil {
		return fail(err)
	}
	for restart := 0; restart < 2; restart++ {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		cmd := exec.CommandContext(ctx, command, serverArgs...)
		cmd.Env = []string{}
		for _, key := range []string{"PATH", "HOME", "TMPDIR", "USER", "LOGNAME", "SHELL"} {
			if v, ok := os.LookupEnv(key); ok {
				cmd.Env = append(cmd.Env, key+"="+v)
			}
		}
		for key, v := range env {
			cmd.Env = append(cmd.Env, key+"="+v)
		}
		cmd.Stdin = strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"check\",\"arguments\":{}}}\n")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		reply, err := cmd.Output()
		cancel()
		if err != nil {
			return fail(fmt.Errorf("checker launch: %w: %s", err, stderr.String()))
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("reply.%d.json", 2*(n-1)+restart+1)), bytes.TrimSpace(reply), 0600); err != nil {
			return fail(err)
		}
	}
	if backend == "claude-code" {
		fmt.Println(`{"type":"system","subtype":"init","mcp_servers":[{"name":"invariant","status":"connected"}]}`)
	}
	if n == 1 {
		if backend == "codex" {
			fmt.Println(`{"type":"turn.failed","error":{"message":"Selected model is at capacity. Please retry."}}`)
		} else {
			fmt.Println(`{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["API Error: 529 overloaded_error"],"total_cost_usd":0.125,"num_turns":1}`)
		}
		return 1
	}
	if backend == "codex" {
		fmt.Println(`{"type":"turn.completed"}`)
	} else {
		fmt.Println(`{"type":"result","subtype":"success","result":"Done.","total_cost_usd":0.125,"num_turns":1}`)
	}
	return 0
}

func draft691Count(dir, name string) (int, error) {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	n, _ := strconv.Atoi(string(b))
	n++
	return n, os.WriteFile(filepath.Join(dir, name), []byte(strconv.Itoa(n)), 0600)
}

type draft691Agent struct {
	run func(context.Context, synth.Job) (synth.Usage, error)
}

func (a draft691Agent) Name() string { return "offline-691" }
func (a draft691Agent) Run(ctx context.Context, job synth.Job) (synth.Usage, error) {
	return a.run(ctx, job)
}

func TestDraftContext691CLIRefusesInvalidAuthority(t *testing.T) {
	dir := t.TempDir()
	cli := draft691Wrapper(t, dir, "invariant", "cli")
	marker := filepath.Join(dir, "verifier-started")
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte("#!/bin/sh\nprintf attempted > "+draft691Quote(marker)+"\nexit 99\n"), 0700); err != nil {
		t.Fatal(err)
	}
	agent := draft691Agent{run: func(ctx context.Context, job synth.Job) (synth.Usage, error) {
		base := append([]string{}, job.GateServer[2:]...)
		if !strings.Contains(strings.Join(base, " "), "-draft-context-endpoint") {
			t.Fatal("formalizer did not configure the local authority service")
		}
		invoke := func(args []string) (string, error) {
			_ = os.Remove(marker)
			bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			cmd := exec.CommandContext(bounded, cli, args...)
			cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			cmd.Stdin = strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/list\"}\n")
			out, err := cmd.CombinedOutput()
			return string(out), err
		}
		for _, change := range []string{"credential", "digest", "parent", "missing endpoint", "missing token", "missing digest", "wrong mode"} {
			args := append([]string{}, base...)
			for i := 0; i+1 < len(args); i++ {
				switch {
				case change == "credential" && args[i] == "-draft-context-token":
					args[i+1] = "invented-run-credential"
				case change == "digest" && args[i] == "-draft-context-sha256":
					args[i+1] = "sha256:" + strings.Repeat("0", 64)
				case change == "parent" && args[i] == "-parent-plan":
					args[i+1] = "668"
				case change == "missing endpoint" && args[i] == "-draft-context-endpoint", change == "missing token" && args[i] == "-draft-context-token", change == "missing digest" && args[i] == "-draft-context-sha256":
					args = append(args[:i], args[i+2:]...)
					i--
				case change == "wrong mode" && args[i] == "-formalize":
					args[i] = "-issues"
				}
			}
			out, err := invoke(args)
			if err == nil || (!strings.Contains(strings.ToLower(out), "context") && !strings.Contains(strings.ToLower(out), "authority")) || strings.Contains(out, "flag provided but not defined") {
				t.Fatalf("%s was not an authority refusal: %v, %s", change, err, out)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("%s reached verifier setup before authority validation", change)
			}
			if strings.Contains(out, `"tools"`) {
				t.Fatalf("%s exposed a check tool", change)
			}
		}
		// Positive reference and the supported no-context standalone launch
		// reach ordinary verifier preparation (a deliberately failing local fake).
		for _, args := range [][]string{base, {"-formalize", job.Workspace}} {
			out, err := invoke(args)
			if err == nil || !strings.Contains(out, "docker isn't available") {
				t.Fatalf("valid authority did not reach verifier preparation: %v, %s", err, out)
			}
			if _, err := os.Stat(marker); err != nil {
				t.Fatalf("verifier setup was not attempted: %v", err)
			}
		}
		if err := os.WriteFile(filepath.Join(job.Workspace, "proposal.json"), []byte(`{"unsupported":"CLI transport fixture."}`), 0600); err != nil {
			t.Fatal(err)
		}
		return synth.Usage{}, nil
	}}
	r, err := (formalize.Formalizer{Backend: agent, Binary: "invariant"}).Formalize(t.Context(), formalize.Request{ParentPlan: 667}, t.TempDir())
	if err != nil || r == nil || r.Problem != "" {
		t.Fatalf("CLI fixture formalization: %+v, %v", r, err)
	}
	if !reflect.DeepEqual(r.CheckContext, &formalize.CheckContext{ParentPlan: 667, Choices: map[string]string{}}) {
		t.Fatal("CLI tests changed the caller's authority")
	}
}
