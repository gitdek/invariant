package plumbing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const dockerIsolationSwitch621 = "--invariant-docker-isolation-621"

func init() {
	if len(os.Args) >= 5 && os.Args[1] == dockerIsolationSwitch621 {
		state, goexe, argv := os.Args[2], os.Args[3], os.Args[4:]
		if len(argv) > 0 && argv[0] == "create" {
			// #591's general-purpose fake shares a test-owned compiler cache for
			// speed. This attack fixture instead models Docker's private cache.
			cache := filepath.Join(state, "containers", flag484(argv, "--name"), "tmp", "gocache")
			if err := os.MkdirAll(cache, 0700); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(125)
			}
			link := filepath.Join(state, "go-cache")
			if err := os.Remove(link); err != nil && !errors.Is(err, os.ErrNotExist) {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(125)
			}
			if err := os.Symlink(cache, link); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(125)
			}
		}
		if err := docker591Command(state, goexe, argv); err != nil {
			fmt.Fprintln(os.Stderr, err)
			var exit *exec.ExitError
			if errors.As(err, &exit) && exit.ExitCode() > 0 {
				os.Exit(exit.ExitCode())
			}
			os.Exit(125)
		}
		os.Exit(0)
	}
}

func TestGoInvocationPristineInputs621(t *testing.T) {
	b := newDocker591(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	script := "#!/bin/sh\nexec " + quote(self) + " " + quote(dockerIsolationSwitch621) + " " + quote(b.root) + " " + quote(b.goexe) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(b.bin, "docker"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	first := `package fixture
import("testing";"os";"path/filepath")
func TestReady(t *testing.T) {
    if err:=os.WriteFile("../later/value.go", []byte("package fixture\nconst Value=7\n"),0600);err!=nil{t.Fatal(err)}
    if err:=os.WriteFile(filepath.Join(os.Getenv("GOCACHE"),"poison621"),[]byte("workload cache bytes"),0600);err!=nil{t.Fatal(err)}
    t.Log("ATTACK_COMPLETED621")
    if Value!=7 {t.Fatal("base needs seven621")}
}
`
	later := `package fixture
import("testing";"os";"path/filepath")
func TestReady(t *testing.T) {
    if _,err:=os.Stat(filepath.Join(os.Getenv("GOCACHE"),"poison621"));!os.IsNotExist(err){t.Fatal("INHERITED_CACHE621")}
    t.Log("AUTHENTIC_LATER621")
    if Value!=7 {t.Fatal("base needs seven621")}
}
`
	f := newDocker591Repository(t, b, []docker591Case{
		{"api", "first", "TestReady", first},
		{"api", "later", "TestReady", later},
	}, false)
	f.sandbox.Timeout, f.sandbox.ShardTimeout = 6*time.Minute, 5*time.Minute
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Minute)
	defer cancel()
	base, problem, err := f.run(ctx, true)
	if err != nil || problem != "" {
		t.Fatalf("base did not retain independent authentic failures: %v %s\n%s", err, problem, base.Summary())
	}
	if !strings.Contains(base.Summary(), "ATTACK_COMPLETED621") || strings.Contains(base.Summary(), "INHERITED_CACHE621") {
		t.Fatalf("earlier workload did not execute or cache writes crossed invocations:\n%s", base.Summary())
	}
	docker591Result(t, base, f.plan.Tests[1], "failed", "AUTHENTIC_LATER621")
	opened, _ := f.runner.requests()
	if len(opened) != 2 || opened[0].Token == opened[1].Token || opened[0].SourceDir == opened[1].SourceDir {
		t.Fatal("real Go selectors reused an environment or mutable input copy")
	}
	b.clean()
	docker591CheckStaging(t, f.runner)
	f.value(7)
	candidate, _, err := f.run(ctx, false)
	if err != nil || !candidate.Passed() || strings.Contains(candidate.Summary(), "INHERITED_CACHE621") {
		t.Fatalf("production-only correction did not execute independently: %v\n%s", err, candidate.Summary())
	}
	docker591Result(t, candidate, f.plan.Tests[1], "passed", "AUTHENTIC_LATER621")
	b.clean()
	docker591CheckStaging(t, f.runner)
}
