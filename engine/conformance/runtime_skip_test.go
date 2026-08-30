package conformance

import (
	"strings"
	"testing"

	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// TestNoContainmentBehaviorCanBeSkipped is the guard on the one promise this
// product cannot make advisory.
//
// Every other behavior here can be skipped by a runtime that says it does not
// do that thing, and the skip is printed by name so a reader can see which
// guarantee went unmeasured. Containment is different: a runtime that could
// declare its way out of it would be a supported way to ship one that lets
// environments reach the internet, so nothing skips an Egress_ behavior.
//
// This existed as a property of how the switch was written and not as
// anything checked, and it had already stopped being true. SkipSlow, which is
// a convenience for a fast local run, skipped
// Egress_NamesDoNotCrossEnvironments, the behavior that proves a service name
// in one environment does not resolve to another environment's service. It
// was the slowest behavior in the suite and it is also the isolation promise,
// and a knob that trades the second for the first is the wrong knob.
//
// So the invariant is asserted rather than described. The inputs are the worst
// case a caller can construct: every capability false, every skip enabled.
func TestNoContainmentBehaviorCanBeSkipped(t *testing.T) {
	nothingWorks := provider.RuntimeCaps{}
	everythingSkipped := RuntimeOptions{SkipSlow: true}

	var checked int
	for _, b := range runtimeBehaviors {
		if !strings.HasPrefix(b.Name, "Egress_") {
			continue
		}
		checked++
		if reason := runtimeSkipReason(b, nothingWorks, everythingSkipped); reason != "" {
			t.Errorf("%s can be skipped (%q). A containment behavior that a caller "+
				"can turn off makes the egress guarantee advisory, which is the one "+
				"thing this suite exists to prevent.", b.Name, reason)
		}
	}

	// A loop that matched nothing would pass this and mean nothing, which is
	// the fault this package's own self test exists to catch elsewhere.
	if checked == 0 {
		t.Fatal("no Egress_ behaviors were checked; either they were renamed or " +
			"the roster is empty, and either way this test is no longer guarding anything")
	}
}

// TestSkippableBehaviorsAreStillSkippable is the other direction.
//
// A guard that refused every skip would pass the test above and quietly turn
// declared capabilities into failures, so the behaviors that are supposed to
// be skippable are checked to still be.
func TestSkippableBehaviorsAreStillSkippable(t *testing.T) {
	nothingWorks := provider.RuntimeCaps{}

	cases := map[string]RuntimeOptions{
		"Up_ReportsAReachableURL":           {},
		"Logs_ReturnWhatAServiceWrote":      {},
		"Down_TouchesOnlyItsOwnEnvironment": {SkipSlow: true},
	}
	for name, opts := range cases {
		var found bool
		for _, b := range runtimeBehaviors {
			if b.Name != name {
				continue
			}
			found = true
			if runtimeSkipReason(b, nothingWorks, opts) == "" {
				t.Errorf("%s is no longer skippable, so a runtime that cannot do it "+
					"now fails instead of saying so by name", name)
			}
		}
		if !found {
			t.Errorf("%s is not in the roster; this test is naming a behavior that "+
				"does not exist", name)
		}
	}
}
