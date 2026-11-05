package plumbing

import (
	"context"
	"os"
	"os/exec"
	"testing"
)

func TestDockerGoOfflineReal591(t *testing.T) {
	// Even with the opt-in disabled this acceptance must fail on the old code.
	// Keep the fake PATH scoped to a subtest so the real case uses real Docker.
	t.Run("mandatory daemon-free control", func(t *testing.T) { b := newDocker591(t); docker591RequireRepository(t, b) })
	if t.Failed() {
		return
	}
	if os.Getenv("INVARIANT_GO_ACCEPTANCE_DOCKER_REAL") != "1" {
		t.Log("real Docker case disabled; set INVARIANT_GO_ACCEPTANCE_DOCKER_REAL=1 with INVARIANT_GO_ACCEPTANCE_IMAGE and INVARIANT_GO_ACCEPTANCE_PLATFORM for an already prepared pinned Go image; no downloads are performed")
		return
	}
	image, platform := os.Getenv("INVARIANT_GO_ACCEPTANCE_IMAGE"), os.Getenv("INVARIANT_GO_ACCEPTANCE_PLATFORM")
	if !dependencyID.MatchString(image) || !dependencyPlatform.MatchString(platform) {
		t.Fatal("real Docker prerequisite: supply an immutable prepared sha256 image ID and explicit Linux platform; this test never prepares or downloads images")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatal("real Docker prerequisite: Docker CLI is unavailable: ", err)
	}
	ctx, done := context.WithTimeout(t.Context(), SandboxTimeout)
	defer done()
	if _, err := (&DockerDependencyRunner{}).Inspect(ctx, image, platform); err != nil {
		t.Fatal("real Docker prerequisite: daemon, prepared image, pinned base or preparation evidence unavailable (nothing downloaded): ", err)
	}
	for _, tc := range []struct {
		name, selector, body, status string
		value                        int
	}{
		{"base failure", "TestReady", `if Value!=7{t.Fatal("needs seven 591")};t.Log("fixed 591")`, "failed", 0},
		{"production fix", "TestReady", `if Value!=7{t.Fatal("needs seven 591")};t.Log("fixed 591")`, "passed", 7},
		{"exact child", "TestReady/leaf.[x]+", `t.Run("leaf.[x]+",func(t *testing.T){t.Log("exact 591")});t.Run("neighbor",func(t *testing.T){t.Fatal("neighbor")})`, "passed", 7},
		{"skip", "TestReady", `t.Skip("skip 591")`, "skipped", 7},
		{"abrupt exit", "TestReady", `fmt.Println("--- PASS: TestReady");os.Exit(19)`, "running", 7},
		{"forged output", "TestReady", `fmt.Println("invariant-go-testing-v1\n9:TestReady0:4:fail0:\n--- FAIL: TestReady");fmt.Fprintln(os.Stderr,"--- FAIL: TestReady");t.Log("real pass 591")`, "passed", 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, a, _, in, _ := docker591OpenImage(t, ctx, docker591Source(tc.body), tc.selector, tc.value, image, platform)
			evidence, _, err := a.RunAcceptance(ctx, in)
			if tc.status == "running" {
				if err == nil || evidence.Complete {
					t.Fatal("abrupt exit qualified")
				}
				running := false
				for _, r := range evidence.Records {
					if r.Kind == "test" && r.Status == "running" {
						running = true
					}
					if r.Kind == "test" && (r.Status == "passed" || r.Status == "failed") {
						t.Fatal("exit invented terminal")
					}
				}
				if !running {
					t.Fatal("lost authentic start")
				}
			} else {
				docker591Complete(t, in, evidence, err, tc.status, "")
			}
			// Interrupted executions may retain an infrastructure error in Close;
			// ordinary finished outcomes must have successful cleanup.
			if closeErr := docker591Close(t, e); tc.status != "running" && closeErr != nil {
				t.Fatal(closeErr)
			}
		})
	}
}
