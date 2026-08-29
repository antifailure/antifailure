package controlplane_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/antifailure/antifailure/engine/internal/controlplane"
	"github.com/antifailure/antifailure/engine/internal/events"
)

// The engine and the control plane are two programs in two languages that have
// to agree on one vocabulary, and nothing in either compiler can make them.
//
// They did not agree. typeMap held nine translations and all nine keys named
// events the engine cannot emit, so every event would have missed the map,
// passed through unchanged, and arrived at the control plane as a type outside
// its accepted set. Such an event is stored and advances nothing, so every
// environment would have sat in the dashboard at whatever state it was first
// reported in. The comment on KnownTypes said the map was kept honest by a
// test. There was no such test, which is exactly how it rotted to nine for
// nine.
//
// These are that test. They read the control plane's own list out of its source
// rather than restating it, because a copy of a list is a second thing to keep
// in step and this file exists because keeping two things in step by hand does
// not work.

// ingestPath is the control plane's ingestion module, relative to this package.
const ingestPath = "../../../web/apps/api/src/ingest.ts"

var eventTypesBlock = regexp.MustCompile(`(?s)export const EVENT_TYPES = \[(.*?)\] as const`)
var quoted = regexp.MustCompile(`'([^']+)'`)

// acceptedByControlPlane reads EVENT_TYPES out of the server's source.
func acceptedByControlPlane(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(ingestPath))
	if err != nil {
		t.Fatalf("read the control plane's event list: %v", err)
	}
	m := eventTypesBlock.FindSubmatch(b)
	if m == nil {
		t.Fatalf("%s no longer declares EVENT_TYPES in a form this test can read; "+
			"fix the test rather than deleting it, because the drift it guards is real", ingestPath)
	}
	var out []string
	for _, q := range quoted.FindAllSubmatch(m[1], -1) {
		out = append(out, string(q[1]))
	}
	if len(out) == 0 {
		t.Fatal("EVENT_TYPES parsed as empty, which would make every assertion below vacuous")
	}
	return out
}

func TestEveryMappedTypeIsOneTheEngineCanActuallyEmit(t *testing.T) {
	emittable := map[string]bool{}
	for _, ty := range events.AllTypes() {
		emittable[string(ty)] = true
	}
	for _, k := range controlplane.KnownTypes() {
		if !emittable[k] {
			t.Errorf("typeMap translates %q, which is not an event type the engine declares. "+
				"An event that is never emitted is a translation that never runs, and a "+
				"translation that never runs is how this map came to be wrong nine times out of nine.", k)
		}
	}
}

func TestEveryMappedTypeIsOneTheControlPlaneAccepts(t *testing.T) {
	accepted := acceptedByControlPlane(t)
	for from, to := range controlplane.MappedTypes() {
		if !slices.Contains(accepted, to) {
			t.Errorf("the engine's %q is translated to %q, which %s does not accept. "+
				"An unaccepted type is stored and advances nothing, so the environment "+
				"would sit in the dashboard at the state it was last understood in.",
				from, to, ingestPath)
		}
	}
}

// The reverse direction, and the one that catches a gap rather than a lie.
//
// A type the control plane accepts and nothing in the engine produces is a
// dashboard column that is always empty. Two of them are correct today and are
// named here; a third appearing should be somebody's decision rather than a
// silent hole, so this test fails until it is either mapped or listed.
func TestTheControlPlaneTypesWithNoEngineEventAreTheExpectedOnes(t *testing.T) {
	// environment.queued is produced by the scheduler, which runs in the
	// control plane and never in the engine. artifact.stored belongs to the
	// artifact uploader rather than to the environment lifecycle.
	expected := []string{"artifact.stored", "environment.queued"}

	produced := map[string]bool{}
	for _, to := range controlplane.MappedTypes() {
		produced[to] = true
	}
	var unmapped []string
	for _, a := range acceptedByControlPlane(t) {
		if !produced[a] {
			unmapped = append(unmapped, a)
		}
	}
	slices.Sort(unmapped)

	if !slices.Equal(unmapped, expected) {
		t.Errorf("the control plane accepts %v that no engine event produces; expected exactly %v.\n"+
			"Either map an engine event to it, or add it here with the reason it has no engine "+
			"source. An accepted type nothing produces is a view that is always empty.",
			unmapped, expected)
	}
}

// The engine emits far more than the control plane stores, which is right: the
// dashboard is not a log. What is not right is a lifecycle event going
// unmapped, because those are what the environment matrix is built from.
func TestEveryEnvironmentLifecycleEventIsMapped(t *testing.T) {
	lifecycle := []events.Type{
		events.EnvCreating, events.EnvReady, events.EnvFailed,
		events.EnvSleeping, events.EnvDestroyed,
	}
	mapped := controlplane.MappedTypes()
	for _, ty := range lifecycle {
		if _, ok := mapped[string(ty)]; !ok {
			t.Errorf("%s is not mapped, so an environment reaching that state would never "+
				"be shown to have reached it", ty)
		}
	}
}

// A guard on the guard. If the parse silently returned the wrong thing, every
// test above would pass while checking nothing, which is the failure mode this
// whole file exists to prevent.
func TestTheControlPlaneListParsesToSomethingRecognisable(t *testing.T) {
	accepted := acceptedByControlPlane(t)
	if len(accepted) < 8 {
		t.Fatalf("parsed only %d accepted types (%v), which is too few to be the real list",
			len(accepted), accepted)
	}
	for _, a := range accepted {
		if !strings.Contains(a, ".") {
			t.Fatalf("%q does not look like an event type; the parse is picking up the wrong quotes", a)
		}
	}
	if !slices.Contains(accepted, "environment.ready") {
		t.Fatal("the parsed list does not contain environment.ready, so it is not the list")
	}
}
