package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/toolchain"
	"github.com/gitdek/invariant/internal/verify"
)

func init() {
	if os.Getenv("INVARIANT_RECEIPT615_CLI_DOCKER") == "1" {
		args := os.Args[1:]
		all := " " + strings.Join(args, " ") + " "
		if len(args) > 0 && args[0] == "run" && strings.Contains(all, " "+toolchain.GobraImage+" ") {
			if !strings.Contains(all, " --overflow ") || !strings.Contains(all, " --network none ") {
				os.Exit(91)
			}
			fmt.Println("Gobra found 0 errors")
			os.Exit(0)
		}
		if len(args) > 0 && args[0] == "run" && strings.Contains(all, " "+toolchain.GoImage+" ") {
			if !strings.Contains(all, " --network none ") || !strings.Contains(all, "TestInvariantAgreement") {
				os.Exit(92)
			}
			status := "0"
			if os.Getenv("INVARIANT_RECEIPT615_CLI_BUILD_FAIL") == "1" {
				status = "1"
			}
			fmt.Printf("@@invariant vet=0\n@@invariant test=%s\ninvariant-agreement states=3 depth=3\n@@invariant agree=0\n", status)
			os.Exit(0)
		}
		os.Exit(reader603Docker(args))
	}
}

func TestReader615VerifyCLIArtifacts(t *testing.T) {
	tc, _ := reader603Tools(t)
	tc.GoImage, tc.GobraImage = toolchain.GoImage, toolchain.GobraImage
	bin := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nexport INVARIANT_RECEIPT615_CLI_DOCKER=1\nexec '" + strings.ReplaceAll(exe, "'", "'\"'\"'") + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	// Intended command-boundary seam: main.go declares
	// var verifyToolchain = toolchain.Ensure and verifyCmd uses it at its
	// existing preparation point. The actual flag/output/exit path is exercised;
	// production still always authenticates its toolchain through Ensure.
	old := verifyToolchain
	verifyToolchain = func(context.Context) (toolchain.Toolchain, error) { return tc, nil }
	t.Cleanup(func() { verifyToolchain = old })
	dir := t.TempDir()
	m := project.Manifest{Name: "CLI receipt", Module: ".invariant/specs/Reader603.tla", Code: "machine", Language: "go"}
	lock := project.Lock{Decision: "receipt-fixture", Bounds: map[string]string{"Limit": "2"}, Statements: []project.Statement{{Name: "Spec", Kind: project.Spec, Says: "Advance, reset or poll."}, {Name: "Safe", Kind: project.Invariant, Says: "The count stays within its limit."}}}
	for i := range lock.Statements {
		h, err := tla.PinHash(reader603Model, lock.Statements[i].Name, project.Model)
		if err != nil {
			t.Fatal(err)
		}
		lock.Statements[i].SHA256 = h
	}
	for name, body := range map[string]string{
		".invariant/invariant.json": string(reader603JSON(t, m)), ".invariant/ratified.lock": string(reader603JSON(t, lock)),
		".invariant/view.json": reader603View, ".invariant/specs/Reader603.tla": reader603Model, ".invariant/specs/Helper603.tla": reader603Include,
		"go.mod": "module example/cli\n\ngo 1.27.1\n", "machine/machine.go": "package machine\ntype State int\nfunc Init() State {return 0}\nfunc Successors(s State) []State {return []State{(s+1)%3}}\n",
	} {
		reader603Write(t, filepath.Join(dir, name), body)
	}
	out := filepath.Join(t.TempDir(), "out", "cli-receipt")
	if code := verifyCmd(t.Context(), []string{"-out", out, dir}); code != 0 {
		t.Fatalf("verify exit = %d", code)
	}
	read := func(name string) []byte {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(out, name))
		if err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
		return b
	}
	var r verify.Report
	if err := json.Unmarshal(read("receipt.json"), &r); err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Reader json.RawMessage `json:"reader"`
	}
	if err := json.Unmarshal(read("receipt.json"), &wire); err != nil {
		t.Fatal(err)
	}
	var fromReport, standalone map[string]any
	if err := json.Unmarshal(wire.Reader, &fromReport); err != nil {
		t.Fatalf("JSON receipt lost reader: %v", err)
	}
	if err := json.Unmarshal(read("reader.json"), &standalone); err != nil || !reflect.DeepEqual(fromReport, standalone) {
		t.Fatalf("standalone reader differs: %v", err)
	}
	var reader struct {
		Chart *statechart.Result `json:"chart"`
	}
	if err := json.Unmarshal(read("reader.json"), &reader); err != nil || reader.Chart == nil {
		t.Fatalf("no ordinary chart: %v", err)
	}
	svg, err := reader.Chart.SVG()
	if err != nil {
		t.Fatal(err)
	}
	if string(read("reader.svg")) != svg || strings.Contains(svg, "<script") {
		t.Fatal("CLI did not use standalone native SVG")
	}
	if string(read("receipt.md")) != receipt.Markdown(&r) || !strings.Contains(string(read("receipt.md")), "stateDiagram-v2") {
		t.Fatal("CLI Markdown differs from receipt rendering")
	}
	// The unchanged CI workflow uploads all files below out, then merges and
	// archives that tree; SVG and JSON must stay within that existing tree.
	workflow, err := os.ReadFile(filepath.Join("..", "..", ".github/workflows/gate.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(workflow), "path: out") < 3 || !strings.Contains(string(workflow), "merge-multiple: true") {
		t.Fatal("receipt archive no longer retains the whole output tree")
	}
	t.Setenv("INVARIANT_RECEIPT615_CLI_BUILD_FAIL", "1")
	if code := verifyCmd(t.Context(), []string{"-out", out, dir}); code != 1 {
		t.Fatalf("chart upgraded failed gate: exit %d", code)
	}
	read("reader.svg")
	t.Setenv("INVARIANT_RECEIPT615_CLI_BUILD_FAIL", "")
	for _, view := range []string{`{"version":1,"state":[]}`, ""} {
		if view == "" {
			if err := os.Remove(filepath.Join(dir, ".invariant/view.json")); err != nil {
				t.Fatal(err)
			}
		} else {
			reader603Write(t, filepath.Join(dir, ".invariant/view.json"), view)
		}
		if code := verifyCmd(t.Context(), []string{"-out", out, dir}); code != 0 {
			t.Fatalf("explanatory absence changed passing gate: exit %d", code)
		}
		if _, err := os.Stat(filepath.Join(out, "reader.svg")); !os.IsNotExist(err) {
			t.Fatal("stale SVG survived a chartless run")
		}
		var current struct {
			Reader struct {
				Chart       *statechart.Result `json:"chart"`
				Unavailable json.RawMessage    `json:"unavailable"`
			} `json:"reader"`
		}
		if err := json.Unmarshal(read("receipt.json"), &current); err != nil || current.Reader.Chart != nil || len(current.Reader.Unavailable) == 0 {
			t.Fatalf("chartless receipt retained stale chart: %v", err)
		}
		if strings.Contains(string(read("reader.json")), `"chart"`) {
			t.Fatal("standalone JSON kept obsolete chart")
		}
	}
	blocked := filepath.Join(t.TempDir(), "file")
	reader603Write(t, blocked, "not a directory")
	if code := verifyCmd(t.Context(), []string{"-out", blocked, dir}); code != 2 {
		t.Fatalf("output failure should exit 2, got %d", code)
	}
}
