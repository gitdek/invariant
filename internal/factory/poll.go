package factory

import (
	"context"
	"crypto/sha256"
	"encoding/json"

	"github.com/gitdek/invariant/internal/github"
)

// listed is the last version discovery saw; read is the one a successful
// step was dispatched for. A poll during that step can change listed, but
// the step must never consume that newer observation when it ends.
type issueObservation struct {
	listed [32]byte
	read   [32]byte
	again  bool
}

type polledIssue struct {
	Number  int
	version [32]byte
}

func issueVersion(issue github.Issue) [32]byte {
	b, _ := json.Marshal(issue)
	return sha256.Sum256(b)
}

type pollKey struct{}
type pollWork struct{ idle bool }

// pollIdle is called only where a step knows it has no continuation. Most
// effects leave another step eligible to read their result and any commands
// still waiting, even if the issue's timestamp hasn't moved.
func pollIdle(ctx context.Context) {
	if work, ok := ctx.Value(pollKey{}).(*pollWork); ok {
		work.idle = true
	}
}

func (f *Factory) pollStep(ctx context.Context, issue polledIssue, merge int) error {
	work := &pollWork{}
	err := f.safeStep(context.WithValue(ctx, pollKey{}, work), issue.Number)
	f.mu.Lock()
	seen := f.observed[issue.Number]
	if err == nil {
		seen.read = issue.version
	}
	seen.again = err != nil || !work.idle
	f.observed[issue.Number] = seen
	f.mu.Unlock()
	f.sawMerge(merge, err)
	return err
}
