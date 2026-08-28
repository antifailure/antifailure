package hud_test

import (
	"strings"
	"testing"
	"time"

	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/internal/hud"
	"pgregory.net/rapid"
)

func ev(seq uint64, t events.Type, data map[string]any) events.Event {
	return events.Event{
		Env: "env1", Seq: seq, Type: t, Level: events.LevelInfo,
		TS: time.Date(2026, 1, 1, 0, 0, int(seq), 0, time.UTC), Data: data,
	}
}

func TestInOrderEventsAreApplied(t *testing.T) {
	m := hud.New("env1", 80, 24)
	m.Apply(ev(1, "service.started", map[string]any{"service": "web", "state": "running", "url": "http://x"}))
	m.Apply(ev(2, "service.started", map[string]any{"service": "worker", "state": "running"}))

	svcs := m.Services()
	if len(svcs) != 2 {
		t.Fatalf("want two services, got %d", len(svcs))
	}
	if svcs[0].Name != "web" || !svcs[0].Ready {
		t.Errorf("first service should be a ready web, got %+v", svcs[0])
	}
	if m.Dropped() != 0 {
		t.Errorf("nothing should have been dropped, got %d", m.Dropped())
	}
}

// Rows must not reshuffle between frames. A dashboard whose lines move every
// time a map is ranged over is unreadable even when every line is correct.
func TestServiceOrderIsStableAcrossUpdates(t *testing.T) {
	m := hud.New("env1", 80, 24)
	for i, name := range []string{"web", "worker", "cron", "api"} {
		m.Apply(ev(uint64(i+1), "service.started", map[string]any{"service": name, "state": "running"}))
	}
	first := names(m.Services())

	for i, name := range []string{"api", "cron", "worker", "web"} {
		m.Apply(ev(uint64(i+5), "service.started", map[string]any{"service": name, "state": "healthy"}))
	}
	if got := names(m.Services()); got != first {
		t.Errorf("order changed from %q to %q", first, got)
	}
}

func names(svcs []hud.Service) string {
	var b []string
	for _, s := range svcs {
		b = append(b, s.Name)
	}
	return strings.Join(b, ",")
}

// The reorder window: an event that arrives early waits for its predecessor
// and both are then shown in sequence.
func TestAnEarlyEventWaitsForItsPredecessor(t *testing.T) {
	m := hud.New("env1", 80, 24)
	m.Apply(ev(1, "service.started", map[string]any{"service": "web", "state": "running"}))

	// 3 arrives before 2.
	m.Apply(ev(3, "service.started", map[string]any{"service": "cron", "state": "running"}))
	if len(m.Services()) != 1 {
		t.Fatalf("the early event must be held, got %v", names(m.Services()))
	}

	m.Apply(ev(2, "service.started", map[string]any{"service": "worker", "state": "running"}))
	if got := names(m.Services()); got != "web,worker,cron" {
		t.Errorf("both should now be applied in sequence, got %q", got)
	}
	if m.Dropped() != 0 {
		t.Errorf("a reordering is not a drop, got %d", m.Dropped())
	}
}

// A gap that never fills must not stall the dashboard for ever. Past the
// window the model gives up, says so, and carries on.
func TestAGapThatNeverFillsIsAbandonedRatherThanStallingTheDashboard(t *testing.T) {
	m := hud.New("env1", 80, 24)
	m.Apply(ev(1, "service.started", map[string]any{"service": "web", "state": "running"}))

	// Sequence 2 never arrives. Everything after it piles up.
	for seq := uint64(3); seq < 3+hud.ReorderWindow+1; seq++ {
		m.Apply(ev(seq, "service.started", map[string]any{"service": "svc", "state": "running"}))
	}

	if len(m.Services()) < 2 {
		t.Fatalf("past the window the held events must be released, got %v", names(m.Services()))
	}
	if m.Dropped() == 0 {
		t.Error("abandoning the gap must be counted, or the HUD lies by omission")
	}
}

