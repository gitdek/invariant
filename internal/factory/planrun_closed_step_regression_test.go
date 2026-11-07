package factory

import (
	"context"
	"testing"
)

// A watcher that stops between opening a step's issue and recording it
// leaves one issue even when someone closes that issue before a fresh
// watcher comes. The plan's core counts every issue ever opened for a step,
// so the fresh watcher records the closed issue instead of opening a second
// (#754).
func TestAClosedUnrecordedStepIsntOpenedAgain(t *testing.T) {
	r, hub, plan := planRig(t, logPipeline...)
	r.f.GitHub = &stopAtRecord{planHub: hub, plan: plan.Issue}
	if err := r.f.WorkPlan(context.Background(), plan); err != nil {
		t.Logf("the watcher stopped: %v", err)
	}
	if records := postsOnPlan(r, plan, KindPlanOpened); len(hub.opened) != 1 || len(records) != 0 {
		t.Fatalf("the watcher that stopped opened %v and recorded %d; want one issue, not recorded", hub.opened, len(records))
	}
	n := hub.opened[0]
	r.gh.issues[n].State = "closed"

	r.f = freshWatcher(r, hub)
	workOn(t, r.f, plan)
	workOn(t, r.f, plan)
	if len(hub.opened) != 1 {
		t.Fatalf("after the crash and the close, the plan has opened %v; want its one issue, not a second", hub.opened)
	}
	expectRecords(t, r, plan, n)
}
