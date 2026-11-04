package plumbing

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
)

func flagValue538(argv []string, name string) string {
	for i, a := range argv {
		if a == name {
			if name == "-short" || name == "-v" || name == "-race" || name == "-trimpath" {
				return "true"
			}
			if i+1 < len(argv) {
				return argv[i+1]
			}
		}
		if v, ok := strings.CutPrefix(a, name+"="); ok {
			return v
		}
	}
	return ""
}

func TestGoProducerPreservesCapturedFlags(t *testing.T) {
	ctx, cache := context538(t), t.TempDir()
	cases := [][]string{
		{"-tags", "included538,extra538", "-timeout=0", "-parallel", "2", "-p=1", "-cpu", "2", "-shuffle=off", "-mod", "readonly", "-short=false", "-v=false", "-race=false", "-trimpath=false"},
		{"-tags=included538,extra538", "-timeout", "13s", "-parallel=3", "-p", "2", "-cpu=1", "-shuffle", "on", "-mod=vendor", "-count", "1", "-short", "-v", "-race=false", "-trimpath"},
		{"-tags=included538,extra538", "-timeout=0", "-parallel=2", "-p=1", "-cpu=1", "-shuffle=-37", "-mod=readonly", "-count=1", "-short=true", "-v=true", "-race=false", "-trimpath=true"},
		{"-tags", "included538,extra538", "-timeout", "0", "-parallel", "2", "-p", "1", "-cpu", "1", "-shuffle", "+37", "-mod", "vendor", "-short=false", "-v=false", "-race=false", "-trimpath=false"},
	}
	for i, race := range []string{"-race=true", "-race"} {
		flags := slices.Clone(cases[i+2])
		flags[slices.Index(flags, "-race=false")] = race
		cases = append(cases, flags)
	}
	for index, flags := range cases {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			boolValue := func(flag string) bool { return flagValue538(flags, flag) == "true" }
			source := fmt.Sprintf(`//go:build included538 && extra538

package fixture
import ("testing"; "flag"; "runtime"; "path/filepath")
var calls int
func TestReady(t *testing.T){
 calls++; if calls!=1 {t.Fatalf("count changed: %%d",calls)}
 if testing.Short()!=%t || testing.Verbose()!=%t {t.Fatal("short/verbosity changed")}
 if RaceEnabled!=%t {t.Fatal("race build constraint changed")}
 _,file,_,_:=runtime.Caller(0)
 if filepath.IsAbs(file)==%t {t.Fatalf("trimpath changed: %%s",file)}
 for key,want:=range map[string]string{"test.parallel":%q,"test.cpu":%q,"test.timeout":%q,"test.count":"1"} {
  f:=flag.Lookup(key);if f==nil || f.Value.String()!=want {t.Fatalf("%%s = %%v, want %%s",key,f,want)}
 }
 if runtime.GOMAXPROCS(0)!=%s {t.Fatal("cpu policy changed")}
 t.Log("captured flags reached real testing")
}
`, boolValue("-short"), boolValue("-v"), boolValue("-race"), boolValue("-trimpath"), flagValue538(flags, "-parallel"), flagValue538(flags, "-cpu"), map[bool]string{true: "0s", false: "13s"}[flagValue538(flags, "-timeout") == "0"], flagValue538(flags, "-cpu"))
			f := fixtureFor538(t, cache, source)
			write538(t, f.root, "api/nested/race_on.go", "//go:build race\n\npackage fixture\nconst RaceEnabled=true\n")
			write538(t, f.root, "api/nested/race_off.go", "//go:build !race\n\npackage fixture\nconst RaceEnabled=false\n")
			argv := []string{"go", "test", "./...", "-run", "^{test}$"}
			if index%2 == 1 {
				argv = []string{"go", "test", "-run={test}", "./..."}
			}
			argv = append(argv, flags...)
			f.part.Commands["test_one"] = command538(argv)
			selection, err := (GoRepositoryAcceptanceAdapter{}).Select(ctx, f.part, f.test)
			if err != nil {
				t.Fatal(err)
			}
			in := repositoryInvocation(&Plan{Sources: map[string]string{f.test.File: source}, Execution: &Execution{Digest: dependencyHash([]byte("captured execution policy 538"))}}, f.test, selection)
			control, controlErr := control538(t, f, ctx, in)
			if controlErr != nil {
				t.Fatalf("control process infrastructure: %v", controlErr)
			}
			run, startErr := f.start(ctx)
			if control.ExitCode != 0 {
				// The local platform may not support -race (e.g. CGO_ENABLED=0).
				// This is a comparison with a real Go refusal, never a skip or an excuse
				// for the producer to refuse a flag that ordinary Go could execute.
				if !boolValue("-race") {
					t.Fatalf("ordinary Go rejected supported flags: %s%s", control.Stdout, control.Stderr)
				}
				diagnostic := string(control.Stdout) + string(control.Stderr)
				if !strings.Contains(diagnostic, "race requires cgo") && !strings.Contains(diagnostic, "race is not supported") {
					t.Fatalf("control failed for a reason other than toolchain race support: %s", diagnostic)
				}
				if startErr != nil {
					var detail *RepositoryAcceptanceDiscoveryError
					if !errors.As(startErr, &detail) || detail.Kind != "setup" || !strings.Contains(startErr.Error(), "race") || len(f.calls) == 0 {
						t.Fatalf("producer replaced the actual Go refusal with another policy: %v", startErr)
					}
				}
				if startErr == nil && run != nil {
					got, err := run.Wait(ctx)
					if err == nil && got.Output.ExitCode == 0 {
						t.Fatal("instrumentation dropped race or another failing flag")
					}
					if len(got.Events) != 0 {
						t.Fatalf("toolchain error became framework events: %+v", got.Events)
					}
				} else if startErr == nil {
					t.Fatal("missing run without refusal")
				}
				return
			}
			if startErr != nil {
				t.Fatalf("ordinary Go passed, producer refused flags %v: %v", flags, startErr)
			}
			got, err := run.Wait(ctx)
			if err != nil {
				t.Fatal(err)
			}
			terminal538(t, got, "TestReady", "pass")
			if got.Output.ExitCode != 0 || !strings.Contains(text538(got), "captured flags reached real testing") {
				t.Fatalf("changed flag semantics: %+v", got)
			}
			if !slices.Equal(got.Invocation.Argv, selection.Argv) {
				t.Fatal("captured flag spelling/order/boundaries changed")
			}
			// Real loading and compiler calls must retain captured build flag policy,
			// including false booleans. Comparing only Select's output is insufficient.
			buildFlags := goDiscoveryBuildFlags(selection.Argv)
			listed, compiled := false, false
			for _, call := range f.calls {
				if len(call) < 2 || call[0] != "go" {
					continue
				}
				if call[1] == "list" {
					listed = true
				}
				if (call[1] == "test" || call[1] == "build" || call[1] == "list") && slices.Contains(call, selection.Package) {
					if call[1] != "list" {
						compiled = true
					}
					if !slices.Equal(goDiscoveryBuildFlags(call), buildFlags) {
						t.Fatalf("real tool lost literal build policy: %q vs %q", call, buildFlags)
					}
				}
			}
			if !listed || !compiled {
				t.Fatal("flag case bypassed real loading/compilation")
			}
			shuffle := flagValue538(flags, "-shuffle")
			if shuffle != "off" {
				want := "-test.shuffle "
				if n, err := strconv.ParseInt(shuffle, 10, 64); err == nil {
					want += strconv.FormatInt(n, 10)
				}
				if !strings.Contains(string(got.Output.Stdout), want) {
					t.Fatalf("shuffle policy did not reach testing: %q", got.Output.Stdout)
				}
			}
		})
	}
}

// Keep the command data literal at all call sites.
func command538(argv []string) repository.Command {
	return repository.Command{Argv: slices.Clone(argv)}
}
