package plumbing

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckQueueRejectsUnavailableAdmission(t *testing.T) {
	for _, limit := range []int{0, -1} {
		if q, err := NewCheckQueue(limit); err == nil {
			q.Close()
			t.Fatalf("accepted check limit %d", limit)
		}
	}
	q, err := NewCheckQueue(1)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	s := Sandbox{Checks: &CheckClient{Address: q.listener.Addr().String(), Build: "unknown"}}
	if _, err := s.Run(t.Context(), t.TempDir(), &Plan{}); err == nil || !strings.Contains(err.Error(), "no active build") {
		t.Fatalf("unregistered checks were not refused: %v", err)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Run(t.Context(), t.TempDir(), &Plan{}); err == nil || !strings.Contains(err.Error(), "queue is unavailable") {
		t.Fatalf("closed queue was bypassed: %v", err)
	}
	if _, err := q.register(t.Context(), Sandbox{}, &Plan{}); err == nil {
		t.Fatal("registered a build after the watcher stopped")
	}
}

func TestCheckQueueCloseStopsActiveAndWaitingBuilds(t *testing.T) {
	f := q458New(t, 1)
	first, waiting := f.build("closing-first"), f.build("closing-waiting")
	first.start()
	first.awaitSandbox(1)
	waiting.start()
	waiting.awaitStatus(true)
	if err := f.queue.Close(); err != nil {
		t.Fatal(err)
	}
	for _, b := range []*q458Build{first, waiting} {
		r, err := b.finish()
		if err == nil || r == nil || r.Built() {
			t.Fatalf("closed watcher accepted build %s: %+v, %v", b.id, r, err)
		}
	}
	waiting.sandboxAbsent(1)
}

func TestCheckQueueHoldsSlotThroughCanceledSandboxCleanup(t *testing.T) {
	f := q458New(t, 1)
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Fatal(err)
	}
	// Hold Docker's cancellation cleanup independently of the test process.
	// The next build must wait for rm to finish, even after its client leaves.
	script := strings.Replace(q458Docker, `[ "$1" = run ] || exit 0`, `if [ "$1" = rm ]; then
  mkdir "$Q458_CONTROL/removing-1.started"
  while [ ! -e "$Q458_CONTROL/removing-1.release" ] && [ ! -e "$Q458_CONTROL/shutdown" ]; do sleep 0.02; done
  exit 0
fi
[ "$1" = run ] || exit 0`, 1)
	q458Write(t, docker, script, 0o755)
	first, later := f.build("cleanup"), f.build("after-cleanup")
	first.start()
	first.awaitSandbox(1)
	later.mark(1, "release")
	later.start()
	later.awaitStatus(true)
	first.cancel()
	// The cleanup marker uses the fixture's sandbox marker convention.
	marker := &q458Build{f: f, id: "removing"}
	marker.awaitSandbox(1)
	later.sandboxAbsent(1)
	q458Write(t, filepath.Join(f.control, "removing-1.release"), "", 0o644)
	r, err := first.finish()
	if r == nil || r.Built() || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled build: %+v, %v", r, err)
	}
	later.passed()
}
