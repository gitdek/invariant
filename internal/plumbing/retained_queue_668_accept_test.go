package plumbing

// Registration has one source of repository authority, sandbox.Original.
// A nil registered plan denotes planning; a nonnil plan denotes building.
// RegisterPlan captures its explicit repository argument into that sandbox
// before using the same registration path. Both modes may retain an original.
// These tests exercise requests and evidence, not struct-field inventories.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/repository"
)

func TestRetainedQueueRegistrationModes668(t *testing.T) {
	q, err := NewCheckQueue(1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := q.Close(); err != nil {
			t.Error(err)
		}
	})
	clientFor := func(b *checkBuild) *CheckClient {
		return &CheckClient{Address: q.listener.Addr().String(), Build: b.id}
	}
	for _, public := range []bool{false, true} {
		if !t.Run(fmt.Sprintf("planning/public=%v", public), func(t *testing.T) {
			f := newFixture515(t)
			allFail515(f.runner)
			ws := n503Draft(t, f.plan)
			write468(t, ws, "repo/api/main.go", "package fixture // the child's replacement base\n")
			write468(t, ws, "repo/.invariant/repository.json", `{"candidate":"not authority"}`)
			retained := f.sandbox
			captured := *retained.Repository
			captured.Adapters = map[string]RepositoryAcceptanceAdapter{"go": f.adapter}
			retained.Repository = &captured
			f.runner.openHook = func(_ context.Context, request DependencyRequest) error {
				data, err := os.ReadFile(filepath.Join(request.SourceDir, "api/main.go"))
				if err != nil || string(data) != "package fixture\n" {
					return fmt.Errorf("wrong retained base: %q (%v)", data, err)
				}
				return nil
			}
			var client *CheckClient
			var finish func()
			if public {
				f.sandbox.Original = t.TempDir() // Explicit RegisterPlan argument wins.
				client, finish, err = q.RegisterPlan(t.Context(), f.sandbox, f.root)
			} else {
				// This common registration path already has all retained authority.
				// No second repository field may be needed to enable draft checks.
				var b *checkBuild
				b, err = q.register(t.Context(), f.sandbox, nil)
				if err == nil {
					client, finish = clientFor(b), func() { b.finish() }
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			defer finish()
			f.sandbox.Original = t.TempDir()
			f.sandbox.Repository.Snapshot = nil
			f.sandbox.Repository.Adapters["go"] = &adapter515{selectHook: func(context.Context, repository.Part, Test) (RepositoryAcceptanceSelection, error) {
				return RepositoryAcceptanceSelection{}, errors.New("caller replaced adapter map")
			}}
			if _, err := client.run(t.Context(), f.candidate); err == nil || !strings.Contains(err.Error(), "planning registration") {
				t.Fatalf("planning ran a build: %v", err)
			}
			if len(f.runner.opens) != 0 {
				t.Fatal("cross-mode request executed")
			}
			tool, err := Check(t.Context(), ws, filepath.Join(ws, "repo"), Sandbox{Checks: client})
			if err != nil || tool == nil || tool.Problem != "" {
				t.Fatalf("retained planning registration cannot check: %+v %v", tool, err)
			}
			final, err := Check(t.Context(), ws, f.root, retained)
			if err != nil || final == nil || final.Problem != "" {
				t.Fatalf("fresh final check disagrees: %+v %v", final, err)
			}
			if tool.Plan.Hash() != final.Plan.Hash() || tool.Plan.Hash() != f.plan.Hash() || !reflect.DeepEqual(tool.Plan.Sources, final.Plan.Sources) {
				t.Fatal("tool/final authority diverged")
			}
			if len(f.runner.opens) != 2 || len(f.runner.closed) != 2 {
				t.Fatal("tool and final did not execute and clean up afresh")
			}
			assertBindings515(t, f)
			for _, test := range f.plan.Tests {
				if result515(t, tool.Run, test).Status != "failed" || result515(t, final.Run, test).Status != "failed" {
					t.Fatal("lost independent base failure")
				}
			}
			// A later check must use new execution evidence, even with the same
			// retained authority and source. Passing bases cannot be proposed.
			f.runner.accept = nil
			tool, err = client.check(t.Context(), ws)
			if err != nil || tool == nil || tool.Problem == "" {
				t.Fatal("tool reused earlier failure evidence")
			}
			final, err = Check(t.Context(), ws, f.root, retained)
			if err != nil || final == nil || final.Problem == "" {
				t.Fatal("final check qualified a passing base")
			}
			if tool.Plan.Hash() != final.Plan.Hash() {
				t.Fatal("fresh evidence changed authority")
			}
			closed484(t, f.runner.runner484)
			finish()
			if _, err := client.check(t.Context(), ws); err == nil {
				t.Fatal("finished registration remained usable")
			}
		}) {
			return
		}
	}
	t.Run("build retains original and copied plan", func(t *testing.T) {
		f := newFixture515(t)
		want := cloneRepositoryPlan(f.plan)
		b, err := q.register(t.Context(), f.sandbox, f.plan)
		if err != nil {
			t.Fatal(err)
		}
		defer b.finish()
		client := clientFor(b)
		f.plan.Files[0] = "forged/unowned.go"
		f.plan.Tests[0].Selector = "TestReplacedByCaller"
		f.plan.Sources[want.Tests[0].File] = "package forged\n"
		f.plan.Execution.Digest = "sha256:" + strings.Repeat("0", 64)
		f.sandbox.Original = t.TempDir()
		f.sandbox.Repository.Snapshot = nil
		delete(f.sandbox.Repository.Adapters, "go")
		result, err := (Sandbox{Checks: client}).Run(t.Context(), f.candidate, &Plan{Files: []string{"child/forgery.go"}})
		if err != nil || !result.Passed() {
			t.Fatalf("build treated Original as planning or lost captured authority: %v\n%s", err, result.Summary())
		}
		for _, test := range want.Tests {
			if result515(t, result, test).Status != "passed" {
				t.Fatal("build lost registered identity")
			}
		}
		for _, in := range f.runner.invocations {
			if in.Source != want.Sources[in.Binding.File] || in.Binding.ExecutionDigest != want.Execution.Digest {
				t.Fatal("caller mutation changed registered bytes")
			}
		}
		opens := len(f.runner.opens)
		if _, err := client.check(t.Context(), n503Draft(t, want)); err == nil || !strings.Contains(err.Error(), "build registration") {
			t.Fatalf("build accepted replacement planning request: %v", err)
		}
		if len(f.runner.opens) != opens {
			t.Fatal("cross-mode request executed")
		}
		closed484(t, f.runner.runner484)
	})
	t.Run("cancellation keeps slot through cleanup", func(t *testing.T) {
		first, second := newFixture515(t), newFixture515(t)
		allFail515(first.runner)
		allFail515(second.runner)
		cleaning, release, waiting := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var cleanOnce, releaseOnce, waitOnce sync.Once
		first.runner.closeHook = func(ctx context.Context, _ DependencyRequest) error {
			if ctx.Err() != nil {
				return errors.New("cleanup inherited cancellation")
			}
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > SandboxCleanupAllowance+time.Second {
				return errors.New("cleanup lost bounded reserve")
			}
			cleanOnce.Do(func() { close(cleaning) })
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		a, err := q.register(t.Context(), first.sandbox, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { a.finish() })
		b, err := q.register(WithCheckStatus(t.Context(), func(w bool) {
			if w {
				waitOnce.Do(func() { close(waiting) })
			}
		}), second.sandbox, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { b.finish() })
		t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
		await := func(ch <-chan struct{}, why string) {
			t.Helper()
			select {
			case <-ch:
			case <-time.After(5 * time.Second):
				t.Fatal("did not reach " + why)
			}
		}
		firstWS, secondWS := n503Draft(t, first.plan), n503Draft(t, second.plan)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() { _, err := clientFor(a).check(ctx, firstWS); done <- err }()
		await(cleaning, "first cleanup")
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("active client did not cancel")
		}
		waitCtx, cancelWait := context.WithCancel(t.Context())
		defer cancelWait()
		go func() { _, err := clientFor(b).check(waitCtx, secondWS); done <- err }()
		await(waiting, "waiter blocked behind canceled check cleanup")
		cancelWait()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("waiting client did not cancel")
		}
		b.finish() // Join the server before inspecting the waiting runner.
		if len(second.runner.installs)+len(second.runner.opens) != 0 {
			t.Fatal("waiting request prepared before cleanup released its slot")
		}
		releaseOnce.Do(func() { close(release) })
		a.finish()
		closed484(t, first.runner.runner484)
		for _, request := range first.runner.opens {
			if _, err := os.Stat(request.SourceDir); !os.IsNotExist(err) {
				t.Fatal("finished registration retained private staging")
			}
		}
		// Releasing cleanup permits another complete registered check.
		c, finish, err := q.RegisterPlan(t.Context(), second.sandbox, second.root)
		if err != nil {
			t.Fatal(err)
		}
		defer finish()
		checked, err := c.check(t.Context(), secondWS)
		if err != nil || checked == nil || checked.Problem != "" {
			t.Fatalf("slot leaked after cleanup: %+v %v", checked, err)
		}
	})
}
