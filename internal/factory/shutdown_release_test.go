package factory

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/github"
)

type shutdownReleaseRepo struct {
	Repo
	sha        string
	record     LeaseRecord
	pushes     int
	beforePush func(context.Context, LeaseRecord)
}

func (r *shutdownReleaseRepo) Lease(context.Context) (string, LeaseRecord, error) {
	return r.sha, r.record, nil
}

func (r *shutdownReleaseRepo) PushLease(ctx context.Context, old string, rec LeaseRecord) (string, error) {
	r.beforePush(ctx, rec)
	if old != r.sha {
		return "", ErrLeaseMoved
	}
	r.pushes++
	r.sha, r.record = fmt.Sprint(r.pushes), rec
	return r.sha, nil
}

func (r *shutdownReleaseRepo) ClearWorktrees(context.Context) error { return nil }

type shutdownReleaseHub struct {
	GitHub
	cancel context.CancelFunc
}

func (h shutdownReleaseHub) OpenIssues(context.Context) ([]github.Issue, error) {
	h.cancel()
	return nil, context.Canceled
}

// A shutdown has a live, bounded context for its release even though the
// watcher's context is canceled. A competing write after the release read
// must still win: checking only the holder before a blind push isn't enough.
func TestShutdownLeaseReleaseContextAndRace422(t *testing.T) {
	for _, race := range []bool{false, true} {
		t.Run(fmt.Sprintf("replacement=%v", race), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			repo := &shutdownReleaseRepo{}
			releases := 0
			repo.beforePush = func(cleanup context.Context, rec LeaseRecord) {
				if rec.Holder != "" {
					return
				}
				releases++
				if ctx.Err() == nil || cleanup.Err() != nil {
					t.Errorf("shutdown contexts: watcher = %v, cleanup = %v", ctx.Err(), cleanup.Err())
				}
				deadline, ok := cleanup.Deadline()
				if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 5*time.Second {
					t.Errorf("cleanup deadline = %v, %v; want a live context bounded by five seconds", deadline, ok)
				}
				if race {
					repo.sha = "replacement"
					repo.record = LeaseRecord{Holder: "successor", Until: time.Now().Add(time.Hour)}
				}
			}
			f := &Factory{Repo: repo, GitHub: shutdownReleaseHub{cancel: cancel}, Work: t.TempDir(),
				Holder: "stopping", LeaseFor: 5 * time.Minute}
			if err := f.Watch(ctx, time.Hour); !errors.Is(err, context.Canceled) {
				t.Fatalf("Watch = %v; want cancellation", err)
			}
			if releases != 1 || f.holds() {
				t.Fatalf("shutdown releases = %d, still holds = %v", releases, f.holds())
			}
			if race {
				if repo.sha != "replacement" || repo.record.Holder != "successor" {
					t.Fatalf("release overwrote its successor: %s %+v", repo.sha, repo.record)
				}
			} else if repo.record.Holder != "" || time.Now().Before(repo.record.Until.Add(leaseSkew)) {
				t.Fatalf("release leaves a lease another watcher must wait for: %+v", repo.record)
			}
		})
	}
}
