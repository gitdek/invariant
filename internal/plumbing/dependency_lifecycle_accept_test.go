package plumbing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/repository"
)

func cleaned468(r *runner468, token string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, got := range r.cleanups {
		if got == token {
			return true
		}
	}
	return false
}

func TestRepositoryDependencyFailedInstallNeverBecomesReusable(t *testing.T) {
	part := part468("go", ".")
	_, snapshot := snapshot468(t, []repository.Part{part}, files468("go", "."))
	r := newRunner468(t)
	p := DependencyPreparer{CacheDir: t.TempDir(), Platform: "linux/amd64", Runner: r}
	r.installHook = func(_ context.Context, q DependencyRequest) (DependencyArtifact, error) {
		// A failed process can leave even a complete-looking image behind.
		// Its exit status must prevent publication regardless of those bytes.
		return r.finish(q), errors.New("locked installation failed after producing output")
	}
	if _, err := p.Prepare(context.Background(), snapshot, []string{part.Name}); err == nil {
		t.Fatal("failed installation produced a usable session")
	}
	r.mu.Lock()
	failed := append([]DependencyRequest(nil), r.installs...)
	r.installHook = nil
	r.mu.Unlock()
	if len(failed) == 0 {
		t.Fatal("fixture never reached installation")
	}
	for _, q := range failed {
		if !cleaned468(r, q.Token) {
			t.Errorf("failed installation resources were not cleaned: %s", q.Token)
		}
	}
	prepare468(t, p, snapshot, part.Name)
	r.mu.Lock()
	installed := len(r.installs)
	r.mu.Unlock()
	if installed <= len(failed) {
		t.Fatal("next preparation reused output from the failed installation")
	}
}

func TestRepositoryDependencyInspectionRejectsIncompleteAndMismatchedArtifacts(t *testing.T) {
	mutations := []struct {
		name   string
		change func(*DependencyArtifact)
	}{
		{"incomplete", func(a *DependencyArtifact) { a.Complete = false }},
		{"mutable image", func(a *DependencyArtifact) { a.Image = "fixture:latest" }},
		{"different immutable image", func(a *DependencyArtifact) { a.Image = hash468("wrong environment") }},
		{"different base", func(a *DependencyArtifact) { a.Base = "registry.example/wrong@" + hash468("wrong base") }},
		{"mutable resolved base", func(a *DependencyArtifact) { a.BaseImage = "fixture:base" }},
		{"different resolved base", func(a *DependencyArtifact) { a.BaseImage = hash468("different resolved base") }},
		{"wrong platform", func(a *DependencyArtifact) { a.Platform = "linux/arm64" }},
		{"wrong recipe", func(a *DependencyArtifact) { a.Key = hash468("wrong recipe") }},
		{"missing input", func(a *DependencyArtifact) { delete(a.Inputs, "go.sum") }},
		{"changed input", func(a *DependencyArtifact) { a.Inputs["go.sum"] = hash468("wrong lock") }},
		{"extra input", func(a *DependencyArtifact) { a.Inputs["undeclared"] = hash468("extra") }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			part := part468("go", ".")
			_, snapshot := snapshot468(t, []repository.Part{part}, files468("go", "."))
			r := newRunner468(t)
			p := DependencyPreparer{CacheDir: t.TempDir(), Platform: "linux/amd64", Timeout: time.Second, Runner: r}
			r.inspectHook = func(a DependencyArtifact) (DependencyArtifact, error) {
				mutation.change(&a)
				return a, nil
			}
			if _, err := p.Prepare(context.Background(), snapshot, []string{part.Name}); err == nil {
				t.Fatal("installation was published despite invalid independent inspection")
			}
			r.mu.Lock()
			failedInstalls, inspections := len(r.installs), len(r.inspections)
			r.inspectHook = nil
			r.mu.Unlock()
			if failedInstalls == 0 || inspections == 0 {
				t.Fatal("fixture did not exercise installation and independent inspection")
			}
			valid := prepare468(t, p, snapshot, part.Name)
			r.mu.Lock()
			installed := len(r.installs)
			r.mu.Unlock()
			if installed <= failedInstalls {
				t.Fatal("preparation reused an artifact whose initial inspection failed")
			}
			if !valid.Artifacts()[part.Name].Complete {
				t.Fatal("successful repair lacks completion evidence")
			}
			// Once published, the same image must still be inspected at reuse.
			r.mu.Lock()
			r.inspectHook = func(a DependencyArtifact) (DependencyArtifact, error) {
				mutation.change(&a)
				return a, nil
			}
			r.mu.Unlock()
			if _, err := p.Prepare(context.Background(), snapshot, []string{part.Name}); err == nil {
				t.Fatal("cached artifact bypassed inspection of its now-invalid evidence")
			}
		})
	}
	for _, mode := range []string{"missing image", "inspection failure"} {
		t.Run(mode, func(t *testing.T) {
			part := part468("python", ".")
			_, snapshot := snapshot468(t, []repository.Part{part}, files468("python", "."))
			r := newRunner468(t)
			p := DependencyPreparer{CacheDir: t.TempDir(), Platform: "linux/amd64", Timeout: time.Second, Runner: r}
			first := prepare468(t, p, snapshot, part.Name).Artifacts()[part.Name]
			r.mu.Lock()
			if mode == "missing image" {
				delete(r.images, first.Image)
			} else {
				r.inspectHook = func(DependencyArtifact) (DependencyArtifact, error) {
					return DependencyArtifact{}, errors.New("cannot read installation evidence")
				}
			}
			before := len(r.installs)
			r.mu.Unlock()
			x, err := p.Prepare(context.Background(), snapshot, []string{part.Name})
			if err != nil {
				return // Refusal and a fully verified rebuild are both permitted.
			}
			r.mu.Lock()
			installed := len(r.installs)
			r.mu.Unlock()
			if mode == "inspection failure" || installed <= before || x == nil || !x.Artifacts()[part.Name].Complete {
				t.Fatal("unverifiable cached artifact was reused without a successful fresh preparation")
			}
		})
	}
}

