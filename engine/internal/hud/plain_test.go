package hud_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/internal/hud"
)

func TestSignificantEventsGetALine(t *testing.T) {
	var b strings.Builder
	p := hud.NewPlain(&b, "env1")

	p.Write(ev(1, "env.ready", nil))
	p.Write(ev(2, "db.branched", map[string]any{"version": "gv_1"}))

	lines := strings.Count(strings.TrimSpace(b.String()), "\n") + 1
	if lines != 2 {
		t.Errorf("want two lines, got %d:\n%s", lines, b.String())
	}
	if p.Shown() != 2 {
		t.Errorf("Shown = %d, want 2", p.Shown())
	}
}

// The reason this exists: a build log with four hundred lines of mask.progress
// in it has buried the one line that says what went wrong.
func TestProgressEventsAreFoldedAwayRatherThanPrinted(t *testing.T) {
	var b strings.Builder
	p := hud.NewPlain(&b, "env1")

	for i := 1; i <= 400; i++ {
		p.Write(ev(uint64(i), events.MaskProgress, map[string]any{"percent": i / 4}))
	}
	if b.Len() != 0 {
		t.Fatalf("progress must not print a line each, got:\n%s", b.String())
	}
	if got := p.Suppressed(events.MaskProgress); got != 400 {
		t.Errorf("want 400 suppressed, got %d", got)
	}

	p.Summary()
	out := b.String()
	if !strings.Contains(out, "400") || !strings.Contains(out, string(events.MaskProgress)) {
		t.Errorf("the summary must say what was folded away, got %q", out)
	}
}

// Suppressing a denial to keep a log tidy would be optimising the wrong thing:
// it is exactly the line somebody greps for.
func TestAWarningOrErrorIsAlwaysShownEvenForANoisyType(t *testing.T) {
	var b strings.Builder
	p := hud.NewPlain(&b, "env1")

	e := ev(1, "egress.decision", map[string]any{"decision": "deny", "host": "evil.example"})
	e.Level = events.LevelWarn
	p.Write(e)

	out := b.String()
	if !strings.Contains(out, "evil.example") {
		t.Errorf("a denied host must be printed, got %q", out)
	}
	if !strings.Contains(out, "WARN") {
		t.Errorf("and it must be marked, got %q", out)
	}
}

func TestANoisyTypeAtInfoIsFolded(t *testing.T) {
	var b strings.Builder
	p := hud.NewPlain(&b, "env1")
	p.Write(ev(1, "egress.decision", map[string]any{"decision": "allow"}))
	if b.Len() != 0 {
		t.Errorf("an allowed request at info is not worth a line, got %q", b.String())
	}
}

func TestDebugIsNeverSignificant(t *testing.T) {
	e := ev(1, "env.ready", nil)
	e.Level = events.LevelDebug
	if hud.Significant(e) {
		t.Error("debug must never earn a line in a build log")
	}
}

func TestAnotherEnvironmentIsNotPrinted(t *testing.T) {
	var b strings.Builder
	p := hud.NewPlain(&b, "env1")
	e := ev(1, "env.ready", nil)
	e.Env = "env2"
	p.Write(e)
	if b.Len() != 0 {
		t.Errorf("another environment must not appear, got %q", b.String())
	}
}

// A line saying zero events were hidden is itself noise.
func TestSummaryPrintsNothingWhenNothingWasFolded(t *testing.T) {
	var b strings.Builder
	p := hud.NewPlain(&b, "env1")
	p.Write(ev(1, "env.ready", nil))
	before := b.Len()
	p.Summary()
	if b.Len() != before {
		t.Errorf("summary should be silent, got %q", b.String()[before:])
	}
}

func TestTheTimestampIsPrintedInUTC(t *testing.T) {
	var b strings.Builder
	p := hud.NewPlain(&b, "env1")
	e := ev(1, "env.ready", nil)
	e.TS = time.Date(2026, 1, 1, 13, 45, 7, 0, time.FixedZone("somewhere", 5*3600))
	p.Write(e)
	if !strings.Contains(b.String(), "08:45:07") {
		t.Errorf("a log read by somebody in another zone needs UTC, got %q", b.String())
	}
}

// Unlike the model, this is written to from whatever goroutine produced the
// event, so it has to be safe under the race detector.
func TestPlainIsSafeUnderConcurrentWriters(t *testing.T) {
	var b strings.Builder
	p := hud.NewPlain(&b, "")

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				p.Write(ev(uint64(n*50+j+1), "env.ready", nil))
			}
		}(i)
	}
	wg.Wait()

	if p.Shown() != 400 {
		t.Errorf("every write should have been counted once, got %d", p.Shown())
	}
}

// A fallback whose output has gone to a closed pipe must not report success.
type failingWriter struct{ n int }

func (f *failingWriter) Write(p []byte) (int, error) {
	f.n++
	if f.n > 1 {
		return 0, errors.New("broken pipe")
	}
	return len(p), nil
}

func TestAWriteFailureIsKeptRatherThanIgnored(t *testing.T) {
	w := &failingWriter{}
	p := hud.NewPlain(w, "env1")

	p.Write(ev(1, "env.ready", nil))
	if p.Err() != nil {
		t.Fatalf("the first write succeeds: %v", p.Err())
	}
	p.Write(ev(2, "env.ready", nil))
	if p.Err() == nil {
		t.Fatal("a failed write must be reported, not swallowed")
	}

	// Only the first is kept: the four hundredth failure says nothing new.
	first := p.Err()
	p.Write(ev(3, "env.ready", nil))
	if !errors.Is(p.Err(), first) {
		t.Error("the first error should be the one kept")
	}
}
