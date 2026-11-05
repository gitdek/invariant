package conformance

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/tla"
	"github.com/gitdek/invariant/internal/tlc"
)

func TestStreamingSchedulerStorageFailures(t *testing.T) {
	previous := runtime.GOMAXPROCS(3)
	defer runtime.GOMAXPROCS(previous)
	for _, mode := range []string{"scheduler", "checker"} {
		for _, order := range []string{"storage first", "semantic first"} {
			t.Run(mode+"/"+order, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				dir, blocked, tmp := t.TempDir(), t.TempDir(), t.TempDir()
				path := filepath.Join(blocked, "Invariant_Tried.tla")
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
				t.Setenv("TMPDIR", tmp)
				t.Setenv("TMP", tmp)
				t.Setenv("TEMP", tmp)
				started, stopped := make(chan struct{}), make(chan struct{})
				var entered, joined atomic.Int32
				semantic := tlc.Result{Outcome: tlc.Violated, Invariant: "Invariant_EveryStepTried",
					Trace:   []tlc.State{{Vars: []tlc.Var{{Name: "invariant_i", Value: "1"}}}},
					Printed: []string{`<<"invariant-untried", {<<"Inc">>}>>`}}
				check := func(ctx context.Context, b int) (tlc.Result, error) {
					if entered.Add(1) == 3 {
						close(started)
					}
					defer joined.Add(1)
					select {
					case <-started:
					case <-ctx.Done():
						return tlc.Result{}, ctx.Err()
					}
					switch b {
					case 0:
						if order == "storage first" {
							// Batch 2 is stopped only after the scheduler processes
							// batch 1's error, so completion order is deterministic.
							select {
							case <-stopped:
							case <-ctx.Done():
								return tlc.Result{}, ctx.Err()
							}
						}
						return semantic, nil
					case 1:
						if order == "semantic first" {
							<-ctx.Done()
						}
						// A pending storage operation can fail even after this
						// batch is canceled, including alongside cancellation.
						err := writeModule(ctx, blocked, "Invariant_Tried", func(*moduleWriter) error {
							t.Error("writing a module over a directory succeeded")
							return nil
						})
						return tlc.Result{}, errors.Join(err, ctx.Err())
					case 2:
						<-ctx.Done()
						close(stopped)
						return tlc.Result{}, fmt.Errorf("batch stopped: %w", errors.Join(ctx.Err(), context.Cause(ctx)))
					default:
						return tlc.Result{}, fmt.Errorf("scheduled batch %d after failure", b)
					}
				}
				var err error
				if mode == "scheduler" {
					var batch int
					var result tlc.Result
					batch, result, err = atOnce(ctx, 10000000, check)
					if batch != 0 || !reflect.DeepEqual(result, semantic) {
						t.Fatalf("lost earliest semantic result: batch %d, %+v", batch, result)
					}
				} else {
					var raw strings.Builder
					raw.WriteString(`{"states":[`)
					for i := 0; i < 3000; i++ {
						if i > 0 {
							raw.WriteByte(',')
						}
						fmt.Fprintf(&raw, `{"n":%d}`, i)
					}
					raw.WriteString(`],"init":[0],"attempts":[]}`)
					runner := declaredAttemptRunner(func(ctx context.Context, dir, module string, _ tlc.Config) (tlc.Result, error) {
						if module != "Invariant_Tried" {
							return tlc.Result{Outcome: tlc.Passed}, nil
						}
						b, err := strconv.Atoi(strings.TrimPrefix(filepath.Base(dir), "tried-"))
						if err != nil {
							return tlc.Result{}, err
						}
						return check(ctx, b-1)
					})
					var result Result
					result, err = CheckReader(ctx, runner, dir, "Counter", nil, []string{"n"}, 3000,
						strings.NewReader(raw.String()), &Model{Steps: []tla.Step{{Name: "Inc"}}})
					if !reflect.DeepEqual(result, Result{ModelStates: 3000}) {
						t.Fatalf("storage failure retained partial evidence: %+v tried=%+v", result, result.Tried)
					}
				}
				var pathErr *os.PathError
				if !errors.As(err, &pathErr) || pathErr.Path != path {
					t.Fatalf("lost module storage failure: %v", err)
				}
				if entered.Load() != 3 || joined.Load() != 3 {
					t.Fatalf("started=%d joined=%d, want 3", entered.Load(), joined.Load())
				}
				for _, path := range []string{dir, tmp} {
					if entries, err := os.ReadDir(path); err != nil || len(entries) != 0 {
						t.Fatalf("private storage remains: %v, %v", entries, err)
					}
				}
			})
		}
	}
}

func TestStreamingSchedulerCancellationCauses(t *testing.T) {
	previous := runtime.GOMAXPROCS(2)
	defer runtime.GOMAXPROCS(previous)
	for _, scheduler := range []bool{false, true} {
		t.Run(fmt.Sprintf("scheduler=%v", scheduler), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			started := make(chan struct{})
			n := 1
			if scheduler {
				n = 2
			}
			batch, result, err := atOnce(ctx, n, func(ctx context.Context, b int) (tlc.Result, error) {
				if !scheduler {
					return tlc.Result{Outcome: tlc.Violated}, context.Canceled
				}
				if b == 0 {
					select {
					case <-started:
					case <-ctx.Done():
						return tlc.Result{}, ctx.Err()
					}
					return tlc.Result{Outcome: tlc.Violated}, nil
				}
				close(started)
				<-ctx.Done()
				return tlc.Result{}, fmt.Errorf("batch stopped: %w", errors.Join(ctx.Err(), context.Cause(ctx)))
			})
			if batch != 0 || result.Outcome != tlc.Violated {
				t.Fatalf("lost earliest semantic result: batch %d, %+v", batch, result)
			}
			if scheduler && err != nil || !scheduler && !errors.Is(err, context.Canceled) {
				t.Fatalf("scheduler=%v: cancellation error %v", scheduler, err)
			}
		})
	}
}
