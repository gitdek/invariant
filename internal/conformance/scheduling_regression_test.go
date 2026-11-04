package conformance

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/tlc"
)

func TestStreamingSchedulerCanceledBeforeDispatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := atOnce(ctx, 1<<30, func(context.Context, int) (tlc.Result, error) {
		t.Error("a canceled invocation dispatched a batch")
		return tlc.Result{Outcome: tlc.Passed}, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
}

func TestStreamingSchedulerShuffledFailures(t *testing.T) {
	previous := runtime.GOMAXPROCS(3)
	defer runtime.GOMAXPROCS(previous)
	for seed := int64(0); seed < 10; seed++ {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			entered := make(chan int, 3)
			release := []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{})}
			done := make(chan error, 1)
			var extra atomic.Int32
			go func() {
				batch, result, err := atOnce(ctx, 10000000, func(ctx context.Context, b int) (tlc.Result, error) {
					if b >= len(release) {
						extra.Add(1)
						return tlc.Result{}, fmt.Errorf("dispatched future batch %d after failure", b)
					}
					entered <- b
					select {
					case <-ctx.Done():
						return tlc.Result{}, ctx.Err()
					case <-release[b]:
						return tlc.Result{Outcome: tlc.Violated, Message: fmt.Sprint(b)}, nil
					}
				})
				if err == nil && (batch != 0 || result.Message != "0") {
					err = fmt.Errorf("earliest result: batch %d, %+v", batch, result)
				}
				if extra.Load() != 0 {
					err = fmt.Errorf("dispatched %d future windows after failure", extra.Load())
				}
				done <- err
			}()
			for i := 0; i < 3; i++ {
				select {
				case <-entered:
				case <-ctx.Done():
					t.Fatal("concurrent windows did not start")
				}
			}
			for _, b := range rand.New(rand.NewSource(seed)).Perm(3) {
				close(release[b])
				runtime.Gosched()
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}
