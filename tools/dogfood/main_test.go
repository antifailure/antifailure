package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Every budget names a step some phase or step actually produces.
//
// A budget for a step nothing emits is a budget that never fires, and it reads
// in review as a guard that is being enforced. This is the same argument
// gatecheck makes about a stale exemption.
func TestBudgets_EveryOneIsReachable(t *testing.T) {
	produced := map[string]bool{
		// The two steps run directly, by name.
		"doctor": true, "ci": true, "golden refresh": true,
	}
	for _, p := range phases {
		produced[p.step] = true
	}
	for _, b := range budgets {
		if !produced[b.step] {
			t.Errorf("budget %q is for a step nothing produces, so it can never fire", b.step)
		}
	}
}

// And every budget says why it is the number it is.
func TestBudgets_EveryOneCarriesItsReason(t *testing.T) {
	for _, b := range budgets {
		if len(b.why) < 40 {
			t.Errorf("budget %q has no reason worth reading: %q", b.step, b.why)
		}
		if b.max <= 0 {
			t.Errorf("budget %q has no limit", b.step)
		}
	}
}

// A phase is bounded by the last closing event, not the first.
//
// Two services build inside one up, and six workflows run inside one test. The
// interval somebody waits for ends when the last of them finishes, and timing
// to the first would report a five minute step as forty seconds.
func TestSpan_EndsAtTheLastClosingEvent(t *testing.T) {
	base := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	events := []logEvent{
		{TS: base, Type: "build.started"},
		{TS: base.Add(10 * time.Second), Type: "build.started"},
		{TS: base.Add(40 * time.Second), Type: "build.finished"},
		{TS: base.Add(5 * time.Minute), Type: "build.finished"},
	}
	from, to, ok := span(events, "build.started", "build.finished")
	if !ok {
		t.Fatal("the span was not found")
	}
	if got := to.Sub(from); got != 5*time.Minute {
		t.Errorf("span is %s, want 5m", got)
	}
}

// An unfinished phase is absent rather than zero.
func TestSpan_RefusesAPhaseThatNeverClosed(t *testing.T) {
	base := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	if _, _, ok := span([]logEvent{{TS: base, Type: "env.creating"}},
		"env.creating", "env.ready"); ok {
		t.Error("a phase that never closed was reported as a measurement")
	}
}

// The log appends across runs, so a run reads only its own events.
//
// Without this, the first dogfood run on a machine is timed correctly and
// every one after it inherits the slowest run in the file's history.
func TestReadLog_SkipsWhatCameBeforeThisRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env.ndjson")
	old := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)
	now := time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC)
	write(t, path,
		logEvent{TS: old, Type: "env.creating"},
		logEvent{TS: old.Add(time.Hour), Type: "env.ready"},
		logEvent{TS: now, Type: "env.creating"},
		logEvent{TS: now.Add(30 * time.Second), Type: "env.ready"},
	)

	events, err := readLog(path, now.Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("read %d events from this run, want 2", len(events))
	}
	from, to, ok := span(events, "env.creating", "env.ready")
	if !ok {
		t.Fatal("no span")
	}
	if got := to.Sub(from); got != 30*time.Second {
		t.Errorf("up took %s, want 30s: the previous run's events were counted", got)
	}
}

// A line that is not JSON does not end the read.
//
// A crashed process can leave a partial line, and one truncated write must not
// discard every event after it. This is the same rule the masking rules follow
// about one bad row.
func TestReadLog_ToleratesAPartialLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "env.ndjson")
	now := time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC)
	write(t, path, logEvent{TS: now, Type: "env.creating"})
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{\"ts\":\"2026-08-29T09:00:0\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	write(t, path, logEvent{TS: now.Add(time.Second), Type: "env.ready"})

	events, err := readLog(path, now.Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("read %d events, want 2: a partial line ended the read", len(events))
	}
}

// A run that leaked is not green, whatever else it did.
func TestRun_ALeakIsNotGreen(t *testing.T) {
	run := &Run{Green: true, Events: map[string]int{"resource.leaked": 2}}
	// The same shape the runner applies.
	if n := run.Events["resource.leaked"]; n > 0 {
		run.Green = false
	}
	if run.Green {
		t.Error("a run that leaked resources was reported green")
	}
}

// The record round trips, because a streak is a claim about these files.
func TestRecord_RoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "record.json")
	want := &Run{
		StartedAt: time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC),
		Mode:      "pr", Green: true, Seconds: 421,
		Steps:  []Step{{Name: "up", Seconds: 92, Budget: 600}},
		Events: map[string]int{"env.ready": 1},
	}
	if err := writeRecord(path, want); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got Run
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Mode != want.Mode || !got.Green || len(got.Steps) != 1 || got.Steps[0].Name != "up" {
		t.Errorf("record did not round trip: %+v", got)
	}
}

func TestDuration_ReadsAsSomebodyWouldSayIt(t *testing.T) {
	for in, want := range map[float64]string{
		0: "0s", 42: "42s", 60: "1m00s", 421: "7m01s",
	} {
		if got := duration(in); got != want {
			t.Errorf("duration(%v) = %q, want %q", in, got, want)
		}
	}
}

func write(t *testing.T, path string, events ...logEvent) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	enc := json.NewEncoder(f)
	for _, e := range events {
		if err := enc.Encode(e); err != nil {
			t.Fatal(err)
		}
	}
}

// The leak check asks about this run's environment, not about the label.
//
// The regression, seen on the first real run of this tool: it reported twelve
// leaked resources that belonged to a test suite running in another terminal.
// A check that blames a run for somebody else's containers is a check people
// learn to ignore, and an ignored leak check is worse than none, because the
// leak is the thing this product sells against.
func TestLeaks_SaysNothingWithoutAnEnvironment(t *testing.T) {
	r := &runner{root: t.TempDir()}
	if got := r.leaks(""); got != nil {
		t.Errorf("a run that produced no environment reported %v as leaked", got)
	}
}
