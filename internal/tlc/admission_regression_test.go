package tlc

import (
	"context"
	"errors"
	"testing"
	"time"
)

func waitTLCQueue(t *testing.T, b *Budget, count int) {
	t.Helper()
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		b.mu.Lock()
		n := len(b.waiters)
		b.mu.Unlock()
		if n == count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("TLC queue did not reach %d waiters", count)
}

func TestTLCAdmissionKeepsFIFOAcrossExclusiveRetries(t *testing.T) {
	b, err := NewBudget(8, 16<<30, 2)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := b.acquire(ctx); err != nil {
		t.Fatal(err)
	}
	start := func(slots int) <-chan error {
		done := make(chan error, 1)
		go func() { done <- b.acquireSlots(ctx, slots) }()
		return done
	}
	first := start(2)
	waitTLCQueue(t, b, 1)
	ordinary := start(1)
	waitTLCQueue(t, b, 2)
	second := start(2)
	waitTLCQueue(t, b, 3)
	b.release()
	for i, done := range []<-chan error{first, ordinary, second} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatalf("waiter %d was starved", i)
		}
		b.mu.Lock()
		used := b.used
		b.mu.Unlock()
		want := 2
		if i == 1 {
			want = 1
		}
		if used != want {
			t.Fatalf("waiter %d shares its allocation: used %d, want %d", i, used, want)
		}
		b.releaseSlots(want)
	}
}

func TestTLCCancelExclusiveWaiterAdmitsOrdinaryWork(t *testing.T) {
	b, err := NewBudget(8, 16<<30, 2)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := b.acquire(ctx); err != nil {
		t.Fatal(err)
	}
	defer b.release()
	queued, stop := context.WithCancelCause(ctx)
	defer stop(context.Canceled)
	exclusive := make(chan error, 1)
	go func() { exclusive <- b.acquireSlots(queued, 2) }()
	waitTLCQueue(t, b, 1)
	ordinary := make(chan error, 1)
	go func() { ordinary <- b.acquire(ctx) }()
	waitTLCQueue(t, b, 2)
	cause := errors.New("cancel exclusive retry")
	stop(cause)
	if err := <-exclusive; !errors.Is(err, cause) {
		t.Fatalf("cancellation lost cause: %v", err)
	}
	select {
	case err := <-ordinary:
		if err != nil {
			t.Fatal(err)
		}
		b.release()
	case <-ctx.Done():
		t.Fatal("canceled head stranded available capacity")
	}
}
