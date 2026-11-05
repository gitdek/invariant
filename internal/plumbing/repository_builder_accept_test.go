package plumbing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/synth"
)

// A configured Builder uses its shared queue for both processes. If no watcher
// queue was supplied, it owns a local queue for its build, so the separate MCP
// tool still reaches the exact captured configuration and dependency runner.
// This does not enable external repository intake or add candidate-controlled
// CLI configuration. The existing queue protocol carries no execution policy.
type builder484 struct {
	tool    Result
	toolErr error
}

func (*builder484) Name() string { return "repository-check fixture" }

func (b *builder484) Run(ctx context.Context, job synth.Job) (synth.Usage, error) {
	if job.ReadOnly {
		return synth.Usage{}, errors.New("suite success must not fabricate native acceptance evidence")
	}
	client := &CheckClient{}
	for i, arg := range job.GateServer {
		if i+1 == len(job.GateServer) {
			continue
		}
		switch arg {
		case "-check-queue":
			client.Address = job.GateServer[i+1]
		case "-check-build":
			client.Build = job.GateServer[i+1]
		}
	}
	if client.Address == "" || client.Build == "" {
		return synth.Usage{}, errors.New("test process cannot reach configured repository checks")
	}
	if err := os.WriteFile(filepath.Join(job.Workspace, "api/main.go"), []byte("package fixture\n\nconst Changed = true\n"), 0o644); err != nil {
		return synth.Usage{}, err
	}
	// This is the same IPC call the separate MCP test process makes. Its
	// request supplies only the candidate, never a replacement contract.
	b.tool, b.toolErr = (Sandbox{Checks: client}).Run(ctx, job.Workspace, &Plan{})
	return synth.Usage{Outcome: "success", Summary: "candidate written and checked"}, b.toolErr
}

func TestRepositoryBuilderUsesSameChecksForToolAndFinalRun(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprintf("shared=%v", shared), func(t *testing.T) {
			part := part484("go", "api")
			part.Commands["test_one"] = repository.Command{Argv: []string{"trusted-fixture", "{test}", "literal argument; $(not-a-shell)"}}
			root, _, snapshot := fixture484(t, []repository.Part{part}, nil)
			const source = "package fixture\n\nimport \"testing\"\n\nfunc TestBuilderNative(t *testing.T) {}\n"
			plan := n503Read(t, &Plan{Name: "configured builder", Summary: "Exercise declared repository checks.", Files: []string{"api/main.go"},
				Tests:   []Test{{Name: "TestBuilderNative", Part: "api", Framework: "go", Selector: "TestBuilderNative", File: "api/builder_native_accept_test.go", Says: "Suite success cannot replace a skipped selection."}},
				Sources: map[string]string{"api/builder_native_accept_test.go": source}}, snapshot)
			if err := plan.Validate(); err != nil {
				t.Fatal(err)
			}
			lock := Lock{Plan: *plan, Ratified: Ratification{By: "fixture", Issue: 484, Comment: "https://github.com/o/r/issues/484#issuecomment-1", Proposal: plan.Hash()}}
			if err := lock.Write(root); err != nil {
				t.Fatal(err)
			}
			git468(t, root, "add", "--all")
			git468(t, root, "commit", "-q", "--no-verify", "-m", "ratified fixture")
			r := newRunner484(t)
			r.openHook = func(_ context.Context, q DependencyRequest) error {
				if !strings.Contains(read484(t, q.SourceDir, "api/main.go"), "Changed = true") {
					return errors.New("checks did not see builder changes")
				}
				return nil
			}
			agent := &builder484{}
			b := Builder{Backend: agent, Reviewer: agent, Binary: "unused-fixture-binary", TestRuns: 2, Timeout: 3 * time.Second, Sandbox: sandbox484(t, root, snapshot, r)}
			b.Sandbox.Repository.Adapters = map[string]RepositoryAcceptanceAdapter{"go": &adapter515{}}
			b.Sandbox.Repository.Dependencies.Runner = &runner515{runner484: r, accept: func(_ context.Context, _ DependencyRequest, in RepositoryAcceptanceInvocation) (RepositoryAcceptanceEvidence, []byte, error) {
				return evidence515(in, "test", "skipped", "no individual completion"), nil, nil
			}}
			if shared {
				q, err := NewCheckQueue(1)
				if err != nil {
					t.Fatal(err)
				}
				defer q.Close()
				b.Checks = q
			}
			start := time.Now()
			built, err := b.Build(context.Background(), root, 484, t.TempDir())
			if err != nil || built == nil || agent.toolErr != nil || built.AgentError != "" {
				t.Fatalf("configured builder: %v, tool=%v, built=%+v", err, agent.toolErr, built)
			}
			if len(r.calls) != 8 || len(r.opens) != 2 {
				t.Fatalf("tool and final check did not both execute all commands: calls=%d environments=%d", len(r.calls), len(r.opens))
			}
			for _, result := range []Result{agent.tool, built.Final} {
				for _, phase := range phases484 {
					if status484(t, result, "api", phase) != "passed" {
						t.Fatal(result.Summary())
					}
				}
			}
			for _, call := range r.calls {
				if call.deadline.After(start.Add(b.Timeout + 100*time.Millisecond)) {
					t.Fatal("tool or final checks renewed the build's shared execution allowance")
				}
			}
			if built.Built() || built.Final.Accepted[plan.Tests[0].Identity()] || result515(t, built.Final, plan.Tests[0]).Status != "skipped" {
				t.Fatal("suite results alone falsely approved individual native acceptance")
			}
			closed484(t, r)
		})
	}
}

