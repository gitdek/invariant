package toolchain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"text/template"
	"time"
)

// This executable also acts as a tiny Docker daemon/client double. It uses
// files for daemon state, so killing a client cannot itself remove a container.
// No Docker installation, registry, network or external interpreter is needed.
func init() {
	if os.Getenv("INVARIANT_433_DOCKER_HELPER") == "1" {
		os.Exit(docker433Main(os.Args[1:]))
	}
}

type docker433Call struct {
	Args        []string `json:"args"`
	At          int64    `json:"at"`
	ClientAlive bool     `json:"client_alive"`
}

func docker433ReadCalls(dir string) []docker433Call {
	b, _ := os.ReadFile(filepath.Join(dir, "calls"))
	var calls []docker433Call
	for _, line := range bytes.Split(b, []byte("\n")) {
		var call docker433Call
		if json.Unmarshal(line, &call) == nil {
			calls = append(calls, call)
		}
	}
	return calls
}

func docker433OfKind(dir, kind string) []docker433Call {
	var found []docker433Call
	for _, c := range docker433ReadCalls(dir) {
		if len(c.Args) > 0 && (c.Args[0] == kind || (kind == "pull" && len(c.Args) > 1 && c.Args[0] == "image" && c.Args[1] == "pull")) {
			found = append(found, c)
		}
	}
	return found
}

func docker433Option(args []string, key string) string {
	for i, arg := range args {
		if arg == key && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(arg, key+"=") {
			return strings.TrimPrefix(arg, key+"=")
		}
	}
	return ""
}

