package toolchain_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/toolchain"
)

const cpu622Setting = "INVARIANT_CONTAINER_CPUS"

func cpu622Docker(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	log := filepath.Join(bin, "argv")
	script := `#!/bin/sh
set -eu
printf '%s\000' "$@" > "$CPU622_ARGV"
printf '%s\n' 'fixture stdout'
printf '%s\n' 'fixture stderr' >&2
`
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CPU622_ARGV", log)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func cpu622Run(t *testing.T, args ...string) []string {
	t.Helper()
	log := cpu622Docker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := toolchain.Docker(ctx, args...)
	out, err := cmd.Output()
	if err != nil || string(out) != "fixture stdout\n" {
		t.Fatalf("Docker command did not retain its result: %v, %q", err, out)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(b), "\x00"), "\x00")
}

func cpu622Values(args []string, flag string) []string {
	var values []string
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			values = append(values, args[i+1])
		} else if value, ok := strings.CutPrefix(arg, flag+"="); ok {
			values = append(values, value)
		}
	}
	return values
}

func cpu622Quota(t *testing.T, args []string, image string, want float64) {
	t.Helper()
	at := slices.Index(args, image)
	if at < 1 {
		t.Fatalf("missing image operand: %q", args)
	}
	values := cpu622Values(args[:at], "--cpus")
	if len(values) != 1 {
		t.Errorf("container must have exactly one CPU quota before the image, got %q in %q", values, args)
		return
	}
	got, err := strconv.ParseFloat(values[0], 64)
	if err != nil || got != want {
		t.Errorf("CPU quota = %q, want %g: %v", values[0], want, err)
	}
}

func TestContainerCPUDefault622(t *testing.T) {
	for _, unset := range []bool{true, false} {
		t.Run(strconv.FormatBool(unset), func(t *testing.T) {
			t.Setenv(cpu622Setting, "")
			if unset {
				if err := os.Unsetenv(cpu622Setting); err != nil {
					t.Fatal(err)
				}
			}
			const image = "cpu622-local-image"
			workload := []string{"literal command", "--cpus", "0", "--cpus=0", "two words;$(literal)"}
			args := []string{"run", "--rm", "--pull=never", "--network", "none", "--memory", "2g", "--pids-limit", "512", "-e", "LITERAL=--cpus", image}
			got := cpu622Run(t, append(args, workload...)...)
			cpu622Quota(t, got, image, 4)
			at := slices.Index(got, image)
			if at >= 0 && !reflect.DeepEqual(got[at+1:], workload) {
				t.Errorf("changed workload arguments: %q", got[at+1:])
			}
			for flag, want := range map[string]string{"--network": "none", "--memory": "2g", "--pids-limit": "512", "--pull": "never"} {
				if values := cpu622Values(got[:at], flag); len(values) != 1 || values[0] != want {
					t.Errorf("%s changed: %q", flag, got)
				}
			}
		})
	}
	// These do not start workload containers and must not receive run options.
	for _, args := range [][]string{{"info"}, {"image", "inspect", "local"}, {"pull", "local"}, {"rm", "--force", "owned"}, {"exec", "owned", "true"}, {"build", "-t", "local", "-"}} {
		got := cpu622Run(t, args...)
		if len(cpu622Values(got, "--cpus")) != 0 {
			t.Errorf("CPU run option added to %q", got)
		}
	}
}

func TestContainerCPUOverride622(t *testing.T) {
	for _, value := range []string{"0.01", "0.5", "2.5", "8"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(cpu622Setting, value)
			want, _ := strconv.ParseFloat(value, 64)
			const image = "cpu622-override-image"
			got := cpu622Run(t, "run", "--pull", "never", "--entrypoint", "/bin/sh", "--", image, "--cpus", "17")
			cpu622Quota(t, got, image, want)
			at := slices.Index(got, image)
			if at < 1 || got[at-1] != "--" || !slices.Equal(got[at+1:], []string{"--cpus", "17"}) {
				t.Errorf("image separator or workload changed: %q", got)
			}
			for _, explicit := range [][]string{{"--cpus", "1"}, {"--cpus=1"}} {
				args := append([]string{"run", "--pull=never"}, explicit...)
				got := cpu622Run(t, append(args, image, "true")...)
				cpu622Quota(t, got, image, 1)
			}
		})
	}
}

func TestContainerCPUInvalid622(t *testing.T) {
	for _, value := range []string{"0", "-1", "0.001", "NaN", "Inf", "1e309", "bad", "1,5", " "} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(cpu622Setting, value)
			log := cpu622Docker(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			// A pinned image also verifies rejection before acquisition.
			cmd := toolchain.Docker(ctx, "run", "image@sha256:"+strings.Repeat("a", 64), "true")
			err := cmd.Run()
			if err == nil || !strings.Contains(err.Error(), cpu622Setting) {
				t.Errorf("invalid CPU setting %q did not return a named configuration error: %v", value, err)
			}
			if b, err := os.ReadFile(log); !os.IsNotExist(err) {
				t.Errorf("invalid CPU setting invoked Docker before refusal: %q, %v", b, err)
			}
			if err := toolchain.Docker(ctx, "rm", "--force", "owned-container").Run(); err != nil {
				t.Errorf("invalid CPU setting blocked independent cleanup: %v", err)
			}
		})
	}
}
