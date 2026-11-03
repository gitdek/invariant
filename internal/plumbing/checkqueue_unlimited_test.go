package plumbing

import (
	"context"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/synth"
)

type unlimitedCheckAgent struct {
	writer *q458Agent
	jobs   []synth.Job
}

func (*unlimitedCheckAgent) Name() string { return "unlimited check fixture" }

func (a *unlimitedCheckAgent) Run(ctx context.Context, job synth.Job) (synth.Usage, error) {
	a.jobs = append(a.jobs, job)
	u, err := a.writer.Run(ctx, job)
	if err == nil && len(a.jobs) == 1 {
		return u, synth.ErrThinkingLoop
	}
	return u, err
}

func TestQueuedBuildToolAllowanceSurvivesFallbackAndRepair(t *testing.T) {
	for _, tc := range []struct {
		name    string
		queued  bool
		timeout time.Duration
		want    time.Duration
	}{
		{name: "unlimited", queued: true, want: -1},
		{name: "negative_is_also_uncut", queued: true, timeout: -time.Second, want: -1},
		{name: "limited", queued: true, timeout: time.Minute, want: time.Minute},
		{name: "ordinary_unlimited"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := q458New(t, 1)
			b := f.build("allowance")
			a := &unlimitedCheckAgent{writer: b.agent}
			b.builder.Backend = a
			b.builder.Timeout = tc.timeout
			b.builder.FallbackEffort = "xhigh"
			if !tc.queued {
				b.builder.Checks = nil
			}
			reviews := 0
			b.builder.Reviewer = &q458Reviewer{run: func(context.Context) (string, error) {
				reviews++
				if reviews == 1 {
					return "Revise the implementation.\nVERDICT: changes needed", nil
				}
				return "VERDICT: approve", nil
			}}
			b.mark(1, "release")
			b.mark(2, "release")
			b.start()
			b.passed()
			if len(a.jobs) != 3 || reviews != 2 || b.result.Fallback == nil {
				t.Fatalf("want initial run, fallback, repair and two reviews; jobs=%d reviews=%d result=%+v", len(a.jobs), reviews, b.result)
			}
			for i, job := range a.jobs {
				if job.ToolTimeout != tc.want {
					t.Errorf("writing run %d tool allowance = %v, want %v", i+1, job.ToolTimeout, tc.want)
				}
			}
		})
	}
}
