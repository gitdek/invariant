package factory

import (
	"testing"
	"time"

	"github.com/gitdek/invariant/factory/recovery/recovery"
)

// The recovery core refuses what would do an effect twice, out of order, or
// without the lease (D-0069).
func TestTheCoreRefusesWhatWouldRepeatAnEffect(t *testing.T) {
	f := &Factory{}
	for _, c := range []struct {
		what string
		ok   bool
	}{
		{"start a draft's run", f.canStartRun(drafting(RunNone), recovery.Solve)},
		{"post a finished draft", f.canPost(drafting(RunDone), recovery.Solve)},
		{"say a recorded draft stopped", f.canReportStopped(drafting(RunRecorded), recovery.Solve)},
		{"push the ratification", f.canPushRatification(ratifying(false))},
		{"say it's ratified", f.canPost(ratifying(true), recovery.Ratify)},
		{"start the build's run", f.canStartRun(building(RunNone, false, false), recovery.Build)},
		{"push the build's code", f.canPushCode(building(RunDone, false, false))},
		{"open the pull request", f.canOpenPullRequest(building(RunDone, true, false))},
		{"post the build", f.canPost(building(RunDone, true, true), recovery.Build)},
		{"merge", f.canMerge(merging(true, false))},
		{"say it merged", f.canPost(merging(true, true), recovery.Merge)},
		{"say it can't merge", f.canPost(merging(false, false), recovery.Merge)},
		{"answer with a note", f.canPost(noting(), recovery.Note)},
	} {
		if !c.ok {
			t.Errorf("the core refused to %s", c.what)
		}
	}
	for _, c := range []struct {
		what string
		ok   bool
	}{
		{"start a draft's run again", f.canStartRun(drafting(RunRecorded), recovery.Solve)},
		{"post a draft whose run never finished", f.canPost(drafting(RunRecorded), recovery.Solve)},
		{"say a finished draft stopped", f.canReportStopped(drafting(RunDone), recovery.Solve)},
		{"push the ratification again", f.canPushRatification(ratifying(true))},
		{"say it's ratified before the push", f.canPost(ratifying(false), recovery.Ratify)},
		{"start the build's run again", f.canStartRun(building(RunDone, false, false), recovery.Build)},
		{"push code before the run finished", f.canPushCode(building(RunRecorded, false, false))},
		{"open a second pull request", f.canOpenPullRequest(building(RunDone, true, true))},
		{"open a pull request before the code is pushed", f.canOpenPullRequest(building(RunDone, false, false))},
		{"post the build before its pull request", f.canPost(building(RunDone, true, false), recovery.Build)},
		{"merge twice", f.canMerge(merging(true, true))},
		{"merge what GitHub can't", f.canMerge(merging(false, false))},
		{"say it merged before merging", f.canPost(merging(true, false), recovery.Merge)},
	} {
		if c.ok {
			t.Errorf("the core allowed a watcher to %s", c.what)
		}
	}

	// A watcher without the lease can't take any effect.
	now := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
	leased := &Factory{LeaseFor: 5 * time.Minute, Now: func() time.Time { return now }}
	for _, c := range []struct {
		what string
		ok   bool
	}{
		{"start a draft's run", leased.canStartRun(drafting(RunNone), recovery.Solve)},
		{"post a note", leased.canPost(noting(), recovery.Note)},
		{"merge", leased.canMerge(merging(true, false))},
	} {
		if c.ok {
			t.Errorf("the core allowed a watcher without the lease to %s", c.what)
		}
	}
}
