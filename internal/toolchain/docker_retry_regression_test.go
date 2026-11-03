package toolchain

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func dockerRetryRun(t *testing.T, method string, cmd *DockerCommand) ([]byte, error) {
	t.Helper()
	var out bytes.Buffer
	switch method {
	case "Output":
		return cmd.Output()
	case "CombinedOutput":
		return cmd.CombinedOutput()
	case "Run":
		cmd.Stdout = &out
		err := cmd.Run()
		return out.Bytes(), err
	case "Start/Wait":
		cmd.Stdout = &out
		err := cmd.Start()
		if err == nil {
			err = cmd.Wait()
		}
		return out.Bytes(), err
	default:
		t.Fatalf("unknown command method %q", method)
		return nil, nil
	}
}

func TestPinnedDockerRunAcquisitionThroughEveryMethod(t *testing.T) {
	for _, method := range []string{"Run", "Output", "CombinedOutput", "Start/Wait"} {
		t.Run(method, func(t *testing.T) {
			dir := docker433Setup(t, "recover")
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			cmd := Docker(ctx, "run", "--rm", "--platform", "linux/amd64", JavaImage, "verify")
			cmd.Stdin = strings.NewReader("payload\n")
			out, err := dockerRetryRun(t, method, cmd)
			if err != nil || string(out) != "verified:payload\n" {
				t.Fatalf("workload output or input changed: %q, %v", out, err)
			}
			if len(docker433OfKind(dir, "pull")) != 3 || len(docker433OfKind(dir, "run")) != 1 {
				t.Fatalf("acquisition or workload repeated incorrectly: %#v", docker433ReadCalls(dir))
			}
			if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 0 {
				t.Fatalf("workload process status lost: %v", cmd.ProcessState)
			}
		})
	}
}

func TestPinnedDockerRunCancellationDuringAcquisition(t *testing.T) {
	for _, method := range []string{"Run", "Output", "CombinedOutput", "Start/Wait"} {
		t.Run(method, func(t *testing.T) {
			dir := docker433Setup(t, "cancel")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cmd := Docker(ctx, "run", "--rm", JavaImage, "verify")
			done := make(chan error, 1)
			go func() {
				_, err := dockerRetryRun(t, method, cmd)
				done <- err
			}()
			docker433Wait(t, func() bool { return len(docker433OfKind(dir, "pull")) > 0 })
			time.Sleep(100 * time.Millisecond)
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("acquisition cancellation lost: %v", err)
				}
			case <-time.After(750 * time.Millisecond):
				t.Fatal("cancellation waited for acquisition backoff")
			}
			if len(docker433OfKind(dir, "pull")) != 1 || len(docker433OfKind(dir, "run")) != 0 || len(docker433OfKind(dir, "rm")) != 0 || cmd.Process != nil {
				t.Fatalf("cancelled acquisition started or removed a workload: %#v", docker433ReadCalls(dir))
			}
		})
	}
}

func TestPinnedDockerRunCachedPlatformSelection(t *testing.T) {
	for _, tc := range []struct {
		name, defaultPlatform, platform string
		wantPulls                       int
	}{
		{name: "cached match", platform: "linux/amd64"},
		{name: "cached wrong platform", platform: "linux/arm64", wantPulls: 3},
		{name: "default wrong platform", defaultPlatform: "linux/arm64", wantPulls: 3},
		{name: "explicit overrides default", defaultPlatform: "linux/arm64", platform: "linux/amd64"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := docker433Setup(t, "recover")
			docker433Ready(t, dir)
			t.Setenv("DOCKER_DEFAULT_PLATFORM", tc.defaultPlatform)
			args := []string{"run", "--rm"}
			if tc.platform != "" {
				args = append(args, "--platform", tc.platform)
			}
			args = append(args, JavaImage, "verify")
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			if out, err := Docker(ctx, args...).Output(); err != nil || string(out) != "verified:" {
				t.Fatalf("platform acquisition failed: %q, %v", out, err)
			}
			pulls := docker433OfKind(dir, "pull")
			if len(pulls) != tc.wantPulls {
				t.Fatalf("pulls=%d, want %d: %#v", len(pulls), tc.wantPulls, docker433ReadCalls(dir))
			}
			wantPlatform := tc.platform
			if wantPlatform == "" {
				wantPlatform = tc.defaultPlatform
			}
			for _, call := range pulls {
				if docker433Option(call.Args, "--platform") != wantPlatform || call.Args[len(call.Args)-1] != JavaImage {
					t.Errorf("pull changed requested image/platform: %q", call.Args)
				}
			}
		})
	}
}

func TestPinnedDockerRunKeepsImageArgumentBoundary(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options []string
	}{
		{name: "quiet long", options: []string{"--quiet"}},
		{name: "quiet short", options: []string{"-q"}},
		{name: "socket boolean", options: []string{"--use-api-socket"}},
		{name: "boolean short bundle", options: []string{"-it"}},
		{name: "environment after short bundle", options: []string{"-ie", "PIN=" + JavaImage}},
		{name: "volume after short bundle", options: []string{"-iv", "/tmp/input with spaces:/work:ro"}},
		{name: "attached environment", options: []string{"-ePIN=" + JavaImage}},
		{name: "option terminator", options: []string{"--env", "PIN=" + JavaImage, "--"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := docker433Setup(t, "recover")
			docker433Ready(t, dir)
			t.Setenv("DOCKER_DEFAULT_PLATFORM", "linux/amd64")
			args := append([]string{"run"}, tc.options...)
			args = append(args, JavaImage, "verify", "--pull=never", "--platform=foreign", JavaImage)
			out, err := Docker(context.Background(), args...).Output()
			if err != nil || string(out) != "verified:" {
				t.Fatalf("argument parsing changed execution: %q, %v", out, err)
			}
			inspects := 0
			for _, call := range docker433ReadCalls(dir) {
				if len(call.Args) >= 2 && call.Args[0] == "image" && call.Args[1] == "inspect" {
					inspects++
					if call.Args[len(call.Args)-1] != JavaImage {
						t.Errorf("inspected an option or workload argument: %q", call.Args)
					}
				}
			}
			if inspects != 1 || len(docker433OfKind(dir, "pull")) != 0 {
				t.Fatalf("cached workload image was not identified: %#v", docker433ReadCalls(dir))
			}
			runs := docker433OfKind(dir, "run")
			wantSuffix := strings.Join(args[1:], "\x00")
			if len(runs) != 1 || !strings.HasSuffix(strings.Join(runs[0].Args, "\x00"), wantSuffix) {
				t.Fatalf("workload argument boundaries changed: %#v", runs)
			}
		})
	}
}
