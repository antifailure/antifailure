package controlplane_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
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
// A type the control plane accepts and no engine event is MAPPED to is a
// dashboard column that is always empty. Two of them are that way today and are
// named here; a third appearing should be somebody's decision rather than a
// silent hole, so this test fails until it is either mapped or listed.
//
// Being mapped is not the same as being emitted, and this test cannot tell the
// difference. TestEveryMappedTypeHasSomethingInTheEngineThatEmitsIt is the one
// that can, and it exists because environment.sleeping is mapped, passes here,
// and is still a column that is always empty.
func TestTheControlPlaneTypesWithNoEngineEventAreTheExpectedOnes(t *testing.T) {
	// Neither has an engine event mapped to it, and neither is built. See the
	// comment on KnownTypes in sink.go for what each is reserved for.
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

// The batch ceiling is the same number written out twice, and its comment on
// this side claims it matches the control plane's.
//
// It does today. Nothing made it so. Raising it on the server alone leaves
// every engine splitting batches it did not need to split; raising it here
// alone turns each oversized batch into a refusal from the far end, after the
// events have been serialised and sent. Both are silent, and both are the sort
// of thing somebody changes on one side while reading the other side's comment
// saying they agree.
var maxBatchDecl = regexp.MustCompile(`export const MAX_BATCH = (\d+)`)

func TestTheBatchCeilingIsTheSameOnBothSides(t *testing.T) {
	b, err := os.ReadFile(filepath.Clean(ingestPath))
	if err != nil {
		t.Fatalf("read the control plane's ingestion module: %v", err)
	}
	m := maxBatchDecl.FindSubmatch(b)
	if m == nil {
		t.Fatalf("%s no longer declares MAX_BATCH in a form this test can read; "+
			"fix the test rather than deleting it, because the engine's MaxBatch "+
			"comment claims to match it and nothing else checks that", ingestPath)
	}
	theirs, err := strconv.Atoi(string(m[1]))
	if err != nil {
		t.Fatalf("MAX_BATCH parsed as %q, which is not a number", m[1])
	}
	if theirs != controlplane.MaxBatch {
		t.Errorf("the engine sends up to %d events in one request and %s accepts %d. "+
			"The lower of the two is the real limit, and whichever side is wrong is wrong "+
			"quietly: too high and every full batch is refused after being sent, too low "+
			"and every run splits requests it did not need to.",
			controlplane.MaxBatch, ingestPath, theirs)
	}
}

// ---------------------------------------------------------------------------
// Mapped is not emitted
// ---------------------------------------------------------------------------

// engineRoot is the module this test walks, relative to this package. Read the
// same way ingestPath is, because a list restated in a test is a second thing
// to keep in step and this file exists because that does not work.
const engineRoot = "../.."

// eventDefs is the events package's own declarations, which is where a type
// name is bound to the string that travels on the wire.
const eventDefs = "../events/event.go"

var typeConst = regexp.MustCompile(`(?m)^\s*([A-Za-z][A-Za-z0-9]*)\s+Type\s*=\s*"([^"]+)"`)

// emitCall matches a type constant handed to something that puts it on the bus,
// rather than any mention of it at all.
//
// The distinction is the whole test. A reference is not a production:
// env.sleeping has a reference, in the type map this file checks, and no
// emitter, and it is the case that motivated writing this. hud.Model.Count and
// hud.Plain.Suppressed both take an events.Type and are consumers, so a
// reference count would call a type live because something displays it.
//
// The four names are the complete set of ways a constant reaches the bus:
// Orchestrator.emit, event and eventErr in internal/env, and Bus.Emit under
// them. emitHelpers below fails if that stops being true, because a fifth one
// added without updating this pattern would make every type it emits look
// unemitted, which is a loud failure rather than a silent one.
var emitCall = regexp.MustCompile(`\b(?:emit|event|eventErr|Emit)\(\s*[^)\n]*?\bevents\.([A-Z][A-Za-z0-9]*)`)

// emitHelpers matches any orchestrator method that takes an event type at all,
// which is the only shape a new way to reach the bus can have.
//
// Matching the three known names instead would count removals and renames and
// miss the case that matters, an ADDED helper, because the count of the three
// stays three. That was the first version of this and it went green against a
// deliberately added fourth.
var emitHelpers = regexp.MustCompile(`(?m)^func \(o \*Orchestrator\) (\w+)\([^\n]*events\.Type`)

const orchestrator = "../env/env.go"

// emittedTypes reports which engine event types something in the engine
// actually names outside the two files that only describe them.
//
// Source scanning rather than reflection, because Go erases constant names at
// run time and the question is precisely "does any line of this program hand
// this constant to an emitter". Three exclusions, each of which would otherwise
// make every type look emitted: the events package declares them all, sink.go
// maps them all, and a generated file embeds the source of other files as
// string literals, so a match inside it is a match on a copy rather than on
// code that runs.
func emittedTypes(t *testing.T) map[string]bool {
	t.Helper()

	defs, err := os.ReadFile(filepath.Clean(eventDefs))
	if err != nil {
		t.Fatalf("read the event type declarations: %v", err)
	}
	valueOf := map[string]string{}
	for _, m := range typeConst.FindAllSubmatch(defs, -1) {
		valueOf[string(m[1])] = string(m[2])
	}
	if len(valueOf) == 0 {
		t.Fatalf("%s no longer declares event types in a form this test can read; "+
			"fix the test rather than deleting it, because the gap it guards is real", eventDefs)
	}

	// The pattern below knows the names of the emit helpers. If a fifth way to
	// reach the bus appears, every type it emits would look unemitted, so this
	// says so rather than letting the gate quietly weaken.
	orch, err := os.ReadFile(filepath.Clean(orchestrator))
	if err != nil {
		t.Fatalf("read the orchestrator: %v", err)
	}
	var helpers []string
	for _, m := range emitHelpers.FindAllSubmatch(orch, -1) {
		helpers = append(helpers, string(m[1]))
	}
	slices.Sort(helpers)
	if known := []string{"emit", "event", "eventErr"}; !slices.Equal(helpers, known) {
		t.Fatalf("%s declares %v as the methods taking an event type and this test's "+
			"pattern knows %v; add the new one to emitCall, because a type only it emits "+
			"would otherwise read as emitted by nothing", orchestrator, helpers, known)
	}

	emitted := map[string]bool{}
	err = filepath.WalkDir(engineRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, ".gen.go") {
			return nil
		}
		clean := filepath.ToSlash(path)
		if strings.Contains(clean, "/internal/events/") || strings.HasSuffix(clean, "/controlplane/sink.go") {
			return nil
		}
		b, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			return err
		}
		for _, m := range emitCall.FindAllSubmatch(b, -1) {
			if v, ok := valueOf[string(m[1])]; ok {
				emitted[v] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the engine source: %v", err)
	}
	if len(emitted) == 0 {
		t.Fatal("no engine event is emitted anywhere, which would make this assertion vacuous")
	}
	return emitted
}

// A type that is mapped and never emitted is the same empty column as a type
// that is not mapped at all, and the test above cannot see it.
//
// That is not hypothetical, which is why this exists. Five of the eleven mapped
// types are in that state, and every one of them passes every other assertion
// in this file. environment.sleeping is the clearest: it is mapped from
// env.sleeping, and no line of the engine has ever emitted it, because idle
// sleep is not built:
// runtime.idle_sleep is defaulted in manifest normalization, checked by
// validation, printed by af explain, and read by nothing that acts on it. That
// is the identical shape runtime.ttl had before the reaper was written, when
// its own comment said it was "declared, validated, defaulted and printed by af
// explain, and READ BY NOTHING". The pattern recurs, so it is worth a name and
// worth a gate.
//
// Fails in both directions on purpose. A type gaining an emitter without being
// taken off this list fails, because a stale exemption is how a gate stops
// checking anything. A type losing its emitter, or being added with neither an
// emitter nor an entry, fails for the reason the list exists.
func TestEveryMappedTypeHasSomethingInTheEngineThatEmitsIt(t *testing.T) {
	// Five, and the number is the finding. Each is listed rather than deleted
	// because deleting any of them removes a value from the control plane's
	// accepted set, and the sleeping one is an enum value in a shipped schema,
	// which is a migration rather than a comment fix.
	//
	// agent.started, agent.finished and agent.verdict are the whole agent run
	// lifecycle. Nothing in the engine emits any of them, so the control
	// plane's runs, its verdicts and every view built on them are fed by
	// nobody. The same gap is visible from the other end: no INSERT into the
	// runs table exists on the ingestion path either.
	//
	// egress.decision is disconnected rather than unbuilt. The decisions are
	// made, Orchestrator.Decisions returns them, af net and af ci render them,
	// and internal/hud already classifies egress.decision as a type to suppress
	// as noisy. Producer and consumer both exist and the wire between them does
	// not, which makes it the most finishable of the five.
	//
	// env.sleeping is reserved for idle sleep, which is not built at all.
	//
	// The three agent types are the set where the code does not say which it
	// is. Nothing consumes them either, so there is no half-built wire to read
	// intent from, and saying so is more useful than guessing.
	expected := []string{
		"agent.finished", "agent.started", "agent.verdict",
		"egress.decision", "env.sleeping",
	}

	emitted := emittedTypes(t)

	var unemitted []string
	for from := range controlplane.MappedTypes() {
		if !emitted[from] {
			unemitted = append(unemitted, from)
		}
	}
	slices.Sort(unemitted)

	if !slices.Equal(unemitted, expected) {
		t.Errorf("the engine maps %v to a control plane type and emits none of them; expected exactly %v.\n"+
			"A mapped type nothing emits is a column that is always empty, and it passes every "+
			"other assertion in this file. Either emit it, or list it here with the reason the "+
			"capability behind it is not built.",
			unemitted, expected)
	}

	// The exemption is only worth having if it is about a real type. A name
	// left here after the type was deleted is an exemption guarding nothing.
	for _, e := range expected {
		if _, ok := controlplane.MappedTypes()[e]; !ok {
			t.Errorf("%q is exempted here and is not a mapped type at all, so the exemption "+
				"guards nothing; remove it", e)
		}
	}
}
