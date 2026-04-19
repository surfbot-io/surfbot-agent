package v1

import (
	"context"

	"github.com/surfbot-io/surfbot-agent/internal/model"
)

// Dispatcher is the narrow surface API handlers need from the master
// ticker when they must fan out an ad-hoc scan request. The production
// type is *intervalsched.Scheduler (satisfied via duck typing — no
// scheduler change required); callers in a non-daemon process (e.g.
// `surfbot ui` without an in-process ticker) pass nil and the dispatch
// endpoints return 503.
//
// This is the only interface SPEC-SCHED1.3a introduces. Every other
// dependency is consumed as a concrete type.
type Dispatcher interface {
	DispatchAdHoc(ctx context.Context, run model.AdHocScanRun) (scanID string, err error)
}
