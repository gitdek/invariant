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

func TestDockerRunAcquisitionFailurePreservesDiagnostics(t *testing.T) {
	for _, failure := range []struct {
		name       string
		diagnostic string
		pulls      string
	}{
		{"registry", "registry 502 Bad Gateway attempt-3", "3\n"},
		{"integrity", "filesystem layer verification failed for digest sha256:bad", "1\n"},
	} {
		t.Run(failure.name, func(t *testing.T) {
			for _, method := range []string{"Run", "Output", "Output captured stderr", "CombinedOutput", "Start/Wait"} {
				t.Run(method, func(t *testing.T) {
					dir := dockerAcquisitionDiagnosticSetup(t, failure.name)
					ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
					defer cancel()
					cmd := Docker(ctx, "run", "--rm", JavaImage, "verify")
					var stdout, stderr bytes.Buffer
					var output []byte
					var err error
					switch method {
					case "Run":
						cmd.Stdout, cmd.Stderr = &stdout, &stderr
						err = cmd.Run()
					case "Output":
						cmd.Stderr = &stderr
						output, err = cmd.Output()
					case "Output captured stderr":
						output, err = cmd.Output()
					case "CombinedOutput":
						output, err = cmd.CombinedOutput()
					case "Start/Wait":
						cmd.Stdout, cmd.Stderr = &stdout, &stderr
						err = cmd.Start()
						if err == nil {
							err = cmd.Wait()
						}
					}
					var exit *exec.ExitError
					if !errors.As(err, &exit) || exit.ExitCode() != 1 {
						t.Fatalf("acquisition error = %v; want Docker's exit status 1", err)
					}
					if ctx.Err() != nil {
						t.Fatalf("acquisition exhausted the test deadline: %v", ctx.Err())
					}
					// Verifiers consume these streams after recognizing an ExitError;
					// a diagnostic available only through err.Error() is lost to them.
					diagnostic := stderr.String()
					if method == "CombinedOutput" {
						diagnostic = string(output)
					} else if method == "Output captured stderr" {
						diagnostic = string(exit.Stderr)
					}
					if !strings.Contains(diagnostic, failure.diagnostic) {
						t.Errorf("diagnostic = %q; want %q", diagnostic, failure.diagnostic)
					}
					if method != "CombinedOutput" && (stdout.Len() != 0 || len(output) != 0) {
						t.Errorf("acquisition diagnostic polluted workload stdout: %q, %q", stdout.String(), output)
					}
					pulls, readErr := os.ReadFile(filepath.Join(dir, "pulls"))
					if readErr != nil || string(pulls) != failure.pulls {
						t.Errorf("pull attempts = %q, %v; want %q", pulls, readErr, failure.pulls)
					}
					if _, statErr := os.Stat(filepath.Join(dir, "workload")); !os.IsNotExist(statErr) {
						t.Errorf("failed acquisition executed a workload: %v", statErr)
					}
				})
			}
		})
	}
}

func dockerAcquisitionDiagnosticSetup(t *testing.T, failure string) string {
	t.Helper()
	dir := t.TempDir()
	const script = `#!/bin/sh
case "$1" in
  image)
    exit 1
    ;;
  pull)
    attempts=0
    if [ -f "$INVARIANT_DIAGNOSTIC_STATE/pulls" ]; then
      read -r attempts < "$INVARIANT_DIAGNOSTIC_STATE/pulls"
    fi
    attempts=$((attempts + 1))
    printf '%s\n' "$attempts" > "$INVARIANT_DIAGNOSTIC_STATE/pulls"
    if [ "$INVARIANT_DIAGNOSTIC_FAILURE" = integrity ]; then
      printf '%s\n' 'filesystem layer verification failed for digest sha256:bad' >&2
    else
      printf 'registry 502 Bad Gateway attempt-%s\n' "$attempts" >&2
    fi
    exit 1
    ;;
  run)
    printf '%s\n' 'workload executed' > "$INVARIANT_DIAGNOSTIC_STATE/workload"
    exit 0
    ;;
esac
exit 2
`
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("INVARIANT_DIAGNOSTIC_STATE", dir)
	t.Setenv("INVARIANT_DIAGNOSTIC_FAILURE", failure)
	return dir
}