func TestRepositoryDependencyConcurrentCreatorsCannotConsumePartialArtifacts(t *testing.T) {
	part := part468("typescript", ".")
	_, snapshot := snapshot468(t, []repository.Part{part}, files468("typescript", "."))
	r := newRunner468(t)
	p := DependencyPreparer{CacheDir: t.TempDir(), Platform: "linux/amd64", Runner: r}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	finish := func() { releaseOnce.Do(func() { close(release) }) }
	defer finish()
	r.installHook = func(ctx context.Context, q DependencyRequest) (DependencyArtifact, error) {
		a := r.finish(q)
		// Distinct creators may produce distinct immutable IDs. Never model
		// an immutable image as changing underneath another creator.
		a.Image = hash468("concurrent environment:" + q.Token)
		a.Complete = false
		r.mu.Lock()
		r.images[a.Image] = cloneArtifact468(a)
		r.mu.Unlock()
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-ctx.Done():
			return a, ctx.Err()
		case <-release:
			a.Complete = true
			r.mu.Lock()
			r.images[a.Image] = cloneArtifact468(a)
			r.mu.Unlock()
			return a, nil
		}
	}
	type outcome struct {
		session *DependencySession
		err     error
	}
	first, second := make(chan outcome, 1), make(chan outcome, 1)
	start := func(done chan<- outcome) {
		x, err := p.Prepare(ctx, snapshot, []string{part.Name})
		done <- outcome{x, err}
	}
	go start(first)
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("first creator never reached installation")
	}
	go start(second)
	// A second creator can wait or perform its own private install. Neither
	// may return success while every installation is still incomplete.
	select {
	case result := <-second:
		t.Fatalf("concurrent preparation returned before any install completed: %v", result.err)
	case <-time.After(50 * time.Millisecond):
	}
	finish()
	for _, done := range []<-chan outcome{first, second} {
		select {
		case result := <-done:
			if result.err != nil || result.session == nil {
				t.Fatalf("concurrent preparation failed: %v", result.err)
			}
			a := result.session.Artifacts()[part.Name]
			if !a.Complete || a.Image == "" {
				t.Fatalf("concurrent creator consumed a partial artifact: %+v", a)
			}
		case <-ctx.Done():
			t.Fatal("concurrent creators failed to finish within their original deadline")
		}
	}
	r.mu.Lock()
	count := len(r.installs)
	requests := append([]DependencyRequest(nil), r.installs...)
	r.mu.Unlock()
	if count < 1 {
		t.Errorf("two concurrent callers performed %d installations", count)
	}
	if len(requests) == 2 && (requests[0].Token == requests[1].Token || requests[0].SourceDir == requests[1].SourceDir) {
		t.Error("concurrent creators shared mutable installation resources")
	}
	prepare468(t, p, snapshot, part.Name)
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.installs) != count {
		t.Error("completed concurrent preparation did not publish a reusable artifact")
	}
}