func TestRepositorySandboxQueueHoldsSlotThroughCleanup(t *testing.T) {
	root, candidate, snapshot := fixture484(t, []repository.Part{part484("go", "api")}, nil)
	queue, err := NewCheckQueue(1)
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first, second := newRunner484(t), newRunner484(t)
	cleanupEntered, releaseCleanup := make(chan struct{}), make(chan struct{})
	first.closeHook = func(ctx context.Context, _ DependencyRequest) error {
		close(cleanupEntered)
		select {
		case <-releaseCleanup:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	plan := &Plan{Files: []string{"api/main.go"}}
	a, err := queue.register(ctx, sandbox484(t, root, snapshot, first), plan)
	if err != nil {
		t.Fatal(err)
	}
	defer a.finish()
	waiting := make(chan struct{}, 1)
	b, err := queue.register(WithCheckStatus(ctx, func(wait bool) {
		if wait {
			select {
			case waiting <- struct{}{}:
			default:
			}
		}
	}), sandbox484(t, root, snapshot, second), plan)
	if err != nil {
		t.Fatal(err)
	}
	defer b.finish()
	// Release before finish waits for in-flight cleanup, including on failure.
	defer func() {
		select {
		case <-releaseCleanup:
		default:
			close(releaseCleanup)
		}
	}()
	type outcome struct {
		result Result
		err    error
	}
	start := func(build *checkBuild, done chan<- outcome) {
		r, err := (Sandbox{Checks: &CheckClient{Address: queue.listener.Addr().String(), Build: build.id}}).Run(ctx, candidate, plan)
		done <- outcome{r, err}
	}
	firstDone, secondDone := make(chan outcome, 1), make(chan outcome, 1)
	go start(a, firstDone)
	select {
	case <-cleanupEntered:
	case <-ctx.Done():
		t.Fatal("first check did not reach cleanup")
	}
	go start(b, secondDone)
	select {
	case <-waiting:
	case <-ctx.Done():
		t.Fatal("second check did not wait for cleanup")
	}
	second.mu484.Lock()
	opened := len(second.opens)
	second.mu484.Unlock()
	if opened != 0 {
		t.Fatal("queue released the slot while cleanup still owned a container")
	}
	close(releaseCleanup)
	for _, done := range []<-chan outcome{firstDone, secondDone} {
		select {
		case result := <-done:
			if result.err != nil || !result.result.Passed() {
				t.Fatalf("queued check: %v %s", result.err, result.result.Summary())
			}
		case <-ctx.Done():
			t.Fatal("queued check did not complete")
		}
	}
	closed484(t, first)
	closed484(t, second)
}
