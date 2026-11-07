package formalize

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
	"github.com/gitdek/invariant/internal/toolchain"
)

func TestBaselineMemoSourceAndToolIdentity(t *testing.T) {
	t.Setenv(toolchain.ContainerCPUsEnv, "4")
	t.Setenv("DOCKER_DEFAULT_PLATFORM", "linux/amd64")
	jar := filepath.Join(t.TempDir(), "tlc.jar")
	if err := os.WriteFile(jar, []byte("original jar"), 0600); err != nil {
		t.Fatal(err)
	}
	tc := toolchain.Toolchain{JavaImage: "pinned-java", TLCJar: jar}
	s := &statechart.ComparisonSnapshot{Kind: "ratification", Name: "captured", Module: "Model", Model: "exact model",
		Includes: map[string]string{"Helper.tla": "exact include"}, View: "exact view",
		Lock: project.Lock{Decision: "D-0362", Ratified: &project.Ratification{Issue: 571, By: "owner", Comment: "original"},
			Bounds: map[string]string{"Limit": "2"}, Statements: []project.Statement{{Name: "Safe", Says: "original words", SHA256: "original pin"}}}}
	want, err := baselineKey(s, tc)
	if err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(*statechart.ComparisonSnapshot){
		"module":      func(s *statechart.ComparisonSnapshot) { s.Module = "Other" },
		"model bytes": func(s *statechart.ComparisonSnapshot) { s.Model += "\n" },
		"include name": func(s *statechart.ComparisonSnapshot) {
			s.Includes["Other.tla"] = s.Includes["Helper.tla"]
			delete(s.Includes, "Helper.tla")
		},
		"include bytes":         func(s *statechart.ComparisonSnapshot) { s.Includes["Helper.tla"] += "\n" },
		"view bytes":            func(s *statechart.ComparisonSnapshot) { s.View += "\n" },
		"bounds":                func(s *statechart.ComparisonSnapshot) { s.Lock.Bounds["Limit"] = "3" },
		"statement words":       func(s *statechart.ComparisonSnapshot) { s.Lock.Statements[0].Says = "changed words" },
		"statement pin":         func(s *statechart.ComparisonSnapshot) { s.Lock.Statements[0].SHA256 = "changed pin" },
		"decision":              func(s *statechart.ComparisonSnapshot) { s.Lock.Decision = "D-0001" },
		"ratification metadata": func(s *statechart.ComparisonSnapshot) { s.Lock.Ratified.Comment = "changed comment" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := detachedSnapshot(s)
			edit(changed)
			got, err := baselineKey(changed, tc)
			if err != nil || got == want {
				t.Fatalf("changed source reused identity: %v", err)
			}
		})
	}
	for _, env := range []string{toolchain.ContainerCPUsEnv, "DOCKER_DEFAULT_PLATFORM"} {
		t.Run(env, func(t *testing.T) {
			value := "linux/arm64"
			if env == toolchain.ContainerCPUsEnv {
				value = "2.5"
			}
			t.Setenv(env, value)
			got, err := baselineKey(s, tc)
			if err != nil || got == want {
				t.Fatalf("changed runner configuration reused identity: %v", err)
			}
		})
	}
	if err := os.WriteFile(jar, []byte("changed jar"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := baselineKey(s, tc)
	if err != nil || got == want {
		t.Fatalf("changed jar bytes reused identity: %v", err)
	}
	if err := os.Remove(jar); err != nil {
		t.Fatal(err)
	}
	if _, err := baselineKey(s, tc); err == nil {
		t.Fatal("missing jar retained usable identity")
	}
}

type baselineWaitingContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *baselineWaitingContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestBaselineMemoCanceledWaiter(t *testing.T) {
	jar := filepath.Join(t.TempDir(), "tlc.jar")
	if err := os.WriteFile(jar, []byte("jar"), 0600); err != nil {
		t.Fatal(err)
	}
	tc := toolchain.Toolchain{TLCJar: jar}
	s := &statechart.ComparisonSnapshot{Module: "Model"}
	key, err := baselineKey(s, tc)
	if err != nil {
		t.Fatal(err)
	}
	// An in-flight owner's completion must not be needed for a waiter to
	// observe its own cancellation, or be canceled by that waiter.
	entry := &baselineEntry{done: make(chan struct{})}
	memo := &baselineMemo{entries: map[[sha256.Size]byte]*baselineEntry{key: entry}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	waiter := &baselineWaitingContext{Context: ctx, waiting: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- memo.check(waiter, s, tc) }()
	select {
	case <-waiter.waiting:
	case <-time.After(time.Second):
		t.Fatal("check never waited for the in-flight baseline")
	}
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("waiting check: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled waiter waited for the owner")
	}
	if memo.entries[key] != entry {
		t.Fatal("waiter discarded the owner's pending work")
	}
}
