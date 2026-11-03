package toolchain

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPinnedDockerRunCancellationInterruptsAcquisition(t *testing.T) {
	for _, method := range []string{"Run", "Output", "CombinedOutput", "Start/Wait"} {
		t.Run(method, func(t *testing.T) {
			dir := docker433Setup(t, "cancel")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cmd := Docker(ctx, "run", "--rm", "--platform", "linux/amd64", JavaImage, "verify")
			done := make(chan error, 1)
			go func() {
				var err error
				switch method {
				case "Run":
					err = cmd.Run()
				case "Output":
					_, err = cmd.Output()
				case "CombinedOutput":
					_, err = cmd.CombinedOutput()
				case "Start/Wait":
					err = cmd.Start()
					if err == nil {
						err = cmd.Wait()
					}
				}
				done <- err
			}()
			docker433Wait(t, func() bool { return len(docker433OfKind(dir, "pull")) > 0 })
			// Let the first failed client exit and enter its one-second backoff.
			time.Sleep(100 * time.Millisecond)
			cancelledAt := time.Now()
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("acquisition cancellation lost: %v", err)
				}
				if time.Since(cancelledAt) > 500*time.Millisecond {
					t.Error("run cancellation waited for acquisition backoff")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("run acquisition ignored cancellation")
			}
			if pulls := docker433OfKind(dir, "pull"); len(pulls) != 1 {
				t.Errorf("cancelled acquisition repeated pulls: %#v", pulls)
			}
			if calls := docker433OfKind(dir, "run"); len(calls) != 0 {
				t.Errorf("cancelled acquisition started workload: %#v", calls)
			}
		})
	}
}

func TestPinnedDockerRunAcquiresImageAfterClusteredOptions(t *testing.T) {
	dir := docker433Setup(t, "recover")
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	args := []string{"run", "--rm", "-itv", "/tmp/a:/work", "--platform", "linux/amd64", "--entrypoint", PythonImage, "--env", "OPTION_PIN=" + PythonImage, JavaImage, "verify", PythonImage}
	cmd := Docker(ctx, args...)
	cmd.Stdin = strings.NewReader("literal input\n")
	out, err := cmd.Output()
	if err != nil || string(out) != "verified:literal input\n" {
		t.Fatalf("clustered options prevented acquisition or changed input: %q, %v", out, err)
	}
	pulls := docker433OfKind(dir, "pull")
	if len(pulls) != 3 {
		t.Fatalf("pulls=%d, want recovery after three attempts", len(pulls))
	}
	for _, pull := range pulls {
		if pull.Args[len(pull.Args)-1] != JavaImage || docker433Option(pull.Args, "--platform") != "linux/amd64" {
			t.Errorf("acquisition selected an option or workload argument: %q", pull.Args)
		}
	}
	calls := docker433OfKind(dir, "run")
	if len(calls) != 1 {
		t.Fatalf("workload executions=%d, want one", len(calls))
	}
	got, want := calls[0].Args, args[1:]
	if len(got) < len(want) || !reflect.DeepEqual(got[len(got)-len(want):], want) {
		t.Errorf("run arguments changed: %q, want suffix %q", got, want)
	}
}

func TestPinnedDockerRunAcquiresRequestedPlatform(t *testing.T) {
	dir := docker433Setup(t, "recover")
	// The double's cached metadata is linux/amd64; that does not satisfy arm64.
	docker433Ready(t, dir)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if out, err := Docker(ctx, "run", "--platform", "linux/arm64", JavaImage, "verify").Output(); err != nil || string(out) != "verified:" {
		t.Fatalf("requested platform was not acquired: %q, %v", out, err)
	}
	pulls := docker433OfKind(dir, "pull")
	if len(pulls) != 3 {
		t.Fatalf("pulls=%d, want 3 for the missing platform", len(pulls))
	}
	for _, pull := range pulls {
		if docker433Option(pull.Args, "--platform") != "linux/arm64" || pull.Args[len(pull.Args)-1] != JavaImage {
			t.Errorf("acquisition changed requested image or platform: %q", pull.Args)
		}
	}
	if calls := docker433OfKind(dir, "run"); len(calls) != 1 {
		t.Errorf("workload executions=%d, want one", len(calls))
	}
}

func TestPinnedDockerPullCancelledBeforeWaitReapsClient(t *testing.T) {
	dir := docker433Setup(t, "recover")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := Docker(ctx, "image", "pull", "--platform", "linux/amd64", JavaImage)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	docker433Wait(t, func() bool { return len(docker433OfKind(dir, "pull")) > 0 })
	cancel()
	if err := cmd.Wait(); !errors.Is(err, context.Canceled) {
		t.Errorf("pull cancellation lost: %v", err)
	}
	if cmd.ProcessState == nil {
		t.Error("Wait returned without reaping the started client")
	}
	if pulls := docker433OfKind(dir, "pull"); len(pulls) != 1 {
		t.Errorf("cancelled pull retried: %#v", pulls)
	}
}
