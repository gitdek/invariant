package toolchain

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestDockerPullPipesReadThroughRetriesBeforeWait(t *testing.T) {
	dir := docker433Setup(t, "recover")
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	cmd := Docker(ctx, "pull", JavaImage)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	type result struct {
		stream string
		data   []byte
		err    error
	}
	results := make(chan result, 2)
	for stream, reader := range map[string]io.Reader{"stdout": stdout, "stderr": stderr} {
		go func() {
			data, err := io.ReadAll(reader)
			results <- result{stream, data, err}
		}()
	}
	got := make(map[string]string)
	// Like an ordinary exec.Cmd, callers can drain both pipes before Wait.
	// The failed attempts must neither close the pipes nor wait for Wait to retry.
	for range 2 {
		select {
		case result := <-results:
			if result.err != nil {
				t.Fatalf("read %s: %v", result.stream, result.err)
			}
			got[result.stream] = string(result.data)
		case <-ctx.Done():
			t.Fatal("pull pipes did not finish before Wait")
		}
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if got["stdout"] != "pulled pinned image\n" {
		t.Errorf("stdout = %q", got["stdout"])
	}
	wantStderr := "registry 502 Bad Gateway attempt-1\nunauthorized: authentication required attempt-2\n"
	if got["stderr"] != wantStderr {
		t.Errorf("stderr = %q, want %q", got["stderr"], wantStderr)
	}
	if calls := docker433OfKind(dir, "pull"); len(calls) != 3 {
		t.Errorf("pulls = %d, want 3", len(calls))
	}
	if cmd.ProcessState == nil || !cmd.ProcessState.Success() {
		t.Errorf("final process status = %v, want success", cmd.ProcessState)
	}
	if err := cmd.Wait(); err == nil {
		t.Error("a second Wait succeeded")
	}
}

func TestDockerPullErrorRetainsLastDiagnosticWithoutWriters(t *testing.T) {
	for _, method := range []string{"Run", "StartWait"} {
		for _, mode := range []string{"forever", "integrity-immediate"} {
			t.Run(method+"/"+mode, func(t *testing.T) {
				dir := docker433Setup(t, mode)
				ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
				defer cancel()
				cmd := Docker(ctx, "pull", JavaImage)
				var err error
				if method == "Run" {
					err = cmd.Run()
				} else {
					err = cmd.Start()
					if err != nil {
						t.Fatalf("start: %v", err)
					}
					err = cmd.Wait()
				}
				want, attempts := "registry 502 Bad Gateway attempt-3", 3
				if mode == "integrity-immediate" {
					want, attempts = "filesystem layer verification failed for digest sha256:bad", 1
				}
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %v, want diagnostic %q", err, want)
				}
				if strings.Contains(err.Error(), "attempt-1") || strings.Contains(err.Error(), "attempt-2") {
					t.Errorf("error retains an earlier attempt's diagnostic: %v", err)
				}
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 1 || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 1 {
					t.Errorf("lost final exit status: error = %v, status = %v", err, cmd.ProcessState)
				}
				if calls := docker433OfKind(dir, "pull"); len(calls) != attempts {
					t.Errorf("pulls = %d, want %d", len(calls), attempts)
				}
			})
		}
	}
}

func TestDockerPullImageWithInterspersedFlags(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{"plain", []string{"pull", JavaImage}, JavaImage},
		{"platform-before", []string{"pull", "--platform", "linux/amd64", JavaImage}, JavaImage},
		{"platform-after", []string{"pull", JavaImage, "--platform", "linux/amd64"}, JavaImage},
		{"platform-equals-after", []string{"pull", JavaImage, "--platform=linux/amd64", "--quiet"}, JavaImage},
		{"image-subcommand", []string{"image", "pull", "-q", JavaImage, "--platform", "linux/amd64"}, JavaImage},
		{"boolean-value", []string{"pull", JavaImage, "--disable-content-trust=false"}, JavaImage},
		{"option-delimiter", []string{"pull", "--", JavaImage}, JavaImage},
		{"local-tag", []string{"pull", "local:tag", "--quiet"}, "local:tag"},
		{"empty", nil, ""},
		{"other-command", []string{"image", "inspect", JavaImage}, ""},
		{"missing-image", []string{"pull", "--platform", "linux/amd64"}, ""},
		{"missing-platform-value", []string{"pull", "--platform"}, ""},
		{"multiple-images", []string{"pull", JavaImage, JavaImage}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := dockerPullImage(test.args); got != test.want {
				t.Errorf("dockerPullImage(%q) = %q, want %q", test.args, got, test.want)
			}
		})
	}
}