// A straggler that arrives after its slot has passed is counted, not applied
// out of order.
func TestAnEventFromThePastIsDroppedAndCounted(t *testing.T) {
	m := hud.New("env1", 80, 24)
	m.Apply(ev(5, "service.started", map[string]any{"service": "web", "state": "running"}))
	m.Apply(ev(6, "service.started", map[string]any{"service": "worker", "state": "running"}))

	m.Apply(ev(5, "service.started", map[string]any{"service": "ghost", "state": "running"}))
	if got := names(m.Services()); strings.Contains(got, "ghost") {
		t.Errorf("a replayed sequence must not be applied again, got %q", got)
	}
	if m.Dropped() != 1 {
		t.Errorf("want one drop counted, got %d", m.Dropped())
	}
}

// Attaching to an environment that is already running must work: the first
// sequence seen sets the expectation rather than being treated as a gap from 1.
func TestAttachingToARunningEnvironmentStartsFromWhatArrives(t *testing.T) {
	m := hud.New("env1", 80, 24)
	m.Apply(ev(9000, "service.started", map[string]any{"service": "web", "state": "running"}))
	if len(m.Services()) != 1 {
		t.Fatalf("the first event seen must be applied, got %v", names(m.Services()))
	}
	if m.Dropped() != 0 {
		t.Errorf("attaching late is not a drop, got %d", m.Dropped())
	}
}

// Another environment's events are counted but not shown, because the count is
// how somebody notices they are watching the wrong one.
func TestAnotherEnvironmentsEventsAreCountedButNotShown(t *testing.T) {
	m := hud.New("env1", 80, 24)
	other := ev(1, "service.started", map[string]any{"service": "web", "state": "running"})
	other.Env = "env2"
	m.Apply(other)

	if len(m.Services()) != 0 {
		t.Errorf("another environment must not appear, got %v", names(m.Services()))
	}
	if m.Count("service.started") != 1 {
		t.Error("but it must be counted")
	}
}

func TestErrorsArePaneled(t *testing.T) {
	m := hud.New("env1", 80, 24)
	e := ev(1, "env.failed", nil)
	e.Level = events.LevelError
	e.Msg = "the build failed"
	m.Apply(e)

	if len(m.Errors()) != 1 {
		t.Fatalf("want one error, got %d", len(m.Errors()))
	}
}

// A HUD that remembers everything is a memory leak with a user interface.
func TestTheTailIsBounded(t *testing.T) {
	m := hud.New("env1", 80, 24)
	for i := 1; i <= hud.TailLines+250; i++ {
		m.Apply(ev(uint64(i), "service.started", map[string]any{"service": "web", "state": "running"}))
	}
	if got := len(m.Tail()); got != hud.TailLines {
		t.Errorf("tail should be capped at %d, got %d", hud.TailLines, got)
	}
	// And it must keep the NEWEST, not the oldest.
	last := m.Tail()[len(m.Tail())-1]
	if last.Seq != uint64(hud.TailLines+250) {
		t.Errorf("the tail should end at the newest event, got seq %d", last.Seq)
	}
}

func TestNetworkDecisionsAreCounted(t *testing.T) {
	m := hud.New("env1", 80, 24)
	m.Apply(ev(1, "egress.decision", map[string]any{"decision": "allow"}))
	m.Apply(ev(2, "egress.decision", map[string]any{"decision": "deny", "host": "evil.example"}))
	m.Apply(ev(3, "egress.decision", map[string]any{"decision": "mock"}))

	n := m.Network()
	if n.Allowed != 1 || n.Denied != 1 || n.Mocked != 1 {
		t.Errorf("counts wrong: %+v", n)
	}
	if n.LastDenied != "evil.example" {
		t.Errorf("the denied host should be shown, got %q", n.LastDenied)
	}
}

// A percentage that has been through JSON is a float64, and a type switch on
// int alone reads zero for every event that came from a file or a socket.
func TestProgressSurvivesARoundTripThroughJSON(t *testing.T) {
	m := hud.New("env1", 80, 24)
	m.Apply(ev(1, events.MaskProgress, map[string]any{"percent": float64(42)}))
	if got := m.Database().Percent; got != 42 {
		t.Errorf("percent from a float64 should be 42, got %d", got)
	}

	m2 := hud.New("env1", 80, 24)
	m2.Apply(ev(1, events.MaskProgress, map[string]any{"percent": 42}))
	if got := m2.Database().Percent; got != 42 {
		t.Errorf("percent from an int should be 42, got %d", got)
	}
}

