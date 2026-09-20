package env

import (
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
