package toolchain

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPinnedRunAcquisitionFailureIsNotWorkloadExit(t *testing.T) {
	for _, mode := range []string{"forever", "integrity-immediate"} {
		for _, method := range []string{"Run", "Output", "CombinedOutput", "Start/Wait"} {
			t.Run(mode+"/"+method, func(t *testing.T) {
				dir := docker433Setup(t, mode)
				ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
				defer cancel()
				cmd := Docker(ctx, "run", "--rm", JavaImage, "verify")
				input := strings.NewReader("payload")
				cmd.Stdin = input
				var out bytes.Buffer
				var err error
				switch method {
				case "Run":
					cmd.Stdout, cmd.Stderr = &out, &out
					err = cmd.Run()
				case "Output":
					var b []byte
					b, err = cmd.Output()
					out.Write(b)
				case "CombinedOutput":
					var b []byte
					b, err = cmd.CombinedOutput()
					out.Write(b)
				case "Start/Wait":
					cmd.Stdout, cmd.Stderr = &out, &out
					err = cmd.Start()
					if err == nil {
						err = cmd.Wait()
					}
				}
				want, diagnostic := 3, "registry 502 Bad Gateway attempt-3"
				if mode == "integrity-immediate" {
					want, diagnostic = 1, "filesystem layer verification failed for digest sha256:bad"
				}
				if err == nil || !strings.Contains(err.Error(), diagnostic) {
					t.Fatalf("lost acquisition diagnostic: output=%q, error=%v", out.String(), err)
				}
				// TLC and other verifiers parse ExitError as a workload result
				// and discard its error text. Acquisition must take their error path.
				var exit *exec.ExitError
				if errors.As(err, &exit) {
					t.Fatalf("acquisition failure would be parsed as a workload exit: %v", err)
				}
				if strings.Contains(err.Error(), "attempt-1") || strings.Contains(err.Error(), "attempt-2") {
					t.Fatalf("acquisition reported an earlier diagnostic: %v", err)
				}
				if len(docker433OfKind(dir, "pull")) != want || len(docker433OfKind(dir, "run")) != 0 {
					t.Fatalf("unexpected acquisition/workload attempts: %#v", docker433ReadCalls(dir))
				}
				if cmd.ProcessState != nil || out.Len() != 0 || input.Len() != len("payload") {
					t.Fatalf("acquisition changed workload state or IO: state=%v, output=%q, unread=%d", cmd.ProcessState, out.String(), input.Len())
				}
			})
		}
	}
}

func TestPinnedRunUsesCachedRequestedPlatform(t *testing.T) {
	for _, source := range []string{"split", "equals", "environment", "native"} {
		t.Run(source, func(t *testing.T) {
			dir := t.TempDir()
			log := filepath.Join(dir, "calls")
			// Both variants are cached. Without a selector Docker reports the
			// native arm64 variant, even though amd64 is available offline too.
			script := `#!/bin/sh
printf '%s\n' "$*" >> "$INVARIANT_433_PLATFORM_CALLS"
case "$1" in
image)
  [ "$2" = inspect ] || exit 2
  platform=linux/arm64/v8
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --platform) shift; platform="$1" ;;
      --platform=*) platform="${1#--platform=}" ;;
    esac
    shift
  done
  case "$platform" in
    linux/amd64|linux/arm64|linux/arm64/v8) printf '%s\n' "$platform" ;;
    *) exit 1 ;;
  esac
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
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("INVARIANT_433_PLATFORM_CALLS", log)
			t.Setenv("DOCKER_DEFAULT_PLATFORM", "")
			args := []string{"run", "--rm"}
			switch source {
			case "split":
				t.Setenv("DOCKER_DEFAULT_PLATFORM", "linux/arm64")
				args = append(args, "--platform", "linux/amd64")
			case "equals":
				args = append(args, "--platform=linux/amd64")
			case "environment":
				t.Setenv("DOCKER_DEFAULT_PLATFORM", "linux/amd64")
			}
			args = append(args, JavaImage, "verify")
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			cmd := Docker(ctx, args...)
			cmd.Stdin = strings.NewReader("payload")
			out, err := cmd.Output()
			if err != nil || string(out) != "verified:payload" {
				t.Fatalf("cached platform was not usable offline: %q, %v", out, err)
			}
			calls, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(calls)), "\n")
			if len(lines) != 2 || !strings.HasPrefix(lines[0], "image inspect ") || !strings.HasPrefix(lines[1], "run ") {
				t.Fatalf("cached platform triggered acquisition or repeated work: %s", calls)
			}
		})
	}
}
