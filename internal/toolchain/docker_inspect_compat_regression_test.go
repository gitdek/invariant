package toolchain

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPinnedRunFallsBackFromUnsupportedPlatformInspection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake docker is a shell script")
	}
	for _, test := range []struct {
		name        string
		diagnostic  string
		platform    string
		cached      string
		environment bool
		wantPulls   int
	}{
		{name: "older client", diagnostic: "unknown flag: --platform", platform: "linux/amd64", cached: "linux/amd64"},
		{name: "older daemon", platform: "linux/amd64", cached: "linux/amd64"},
		{name: "default platform", platform: "linux/amd64", cached: "linux/amd64", environment: true},
		{name: "cached variant", platform: "linux/arm64", cached: "linux/arm64/v8"},
		{name: "wrong architecture", platform: "linux/amd64", cached: "linux/arm64/v8", wantPulls: 3},
		{name: "wrong variant", platform: "linux/arm/v7", cached: "linux/arm/v6", wantPulls: 3},
		{name: "missing image", platform: "linux/amd64", wantPulls: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			log := filepath.Join(dir, "calls")
			// Older Docker can run a cached platform but cannot select it in
			// image inspect. The registry is unavailable throughout the test.
			script := `#!/bin/sh
printf '%s\n' "$*" >> "$INVARIANT_INSPECT_COMPAT_CALLS"
case "$1" in
image)
  [ "$2" = inspect ] || exit 2
  for arg in "$@"; do
    case "$arg" in
      --platform|--platform=*)
        printf '%s\n' "$INVARIANT_INSPECT_COMPAT_ERROR" >&2
        exit 1
        ;;
    esac
  done
  if [ -z "$INVARIANT_INSPECT_COMPAT_CACHED" ]; then
    echo 'No such image' >&2
    exit 1
  fi
  printf '%s\n' "$INVARIANT_INSPECT_COMPAT_CACHED"
  ;;
pull)
  echo 'registry unavailable' >&2
  exit 1
  ;;
run)
  printf 'verified:'
  cat
  ;;
*) exit 2 ;;
esac
`
			if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			diagnostic := test.diagnostic
			if diagnostic == "" {
				diagnostic = "--platform requires API version 1.49, but the Docker daemon API version is 1.48"
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("INVARIANT_INSPECT_COMPAT_CALLS", log)
			t.Setenv("INVARIANT_INSPECT_COMPAT_ERROR", diagnostic)
			t.Setenv("INVARIANT_INSPECT_COMPAT_CACHED", test.cached)
			t.Setenv("DOCKER_DEFAULT_PLATFORM", "")
			args := []string{"run", "--rm"}
			if test.environment {
				t.Setenv("DOCKER_DEFAULT_PLATFORM", test.platform)
			} else {
				args = append(args, "--platform", test.platform)
			}
			args = append(args, JavaImage, "verify")
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			cmd := Docker(ctx, args...)
			cmd.Stdin = strings.NewReader("payload")
			out, err := cmd.Output()
			if test.wantPulls == 0 {
				if err != nil || string(out) != "verified:payload" {
					t.Fatalf("cached platform was not usable offline: %q, %v", out, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "registry unavailable") || len(out) != 0 || cmd.ProcessState != nil {
				t.Fatalf("missing platform did not fail acquisition: output=%q, error=%v, state=%v", out, err, cmd.ProcessState)
			}
			calls, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			var pulls, runs int
			for _, call := range strings.Split(strings.TrimSpace(string(calls)), "\n") {
				switch {
				case strings.HasPrefix(call, "image inspect "):
					if !strings.HasSuffix(call, " "+JavaImage) {
						t.Errorf("inspected a different image: %s", call)
					}
				case strings.HasPrefix(call, "pull "):
					pulls++
					if !strings.Contains(call, "--platform "+test.platform+" ") || !strings.HasSuffix(call, " "+JavaImage) {
						t.Errorf("acquisition changed the pin or platform: %s", call)
					}
				case strings.HasPrefix(call, "run "):
					runs++
				default:
					t.Errorf("unexpected Docker call: %s", call)
				}
			}
			wantRuns := 1
			if test.wantPulls != 0 {
				wantRuns = 0
			}
			if pulls != test.wantPulls || runs != wantRuns {
				t.Fatalf("pulls=%d, runs=%d; want %d, %d: %s", pulls, runs, test.wantPulls, wantRuns, calls)
			}
		})
	}
}
