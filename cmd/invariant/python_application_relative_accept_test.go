package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/verify"
)

func TestPythonApplicationRelativeCLI(t *testing.T) {
	// As in #327's CLI test, stop at Docker setup in the offline test. The
	// verify package separately executes real Python with the transport double.
	t.Run("offline preflight", func(t *testing.T) {
		bin := t.TempDir()
		log := filepath.Join(t.TempDir(), "docker-calls")
		t.Setenv("PA333_CLI_LOG", log)
		if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$PA333_CLI_LOG\"\nexit 73\n"), 0755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		for _, explicit := range []bool{true, false} {
			t.Run(fmt.Sprintf("explicit=%v", explicit), func(t *testing.T) {
				dir, _ := pa327CLIFixture(t, explicit)
				root := filepath.Dir(filepath.Dir(dir))
				t.Chdir(filepath.Dir(root))
				relative := filepath.Join(filepath.Base(root), "verification/lifecycle")
				for _, arg := range []string{dir, relative} {
					if err := os.WriteFile(log, nil, 0644); err != nil {
						t.Fatal(err)
					}
					code, out := agentCLI(t, "verify", "-out", "receipt", arg)
					if code != 2 || !strings.Contains(out, "docker isn't available") {
						t.Fatalf("verify -out receipt %q did not pass preflight: exit=%d %s", arg, code, out)
					}
					calls, err := os.ReadFile(log)
					if err != nil || strings.TrimSpace(string(calls)) != "info" {
						t.Fatalf("expected Docker setup after preflight: %q %v", calls, err)
					}
				}
			})
		}
	})
	// Merger work when Docker/TLC/network are unavailable. Enabling this must
	// fail on missing prerequisites, never skip or turn failure into success.
	if os.Getenv("INVARIANT_PYTHON_APPLICATION_GATE_REAL") == "1" {
		t.Run("real receipts", func(t *testing.T) {
			for _, explicit := range []bool{true, false} {
				t.Run(fmt.Sprintf("explicit=%v", explicit), func(t *testing.T) {
					dir, _ := pa327CLIFixture(t, explicit)
					root := filepath.Dir(filepath.Dir(dir))
					t.Chdir(filepath.Dir(root))
					relative := filepath.Join(filepath.Base(root), "verification/lifecycle")
					var absolute *verify.Report
					for _, arg := range []string{dir, relative} {
						code, out := agentCLI(t, "verify", "-out", "receipt", arg)
						if code != 0 {
							t.Fatalf("real verify %q: exit=%d %s", arg, code, out)
						}
						raw, err := os.ReadFile("receipt/receipt.json")
						if err != nil {
							t.Fatal(err)
						}
						var r verify.Report
						if err := json.Unmarshal(raw, &r); err != nil {
							t.Fatal(err)
						}
						if !r.Passed || r.Application == nil || len(r.Application.Executions) == 0 || r.Fingerprint == "" || verify.Fingerprint(r) != r.Fingerprint {
							t.Fatalf("invalid successful receipt: %s", raw)
						}
						md, err := os.ReadFile("receipt/receipt.md")
						if err != nil || string(md) != receipt.Markdown(&r) || !strings.Contains(string(md), "sampled") {
							t.Fatalf("missing or inconsistent Markdown: %s %v", md, err)
						}
						if absolute == nil {
							absolute = &r
						} else if r.Fingerprint != absolute.Fingerprint {
							t.Fatalf("CLI path spelling changed portable fingerprint: %s != %s", r.Fingerprint, absolute.Fingerprint)
						}
					}
				})
			}
		})
	}
}
