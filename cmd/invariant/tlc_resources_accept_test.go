package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTLC497VerifyFlag(t *testing.T) {
	_, help := agentCLI(t, "verify", "-h")
	text := strings.ToLower(help)
	if !strings.Contains(text, "-tlc-parallel") || !strings.Contains(text, "automatic") {
		t.Errorf("verify help lacks automatic TLC concurrency limit: %s", help)
	}
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	t.Setenv("TLC497_CLI_LOG", log)
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$TLC497_CLI_LOG\"\nexit 71\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	missing := filepath.Join(t.TempDir(), "missing-project")
	for _, n := range []string{"0", "2"} {
		code, out := agentCLI(t, "verify", "-tlc-parallel", n, missing)
		if code != 2 || !strings.Contains(out, "missing-project") || strings.Contains(out, "flag provided but not defined") {
			t.Errorf("valid TLC limit %s did not reach project preflight: code=%d %s", n, code, out)
		}
	}
	code, out := agentCLI(t, "verify", "-tlc-parallel", "-1", missing)
	text = strings.ToLower(out)
	if code != 2 || !strings.Contains(text, "tlc-parallel") || strings.Contains(text, "flag provided but not defined") || strings.Contains(text, "missing-project") {
		t.Errorf("negative TLC limit was not refused before project/tool setup: code=%d %s", code, out)
	}
	if data, err := os.ReadFile(log); err == nil {
		t.Errorf("flag/preflight validation invoked Docker: %s", data)
	}
}
