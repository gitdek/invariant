package verify

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/gobra"
	"github.com/gitdek/invariant/internal/tlc"
)

func gateCPU622Options(args []string, flag string) []string {
	var out []string
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			out = append(out, args[i+1])
		} else if value, ok := strings.CutPrefix(arg, flag+"="); ok {
			out = append(out, value)
		}
	}
	return out
}

// Observe the real verifier launch paths; only Docker's executable is replaced.
// Verifier output is deliberately small and complete so no timeout or retry is
// needed to observe the launched containers.
func TestGateContainerCPU622(t *testing.T) {
	for _, value := range []string{"", "0.5", "7"} {
		t.Run("cpus="+value, func(t *testing.T) {
			t.Setenv("INVARIANT_CONTAINER_CPUS", value)
			want := 4.0
			if value != "" {
				want, _ = strconv.ParseFloat(value, 64)
			}
			bin, logs, work := t.TempDir(), t.TempDir(), t.TempDir()
			script := `#!/bin/sh
set -eu
printf '%s\000' "$@" > "$GATE_CPU622_LOGS/$$"
case "$1" in
  info) printf '%s\n' '[]'; exit 0 ;;
  rm) exit 0 ;;
  run) ;;
  *) echo 'unexpected Docker command' >&2; exit 89 ;;
esac
case " $* " in
  *" cpu622-tlc "*)
    printf '%s\n' '@!@!@STARTMSG 2193:0 @!@!@' 'Model checking completed. No error has been found.' '@!@!@ENDMSG 2193 @!@!@' ;;
  *" cpu622-gobra "*) printf '%s\n' 'Gobra found 0 errors' ;;
  *" cpu622-tlaps "*)
    printf '%s\n' '@!!BEGIN' '@!!type:obligationsnumber' '@!!count:1' '@!!END' '@!!BEGIN' '@!!type:obligation' '@!!id:1' '@!!status:proved' '@!!prover:z3' '@!!END' ;;
  *" cpu622-nagini "*|*" cpu622-app-nagini "*) printf '%s\n' 'Verification successful' ;;
  *" cpu622-python "*) printf '%s\n' 'application stdout' ;;
  *) echo 'unexpected image' >&2; exit 89 ;;
esac
`
			if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("GATE_CPU622_LOGS", logs)
			for name, text := range map[string]string{
				"Model.tla":     "---- MODULE Model ----\nVARIABLE n\nInit == n = 0\nNext == UNCHANGED n\nSpec == Init /\\ [][Next]_n\n====\n",
				"machine.go":    "// +gobra\npackage machine\nfunc Step() {}\n",
				"tla2tools.jar": "fixture jar",
			} {
				if err := os.WriteFile(filepath.Join(work, name), []byte(text), 0644); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			r, err := (tlc.Runner{Image: "cpu622-tlc", Jar: filepath.Join(work, "tla2tools.jar")}).Check(ctx, work, "Model", tlc.Config{Specification: "Spec"})
			if err != nil || r.Outcome != tlc.Passed {
				t.Fatalf("TLC fixture did not complete: %+v, %v", r, err)
			}
			g, err := gobra.Run(ctx, "cpu622-gobra", work, true)
			if err != nil || !g.Passed {
				t.Fatalf("Gobra fixture did not complete: %+v, %v", g, err)
			}
			out, exit, err := naginiProve(ctx, "cpu622-nagini", "machine.py", []byte("def step():\n    return 1\n"))
			if err != nil || exit != 0 || !strings.Contains(out, "Verification successful") {
				t.Fatalf("Nagini fixture did not complete: %q, %d, %v", out, exit, err)
			}
			proof, err := runTLAPS(ctx, "cpu622-tlaps", work, "Model")
			if err != nil {
				t.Fatal(err)
			}
			if passed, message := proof.proved(); !passed {
				t.Fatalf("TLAPS fixture did not complete: %s", message)
			}
			p, err := RunPythonApplicationNagini(ctx, PythonApplicationNaginiCommand{Image: "cpu622-app-nagini", Dir: work, Main: "machine.py"})
			if err != nil || p.ExitCode != 0 {
				t.Fatalf("application proof fixture did not complete: %+v, %v", p, err)
			}
			output := t.TempDir()
			if err := os.WriteFile(filepath.Join(output, "events.jsonl"), []byte("fixture event bytes\n"), 0600); err != nil {
				t.Fatal(err)
			}
			p, err = RunPythonApplicationCommand(ctx, PythonApplicationCommand{
				Image: "cpu622-python", Root: work, RuntimeDir: t.TempDir(), OutputDir: output, WorkingDir: "/src",
				Interpreter: "/usr/local/bin/python", Args: []string{"-I", "/runtime/runner.py", "literal argument"},
				Env: map[string]string{"HOME": "/tmp", "PYTHONHASHSEED": "0"},
			})
			if err != nil || p.ExitCode != 0 || p.Stdout != "application stdout\n" || string(p.Events) != "fixture event bytes\n" {
				t.Fatalf("application executor changed its output: %+v, %v", p, err)
			}
			entries, err := os.ReadDir(logs)
			if err != nil {
				t.Fatal(err)
			}
			images := []string{"cpu622-tlc", "cpu622-gobra", "cpu622-nagini", "cpu622-tlaps", "cpu622-app-nagini", "cpu622-python"}
			seen := map[string]int{}
			for _, entry := range entries {
				b, err := os.ReadFile(filepath.Join(logs, entry.Name()))
				if err != nil {
					t.Fatal(err)
				}
				args := strings.Split(strings.TrimSuffix(string(b), "\x00"), "\x00")
				if args[0] != "run" {
					if len(gateCPU622Options(args, "--cpus")) != 0 {
						t.Errorf("CPU option on non-launch operation: %q", args)
					}
					continue
				}
				for _, image := range images {
					at := slices.Index(args, image)
					if at < 0 {
						continue
					}
					seen[image]++
					opts := args[:at]
					quota := want
					if image == "cpu622-python" {
						quota = 1
					}
					values := gateCPU622Options(opts, "--cpus")
					if len(values) != 1 {
						t.Errorf("%s must have exactly one CPU cap before the image: %q", image, args)
					} else if got, err := strconv.ParseFloat(values[0], 64); err != nil || got != quota {
						t.Errorf("%s CPU quota %q, want %g", image, values[0], quota)
					}
					keep := map[string]string{"--network": "none"}
					if image == "cpu622-python" {
						keep["--memory"], keep["--pids-limit"], keep["--pull"] = "512m", "128", "never"
					}
					if image == "cpu622-tlc" {
						keep["--memory"], keep["--memory-swap"] = "1024m", "1024m"
						if !slices.Contains(args[at+1:], "-workers") || !slices.Contains(args[at+1:], "-Xmx768m") {
							t.Errorf("TLC lost its worker or heap setting: %q", args)
						}
					}
					for flag, expected := range keep {
						if values := gateCPU622Options(opts, flag); len(values) != 1 || values[0] != expected {
							t.Errorf("%s changed %s=%s: %q", image, flag, expected, args)
						}
					}
				}
			}
			for _, image := range images {
				if seen[image] != 1 {
					t.Errorf("%s launched %d times, want once", image, seen[image])
				}
			}
		})
	}
}
