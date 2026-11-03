package factory_test

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/dashboard"
	"github.com/gitdek/invariant/internal/factory"
)

// The dashboard and /act use Waiting and Takes to offer and accept commands.
// A closure observation must leave the original controls usable on reopening.
func TestReopenedIssueKeepsDashboardCommands434(t *testing.T) {
	for _, tc := range []struct{ kind, command string }{
		{factory.KindForks, factory.Choose},
		{factory.KindProposal, factory.Ratify},
		{factory.KindFailed, factory.Retry},
	} {
		t.Run(tc.command, func(t *testing.T) {
			before, closed, reopened, beforeComments, afterComments := factory.ClosedReaderFixture434(t, tc.kind)
			now := time.Date(2026, 9, 25, 13, 0, 0, 0, time.UTC)
			original := dashboard.Lane(before, beforeComments, "gitdek", now)
			if original.Waiting == nil || original.Waiting.Kind != tc.kind || !slices.Contains(original.Waiting.Takes, tc.command) {
				t.Fatalf("the started issue must offer %s: %+v", tc.command, original.Waiting)
			}
			if stopped := dashboard.Lane(closed, afterComments, "gitdek", now); stopped.Waiting != nil {
				t.Fatalf("a closed issue must offer no commands: %+v", stopped.Waiting)
			}
			resumed := dashboard.Lane(reopened, afterComments, "gitdek", now)
			if !reflect.DeepEqual(resumed.Waiting, original.Waiting) {
				t.Fatalf("reopening must restore %s and its original details:\ngot %+v\nwant %+v", tc.command, resumed.Waiting, original.Waiting)
			}
		})
	}
}