func TestRepositoryDependencyCancellationCleansUpWithoutPublishing(t *testing.T) {
	part := part468("python", ".")
	_, snapshot := snapshot468(t, []repository.Part{part}, files468("python", "."))
	r := newRunner468(t)
	p := DependencyPreparer{CacheDir: t.TempDir(), Platform: "linux/amd64", Runner: r}
	started := make(chan DependencyRequest, 1)
	r.installHook = func(ctx context.Context, q DependencyRequest) (DependencyArtifact, error) {
		a := r.finish(q)
		started <- q
		<-ctx.Done()
		return a, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := p.Prepare(ctx, snapshot, []string{part.Name})
		done <- err
	}()
	var q DependencyRequest
	select {
	case q = <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("preparation did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled preparation succeeded")
		}
	case <-time.After(SandboxCleanupAllowance + time.Second):
		t.Fatal("cancellation exceeded the cleanup reserve")
	}
	if !cleaned468(r, q.Token) {
		t.Fatal("cancelled container resources were not cleaned")
	}
	if _, err := os.Lstat(q.SourceDir); !os.IsNotExist(err) {
		t.Fatalf("cancelled preparation left its staged dependency inputs: %v", err)
	}
	r.mu.Lock()
	before := len(r.installs)
	r.installHook = nil
	r.mu.Unlock()
	prepare468(t, p, snapshot, part.Name)
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.installs) <= before {
		t.Fatal("a later preparation reused cancelled installation output")
	}
}

func TestRepositoryDependencyPreparationAndChecksShareOriginalDeadline(t *testing.T) {
	parts := []repository.Part{part468("go", "first"), part468("python", "second")}
	files := files468("go", "first")
	for name, body := range files468("python", "second") {
		files[name] = body
	}
	root, snapshot := snapshot468(t, parts, files)
	r := newRunner468(t)
	p := DependencyPreparer{CacheDir: t.TempDir(), Platform: "linux/amd64", Timeout: 2 * time.Second, Runner: r}
	r.installHook = func(ctx context.Context, q DependencyRequest) (DependencyArtifact, error) {
		select {
		case <-ctx.Done():
			return DependencyArtifact{}, ctx.Err()
		case <-time.After(20 * time.Millisecond):
			return r.finish(q), nil
		}
	}
	r.runHook = func(ctx context.Context, _ DependencyRequest) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	started := time.Now()
	x := prepare468(t, p, snapshot, parts[0].Name, parts[1].Name)
	checkContext, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := x.Run(checkContext, parts[0].Name, root, []string{"check-fixture"}); err == nil {
		t.Fatal("check outlived the session deadline without failure")
	}
	// An expired session must not obtain fresh time by starting another check.
	if _, err := x.Run(checkContext, parts[1].Name, root, []string{"check-fixture"}); err == nil {
		t.Fatal("a later check renewed an expired session deadline")
	}
	r.mu.Lock()
	deadlines := append([]time.Time(nil), r.deadlines...)
	requests := append([]DependencyRequest(nil), r.installs...)
	requests = append(requests, r.runs...)
	r.mu.Unlock()
	if len(deadlines) < 3 {
		t.Fatalf("fixture did not observe both cold installs and a check: %v", deadlines)
	}
	for _, deadline := range deadlines {
		if !deadline.Equal(deadlines[0]) {
			t.Errorf("cold installation or later check renewed the deadline: %v versus %v", deadline, deadlines[0])
		}
	}
	if deadlines[0].After(started.Add(p.Timeout + 100*time.Millisecond)) {
		t.Error("preparation deadline exceeds its requested allowance")
	}
	for _, q := range requests {
		if !cleaned468(r, q.Token) {
			t.Errorf("deadline failure left resources for %s", q.Token)
		}
	}
}