func TestVerificationAndFindings(t *testing.T) {
	m := hud.New("env1", 80, 24)
	m.Apply(ev(1, events.MaskFinding, map[string]any{}))
	m.Apply(ev(2, events.MaskFinding, map[string]any{}))
	m.Apply(ev(3, events.MaskVerified, map[string]any{"version": "gv_1"}))

	d := m.Database()
	if d.Findings != 2 || !d.Verified || d.Version != "gv_1" {
		t.Errorf("database pane wrong: %+v", d)
	}
}

func TestTruncateCutsOnRunesAndMarksTheCut(t *testing.T) {
	cases := []struct {
		in    string
		width int
		want  string
	}{
		{"web", 10, "web"},
		{"a-very-long-service-name", 8, "a-very-…"},
		{"web", 3, "web"},
		{"webs", 3, "we…"},
		{"web", 0, ""},
		{"web", 1, "…"},
		// A name with a multi byte rune must not be cut mid character.
		{"café-service", 5, "café…"},
	}
	for _, c := range cases {
		if got := hud.Truncate(c.in, c.width); got != c.want {
			t.Errorf("Truncate(%q, %d) = %q, want %q", c.in, c.width, got, c.want)
		}
	}
}

// The exit criterion from the spec: no panic under any sequence of events.
// Rapid generates the nasty ones, including sequences that go backwards, repeat
// and skip, and payloads whose fields are the wrong type entirely.
func TestAnySequenceOfEventsRendersWithoutPanic(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		m := hud.New(rapid.SampledFrom([]string{"", "env1"}).Draw(t, "env"), 80, 24)

		n := rapid.IntRange(0, 200).Draw(t, "n")
		for i := 0; i < n; i++ {
			e := events.Event{
				Env: rapid.SampledFrom([]string{"", "env1", "env2"}).Draw(t, "eventEnv"),
				Seq: uint64(rapid.IntRange(0, 50).Draw(t, "seq")),
				Type: events.Type(rapid.SampledFrom([]string{
					"service.started", "env.ready", "egress.decision",
					"mask.progress", "mask.verified", "mask.finding",
					"agent.started", "db.branched", "something.unknown", "",
				}).Draw(t, "type")),
				Level: events.Level(rapid.SampledFrom([]string{"info", "warn", "error", ""}).Draw(t, "level")),
				Msg:   rapid.StringN(0, 40, -1).Draw(t, "msg"),
			}
			// Payloads that are the wrong shape on purpose. A dashboard must
			// not crash on a field somebody added or renamed.
			if rapid.Bool().Draw(t, "hasData") {
				e.Data = map[string]any{
					"service":  rapid.SampledFrom([]any{"web", 42, nil, true}).Draw(t, "service"),
					"percent":  rapid.SampledFrom([]any{1, float64(2), "three", nil}).Draw(t, "percent"),
					"decision": rapid.SampledFrom([]any{"allow", "deny", 7, nil}).Draw(t, "decision"),
				}
			}
			m.Apply(e)
		}

		// Every accessor must be safe to call afterwards.
		_ = m.Services()
		_ = m.Agents()
		_ = m.Errors()
		_ = m.Tail()
		_ = m.Network()
		_ = m.Database()
		_ = m.Elapsed()
		if m.Dropped() < 0 {
			t.Fatal("dropped went negative")
		}
	})
}

// A run that branches from a golden verified last week never emits
// mask.verified, so the pane has to take the answer from the field the engine
// attaches. Without this it called every verified golden unverified, on every
// ordinary af up, which is the one case that happens most.
func TestDatabasePaneTakesVerifiedFromTheEvent(t *testing.T) {
	m := hud.New("pr-482", 80, 24)
	e := ev(1, events.GoldenReady, map[string]any{"version": "gv_1", "verified": true})
	e.Env = "pr-482"
	m.Apply(e)

	if !m.Database().Verified {
		t.Error("a golden reported as verified should show as verified")
	}

	e = ev(2, events.GoldenReady, map[string]any{"version": "gv_2", "verified": false})
	e.Env = "pr-482"
	m.Apply(e)
	if m.Database().Verified {
		t.Error("a golden reported as unverified should not show as verified")
	}
}