func docker433Main(args []string) int {
	dir, mode := os.Getenv("INVARIANT_433_DOCKER_STATE"), os.Getenv("INVARIANT_433_DOCKER_MODE")
	fail := func(message string) int { fmt.Fprintln(os.Stderr, message); return 1 }
	if len(args) == 0 {
		return fail("missing Docker command")
	}
	call := docker433Call{Args: args, At: time.Now().UnixNano()}
	if args[0] == "rm" {
		b, _ := os.ReadFile(filepath.Join(dir, "client"))
		pid, _ := strconv.Atoi(string(b))
		call.ClientAlive = pid > 0 && syscall.Kill(pid, 0) == nil
	}
	b, _ := json.Marshal(call)
	f, err := os.OpenFile(filepath.Join(dir, "calls"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fail(err.Error())
	}
	_, err = f.Write(append(b, '\n'))
	f.Close()
	if err != nil {
		return fail(err.Error())
	}
	ready := filepath.Join(dir, "image-ready")
	if args[0] == "pull" || (len(args) > 1 && args[0] == "image" && args[1] == "pull") {
		if args[len(args)-1] != JavaImage {
			return fail("pulled a different image: " + strings.Join(args, " "))
		}
		n := len(docker433OfKind(dir, "pull"))
		integrity := os.Getenv("INVARIANT_433_INTEGRITY")
		if integrity == "" {
			integrity = "filesystem layer verification failed for digest sha256:bad"
		}
		if mode == "integrity-immediate" || (mode == "integrity" && n >= 2) {
			return fail(integrity)
		}
		if mode == "forever" || mode == "cancel" {
			return fail(fmt.Sprintf("registry 502 Bad Gateway attempt-%d", n))
		}
		if n <= 2 {
			if n == 1 {
				return fail("registry 502 Bad Gateway attempt-1")
			}
			return fail("unauthorized: authentication required attempt-2")
		}
		if err := os.WriteFile(ready, []byte("ready"), 0600); err != nil {
			return fail(err.Error())
		}
		fmt.Fprintln(os.Stdout, "pulled pinned image")
		return 0
	}
	if args[0] == "inspect" || (len(args) > 1 && args[0] == "image" && args[1] == "inspect") {
		if _, err := os.Stat(ready); err != nil {
			return fail("No such image")
		}
		metadata := map[string]any{"Id": "sha256:" + strings.Repeat("a", 64), "Os": "linux", "Architecture": "amd64", "Variant": "", "RepoDigests": []string{JavaImage}}
		format := docker433Option(args, "--format")
		if format == "" {
			format = docker433Option(args, "-f")
		}
		if format == "" {
			json.NewEncoder(os.Stdout).Encode([]any{metadata})
			return 0
		}
		tmpl, err := template.New("inspect").Funcs(template.FuncMap{"json": func(v any) string { b, _ := json.Marshal(v); return string(b) }}).Parse(format)
		if err != nil {
			return fail(err.Error())
		}
		if err := tmpl.Execute(os.Stdout, metadata); err != nil {
			return fail(err.Error())
		}
		fmt.Fprintln(os.Stdout)
		return 0
	}
	switch args[0] {
	case "run":
		if mode == "late-create" {
			if err := os.WriteFile(filepath.Join(dir, "client"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
				return fail(err.Error())
			}
			for {
				time.Sleep(time.Second)
			}
		}
		if mode != "local" {
			if _, err := os.Stat(ready); err != nil {
				return fail("Unable to find image locally: registry 502 Bad Gateway")
			}
		}
		if mode == "workload-failure" {
			fmt.Fprintln(os.Stderr, "verification failed")
			return 7
		}
		input, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fail(err.Error())
		}
		fmt.Fprint(os.Stdout, "verified:"+string(input))
		return 0
	case "rm":
		name := args[len(args)-1]
		container := filepath.Join(dir, name)
		os.Remove(container)
		if mode == "late-create" && call.ClientAlive {
			// The remove request has already seen no container. A pending
			// create now lands in the daemon before the client is killed.
			if err := os.WriteFile(container, []byte("Created"), 0600); err != nil {
				return fail(err.Error())
			}
		}
		return 0
	default:
		return fail("unexpected Docker command: " + strings.Join(args, " "))
	}
}

func docker433Setup(t *testing.T, mode string) string {
	t.Helper()
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	quoted := "'" + strings.ReplaceAll(exe, "'", "'\"'\"'") + "'"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte("#!/bin/sh\nexec "+quoted+" \"$@\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("INVARIANT_433_DOCKER_HELPER", "1")
	t.Setenv("INVARIANT_433_DOCKER_STATE", dir)
	t.Setenv("INVARIANT_433_DOCKER_MODE", mode)
	return dir
}

func docker433Ready(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "image-ready"), []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
}

func docker433Wait(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !predicate() {
		if time.Now().After(deadline) {
			t.Fatal("Docker double never reached the requested phase")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestPinnedDockerPullRecoversRegistryFailures(t *testing.T) {
	for _, method := range []string{"Run", "Output", "CombinedOutput", "Start/Wait"} {
		t.Run(method, func(t *testing.T) {
			dir := docker433Setup(t, "recover")
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			cmd := Docker(ctx, "pull", "--platform", "linux/amd64", JavaImage)
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
			if err != nil {
				t.Fatalf("registry hiccups failed the pull: %v", err)
			}
			calls := docker433OfKind(dir, "pull")
			if len(calls) != 3 {
				t.Fatalf("pulls=%d, want 3", len(calls))
			}
			for _, c := range calls {
				if docker433Option(c.Args, "--platform") != "linux/amd64" || c.Args[len(c.Args)-1] != JavaImage {
					t.Errorf("pull changed pin/platform: %q", c.Args)
				}
			}
			if time.Duration(calls[1].At-calls[0].At) < 800*time.Millisecond || time.Duration(calls[2].At-calls[1].At) < 1600*time.Millisecond {
				t.Error("pulls did not back off for one then two seconds")
			}
		})
	}
}

func TestPinnedDockerPullBoundsRetriesAndStopsAtIntegrityFailure(t *testing.T) {
	for _, mode := range []string{"forever", "integrity", "integrity-immediate"} {
		for _, diagnostic := range []string{"filesystem layer verification failed for digest sha256:bad", "digest mismatch: expected sha256:good, got sha256:bad", "unexpected commit digest sha256:bad, expected sha256:good"} {
			t.Run(mode+"/"+strings.Split(diagnostic, ":")[0], func(t *testing.T) {
				dir := docker433Setup(t, mode)
				t.Setenv("INVARIANT_433_INTEGRITY", diagnostic)
				ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
				defer cancel()
				out, err := Docker(ctx, "pull", JavaImage).CombinedOutput()
				want, last := 2, diagnostic
				if mode == "forever" {
					want, last = 3, "attempt-3"
				}
				if mode == "integrity-immediate" {
					want = 1
				}
				if err == nil || len(docker433OfKind(dir, "pull")) != want || !strings.Contains(string(out)+fmt.Sprint(err), last) || ctx.Err() != nil {
					t.Fatalf("pulls=%d, output=%q, error=%v; want %d attempts ending in %q", len(docker433OfKind(dir, "pull")), out, err, want, last)
				}
			})
		}
	}
}

func TestPinnedDockerRunAcquiresImageWithoutRepeatingWork(t *testing.T) {
	for _, scenario := range []string{"missing", "cached", "never-equals", "never-split", "workload-failure", "acquisition-failure", "local-tag", "local-id"} {
		t.Run(scenario, func(t *testing.T) {
			mode := "recover"
			if scenario == "workload-failure" {
				mode = scenario
			}
			if scenario == "acquisition-failure" {
				mode = "forever"
			}
			if strings.HasPrefix(scenario, "local-") {
				mode = "local"
			}
			dir := docker433Setup(t, mode)
			if scenario == "cached" || scenario == "workload-failure" {
				docker433Ready(t, dir)
			}
			image := JavaImage
			if scenario == "local-tag" {
				image = "invariant-local:recipe"
			}
			if scenario == "local-id" {
				image = "sha256:" + strings.Repeat("a", 64)
			}
			args := []string{"run", "--rm", "--network", "none", "--platform=linux/amd64", "--env", "LOOKS_LIKE_IMAGE=" + JavaImage, "-v", "/tmp/input with spaces:/work:ro"}
			if scenario == "never-equals" {
				args = append(args, "--pull=never")
			}
			if scenario == "never-split" {
				args = append(args, "--pull", "never")
			}
			args = append(args, image, "verify", "literal argument", JavaImage)
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			cmd := Docker(ctx, args...)
			cmd.Stdin = strings.NewReader("input bytes\n")
			out, err := cmd.Output()
			pulls, runs := docker433OfKind(dir, "pull"), docker433OfKind(dir, "run")
			wantPulls := 0
			if scenario == "missing" || scenario == "acquisition-failure" {
				wantPulls = 3
			}
			if len(pulls) != wantPulls {
				t.Errorf("pulls=%d, want %d", len(pulls), wantPulls)
			}
			for _, p := range pulls {
				if docker433Option(p.Args, "--platform") != "linux/amd64" {
					t.Errorf("acquisition lost platform: %q", p.Args)
				}
			}
			if scenario == "acquisition-failure" {
				if err == nil || len(runs) != 0 {
					t.Fatalf("failed acquisition ran a workload: runs=%d err=%v", len(runs), err)
				}
				return
			}
			if len(runs) != 1 {
				t.Fatalf("workload executions=%d, want exactly one", len(runs))
			}
			joined := strings.Join(runs[0].Args, "\x00")
			if !strings.Contains(joined, strings.Join([]string{image, "verify", "literal argument", JavaImage}, "\x00")) || !strings.Contains(joined, "LOOKS_LIKE_IMAGE="+JavaImage) || !strings.Contains(joined, "/tmp/input with spaces:/work:ro") {
				t.Errorf("run changed arguments: %q", runs[0].Args)
			}
			if strings.HasPrefix(scenario, "never-") {
				if err == nil || docker433Option(runs[0].Args, "--pull") != "never" {
					t.Fatalf("pull-never changed: %q %v", runs[0].Args, err)
				}
				return
			}
			if scenario == "workload-failure" {
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 7 || cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 7 {
					t.Fatalf("lost workload status: %v, %v", err, cmd.ProcessState)
				}
				return
			}
			if err != nil || string(out) != "verified:input bytes\n" {
				t.Fatalf("workload output polluted or stdin lost: %q, %v", out, err)
			}
			if len(docker433OfKind(dir, "rm")) != 0 {
				t.Error("normal completion forced container removal")
			}
		})
	}
}

func TestPinnedDockerPullCancellationInterruptsBackoff(t *testing.T) {
	dir := docker433Setup(t, "cancel")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := Docker(ctx, "pull", JavaImage).CombinedOutput(); done <- err }()
	docker433Wait(t, func() bool { return len(docker433OfKind(dir, "pull")) > 0 })
	time.Sleep(100 * time.Millisecond)
	started := time.Now()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || len(docker433OfKind(dir, "pull")) != 1 {
			t.Fatalf("cancellation lost or pull repeated: %v; %#v", err, docker433ReadCalls(dir))
		}
		if time.Since(started) > 500*time.Millisecond {
			t.Error("cancellation waited for the pull backoff")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("pull ignored cancellation")
	}
	if len(docker433OfKind(dir, "run")) != 0 {
		t.Error("cancelled acquisition started a workload")
	}
}

func TestCancelledDockerRunRemovesLateCreatedContainer(t *testing.T) {
	for _, method := range []string{"Run", "Output", "CombinedOutput", "Start/Wait"} {
		t.Run(method, func(t *testing.T) {
			dir := docker433Setup(t, "late-create")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cmd := Docker(ctx, "run", "--rm", "local-test:recipe", "wait")
			name := docker433Option(cmd.Args, "--name")
			if !strings.HasPrefix(name, fmt.Sprintf("invariant-%d-", os.Getpid())) {
				t.Fatalf("run has no process-scoped name: %q", cmd.Args)
			}
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
			docker433Wait(t, func() bool { _, err := os.Stat(filepath.Join(dir, "client")); return err == nil })
			cancel()
			select {
			case err := <-done:
				if err == nil {
					t.Error("cancelled run succeeded")
				}
			case <-time.After(15 * time.Second):
				t.Fatal("cancelled run did not finish bounded cleanup")
			}
			removals := docker433OfKind(dir, "rm")
			if len(removals) < 2 || !removals[0].ClientAlive || removals[len(removals)-1].ClientAlive {
				t.Errorf("removals did not bracket client exit: %#v", removals)
			}
			for _, rm := range removals {
				if rm.Args[len(rm.Args)-1] != name {
					t.Errorf("removed a different container: %q", rm.Args)
				}
			}
			if b, err := os.ReadFile(filepath.Join(dir, name)); !os.IsNotExist(err) {
				t.Errorf("late-created container remains after %s: %q, %v", method, b, err)
			}
		})
	}
}
