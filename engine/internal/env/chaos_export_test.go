package env

import (
	"context"

	"github.com/antifailure/antifailure/engine/internal/fault"
	"github.com/antifailure/antifailure/engine/internal/pgcrash"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The three translations between a manifest, an injector and a report.
//
// They are unexported because nothing outside this file should be turning a
// manifest fault into an injector fault, and they are opened here because they
// are the only part of RunChaos that a test can drive without a live
// environment. Every branch in them is a wiring decision, and a wiring
// decision nobody exercises is the one that carries a kind across wrongly.

func FaultFromForTest(f schema.Fault) fault.Fault { return faultFrom(f) }

func WantsCrashProofForTest(f schema.Fault, cr *schema.CrashRecovery) bool {
	return wantsCrashProof(f, cr)
}

func RecoveryOfForTest(res pgcrash.Result) *report.ChaosRecovery { return recoveryOf(res) }

// RefusedAsUnsafeForTest is the one decision that sorts a refusal from a
// failure, and it is only reachable through a live injector otherwise.
func RefusedAsUnsafeForTest(err error) bool { return refusedAsUnsafe(err) }

// RunOneFaultForTest runs one declared fault through the production step,
// with no durability proof around it, against whatever daemon the injector
// was built on. It is the door the timing tests use: what the step REPORTS
// about how long a fault lasted is only checkable against the step itself.
func RunOneFaultForTest(ctx context.Context, inj *fault.Injector, declared schema.Fault) report.ChaosFault {
	o := &Orchestrator{progress: func(string) {}}
	entry, _ := o.runOneFault(ctx, inj, "", declared, nil)
	return entry
}