func TestRepositoryDependencyCheckCancellationPreservesVerifiedArtifact(t *testing.T) {
	part := part468("go", ".")
	root, snapshot := snapshot468(t, []repository.Part{part}, files468("go", "."))
	r := newRunner468(t)
	p := DependencyPreparer{CacheDir: t.TempDir(), Platform: "linux/amd64", Timeout: 10 * time.Second, Runner: r}
	parent, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	parentDeadline, _ := parent.Deadline()
	x, err := p.Prepare(parent, snapshot, []string{part.Name})
	if err != nil || x == nil {
		t.Fatalf("prepare under a shorter parent deadline: %v", err)
	}
	started := make(chan DependencyRequest, 1)
	r.runHook = func(ctx context.Context, q DependencyRequest) ([]byte, error) {
		started <- q
		<-ctx.Done()
		return nil, ctx.Err()
	}
	done := make(chan error, 1)
	go func() {
		// Session cancellation must propagate even if this caller supplies
		// a fresh context without the original parent deadline.
		_, err := x.Run(context.Background(), part.Name, root, []string{"check-fixture"})
		done <- err
	}()
	var q DependencyRequest
	select {
	case q = <-started:
	case <-parent.Done():
		t.Fatal("prepared session never started its check")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("check succeeded after its preparation parent was cancelled")
		}
	case <-time.After(SandboxCleanupAllowance + time.Second):
		t.Fatal("check cancellation exceeded the cleanup reserve")
	}
	if !cleaned468(r, q.Token) {
		t.Fatal("cancelled check resources were not cleaned")
	}
	r.mu.Lock()
	deadlines := append([]time.Time(nil), r.deadlines...)
	r.runHook = nil
	r.mu.Unlock()
	for _, deadline := range deadlines {
		if !deadline.Equal(parentDeadline) {
			t.Errorf("preparation/check failed to retain the shorter parent deadline: %v, want %v", deadline, parentDeadline)
		}
	}
	prepare468(t, p, snapshot, part.Name)
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.installs) != 1 {
		t.Error("cancelling a private candidate check destroyed the verified reusable artifact")
	}
}

func TestRepositoryDependencyCandidateStorageCannotPoisonLaterSessions(t *testing.T) {
	part := part468("go", ".")
	root, snapshot := snapshot468(t, []repository.Part{part}, files468("go", "."))
	r := newRunner468(t)
	shared := t.TempDir()
	p := DependencyPreparer{CacheDir: shared, Platform: "linux/amd64", Runner: r}
	x := prepare468(t, p, snapshot, part.Name)
	artifact := x.Artifacts()[part.Name]
	var hookMu sync.Mutex
	tokens := map[string]bool{}
	r.runHook = func(_ context.Context, q DependencyRequest) ([]byte, error) {
		hookMu.Lock()
		defer hookMu.Unlock()
		if tokens[q.Token] {
			return nil, errors.New("candidate sessions shared a mutable container")
		}
		tokens[q.Token] = true
		if q.Network || q.Pull || q.Image != artifact.Image {
			return nil, errors.New("candidate check did not use its offline immutable artifact")
		}
		// Empty CacheDir means writable state is entirely inside disposable
		// container storage. If host scratch is used, it must be private to
		// this session, never the cache holding reusable dependency records.
		if q.CacheDir != "" {
			if filepath.Clean(q.CacheDir) == filepath.Clean(shared) {
				return nil, errors.New("candidate received the shared dependency cache writable")
			}
			marker := filepath.Join(q.CacheDir, "candidate-poison")
			if _, err := os.Lstat(marker); !os.IsNotExist(err) {
				return nil, fmt.Errorf("candidate inherited a previous session's writable state: %v", err)
			}
			if err := os.MkdirAll(q.CacheDir, 0o700); err != nil {
				return nil, err
			}
			if err := os.WriteFile(marker, []byte("malicious candidate output"), 0o600); err != nil {
				return nil, err
			}
		}
		return []byte("checked offline\n"), nil
	}
	if _, err := x.Run(context.Background(), part.Name, root, []string{"check-fixture"}); err != nil {
		t.Fatal(err)
	}
	y := prepare468(t, p, snapshot, part.Name)
	if got := y.Artifacts()[part.Name]; !reflect.DeepEqual(got, artifact) {
		t.Fatalf("candidate changed shared dependency evidence: got %+v, want %+v", got, artifact)
	}
	if _, err := y.Run(context.Background(), part.Name, root, []string{"check-fixture"}); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.installs) != 1 {
		t.Error("candidate output invalidated or poisoned the immutable dependency cache")
	}
}

func TestRepositoryDependencyBudgetsCannotBeExtended(t *testing.T) {
	part := part468("go", ".")
	_, snapshot := snapshot468(t, []repository.Part{part}, files468("go", "."))
	for _, timeout := range []time.Duration{0, 2 * SandboxTimeout} {
		r := newRunner468(t)
		p := DependencyPreparer{CacheDir: t.TempDir(), Platform: "linux/amd64", Timeout: timeout, Runner: r}
		started := time.Now()
		prepare468(t, p, snapshot, part.Name)
		r.mu.Lock()
		deadlines := append([]time.Time(nil), r.deadlines...)
		r.mu.Unlock()
		if len(deadlines) == 0 {
			t.Fatal("fixture observed no bounded installation")
		}
		for _, deadline := range deadlines {
			if deadline.After(started.Add(SandboxTimeout + 100*time.Millisecond)) {
				t.Fatalf("timeout %v extended the production allowance: deadline=%v", timeout, deadline)
			}
		}
	}
}
