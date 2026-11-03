package verify_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/verify"
)

func TestTLCResourceDiscoveryRefusals(t *testing.T) {
	f := tlc497Setup(t)
	for _, model := range []bool{false, true} {
		var err error
		missing := filepath.Join(t.TempDir(), "missing-project")
		if model {
			_, err = verify.RunModel(tlc497Context(t), missing, f.tc, verify.Options{TLCParallel: -1})
		} else {
			_, err = verify.Run(tlc497Context(t), missing, "", f.tc, verify.Options{TLCParallel: -1})
		}
		if err == nil || !strings.Contains(err.Error(), "TLCParallel") || len(f.events(t)) != 0 {
			t.Fatalf("negative option reached project or Docker setup: %v, %v", err, f.events(t))
		}
	}
	t.Setenv("TLC497_INFO", `{"NCPU":8,"MemTotal":134217728}`)
	if _, err := verify.RunModel(tlc497Context(t), tlc497Project(t), f.tc); err == nil {
		t.Fatal("known insufficient Docker memory accepted")
	}
	_, modules, _ := f.runs(t)
	if len(modules) != 0 {
		t.Fatalf("insufficient memory started checks: %v", modules)
	}
}

func TestTLCQueuedDeadlineAndStandaloneDump(t *testing.T) {
	f := tlc497Setup(t)
	r := f.runner(tlc497Budget(t, 1))
	ctx := tlc497Context(t)
	cfg := tlc.Config{Specification: "Spec"}
	held := make(chan error, 1)
	dir := t.TempDir()
	go func() {
		_, err := r.Check(ctx, dir, "Hold", cfg)
		held <- err
	}()
	f.waitStart(t, "Hold")
	qctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	_, err := r.CheckWithin(qctx, t.TempDir(), "NeverStart", cfg, time.Minute)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("queue lost overall deadline: %v", err)
	}
	tlc497Write(t, filepath.Join(f.root, "release"), nil)
	if err := <-held; err != nil {
		t.Fatal(err)
	}
	_, modules, _ := f.runs(t)
	if modules["NeverStart"] != 0 {
		t.Fatal("expired queued check started a container")
	}
	if _, err := f.runner(nil).Dump(ctx, t.TempDir(), "Dump", cfg); err != nil {
		t.Fatal(err)
	}
	_, _, names := f.runs(t)
	f.limits(t, names[len(names)-1:], 1<<30, 768<<20)
}
